//go:build windows

package vssfs

import (
	"io"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

type vssFile struct {
	handle windows.Handle
	name   string
	offset int64
	mu     sync.Mutex
}

func (f *vssFile) Name() string {
	return f.name
}

func (f *vssFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var bytesRead uint32
	err := windows.ReadFile(f.handle, p, &bytesRead, nil)
	if err != nil {
		return 0, err
	}
	f.offset += int64(bytesRead)
	return int(bytesRead), nil
}

func (f *vssFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	current := f.offset
	defer func() {
		_, _ = f.seekLocked(current, io.SeekStart)
	}()

	_, err := f.seekLocked(off, io.SeekStart)
	if err != nil {
		return 0, err
	}

	var bytesRead uint32
	err = windows.ReadFile(f.handle, p, &bytesRead, nil)
	return int(bytesRead), err
}

func (f *vssFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.seekLocked(offset, whence)
}

func (f *vssFile) seekLocked(offset int64, whence int) (int64, error) {
	var moveMethod uint32
	switch whence {
	case io.SeekStart:
		moveMethod = windows.FILE_BEGIN
	case io.SeekCurrent:
		moveMethod = windows.FILE_CURRENT
	case io.SeekEnd:
		moveMethod = windows.FILE_END
	default:
		return 0, syscall.EINVAL
	}

	newOffset, err := windows.Seek(f.handle, offset, int(moveMethod))
	if err != nil {
		return 0, err
	}

	f.offset = newOffset
	return newOffset, nil
}

func (f *vssFile) Write(p []byte) (int, error) {
	return 0, os.ErrPermission
}

func (f *vssFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return windows.CloseHandle(f.handle)
}

func (f *vssFile) Truncate(size int64) error {
	return os.ErrPermission
}

func (f *vssFile) Lock() error {
	return nil
}

func (f *vssFile) Unlock() error {
	return nil
}
