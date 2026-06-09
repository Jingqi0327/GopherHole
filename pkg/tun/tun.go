package tun

import (
	"fmt"
	"net"
)

// Tunnel represents a virtual network interface (TUN).
type Tunnel interface {
	Read(packet []byte) (int, error)
	Write(packet []byte) (int, error)
	Close() error
	Name() string
}

// getSubnet24 returns the /24 network address for a given IP.
// e.g. "10.8.0.2" -> "10.8.0.0/24"
func getSubnet24(virtualIP string) string {
	ip := net.ParseIP(virtualIP)
	if ip == nil || ip.To4() == nil {
		return "10.8.0.0/24" // Fallback
	}
	ip = ip.To4()
	return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
}
