package property

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

var useTimingWheelLog = false

func debugLog(msg string, args ...any) {
	if useTimingWheelLog {
		slog.Debug(msg, args...)
	}
}

type TimingWheelLevel struct {
	level     int           // 层级索引
	tick      time.Duration // 本层的tick间隔
	wheelSize int           // 槽数
	slots     []*TimingWheelSlot
	current   int       // 当前槽索引
	startTime time.Time // 启动时间（对齐后的）
	child     *TimingWheelLevel
	parent    *TimingWheelLevel
	mu        sync.Mutex
}

type TimingWheelSlot struct {
	mu   sync.RWMutex // 读写锁优化
	head *ExpiryRecord
	tail *ExpiryRecord
}

type TimingWheelConfig struct {
	Tick      time.Duration
	WheelSize int
	MaxLevel  int
	Name      string
}

type TimingWheel struct {
	levels         []*TimingWheelLevel
	config         TimingWheelConfig
	ProcessExpired func([]*ExpiryRecord)
	running        int64
	wg             sync.WaitGroup
	stopCh         chan struct{}
	name           string

	stats struct {
		totalAdded     int64
		totalExpired   int64
		totalDemotions int64
		totalTicks     int64
		maxLevelUsed   int64
	}
}

func NewTimingWheel(config TimingWheelConfig) *TimingWheel {
	// 配置验证
	if config.Tick <= 0 {
		config.Tick = 10 * time.Millisecond
		slog.Warn("时间轮配置: tick无效，使用默认值", "tick", config.Tick.String())
	}
	if config.WheelSize <= 0 {
		config.WheelSize = 120
		slog.Warn("时间轮配置: wheelSize无效，使用默认值", "wheelSize", config.WheelSize)
	}
	if config.MaxLevel <= 0 {
		config.MaxLevel = 3
		slog.Warn("时间轮配置: maxLevel无效，使用默认值", "maxLevel", config.MaxLevel)
	}
	if config.Name == "" {
		config.Name = "timing_wheel"
	}

	// 创建时间轮
	tw := &TimingWheel{
		config:  config,
		name:    config.Name,
		running: 0,
		stopCh:  make(chan struct{}),
	}

	// 初始化层级
	tw.levels = make([]*TimingWheelLevel, config.MaxLevel)
	for i := 0; i < config.MaxLevel; i++ {
		level := &TimingWheelLevel{
			level:     i,
			tick:      config.Tick,
			wheelSize: config.WheelSize,
			slots:     make([]*TimingWheelSlot, config.WheelSize),
		}

		// 初始化槽
		for j := 0; j < config.WheelSize; j++ {
			level.slots[j] = &TimingWheelSlot{}
		}

		// 计算高层级的tick
		if i > 0 {
			level.tick = time.Duration(level.wheelSize) * tw.levels[i-1].tick
		}

		tw.levels[i] = level

		capacity := time.Duration(level.wheelSize) * level.tick
		debugLog("创建时间轮层级",
			"name", tw.name,
			"level", i,
			"tick", level.tick.String(),
			"capacity", capacity.String())
	}

	// 建立层级关系
	for i := 0; i < config.MaxLevel; i++ {
		if i > 0 {
			tw.levels[i].child = tw.levels[i-1]
		}
		if i < config.MaxLevel-1 {
			tw.levels[i].parent = tw.levels[i+1]
		}
	}

	return tw
}

// GetBaseTick 获取基础tick间隔
func (tw *TimingWheel) GetBaseTick() time.Duration {
	if len(tw.levels) > 0 {
		return tw.levels[0].tick
	}
	return tw.config.Tick
}

// GetWheelSize 获取时间轮槽数
func (tw *TimingWheel) GetWheelSize() int {
	return tw.config.WheelSize
}

func (tw *TimingWheel) Start() {
	if atomic.CompareAndSwapInt64(&tw.running, 0, 1) {
		for i := 0; i < len(tw.levels); i++ {
			tw.wg.Add(1)
			go func(levelIdx int) {
				tw.runLevel(levelIdx)
			}(i)
		}

		debugLog("时间轮启动",
			"name", tw.name,
			"levels", len(tw.levels),
			"tick", tw.config.Tick.String())
	}
}

func (tw *TimingWheel) Stop() {
	if atomic.CompareAndSwapInt64(&tw.running, 1, 0) {
		close(tw.stopCh)

		// 等待所有层级停止
		done := make(chan struct{})
		go func() {
			tw.wg.Wait()
			close(done)
		}()

		// 设置超时
		select {
		case <-done:
			debugLog("时间轮已优雅停止", "name", tw.name)
		case <-time.After(5 * time.Second):
			slog.Warn("时间轮停止超时", "name", tw.name)
		}
	}
}

func (tw *TimingWheel) runLevel(levelIdx int) {
	defer tw.wg.Done()

	// 添加panic恢复机制
	defer func() {
		if r := recover(); r != nil {
			slog.Error("时间轮层级发生panic",
				"name", tw.name,
				"level", levelIdx,
				"panic", r,
				"stack", debug.Stack())
		}
	}()

	if levelIdx < 0 || levelIdx >= len(tw.levels) {
		return
	}

	level := tw.levels[levelIdx]
	ticker := time.NewTicker(level.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			currentTime := time.Now()
			expired := tw.AdvanceLevel(levelIdx, currentTime)

			if len(expired) > 0 && tw.ProcessExpired != nil {
				tw.ProcessExpired(expired)
			}
		case <-tw.stopCh:
			debugLog("时间轮层级运行停止", "name", tw.name, "level", levelIdx)
			return
		}
	}
}

func (tw *TimingWheel) AdvanceLevel(levelIdx int, currentTime time.Time) []*ExpiryRecord {
	if levelIdx < 0 || levelIdx >= len(tw.levels) {
		return nil
	}

	level := tw.levels[levelIdx]

	// 对齐启动时间
	level.alignStartTime(currentTime)

	// 计算应该前进的槽数
	elapsedTicks := int(currentTime.Sub(level.startTime) / level.tick)
	ticksToAdvance := elapsedTicks - level.current

	if ticksToAdvance <= 0 {
		return nil
	}

	var allExpired []*ExpiryRecord

	// 推进ticksToAdvance个槽
	for i := 0; i < ticksToAdvance; i++ {
		// 计算下一个槽索引
		nextSlot := (level.current + 1) % level.wheelSize

		// 获取当前槽的所有记录
		slot := level.slots[level.current]
		slot.mu.Lock()
		records := slot.head
		slot.head = nil
		slot.tail = nil
		slot.mu.Unlock()

		// 处理当前槽的记录
		if records != nil {
			processed := tw.processSlotRecords(level, records, currentTime)
			allExpired = append(allExpired, processed...)
		}

		// 更新当前槽
		level.current = nextSlot

		// 如果回到0，需要处理父层
		if nextSlot == 0 && level.parent != nil {
			parentExpired := tw.AdvanceLevel(level.parent.level, currentTime)
			allExpired = append(allExpired, parentExpired...)
		}
	}

	atomic.AddInt64(&tw.stats.totalTicks, int64(ticksToAdvance))
	return allExpired
}

func (tw *TimingWheel) processSlotRecords(level *TimingWheelLevel, head *ExpiryRecord, currentTime time.Time) []*ExpiryRecord {
	var expired []*ExpiryRecord
	var toReadd []*ExpiryRecord
	
	// 遍历链表
	for record := head; record != nil; record = record.GetNext() {
		// 使用精确比较
		remaining := record.ExpiryTime.Sub(currentTime)
		
		if remaining <= 0 {
			// 已过期
			expired = append(expired, record)
		} else {
			// 未过期，需要重新评估
			toReadd = append(toReadd, record)
		}
	}
	
	// 修复：正确统计过期记录数量
	if len(expired) > 0 {
		atomic.AddInt64(&tw.stats.totalExpired, int64(len(expired)))
	}
	
	// 重新添加未过期的记录
	for _, record := range toReadd {
		remaining := record.ExpiryTime.Sub(currentTime)
		
		// 如果剩余时间>0，才重新添加
		if remaining > 0 {
			// 检查是否需要降级到更精确的层
			if level.child != nil && remaining <= time.Duration(level.child.wheelSize)*level.child.tick {
				// 可以降级到子层
				record.SetLevel(level.child.level)
				tw.Add(record)
				atomic.AddInt64(&tw.stats.totalDemotions, 1)
				
				if useTimingWheelLog {
					slog.Debug("时间轮记录降级",
						"from_level", level.level,
						"to_level", level.child.level,
						"object_id", record.ObjectID,
						"remaining", remaining.String())
				}
			} else {
				// 仍然在本层
				tw.Add(record)
			}
		} else {
			// 剩余时间<=0，应该被触发
			expired = append(expired, record)
		}
	}
	
	return expired
}

func (tw *TimingWheel) Add(record *ExpiryRecord) bool {
	if record == nil || record.Modifier == nil {
		slog.Warn("时间轮添加: 记录或修改器为空", "name", tw.name)
		return false
	}

	now := time.Now()
	if now.After(record.ExpiryTime) {
		debugLog("时间轮添加: 记录已过期",
			"name", tw.name,
			"object_id", record.ObjectID,
			"now", now.Format("15:04:05.000"),
			"expiry_time", record.ExpiryTime.Format("15:04:05.000"))
		atomic.AddInt64(&tw.stats.totalAdded, 1)
		atomic.AddInt64(&tw.stats.totalExpired, 1)			
		if tw.ProcessExpired != nil {
			tw.ProcessExpired([]*ExpiryRecord{record})
		}			
		return true
	}

	// 计算剩余时间
	duration := record.ExpiryTime.Sub(now)
	debugLog("EX时间轮添加: 开始计算",
		"name", tw.name,
		"object_id", record.ObjectID,
		"now", now.Format("15:04:05.000"),
		"expiry_time", record.ExpiryTime.Format("15:04:05.000"),
		"duration", duration.String())

	// 找到合适的层级
	level := tw.findSuitableLevel(duration)
	if level == nil {
		slog.Error("时间轮添加: 找不到合适的层级",
			"name", tw.name,
			"duration", duration.String())
		return false
	}

	debugLog("EX时间轮添加: 找到层级",
		"name", tw.name,
		"object_id", record.ObjectID,
		"level", level.level,
		"level_tick", level.tick.String(),
		"level_wheel_size", level.wheelSize)

	// 对齐启动时间
	level.alignStartTime(now)
	debugLog("EX时间轮添加: 对齐后",
		"name", tw.name,
		"object_id", record.ObjectID,
		"level", level.level,
		"start_time", level.startTime.Format("15:04:05.000"),
		"current_time", now.Format("15:04:05.000"),
		"tick", level.tick.String())

	// 计算在当前层的ticks
	timeSinceStart := now.Sub(level.startTime)
	if timeSinceStart < 0 {
		slog.Error("时间轮添加: 当前时间早于启动时间",
			"name", tw.name,
			"object_id", record.ObjectID,
			"level", level.level,
			"now", now.Format("15:04:05.000"),
			"start_time", level.startTime.Format("15:04:05.000"))
		return false
	}

	elapsedTicks := int(timeSinceStart / level.tick)
	ticksInThisLevel := int(duration / level.tick)
	if ticksInThisLevel < 1 {
		ticksInThisLevel = 1
	}

	// 计算槽索引
	slotIdx := (elapsedTicks + ticksInThisLevel) % level.wheelSize

	debugLog("EX时间轮添加: 计算槽索引",
		"name", tw.name,
		"object_id", record.ObjectID,
		"level", level.level,
		"time_since_start", timeSinceStart.String(),
		"tick", level.tick.String(),
		"elapsed_ticks", elapsedTicks,
		"ticks_in_this_level", ticksInThisLevel,
		"wheel_size", level.wheelSize,
		"slot_idx", slotIdx)

	// 设置记录的层级信息
	record.SetLevel(level.level)
	record.SetSlotIdx(slotIdx)

	// 添加到槽
	slot := level.slots[slotIdx]
	slot.mu.Lock()

	record.SetNext(nil)
	if slot.head == nil {
		slot.head = record
		slot.tail = record
	} else {
		slot.tail.SetNext(record)
		slot.tail = record
	}

	slot.mu.Unlock()

	atomic.AddInt64(&tw.stats.totalAdded, 1)
	if int64(level.level) > atomic.LoadInt64(&tw.stats.maxLevelUsed) {
		atomic.StoreInt64(&tw.stats.maxLevelUsed, int64(level.level))
	}

	debugLog("时间轮添加: 成功",
		"name", tw.name,
		"level", level.level,
		"object_id", record.ObjectID,
		"slot_idx", slotIdx,
		"ticks", ticksInThisLevel,
		"elapsed_ticks", elapsedTicks,
		"expiry_time", record.ExpiryTime.Format("15:04:05.000"),
		"level_tick", level.tick.String(),
		"start_time", level.startTime.Format("15:04:05.000"))

	return true
}

func (tw *TimingWheel) RemoveForObject(objectID int64) int {
	removed := 0

	for _, level := range tw.levels {
		for _, slot := range level.slots {
			slot.mu.Lock()

			var prev *ExpiryRecord
			curr := slot.head

			for curr != nil {
				if curr.ObjectID == objectID {
					// 移除记录
					if prev == nil {
						slot.head = curr.GetNext()
						if slot.head == nil {
							slot.tail = nil
						}
					} else {
						prev.SetNext(curr.GetNext())
						if curr.GetNext() == nil {
							slot.tail = prev
						}
					}
					removed++
				} else {
					prev = curr
				}
				curr = curr.GetNext()
			}

			slot.mu.Unlock()
		}
	}

	if removed > 0 {
		debugLog("时间轮移除对象记录",
			"name", tw.name,
			"object_id", objectID,
			"removed_count", removed)
	}

	return removed
}

func (tw *TimingWheel) findSuitableLevel(duration time.Duration) *TimingWheelLevel {
	// 计算第0层容量
	level0Capacity := time.Duration(tw.levels[0].wheelSize) * tw.levels[0].tick

	// 如果duration能被第0层容纳，优先放入第0层
	if duration <= level0Capacity {
		return tw.levels[0]
	}

	// 遍历其他层级
	for i := 0; i < len(tw.levels); i++ {
		level := tw.levels[i]
		levelCapacity := time.Duration(level.wheelSize) * level.tick

		// 如果能被本层容纳，或者是最后一层
		if duration <= levelCapacity || i == len(tw.levels)-1 {
			return level
		}
	}

	return tw.levels[len(tw.levels)-1]
}

func (level *TimingWheelLevel) alignStartTime(currentTime time.Time) {
	level.mu.Lock()
	defer level.mu.Unlock()

	if level.startTime.IsZero() {
		// 向下取整对齐
		level.startTime = currentTime.Truncate(level.tick)

		debugLog("时间轮层级对齐启动时间",
			"level", level.level,
			"current_time", currentTime.Format("15:04:05.000"),
			"tick", level.tick.String(),
			"start_time", level.startTime.Format("15:04:05.000"),
			"level_index", level.level)
	}
}

func (tw *TimingWheel) PrintStats() {
	stats := tw.GetStats()

	debugLog("时间轮统计",
		"name", stats["name"],
		"total_added", stats["total_added"],
		"total_expired", stats["total_expired"],
		"total_demotions", stats["total_demotions"],
		"total_ticks", stats["total_ticks"],
		"max_level_used", stats["max_level_used"],
		"level_count", stats["level_count"])

	// 添加各层信息
	for i := 0; i < len(tw.levels); i++ {
		level := tw.levels[i]
		capacity := time.Duration(level.wheelSize) * level.tick
		debugLog("时间轮层级",
			"level", i,
			"tick", level.tick.String(),
			"capacity", capacity.String(),
			"current_slot", level.current)
	}
}

func (tw *TimingWheel) GetStats() map[string]interface{} {
	stats := map[string]interface{}{
		"name":            tw.name,
		"total_added":     atomic.LoadInt64(&tw.stats.totalAdded),
		"total_expired":   atomic.LoadInt64(&tw.stats.totalExpired),
		"total_demotions": atomic.LoadInt64(&tw.stats.totalDemotions),
		"total_ticks":     atomic.LoadInt64(&tw.stats.totalTicks),
		"max_level_used":  atomic.LoadInt64(&tw.stats.maxLevelUsed),
		"level_count":     len(tw.levels),
	}

	return stats
}

func (tw *TimingWheel) HealthCheck() (bool, map[string]interface{}) {
	status := map[string]interface{}{
		"name":          tw.name,
		"is_running":    atomic.LoadInt64(&tw.running) == 1,
		"total_added":   atomic.LoadInt64(&tw.stats.totalAdded),
		"total_expired": atomic.LoadInt64(&tw.stats.totalExpired),
		"level_count":   len(tw.levels),
	}

	// 检查各层状态
	for i, level := range tw.levels {
		status[fmt.Sprintf("level_%d_current", i)] = level.current
		status[fmt.Sprintf("level_%d_tick", i)] = level.tick.String()
	}

	isHealthy := atomic.LoadInt64(&tw.running) == 1
	return isHealthy, status
}
