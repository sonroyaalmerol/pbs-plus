//go:build windows
// +build windows

package vssfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent/snapshots"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils/pattern"
)

type VSSFS struct {
	billy.Filesystem
	snapshot      *snapshots.WinVSSSnapshot
	excludedPaths *pattern.Matcher
	root          string
	volumeSerial  uint32

	mu            sync.RWMutex
	pathToID      sync.Map
	idToPath      sync.Map
	fileInfoCache sync.Map
}

var _ billy.Filesystem = (*VSSFS)(nil)

func NewVSSFS(snapshot *snapshots.WinVSSSnapshot, excludedPaths *pattern.Matcher) billy.Filesystem {
	fs := &VSSFS{
		Filesystem:    osfs.New(snapshot.SnapshotPath, osfs.WithBoundOS()),
		snapshot:      snapshot,
		excludedPaths: excludedPaths,
		root:          snapshot.SnapshotPath,
	}

	fs.initVolumeSerial()
	fs.cacheRootDirectory()

	return fs
}

// Override write operations to return read-only errors
func (fs *VSSFS) Create(filename string) (billy.File, error) {
	return nil, fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	syslog.L.Infof("OpenFile request: filename=%s, flags=%d, perm=%o", filename, flag, perm)

	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		syslog.L.Infof("OpenFile rejected: read-only filesystem access attempted for %s", filename)
		return nil, fmt.Errorf("filesystem is read-only")
	}

	normalizedPath := fs.normalizePath(filename)
	fullPath := filepath.Join(fs.root, normalizedPath)
	syslog.L.Infof("OpenFile normalized path: %s -> %s", filename, fullPath)

	if err := fs.validateAndCacheFile(normalizedPath, fullPath); err != nil {
		syslog.L.Infof("OpenFile validation failed for %s: %v", fullPath, err)
		return nil, err
	}

	return fs.Filesystem.OpenFile(filename, flag, perm)
}

func (fs *VSSFS) Stat(filename string) (os.FileInfo, error) {
	syslog.L.Infof("Stat request for file: %s", filename)

	normalizedPath := fs.normalizePath(filename)
	if cached, exists := fs.fileInfoCache.Load(normalizedPath); exists {
		syslog.L.Infof("Stat cache hit for %s", normalizedPath)
		return cached.(*VSSFileInfo), nil
	}

	fullPath := filepath.Join(fs.root, normalizedPath)
	pathPtr, err := syscall.UTF16PtrFromString(fullPath)
	if err != nil {
		syslog.L.Infof("Stat UTF16 conversion failed for %s: %v", fullPath, err)
		return nil, err
	}

	var findData syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pathPtr, &findData)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			syslog.L.Infof("Stat file not found: %s", fullPath)
			return nil, os.ErrNotExist
		}
		syslog.L.Infof("Stat FindFirstFile failed for %s: %v", fullPath, err)
		return nil, fmt.Errorf("FindFirstFile failed: %w", err)
	}
	defer syscall.FindClose(handle)

	foundName := syscall.UTF16ToString(findData.FileName[:])
	expectedName := filepath.Base(normalizedPath)
	if !strings.EqualFold(foundName, expectedName) && expectedName != "\\" {
		syslog.L.Infof("Stat name mismatch: found=%s, expected=%s", foundName, expectedName)
		return nil, os.ErrNotExist
	}

	if fs.shouldSkipEntry(&findData, fullPath) {
		syslog.L.Infof("Stat skipping entry: %s", fullPath)
		return nil, os.ErrNotExist
	}

	return fs.createFileInfo(normalizedPath, &findData), nil
}

func (fs *VSSFS) ReadDir(dirname string) ([]os.FileInfo, error) {
	syslog.L.Infof("ReadDir request for directory: %s", dirname)

	normalizedDir := fs.normalizePath(dirname)
	if _, err := fs.Stat(normalizedDir); err != nil {
		syslog.L.Infof("ReadDir directory inaccessible %s: %v", normalizedDir, err)
		return nil, fmt.Errorf("directory inaccessible: %w", err)
	}

	findData, handle, err := fs.initDirectorySearch(dirname)
	if err != nil {
		syslog.L.Infof("ReadDir failed to initialize directory search for %s: %v", dirname, err)
		return nil, err
	}
	defer syscall.FindClose(handle)

	return fs.processDirectoryEntries(dirname, handle, findData)
}

func (fs *VSSFS) Lstat(filename string) (os.FileInfo, error) {
	return fs.Stat(filename)
}

func (fs *VSSFS) Readlink(link string) (string, error) {
	return fs.Filesystem.Readlink(link)
}

func (fs *VSSFS) Rename(oldpath, newpath string) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Remove(filename string) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) MkdirAll(filename string, perm os.FileMode) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Symlink(target, link string) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) TempFile(dir, prefix string) (billy.File, error) {
	return nil, fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Chmod(name string, mode os.FileMode) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Lchown(name string, uid, gid int) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Chown(name string, uid, gid int) error {
	return fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return fmt.Errorf("filesystem is read-only")
}
