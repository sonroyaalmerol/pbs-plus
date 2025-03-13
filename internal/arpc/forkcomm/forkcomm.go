package forkcomm

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/xtaci/smux"
)

type Session struct {
	Pipe *smux.Session
	// Context for coordinating shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc

	cmd *exec.Cmd
}

func (s *Session) GetPID() int {
	if s.cmd == nil {
		return 0
	}

	return s.cmd.Process.Pid
}

func GetParentProcess() (*Session, error) {
	// Check if we're running as a child.
	if os.Getenv("FORK_SMUX_CHILD") != "1" {
		return nil, errors.New(
			"environment variable FORK_SMUX_CHILD not set; not running as a child",
		)
	}

	conn := &pipeConn{
		// os.Stdin and os.Stdout implement io.ReadCloser/io.WriteCloser.
		// Note: os.Stdin is an *os.File which already implements io.ReadCloser.
		reader: os.Stdin,
		writer: os.Stdout,
	}
	s, err := smux.Server(conn, smux.DefaultConfig())
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		ctx:        ctx,
		cancelFunc: cancel,
		Pipe:       s,
	}

	return session, nil
}

func CreateChildProcess(parentCtx context.Context, cmdPath string, args []string) (*Session, error) {
	ctx, cancel := context.WithCancel(parentCtx)

	// Prepare the environment for the child.
	env := os.Environ()
	env = append(env, "FORK_SMUX_CHILD=1")

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Env = env

	// Obtain child's stdout pipe (for reading) and stdin pipe (for writing).
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	// Optionally, connect child's stderr to parent's stderr.
	cmd.Stderr = os.Stderr

	// Start the child process.
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	// Wrap the pipes into a connection.
	conn := &pipeConn{reader: stdout, writer: stdin}
	s, err := smux.Client(conn, smux.DefaultConfig())
	if err != nil {
		cancel()
		return nil, err
	}

	session := &Session{
		ctx:        ctx,
		cancelFunc: cancel,
		Pipe:       s,
		cmd:        cmd,
	}

	return session, nil
}

func (s *Session) Close() {
	s.Pipe.Close()
	if s.cmd != nil {
		_ = s.cmd.Process.Kill()
	}
	s.cancelFunc()
}
