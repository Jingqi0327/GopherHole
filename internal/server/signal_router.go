package server

import (
	"sync"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

// SignalRouter 管理 P2P 信令推送通道
type SignalRouter struct {
	mu       sync.RWMutex
	channels map[string]chan *pb.SignalMessage
}

// NewSignalRouter 创建一个新的 SignalRouter
func NewSignalRouter() *SignalRouter {
	return &SignalRouter{
		channels: make(map[string]chan *pb.SignalMessage),
	}
}

// Register 为指定节点注册一个专门的信令接收通道
func (r *SignalRouter) Register(virtualIP string) chan *pb.SignalMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan *pb.SignalMessage, 10)
	r.channels[virtualIP] = ch
	return ch
}

// Unregister 注销信令接收通道
func (r *SignalRouter) Unregister(virtualIP string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, exists := r.channels[virtualIP]; exists {
		delete(r.channels, virtualIP)
		close(ch)
	}
}

// Route 精准路由信令给目标节点
func (r *SignalRouter) Route(virtualIP string, msg *pb.SignalMessage) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, exists := r.channels[virtualIP]
	if !exists {
		return false
	}
	select {
	case ch <- msg:
		return true
	default:
		// 如果通道满，可能对方堵塞
		return false
	}
}
