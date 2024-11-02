package utils

import (
	"fmt"
	"strconv"
)

func DriveLetterPort(letter rune) (string, error) {
	if letter < 'A' || letter > 'Z' {
		return "", fmt.Errorf("DriveLetterPort: invalid letter: %c; must be between A and Z", letter)
	}
	return strconv.Itoa(33450 + int(letter-'A')), nil
}
