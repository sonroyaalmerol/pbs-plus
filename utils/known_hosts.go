package utils

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var knownHostsLock sync.Mutex

func generateSalt() (string, error) {
	salt := make([]byte, 16) // 16 bytes should be sufficient for a salt
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(salt), nil
}

func hashKnownHost(host string) (string, error) {
	salt, err := generateSalt()
	if err != nil {
		return "", err
	}

	key, err := base64.StdEncoding.DecodeString(salt)
	if err != nil {
		return "", err
	}

	h := hmac.New(sha1.New, key)
	if _, err := io.WriteString(h, host); err != nil {
		return "", err
	}
	hash := h.Sum(nil)

	hashBase64 := base64.StdEncoding.EncodeToString(hash)

	hashedKnownHost := fmt.Sprintf("|1|%s|%s|", salt, hashBase64)
	return hashedKnownHost, nil
}

func AddHostToKnownHosts(host, basePath, publicKey string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get user home directory: %w", err)
	}

	driveLetterRune := []rune(basePath)[0]
	port, err := DriveLetterPort(driveLetterRune)
	if err != nil {
		return fmt.Errorf("could not get port number from drive letter: %w", err)
	}

	hashedHost, err := hashKnownHost(fmt.Sprintf("[%s]:%s", host, port))
	if err != nil {
		return fmt.Errorf("could not generate hash from [%s]:%s: %w", host, port, err)
	}

	knownHostsPath := filepath.Join(homeDir, ".ssh", "known_hosts")
	knownHostsLock.Lock()
	defer knownHostsLock.Unlock()

	// Check if the host is already in known_hosts
	exists, err := hostExistsInKnownHosts(knownHostsPath, hashedHost)
	if err != nil {
		return fmt.Errorf("could not read known_hosts: %w", err)
	}
	if exists {
		return nil // Host already exists, no need to add
	}

	// Open known_hosts file for appending
	knownHosts, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("could not open known_hosts: %w", err)
	}
	defer knownHosts.Close()

	// Ensure the last line ends with a newline
	if err := ensureTrailingNewline(knownHostsPath); err != nil {
		return fmt.Errorf("could not ensure trailing newline: %w", err)
	}

	// Write the new entry on a new line
	entry := fmt.Sprintf("%s %s\n", hashedHost, publicKey)
	if _, err = knownHosts.WriteString(entry); err != nil {
		return fmt.Errorf("could not write to known_hosts: %w", err)
	}

	return nil
}

func hostExistsInKnownHosts(knownHostsPath, host string) (bool, error) {
	file, err := os.Open(knownHostsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // File doesn't exist yet, so the host isn't present
		}
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	searchEntry := fmt.Sprintf("%s", host)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), searchEntry) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func ensureTrailingNewline(filePath string) error {
	file, err := os.OpenFile(filePath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to the end and read the last byte
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		return nil // Empty file, no newline needed
	}

	_, err = file.Seek(-1, io.SeekEnd)
	if err != nil {
		return err
	}

	// Read the last byte
	buf := make([]byte, 1)
	if _, err = file.Read(buf); err != nil {
		return err
	}

	// If last byte isn't a newline, append one
	if buf[0] != '\n' {
		if _, err = file.WriteString("\n"); err != nil {
			return err
		}
	}

	return nil
}

