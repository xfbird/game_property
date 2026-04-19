// property/network_sync.go
package property

import (
	"encoding/json"
	"fmt"
	"sync"
)

// NetworkSyncManager 网络同步管理器
type NetworkSyncManager struct {
	mu       sync.RWMutex
	clients  map[int64]bool // 连接的客户端
	encoder  *json.Encoder
	batching bool // 是否启用批量发送
	batch    []PropChangeEvent
	batchMu  sync.Mutex
}

// NewNetworkSyncManager 创建网络同步管理器
func NewNetworkSyncManager() *NetworkSyncManager {
	return &NetworkSyncManager{
		clients:  make(map[int64]bool),
		batching: true,
		batch:    make([]PropChangeEvent, 0, 100),
	}
}

// AddClient 添加客户端
func (mgr *NetworkSyncManager) AddClient(clientID int64) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.clients[clientID] = true
}

// RemoveClient 移除客户端
func (mgr *NetworkSyncManager) RemoveClient(clientID int64) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.clients, clientID)
}

// OnPropChanged 实现GlobalEventListener接口
func (mgr *NetworkSyncManager) OnPropChanged(event PropChangeEvent) {
	if mgr.batching {
		mgr.batchMu.Lock()
		mgr.batch = append(mgr.batch, event)

		// 批量达到阈值时发送
		if len(mgr.batch) >= 100 {
			mgr.sendBatch()
		}
		mgr.batchMu.Unlock()
	} else {
		// 立即发送
		mgr.sendEvent(event)
	}
}

// sendEvent 发送单个事件
func (mgr *NetworkSyncManager) sendEvent(event PropChangeEvent) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if len(mgr.clients) == 0 {
		return
	}

	// 构建消息
	msg := map[string]interface{}{
		"type": "prop_change",
		"data": map[string]interface{}{
			"object_id": event.ObjectID,
			"prop_id":   event.PropID,
			"old_value": event.OldValue,
			"new_value": event.NewValue,
			"source":    event.Source,
			"timestamp": event.Timestamp,
		},
	}

	// 序列化
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("序列化事件失败: %v\n", err)
		return
	}

	// 发送给所有客户端（模拟）
	for clientID := range mgr.clients {
		mgr.sendToClient(clientID, data)
	}
}

// sendBatch 发送批量事件
func (mgr *NetworkSyncManager) sendBatch() {
	if len(mgr.batch) == 0 {
		return
	}

	mgr.batchMu.Lock()
	batch := make([]PropChangeEvent, len(mgr.batch))
	copy(batch, mgr.batch)
	mgr.batch = mgr.batch[:0]
	mgr.batchMu.Unlock()

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if len(mgr.clients) == 0 {
		return
	}

	// 构建批量消息
	msg := map[string]interface{}{
		"type": "prop_batch",
		"data": batch,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("序列化批量事件失败: %v\n", err)
		return
	}

	for clientID := range mgr.clients {
		mgr.sendToClient(clientID, data)
	}
}

// sendToClient 发送数据到客户端（模拟）
func (mgr *NetworkSyncManager) sendToClient(clientID int64, data []byte) {
	// 模拟网络发送
	// 在实际系统中，这里会调用网络库发送数据
	fmt.Printf("[网络] 发送到客户端%d: %s\n", clientID, string(data))
}

// Flush 强制刷新批量数据
func (mgr *NetworkSyncManager) Flush() {
	mgr.batchMu.Lock()
	if len(mgr.batch) > 0 {
		mgr.sendBatch()
	}
	mgr.batchMu.Unlock()
}
