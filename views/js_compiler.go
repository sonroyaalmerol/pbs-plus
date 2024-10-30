package views

import (
	"embed"
	"log"
	"os"
	"path"
)

func CompileCustomJS(customJsFS *embed.FS, basePath string) []byte {
	var result []byte

	if basePath == "" {
		basePath = "."
	}

	files, err := customJsFS.ReadDir(basePath)
	if err != nil {
		log.Fatalf("failed to read directory: %v", err)
	}

	for _, file := range files {
		filePath := path.Join(basePath, file.Name())
		if !file.IsDir() {
			content, err := os.ReadFile(filePath)
			if err != nil {
				log.Printf("failed to read file %s: %v", filePath, err)
				continue
			}
			result = append(result, content...)
			result = append(result, []byte("\n")...)
		} else {
			result = append(result, CompileCustomJS(customJsFS, filePath)...)
		}
	}

	return result
}
