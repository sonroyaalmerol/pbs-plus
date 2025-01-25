//go:build windows

package vssfs

import (
	"os"
	"strings"

	"github.com/sonroyaalmerol/pbs-plus/internal/agent/snapshots"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils/pattern"
	"golang.org/x/sys/windows"
)

func skipPath(path string, snapshot *snapshots.WinVSSSnapshot, exclusions *pattern.Matcher) bool {
	pathWithoutSnap := strings.TrimPrefix(path, snapshot.SnapshotPath)
	normalizedPath := strings.ToUpper(strings.TrimPrefix(pathWithoutSnap, "\\"))

	if strings.TrimSpace(normalizedPath) == "" {
		return false
	}

	if matched, _ := exclusions.Match(normalizedPath); matched {
		return true
	}

	invalid, err := hasInvalidAttributes(path)
	if err != nil || invalid {
		return true
	}

	return false
}

func hasInvalidAttributes(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}

	attributes, err := windows.GetFileAttributes(p)
	if err != nil {
		return false, os.NewSyscallError("GetFileAttributes", err)
	}

	invalidAttributes := map[uint32]string{
		windows.FILE_ATTRIBUTE_TEMPORARY:             "TEMPORARY",
		windows.FILE_ATTRIBUTE_RECALL_ON_OPEN:        "RECALL_ON_OPEN",
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS: "RECALL_ON_DATA_ACCESS",
		windows.FILE_ATTRIBUTE_VIRTUAL:               "VIRTUAL",
		windows.FILE_ATTRIBUTE_OFFLINE:               "OFFLINE",
		windows.FILE_ATTRIBUTE_REPARSE_POINT:         "REPARSE_POINT",
	}

	for attr := range invalidAttributes {
		if attributes&attr != 0 {
			return true, nil
		}
	}
	return false, nil
}

func getFileIDWindows(path string, fi *windows.ByHandleFileInformation) (uint64, uint32, uint32, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, err
	}

	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, 0, 0, err
	}
	defer windows.CloseHandle(handle)

	if err := windows.GetFileInformationByHandle(handle, fi); err != nil {
		return 0, 0, 0, err
	}

	fileIndex := uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow)
	stableID := uint64(fi.VolumeSerialNumber)<<48 | (fileIndex & 0xFFFFFFFFFFFF)
	return stableID, fi.NumberOfLinks, fi.FileAttributes, nil
}

// Use existing syscall data when possible
func computeIDFromExisting(fi *windows.ByHandleFileInformation) (uint64, uint32) {
	fileIndex := uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow)
	return uint64(fi.VolumeSerialNumber)<<48 | (fileIndex & 0xFFFFFFFFFFFF), fi.NumberOfLinks
}
