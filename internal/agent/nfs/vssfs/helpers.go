//go:build windows

package vssfs

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/dgraph-io/badger/v4"
	"github.com/go-git/go-billy/v5/osfs"
	"golang.org/x/sys/windows"
)

func skipPathWithAttributes(attrs uint32) bool {
	return attrs&(windows.FILE_ATTRIBUTE_REPARSE_POINT|
		windows.FILE_ATTRIBUTE_DEVICE|
		windows.FILE_ATTRIBUTE_OFFLINE|
		windows.FILE_ATTRIBUTE_VIRTUAL|
		windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0
}

const (
	AppName = "pbs-plus"
)

// GetDBPath returns the path to the database file in a hidden folder on C drive
func getDBPath(drivePath string) (string, error) {
	// Create hidden folder path (C:\.pbs-plus)
	appDir := filepath.Join("C:\\", "."+AppName)
	handlerDir := filepath.Join(appDir, "handlers")

	if err := os.MkdirAll(handlerDir, 0700); err != nil {
		return "", err
	}

	// Set folder as hidden
	if err := setHiddenAttribute(appDir); err != nil {
		return "", err
	}

	dbFileName := strings.ReplaceAll(drivePath, ":", "")
	dbFileName = strings.ReplaceAll(dbFileName, "\\", "")
	dbFileName = strings.ReplaceAll(dbFileName, "/", "")
	dbFileName = strings.ToUpper(dbFileName) + ".db"

	return filepath.Join(handlerDir, dbFileName), nil
}

// setHiddenAttribute sets the hidden attribute on Windows
func setHiddenAttribute(path string) error {
	kernel32 := windows.NewLazyDLL("kernel32.dll")
	setFileAttributes := kernel32.NewProc("SetFileAttributesW")

	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	// FILE_ATTRIBUTE_HIDDEN = 2
	r1, _, err := setFileAttributes.Call(uintptr(unsafe.Pointer(ptr)), 2)
	if r1 == 0 {
		return err
	}
	return nil
}

func PreGenerateHandles(ctx context.Context, drivePath string, pauseCh chan struct{}) error {
	dbPath, err := getDBPath(drivePath)
	if err != nil {
		return fmt.Errorf("failed to get db path: %w", err)
	}

	vssFS := VSSFS{
		Filesystem: osfs.New(drivePath, osfs.WithBoundOS()),
		snapshot:   nil,
		root:       drivePath,
	}

	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return fmt.Errorf("failed to open handle database for pre-generation: %w", err)
	}
	defer db.Close()

	root := vssFS.Root() // Precompute the root path once

	var processDir func(path string) error
	processDir = func(path string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pauseCh:
			pauseCh <- struct{}{} // Acknowledge pause
			<-pauseCh             // Wait for resume
		default:
		}

		files, err := vssFS.ReadDir(path)
		if err != nil {
			return err
		}

		// Collect all file entries in the directory
		type fileEntry struct {
			fileID   uint64
			fullPath string
			isDir    bool
		}
		var entries []fileEntry

		for _, file := range files {
			fullPath := filepath.Join(path, file.Name())
			var fileID uint64
			var isDir bool

			if fi, ok := file.(*VSSFileInfo); ok {
				fileID = fi.stableID
				isDir = file.IsDir()
			} else {
				// Fallback to Stat if not VSSFileInfo
				info, err := vssFS.Stat(fullPath)
				if err != nil {
					continue // Skip files that can't be stated
				}
				vssInfo := info.(*VSSFileInfo)
				fileID = vssInfo.stableID
				isDir = vssInfo.IsDir()
			}

			entries = append(entries, fileEntry{
				fileID:   fileID,
				fullPath: fullPath,
				isDir:    isDir,
			})
		}

		// Batch process all entries in a single transaction
		keyBuf := make([]byte, 8) // Reusable buffer for key encoding
		err = db.Update(func(txn *badger.Txn) error {
			for _, entry := range entries {
				binary.BigEndian.PutUint64(keyBuf, entry.fileID)
				_, err := txn.Get(keyBuf)
				if err == badger.ErrKeyNotFound {
					value := filepath.Join(root, entry.fullPath)
					if err := txn.Set(keyBuf, []byte(value)); err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("batch update failed: %w", err)
		}

		// Recursively process directories
		for _, entry := range entries {
			if entry.isDir {
				if err := processDir(entry.fullPath); err != nil {
					return err
				}
			}
		}

		return nil
	}

	return processDir("")
}
