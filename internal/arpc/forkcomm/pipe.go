package forkcomm

import (
	"io"
	"net"
	"time"
)

// pipeConn wraps an io.ReadCloser and an io.WriteCloser into a net.Conn.
type pipeConn struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (p *pipeConn) Read(b []byte) (int, error) {
	return p.reader.Read(b)
}

func (p *pipeConn) Write(b []byte) (int, error) {
	return p.writer.Write(b)
}

func (p *pipeConn) Close() error {
	// Try to close both reader and writer.
	err1 := p.reader.Close()
	err2 := p.writer.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// dummyAddr is a dummy net.Addr implementation for pipeConn.
type dummyAddr struct{}

func (d dummyAddr) Network() string {
	return "pipe"
}

func (d dummyAddr) String() string {
	return "pipe"
}

func (p *pipeConn) LocalAddr() net.Addr {
	return dummyAddr{}
}

func (p *pipeConn) RemoteAddr() net.Addr {
	return dummyAddr{}
}

// SetDeadline is a no-op for pipeConn.
func (p *pipeConn) SetDeadline(t time.Time) error {
	return nil
}

// SetReadDeadline is a no-op for pipeConn.
func (p *pipeConn) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline is a no-op for pipeConn.
func (p *pipeConn) SetWriteDeadline(t time.Time) error {
	return nil
}
