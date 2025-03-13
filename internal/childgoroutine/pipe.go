package childgoroutine

import (
	"net"
	"os"
	"time"
)

// ---------------------------------------------------------------------
// Implementation of a full‑duplex net.Conn over two unidirectional pipes.
// We create two pipes:
//   • Pipe1: parent's read (pr1) gets what the child writes (pw1).
//   • Pipe2: parent's write (pw2) goes to what the child reads (pr2).
//
// The parent's net.Conn is constructed from pr1 and pw2, while the child
// will reassemble a net.Conn from pr2 and pw1.
// ---------------------------------------------------------------------

// pipeDuplex implements net.Conn using separate *os.File objects for read and write.
type pipeDuplex struct {
	r *os.File
	w *os.File
}

func (p *pipeDuplex) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

func (p *pipeDuplex) Write(b []byte) (int, error) {
	return p.w.Write(b)
}

func (p *pipeDuplex) Close() error {
	err1 := p.r.Close()
	err2 := p.w.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func (p *pipeDuplex) LocalAddr() net.Addr                { return dummyAddr("pipe") }
func (p *pipeDuplex) RemoteAddr() net.Addr               { return dummyAddr("pipe") }
func (p *pipeDuplex) SetDeadline(t time.Time) error      { return nil }
func (p *pipeDuplex) SetReadDeadline(t time.Time) error  { return nil }
func (p *pipeDuplex) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return "pipe" }
func (d dummyAddr) String() string  { return string(d) }

// newPipeDuplex returns a net.Conn built from a read file and a write file.
func newPipeDuplex(r, w *os.File) net.Conn {
	return &pipeDuplex{r: r, w: w}
}
