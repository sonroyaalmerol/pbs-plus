package proxy

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed all:views
var customJsFS embed.FS

func compileCustomJS() []byte {
	var result []byte

	err := fs.WalkDir(customJsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		content, err := customJsFS.ReadFile(path)
		if err != nil {
			return err
		}

		result = append(result, content...)
		result = append(result, []byte("\n")...)

		return nil
	})

	if err != nil {
		log.Println(err)
	}

	return result
}
