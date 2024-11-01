package utils

import (
	"os"
)

func IsValid(fp string) bool {
	if _, err := os.Stat(fp); err == nil {
		return true
	}

	var d []byte
	if err := os.WriteFile(fp, d, 0644); err == nil {
		os.Remove(fp)
		return true
	}

	return false
}
