//go:build windows
// +build windows

package vssfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent/snapshots"
	"golang.org/x/sys/windows"
)

const (
	cacheSizePercent    = 5  // 5% of total system memory
	evictionThreshold   = 80 // Evict when system memory usage exceeds 80%
	monitorInterval     = 10 * time.Second
	approxEntryOverhead = 100 // Estimated overhead per cache entry in bytes
)

// VSSFS extends osfs while enforcing read-only operations
type VSSFS struct {
	billy.Filesystem
	snapshot *snapshots.WinVSSSnapshot
	root     string

	cache     *ConcurrentLRUCache
	stopEvict chan struct{}
	totalMem  uint64
}

var _ billy.Filesystem = (*VSSFS)(nil)

func NewVSSFS(snapshot *snapshots.WinVSSSnapshot, baseDir string) billy.Filesystem {
	fs := &VSSFS{
		Filesystem: osfs.New(filepath.Join(snapshot.SnapshotPath, baseDir), osfs.WithBoundOS()),
		snapshot:   snapshot,
		root:       filepath.Join(snapshot.SnapshotPath, baseDir),
		stopEvict:  make(chan struct{}),
	}

	// Get total system memory
	if vm, err := mem.VirtualMemory(); err == nil {
		fs.totalMem = vm.Total
	} else {
		// Fallback to 1GB if memory detection fails
		fs.totalMem = 1 << 30
	}

	shardCount := calculateShardCount()
	fs.cache = NewConcurrentLRUCache(shardCount, uint64(float64(fs.totalMem)*cacheSizePercent/100))

	go fs.monitorMemoryPressure()
	return fs
}

func NewConcurrentLRUCache(shardCount int, maxSize uint64) *ConcurrentLRUCache {
	cache := &ConcurrentLRUCache{
		shards:    make([]*cacheShard, shardCount),
		shardMask: uint64(shardCount - 1),
	}
	for i := range cache.shards {
		cache.shards[i] = &cacheShard{
			entries: make(map[string]*cacheEntry),
			order:   make([]string, 0),
		}
	}
	cache.maxSize.Store(maxSize)
	return cache
}

// Override write operations to return read-only errors
func (fs *VSSFS) Create(filename string) (billy.File, error) {
	return nil, fmt.Errorf("filesystem is read-only")
}

func (fs *VSSFS) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

func (fs *VSSFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, fmt.Errorf("filesystem is read-only")
	}

	path, err := fs.abs(filename)
	if err != nil {
		return nil, err
	}

	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	handle, err := windows.CreateFile(
		pathp,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, err
	}

	return &vssfile{File: os.NewFile(uintptr(handle), path)}, nil
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

func (fs *VSSFS) Lstat(filename string) (os.FileInfo, error) {
	return fs.Stat(filename)
}

func (fs *VSSFS) Stat(filename string) (os.FileInfo, error) {
	windowsPath := filepath.FromSlash(filename)
	fullPath, err := fs.abs(filename)
	if err != nil {
		return nil, err
	}
	if filename == "." || filename == "" {
		fullPath = fs.root
		windowsPath = "."
	}

	// Try cache first
	if info, err := fs.cache.Get(fullPath); err == nil {
		return info, nil
	}

	// Check memory pressure before filesystem operation
	if vm, err := mem.VirtualMemory(); err == nil && vm.UsedPercent >= evictionThreshold {
		fs.cache.Evict(fs.cache.maxSize.Load() / 2)
	}

	pathPtr, err := windows.UTF16PtrFromString(fullPath)
	if err != nil {
		fs.cache.Set(fullPath, nil, err)
		return nil, err
	}

	var findData windows.Win32finddata
	handle, err := windows.FindFirstFile(pathPtr, &findData)
	if err != nil {
		mappedErr := mapWinError(err, filename)
		fs.cache.Set(fullPath, nil, err)
		return nil, mappedErr
	}
	defer windows.FindClose(handle)

	foundName := windows.UTF16ToString(findData.FileName[:])
	expectedName := filepath.Base(fullPath)
	if filename == "." {
		expectedName = foundName
	}
	if !strings.EqualFold(foundName, expectedName) {
		fs.cache.Set(fullPath, nil, os.ErrNotExist)
		return nil, os.ErrNotExist
	}

	name := foundName
	switch filename {
	case ".", "/":
		name = filename
	}

	info := createFileInfoFromFindData(name, windowsPath, &findData)
	fs.cache.Set(fullPath, info, nil)
	return info, nil
}

func (fs *VSSFS) ReadDir(dirname string) ([]os.FileInfo, error) {
	windowsDir := filepath.FromSlash(dirname)
	fullDirPath, err := fs.abs(windowsDir)
	if err != nil {
		return nil, err
	}
	if dirname == "." || dirname == "" {
		windowsDir = "."
		fullDirPath = fs.root
	}

	searchPath := filepath.Join(fullDirPath, "*")
	var findData windows.Win32finddata
	utf16Path, err := windows.UTF16PtrFromString(searchPath)
	if err != nil {
		return nil, mapWinError(err, dirname)
	}

	handle, err := windows.FindFirstFile(utf16Path, &findData)
	if err != nil {
		return nil, mapWinError(err, dirname)
	}
	defer windows.FindClose(handle)

	var entries []os.FileInfo
	for {
		name := windows.UTF16ToString(findData.FileName[:])
		if name != "." && name != ".." {
			if !skipPathWithAttributes(findData.FileAttributes) {
				entryFullPath := filepath.Join(fullDirPath, name)
				winEntryPath := filepath.Join(windowsDir, name)

				var info os.FileInfo

				if entry, err := fs.cache.Get(entryFullPath); err == nil {
					info = entry
				} else {
					createFileInfoFromFindData(name, winEntryPath, &findData)
					fs.cache.Set(entryFullPath, info, nil)
				}
				entries = append(entries, info)
			}
		}

		if err := windows.FindNextFile(handle, &findData); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, err
		}
	}
	return entries, nil
}

func (fs *VSSFS) ClearCache() {
	fs.cache.Evict(0)
}

func (fs *VSSFS) Close() {
	fs.ClearCache()
	close(fs.stopEvict)
}

func mapWinError(err error, path string) error {
	switch err {
	case windows.ERROR_FILE_NOT_FOUND:
		return os.ErrNotExist
	case windows.ERROR_PATH_NOT_FOUND:
		return os.ErrNotExist
	case windows.ERROR_ACCESS_DENIED:
		return os.ErrPermission
	default:
		return &os.PathError{
			Op:   "access",
			Path: path,
			Err:  err,
		}
	}
}

func (fs *VSSFS) abs(filename string) (string, error) {
	if filename == fs.root {
		filename = string(filepath.Separator)
	}

	path, err := securejoin.SecureJoin(fs.root, filename)
	if err != nil {
		return "", nil
	}

	return path, nil
}

func (fs *VSSFS) monitorMemoryPressure() {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get current memory usage
			vm, err := mem.VirtualMemory()
			if err != nil {
				continue
			}

			// Update cache max size based on current available memory
			newMax := uint64(float64(vm.Available) * cacheSizePercent / 100)
			fs.cache.maxSize.Store(newMax)

			// Check memory pressure
			if vm.UsedPercent >= evictionThreshold {
				fs.cache.Evict(uint64(float64(newMax) * 0.8))
			}

		case <-fs.stopEvict:
			return
		}
	}
}
