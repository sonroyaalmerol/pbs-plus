//go:build windows

package sftp

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pkg/sftp"
)

type FileLister struct {
	files []os.FileInfo
}

func (fl *FileLister) ListAt(fileList []os.FileInfo, offset int64) (int, error) {
	if int(offset) >= len(fl.files) {
		return 0, nil
	}

	n := copy(fileList, fl.files[offset:])
	return n, nil
}

func (h *SftpHandler) FileLister(dirPath string) (*FileLister, error) {
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	fileInfos := make([]os.FileInfo, 0, len(dirEntries))
	for _, entry := range dirEntries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		fullPath := filepath.Join(dirPath, entry.Name())
		if skipFile(fullPath, info) {
			continue
		}
		fileInfos = append(fileInfos, info)
	}

	return &FileLister{files: fileInfos}, nil
}

func (h *SftpHandler) FileStat(filename string) (*FileLister, error) {
	stat, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}

	if skipFile(filename, stat) {
		return nil, fmt.Errorf("access denied or restricted file: %s", filename)
	}

	return &FileLister{files: []os.FileInfo{stat}}, nil
}

func (h *SftpHandler) setFilePath(r *sftp.Request) {
	r.Filepath = filepath.Join(h.BasePath, filepath.Clean(r.Filepath))
}

func (h *SftpHandler) fetch(path string, mode int) (*os.File, error) {
	return os.OpenFile(path, mode, 0777)
}

func skipFile(path string, fileInfo os.FileInfo) bool {
	restrictedDirs := []string{"$RECYCLE.BIN", "System Volume Information"}
	for _, dir := range restrictedDirs {
		if fileInfo.IsDir() && fileInfo.Name() == dir {
			return true
		}
	}

	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return true
	}

	if !fileInfo.IsDir() {
		if _, err := os.Open(path); err != nil {
			if pe, ok := err.(*os.PathError); ok && pe.Err == syscall.ERROR_ACCESS_DENIED {
				return true
			}
		}
	}
	return false
}
