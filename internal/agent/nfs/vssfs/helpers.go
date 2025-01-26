//go:build windows

package vssfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"golang.org/x/sys/windows"
)

var InvalidFileAttributes = []uint32{
	windows.FILE_ATTRIBUTE_TEMPORARY,
	windows.FILE_ATTRIBUTE_RECALL_ON_OPEN,
	windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS,
	windows.FILE_ATTRIBUTE_VIRTUAL,
	windows.FILE_ATTRIBUTE_OFFLINE,
	windows.FILE_ATTRIBUTE_REPARSE_POINT,
}

func (fs *VSSFS) GetExclusions() []string {
	return fs.excludedPaths.ToStringArray()
}

func (fs *VSSFS) initVolumeSerial() {
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(fs.root),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err == nil {
		windows.GetVolumeInformationByHandle(h, nil, 0, &fs.volumeSerial, nil, nil, nil, 0)
		windows.CloseHandle(h)
	}
}

func (fs *VSSFS) cacheRootDirectory() {
	rootID := fs.getFileID(0, 0)
	fs.pathToID.Store("/", rootID)
	fs.idToPath.Store(rootID, "/")
}

func (fs *VSSFS) validateAndCacheFile(normalizedPath, fullPath string) error {
	if _, cached := fs.fileInfoCache.Load(normalizedPath); cached {
		return nil
	}

	info, err := fs.Filesystem.Stat(normalizedPath)
	if err != nil {
		return err
	}

	sysInfo, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return fmt.Errorf("invalid file information")
	}

	for _, attr := range InvalidFileAttributes {
		if sysInfo.FileAttributes&attr != 0 {
			return os.ErrNotExist
		}
	}

	stableID := fs.getFileIDFromPath(normalizedPath)
	vssInfo := &VSSFileInfo{
		FileInfo: info,
		stableID: stableID,
		fullPath: fullPath,
		attrs:    sysInfo.FileAttributes,
	}
	fs.fileInfoCache.Store(normalizedPath, vssInfo)

	return nil
}

func (fs *VSSFS) initDirectorySearch(dirname string) (*syscall.Win32finddata, syscall.Handle, error) {
	fullPath := filepath.Join(fs.root, dirname, "*")
	pathPtr, err := syscall.UTF16PtrFromString(fullPath)
	if err != nil {
		return nil, 0, err
	}

	var findData syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pathPtr, &findData)
	if err != nil {
		return nil, 0, fmt.Errorf("FindFirstFile failed: %w", err)
	}

	return &findData, handle, nil
}

func (fs *VSSFS) processDirectoryEntries(dirname string, handle syscall.Handle, findData *syscall.Win32finddata) ([]os.FileInfo, error) {
	var entries []os.FileInfo

	for {
		name := syscall.UTF16ToString(findData.FileName[:])
		if name != "." && name != ".." {
			entryPath := filepath.Join(dirname, name)
			fullEntryPath := filepath.Join(fs.root, entryPath)

			if !fs.shouldSkipEntry(findData, fullEntryPath) {
				info := fs.createFileInfo(entryPath, findData)
				entries = append(entries, info)
			}
		}

		if err := syscall.FindNextFile(handle, findData); err != nil {
			break
		}
	}

	return entries, nil
}

func (fs *VSSFS) createFileInfo(path string, findData *syscall.Win32finddata) *VSSFileInfo {
	syslog.L.Infof("Creating file info for path: %s", path)

	name := filepath.Base(path)
	size := int64(findData.FileSizeHigh)<<32 + int64(findData.FileSizeLow)
	modTime := time.Unix(0, findData.LastWriteTime.Nanoseconds())
	stableID := fs.getFileID(findData.Reserved0, findData.Reserved1)

	normalizedPath := fs.normalizePath(path)
	fs.pathToID.Store(normalizedPath, stableID)
	fs.idToPath.Store(stableID, normalizedPath)

	syslog.L.Infof("File details - name: %s, size: %d, modTime: %v, stableID: %d",
		name, size, modTime, stableID)

	vssInfo := &VSSFileInfo{
		name:     name,
		size:     size,
		mode:     fs.fileModeFromAttributes(findData.FileAttributes),
		modTime:  modTime,
		stableID: stableID,
		attrs:    findData.FileAttributes,
	}

	fs.fileInfoCache.Store(normalizedPath, vssInfo)
	syslog.L.Infof("Cached file info for %s", normalizedPath)

	return vssInfo
}

func (fs *VSSFS) fileModeFromAttributes(attrs uint32) os.FileMode {
	mode := os.FileMode(0444) // Always read-only.
	if attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= os.ModeDir
	}
	return mode
}

func (fs *VSSFS) shouldSkipEntry(data *syscall.Win32finddata, fullPath string) bool {
	pathWithoutSnap := strings.TrimPrefix(fullPath, fs.snapshot.SnapshotPath)
	normalizedPath := strings.TrimPrefix(pathWithoutSnap, "\\")

	if normalizedPath == "" {
		return false
	}

	if matched, pattern := fs.excludedPaths.Match(normalizedPath); matched {
		syslog.L.Infof("Matched pattern: %s", pattern.String())
		return true
	}

	for _, attr := range InvalidFileAttributes {
		if data.FileAttributes&attr != 0 {
			return true
		}
	}

	return false
}

func (fs *VSSFS) getFileID(fileIndexHigh, fileIndexLow uint32) uint64 {
	return (uint64(fs.volumeSerial) << 48) | (uint64(fileIndexHigh) << 32) | uint64(fileIndexLow)
}

func (fs *VSSFS) getFileIDFromPath(path string) uint64 {
	normalized := fs.normalizePath(path)
	if id, exists := fs.pathToID.Load(normalized); exists {
		return id.(uint64)
	}
	return 0
}

func (fs *VSSFS) normalizePath(path string) string {
	var b strings.Builder
	b.Grow(len(path) + 1)

	cleanPath := filepath.Clean(path)
	if cleanPath == "." {
		cleanPath = ""
	}

	for _, c := range cleanPath {
		if c == '\\' {
			b.WriteByte('/')
		} else {
			b.WriteRune(unicode.ToUpper(c))
		}
	}

	result := b.String()
	if !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	if len(result) > 1 && strings.HasSuffix(result, "/") {
		result = result[:len(result)-1]
	}

	return result
}
