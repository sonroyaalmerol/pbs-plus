//go:build windows
// +build windows

package sftp

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent/cache"
)

type CustomFileInfo struct {
	os.FileInfo
	filePath   string
	snapshotId string
}

func (f *CustomFileInfo) Size() int64 {
	metadataSize := f.FileInfo.Size()

	scanFile := false
	for _, regex := range cache.PartialFilePathRegexes {
		if regex.MatchString(f.filePath) {
			scanFile = true
			break
		}
	}

	/*
		if !scanFile && metadataSize <= 1*1024*1024 {
			d := f.Sys().(*syscall.Win32FileAttributeData)
			cTime := time.Unix(0, d.CreationTime.Nanoseconds())

			if f.ModTime().Before(cTime) {
				scanFile = true
			}
		}
	*/

	if !scanFile {
		return metadataSize
	}

	// Check size cache with read lock
	if snapSizes, ok := cache.SizeCache.Load(f.snapshotId); ok {
		if cachedSize, ok := snapSizes.(map[string]int64)[f.filePath]; ok {
			return cachedSize
		}
	}

	// Compute file size if not cached
	file, err := os.Open(f.filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	byteCount, err := io.Copy(io.Discard, file)
	if err != nil {
		return 0
	}

	snapSizes, _ := cache.SizeCache.LoadOrStore(f.snapshotId, map[string]int64{})
	snapSizes.(map[string]int64)[f.filePath] = byteCount

	return byteCount
}

type FileLister struct {
	files []os.FileInfo
}

func (fl *FileLister) ListAt(fileList []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(fl.files)) {
		return 0, io.EOF
	}

	n := copy(fileList, fl.files[offset:])
	if n < len(fileList) {
		return n, io.EOF
	}
	return n, nil
}

func (h *SftpHandler) FileLister(dirPath string) (*FileLister, error) {
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	fileInfos := make([]os.FileInfo, 0, len(dirEntries))
	for _, entry := range dirEntries {
		select {
		case <-h.ctx.Done():
			return &FileLister{files: fileInfos}, nil
		default:
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}

			fullPath := filepath.Join(dirPath, entry.Name())
			if h.skipFile(fullPath) {
				continue
			}
			// Wrap the original os.FileInfo to override its Size method.
			fileInfos = append(fileInfos, &CustomFileInfo{FileInfo: info, filePath: fullPath, snapshotId: h.Snapshot.Id})
		}
	}

	return &FileLister{files: fileInfos}, nil
}

func (h *SftpHandler) FileStat(filename string) (*FileLister, error) {
	var stat fs.FileInfo
	var err error

	isRoot := strings.TrimPrefix(filename, h.Snapshot.SnapshotPath) == ""
	if isRoot {
		stat, err = os.Stat(filename)
		if err != nil {
			return nil, err
		}
	} else {
		stat, err = os.Lstat(filename)
		if err != nil {
			return nil, err
		}
	}

	// Wrap the original os.FileInfo to override its Size method.
	return &FileLister{files: []os.FileInfo{&CustomFileInfo{FileInfo: stat, filePath: filename, snapshotId: h.Snapshot.Id}}}, nil
}

func (h *SftpHandler) setFilePath(r *sftp.Request) {
	r.Filepath = filepath.Join(h.Snapshot.SnapshotPath, filepath.Clean(r.Filepath))
}
