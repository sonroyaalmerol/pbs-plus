//go:build linux

package utils

import (
	"io"
	"os"
	"strconv"
	"sync"
	"syscall"
)

type FileMutexCounter struct {
	filename string
	mu       sync.Mutex
}

// NewFileMutexCounter initializes the counter with the given file.
func NewFileMutexCounter(filename string) (*FileMutexCounter, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		err := os.WriteFile(filename, []byte("0"), 0644)
		if err != nil {
			return nil, err
		}
	}
	return &FileMutexCounter{filename: filename}, nil
}

// Increment safely increments the counter.
func (fmc *FileMutexCounter) Increment() (int, error) {
	fmc.mu.Lock()
	defer fmc.mu.Unlock()

	file, err := os.OpenFile(fmc.filename, os.O_RDWR, 0644)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Lock the file
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	// Read the current value
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, err
	}

	// Increment the value
	value++

	// Write the new value
	if _, err := file.Seek(0, 0); err != nil {
		return 0, err
	}
	if _, err := file.WriteString(strconv.Itoa(value)); err != nil {
		return 0, err
	}

	return value, nil
}

// Increment safely decrements the counter.
func (fmc *FileMutexCounter) Decrement() (int, error) {
	fmc.mu.Lock()
	defer fmc.mu.Unlock()

	file, err := os.OpenFile(fmc.filename, os.O_RDWR, 0644)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Lock the file
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	// Read the current value
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, err
	}

	// Decrement the value
	value--

	// Write the new value
	if _, err := file.Seek(0, 0); err != nil {
		return 0, err
	}
	if _, err := file.WriteString(strconv.Itoa(value)); err != nil {
		return 0, err
	}

	return value, nil
}
