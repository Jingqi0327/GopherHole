package server

import (
	"errors"
	"fmt"
	"sync"
)

var ErrIPConflict = errors.New("requested IP is already in use")

// IPAM 简易的 IP 分配器
type IPAM struct {
	mu     sync.Mutex
	nextIP int
	prefix string
	used   map[string]bool
}

// NewIPAM 创建一个新的 IP 分配器，prefix 示例: "10.8.0"
func NewIPAM(prefix string) *IPAM {
	return &IPAM{
		nextIP: 2, // 10.8.0.1 保留，从 .2 开始分配
		prefix: prefix,
		used:   make(map[string]bool),
	}
}

// Allocate 分配一个 IP 地址。如果 requested 不为空且未被占用，则优先分配。
func (i *IPAM) Allocate(requested string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if requested != "" {
		if i.used[requested] {
			return "", ErrIPConflict
		}
		i.used[requested] = true
		return requested, nil
	}

	// 自动分配一个可用的
	for {
		ip := fmt.Sprintf("%s.%d", i.prefix, i.nextIP)
		i.nextIP++
		if i.nextIP > 254 {
			i.nextIP = 2 // MVP-1 简单循环处理
		}
		if !i.used[ip] {
			i.used[ip] = true
			return ip, nil
		}
	}
}

// Release 释放一个被占用的 IP
func (i *IPAM) Release(ip string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.used, ip)
}
