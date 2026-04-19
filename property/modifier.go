// property/modifier.go
package property

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// 全局修改器对象池
var modifierPool = sync.Pool{
	New: func() interface{} {
		return &TimedModifier{
			isFromPool: true,
		}
	},
}
var use_modifylog bool = false

func debugModifierLog(msg string, args ...any) {
	if use_modifylog {
		slog.Debug(msg, args...)
	}
}

// GetModifier 从池中获取修改器
func GetModifier(value uint32, vtype ValueType, opType OpType, sourceType SourceType,
	sourceID int32, duration time.Duration) *TimedModifier {
	mod := modifierPool.Get().(*TimedModifier)

	mod.Value = value
	mod.OpType = opType
	mod.vtype = vtype
	mod.SourceType = sourceType
	mod.SourceID = sourceID
	mod.Duration = duration
	mod.StartTime = time.Now()
	mod.isPermanent = (duration < 0)
	mod.PropID = 0 // 初始化为0，使用时设置
	if use_modifylog {
		slog.Debug("从池中获取修改器",
			"value", value,
			"op_type", opType.String(),
			"source_type", sourceType.String(),
			"vtype", vtype.String(),
			"source_id", sourceID,
			"duration", duration.String())
	}

	return mod
}

// PutModifier 将修改器归还到池
func PutModifier(mod *TimedModifier) {
	if mod == nil {
		return
	}

	// 保留isFromPool标志
	isFromPool := mod.isFromPool

	// 重置字段，但保留池化标记
	mod.Value = 0
	mod.vtype = ValueTypeUnknown
	mod.StartTime = time.Time{}
	mod.Duration = 0
	mod.SourceType = 0
	mod.SourceID = 0
	mod.OpType = 0
	mod.PropID = 0
	mod.isPermanent = false
	mod.isFromPool = isFromPool // 保留原来的值

	if isFromPool {
		modifierPool.Put(mod)
		if use_modifylog {
			slog.Debug("修改器归还到池")
		}
	}
}

// TimedModifier 带时间的修改器
type TimedModifier struct {
	Value      uint32
	StartTime  time.Time
	Duration   time.Duration
	SourceID   int32
	PropID     int32
	SourceType SourceType
	vtype      ValueType
	OpType     OpType
	// 池化标记
	isFromPool  bool
	isPermanent bool
}

func NewTimedModifier(value any, opType OpType, sourceType SourceType,
	sourceID int32, duration time.Duration) *TimedModifier {
	rawvalue, vtype := AnyToValueTypeRaw(value)
	if vtype == ValueTypeUnknown {
		slog.Warn("创建修改器时值类型未知",
			"value", value,
			"op_type", opType.String(),
			"source_type", sourceType.String(),
			"source_id", sourceID,
			"duration", duration.String())
		return nil
	}
	return NewTimedModifierType(rawvalue, vtype, opType, sourceType, sourceID, duration)
}

// NewPermanentModifier 创建永久修改器（使用对象池）
func NewPermanentModifier(value any, opType OpType, sourceType SourceType, sourceID int32) *TimedModifier {
	debugModifierLog("创建永久修改器",
		"value", value,
		"op_type", opType.String(),
		"source_type", sourceType.String(),
		"source_id", sourceID)
	return NewTimedModifier(value, opType, sourceType, sourceID, time.Duration(-1))
}

// NewTimedModifier 创建带时间修改器（使用对象池）
func NewTimedModifierType(value uint32, vtype ValueType, opType OpType, sourceType SourceType,
	sourceID int32, duration time.Duration) *TimedModifier {
	return GetModifier(value, vtype, opType, sourceType, sourceID, duration)
}

// // NewInstantModifier 创建即时修改器（使用对象池）
// func NewInstantModifier(value float32, opType OpType,
// 	sourceType SourceType, sourceID int32) *TimedModifier {
// 	debugModifierLog("创建即时修改器",
// 		"value", value,
// 		"op_type", opType.String(),
// 		"source_type", sourceType.String(),
// 		"source_id", sourceID)
// 	return NewTimedModifier(value, opType, sourceType, sourceID,
// 		time.Duration(0))
// }

// GetState 获取修改器状态
func (tm *TimedModifier) GetState(currentTime time.Time) ModifierState {
	if tm.Duration < 0 {
		return ModifierStatePermanent
	}
	if tm.Duration == 0 {
		return ModifierStateInstant
	}

	if currentTime.Sub(tm.StartTime) >= tm.Duration {
		return ModifierStateExpired
	}
	return ModifierStateActive
}

// IsExpired 是否已过期
func (tm *TimedModifier) IsExpired(currentTime time.Time) bool {
	return tm.GetState(currentTime) == ModifierStateExpired
}

// GetRemainingTime 获取剩余时间
func (tm *TimedModifier) GetRemainingTime(currentTime time.Time) time.Duration {
	if tm.Duration <= 0 {
		return tm.Duration
	}

	elapsed := currentTime.Sub(tm.StartTime)
	if elapsed >= tm.Duration {
		return 0
	}
	return tm.Duration - elapsed
}

// GetElapsedTime 获取已过时间
func (tm *TimedModifier) GetElapsedTime(currentTime time.Time) time.Duration {
	if tm.Duration <= 0 {
		return 0
	}
	return currentTime.Sub(tm.StartTime)
}

// GetExpiryTime 获取到期时间
func (tm *TimedModifier) GetExpiryTime() time.Time {
	if tm.Duration <= 0 {
		return time.Time{} // 永久或即时修改器没有到期时间
	}
	return tm.StartTime.Add(tm.Duration)
}

// IsPermanent 是否永久
func (tm *TimedModifier) IsPermanent() bool {
	return tm.Duration < 0
}

// IsInstant 是否即时
func (tm *TimedModifier) IsInstant() bool {
	return tm.Duration == 0
}

// IsTemporary 是否临时
func (tm *TimedModifier) IsTemporary() bool {
	return tm.Duration > 0
}

// String 字符串表示
func (tm *TimedModifier) String() string {
	durationStr := "永久"
	if tm.Duration == 0 {
		durationStr = "即时"
	} else if tm.Duration > 0 {
		durationStr = tm.Duration.String()
	}

	return fmt.Sprintf("值:%.1f 类型:%v 来源:%v ID:%d 持续:%s",
		tm.Value, tm.OpType, tm.SourceType, tm.SourceID, durationStr)
}
