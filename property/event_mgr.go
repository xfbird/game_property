// property/event_mgr.go
package property

import (
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// PropChangeCallback 属性变更回调函数类型
type PropChangeCallback func(event PropChangeEvent)

// EventFilter 事件过滤器
type EventFilter struct {
	ObjectID   int64
	PropID     int
	PropIdent  string
	SourceType SourceType
}

// Match 检查事件是否匹配过滤器
func (f *EventFilter) Match(event PropChangeEvent, defTable *PropDefTable) bool {
	if f.ObjectID != 0 && f.ObjectID != event.ObjectID {
		return false
	}

	if f.PropID != 0 && f.PropID != event.PropID {
		return false
	}

	if f.PropIdent != "" && defTable != nil {
		id, ok := defTable.GetIDByIdent(f.PropIdent)
		if !ok || id != event.PropID {
			return false
		}
	}

	if f.SourceType != 0 && f.SourceType != event.Source {
		return false
	}

	return true
}

// 事件处理器ID生成器
var nextHandlerID int64 = 1

// EventHandler 事件处理器
type EventHandler struct {
	ID       int64
	Callback PropChangeCallback
	Filter   EventFilter
	Priority int
}

// GlobalEventListener 全局监听器接口
type GlobalEventListener interface {
	OnPropChanged(event PropChangeEvent)
}

// globalListenerFunc 函数类型包装
type globalListenerFunc func(PropChangeEvent)

func (f globalListenerFunc) OnPropChanged(event PropChangeEvent) {
	f(event)
}

// DynamicEventQueue 动态扩容的事件队列
type DynamicEventQueue struct {
	mu sync.RWMutex

	queue chan PropChangeEvent

	minSize      int
	maxSize      int
	growFactor   float64
	shrinkFactor float64

	stats struct {
		totalEnqueued  int64
		totalDropped   int64
		totalExpanded  int64
		totalShrunk    int64
		maxQueueSize   int
		avgEnqueueTime time.Duration
		lastShrinkTime time.Time
	}

	resizing   int32
	lastResize time.Time
}

// NewDynamicEventQueue 创建动态事件队列
func NewDynamicEventQueue(initialSize int) *DynamicEventQueue {
	if initialSize <= 0 {
		initialSize = 1000
	}

	return &DynamicEventQueue{
		queue:        make(chan PropChangeEvent, initialSize),
		minSize:      100,
		maxSize:      100000,
		growFactor:   1.5,
		shrinkFactor: 0.7,
	}
}

// Enqueue 入队事件，支持动态扩容
func (q *DynamicEventQueue) Enqueue(event PropChangeEvent) bool {
	startTime := time.Now()

	select {
	case q.queue <- event:
		q.updateStats(true, false, time.Since(startTime))
		return true
	default:
		if q.tryExpand() {
			select {
			case q.queue <- event:
				q.updateStats(true, true, time.Since(startTime))
				return true
			default:
				q.updateStats(false, false, time.Since(startTime))
				return false
			}
		}
		q.updateStats(false, false, time.Since(startTime))
		return false
	}
}

// tryExpand 尝试扩容队列
func (q *DynamicEventQueue) tryExpand() bool {
	if !atomic.CompareAndSwapInt32(&q.resizing, 0, 1) {
		return false
	}
	defer atomic.StoreInt32(&q.resizing, 0)

	if time.Since(q.lastResize) < 100*time.Millisecond {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	currentCap := cap(q.queue)
	if currentCap >= q.maxSize {
		return false
	}

	newCap := int(float64(currentCap) * q.growFactor)
	if newCap > q.maxSize {
		newCap = q.maxSize
	}
	if newCap <= currentCap {
		newCap = currentCap + 1
	}

	newQueue := make(chan PropChangeEvent, newCap)
	migrated := 0
	for {
		select {
		case event := <-q.queue:
			select {
			case newQueue <- event:
				migrated++
			default:
				q.queue <- event
			}
		default:
			goto migrationDone
		}
	}

migrationDone:
	q.queue = newQueue
	q.lastResize = time.Now()
	atomic.AddInt64(&q.stats.totalExpanded, 1)

	slog.Info("事件队列已扩容",
		"old_capacity", currentCap,
		"new_capacity", newCap,
		"migrated_events", migrated)

	return true
}

// tryShrink 尝试收缩队列
func (q *DynamicEventQueue) tryShrink() {
	if !atomic.CompareAndSwapInt32(&q.resizing, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&q.resizing, 0)

	if time.Since(q.lastResize) < 5*time.Second {
		return
	}

	if time.Since(q.stats.lastShrinkTime) < time.Minute {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	currentCap := cap(q.queue)
	if currentCap <= q.minSize {
		return
	}

	queueLen := len(q.queue)
	utilization := float64(queueLen) / float64(currentCap)

	if utilization < 0.3 && currentCap > q.minSize*2 {
		newCap := int(float64(currentCap) * q.shrinkFactor)
		if newCap < q.minSize {
			newCap = q.minSize
		}

		if newCap < queueLen {
			newCap = queueLen + 100
		}

		if newCap < currentCap {
			newQueue := make(chan PropChangeEvent, newCap)
			migrated := 0
			for i := 0; i < queueLen; i++ {
				select {
				case event := <-q.queue:
					select {
					case newQueue <- event:
						migrated++
					default:
						q.mu.Unlock()
						return
					}
				default:
					break
				}
			}

			q.queue = newQueue
			q.lastResize = time.Now()
			q.stats.lastShrinkTime = time.Now()
			atomic.AddInt64(&q.stats.totalShrunk, 1)

			slog.Info("事件队列已收缩",
				"old_capacity", currentCap,
				"new_capacity", newCap,
				"utilization", utilization,
				"migrated_events", migrated)
		}
	}
}

// updateStats 更新统计信息
func (q *DynamicEventQueue) updateStats(success, expanded bool, latency time.Duration) {
	if success {
		atomic.AddInt64(&q.stats.totalEnqueued, 1)
		atomic.StoreInt64((*int64)(&q.stats.avgEnqueueTime),
			int64((time.Duration(atomic.LoadInt64((*int64)(&q.stats.avgEnqueueTime))*9)+latency)/10))
		queueLen := len(q.queue)
		if queueLen > q.stats.maxQueueSize {
			q.stats.maxQueueSize = queueLen
		}
	} else {
		atomic.AddInt64(&q.stats.totalDropped, 1)
	}

	if expanded {
		atomic.AddInt64(&q.stats.totalExpanded, 1)
	}
}

// GetStats 获取队列统计
func (q *DynamicEventQueue) GetStats() map[string]interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return map[string]interface{}{
		"capacity":         cap(q.queue),
		"length":           len(q.queue),
		"total_enqueued":   atomic.LoadInt64(&q.stats.totalEnqueued),
		"total_dropped":    atomic.LoadInt64(&q.stats.totalDropped),
		"total_expanded":   atomic.LoadInt64(&q.stats.totalExpanded),
		"total_shrunk":     atomic.LoadInt64(&q.stats.totalShrunk),
		"max_queue_size":   q.stats.maxQueueSize,
		"avg_enqueue_time": time.Duration(atomic.LoadInt64((*int64)(&q.stats.avgEnqueueTime))).String(),
		"utilization":      float64(len(q.queue)) / float64(cap(q.queue)),
	}
}

// EventManagerStats 事件管理器统计信息
type EventManagerStats struct {
	SkippedNoListener int64
	Enqueued          int64
	Dequeued          int64
	Dropped           int64
}

// EventManager 事件管理器
type EventManager struct {
	mu sync.RWMutex

	handlers    map[int][]*EventHandler
	allHandlers []*EventHandler

	globalListener GlobalEventListener
	globalPriority int

	enabled  int32
	queue    *DynamicEventQueue
	stopChan chan struct{}
	wg       sync.WaitGroup

	defTable *PropDefTable
	shrinkTicker *time.Ticker

	stats   EventManagerStats
	statsMu sync.RWMutex
}

// NewEventManager 创建事件管理器
func NewEventManager(initialBufferSize int) *EventManager {
	if initialBufferSize <= 0 {
		initialBufferSize = 1000
	}

	mgr := &EventManager{
		handlers:       make(map[int][]*EventHandler),
		allHandlers:    make([]*EventHandler, 0),
		enabled:        1,
		queue:          NewDynamicEventQueue(initialBufferSize),
		stopChan:       make(chan struct{}),
		globalPriority: 0,
		shrinkTicker:   time.NewTicker(30 * time.Second),
		stats:          EventManagerStats{},
	}

	mgr.wg.Add(2)
	go mgr.processEvents()
	go mgr.shrinkChecker()

	slog.Info("事件管理器已创建",
		"initial_buffer_size", initialBufferSize)

	return mgr
}

// shrinkChecker 定期检查是否需要收缩队列
func (mgr *EventManager) shrinkChecker() {
	defer mgr.wg.Done()

	for {
		select {
		case <-mgr.shrinkTicker.C:
			mgr.queue.tryShrink()
		case <-mgr.stopChan:
			return
		}
	}
}

// GetDefTable 获取属性定义表
func (mgr *EventManager) GetDefTable() *PropDefTable {
	return mgr.defTable
}

// SetDefTable 设置属性定义表
func (mgr *EventManager) SetDefTable(defTable *PropDefTable) {
	mgr.defTable = defTable
}

// Enable 启用事件管理器
func (mgr *EventManager) Enable() {
	atomic.StoreInt32(&mgr.enabled, 1)
	slog.Debug("事件管理器已启用")
}

// Disable 禁用事件管理器
func (mgr *EventManager) Disable() {
	atomic.StoreInt32(&mgr.enabled, 0)
	slog.Debug("事件管理器已禁用")
}

// IsEnabled 检查是否启用
func (mgr *EventManager) IsEnabled() bool {
	return atomic.LoadInt32(&mgr.enabled) == 1
}

// SetGlobalListener 设置全局监听者
func (mgr *EventManager) SetGlobalListener(listener GlobalEventListener) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.globalListener = listener

	if listener != nil {
		slog.Info("设置全局监听者")
	} else {
		slog.Debug("移除全局监听者")
	}
}

// SetGlobalListenerFunc 设置全局监听者
func (mgr *EventManager) SetGlobalListenerFunc(callback PropChangeCallback) {
	mgr.SetGlobalListener(globalListenerFunc(callback))
}

// Register 注册事件处理器
func (mgr *EventManager) Register(callback PropChangeCallback, filter EventFilter, priority int) int64 {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	id := atomic.AddInt64(&nextHandlerID, 1)
	handler := &EventHandler{
		ID:       id,
		Callback: callback,
		Filter:   filter,
		Priority: priority,
	}

	if filter.PropIdent != "" && mgr.defTable != nil {
		if propID, ok := mgr.defTable.GetIDByIdent(filter.PropIdent); ok {
			filter.PropID = propID
			handler.Filter = filter
		}
	}

	if filter.PropID == 0 {
		mgr.allHandlers = append(mgr.allHandlers, handler)
		slog.Debug("注册全局事件处理器",
			"handler_id", id,
			"priority", priority)
	} else {
		mgr.handlers[filter.PropID] = append(mgr.handlers[filter.PropID], handler)
		slog.Debug("注册属性事件处理器",
			"handler_id", id,
			"prop_id", filter.PropID,
			"priority", priority)
	}

	return id
}

// RegisterForProp 为特定属性注册处理器
func (mgr *EventManager) RegisterForProp(propID int, callback PropChangeCallback, priority int) int64 {
	slog.Debug("注册属性事件处理器",
		"prop_id", propID,
		"priority", priority)

	return mgr.Register(callback, EventFilter{PropID: propID}, priority)
}

// RegisterForPropIdent 为特定属性标识符注册处理器
func (mgr *EventManager) RegisterForPropIdent(ident string, callback PropChangeCallback, priority int) int64 {
	slog.Debug("注册标识符事件处理器",
		"ident", ident,
		"priority", priority)

	return mgr.Register(callback, EventFilter{PropIdent: ident}, priority)
}

// Unregister 注销处理器
func (mgr *EventManager) Unregister(handlerID int64) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	found := false
	for propID, handlers := range mgr.handlers {
		newHandlers := make([]*EventHandler, 0, len(handlers))
		for _, h := range handlers {
			if h.ID != handlerID {
				newHandlers = append(newHandlers, h)
			} else {
				found = true
			}
		}
		mgr.handlers[propID] = newHandlers
	}

	newAllHandlers := make([]*EventHandler, 0, len(mgr.allHandlers))
	for _, h := range mgr.allHandlers {
		if h.ID != handlerID {
			newAllHandlers = append(newAllHandlers, h)
		} else {
			found = true
		}
	}
	mgr.allHandlers = newAllHandlers

	if found {
		slog.Debug("注销事件处理器",
			"handler_id", handlerID)
	}
}

// TriggerEvent 触发事件
func (mgr *EventManager) TriggerEvent(event PropChangeEvent) {
	if atomic.LoadInt32(&mgr.enabled) == 0 {
		return
	}

	slog.Debug("🎯 触发属性变更事件",
		"object_id", event.ObjectID,
		"prop_id", event.PropID,
		"old_value", event.OldValue,
		"new_value", event.NewValue,
		"source_type", event.Source)

	mgr.mu.RLock()
	globalListener := mgr.globalListener
	handlers := make([]*EventHandler, 0)
	if propHandlers, ok := mgr.handlers[event.PropID]; ok {
		handlers = append(handlers, propHandlers...)
	}
	handlers = append(handlers, mgr.allHandlers...)
	mgr.mu.RUnlock()

	if len(handlers) > 0 {
		slog.Debug("找到事件处理器",
			"prop_id", event.PropID,
			"handler_count", len(handlers),
			"global_listener_exists", globalListener != nil)
	} else {
		slog.Debug("没有找到事件处理器",
			"prop_id", event.PropID)
	}

	if globalListener != nil {
		slog.Debug("执行全局监听器",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
		globalListener.OnPropChanged(event)
	}

	mgr.sortHandlers(handlers)
	matchedCount := 0
	for _, handler := range handlers {
		if handler.Filter.Match(event, mgr.defTable) {
			slog.Debug("执行属性事件处理器",
				"handler_id", handler.ID,
				"prop_id", event.PropID,
				"priority", handler.Priority)
			handler.Callback(event)
			matchedCount++
		}
	}

	if matchedCount > 0 {
		slog.Debug("事件处理完成",
			"prop_id", event.PropID,
			"matched_handlers", matchedCount)
	}
}

// sortHandlers 按优先级排序
func (mgr *EventManager) sortHandlers(handlers []*EventHandler) {
	sort.SliceStable(handlers, func(i, j int) bool {
		return handlers[i].Priority < handlers[j].Priority
	})
}

// QueueEvent 队列事件
func (mgr *EventManager) QueueEvent(event PropChangeEvent) bool {
	if atomic.LoadInt32(&mgr.enabled) == 0 {
		slog.Debug("事件管理器被禁用，跳过事件入队",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
		return false
	}

	slog.Debug("📥 事件入队",
		"object_id", event.ObjectID,
		"prop_id", event.PropID,
		"old_value", event.OldValue,
		"new_value", event.NewValue,
		"source", event.Source,
		"timestamp", time.Now().Format("15:04:05.000"))

	if !mgr.HasListenerForProp(event.PropID) && mgr.globalListener == nil {
		mgr.statsMu.Lock()
		mgr.stats.SkippedNoListener++
		mgr.statsMu.Unlock()
		
		slog.Debug("属性无监听者，跳过事件入队",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
		return false
	}

	success := mgr.queue.Enqueue(event)

	if success {
		mgr.statsMu.Lock()
		mgr.stats.Enqueued++
		mgr.statsMu.Unlock()
		
		slog.Debug("事件成功入队",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
	} else {
		mgr.statsMu.Lock()
		mgr.stats.Dropped++
		mgr.statsMu.Unlock()
		
		slog.Warn("事件队列已满，丢弃事件",
			"object_id", event.ObjectID,
			"prop_id", event.PropID)
	}

	return success
}

// processEvents 处理事件队列
func (mgr *EventManager) processEvents() {
	defer mgr.wg.Done()

	slog.Debug("事件处理循环已启动")
	processedCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case event := <-mgr.queue.queue:
			slog.Debug("📤 从队列取出事件开始处理",
				"object_id", event.ObjectID,
				"prop_id", event.PropID,
				"queue_size", len(mgr.queue.queue),
				"timestamp", time.Now().Format("15:04:05.000"))

			mgr.TriggerEvent(event)
			processedCount++
			
			mgr.statsMu.Lock()
			mgr.stats.Dequeued++
			mgr.statsMu.Unlock()

			if processedCount%1000 == 0 {
				slog.Info("事件处理统计",
					"processed_count", processedCount,
					"queue_length", len(mgr.queue.queue),
					"queue_capacity", cap(mgr.queue.queue))
			}

		case <-mgr.stopChan:
			slog.Info("事件处理循环已停止",
				"total_processed", processedCount)
			return
		}

		if time.Since(lastLogTime) >= 5*time.Second {
			queueLen := len(mgr.queue.queue)
			if queueLen > 0 {
				slog.Info("事件队列状态",
					"processed_count", processedCount,
					"queue_length", queueLen,
					"queue_capacity", cap(mgr.queue.queue))
			}
			lastLogTime = time.Now()
		}
	}
}

// Stop 停止事件管理器
func (mgr *EventManager) Stop() {
	slog.Info("停止事件管理器")

	mgr.Disable()
	mgr.shrinkTicker.Stop()
	close(mgr.stopChan)
	mgr.wg.Wait()

	slog.Info("事件管理器已停止")
	stats := mgr.queue.GetStats()
	slog.Info("事件队列最终统计",
		"total_enqueued", stats["total_enqueued"],
		"total_dropped", stats["total_dropped"],
		"total_expanded", stats["total_expanded"],
		"max_queue_size", stats["max_queue_size"],
		"final_capacity", stats["capacity"])
		
	mgrStats := mgr.GetStats()
	slog.Info("事件管理器统计",
		"skipped_no_listener", mgrStats.SkippedNoListener,
		"enqueued", mgrStats.Enqueued,
		"dequeued", mgrStats.Dequeued,
		"dropped", mgrStats.Dropped)
}

// GetStats 获取事件管理器统计信息
func (mgr *EventManager) GetStats() EventManagerStats {
	mgr.statsMu.RLock()
	defer mgr.statsMu.RUnlock()
	
	return EventManagerStats{
		SkippedNoListener: mgr.stats.SkippedNoListener,
		Enqueued:          mgr.stats.Enqueued,
		Dequeued:          mgr.stats.Dequeued,
		Dropped:           mgr.stats.Dropped,
	}
}

// GetQueueStats 获取队列统计
func (mgr *EventManager) GetQueueStats() map[string]interface{} {
	return mgr.queue.GetStats()
}

// HasListenerForProp 检查属性是否有监听者
func (mgr *EventManager) HasListenerForProp(propID int) bool {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.globalListener != nil {
		return true
	}

	if handlers, ok := mgr.handlers[propID]; ok && len(handlers) > 0 {
		return true
	}

	for _, handler := range mgr.allHandlers {
		if handler.Filter.PropID == 0 || handler.Filter.PropID == propID {
			return true
		}
	}

	return false
}

// HasAnyListener 检查是否有任何监听者
func (mgr *EventManager) HasAnyListener() bool {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.globalListener != nil {
		return true
	}

	if len(mgr.handlers) > 0 {
		return true
	}

	if len(mgr.allHandlers) > 0 {
		return true
	}

	return false
}