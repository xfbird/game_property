// property/formula_impl.go
package property
import (
	"log/slog"
)

func GetPropFloat64(prop *RuntimeProp) float64 {
	if prop == nil {
		return 0
	}
	return float64(prop.GetFloat())
}

// 辅助函数
func GetPropFloat(prop *RuntimeProp) float32 {
	if prop == nil {
		return 0
	}
	return prop.GetFloat()
}

func GetPropInt(prop *RuntimeProp) int32 {
	if prop == nil {
		return 0
	}
	return prop.GetInt()
}

// LinearAttackFormula 线性攻击力公式
type LinearAttackFormula struct{}

func (f *LinearAttackFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
	if len(props) < 2 {
		return 0
	}

	strength := GetPropFloat64(props[0])
	agility := GetPropFloat64(props[1])
	slog.Debug("LinearAttackFormula Calculate",
		"strength", strength,
		"agility", agility,
		"result", strength*2 + agility*1.5,
	)
	// 计算攻击力
	return vt.FromFloat64ToRaw(strength*2 + agility*1.5) //将结果转换为 vt 需要的raw 表达形式
}

// MaxHPFormula 最大生命值公式
type MaxHPFormula struct{}

func (f *MaxHPFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
	if len(props) < 1 {
		return 0
	}

	stamina := GetPropInt(props[0])
	// 返回浮点数编码
	return vt.FromInt32ToRaw(stamina * 20)
}

// DefenseFormula 防御力公式
type DefenseFormula struct{}

// func (f *DefenseFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
// 	if len(props) < 2 {
// 		return 0
// 	}

// 	stamina := GetPropFloat64(props[0])
// 	strength := GetPropFloat64(props[1])
// 	return vt.FromFloat64ToRaw(stamina*1.5 + strength*0.5)
// 	// 返回浮点数编码
// }

func (f *DefenseFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
	if len(props) < 2 {
		return 0
	}

	// 根据依赖顺序：["stamina", "strength"]
	stamina := GetPropFloat64(props[0])  // 第一个是耐力
	strength := GetPropFloat64(props[1]) // 第二个是力量
	
	slog.Debug("DefenseFormula Calculate",
		"stamina", stamina,
		"strength", strength,
		"strength * 3", strength*3,
		"result", stamina + strength*0.3,  // 修改为 stamina + strength*0.3
	)
	// 防御力 = 耐力 + 力量*0.3
	return vt.FromFloat64ToRaw(stamina + strength*0.3)
	// 返回浮点数编码
}

// CombatPowerFormula 战斗力公式
type CombatPowerFormula struct{}

func (f *CombatPowerFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
	if len(props) < 3 {
		return 0
	}

	attack := GetPropFloat64(props[0])
	hp := GetPropFloat64(props[1])
	defense := GetPropFloat64(props[2])
	return vt.FromFloat64ToRaw(attack*0.5 + hp*0.3 + defense*0.2)
	// 返回浮点数编码
}

// SumFormula 求和公式
type SumFormula struct{}

func (f *SumFormula) Calculate(vt ValueType, props []*RuntimeProp) uint32 {
	if len(props) == 0 {
		return 0
	}

	switch  vt {		//根据结果的类型，将所有的类型的值 进行 相加。
		case ValueTypeFloat32:
			sum := float64(0)
			for _, prop := range props {
				sum += GetPropFloat64(prop)
			}
			return vt.FromFloat64ToRaw(sum)

			// 处理 Float32 类型的属性
		case ValueTypeInt32:
		sum := int32(0)
			for _, prop := range props {
				sum += GetPropInt(prop)
			}
			return vt.FromInt32ToRaw(sum)
			// 处理 Int32 类型的属性
		case ValueTypeBool:
			sum := float64(0.0)
			for _, prop := range props {
				sum += GetPropFloat64(prop)
			}
			return vt.FromBoolToRaw(sum!=0.0)
			// 处理 Bool 类型的属性
		default:
		return vt.FromInt32ToRaw(0)
	}
	// 无视类型把 所有的 值 取出来 转换为 Float64
}
