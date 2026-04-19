// property/expiry_manager.go
package property

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var dOutExpiryLog = true

func debugExpiryLog(msg string, args ...any) {
	if dOutExpiryLog {
		slog.Debug(msg, args...)
	}
}

// ExpiryRecord 到期记录
type ExpiryRecord struct {
	ObjectID   int64
	PropID     int
	Modifier   *TimedModifier
	ExpiryTime time.Time
	SourceID   int32 // 存储sourceID，用于正确移除修改器

	// 内部字段
	next    *ExpiryRecord // 链表下一个
	level   int           // 当前所在的层级
	slotIdx int           // 在当前层的槽索引
}

// GetNext 获取下一个记录
func (r *ExpiryRecord) GetNext() *ExpiryRecord {
	if r == nil {
		return nil
	}
	return r.next
}

// SetNext 设置下一个记录
func (r *ExpiryRecord) SetNext(next *ExpiryRecord) {
	if r == nil {
		return
	}
	r.next = next
}

// SetLevel 设置层级
func (r *ExpiryRecord) SetLevel(level int) {
	if r == nil {
		return
	}
	r.level = level
}

// GetLevel 获取层级
func (r *ExpiryRecord) GetLevel() int {
	if r == nil {
		return 0
	}
	return r.level
}

// SetSlotIdx 设置槽索引
func (r *ExpiryRecord) SetSlotIdx(slotIdx int) {
	if r == nil {
		return
	}
	r.slotIdx = slotIdx
}

// GetSlotIdx 获取槽索引
func (r *ExpiryRecord) GetSlotIdx() int {
	if r == nil {
		return 0
	}
	return r.slotIdx
}

// ExpiryCallback 到期回调函数类型
type ExpiryCallback func(objectID int64, propID int, srcType SourceType, sourceID int32) // 修改：增加sourceID参数

// ExpiryManager 全局到期管理器
type ExpiryManager struct {
	mu sync.Mutex

	// 时间轮
	timingWheel *TimingWheel

	// 回调映射
	expiryCallbacks map[int64]ExpiryCallback

	// 运行状态
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup

	// 统计信息
	statTotalRegistered int64
	statTotalExpired    int64
	statCleanupCalls    int64
	statSkippedCleanups int64
	statMaxCleanupTime  time.Duration
}

// 全局到期管理器实例
var (
	expiryManager     *ExpiryManager
	expiryManagerOnce sync.Once
)

// GetExpiryManager 获取全局到期管理器
func GetExpiryManager() *ExpiryManager {
	expiryManagerOnce.Do(func() {
		// 创建时间轮配置
		config := TimingWheelConfig{
			Tick:      10 * time.Millisecond,
			WheelSize: 120, // 增加槽数
			MaxLevel:  4,   // 4层
			Name:      "global_expiry",
		}

		// 创建时间轮
		timingWheel := NewTimingWheel(config)

		expiryManager = &ExpiryManager{
			timingWheel:     timingWheel,
			expiryCallbacks: make(map[int64]ExpiryCallback),
			stopCh:          make(chan struct{}),
		}
		expiryManager.start()
		debugExpiryLog("✅ 全局到期管理器已创建（10ms精度多层时间轮）",
			"levels", config.MaxLevel,
			"base_tick", config.Tick.String(),
			"wheel_size", config.WheelSize)

	})
	return expiryManager
}

// GetExpiryCallback 获取对象的到期回调
func (em *ExpiryManager) GetExpiryCallback(objectID int64) ExpiryCallback {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.expiryCallbacks == nil {
		return nil
	}

	return em.expiryCallbacks[objectID]
}

// SetExpiryCallback 设置对象的到期回调
func (em *ExpiryManager) SetExpiryCallback(objectID int64, callback ExpiryCallback) {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.expiryCallbacks == nil {
		em.expiryCallbacks = make(map[int64]ExpiryCallback)
	}

	if callback == nil {
		delete(em.expiryCallbacks, objectID)
	} else {
		em.expiryCallbacks[objectID] = callback
	}
}

// Register 注册到期记录
func (em *ExpiryManager) Register(objectID int64, propID int, modifier *TimedModifier, expiryTime time.Time) {
	if !modifier.IsTemporary() {
		return
	}

	em.mu.Lock()
	defer em.mu.Unlock()

	// 创建记录
	record := &ExpiryRecord{
		ObjectID:   objectID,
		PropID:     propID,
		Modifier:   modifier,
		ExpiryTime: expiryTime,
		SourceID:   modifier.SourceID, // 存储sourceID
	}

	// 添加到时间轮
	success := em.timingWheel.Add(record)
	if success {
		atomic.AddInt64(&em.statTotalRegistered, 1)
		debugExpiryLog("到期管理: 注册记录",
			"object_id", objectID,
			"prop_id", propID,
			"source_id", modifier.SourceID,
			"expiry_time", expiryTime.Format("15:04:05.000"))
	}
}

// UnregisterAllForObject 移除对象的所有到期记录
func (em *ExpiryManager) UnregisterAllForObject(objectID int64) int {
	em.mu.Lock()
	defer em.mu.Unlock()

	// 从时间轮移除
	removed := 0
	if em.timingWheel != nil {
		removed = em.timingWheel.RemoveForObject(objectID)
	}

	// 同时移除回调
	delete(em.expiryCallbacks, objectID)

	if removed > 0 {
		debugExpiryLog("到期管理: 移除对象的到期记录",
			"object_id", objectID,
			"removed_count", removed)
	}

	return removed
}

// handleExpiredRecords 处理过期记录（由时间轮调用）
func (em *ExpiryManager) handleExpiredRecords(records []*ExpiryRecord) {
	startTime := time.Now()

	if len(records) == 0 {
		return
	}

	// 统计
	atomic.AddInt64(&em.statCleanupCalls, 1)
	atomic.AddInt64(&em.statTotalExpired, int64(len(records)))

	// 处理每个过期记录
	for _, record := range records {
		em.processExpiredRecord(record)
	}

	// 更新最大处理时间
	cleanupTime := time.Since(startTime)
	if cleanupTime > em.statMaxCleanupTime {
		atomic.StoreInt64((*int64)(&em.statMaxCleanupTime), int64(cleanupTime))
	}

	if len(records) > 0 {
		debugExpiryLog("时间轮清理: 处理过期修改器",
			"expired_count", len(records),
			"cleanup_time", cleanupTime.String())
	}
}

// processExpiredRecord 处理过期记录
func (em *ExpiryManager) processExpiredRecord(record *ExpiryRecord) {
	// 获取回调
	em.mu.Lock()
	callback := em.expiryCallbacks[record.ObjectID]
	objID := record.ObjectID
	propID := record.PropID
	srcType := record.Modifier.SourceType
	srcID := record.SourceID // 使用存储的sourceID
	em.mu.Unlock()

	// 异步处理回调
	if callback != nil {
		debugExpiryLog("callback processExpiredRecord",
			"object_id", objID,
			"prop_id", propID,
			"source_type", srcType,
			"source_id", srcID)

		go func(objID int64, propID int, srcType SourceType, srcID int32) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("到期回调panic",
						"object_id", objID,
						"prop_id", propID,
						"source_type", srcType,
						"source_id", srcID,
						"panic", r)
				}
			}()
			callback(objID, propID, srcType, srcID)
		}(objID, propID, srcType, srcID)
	}
}

// 清理循环（现在只是打印统计，实际清理由时间轮驱动）
func (em *ExpiryManager) cleanupLoop() {
	defer em.wg.Done()

	ticker := time.NewTicker(5 * time.Second) // 5秒打印一次统计
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 每5秒打印一次统计
			if em.statTotalExpired > 0 || em.statTotalRegistered > 0 {
				em.PrintStatsBrief()
			}

		case <-em.stopCh:
			return
		}
	}
}

// 启动清理循环
func (em *ExpiryManager) start() {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.running {
		return
	}

	em.running = true

	// 设置时间轮的处理函数
	if em.timingWheel != nil {
		em.timingWheel.ProcessExpired = em.handleExpiredRecords

		// 启动时间轮
		em.timingWheel.Start()
	}

	// 启动统计循环
	em.wg.Add(1)
	go em.cleanupLoop()

	// 获取基础配置信息
	baseTick := em.timingWheel.GetBaseTick()
	wheelSize := em.timingWheel.GetWheelSize()
	levelCount := 0
	if em.timingWheel.levels != nil {
		levelCount = len(em.timingWheel.levels)
	}
	debugExpiryLog("✅ 全局到期管理器已启动（新多层时间轮）",
		"base_tick", baseTick.String(),
		"wheel_size", wheelSize,
		"levels", levelCount)
}

// 停止清理循环
func (em *ExpiryManager) Stop() {
	em.mu.Lock()
	defer em.mu.Unlock()

	if !em.running {
		return
	}

	// 停止时间轮
	if em.timingWheel != nil {
		em.timingWheel.Stop()
	}

	// 停止统计循环
	close(em.stopCh)
	em.wg.Wait()
	em.running = false
	debugExpiryLog("✅ 全局到期管理器已停止")
}

// 获取统计信息
func (em *ExpiryManager) GetStats() (totalRegistered, totalExpired, cleanupCalls, skippedCleanups int64, maxCleanupTime time.Duration) {
	return atomic.LoadInt64(&em.statTotalRegistered),
		atomic.LoadInt64(&em.statTotalExpired),
		atomic.LoadInt64(&em.statCleanupCalls),
		atomic.LoadInt64(&em.statSkippedCleanups),
		time.Duration(atomic.LoadInt64((*int64)(&em.statMaxCleanupTime)))
}

// 打印简要统计
func (em *ExpiryManager) PrintStatsBrief() {
	totalRegistered, totalExpired, cleanupCalls, skippedCleanups, maxCleanupTime := em.GetStats()
	debugExpiryLog("到期管理器简要统计",
		"总注册数", totalRegistered,
		"已过期数", totalExpired,
		"清理次数", cleanupCalls,
		"跳过次数", skippedCleanups,
		"最长处理时间", maxCleanupTime.Truncate(time.Millisecond).String())
}

// 打印统计信息
func (em *ExpiryManager) PrintStats() {
	totalRegistered, totalExpired, cleanupCalls, skippedCleanups, maxCleanupTime := em.GetStats()
	debugExpiryLog("到期管理器统计",
		"总注册数", totalRegistered,
		"已过期数", totalExpired,
		"清理次数", cleanupCalls,
		"跳过次数", skippedCleanups,
		"最长处理时间", maxCleanupTime.Truncate(time.Millisecond).String())
	// 打印时间轮统计
	if em.timingWheel != nil {
		em.timingWheel.PrintStats()
	}
}
