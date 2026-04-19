// property/types.go
package property

import "math"

// 值类型
type ValueType int8

const (
	ValueTypeUnknown ValueType = 0
	ValueTypeFloat32 ValueType = 1
	ValueTypeInt32   ValueType = 2
	ValueTypeBool    ValueType = 3
)

func (vt ValueType) String() string {
	switch vt {
	case ValueTypeFloat32:
		return "float32"
	case ValueTypeInt32:
		return "int32"
	case ValueTypeBool:
		return "bool"
	default:
		return "unknown"
	}
}
func (vt ValueType) ToFloat32(value uint32) (float32, bool) {
	switch vt {
	case ValueTypeFloat32:
		return math.Float32frombits(value), true
	case ValueTypeInt32:
		return float32(int32(value)), true
	case ValueTypeBool:
		if value != 0 {
			return 1.0, true
		}
		return 0.0, true
	default:
		return 0.0, false
	}
}
func (vt ValueType) ToFloat32Value(value uint32) float32 {
	vout, _ := vt.ToFloat32(value)
	return vout
}
func (vt ValueType) ToFloat64(value uint32) (float64, bool) {
	switch vt {
	case ValueTypeFloat32:
		return float64(math.Float32frombits(value)),	 true
	case ValueTypeInt32:
		return float64(int32(value)), true
	case ValueTypeBool:
		if value != 0 {
			return float64(1.0), true
		}
		return float64(0.0), true
	default:
		return float64(0.0), false
	}
}

func (vt ValueType) ToFloat64Value(value uint32) float64 {
	vout, _ := vt.ToFloat64(value)
	return vout
}

func (vt ValueType) ToInt32(value uint32) (int32, bool) {
	switch vt {
	case ValueTypeFloat32:
		return int32(math.Float32frombits(value)), true
	case ValueTypeInt32:
		return int32(value), true
	case ValueTypeBool:
		if value != 0 {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
func (vt ValueType) ToInt32Value(value uint32) int32 {
	vout, _ := vt.ToInt32(value)
	return vout
}
func (vt ValueType) ToBool(value uint32) (bool, bool) {
	switch vt {
	case ValueTypeFloat32:
		return !(math.Float32frombits(value) == 0.0), true
	case ValueTypeInt32:
		return !(int32(value) == 0), true
	case ValueTypeBool:
		if value == 0 {
			return false, true
		}
		return true, true
	default:
		return false, false
	}
}
func (vt ValueType) ToBoolValue(value uint32) bool {
	vout, _ := vt.ToBool(value)
	return vout
}
func (vt ValueType) FromFloat32ToRaw(value float32) uint32 {
	switch vt {
	case ValueTypeFloat32:
		return math.Float32bits(value)
	case ValueTypeInt32:
		return uint32(value)
	case ValueTypeBool:
		if value == 0.0 {
			return 0
		}
		return 1
	default:
		return 0
	}
}
func (vt ValueType) FromFloat64ToRaw(value float64) uint32 {
	switch vt {
	case ValueTypeFloat32:
		return math.Float32bits(float32(value))
	case ValueTypeInt32:
		return uint32(value)
	case ValueTypeBool:
		if value == 0.0 {
			return 0
		}
		return 1
	default:
		return 0
	}
}

func (vt ValueType) FromInt32ToRaw(value int32) uint32 {
	switch vt {
	case ValueTypeFloat32:
		return math.Float32bits(float32(value))
	case ValueTypeInt32:
		return uint32(value)
	case ValueTypeBool:
		if value == 0 {
			return 0
		}
		return 1
	default:
		return 0
	}
}
func (vt ValueType) FromBoolToRaw(value bool) uint32 {
	switch vt {
	case ValueTypeFloat32:
		if value == false {
			return math.Float32bits(float32(0.0))
		}
		return math.Float32bits(float32(1.0))
	case ValueTypeInt32:
		if value == false {
			return uint32(0)
		}
		return uint32(1)
	case ValueTypeBool:
		if value == false {
			return uint32(0)
		}
		return uint32(1)
	default:
		return uint32(0)
	}
}
func (vt ValueType) FromAnyToRaw(value any) uint32 {

	var rawvalue uint32 = 0
	switch rvalue := value.(type) {
	case float32:
		rawvalue = vt.FromFloat32ToRaw(rvalue)
	case int32:
		rawvalue = vt.FromInt32ToRaw(rvalue)
	case bool:
		rawvalue = vt.FromBoolToRaw(rvalue)
	default:
		rawvalue = 0
	}

	return rawvalue
}
func AnyToValueTypeRaw(value any) (uint32, ValueType) {
	vtype := ValueTypeUnknown
	var rawvalue uint32 = 0
	switch rvalue := value.(type) {
	case float64:
		vtype = ValueTypeFloat32
		rawvalue = vtype.FromFloat32ToRaw(float32(rvalue))
	case float32:
		vtype = ValueTypeFloat32
		rawvalue = vtype.FromFloat32ToRaw(rvalue)
	case int:
		vtype = ValueTypeInt32
		rawvalue = vtype.FromInt32ToRaw(int32(rvalue))
	case int32:
		vtype = ValueTypeInt32
		rawvalue = vtype.FromInt32ToRaw(rvalue)
	case bool:
		vtype = ValueTypeBool
		rawvalue = vtype.FromBoolToRaw(rvalue)
	default:
		vtype = ValueTypeUnknown
		rawvalue = uint32(0)
	}
	return rawvalue, vtype
}
func Float64ToRaw(value float64) uint32 {
	return math.Float32bits(float32(value))
}

// 属性类型
type PropType int8

const (
	PropTypeImmediate PropType = 0 // 立即型
	PropTypeStandard  PropType = 1 // 标准型
	PropTypeDerived   PropType = 2 // 推导型
)

func (pt PropType) String() string {
	switch pt {
	case PropTypeImmediate:
		return "immediate"
	case PropTypeStandard:
		return "standard"
	case PropTypeDerived:
		return "derived"
	default:
		return "unknown"
	}
}

// 操作类型
type OpType int8

const (
	OpTypeFlat        OpType = 0 // 绝对值
	OpTypePercentAdd  OpType = 1 // 百分比加
	OpTypePercentMult OpType = 2 // 百分比乘
)

func (ot OpType) String() string {
	switch ot {
	case OpTypeFlat:
		return "flat"
	case OpTypePercentAdd:
		return "percent_add"
	case OpTypePercentMult:
		return "percent_mult"
	default:
		return "unknown"
	}
}

// 来源类型
type SourceType int8

const (
	SourceTypeBase   SourceType = 0 // 基础
	SourceTypeEquip  SourceType = 1 // 装备
	SourceTypeBuff   SourceType = 2 // Buff
	SourceTypeTalent SourceType = 3 // 天赋
	SourceTypeSkill  SourceType = 4 // 技能
)

func (st SourceType) String() string {
	switch st {
	case SourceTypeBase:
		return "base"
	case SourceTypeEquip:
		return "equip"
	case SourceTypeBuff:
		return "buff"
	case SourceTypeTalent:
		return "talent"
	case SourceTypeSkill:
		return "skill"
	default:
		return "unknown"
	}
}

// 修改器状态
type ModifierState int

const (
	ModifierStateActive    ModifierState = iota // 活跃
	ModifierStateExpired                        // 已过期
	ModifierStatePermanent                      // 永久
	ModifierStateInstant                        // 即时
)

func (mt ModifierState) String() string {
	switch mt {
	case ModifierStateActive:
		return "active"
	case ModifierStateExpired:
		return "expired"
	case ModifierStatePermanent:
		return "permanent"
	case ModifierStateInstant:
		return "instant"
	default:
		return "unknown"
	}
}

// FormulaCalculator 公式计算器接口
type FormulaCalculator interface {
	Calculate(vt ValueType, props []*RuntimeProp) uint32
}
