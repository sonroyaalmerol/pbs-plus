package forkcomm

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

// ======================================================================
// Dummy types for testing pipeConn
// ======================================================================

// nopWriteCloser wraps an io.Writer so that it implements io.WriteCloser.
type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error {
	return nil
}

// ======================================================================
// Test methods of pipeConn.
// ======================================================================

func TestPipeConnMethods(t *testing.T) {
	// Prepare a dummy reader that already contains some content.
	reader := io.NopCloser(bytes.NewBufferString("hello"))
	// Prepare a dummy writer using a bytes.Buffer.
	writerBuffer := &bytes.Buffer{}
	writer := nopWriteCloser{writerBuffer}

	pc := &pipeConn{
		reader: reader,
		writer: writer,
	}

	// Test LocalAddr and RemoteAddr.
	if pc.LocalAddr().String() != "pipe" {
		t.Errorf("LocalAddr expected 'pipe', got %s", pc.LocalAddr().String())
	}
	if pc.RemoteAddr().String() != "pipe" {
		t.Errorf("RemoteAddr expected 'pipe', got %s", pc.RemoteAddr().String())
	}

	// Test that deadline-related methods return no error.
	now := time.Now()
	if err := pc.SetDeadline(now); err != nil {
		t.Errorf("SetDeadline returned error: %v", err)
	}
	if err := pc.SetReadDeadline(now); err != nil {
		t.Errorf("SetReadDeadline returned error: %v", err)
	}
	if err := pc.SetWriteDeadline(now); err != nil {
		t.Errorf("SetWriteDeadline returned error: %v", err)
	}

	// Test Write: write "world" and check that it ends up in our writer buffer.
	n, err := pc.Write([]byte("world"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected Write to write 5 bytes, got %d", n)
	}
	if writerBuffer.String() != "world" {
		t.Errorf("writer buffer contains %q, expected %q", writerBuffer.String(), "world")
	}

	// Test Read: read the content from our dummy reader.
	buf := make([]byte, 5)
	n, err = pc.Read(buf)
	// io.EOF is acceptable if we exhaust the buffer.
	if err != nil && err != io.EOF {
		t.Fatalf("Read returned error: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read got %q, expected %q", string(buf[:n]), "hello")
	}

	// Test Close: both the reader and writer should be closed.
	if err := pc.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// ======================================================================
// Test GetParentProcess (should fail when the env variable is not set)
// ======================================================================

func TestGetParentProcessEnvNotSet(t *testing.T) {
	// Ensure the environment variable is not set.
	os.Unsetenv("FORK_SMUX_CHILD")
	_, err := GetParentProcess()
	if err == nil {
		t.Error("expected error when FORK_SMUX_CHILD is not set, got nil")
	}
}

// ======================================================================
// Test CreateChildProcess using an invalid command path.
// ======================================================================

func TestCreateChildProcessInvalidCmd(t *testing.T) {
	_, err := CreateChildProcess(context.Background(), "nonexistent_cmd", []string{})
	if err == nil {
		t.Fatal("expected error for invalid command, got nil")
	}
}
