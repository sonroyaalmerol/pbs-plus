//go:build windows

package childgoroutine

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// createDuplexPipe creates a full-duplex connection by composing two unidirectional pipes.
// It returns parent's net.Conn and the two *os.File handles (child's read and write) that
// should be passed to the child process.
func createDuplexPipe() (net.Conn, *os.File, *os.File, error) {
	// Pipe 1: parent's read end receives what the child writes.
	pr1, pw1, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create pipe1: %v", err)
	}
	// Pipe 2: parent's write end sends data that the child reads.
	pr2, pw2, err := os.Pipe()
	if err != nil {
		pr1.Close()
		pw1.Close()
		return nil, nil, nil, fmt.Errorf("failed to create pipe2: %v", err)
	}
	// Parent's full-duplex connection: read from pr1, write to pw2.
	parentConn := newPipeDuplex(pr1, pw2)

	// For the child, reassemble its full-duplex connection
	// from pr2 (child's read) and pw1 (child's write).
	// On Windows, mark these file handles as inheritable.
	err = syscall.SetHandleInformation(syscall.Handle(pr2.Fd()),
		syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT)
	if err != nil {
		pr1.Close()
		pw1.Close()
		pr2.Close()
		pw2.Close()
		return nil, nil, nil,
			fmt.Errorf("failed to mark pr2 as inheritable: %v", err)
	}
	err = syscall.SetHandleInformation(syscall.Handle(pw1.Fd()),
		syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT)
	if err != nil {
		pr1.Close()
		pw1.Close()
		pr2.Close()
		pw2.Close()
		return nil, nil, nil,
			fmt.Errorf("failed to mark pw1 as inheritable: %v", err)
	}

	// Return parent's connection plus the child's FDs.
	return parentConn, pr2, pw1, nil
}
