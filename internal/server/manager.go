package server

import (
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type Peer struct {
	VirtualIP     string
	PublicIP      string
	PublicPort    int32
	LastHeartbeat time.Time
}

// PeerManager 并发安全的节点管理器
type PeerManager struct {
	mu          sync.RWMutex
	peers       map[string]*Peer
	subscribers map[chan struct{}]struct{} // 用于发布节点变动事件
	onRemove    func(virtualIP string)           // 节点移除时的回调函数
}

func NewPeerManager(onRemove func(virtualIP string)) *PeerManager {
	return &PeerManager{
		peers:       make(map[string]*Peer),
		subscribers: make(map[chan struct{}]struct{}),
		onRemove:    onRemove,
	}
}

// AddOrUpdatePeer 添加或更新节点信息
func (m *PeerManager) AddOrUpdatePeer(virtualIP, publicIP string, publicPort int32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.peers[virtualIP]
	if !exists {
		m.peers[virtualIP] = &Peer{
			VirtualIP:     virtualIP,
			PublicIP:      publicIP,
			PublicPort:    publicPort,
			LastHeartbeat: time.Now(),
		}
		m.notifyUpdate()
		return
	}

	p.PublicIP = publicIP
	p.PublicPort = publicPort
	p.LastHeartbeat = time.Now()
}

// RemovePeer 移除节点
func (m *PeerManager) RemovePeer(virtualIP string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.peers[virtualIP]; exists {
		delete(m.peers, virtualIP)
		if m.onRemove != nil {
			m.onRemove(virtualIP)
		}
		m.notifyUpdate()
	}
}

// GetAllPeers 获取所有在线节点
func (m *PeerManager) GetAllPeers() []*pb.RemotePeer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*pb.RemotePeer
	for _, p := range m.peers {
		result = append(result, &pb.RemotePeer{
			VirtualIp:  p.VirtualIP,
			PublicIp:   p.PublicIP,
			PublicPort: p.PublicPort,
		})
	}
	return result
}

// Subscribe 订阅节点更新事件
func (m *PeerManager) Subscribe() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan struct{}, 1)
	m.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe 取消订阅
func (m *PeerManager) Unsubscribe(ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subscribers, ch)
	close(ch)
}

func (m *PeerManager) notifyUpdate() {
	for ch := range m.subscribers {
		select {
		case ch <- struct{}{}:
		default: // 管道满了，说明上一次通知还没处理，直接跳过，保证不会阻塞
		}
	}
}

// CleanExpired 清理超时节点
func (m *PeerManager) CleanExpired(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	changed := false
	for virtualIP, p := range m.peers {
		if now.Sub(p.LastHeartbeat) > timeout {
			delete(m.peers, virtualIP)
			if m.onRemove != nil {
				m.onRemove(virtualIP)
			}
			changed = true
		}
	}
	if changed {
		m.notifyUpdate()
	}
}
