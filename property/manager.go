// property/manager.go
package property

import (
	"fmt"
	"log/slog"
	"math"

	//"game_property/property"
	"sync"
	"sync/atomic"
	"time"
)

var useManagerLog = false

func debugManagerLog(msg string, args ...any) {
	if useManagerLog {
		slog.Debug(msg, args...)
	}
}

// DecodeRawToFloat32 解码uint32为float32
func DecodeRawToFloat32(raw uint32, valueType ValueType) float32 {
	switch valueType {
	case ValueTypeFloat32:
		return math.Float32frombits(raw)
	case ValueTypeInt32:
		return float32(int32(raw))
	case ValueTypeBool:
		if raw != 0 {
			return 1.0
		}
		return 0.0
	default:
		return 0.0
	}
}

const beginSourceID = 1000000000

// boolToInt32 布尔值转int32
func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// boolToFloat32 布尔值转float32
func boolToFloat32(b bool) float32 {
	if b {
		return 1.0
	}
	return 0.0
}

// BatchModifierItem 批量修改器项
type BatchModifierItem struct {
	PropID int    // 属性ID
	Value  any    // 值（可以是float32, int32, bool）
	OpType OpType // 操作类型（Flat, PercentAdd, PercentMult）
}

// PropertyManager 属性管理器
type PropertyManager struct {
	objectID        int64
	template        *PropTemplate
	props           []*RuntimeProp
	defTable        *PropDefTable
	eventMgr        *EventManager
	mu              sync.Mutex
	sourceIDCounter int32
	// 统计信息
	statPropagateCount int64
	statMarkDirtyCount int64
	statCalcPropCount  int64
	statEventFireCount int64
	statEventSkipCount int64

	// 标记是否已销毁
	destroyed bool
}

// 回调数据结构
type expiryCallbackData struct {
	callback ExpiryCallback
	objID    int64
	propID   int
	srcType  SourceType
}

// safeAsyncCallback 安全的异步回调执行
func (mgr *PropertyManager) safeAsyncCallback(callback func(), name string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("callback panic", "name", name, "panic", r)
			}
		}()
		callback()
	}()
}

// GenerateSourceID 生成对象内唯一的sourceID
// 注意：调用此函数时必须在写锁保护下
func (mgr *PropertyManager) GenerateSourceID() int32 {
	// 简单的递增操作，无需原子操作
	// 因为调用者已持有写锁
	if mgr.sourceIDCounter == 0 {
		mgr.sourceIDCounter = beginSourceID
	}
	mgr.sourceIDCounter++
	return mgr.sourceIDCounter
}

// newRuntimeProp 创建运行时属性（从池中获取）
func newRuntimeProp(def *PropDefConfig, index int, templateDefaults map[int]float64) *RuntimeProp {
	// 获取模板默认值（如果有）
	var templateDefault float64
	if templateDefaults != nil {
		if value, ok := templateDefaults[def.ID]; ok {
			templateDefault = value // 模板覆盖
		} else {
			templateDefault = def.DefaultValue // 全局默认
		}
	} else {
		templateDefault = def.DefaultValue // 没有模板，用全局默认
	}

	prop := GetRuntimeProp(def, index, templateDefault)

	// 修改日志：区分推导属性和其他属性
	if def.Type == PropTypeDerived {
		debugManagerLog("newRuntimeProp: 推导属性",
			"prop_id", def.ID,
			"name", def.Name,
			"type", def.Type)
	} else {
		debugManagerLog("newRuntimeProp: 属性",
			"prop_id", def.ID,
			"name", def.Name,
			"type", def.Type,
			"default_value", templateDefault)
	}

	// 推导属性默认标记为脏
	if def.Type == PropTypeDerived {
		prop.SetDirty(true)
		// if def.ID == PROP_ATK {
		// 	debugManagerLog("newRuntimeProp: 标记攻击力为脏")
		// }
	}

	return prop
}

// NewPropertyManager 创建新的属性管理器
func NewPropertyManager(defTable *PropDefTable, template *PropTemplate, objectID int64) *PropertyManager {
	// 使用模板的扩展属性列表
	propIDs := template.expandedPropIDs
	n := len(propIDs)

	mgr := &PropertyManager{
		defTable:        defTable,
		template:        template,
		objectID:        objectID,
		sourceIDCounter: beginSourceID,
		props:           make([]*RuntimeProp, n),
	}

	// 获取模板默认值
	templateDefaults := template.Defaults

	// 创建运行时属性（从池中获取）
	for i, propID := range propIDs {
		def, ok := defTable.GetDefByID(propID)
		if !ok {
			continue
		}

		prop := newRuntimeProp(def, i, templateDefaults)
		mgr.props[i] = prop

		// 只有推导型需要初始标记为脏
		// 标准型和立即型已经有正确的 raw 值
		if def.Type == PropTypeDerived {
			// 推导型需要初始计算
			prop.SetDirty(true)
			debugManagerLog("初始化标记脏",
				"prop_id", propID,
				"name", def.Name)
		}
		// 立即型属性不需要标记，直接使用设置的值

		// 调试：检查攻击力属性
		// if propID == PROP_ATK {
		// 	debugManagerLog("攻击力属性",
		// 		"index", i,
		// 		"type", def.Type)
		// }
	}

	// 构建依赖关系和affects表
	mgr.buildDependencyGraph()

	// 设置到期回调
	mgr.setupExpiryCallback()

	return mgr
}

// setupExpiryCallback 设置到期回调
func (mgr *PropertyManager) setupExpiryCallback() {
	expiryMgr := GetExpiryManager()
	//func(objectID int64, propID int, srcType SourceType, sourceID int32)
	expiryMgr.SetExpiryCallback(mgr.objectID, func(objID int64, propID int, srcType SourceType, sourceID int32) {
		mgr.onExpiryCallback(objID, propID, srcType, sourceID)
	})
}

// onExpiryCallback 到期回调包装
func (mgr *PropertyManager) onExpiryCallback(objID int64, propID int, srcType SourceType, sourceID int32) {
	// 异步处理，避免阻塞
	go mgr.handleExpiryAsync(objID, propID, srcType, sourceID)
}

func (mgr *PropertyManager) propagateAndStats(prop *RuntimeProp) {
	count := prop.PropagateDirty()
	atomic.AddInt64(&mgr.statPropagateCount, int64(count))
}

// // handleExpiryAsync 异步处理到期 - 基于实际接口修复版本
// func (mgr *PropertyManager) handleExpiryAsync(objID int64, propID int, srcType SourceType, sourceID int32) {
// 	debugManagerLog("到期处理: handleExpiryAsync",
// 		"object_id", objID,
// 		"prop_id", propID,
// 		"source_type", srcType,
// 		"source_id", sourceID) // 重要：确认sourceID有值

// 	// 检查状态
// 	if mgr.destroyed || objID != mgr.objectID {
// 		return
// 	}

// 	// 查找属性
// 	globalToLocal := mgr.template.globalToLocal
// 	propIdx, ok := globalToLocal[propID]
// 	if !ok {
// 		slog.Warn("到期处理: 找不到属性的本地索引",
// 			"prop_id", propID)
// 		return
// 	}

// 	mgr.mu.Lock()
// 	defer mgr.mu.Unlock()
// 	prop := mgr.props[propIdx]
// 	if prop == nil || prop.Type() != PropTypeStandard {
// 		return
// 	}

// 	// 记录到期事件
// 	debugManagerLog("到期处理",
// 		"object_id", objID,
// 		"prop_id", propID,
// 		"prop_index", propIdx,
// 		"source_type", srcType,
// 		"source_id", sourceID,
// 	)

// 	// 获取当前值
// 	currentTime := time.Now()
// 	if prop.IsDirty() {
// 		prop.Calculate(currentTime)
// 	}
// 	oldRaw := prop.GetRaw()
// 	oldValue := DecodeRawToFloat32(oldRaw, prop.valueType)

// 	// 基于实际的modifierList接口实现
// 	removed := 0
// 	if ml := prop.getModifiers(); ml != nil {
// 		// 方案1：尝试移除指定source_type的所有修改器
// 		// 由于我们不知道具体的sourceID，移除该类型的所有修改器
// 		removed = ml.RemoveModifier(srcType, sourceID) // 0表示移除该类型的所有修改器
// 		debugManagerLog("执行到期处理：移除修改器 :",
// 			"removed", removed,
// 			"object_id", objID,
// 			"prop_id", propID,
// 			"prop_index", propIdx,
// 			"source_type", srcType,
// 			"source_id", sourceID,
// 		)

// 		// if removed == 0 {
// 		//     // 方案2：如果没有找到，尝试移除过期的修改器
// 		//     removed = ml.RemoveExpiredModifiers(currentTime)
// 		//     if removed > 0 {
// 		//         debugManagerLog("到期处理: 通过过期检查移除修改器",
// 		//             "prop_id", propID,
// 		//             "removed_count", removed)
// 		//     } else {
// 		//         // 方案3：检查是否有任何修改器
// 		//         flat, add, mult := ml.GetModifierCount()
// 		//         totalModifiers := flat + add + mult
// 		//         if totalModifiers > 0 {
// 		//             // 记录详细的修改器信息
// 		//             debugManagerLog("到期处理: 有修改器但无法匹配",
// 		//                 "prop_id", propID,
// 		//                 "total_modifiers", totalModifiers,
// 		//                 "flats", flat,
// 		//                 "adds", add,
// 		//                 "mults", mult,
// 		//                 "source_type", srcType)
// 		//             // 强制移除所有过期的修改器
// 		//             removed = ml.forceRemoveExpired(currentTime)
// 		//             if removed > 0 {
// 		//                 slog.Warn("到期处理: 强制移除过期修改器",
// 		//                     "prop_id", propID,
// 		//                     "removed_count", removed)
// 		//             } else {
// 		//                 // 尝试移除该类型的所有修改器（通过不同的sourceID）
// 		//                 // 遍历可能的所有sourceID（这里可能需要根据实际情况调整）
// 		//                 for sourceID := int32(1); sourceID <= 1000; sourceID++ {
// 		//                     removed = ml.RemoveModifier(srcType, sourceID)
// 		//                     if removed > 0 {
// 		//                         debugManagerLog("到期处理: 通过特定sourceID移除修改器",
// 		//                             "prop_id", propID,
// 		//                             "source_id", sourceID,
// 		//                             "removed_count", removed)
// 		//                         break
// 		//                     }
// 		//                 }
// 		//             }
// 		//         } else {
// 		//             debugManagerLog("到期处理: 属性没有修改器",
// 		//                 "prop_id", propID)
// 		//         }
// 		//     }
// 		// } else {
// 		//     debugManagerLog("到期处理: 通过source_type移除修改器",
// 		//         "prop_id", propID,
// 		//         "source_type", srcType,
// 		//         "removed_count", removed)
// 		// }
// 	} else {
// 		debugManagerLog("到期处理: 属性没有修改器列表",
// 			"prop_id", propID)
// 	}

// 	if removed == 0 {
// 		debugManagerLog("到期处理: 没有找到过期修改器 removed == 0",
// 			"prop_id", propID)

// 		// 特殊处理：如果srcType是buff，可以尝试更积极的清理
// 		if srcType == SourceTypeBuff {
// 			slog.Warn("到期处理: buff到期但未找到修改器，尝试更彻底的清理",
// 				"prop_id", propID)

// 			// 强制重新计算属性
// 			prop.SetDirty(true)
// 			prop.Calculate(currentTime)
// 			newRaw := prop.GetRaw()

// 			if oldRaw != newRaw {
// 				debugManagerLog("到期处理: 强制重新计算后值变化",
// 					"prop_id", propID,
// 					"old_value", oldValue,
// 					"new_value", DecodeRawToFloat32(newRaw, prop.valueType))
// 			}
// 		}

// 		return
// 	}

// 	debugManagerLog("到期处理: 移除过期修改器",
// 		"prop_id", propID,
// 		"removed_count", removed)

// 	// 标记为脏并传播
// 	prop.SetDirty(true)
// 	mgr.propagateAndStats(prop)

// 	// 重新计算
// 	prop.Calculate(currentTime)
// 	newRaw := prop.GetRaw()
// 	newValue := DecodeRawToFloat32(newRaw, prop.valueType)

// 	// 记录值变化
// 	if oldRaw != newRaw {
// 		debugManagerLog("到期处理: 属性值变化",
// 			"prop_id", propID,
// 			"old_value", oldValue,
// 			"new_value", newValue,
// 			"delta", newValue-oldValue)

// 		// 触发到期事件
// 		mgr.fireEvent(prop, oldRaw, newRaw, srcType)
// 	} else {
// 		// 值没有变化，记录日志帮助调试
// 		slog.Warn("到期处理: 移除修改器后属性值未变化",
// 			"prop_id", propID,
// 			"old_value", oldValue,
// 			"new_value", newValue)
// 	}
// }

func (mgr *PropertyManager) handleExpiryAsync(objID int64, propID int, sourceType SourceType, sourceID int32) {
	// 1. 验证对象ID是否匹配
	if mgr.objectID != objID {
		debugManagerLog("handleExpiryAsync: 对象ID不匹配",
			"expected", mgr.objectID,
			"actual", objID)
		return
	}

	// 获取锁保护并发访问
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	// 2. 基础安全检查
	if mgr == nil {
		debugManagerLog("handleExpiryAsync: 管理器为nil", "object_id", mgr.objectID)
		return
	}

	if mgr.props == nil {
		debugManagerLog("handleExpiryAsync: 属性数组为nil", "object_id", mgr.objectID)
		return
	}

	if len(mgr.props) == 0 {
		debugManagerLog("handleExpiryAsync: 属性数组为空", "object_id", mgr.objectID)
		return
	}

	if mgr.template == nil {
		debugManagerLog("handleExpiryAsync: 模板为nil", "object_id", mgr.objectID)
		return
	}

	if mgr.template.globalToLocal == nil {
		debugManagerLog("handleExpiryAsync: 全局到本地映射为nil", "object_id", mgr.objectID)
		return
	}

	// 3. 查找属性索引
	idx, ok := mgr.template.globalToLocal[propID]
	if !ok {
		debugManagerLog("handleExpiryAsync: 属性不存在",
			"object_id", mgr.objectID,
			"prop_id", propID)
		return
	}

	// 4. 索引边界检查
	if idx < 0 || idx >= len(mgr.props) {
		slog.Warn("handleExpiryAsync: 索引超出范围",
			"object_id", mgr.objectID,
			"prop_id", propID,
			"index", idx,
			"array_length", len(mgr.props))
		return
	}

	// 5. 获取属性对象
	prop := mgr.props[idx]
	if prop == nil {
		slog.Warn("handleExpiryAsync: 属性对象为nil",
			"object_id", mgr.objectID,
			"prop_id", propID,
			"index", idx)
		return
	}

	// 6. 移除修改器
	removed := prop.RemoveModifiersBySource(sourceType, sourceID)

	if removed > 0 {
		// 7. 标记属性为脏
		prop.SetDirty(true)
		mgr.propagateAndStats(prop)

		debugManagerLog("执行到期处理：移除修改器",
			"removed", removed,
			"object_id", mgr.objectID,
			"prop_id", propID,
			"prop_index", idx,
			"source_type", sourceType,
			"source_id", sourceID)

		debugManagerLog("到期处理: 移除过期修改器",
			"prop_id", propID,
			"removed_count", removed)

		// 8. 获取当前时间
		currentTime := time.Now()

		// 9. 获取旧值
		var oldValue, newValue uint32
		if prop.IsDirty() {
			prop.Calculate(currentTime)
		}
		oldValue = prop.GetRaw()

		// 10. 重新计算新值
		prop.SetDirty(true)
		prop.Calculate(currentTime)
		newValue = prop.GetRaw()

		// 11. 计算变化量
		oldFloat := DecodeRawToFloat32(oldValue, prop.Vtype())
		newFloat := DecodeRawToFloat32(newValue, prop.Vtype())
		delta := newFloat - oldFloat

		debugManagerLog("到期处理: 属性值变化",
			"prop_id", propID,
			"old_value", oldFloat,
			"new_value", newFloat,
			"delta", delta)

		// 12. 触发属性变更事件
		if mgr.eventMgr != nil {
			// 使用QueueEvent而不是Trigger
			mgr.eventMgr.QueueEvent(PropChangeEvent{
				ObjectID:     mgr.objectID,
				PropID:       propID,
				OldValue:     oldValue,
				NewValue:     newValue,
				Source:       sourceType, // 这里是SourceType类型
				TypeForValue: prop.Vtype(),
			})
		}
	} else {
		debugManagerLog("handleExpiryAsync: 未找到匹配的修改器",
			"object_id", mgr.objectID,
			"prop_id", propID,
			"source_type", sourceType,
			"source_id", sourceID)
	}
}

// buildDependencyGraph 构建依赖图
func (mgr *PropertyManager) buildDependencyGraph() {
	// 1. 建立直接依赖
	globalToLocal := mgr.template.globalToLocal
	for _, prop := range mgr.props {
		if prop.IsDerived() {
			// 建立直接依赖
			deps := make([]*RuntimeProp, 0, len(prop.def.DependsOn))

			for _, depID := range prop.def.DependsOn {
				if depIdx, ok := globalToLocal[depID]; ok {
					depProp := mgr.props[depIdx]
					deps = append(deps, depProp)
				}
			}

			// 设置依赖
			if ptr := prop.getCalculatorPtr(); ptr != nil {
				ptr.deps = deps
			}
		}
	}

	// 2. 计算全影响表（affects）
	// 为每个属性计算它会影响的所有属性
	for _, prop := range mgr.props {
		// 计算这个属性会影响哪些属性
		affects := mgr.calcAllAffects(prop, make(map[int]bool))

		// 转换为指针列表
		ptrList := make([]*RuntimeProp, len(affects))
		for j, idx := range affects {
			ptrList[j] = mgr.props[idx]
		}

		// 设置affects
		prop.setAffects(ptrList)

		if len(affects) > 0 {
			debugManagerLog("属性影响关系",
				"prop_id", prop.ID(),
				"affected_count", len(affects),
				"affected_props", affects)
		}
	}
}

// calcAllAffects 计算属性会影响的所有属性（直接影响+间接影响）
func (mgr *PropertyManager) calcAllAffects(startProp *RuntimeProp, visited map[int]bool) []int {
	if visited[int(startProp.index)] {
		return nil
	}
	visited[int(startProp.index)] = true

	affects := make([]int, 0)

	// 遍历所有推导型属性，找出依赖startProp的属性
	for _, prop := range mgr.props {
		if prop.IsDerived() {
			ptr := prop.getCalculatorPtr()
			if ptr == nil || ptr.deps == nil {
				continue
			}

			for _, dep := range ptr.deps {
				if dep.GetIndex() == int(startProp.index) {
					// prop依赖startProp
					affects = append(affects, prop.GetIndex())

					// 递归计算间接影响
					indirectAffects := mgr.calcAllAffects(prop, visited)
					affects = append(affects, indirectAffects...)
					break
				}
			}
		}
	}

	// 去重
	return mgr.uniqueInts(affects)
}

// uniqueInts 去重
func (mgr *PropertyManager) uniqueInts(arr []int) []int {
	seen := make(map[int]bool)
	result := make([]int, 0, len(arr))

	for _, v := range arr {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}

	return result
}

// RemoveModifiersBySource 移除指定来源的所有修改器
func (mgr *PropertyManager) RemoveModifiersBySource(sourceType SourceType, sourceID int32) int {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	removed := 0
	for _, prop := range mgr.props {
		if prop.Type() == PropTypeStandard {
			propRemoved := prop.RemoveModifiersBySource(sourceType, sourceID)
			if propRemoved > 0 {
				removed += propRemoved
				// 设置脏标记并传播
				prop.SetDirty(true)
				mgr.propagateAndStats(prop)
			}
		}
	}

	return removed
}

// RemoveExpiredModifiers 移除所有过期修改器
func (mgr *PropertyManager) RemoveExpiredModifiers() int {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	currentTime := time.Now()
	removed := 0

	for _, prop := range mgr.props {
		if prop.Type() == PropTypeStandard {
			propRemoved := prop.RemoveExpiredModifiers(currentTime)
			if propRemoved > 0 {
				removed += propRemoved
				// 设置脏标记并传播
				prop.SetDirty(true)
				mgr.propagateAndStats(prop)
			}
		}
	}

	return removed
}

// GetModifierInfo 获取修改器信息
func (mgr *PropertyManager) GetModifierInfo(propID int) (flat, add, mult int, hasModifiers bool) {

	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[propID]
	if !ok {
		return 0, 0, 0, false
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	prop := mgr.props[idx]
	if prop.Type() != PropTypeStandard {
		return 0, 0, 0, false
	}

	ml := prop.getModifiers()
	if ml == nil {
		return 0, 0, 0, false
	}

	currentTime := time.Now()
	flat, add, mult = ml.GetActiveModifierCount(currentTime)
	return flat, add, mult, true
}

// // fireEvent 触发事件
// func (mgr *PropertyManager) fireEvent(prop *RuntimeProp, oldRaw, newRaw uint32, source SourceType) {
// 	if mgr.eventMgr == nil {
// 		return
// 	}
// 	if !mgr.eventMgr.HasAnyListener() && !mgr.eventMgr.HasListenerForProp(prop.ID()) {
// 		return
// 	}
// 	atomic.AddInt64(&mgr.statEventFireCount, 1)

// 	if oldRaw == newRaw {
// 		// 值没有变化，不触发事件
// 		atomic.AddInt64(&mgr.statEventSkipCount, 1)
// 		return
// 	}

// 	if mgr.eventMgr == nil {
// 		return
// 	}

// 	// 保持原有事件接口不变
// 	event := NewPropChangeEvent(
// 		mgr.objectID,
// 		prop.ID(),
// 		prop.valueType,
// 		oldRaw,
// 		newRaw,
// 		source,
// 	)

// 	mgr.eventMgr.QueueEvent(event)
// }

// // fireEvent 触发属性变更事件
// func (mgr *PropertyManager) fireEvent(prop *RuntimeProp, oldRaw, newRaw uint32, source SourceType) {
// 	slog.Debug("fireEvent: 开始",
// 		"object_id", mgr.objectID,
// 		"prop_id", prop.ID(),
// 		"old_raw", oldRaw,
// 		"new_raw", newRaw)

// 	if mgr.eventMgr == nil {
// 		slog.Debug("fireEvent: 事件管理器为nil，跳过")
// 		return
// 	}

// 	if !mgr.eventMgr.IsEnabled() {
// 		slog.Debug("fireEvent: 事件管理器被禁用，跳过")
// 		return
// 	}

// 	// 注意：这里需要检查PropChangeEvent结构的字段类型
// 	// 如果PropChangeEvent期望的是float64，则需要类型转换
// 	event := property.PropChangeEvent{
// 		ObjectID:  mgr.objectID,
// 		PropID:    int(prop.ID()),
// 		OldValue:  float64(oldRaw), // 如果需要转换为float64
// 		NewValue:  float64(newRaw), // 如果需要转换为float64
// 		Source:    source,
// 		Timestamp: time.Now(),
// 	}

// 	slog.Debug("fireEvent: 准备入队事件",
// 		"object_id", event.ObjectID,
// 		"prop_id", event.PropID)

// 	success := mgr.eventMgr.QueueEvent(event)

// 	if success {
// 		slog.Debug("fireEvent: 事件入队成功",
// 			"object_id", event.ObjectID,
// 			"prop_id", event.PropID)
// 	} else {
// 		slog.Debug("fireEvent: 事件入队失败",
// 			"object_id", event.ObjectID,
// 			"prop_id", event.PropID)
// 	}
// }

// // fireEvent 触发属性变更事件
// func (mgr *PropertyManager) fireEvent(prop *RuntimeProp, oldRaw, newRaw uint32, source SourceType) {
//     slog.Debug("fireEvent: 开始",
//         "object_id", mgr.objectID,
//         "prop_id", prop.ID(),
//         "old_raw", oldRaw,
//         "new_raw", newRaw)

//     if mgr.eventMgr == nil {
//         slog.Debug("fireEvent: 事件管理器为nil，跳过")
//         return
//     }

//     if !mgr.eventMgr.IsEnabled() {
//         slog.Debug("fireEvent: 事件管理器被禁用，跳过")
//         return
//     }

//     // 注意：这里需要检查PropChangeEvent结构的字段类型
//     // 如果PropChangeEvent期望的是float64，则需要类型转换
//     event := property.PropChangeEvent{
//         ObjectID:  mgr.objectID,
//         PropID:    int(prop.ID()),
//         OldValue:  float64(oldRaw),  // 如果需要转换为float64
//         NewValue:  float64(newRaw),  // 如果需要转换为float64
//         Source:    source,
//         Timestamp: time.Now(),
//     }

//     slog.Debug("fireEvent: 准备入队事件",
//         "object_id", event.ObjectID,
//         "prop_id", event.PropID)

//     success := mgr.eventMgr.QueueEvent(event)

//	    if success {
//	        slog.Debug("fireEvent: 事件入队成功",
//	            "object_id", event.ObjectID,
//	            "prop_id", event.PropID)
//	    } else {
//	        slog.Debug("fireEvent: 事件入队失败",
//	            "object_id", event.ObjectID,
//	            "prop_id", event.PropID)
//	    }
//	}
//
// fireEvent 触发属性变更事件
func (mgr *PropertyManager) fireEvent(prop *RuntimeProp, oldRaw, newRaw uint32, source SourceType) {
	slog.Debug("fireEvent: 开始",
		"object_id", mgr.objectID,
		"prop_id", prop.ID(),
		"old_raw", oldRaw,
		"new_raw", newRaw)

	if mgr.eventMgr == nil {
		slog.Debug("fireEvent: 事件管理器为nil，跳过")
		return
	}

	if !mgr.eventMgr.IsEnabled() {
		slog.Debug("fireEvent: 事件管理器被禁用，跳过")
		return
	}

	// 注意：这里需要检查PropChangeEvent结构的字段类型
	// 如果PropChangeEvent期望的是float64，则需要类型转换
	event := PropChangeEvent{
		ObjectID:  mgr.objectID,
		PropID:    int(prop.ID()),
		OldValue:  oldRaw, // 如果需要转换为float64
		NewValue:  newRaw, // 如果需要转换为float64
		Source:    source,
		Timestamp: int64(time.Now().UnixNano() / 1e6),
	}

	slog.Debug("fireEvent: 准备入队事件",
		"object_id", event.ObjectID,
		"prop_id", event.PropID)

	success := mgr.eventMgr.QueueEvent(event)

	if success {
		slog.Debug("fireEvent: 事件入队成功",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
	} else {
		slog.Debug("fireEvent: 事件入队失败",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
	}
}

// SetEventManager 设置事件管理器
func (mgr *PropertyManager) SetEventManager(eventMgr *EventManager) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.eventMgr = eventMgr
}

// GetStats 获取性能统计
func (mgr *PropertyManager) GetStats() (propagate, markDirty, calcProp, eventFire, eventSkip int64) {
	return atomic.LoadInt64(&mgr.statPropagateCount),
		atomic.LoadInt64(&mgr.statMarkDirtyCount),
		atomic.LoadInt64(&mgr.statCalcPropCount),
		atomic.LoadInt64(&mgr.statEventFireCount),
		atomic.LoadInt64(&mgr.statEventSkipCount)
}

// PrintStats 打印性能统计
func (mgr *PropertyManager) PrintStats() {
	propagate, markDirty, calcProp, eventFire, eventSkip := mgr.GetStats()

	debugManagerLog("属性管理器统计",
		"object_id", mgr.objectID,
		"传播调用次数", propagate,
		"传播影响属性数", markDirty,
		"属性计算次数", calcProp,
		"事件触发次数", eventFire,
		"事件跳过次数", eventSkip)

	totalEvents := eventFire + eventSkip
	if totalEvents > 0 {
		skipRate := float64(eventSkip) / float64(totalEvents) * 100
		debugManagerLog("事件统计",
			"事件跳过率", skipRate)
	}
}

// Destroy 销毁属性管理器
func (mgr *PropertyManager) Destroy() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if mgr.destroyed {
		return
	}

	// 从全局到期管理器移除所有记录
	expiryMgr := GetExpiryManager()
	if expiryMgr != nil {
		expiryMgr.UnregisterAllForObject(mgr.objectID)
	}

	// 移除所有修改器
	mgr.removeAllModifiers()

	// 断开外部引用
	mgr.eventMgr = nil
	mgr.defTable = nil
	mgr.template = nil

	// 归还所有RuntimeProp到池
	for _, prop := range mgr.props {
		PutRuntimeProp(prop)
	}

	// 清空切片
	mgr.props = nil

	mgr.destroyed = true

	debugManagerLog("销毁属性管理器",
		"object_id", mgr.objectID)
}

// removeAllModifiers 移除所有修改器
func (mgr *PropertyManager) removeAllModifiers() {
	for _, prop := range mgr.props {
		if prop.Type() == PropTypeStandard {
			if ml := prop.getModifiers(); ml != nil {
				ml.Reset()
			}
		}
	}
}

func (mgr *PropertyManager) GetFloatByID(propID int) (float32, bool) {
	raw, vt := mgr.GetRawProp(propID)
	if vt != ValueTypeUnknown {
		return vt.ToFloat32(raw)
	}

	return 0.0, false
}
func (mgr *PropertyManager) GetInt32ByID(propID int) (int32, bool) {
	raw, vt := mgr.GetRawProp(propID)
	if vt != ValueTypeUnknown {
		return vt.ToInt32(raw)
	}

	return 0, false
}
func (mgr *PropertyManager) GetBoolByID(propID int) (bool, bool) {
	raw, vt := mgr.GetRawProp(propID)
	if vt != ValueTypeUnknown {
		return vt.ToBool(raw)
	}

	return false, false
}

// GetPropByIdent 通过标识符获取属性值
func (mgr *PropertyManager) GetRawPropByIdent(ident string) (uint32, ValueType) {
	if mgr.defTable == nil {
		return 0, ValueTypeUnknown
	}

	// 通过标识符获取ID
	propID, ok := mgr.defTable.GetIDByIdent(ident)
	if !ok {
		return 0, ValueTypeUnknown
	}

	// 通过ID获取属性值
	return mgr.GetRawProp(propID)
}

// GetRawProp 通过ID获取属性的原始uint32值
func (mgr *PropertyManager) GetRawProp(propID int) (uint32, ValueType) {
	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[propID]
	if !ok {
		return 0, ValueTypeUnknown
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	return mgr.getRawValue(idx)
}

// getRawValue 通过索引获取属性的原始uint32值
// 注意：此函数是内部公共函数，上层调用者需要处理propID到idx的转换 并加锁
func (mgr *PropertyManager) getRawValue(idx int) (uint32, ValueType) {
	// 边界检查
	if idx < 0 || idx >= len(mgr.props) {
		return 0, ValueTypeUnknown
	}

	prop := mgr.props[idx]

	// 检查本地脏标记
	if prop.IsDirty() {
		debugManagerLog("GetRawValue: 属性是脏的，开始计算",
			"index", idx,
			"prop_id", prop.ID())
		currentTime := time.Now()
		prop.Calculate(currentTime)
		atomic.AddInt64(&mgr.statCalcPropCount, 1) // 添加统计
	} else {
		debugManagerLog("GetRawValue: 属性不是脏的，直接返回值",
			"index", idx,
			"prop_id", prop.ID())
	}

	// 返回原始uint32值
	return prop.GetRaw(), prop.valueType
}

// SetPropFloat 设置立即型属性
func (mgr *PropertyManager) SetPropFloat(propID int, value float32, sourceID int32) (bool, int32) {
	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[propID]
	if !ok {
		return false, 0
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	return mgr.setRawValue(idx, value, sourceID)

}

// SetPropInt 设置立即型属性（int32）
func (mgr *PropertyManager) SetPropInt(propID int, value int32, sourceID int32) (bool, int32) {
	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[propID]
	if !ok {
		return false, 0
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	return mgr.setRawValue(idx, value, sourceID)
}

// SetPropBool 设置立即型属性（bool）
func (mgr *PropertyManager) SetPropBool(propID int, value bool, sourceID int32) (bool, int32) {
	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[propID]
	if !ok {
		return false, 0
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.setRawValue(idx, value, sourceID)
}

// SetRawValue 通过索引设置属性的原始uint32值
// 参数: idx - 属性索引, raw - 原始uint32值
// 返回: (是否成功, 用于事件触发的sourceID)
// 注意：此函数是内部公共函数，上层调用者需要处理propID到idx的转换和加锁
func (mgr *PropertyManager) setRawValue(idx int, value any, sourceID int32) (bool, int32) {
	// 边界检查
	if idx < 0 || idx >= len(mgr.props) {
		return false, 0
	}

	prop := mgr.props[idx] //获得属性
	if prop == nil {
		return false, 0
	}

	vtype := prop.Vtype() //获取类型

	raw := vtype.FromAnyToRaw(value) //将任意值 转换为 自己的uint32值

	// 根据属性类型分别处理
	switch prop.Type() {
	case PropTypeImmediate:
		// 立即型属性可以直接设置
		return mgr.setImmediateProp(prop, idx, raw)

	case PropTypeStandard:
		// 标准型属性通过应用永久修改器来设置
		return mgr.setStandardPropByModifier(prop, idx, raw, sourceID)

	case PropTypeDerived:
		// 推导型属性不能直接设置，由公式计算
		slog.Error("尝试直接设置推导型属性",
			"index", idx,
			"prop_id", prop.ID(),
			"prop_type", "derived")
		return false, 0

	default:
		slog.Error("未知的属性类型",
			"index", idx,
			"prop_id", prop.ID(),
			"prop_type", prop.Type())
		return false, 0
	}
}

// setImmediateProp 设置立即型属性
func (mgr *PropertyManager) setImmediateProp(prop *RuntimeProp, idx int, raw uint32) (bool, int32) {
	// 获取旧值用于事件比较
	oldRaw := prop.GetRaw()

	// 值没有变化，直接返回成功
	if oldRaw == raw {
		return true, int32(SourceTypeBase)
	}

	// 根据属性值类型验证原始值
	switch prop.valueType {
	case ValueTypeFloat32:
		// 对于float32，解码检查是否是有效浮点数
		floatValue := DecodeRawToFloat32(raw, prop.valueType)
		if math.IsNaN(float64(floatValue)) || math.IsInf(float64(floatValue), 0) {
			slog.Error("设置立即型属性包含无效的浮点值",
				"index", idx,
				"prop_id", prop.ID(),
				"value", floatValue)
			return false, 0
		}
	case ValueTypeInt32:
		// 对于int32，验证值是否在合理范围内
		intValue := DecodeRawToFloat32(raw, prop.valueType)
		if intValue < math.MinInt32 || intValue > math.MaxInt32 {
			slog.Error("设置立即型属性超出int32范围",
				"index", idx,
				"prop_id", prop.ID(),
				"value", intValue)
			return false, 0
		}
	case ValueTypeBool:
		// 布尔值验证：只能是0或1
		if raw != 0 && raw != 1 {
			slog.Warn("设置立即型布尔属性使用非标准值",
				"index", idx,
				"prop_id", prop.ID(),
				"raw_value", raw)
		}
	default:
		slog.Error("未知的属性值类型",
			"index", idx,
			"prop_id", prop.ID(),
			"value_type", prop.valueType)
		return false, 0
	}

	// 设置新值
	prop.SetRaw(raw)

	// 记录调试信息
	debugManagerLog("SetRawValue: 设置立即型属性成功",
		"index", idx,
		"prop_id", prop.ID(),
		"prop_type", "immediate",
		"value_type", prop.valueType,
		"old_raw", oldRaw,
		"new_raw", raw)

	// 触发事件
	mgr.fireEvent(prop, oldRaw, raw, SourceTypeBase)

	// 传播脏标记
	prop.SetDirty(true)
	mgr.propagateAndStats(prop)

	// 返回成功和sourceID
	return true, int32(SourceTypeBase)
}

func (mgr *PropertyManager) setStandardPropByModifier(prop *RuntimeProp, idx int, raw uint32, sourceID int32) (bool, int32) {
	// 生成实际使用的sourceID
	actualSourceID := sourceID
	if actualSourceID == 0 {
		// 生成新的sourceID
		actualSourceID = mgr.GenerateSourceID()
	}
	// 创建永久修改器
	mod := NewTimedModifierType(raw, prop.valueType, OpTypeFlat, SourceTypeBase, actualSourceID, time.Duration(-1))
	mod.PropID = int32(prop.ID())

	// 应用修改器
	success := mgr.ApplyModifier(mod)
	if success {
		return true, actualSourceID
	}

	slog.Warn("创建修改器失败",
		"object_id", mgr.objectID,
		"index", idx,
		"prop_id", prop.ID(),
		"source_type", SourceTypeBase,
		"source_id", actualSourceID)
	return false, 0
}

// getPropBaseValue 获取属性的基础值（去除修改器影响）
// 注意：这是一个简化的实现，实际实现需要从modifierList中计算基础值
func (mgr *PropertyManager) getPropBaseValue(prop *RuntimeProp, idx int) float32 {
	// 获取属性的默认值
	if mgr.template != nil && mgr.template.Defaults != nil {
		if defValue, ok := mgr.template.Defaults[prop.ID()]; ok {
			return float32(defValue)
		}
	}

	// 如果没有模板默认值，尝试从defTable获取
	if mgr.defTable != nil {
		if def, ok := mgr.defTable.GetDefByID(prop.ID()); ok {
			return float32(def.DefaultValue)
		}
	}

	// 回退到0
	return 0
}

// func (mgr *PropertyManager) ApplyModifier(mod *TimedModifier) bool {
// 	globalToLocal := mgr.template.globalToLocal
// 	idx, ok := globalToLocal[int(mod.PropID)]
// 	if !ok {
// 		PutModifier(mod) // ✅ 归还mod到对象池
// 		return false
// 	}
// 	mgr.mu.Lock()
// 	defer mgr.mu.Unlock()

// 	prop := mgr.props[idx]
// 	if prop.Type() != PropTypeStandard {
// 		PutModifier(mod) // ✅ 归还mod到对象池
// 		return false
// 	}

// 	// 获取当前时间
// 	currentTime := time.Now()

// 	// 先计算当前值，确保有正确的旧值
// 	if prop.IsDirty() {
// 		prop.Calculate(currentTime)
// 	}
// 	oldValue := prop.GetRaw() // 现在得到的是正确的当前值

// 	// 检查是否已存在相同条件的修改器
// 	exismod, changemod := prop.FindAndUpdateModifier(mod.SourceType, mod.SourceID, mod.Value, mod.OpType)

// 	if exismod {
// 		// 找到相同修改器，无论是否更新值，都需要归还新创建的mod
// 		PutModifier(mod) // ✅ 归还mod到对象池
// 	} else {
// 		// 不存在相同修改器，应用新修改器
// 		prop.ApplyModifier(mod)

// 		// 注册到期管理（如果是临时修改器）
// 		if mod.IsTemporary() {
// 			expiryMgr := GetExpiryManager()
// 			expiryMgr.Register(mgr.objectID, int(mod.PropID), mod, mod.GetExpiryTime())

// 			debugManagerLog("到期管理: 注册临时修改器",
// 				"object_id", mgr.objectID,
// 				"prop_id", mod.PropID,
// 				"duration", mod.Duration)
// 		}
// 		changemod = true
// 	}

// 	if changemod {
// 		// 标记为脏并传播
// 		debugManagerLog("ApplyModifier: 属性变化，触发传播", "prop_id", prop.ID())
// 		prop.SetDirty(true)
// 		mgr.propagateAndStats(prop)

// 		// 然后计算当前属性
// 		prop.Calculate(currentTime)
// 		newValue := prop.GetRaw()

// 		sourceType := mod.SourceType
// 		mgr.fireEvent(prop, oldValue, newValue, sourceType)
// 	}

// 	return changemod //如果 返回真 表示 有改变。
// }

// ApplyModifier 应用修改器
func (mgr *PropertyManager) ApplyModifier(mod *TimedModifier) bool {
	globalToLocal := mgr.template.globalToLocal
	idx, ok := globalToLocal[int(mod.PropID)]
	if !ok {
		slog.Debug("ApplyModifier失败: 属性ID未找到",
			"prop_id", mod.PropID)
		PutModifier(mod)
		return false
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	prop := mgr.props[idx]
	if prop.Type() != PropTypeStandard {
		slog.Debug("ApplyModifier失败: 属性类型不匹配",
			"prop_id", mod.PropID,
			"type", prop.Type())
		PutModifier(mod)
		return false
	}

	// 获取当前时间
	currentTime := time.Now()

	// 先计算当前值，确保有正确的旧值
	if prop.IsDirty() {
		prop.Calculate(currentTime)
	}
	oldValue := prop.GetRaw()

	// 记录详细的修改器信息
	slog.Debug("ApplyModifier: 处理修改器开始",
		"object_id", mgr.objectID,
		"prop_id", mod.PropID,
		"source_id", mod.SourceID,
		"value", mod.Value,
		"op_type", mod.OpType,
		"old_value", oldValue)

	// 检查是否已存在相同条件的修改器
	exismod, changemod := prop.FindAndUpdateModifier(mod.SourceType, mod.SourceID, mod.Value, mod.OpType)

	slog.Debug("ApplyModifier: FindAndUpdateModifier结果",
		"object_id", mgr.objectID,
		"prop_id", mod.PropID,
		"source_id", mod.SourceID,
		"exismod", exismod,
		"changemod", changemod)

	if exismod {
		// 找到相同修改器，无论是否更新值，都需要归还新创建的mod
		slog.Debug("ApplyModifier: 存在相同修改器，归还原修改器",
			"object_id", mgr.objectID,
			"prop_id", mod.PropID,
			"source_id", mod.SourceID)
		PutModifier(mod)
	} else {
		// 不存在相同修改器，应用新修改器
		slog.Debug("ApplyModifier: 应用新修改器",
			"object_id", mgr.objectID,
			"prop_id", mod.PropID,
			"source_id", mod.SourceID)
		prop.ApplyModifier(mod)

		// 注册到期管理（如果是临时修改器）
		if mod.IsTemporary() {
			expiryMgr := GetExpiryManager()
			expiryMgr.Register(mgr.objectID, int(mod.PropID), mod, mod.GetExpiryTime())

			slog.Debug("ApplyModifier: 注册临时修改器到期管理",
				"object_id", mgr.objectID,
				"prop_id", mod.PropID,
				"source_id", mod.SourceID)
		}
		changemod = true
	}

	if changemod {
		// 标记为脏并传播
		slog.Debug("ApplyModifier: 属性变化，触发传播",
			"object_id", mgr.objectID,
			"prop_id", prop.ID())
		prop.SetDirty(true)
		mgr.propagateAndStats(prop)

		// 然后计算当前属性
		prop.Calculate(currentTime)
		newValue := prop.GetRaw()

		slog.Debug("ApplyModifier: 属性值计算完成",
			"object_id", mgr.objectID,
			"prop_id", mod.PropID,
			"old_value", oldValue,
			"new_value", newValue,
			"diff", newValue-oldValue)

		sourceType := mod.SourceType

		// 检查事件管理器是否存在
		if mgr.eventMgr != nil && mgr.eventMgr.IsEnabled() {
			slog.Debug("ApplyModifier: 准备触发事件",
				"object_id", mgr.objectID,
				"prop_id", mod.PropID,
				"source_type", sourceType)

			mgr.fireEvent(prop, oldValue, newValue, sourceType)
		} else {
			slog.Debug("ApplyModifier: 事件管理器不可用，跳过事件触发",
				"object_id", mgr.objectID,
				"prop_id", mod.PropID,
				"has_event_mgr", mgr.eventMgr != nil,
				"is_running", mgr.eventMgr != nil && mgr.eventMgr.IsEnabled())
		}
	} else {
		slog.Debug("ApplyModifier: 属性无变化，不触发事件",
			"object_id", mgr.objectID,
			"prop_id", mod.PropID,
			"source_id", mod.SourceID)
	}

	return changemod
}

// ApplyModifierByID 通过ID应用修改器
func (mgr *PropertyManager) ApplyModifierByID(propID int, value any, opType OpType, sourceType SourceType, sourceID int32, duration time.Duration) bool {
	mod := NewTimedModifier(value, opType, sourceType, sourceID, duration)
	mod.PropID = int32(propID)
	return mgr.ApplyModifier(mod)
}

// ApplyPermanentModifier 应用永久修改器
func (mgr *PropertyManager) ApplyPermanentModifier(propID int, value any, opType OpType, sourceType SourceType, sourceID int32) bool {
	mod := NewPermanentModifier(value, opType, sourceType, sourceID)
	mod.PropID = int32(propID)
	return mgr.ApplyModifier(mod)
}

// ApplyBatchModifier 应用批量修改器
// 参数：
//
//	sourceType - 修改器来源类型
//	sourceID   - 统一的sourceID（同一批修改器使用相同的sourceID）
//	items      - 修改器项列表
//	duration   - 持续时间（0表示永久，-1表示使用默认值）
//
// 返回值：
//
//	int  - 成功应用的修改器数量
//	bool - 是否全部应用成功
//
// 限制：
//  1. 同一属性上不能有重复的操作类型
//  2. 同一批修改器使用相同的sourceType和sourceID
func (mgr *PropertyManager) ApplyBatchModifier(sourceType SourceType, sourceID int32, items []BatchModifierItem, duration time.Duration) (int, bool) {
	// 参数验证
	if len(items) == 0 {
		return 0, true // 空列表，视为成功
	}

	if sourceID == 0 {
		// 如果没有提供sourceID，生成一个新的
		sourceID = mgr.GenerateSourceID()
	}

	// 检查同一属性上是否有重复的操作类型
	seenKeys := make(map[string]bool, len(items))
	for _, item := range items {
		// 验证OpType
		if item.OpType != OpTypeFlat && item.OpType != OpTypePercentAdd && item.OpType != OpTypePercentMult {
			slog.Error("ApplyBatchModifier: 无效的操作类型",
				"object_id", mgr.objectID,
				"prop_id", item.PropID,
				"op_type", item.OpType)
			return 0, false
		}

		// 检查同一属性上是否有重复的操作类型
		key := fmt.Sprintf("%d-%d", item.PropID, item.OpType)
		if seenKeys[key] {
			slog.Error("ApplyBatchModifier: 同一属性上有重复的操作类型",
				"object_id", mgr.objectID,
				"prop_id", item.PropID,
				"op_type", item.OpType)
			return 0, false
		}
		seenKeys[key] = true
	}

	// 获取锁
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	// 记录成功应用的修改器数量
	successCount := 0

	// 记录应用失败的修改器
	var failedItems []BatchModifierItem

	// 应用所有修改器
	for _, item := range items {
		// 创建TimedModifier
		mod := NewTimedModifier(item.Value, item.OpType, sourceType, sourceID, duration)
		mod.PropID = int32(item.PropID)

		// 查找属性
		globalToLocal := mgr.template.globalToLocal
		idx, ok := globalToLocal[item.PropID]
		if !ok {
			// 属性不存在，记录失败
			failedItems = append(failedItems, item)
			PutModifier(mod) // 归还mod到对象池
			continue
		}

		prop := mgr.props[idx]
		if prop == nil {
			failedItems = append(failedItems, item)
			PutModifier(mod)
			continue
		}

		// 获取当前时间
		currentTime := time.Now()

		// 先计算当前值，确保有正确的旧值
		if prop.IsDirty() {
			prop.Calculate(currentTime)
		}
		oldValue := prop.GetRaw() // 现在得到的是正确的当前值

		// 检查是否已存在相同条件的修改器
		exismod, changemod := prop.FindAndUpdateModifier(mod.SourceType, mod.SourceID, mod.Value, mod.OpType)
		if exismod {
			// 找到相同修改器，无论是否更新值，都需要归还新创建的mod
			PutModifier(mod)
		} else {
			// 不存在相同修改器，应用新修改器
			prop.ApplyModifier(mod)

			// 注册到期管理（如果是临时修改器）
			if mod.IsTemporary() {
				expiryMgr := GetExpiryManager()
				expiryMgr.Register(mgr.objectID, int(mod.PropID), mod, mod.GetExpiryTime())

				debugManagerLog("到期管理: 注册临时修改器（批量）",
					"object_id", mgr.objectID,
					"prop_id", mod.PropID,
					"duration", mod.Duration)
			}
			changemod = true
		}

		if changemod {
			// 标记为脏并传播
			debugManagerLog("ApplyBatchModifier: 属性变化，触发传播",
				"object_id", mgr.objectID,
				"prop_id", prop.ID())
			prop.SetDirty(true)
			mgr.propagateAndStats(prop)

			// 然后计算当前属性
			prop.Calculate(currentTime)
			newValue := prop.GetRaw()

			if oldValue != newValue {
				mgr.fireEvent(prop, oldValue, newValue, sourceType)
			}
		}

		successCount++
	}

	// 记录批量操作结果
	if len(failedItems) > 0 {
		slog.Warn("ApplyBatchModifier: 部分修改器应用失败",
			"object_id", mgr.objectID,
			"total_items", len(items),
			"success_count", successCount,
			"failed_count", len(failedItems),
			"source_type", sourceType,
			"source_id", sourceID)

		// 记录失败的详细原因
		for _, item := range failedItems {
			debugManagerLog("ApplyBatchModifier: 失败项详情",
				"prop_id", item.PropID,
				"op_type", item.OpType,
				"value", item.Value)
		}
	} else {
		debugManagerLog("ApplyBatchModifier: 批量修改器应用成功",
			"object_id", mgr.objectID,
			"total_items", len(items),
			"success_count", successCount,
			"source_type", sourceType,
			"source_id", sourceID)
	}

	// 如果所有修改器都应用成功，返回true
	allSuccess := len(failedItems) == 0
	return successCount, allSuccess
}
