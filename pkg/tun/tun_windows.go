//go:build windows

package tun

import (
	"fmt"
	"os/exec"

	"github.com/songgao/water"
)

// NewTunnel creates a TUN device on Windows and configures its IP and MTU.
func NewTunnel(name string, virtualIP string) (Tunnel, error) {
	config := water.Config{
		DeviceType: water.TUN,
	}
	
	config.PlatformSpecificParams = water.PlatformSpecificParams{
		ComponentID: "tap0901",
		// 传递本机的虚拟IP(10.8.0.100)和所在虚拟网段(/24),而不是直接传网段(10.8.0.0/24)
		// water在解析时会解析出两部分:网卡地址和子网掩码,若仅传网段，会导致底层驱动认为网卡地址为10.8.0.0
		// 从而导致驱动拦截并丢弃源/目的 IP 为本机的进出数据包（表现为可以建立UDP连接但双向 Ping 不通）。
		Network:     virtualIP + "/24", 
	}

	ifce, err := water.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create tun device: %w\n(请确保系统已安装 OpenVPN TAP-Windows6 虚拟网卡驱动)", err)
	}

	actualName := ifce.Name()

	// netsh interface ip set address name="<name>" static <VIP> 255.255.255.0
	if err := runCmd("netsh", "interface", "ip", "set", "address", "name="+actualName, "static", virtualIP, "255.255.255.0"); err != nil {
		return nil, err
	}

	// netsh interface ipv4 set subinterface "<name>" mtu=1420 store=persistent
	if err := runCmd("netsh", "interface", "ipv4", "set", "subinterface", actualName, "mtu=1420", "store=persistent"); err != nil {
		return nil, err
	}

	return ifce, nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("command %s %v failed: %w, output: %s", name, args, err, string(out))
	}
	return nil
}
