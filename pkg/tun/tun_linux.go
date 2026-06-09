//go:build linux

package tun

import (
	"fmt"
	"os/exec"

	"github.com/songgao/water"
)

// NewTunnel creates a TUN device on Linux and configures its IP and MTU.
func NewTunnel(name string, virtualIP string) (Tunnel, error) {
	config := water.Config{
		DeviceType: water.TUN,
	}
	config.Name = name

	ifce, err := water.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create tun device: %w", err)
	}

	actualName := ifce.Name()

	// ip link set dev <name> up
	if err := runCmd("ip", "link", "set", "dev", actualName, "up"); err != nil {
		return nil, err
	}

	// ip addr add <VirtualIP>/24 dev <name>
	ipWithMask := fmt.Sprintf("%s/24", virtualIP)
	if err := runCmd("ip", "addr", "add", ipWithMask, "dev", actualName); err != nil {
		return nil, err
	}

	// ip link set dev <name> mtu 1420
	if err := runCmd("ip", "link", "set", "dev", actualName, "mtu", "1420"); err != nil {
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
