// utils/gametime/gametime.go
package gametime

import "time"

// Now 获取当前游戏时间
func Now() time.Time {
    return time.Now()
}

// DurationPermanent 永久持续时间
const DurationPermanent = time.Duration(-1)

// DurationInstant 即时效果
const DurationInstant = time.Duration(0)

// IsTimeout 检查是否超时
func IsTimeout(startTime time.Time, duration time.Duration, currentTime time.Time) bool {
    if duration <= 0 {
        return false
    }
    return currentTime.Sub(startTime) >= duration
}

// RemainingTime 获取剩余时间
func RemainingTime(startTime time.Time, duration time.Duration, currentTime time.Time) time.Duration {
    if duration <= 0 {
        return duration
    }
    elapsed := currentTime.Sub(startTime)
    if elapsed >= duration {
        return 0
    }
    return duration - elapsed
}

// Sub 计算时间差
func Sub(t1, t2 time.Time) time.Duration {
    return t1.Sub(t2)
}

// Init 初始化时间系统
func Init() {
    // 现在不需要特殊初始化
}

// 如果有其他模块依赖Tick类型，可以添加兼容性包装
// 但建议直接迁移到time.Time