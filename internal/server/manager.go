package server

import (
	"bytes"
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type Peer struct {
	Hostname      string
	VirtualIP     string
	PublicIP      string
	PublicPort    int32
	PublicKey     []byte
	NatType       string
	LastHeartbeat time.Time
}

// PeerManager 并发安全的节点管理器
type PeerManager struct {
	mu       sync.RWMutex
	peers    map[string]*Peer
	Events   *EventBus
	Router   *SignalRouter
	onRemove func(virtualIP string) // 节点移除时的回调函数
}

func NewPeerManager(onRemove func(virtualIP string)) *PeerManager {
	return &PeerManager{
		peers:    make(map[string]*Peer),
		Events:   NewEventBus(),
		Router:   NewSignalRouter(),
		onRemove: onRemove,
	}
}

// AddOrUpdatePeer 添加或更新节点信息
func (m *PeerManager) AddOrUpdatePeer(hostname, virtualIP, publicIP string, publicPort int32, publicKey []byte, natType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.peers[virtualIP]
	if !exists {
		m.peers[virtualIP] = &Peer{
			Hostname:      hostname,
			VirtualIP:     virtualIP,
			PublicIP:      publicIP,
			PublicPort:    publicPort,
			PublicKey:     publicKey,
			NatType:       natType,
			LastHeartbeat: time.Now(),
		}
		m.Events.Notify()
		return
	}

	changed := p.PublicIP != publicIP || p.PublicPort != publicPort || !bytes.Equal(p.PublicKey, publicKey) || p.NatType != natType

	p.Hostname = hostname
	p.PublicIP = publicIP
	p.PublicPort = publicPort
	p.PublicKey = publicKey
	p.NatType = natType
	p.LastHeartbeat = time.Now()

	if changed {
		m.Events.Notify()
	}
}

// removePeerLocked 执行实际的节点清理逻辑，调用方需保证已持有写锁
func (m *PeerManager) removePeerLocked(virtualIP string) {
	delete(m.peers, virtualIP)
	m.Router.Unregister(virtualIP)
	if m.onRemove != nil {
		m.onRemove(virtualIP)
	}
}

// RemovePeer 移除节点
func (m *PeerManager) RemovePeer(virtualIP string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.peers[virtualIP]; exists {
		m.removePeerLocked(virtualIP)
		m.Events.Notify()
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
			PublicKey:  p.PublicKey,
			NatType:    p.NatType,
		})
	}
	return result
}

// CleanExpired 清理超时节点
func (m *PeerManager) CleanExpired(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	changed := false
	for virtualIP, p := range m.peers {
		if now.Sub(p.LastHeartbeat) > timeout {
			m.removePeerLocked(virtualIP)
			changed = true
		}
	}
	if changed {
		m.Events.Notify()
	}
}
