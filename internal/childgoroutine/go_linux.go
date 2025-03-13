//go:build linux

package childgoroutine

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/xtaci/smux"
)

// ---------------------------------------------------------------------
// Parent-Side: Go()
// ---------------------------------------------------------------------

// Go spawns a child process that will run the registered function given by name.
// It creates a full-duplex IPC channel over two pipes and wraps it in a smux session
// (acting as the smux server). The child process will receive its endpoints as
// inherited file descriptors, reassemble its net.Conn, and then wrap it with smux.Client.
func Go(name string, args string) (*Child, error) {
	parentConn, childRead, childWrite, err := createDuplexPipe()
	if err != nil {
		return nil, err
	}
	// Wrap the parent's connection in a smux server session.
	session, err := smux.Server(parentConn, smux.DefaultConfig())
	if err != nil {
		parentConn.Close()
		return nil, fmt.Errorf("failed to create smux server: %v", err)
	}

	// Use childExecutable() to decide which binary to spawn.
	exe, err := os.Executable()
	if err != nil {
		session.Close()
		return nil, err
	}

	// Pass command-line flags so that the child knows:
	//   • It is running in child mode.
	//   • Which registered function to run.
	//   • The file-descriptor numbers for its IPC channel.
	//
	// The ordering of Files in os.StartProcess causes the inherited files to be
	// assigned to FDs 3 and 4 in the child (after stdin=0, stdout=1, stderr=2).
	cmdArgs := []string{
		"--child",
		"--childName", name,
		"--ipcReadFD", "3",
		"--ipcWriteFD", "4",
		"--args", args,
	}

	// Prepare the command.
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Dir = ""
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Pass the extra files (they become FDs 3 and 4 in the child process).
	cmd.ExtraFiles = []*os.File{childRead, childWrite}

	// Start the child process.
	if err = cmd.Start(); err != nil {
		session.Close()
		childRead.Close()
		childWrite.Close()
		return nil, fmt.Errorf("failed to start child process: %v", err)
	}
	// The parent's copies of the child's pipe endpoints are no longer needed.
	childRead.Close()
	childWrite.Close()

	if err = cmd.Process.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to release child process: %v\n", err)
	}

	return &Child{
		Process: cmd.Process,
		Mux:     session,
	}, nil
}
