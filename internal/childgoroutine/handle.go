//go:build !windows
// +build !windows

package childgoroutine

import "fmt"

// ProcessHandle is not implemented on non‑Windows systems.
func (c *Child) ProcessHandle() (uintptr, error) {
	return 0, fmt.Errorf("ProcessHandle is only implemented on Windows")
}
