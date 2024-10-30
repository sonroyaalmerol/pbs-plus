package views

import (
	"embed"
	"io/fs"
	"os"
)

func CompileCustomJS(customJsFS *embed.FS) []byte {
	var result []byte

	fs.WalkDir(customJsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		result = append(result, content...)
		result = append(result, []byte("\n")...)

		return nil
	})

	return result
}
