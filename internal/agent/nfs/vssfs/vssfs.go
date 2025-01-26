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

	// Use the base Filesystem which is already chrooted to the snapshot
	return fs.Filesystem.OpenFile(filename, flag, perm)
}

func (fs *VSSFS) Stat(filename string) (os.FileInfo, error) {
	syslog.L.Infof("Stat called for path: %s", filename)
	normalizedPath := fs.normalizePath(filename)
	syslog.L.Infof("Normalized path: %s", normalizedPath)

	if cached, exists := fs.fileInfoCache.Load(normalizedPath); exists {
		syslog.L.Infof("Returning cached FileInfo for: %s", normalizedPath)
		return cached.(*VSSFileInfo), nil
	}

	windowsPath := fs.toWindowsPath(normalizedPath)
	syslog.L.Infof("Windows path: %s", windowsPath)

	pathPtr, err := syscall.UTF16PtrFromString(windowsPath)
	if err != nil {
		syslog.L.Infof("UTF16PtrFromString failed for: %s, error: %v", windowsPath, err)
		return nil, err
	}

	var findData syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pathPtr, &findData)
	if err != nil {
		syslog.L.Infof("FindFirstFile failed for: %s, error: %v", windowsPath, err)
		return nil, fmt.Errorf("FindFirstFile failed: %w", err)
	}
	defer syscall.FindClose(handle)

	foundName := syscall.UTF16ToString(findData.FileName[:])
	expectedName := filepath.Base(normalizedPath)
	syslog.L.Infof("Found name: %s, expected name: %s", foundName, expectedName)

	if !strings.EqualFold(foundName, expectedName) && normalizedPath != "/" {
		syslog.L.Infof("Name mismatch for: %s", normalizedPath)
		return nil, os.ErrNotExist
	}

	fileInfo := fs.cacheFileInfo(normalizedPath, &findData)
	syslog.L.Infof("Cached FileInfo for: %s", normalizedPath)
	return fileInfo, nil
}

func (fs *VSSFS) ReadDir(dirname string) ([]os.FileInfo, error) {
	syslog.L.Infof("ReadDir called for directory: %s", dirname)
	normalizedDir := fs.normalizePath(dirname)
	syslog.L.Infof("Normalized directory: %s", normalizedDir)

	windowsPath := fs.toWindowsPath(normalizedDir + "/*")
	syslog.L.Infof("Windows path pattern: %s", windowsPath)

	pathPtr, err := syscall.UTF16PtrFromString(windowsPath)
	if err != nil {
		syslog.L.Infof("UTF16PtrFromString failed for: %s, error: %v", windowsPath, err)
		return nil, err
	}

	var findData syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pathPtr, &findData)
	if err != nil {
		syslog.L.Infof("FindFirstFile failed for: %s, error: %v", windowsPath, err)
		return nil, fmt.Errorf("FindFirstFile failed: %w", err)
	}
	defer syscall.FindClose(handle)

	var entries []os.FileInfo
	for {
		name := syscall.UTF16ToString(findData.FileName[:])
		syslog.L.Infof("Processing directory entry: %s", name)

		if name != "." && name != ".." {
			entryPath := filepath.Join(normalizedDir, name)
			syslog.L.Infof("Entry path: %s", entryPath)

			if !fs.shouldSkipEntry(&findData, entryPath) {
				fileInfo := fs.cacheFileInfo(entryPath, &findData)
				entries = append(entries, fileInfo)
				syslog.L.Infof("Added entry to results: %s", entryPath)
			} else {
				syslog.L.Infof("Skipping entry: %s", entryPath)
			}
		}

		if err := syscall.FindNextFile(handle, &findData); err != nil {
			syslog.L.Infof("FindNextFile finished or failed for: %s, error: %v", windowsPath, err)
			break
		}
	}

	syslog.L.Infof("ReadDir completed for %s, found %d entries", dirname, len(entries))
	return entries, nil
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
