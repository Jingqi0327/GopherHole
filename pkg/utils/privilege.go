//go:build !windows

package utils

import "os"

// IsAdmin checks if the current process is running with root/administrator privileges.
func IsAdmin() bool {
	return os.Geteuid() == 0
}
