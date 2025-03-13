//go:build windows

package childgoroutine

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// ProcessHandle returns a duplicate handle to the child process on Windows.
// The caller is responsible for closing the returned handle (e.g., by calling
// windows.CloseHandle). On non‑Windows systems this function is not available.
func (c *Child) ProcessHandle() (windows.Handle, error) {
	// This check is extra defensive.
	if runtime.GOOS != "windows" {
		return 0, fmt.Errorf("ProcessHandle is only available on Windows")
	}

	targetProcess, err := windows.OpenProcess(
		windows.PROCESS_DUP_HANDLE,
		false,
		uint32(c.Process.Pid),
	)
	if err != nil {
		return 0, os.ErrInvalid
	}
	return targetProcess, nil
}
