package server

import (
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type Peer struct {
	Hostname      string
	VirtualIP     string
	PublicIP      string
	PublicPort    int32
	LastHeartbeat time.Time
}

// PeerManager 并发安全的节点管理器
type PeerManager struct {
	mu          sync.RWMutex
	peers       map[string]*Peer
	subscribers map[chan struct{}]struct{}        // 用于发布节点变动事件
	signalChans map[string]chan *pb.SignalMessage // 针对每个虚拟IP的信令推送通道
	onRemove    func(virtualIP string)            // 节点移除时的回调函数
}

func NewPeerManager(onRemove func(virtualIP string)) *PeerManager {
	return &PeerManager{
		peers:       make(map[string]*Peer),
		subscribers: make(map[chan struct{}]struct{}),
		signalChans: make(map[string]chan *pb.SignalMessage),
		onRemove:    onRemove,
	}
}

// AddOrUpdatePeer 添加或更新节点信息
func (m *PeerManager) AddOrUpdatePeer(hostname, virtualIP, publicIP string, publicPort int32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.peers[virtualIP]
	if !exists {
		m.peers[virtualIP] = &Peer{
			Hostname:      hostname,
			VirtualIP:     virtualIP,
			PublicIP:      publicIP,
			PublicPort:    publicPort,
			LastHeartbeat: time.Now(),
		}
		m.notifyUpdate()
		return
	}

	p.Hostname = hostname
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
		if ch, ok := m.signalChans[virtualIP]; ok {
			delete(m.signalChans, virtualIP)
			close(ch)
		}
		if m.onRemove != nil {
			m.onRemove(virtualIP)
		}
		m.notifyUpdate()
	}
}

// HasPeer 检查是否存在对应节点
func (m *PeerManager) HasPeer(virtualIP string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.peers[virtualIP]
	return exists
}

// GetAllPeers 获取所有在线节点
func (m *PeerManager) GetAllPeers() []*pb.RemotePeer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*pb.RemotePeer
	for _, p := range m.peers {
		result = append(result, &pb.RemotePeer{
			Hostname:   p.Hostname,
			VirtualIp:  p.VirtualIP,
			PublicIp:   p.PublicIP,
			PublicPort: p.PublicPort,
		})
	}
	return result
}

// RegisterSignalChannel 为指定节点注册一个专门的信令接收通道
func (m *PeerManager) RegisterSignalChannel(virtualIP string) chan *pb.SignalMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan *pb.SignalMessage, 10)
	m.signalChans[virtualIP] = ch
	return ch
}

// UnregisterSignalChannel 注销信令接收通道
func (m *PeerManager) UnregisterSignalChannel(virtualIP string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, exists := m.signalChans[virtualIP]; exists {
		delete(m.signalChans, virtualIP)
		close(ch)
	}
}

// RouteSignal 精准路由信令给目标节点
func (m *PeerManager) RouteSignal(virtualIP string, msg *pb.SignalMessage) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, exists := m.signalChans[virtualIP]
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
			if ch, ok := m.signalChans[virtualIP]; ok {
				delete(m.signalChans, virtualIP)
				close(ch)
			}
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
