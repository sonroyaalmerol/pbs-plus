//go:build windows

package vssfs

import (
	"os"
	"time"
)

type VSSFileInfo struct {
	os.FileInfo
	name     string
	size     int64
	mode     os.FileMode
	modTime  time.Time
	stableID uint64
	fullPath string
	attrs    uint32
}

func (vi *VSSFileInfo) Name() string       { return vi.name }
func (vi *VSSFileInfo) Size() int64        { return vi.size }
func (vi *VSSFileInfo) Mode() os.FileMode  { return vi.mode }
func (vi *VSSFileInfo) ModTime() time.Time { return vi.modTime }
func (vi *VSSFileInfo) IsDir() bool        { return vi.mode.IsDir() }
func (vi *VSSFileInfo) Sys() interface{} {
	return struct {
		Nlink  uint32
		UID    uint32
		GID    uint32
		Fileid uint64
	}{
		Nlink:  1,
		UID:    1000,
		GID:    1000,
		Fileid: vi.stableID,
	}
}
