//go:build windows

package unsafefs

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"unsafe"

	"github.com/sonroyaalmerol/pbs-plus/internal/agent/agentfs/types"
	binarystream "github.com/sonroyaalmerol/pbs-plus/internal/arpc/binary"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"github.com/xtaci/smux"
	"golang.org/x/sys/windows"
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
	defer stream.Close()

	reqBuf := bufferPool.Get().([]byte)
	defer bufferPool.Put(reqBuf)

	_, err := stream.Read(reqBuf)
	if err != nil {
		return err
	}

	var req types.UnsafeReq
	if err := req.Decode(reqBuf); err != nil {
		return err
	}

	var payload types.ReadAtReq
	if err := payload.Decode(req.Request); err != nil {
		return err
	}

	// Validate the payload parameters.
	if payload.Length < 0 {
		return fmt.Errorf("invalid negative length requested: %d", payload.Length)
	}

	// Retrieve the file handle.
	fh := windows.Handle(req.Handle)

	// Align the offset down to the nearest multiple of the allocation granularity.
	alignedOffset := payload.Offset - (payload.Offset % int64(s.allocGranularity))
	offsetDiff := int(payload.Offset - alignedOffset)
	viewSize := uintptr(payload.Length + offsetDiff)

	// Attempt to create a file mapping.
	h, err := windows.CreateFileMapping(fh, nil, windows.PAGE_READONLY, 0, 0, nil)
	if err == nil {
		// Map the requested view.
		addr, err := windows.MapViewOfFile(
			h,
			windows.FILE_MAP_READ,
			uint32(alignedOffset>>32),
			uint32(alignedOffset&0xFFFFFFFF),
			viewSize,
		)
		if err == nil {
			ptr := (*byte)(unsafe.Pointer(addr))
			data := unsafe.Slice(ptr, viewSize)
			// Verify we’re not slicing outside the allocated region.
			if offsetDiff+payload.Length > len(data) {
				syslog.L.Error(fmt.Errorf(
					"invalid slice bounds: offsetDiff=%d, payload.Length=%d, data len=%d",
					offsetDiff, payload.Length, len(data)),
				).WithMessage("invalid file mapping boundaries").Write()

				windows.UnmapViewOfFile(addr)
				windows.CloseHandle(h)
				return fmt.Errorf("invalid file mapping boundaries")
			}
			result := data[offsetDiff : offsetDiff+payload.Length]
			reader := bytes.NewReader(result)

			// Ensure we free up resources once streaming is done.
			defer func() {
				windows.UnmapViewOfFile(addr)
				windows.CloseHandle(h)
			}()
			if err := binarystream.SendDataFromReader(reader, payload.Length, stream); err != nil {
				syslog.L.Error(err).WithMessage("failed sending data from reader via binary stream").Write()
				return err
			}

			return nil
		}
		// If mapping fails, clean up.
		windows.CloseHandle(h)
	}

	// Fallback to using the OVERLAPPED ReadFile method.
	var overlapped windows.Overlapped
	overlapped.Offset = uint32(payload.Offset & 0xFFFFFFFF)
	overlapped.OffsetHigh = uint32(payload.Offset >> 32)

	buffer := make([]byte, payload.Length)
	var bytesRead uint32
	err = windows.ReadFile(fh, buffer, &bytesRead, &overlapped)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(buffer[:bytesRead])
	if err := binarystream.SendDataFromReader(reader, int(bytesRead), stream); err != nil {
		syslog.L.Error(err).WithMessage("failed sending data from reader via binary stream").Write()
		return err
	}

	return nil
}
