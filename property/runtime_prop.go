// property/runtime_prop.go
package property

import (
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
	"unsafe"
	//"golang.org/x/text/cases"
)

var useRuntimeLog = false

func debugRuntimeLog(msg string, args ...any) {
	if useRuntimeLog {
		slog.Debug(msg, args...)
	}
}

// formulaCalculatorPtr 公式计算器指针包装
type formulaCalculatorPtr struct {
	deps []*RuntimeProp    // 依赖属性列表
	calc FormulaCalculator // 公式计算器
}

// modifierListPtr 修改器列表指针包装
type modifierListPtr struct {
	ml *modifierList // 修改器列表
}

// 全局RuntimeProp池
var runtimePropPool = sync.Pool{
	New: func() interface{} {
		return &RuntimeProp{
			// 注意：affects和extPtr会在使用时分配
		}
	},
}

// GetRuntimeProp 从池中获取RuntimeProp
func GetRuntimeProp(def *PropDefConfig, index int, templateDefault float64) *RuntimeProp {
	prop := runtimePropPool.Get().(*RuntimeProp)

	// 重置字段
	prop.def = def
	prop.index = int32(index)
	prop.dirty = false
	prop.propType = def.Type
	prop.valueType = def.ValueType
	prop.defaultVal = encodeDefaultValue(templateDefault, def.ValueType)

	// 根据类型初始化 raw
	switch def.Type {
	case PropTypeImmediate:
		// 立即型：raw 初始化为默认值
		prop.raw = prop.defaultVal
	case PropTypeStandard:
		// 标准型：raw 也初始化为默认值
		prop.raw = prop.defaultVal
		prop.initModifiers()
	case PropTypeDerived:
		// 推导型：raw 初始为0，需要计算
		prop.raw = 0
		if def.FormulaName != "" {
			if calculator, ok := GetFormula(def.FormulaName); ok {
				ptr := &formulaCalculatorPtr{
					calc: calculator,
					deps: nil, // 依赖会在 buildDependencyGraph 中设置
				}
				prop.extPtr = unsafe.Pointer(ptr)
				debugRuntimeLog("公式设置成功",
					"prop_id", def.ID,
					"formula_name", def.FormulaName)
			} else {
				slog.Error("公式设置失败",
					"prop_id", def.ID,
					"formula_name", def.FormulaName)
			}
		}
	}

	// 清空指针字段
	prop.affects = nil
	// extPtr 已经在 switch 中处理

	return prop
}

// PutRuntimeProp 将RuntimeProp放回池中
func PutRuntimeProp(prop *RuntimeProp) {
	// 清理affects
	if prop.affects != nil {
		// 只清空指针，让GC处理底层数组
		prop.affects = nil
	}

	// 清理extPtr
	if prop.extPtr != nil {
		switch prop.propType {
		case PropTypeStandard:
			if ptr := prop.getModifierListPtr(); ptr != nil && ptr.ml != nil {
				ptr.ml.Reset()
			}
		case PropTypeDerived:
			// formulaCalculatorPtr 让GC处理
			// 注意：deps切片是引用，不清空
		}
		prop.extPtr = nil
	}

	// 重置指针引用
	prop.def = nil

	runtimePropPool.Put(prop)
}

// RuntimeProp 运行时属性
type RuntimeProp struct {
	// === 热路径字段（16字节） ===
	raw        uint32    // 4字节：当前值（编码后）
	defaultVal uint32    // 4字节：默认值（编码后）
	index      int32     // 4字节：本地索引
	dirty      bool      // 1字节：脏标记
	propType   PropType  // 1字节：属性类型
	valueType  ValueType // 1字节：值类型
	_          byte      // 1字节填充（到16字节边界）

	// === 中频字段（16字节） ===
	def     *PropDefConfig // 8字节：定义引用
	affects unsafe.Pointer // 8字节：affects切片指针

	// === 低频字段（16字节） ===
	extPtr unsafe.Pointer // 8字节：扩展指针
	//_      [8]byte        // 8字节预留/填充
}

// 总计：16 + 16 + 16 = 48字节
// 内存布局：16字节对齐，缓存友好

// encodeDefaultValue 编码默认值
func encodeDefaultValue(value float64, valueType ValueType) uint32 {
	switch valueType {
	case ValueTypeFloat32:
		return math.Float32bits(float32(value))
	case ValueTypeInt32:
		return uint32(int32(value))
	case ValueTypeBool:
		if value != 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// GetDefaultFloat 获取默认值（float32）
func (p *RuntimeProp) GetDefaultFloat() float32 {
	switch p.valueType {
	case ValueTypeFloat32:
		return math.Float32frombits(p.defaultVal)
	case ValueTypeInt32:
		return float32(int32(p.defaultVal))
	case ValueTypeBool:
		if p.defaultVal != 0 {
			return 1.0
		}
		return 0.0
	default:
		return 0.0
	}
}

// ID 获取属性ID
func (p *RuntimeProp) ID() int {
	if p.def != nil {
		return p.def.ID
	}
	return 0
}

// Type 获取属性类型
func (p *RuntimeProp) Type() PropType {
	return p.propType
}

// Type 获取属性类型
func (p *RuntimeProp) Vtype() ValueType {
	return p.valueType
}

// IsDerived 是否推导属性
func (p *RuntimeProp) IsDerived() bool {
	return p.propType == PropTypeDerived
}

// GetIndex 获取索引
func (p *RuntimeProp) GetIndex() int {
	return int(p.index)
}

// 获取/设置affects切片
func (p *RuntimeProp) getAffects() []*RuntimeProp {
	if p.affects == nil {
		return nil
	}
	return *(*[]*RuntimeProp)(p.affects)
}

func (p *RuntimeProp) setAffects(affects []*RuntimeProp) {
	if len(affects) == 0 {
		p.affects = nil
	} else {
		p.affects = unsafe.Pointer(&affects)
	}
}

// 标准型属性方法
func (p *RuntimeProp) getModifierListPtr() *modifierListPtr {
	if p.propType != PropTypeStandard || p.extPtr == nil {
		return nil
	}
	return (*modifierListPtr)(p.extPtr)
}

func (p *RuntimeProp) getModifiers() *modifierList {
	ptr := p.getModifierListPtr()
	if ptr == nil {
		return nil
	}
	return ptr.ml
}

func (p *RuntimeProp) initModifiers() {
	if p.propType == PropTypeStandard && p.extPtr == nil {
		ptr := &modifierListPtr{ml: newModifierList(p.valueType)}
		p.extPtr = unsafe.Pointer(ptr)
	}
}

// 推导型属性方法
func (p *RuntimeProp) getCalculatorPtr() *formulaCalculatorPtr {
	if p.propType != PropTypeDerived || p.extPtr == nil {
		return nil
	}
	return (*formulaCalculatorPtr)(p.extPtr)
}

func (p *RuntimeProp) getCalculator() FormulaCalculator {
	ptr := p.getCalculatorPtr()
	if ptr == nil {
		return nil
	}
	return ptr.calc
}

func (p *RuntimeProp) getDeps() []*RuntimeProp {
	ptr := p.getCalculatorPtr()
	if ptr == nil {
		return nil
	}
	return ptr.deps
}

func (p *RuntimeProp) setDeps(deps []*RuntimeProp) {
	if p.propType != PropTypeDerived {
		return
	}

	ptr := p.getCalculatorPtr()
	if ptr == nil {
		// 需要先创建calculatorPtr
		return
	}
	ptr.deps = deps
}

// 脏标记相关方法
func (p *RuntimeProp) IsDirty() bool {
	return p.dirty
}

func (p *RuntimeProp) SetDirty(dirty bool) {
	p.dirty = dirty
}

// 传播脏标记
func (p *RuntimeProp) PropagateDirty() int32 {
	if p.affects == nil {
		return 0
	}

	affects := p.getAffects()
	var count = int32(0)
	for _, affected := range affects {
		if affected != nil {
			debugRuntimeLog("属性传播",
				"from_prop_id", p.ID(),
				"to_prop_id", affected.ID())
			affected.SetDirty(true)
			count++
		}
	}
	return count
}

// 值获取/设置方法
func (p *RuntimeProp) GetFloat() float32 {
	return p.valueType.ToFloat32Value(p.raw)
}

func (p *RuntimeProp) GetFloat64() float64 {
	return p.valueType.ToFloat64Value(p.raw)
}

func (p *RuntimeProp) GetInt() int32 {
	return p.valueType.ToInt32Value(p.raw)
}

func (p *RuntimeProp) GetBool() bool {
	return p.valueType.ToBoolValue(p.raw)
	// return p.raw != 0
}

func (p *RuntimeProp) SetFloat(value float32) {
	oldValue := p.GetFloat()
	if oldValue == value {
		return
	}

	switch p.valueType {
	case ValueTypeFloat32:
		p.raw = math.Float32bits(value)
	case ValueTypeInt32:
		p.raw = uint32(int32(value))
	case ValueTypeBool:
		if value != 0 {
			p.raw = 1
		} else {
			p.raw = 0
		}
	}

	p.SetDirty(true)
}

func (p *RuntimeProp) SetInt(value int32) {
	oldInt := p.GetInt()
	if oldInt == value {
		return
	}

	switch p.valueType {
	case ValueTypeFloat32:
		p.raw = math.Float32bits(float32(value))
	case ValueTypeInt32:
		p.raw = uint32(value)
	case ValueTypeBool:
		if value != 0 {
			p.raw = 1
		} else {
			p.raw = 0
		}
	}

	p.SetDirty(true)
}

func (p *RuntimeProp) SetBool(value bool) {
	oldBool := p.GetBool()
	if oldBool == value {
		return
	}

	if value {
		p.raw = 1
	} else {
		p.raw = 0
	}

	p.SetDirty(true)
}

func (p *RuntimeProp) GetRaw() uint32 {
	return p.raw
}

func (p *RuntimeProp) SetRaw(value uint32) {
	old := p.raw
	p.raw = value
	p.SetDirty(true)
	if old != value {
		debugRuntimeLog("SetRaw",
			"prop_id", p.ID(),
			"old_raw", old,
			"new_raw", value,
			"old_decoded", DecodeRawToFloat32(old, p.valueType),
			"new_decoded", DecodeRawToFloat32(value, p.valueType))
	}
}

func (prop *RuntimeProp) findAndUpdateInList(modifiers []*TimedModifier, sourceType SourceType, sourceID int32, newRaw uint32) (bool, bool) {
	for _, mod := range modifiers {
		if mod == nil {
			continue
		}

		// 检查sourceType和sourceID是否匹配
		if mod.SourceType == sourceType && mod.SourceID == sourceID {
			// 直接比较uint32原始值
			if mod.Value == newRaw {
				// 值相同，不需要更新
				debugManagerLog("RuntimeProp.findAndUpdateInList: 修改器值相同，无需更新",
					"prop_id", prop.ID(),
					"source_type", sourceType,
					"source_id", sourceID,
					"raw_value", newRaw)
				return true, false
			}

			// 值不同，记录旧值并更新
			oldRaw := mod.Value
			mod.Value = newRaw

			// 标记属性为脏
			prop.SetDirty(true)

			// 记录更新日志
			debugManagerLog("RuntimeProp.findAndUpdateInList: 更新修改器值",
				"prop_id", prop.ID(),
				"source_type", sourceType,
				"source_id", sourceID,
				"old_raw", oldRaw,
				"new_raw", newRaw)

			return true, true
		}
	}

	return false, false
}

func (prop *RuntimeProp) FindAndUpdateModifier(sourceType SourceType, sourceID int32, newRaw uint32, opType OpType) (bool, bool) {
	// 获取修改器列表
	ml := prop.getModifiers()
	if ml == nil {
		return false, false
	}

	// 根据opType确定要查找的列表
	switch opType {
	case OpTypeFlat:
		return prop.findAndUpdateInList(ml.flats, sourceType, sourceID, newRaw)
	case OpTypePercentAdd:
		return prop.findAndUpdateInList(ml.adds, sourceType, sourceID, newRaw)
	case OpTypePercentMult:
		return prop.findAndUpdateInList(ml.mults, sourceType, sourceID, newRaw)
	default:
		return false, false
	}
}

// encodeValue 编码值
func (p *RuntimeProp) encodeValue(value float32) uint32 {
	switch p.valueType {
	case ValueTypeFloat32:
		return math.Float32bits(value)
	case ValueTypeInt32:
		return uint32(int32(value))
	case ValueTypeBool:
		if value != 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// modifierList 修改器列表
type modifierList struct {
	flats []*TimedModifier
	adds  []*TimedModifier
	mults []*TimedModifier

	// 标记是否需要清理
	vtype        ValueType
	needsCleanup bool
	lastCleanup  time.Time
}

// newModifierList 创建修改器列表
func newModifierList(vtype ValueType) *modifierList {
	return &modifierList{
		flats: make([]*TimedModifier, 0),
		adds:  make([]*TimedModifier, 0),
		mults: make([]*TimedModifier, 0),
		vtype: vtype,
	}
}

// Reset 重置修改器列表
func (ml *modifierList) Reset() {
	// 归还所有修改器到池
	for _, mod := range ml.flats {
		if mod != nil {
			PutModifier(mod)
		}
	}
	for _, mod := range ml.adds {
		if mod != nil {
			PutModifier(mod)
		}
	}
	for _, mod := range ml.mults {
		if mod != nil {
			PutModifier(mod)
		}
	}

	ml.flats = nil
	ml.adds = nil
	ml.mults = nil
	ml.vtype = ValueTypeUnknown
	ml.needsCleanup = false
	ml.lastCleanup = time.Time{}
}

// AddModifier 添加修改器
func (ml *modifierList) AddModifier(mod *TimedModifier) {
	if mod == nil {
		return
	}

	switch mod.OpType {
	case OpTypeFlat:
		ml.flats = append(ml.flats, mod)
	case OpTypePercentAdd:
		ml.adds = append(ml.adds, mod)
	case OpTypePercentMult:
		ml.mults = append(ml.mults, mod)
	}

	// 标记需要清理
	if mod.Duration > 0 {
		ml.needsCleanup = true
	}
}

// RemoveModifier 移除指定来源的修改器
func (ml *modifierList) RemoveModifier(sourceType SourceType, sourceID int32) int {
	removed := 0

	// 移除flats
	newFlats := make([]*TimedModifier, 0, len(ml.flats))
	for _, mod := range ml.flats {
		if mod == nil {
			continue
		}
		if mod.SourceType == sourceType && mod.SourceID == sourceID {
			slog.Debug("RemoveModifier 确实存在交还PutModifier", "source_id:", sourceID)
			PutModifier(mod)
			removed++
		} else {
			newFlats = append(newFlats, mod)
		}
	}
	ml.flats = newFlats

	// 移除adds
	newAdds := make([]*TimedModifier, 0, len(ml.adds))
	for _, mod := range ml.adds {
		if mod == nil {
			continue
		}
		if mod.SourceType == sourceType && mod.SourceID == sourceID {
			slog.Debug("RemoveModifier 确实存在交还PutModifier", "source_id:", sourceID)
			PutModifier(mod)
			removed++
		} else {
			newAdds = append(newAdds, mod)
		}
	}
	ml.adds = newAdds

	// 移除mults
	newMults := make([]*TimedModifier, 0, len(ml.mults))
	for _, mod := range ml.mults {
		if mod == nil {
			continue
		}
		if mod.SourceType == sourceType && mod.SourceID == sourceID {
			slog.Debug("RemoveModifier 确实存在交还PutModifier", "source_id:", sourceID)
			PutModifier(mod)
			removed++
		} else {
			newMults = append(newMults, mod)
		}
	}
	ml.mults = newMults

	return removed
}

// RemoveExpiredModifiers 移除过期修改器
func (ml *modifierList) RemoveExpiredModifiers(currentTime time.Time) int {
	if !ml.needsCleanup {
		return 0
	}

	// 定期清理，避免每次调用都清理
	if currentTime.Sub(ml.lastCleanup) < time.Second {
		return 0
	}

	removed := 0

	// 清理flats
	newFlats := make([]*TimedModifier, 0, len(ml.flats))
	for _, mod := range ml.flats {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newFlats = append(newFlats, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.flats = newFlats

	// 清理adds
	newAdds := make([]*TimedModifier, 0, len(ml.adds))
	for _, mod := range ml.adds {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newAdds = append(newAdds, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.adds = newAdds

	// 清理mults
	newMults := make([]*TimedModifier, 0, len(ml.mults))
	for _, mod := range ml.mults {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newMults = append(newMults, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.mults = newMults

	// 更新清理时间和标记
	ml.lastCleanup = currentTime
	ml.needsCleanup = len(ml.flats) > 0 || len(ml.adds) > 0 || len(ml.mults) > 0

	return removed
}

func (ml *modifierList) forceRemoveExpired(currentTime time.Time) int {
	if ml == nil {
		return 0
	}

	removed := 0

	// 清理flats
	newFlats := make([]*TimedModifier, 0, len(ml.flats))
	for _, mod := range ml.flats {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newFlats = append(newFlats, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.flats = newFlats

	// 清理adds
	newAdds := make([]*TimedModifier, 0, len(ml.adds))
	for _, mod := range ml.adds {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newAdds = append(newAdds, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.adds = newAdds

	// 清理mults
	newMults := make([]*TimedModifier, 0, len(ml.mults))
	for _, mod := range ml.mults {
		if mod == nil {
			removed++
			continue
		}
		if !mod.IsExpired(currentTime) {
			newMults = append(newMults, mod)
		} else {
			PutModifier(mod)
			removed++
		}
	}
	ml.mults = newMults

	// 更新标记
	ml.needsCleanup = len(ml.flats) > 0 || len(ml.adds) > 0 || len(ml.mults) > 0
	ml.lastCleanup = currentTime

	return removed
}

// CalculateFlatSum 计算Flat总和
func (ml *modifierList) CalculateFlatSum(currentTime time.Time) float32 {
	// ml.RemoveExpiredModifiers(currentTime)
	sum := float32(0)
	for _, mod := range ml.flats {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			sum += float32FromBits(mod.Value)
		}
	}
	return sum
}

// CalculateAddSum 计算Add总和
func (ml *modifierList) CalculateAddSum(currentTime time.Time) float32 {
	// ml.RemoveExpiredModifiers(currentTime)

	sum := float32(0)
	for _, mod := range ml.adds {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			sum += mod.vtype.ToFloat32Value(mod.Value)
		}
	}
	return sum
}

// CalculateMultProduct 计算Mult乘积
func (ml *modifierList) CalculateMultProduct(currentTime time.Time) float32 {
	// ml.RemoveExpiredModifiers(currentTime)

	product := float32(1)
	for _, mod := range ml.mults {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			// 假设Value是百分比，如0.1表示+10%
			product *= (1.0 + mod.vtype.ToFloat32Value(mod.Value))
		}
	}
	return product
}

// CalculateInt32Sum 计算Flat总和
func (ml *modifierList) CalculateInt32Sum(currentTime time.Time) int32 {
	sum := int32(0)
	for _, mod := range ml.flats {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			sum += mod.vtype.ToInt32Value(mod.Value)
		}
	}
	return sum
}

// CalculateAddInt32Sum 计算Add总和
func (ml *modifierList) CalculateAddInt32Sum(currentTime time.Time) int32 {
	// ml.RemoveExpiredModifiers(currentTime)

	sum := int32(0)
	for _, mod := range ml.adds {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			sum += mod.vtype.ToInt32Value(mod.Value)
		}
	}
	return sum
}

// CalculateMultProductInt32 计算Mult乘积
func (ml *modifierList) CalculateMultProductInt32(currentTime time.Time) int32 {
	// ml.RemoveExpiredModifiers(currentTime)

	product := int32(100000)
	for _, mod := range ml.mults {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			// 假设Value是百分比，如0.1表示+10%
			product *= (100000 + mod.vtype.ToInt32Value(mod.Value))
		}
	}
	return product
}

// GetModifierCount 获取修改器数量
func (ml *modifierList) GetModifierCount() (flat, add, mult int) {
	flat = 0
	for _, mod := range ml.flats {
		if mod != nil {
			flat++
		}
	}
	add = 0
	for _, mod := range ml.adds {
		if mod != nil {
			add++
		}
	}
	mult = 0
	for _, mod := range ml.mults {
		if mod != nil {
			mult++
		}
	}
	return
}

// GetActiveModifierCount 获取活跃修改器数量
func (ml *modifierList) GetActiveModifierCount(currentTime time.Time) (flat, add, mult int) {
	for _, mod := range ml.flats {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			flat++
		}
	}
	for _, mod := range ml.adds {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			add++
		}
	}
	for _, mod := range ml.mults {
		if mod == nil {
			continue
		}
		state := mod.GetState(currentTime)
		if state == ModifierStateActive || state == ModifierStatePermanent || state == ModifierStateInstant {
			mult++
		}
	}
	return
}

// SortByExpireTime 按过期时间排序
func (ml *modifierList) SortByExpireTime(currentTime time.Time) {
	// 过滤掉nil指针
	validFlats := make([]*TimedModifier, 0, len(ml.flats))
	for _, mod := range ml.flats {
		if mod != nil {
			validFlats = append(validFlats, mod)
		}
	}

	sort.Slice(validFlats, func(i, j int) bool {
		remainI := validFlats[i].GetRemainingTime(currentTime)
		remainJ := validFlats[j].GetRemainingTime(currentTime)
		return remainI < remainJ
	})
	ml.flats = validFlats

	validAdds := make([]*TimedModifier, 0, len(ml.adds))
	for _, mod := range ml.adds {
		if mod != nil {
			validAdds = append(validAdds, mod)
		}
	}

	sort.Slice(validAdds, func(i, j int) bool {
		remainI := validAdds[i].GetRemainingTime(currentTime)
		remainJ := validAdds[j].GetRemainingTime(currentTime)
		return remainI < remainJ
	})
	ml.adds = validAdds

	validMults := make([]*TimedModifier, 0, len(ml.mults))
	for _, mod := range ml.mults {
		if mod != nil {
			validMults = append(validMults, mod)
		}
	}

	sort.Slice(validMults, func(i, j int) bool {
		remainI := validMults[i].GetRemainingTime(currentTime)
		remainJ := validMults[j].GetRemainingTime(currentTime)
		return remainI < remainJ
	})
	ml.mults = validMults
}

// ApplyModifier 应用修改器
func (p *RuntimeProp) ApplyModifier(mod *TimedModifier) {
	if p.propType != PropTypeStandard {
		return
	}

	ml := p.getModifiers()
	if ml == nil {
		p.initModifiers()
		ml = p.getModifiers()
	}

	if ml != nil {
		ml.AddModifier(mod)
		// 设置脏标记
		p.SetDirty(true)
	}
}

// RemoveModifiersBySource 移除指定来源的修改器
func (p *RuntimeProp) RemoveModifiersBySource(sourceType SourceType, sourceID int32) int {
	if p.propType != PropTypeStandard {
		return 0
	}

	ml := p.getModifiers()
	if ml == nil {
		return 0
	}

	removed := ml.RemoveModifier(sourceType, sourceID)
	if removed > 0 {
		p.SetDirty(true)
	}
	return removed
}

// RemoveExpiredModifiers 移除过期修改器
func (p *RuntimeProp) RemoveExpiredModifiers(currentTime time.Time) int {
	if p.propType != PropTypeStandard {
		return 0
	}

	ml := p.getModifiers()
	if ml == nil {
		return 0
	}

	removed := ml.RemoveExpiredModifiers(currentTime)
	if removed > 0 {
		p.SetDirty(true)
	}
	return removed
}

// CalculateStandard 计算标准型属性
func (p *RuntimeProp) CalculateStandard(currentTime time.Time) uint32 {
	if p.propType != PropTypeStandard {
		return p.defaultVal // 返回默认值
	}

	ml := p.getModifiers()
	if ml == nil {
		return p.defaultVal // 无修改器，返回默认值
	}

	// 强制清理过期修改器
	ml.forceRemoveExpired(currentTime)

	// 检查是否还有修改器
	if len(ml.flats) == 0 && len(ml.adds) == 0 && len(ml.mults) == 0 {
		return p.defaultVal // 无活跃修改器，返回默认值
	}
	//ml.RemoveExpiredModifiers(currentTime)	已经强制执行到期了。
	switch p.valueType {
	case ValueTypeFloat32: //浮点
		// 计算修改器效果
		flatSum := ml.CalculateFlatSum(currentTime)
		addSum := ml.CalculateAddSum(currentTime)
		multProduct := ml.CalculateMultProduct(currentTime)
		return p.valueType.FromFloat32ToRaw((flatSum) * (1.0 + addSum) * multProduct)

	case ValueTypeInt32: //整形
		// 计算修改器效果
		flatInt32Sum := ml.CalculateInt32Sum(currentTime)
		addInt32Sum := ml.CalculateAddInt32Sum(currentTime)
		multProductInt32 := ml.CalculateMultProductInt32(currentTime)
		result := int32(int64(flatInt32Sum) * int64(10000+addInt32Sum) / 10000 * int64(multProductInt32) / 10000)
		return p.valueType.FromInt32ToRaw(result)
	case ValueTypeBool:
		return 0
	default:
		return 0
	}
}

// CalculateDerived 计算推导型属性
func (p *RuntimeProp) CalculateDerived() uint32 {
	if p.propType != PropTypeDerived {
		return 0
	}

	ptr := p.getCalculatorPtr()
	if ptr == nil {
		return 0
	}

	deps := ptr.deps
	if deps == nil {
		return 0
	}

	return ptr.calc.Calculate(p.valueType, deps)
}

// Calculate 计算属性值
func (p *RuntimeProp) Calculate(currentTime time.Time) {
	if !p.dirty {
		return
	}

	switch p.propType {
	case PropTypeImmediate:
		// 立即型属性不计算
		p.dirty = false

	case PropTypeStandard:
		oldRaw := p.GetRaw()
		newRaw := p.CalculateStandard(currentTime)

		if oldRaw != newRaw {
			p.SetRaw(newRaw)
		}
		p.dirty = false

	case PropTypeDerived:
		// 先确保所有依赖都是干净的
		debugRuntimeLog("推导计算: 开始计算",
			"prop_id", p.ID(),
			"current_raw", p.raw)
		ptr := p.getCalculatorPtr()
		if ptr != nil && ptr.deps != nil {
			for _, dep := range ptr.deps {
				if dep.IsDirty() {
					dep.Calculate(currentTime)
				}
			}
		}

		oldRaw := p.GetRaw()
		newRaw := p.CalculateDerived()

		if oldRaw != newRaw {
			p.SetRaw(newRaw)
			debugRuntimeLog("推导计算: 值变化",
				"prop_id", p.ID(),
				"old_raw", oldRaw,
				"new_raw", newRaw)
		}
		p.dirty = false
		debugRuntimeLog("推导计算: 标记为干净",
			"prop_id", p.ID())
	}
}

// float32转换辅助函数
func float32ToBits(f float32) uint32 {
	return math.Float32bits(f)
}

func float32FromBits(b uint32) float32 {
	return math.Float32frombits(b)
}
