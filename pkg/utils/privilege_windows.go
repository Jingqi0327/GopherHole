//go:build windows

package utils

import "os"

// IsAdmin checks if the current process is running with root/administrator privileges.
func IsAdmin() bool {
	// Opening PhysicalDrive0 requires Administrator privileges on Windows.
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	if err != nil {
		return false
	}
	return true
}
