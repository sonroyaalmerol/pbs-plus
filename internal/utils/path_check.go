package utils

import (
	"os"
)

func IsValid(path string) bool {
	// Check if path is not empty and is an absolute path
	if path == "" {
		return false
	}

	// Check if the path exists
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		return false
	}

	// Path exists, return true and no error
	return true
}
