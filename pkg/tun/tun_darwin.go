//go:build darwin

package tun

import (
	"fmt"
	"os/exec"

	"github.com/songgao/water"
)

// NewTunnel creates a TUN device on macOS and configures its IP and MTU.
func NewTunnel(name string, virtualIP string) (Tunnel, error) {
	config := water.Config{
		DeviceType: water.TUN,
	}

	ifce, err := water.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create tun device: %w", err)
	}

	actualName := ifce.Name()

	// ifconfig <name> inet <VirtualIP> <VirtualIP> up
	if err := runCmd("ifconfig", actualName, "inet", virtualIP, virtualIP, "up"); err != nil {
		return nil, err
	}

	// ifconfig <name> mtu 1420
	if err := runCmd("ifconfig", actualName, "mtu", "1420"); err != nil {
		return nil, err
	}

	// route add -net <network> -interface <name>
	network := getSubnet24(virtualIP)
	if err := runCmd("route", "add", "-net", network, "-interface", actualName); err != nil {
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
