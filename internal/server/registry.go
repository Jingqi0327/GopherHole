package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrIPConflict = errors.New("requested IP is already in use")

// Registry 简易的 IP 和名称注册表
type Registry struct {
	mu       sync.Mutex
	nextIP   int
	prefix   string
	usedIP   map[string]bool
	usedName map[string]bool
	ipToName map[string]string
}

// NewRegistry 创建一个新的注册表，prefix 示例: "10.8.0"
func NewRegistry(prefix string) *Registry {
	return &Registry{
		nextIP:   2, // 10.8.0.1 保留，从 .2 开始分配
		prefix:   prefix,
		usedIP:   make(map[string]bool),
		usedName: make(map[string]bool),
		ipToName: make(map[string]string),
	}
}

// Allocate 分配一个 IP 地址。如果 requested 不为空且未被占用，则优先分配。
func (r *Registry) Allocate(reqName string, reqIP string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var allocIp, allocName string

	if reqIP != "" {
		if r.usedIP[reqIP] {
			return "", "", ErrIPConflict
		}
		r.usedIP[reqIP] = true
		allocIp = reqIP
	} else {
		// 自动分配一个可用的
		for {
			ip := fmt.Sprintf("%s.%d", r.prefix, r.nextIP)
			r.nextIP++
			if r.nextIP > 254 {
				r.nextIP = 2 // MVP-1 简单循环处理
			}
			if !r.usedIP[ip] {
				r.usedIP[ip] = true
				allocIp = ip
				break
			}
		}
	}

	if reqName != "" {
		allocName = reqName
		counter := 2
		for r.usedName[allocName] {
			allocName = fmt.Sprintf("%s-%d", reqName, counter)
			counter++
		}
		r.usedName[allocName] = true
	} else {
		allocName = fmt.Sprintf("Client-%s", extractLastNum(allocIp))
		r.usedName[allocName] = true
	}

	r.ipToName[allocIp] = allocName
	
	return allocName, allocIp, nil
}

// Release 释放一个被占用的 IP 和 name
func (r *Registry) Release(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.usedIP, ip)
	delete(r.usedName, r.ipToName[ip])
	delete(r.ipToName, ip)
}

func extractLastNum(ip string) string {
	parts := strings.Split(ip, ".")
	return parts[len(parts)-1]
}
