package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrIPConflict = errors.New("requested IP is already in use")

// IPAM 简易的 IP 分配器
type IPAM struct {
	mu       sync.Mutex
	nextIP   int
	prefix   string
	usedIP   map[string]bool
	usedName map[string]bool
	ipToName map[string]string
}

// NewIPAM 创建一个新的 IP 分配器，prefix 示例: "10.8.0"
func NewIPAM(prefix string) *IPAM {
	return &IPAM{
		nextIP:   2, // 10.8.0.1 保留，从 .2 开始分配
		prefix:   prefix,
		usedIP:   make(map[string]bool),
		usedName: make(map[string]bool),
		ipToName: make(map[string]string),
	}
}

// Allocate 分配一个 IP 地址。如果 requested 不为空且未被占用，则优先分配。
func (i *IPAM) Allocate(reqName string, reqIP string) (string, string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var allocIp, allocName string

	if reqIP != "" {
		if i.usedIP[reqIP] {
			return "", "", ErrIPConflict
		}
		i.usedIP[reqIP] = true
		allocIp = reqIP
	} else {
		// 自动分配一个可用的
		for {
			ip := fmt.Sprintf("%s.%d", i.prefix, i.nextIP)
			i.nextIP++
			if i.nextIP > 254 {
				i.nextIP = 2 // MVP-1 简单循环处理
			}
			if !i.usedIP[ip] {
				i.usedIP[ip] = true
				allocIp = ip
				break
			}
		}
	}

	if reqName != "" {
		allocName = reqName
		counter := 2
		for i.usedName[allocName] {
			allocName = fmt.Sprintf("%s-%d", reqName, counter)
			counter++
		}
		i.usedName[allocName] = true
	} else {
		allocName = fmt.Sprintf("Client-%s", extractLastNum(allocIp))
		i.usedName[allocName] = true
	}

	i.ipToName[allocIp] = allocName
	
	return allocName, allocIp, nil
}

// Release 释放一个被占用的 IP
func (i *IPAM) Release(ip string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.usedIP, ip)
	delete(i.usedName, i.ipToName[ip])
	delete(i.ipToName, ip)
}

func extractLastNum(ip string) string {
	parts := strings.Split(ip, ".")
	return parts[len(parts)-1]
}
