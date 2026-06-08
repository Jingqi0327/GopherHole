package client

import (
	"net"
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type PeerState int

const (
	StateDisconnected PeerState = iota
	StatePunching
	StateConnected
)

type PeerConnection struct {
	VirtualIP     string
	SignalingAddr *net.UDPAddr // Server 下发的参考地址（用于感知节点重启或网络切换）
	PublicAddr    *net.UDPAddr // 实际打通的公网地址（NAT 穿透后的真实端点）
	State         PeerState
	LastActive    time.Time
}

type PeerTable struct {
	mu    sync.RWMutex
	peers map[string]*PeerConnection
}

func NewPeerTable() *PeerTable {
	return &PeerTable{
		peers: make(map[string]*PeerConnection),
	}
}

// SyncPeers 全量同步 Server 下发的在线节点列表
func (pt *PeerTable) SyncPeers(onlinePeers []*pb.RemotePeer) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	onlineMap := make(map[string]*pb.RemotePeer)
	for _, p := range onlinePeers {
		onlineMap[p.VirtualIp] = p
	}

	// 1. 踢掉已经下线的节点
	for vip := range pt.peers {
		if _, ok := onlineMap[vip]; !ok {
			delete(pt.peers, vip)
		}
	}

	// 2. 更新或添加在线节点
	for vip, p := range onlineMap {
		sigAddr := &net.UDPAddr{
			IP:   net.ParseIP(p.PublicIp),
			Port: int(p.PublicPort),
		}

		existing, ok := pt.peers[vip]
		if !ok {
			// 新上线的节点
			pt.peers[vip] = &PeerConnection{
				VirtualIP:     vip,
				SignalingAddr: sigAddr,
				PublicAddr:    sigAddr, // 初始时，将信令地址作为预测的打洞地址
				State:         StateDisconnected,
			}
		} else {
			// 节点仍在运行，但如果信令服务器下发的端点发生了变化（IP变了或者绑定的本地UDP端口变了）
			// 这意味着对方客户端重启了，或者网络环境切换了。之前的打洞状态完全失效！
			if existing.SignalingAddr.String() != sigAddr.String() {
				existing.SignalingAddr = sigAddr
				existing.PublicAddr = sigAddr // 重置预测地址
				existing.State = StateDisconnected
			}
		}
	}
}

func (pt *PeerTable) GetPeer(virtualIP string) *PeerConnection {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if p, ok := pt.peers[virtualIP]; ok {
		// Return a copy
		return &PeerConnection{
			VirtualIP:     p.VirtualIP,
			SignalingAddr: p.SignalingAddr,
			PublicAddr:    p.PublicAddr,
			State:         p.State,
			LastActive:    p.LastActive,
		}
	}
	return nil
}

func (pt *PeerTable) UpdateState(virtualIP string, state PeerState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if p, ok := pt.peers[virtualIP]; ok {
		p.State = state
		p.LastActive = time.Now()
	}
}

// UpdateAddr updates the UDP address learned from actual UDP packets (NAT behavior)
func (pt *PeerTable) UpdateAddr(virtualIP string, addr *net.UDPAddr) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if p, ok := pt.peers[virtualIP]; ok {
		p.PublicAddr = addr
		p.LastActive = time.Now()
	}
}
