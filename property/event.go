// property/event.go
package property

import "time"

// PropChangeEvent 属性变更事件
type PropChangeEvent struct {
	ObjectID     int64
	PropID       int
	OldValue     uint32
	NewValue     uint32
	TypeForValue ValueType
	Timestamp    int64
	Source       SourceType
	Reason       string
}

// NewPropChangeEvent 创建属性变更事件
func NewPropChangeEvent(objectID int64, propID int, valueType ValueType, oldVal, newVal uint32, source SourceType) PropChangeEvent {
	return PropChangeEvent{
		ObjectID:     objectID,
		PropID:       propID,
		TypeForValue: valueType,
		OldValue:     oldVal,
		NewValue:     newVal,
		Timestamp:    time.Now().UnixMilli(),
		Source:       source,
	}
}
