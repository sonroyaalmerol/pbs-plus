package utils

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/charlievieth/fastwalk"
)

func GetExclusions(root string) ([]string, error) {
	var excludedPaths []string
	storeMutex := new(sync.Mutex)

	conf := fastwalk.Config{
		Follow: false,
	}

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			var skipDir bool
			if pathErr, ok := err.(*os.PathError); ok {
				if errno, ok := pathErr.Err.(syscall.Errno); ok && errno == syscall.ENOTSUP {
					storeMutex.Lock()
					excludedPaths = append(excludedPaths, convertToGlobPattern(path, root))
					storeMutex.Unlock()
					skipDir = true
				}
			} else {
				storeMutex.Lock()
				excludedPaths = append(excludedPaths, convertToGlobPattern(path, root))
				storeMutex.Unlock()
			}
			if skipDir {
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	}

	err := fastwalk.Walk(&conf, root, walkFn)

	return excludedPaths, err
}

func convertToGlobPattern(path, root string) string {
	relPath, _ := filepath.Rel(root, path)
	if d := filepath.Dir(relPath); d == "." {
		return "*" + filepath.Base(relPath)
	}
	return relPath + (string(os.PathSeparator) + "*")
}
