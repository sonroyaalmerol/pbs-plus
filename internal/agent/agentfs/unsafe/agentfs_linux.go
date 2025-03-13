//go:build linux

package unsafefs

import (
	"context"
	"fmt"
	"sync"

	"github.com/xtaci/smux"
)

// Buffer pool for reusing buffers across requests and responses.
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 4096) // Default buffer size
	},
}

type UnsafeFSServer struct {
	ctx              context.Context
	ctxCancel        context.CancelFunc
	allocGranularity uint32
	session          *smux.Session
}

func Initialize(session *smux.Session, allocGranularity uint32) *UnsafeFSServer {
	ctx, cancel := context.WithCancel(context.Background())

	if allocGranularity == 0 {
		allocGranularity = 65536 // 64 KB usually
	}

	if session == nil {
		cancel()
		return nil
	}

	s := &UnsafeFSServer{
		ctx:              ctx,
		ctxCancel:        cancel,
		allocGranularity: allocGranularity,
		session:          session,
	}

	return s
}

func (s *UnsafeFSServer) ServeReadAt() error {
	for {
		stream, err := s.session.AcceptStream()
		if err != nil {
			return err
		}

		s.handleReadAt(stream)
	}
}

func (s *UnsafeFSServer) handleReadAt(stream *smux.Stream) error {
	return fmt.Errorf("not implemented")
}
