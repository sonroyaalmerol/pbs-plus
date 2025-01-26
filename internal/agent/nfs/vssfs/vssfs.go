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

func NewVSSFS(snapshot *snapshots.WinVSSSnapshot, baseDir string, excludedPaths *pattern.Matcher) billy.Filesystem {
	rootPath := filepath.Join(snapshot.SnapshotPath, baseDir)
	fs := &VSSFS{
		Filesystem:    osfs.New(rootPath, osfs.WithBoundOS()),
		snapshot:      snapshot,
		excludedPaths: excludedPaths,
		root:          rootPath,
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
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, fmt.Errorf("filesystem is read-only")
	}

	normalizedPath := fs.normalizePath(filename)
	fullPath := filepath.Join(fs.root, normalizedPath)

	if err := fs.validateAndCacheFile(normalizedPath, fullPath); err != nil {
		return nil, err
	}

	return fs.Filesystem.OpenFile(filename, flag, perm)
}

func (fs *VSSFS) Stat(filename string) (os.FileInfo, error) {
	normalizedPath := fs.normalizePath(filename)

	if cached, exists := fs.fileInfoCache.Load(normalizedPath); exists {
		return cached.(*VSSFileInfo), nil
	}

	fullPath := filepath.Join(fs.root, normalizedPath)
	pathPtr, err := syscall.UTF16PtrFromString(fullPath)
	if err != nil {
		return nil, err
	}

	var findData syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pathPtr, &findData)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("FindFirstFile failed: %w", err)
	}
	defer syscall.FindClose(handle)

	foundName := syscall.UTF16ToString(findData.FileName[:])
	expectedName := filepath.Base(normalizedPath)
	if !strings.EqualFold(foundName, expectedName) {
		return nil, os.ErrNotExist
	}

	if fs.shouldSkipEntry(&findData, fullPath) {
		return nil, os.ErrNotExist
	}

	return fs.createFileInfo(normalizedPath, &findData), nil
}

func (fs *VSSFS) ReadDir(dirname string) ([]os.FileInfo, error) {
	normalizedDir := fs.normalizePath(dirname)
	if _, err := fs.Stat(normalizedDir); err != nil {
		return nil, fmt.Errorf("directory inaccessible: %w", err)
	}

	findData, handle, err := fs.initDirectorySearch(dirname)
	if err != nil {
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
