package sftp

import (
	"os"
	"path/filepath"

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
		fileInfos = append(fileInfos, info)
	}

	return &FileLister{files: fileInfos}, nil
}

func (h *SftpHandler) FileStat(filename string) (*FileLister, error) {
	stat, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}

	return &FileLister{files: []os.FileInfo{stat}}, nil
}

func (h *SftpHandler) setFilePath(r *sftp.Request) {
	r.Filepath = filepath.Join(h.BasePath, filepath.Clean(r.Filepath))
}

func (h *SftpHandler) fetch(path string, mode int) (*os.File, error) {
	return os.OpenFile(path, mode, 0777)
}
