package server

import "sync"

// EventBus 处理节点变动事件的发布与订阅
type EventBus struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

// NewEventBus 创建一个新的 EventBus
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan struct{}]struct{}),
	}
}

// Subscribe 订阅节点更新事件
func (b *EventBus) Subscribe() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan struct{}, 1)
	b.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe 取消订阅
func (b *EventBus) Unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, ch)
	close(ch)
}

// Notify 通知所有订阅者
func (b *EventBus) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- struct{}{}:
		default: // 管道满了，说明上一次通知还没处理，直接跳过，保证不会阻塞
		}
	}
}
