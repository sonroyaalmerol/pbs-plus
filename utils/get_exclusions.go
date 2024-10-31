package utils

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"

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
			if _, ok := err.(*os.PathError); ok {
				info, err := os.Lstat(path)
				if err != nil {
					storeMutex.Lock()
					excludedPaths = append(excludedPaths, convertToGlobPattern(path, root))
					storeMutex.Unlock()
				}

				if info.Mode()&fs.ModeSymlink != 0 {
					storeMutex.Lock()
					excludedPaths = append(excludedPaths, convertToGlobPattern(path, root))
					storeMutex.Unlock()
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
	return relPath
}
