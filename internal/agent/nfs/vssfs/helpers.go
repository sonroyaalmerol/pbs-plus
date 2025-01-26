//go:build windows

package vssfs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

func (fs *VSSFS) cacheRootDirectory() {
	rootID := uint64(0)
	fs.pathToID.Store("/", rootID)
	fs.idToPath.Store(rootID, "/")
}

func (fs *VSSFS) fileModeFromAttributes(attrs uint32) os.FileMode {
	mode := os.FileMode(0444) // Always read-only.
	if attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= os.ModeDir
	}
	return mode
}

func (fs *VSSFS) shouldSkipEntry(data *syscall.Win32finddata, fullPath string) bool {
	if fullPath == "" {
		return false
	}

	if matched, pattern := fs.excludedPaths.Match(fullPath); matched {
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

func (fs *VSSFS) normalizePath(path string) string {
	// Convert to Unix-style slashes
	unixPath := strings.ReplaceAll(path, "\\", "/")
	cleanPath := strings.ReplaceAll(unixPath, "//", "/")
	cleanPath = strings.TrimPrefix(strings.TrimSuffix(cleanPath, "/"), "/")

	if cleanPath == "." || cleanPath == "" {
		cleanPath = ""
	}
	cleanPath = "/" + strings.ToLower(cleanPath)
	if len(cleanPath) > 1 {
		cleanPath = strings.TrimSuffix(cleanPath, "/")
	}
	return cleanPath
}

func (fs *VSSFS) toWindowsPath(normalizedPath string) string {
	// Get relative path within the filesystem
	relPath := strings.TrimPrefix(normalizedPath, "/")

	// Join with snapshot path using Windows separators
	windowsPath := filepath.Join(fs.snapshot.SnapshotPath, filepath.FromSlash(relPath))

	// Add extended-length prefix for Windows API compatibility
	if !strings.HasPrefix(windowsPath, `\\?\`) {
		windowsPath = `\\?\` + windowsPath
	}
	return windowsPath
}

func (fs *VSSFS) cacheFileInfo(normalizedPath string, findData *syscall.Win32finddata) *VSSFileInfo {
	// Get original case from the filesystem
	originalName := syscall.UTF16ToString(findData.FileName[:])

	info := &VSSFileInfo{
		name:     originalName,
		size:     int64(findData.FileSizeHigh)<<32 + int64(findData.FileSizeLow),
		modTime:  time.Unix(0, findData.LastWriteTime.Nanoseconds()),
		mode:     fs.fileModeFromAttributes(findData.FileAttributes),
		stableID: (uint64(findData.Reserved0) << 32) | uint64(findData.Reserved1),
	}

	fs.fileInfoCache.Store(normalizedPath, info)
	fs.pathToID.Store(normalizedPath, info.stableID)
	fs.idToPath.Store(info.stableID, normalizedPath)

	return info
}
