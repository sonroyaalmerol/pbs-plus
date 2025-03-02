//go:build windows

package vssfs

import (
	"context"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// overlappedPool reuses OVERLAPPED structures.
var overlappedPool = sync.Pool{
	New: func() interface{} {
		return new(windows.Overlapped)
	},
}

// Get a new OVERLAPPED instance.
func getOverlapped() *windows.Overlapped {
	return overlappedPool.Get().(*windows.Overlapped)
}

// Return an OVERLAPPED instance to the pool.
func putOverlapped(ov *windows.Overlapped) {
	*ov = windows.Overlapped{}
	overlappedPool.Put(ov)
}

type Completion struct {
	BytesTransferred uint32
	Key              uintptr
	Overlapped       *windows.Overlapped
	Err              error
}

// IOCP encapsulates a completion port.
type IOCP struct {
	Port windows.Handle
	// You might deliver completions through a channel.
	Completions chan Completion
}

// NewIOCP creates a new IOCP.
func NewIOCP() (*IOCP, error) {
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 0)
	if err != nil {
		return nil, mapWinError(err)
	}
	return &IOCP{
		Port:        port,
		Completions: make(chan Completion, 128),
	}, nil
}

// AssociateHandle associates a file handle with the IOCP.
func (iocp *IOCP) AssociateHandle(handle windows.Handle, key uintptr) error {
	// Passing our IOCP port ensures that all completions are queued to our port.
	_, err := windows.CreateIoCompletionPort(handle, iocp.Port, key, 0)
	if err != nil {
		return mapWinError(err)
	}
	return nil
}

// Loop waits for I/O completions and sends them to the Completions channel.
func (iocp *IOCP) Loop(ctx context.Context) {
	go func() {
		defer close(iocp.Completions)
		for {
			var bytesTransferred uint32
			var completionKey uintptr
			var ov *windows.Overlapped
			err := windows.GetQueuedCompletionStatus(iocp.Port, &bytesTransferred, &completionKey, &ov, windows.INFINITE)
			comp := Completion{
				BytesTransferred: bytesTransferred,
				Key:              completionKey,
				Overlapped:       ov,
				Err:              err,
			}
			select {
			case iocp.Completions <- comp:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func asyncReadFile(handle windows.Handle, buf []byte, offset int64, iocp *IOCP, timeout time.Duration) (uint32, error) {
	ov := getOverlapped()
	// Set the offset in the OVERLAPPED structure.
	ov.Offset = uint32(offset)
	ov.OffsetHigh = uint32(offset >> 32)

	// We assume the handle has already been associated with IOCP.
	var bytesRead uint32
	err := windows.ReadFile(handle, buf, &bytesRead, ov)
	if err != nil && err != windows.ERROR_IO_PENDING {
		putOverlapped(ov)
		return 0, mapWinError(err)
	}

	timeoutCh := time.After(timeout)
	for {
		select {
		case comp := <-iocp.Completions:
			// Match the overlapped pointer.
			if comp.Overlapped == ov {
				putOverlapped(ov)
				// If the I/O was canceled, ERROR_OPERATION_ABORTED may be returned.
				if comp.Err != nil && comp.Err != windows.ERROR_OPERATION_ABORTED {
					return 0, mapWinError(comp.Err)
				}
				return comp.BytesTransferred, nil
			}
			// If the completion is not for this operation, ignore or re-dispatch.
		case <-timeoutCh:
			// Cancel the pending I/O operation.
			cancelErr := windows.CancelIoEx(handle, ov)
			if cancelErr != nil {
				// Optionally log cancellation error.
			}
			putOverlapped(ov)
			return 0, os.ErrInvalid
		}
	}
}
