package windows

import (
	"fmt"
	"os"
	"strconv"
)

func GetLocalDrives() (r []string) {
	for _, drive := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		f, err := os.Open(string(drive) + ":\\")
		if err == nil {
			r = append(r, string(drive))
			f.Close()
		}
	}
	return
}

func DriveLetterPort(letter rune) (string, error) {
	if letter < 'A' || letter > 'Z' {
		return "", fmt.Errorf("invalid letter: %c; must be between A and Z", letter)
	}
	return strconv.Itoa(33450 + int(letter-'A')), nil
}
