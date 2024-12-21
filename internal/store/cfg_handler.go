package store

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type CfgDatabase struct {
	cfgCache map[string]map[string]map[string]string
	lastRead map[string]time.Time
	cacheMu  sync.RWMutex
	fileMu   sync.RWMutex
}

func NewCfgDatabase() *CfgDatabase {
	return &CfgDatabase{
		cfgCache: make(map[string]map[string]map[string]string),
		lastRead: make(map[string]time.Time),
	}
}

func (s *CfgDatabase) WriteObject(filePath string, data map[string]string) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("%s: %s\n", data["object"], data["id"]))
	for key, value := range data {
		if key != "id" && key != "object" {
			buffer.WriteString(fmt.Sprintf("\t%s %s\n", key, value))
		}
	}

	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("writeObject: error opening file -> %w", err)
	}
	defer file.Close()

	if _, err := file.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("writeObject: error writing to file -> %w", err)
	}

	return nil
}

func (s *CfgDatabase) WriteAllObjects(filePath string, data map[string]map[string]string) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	var buffer bytes.Buffer
	for _, obj := range data {
		buffer.WriteString(fmt.Sprintf("%s: %s\n", obj["object"], obj["id"]))
		for key, value := range obj {
			if key != "id" && key != "object" {
				buffer.WriteString(fmt.Sprintf("\t%s %s\n", key, value))
			}
		}
		buffer.WriteString("\n") // Separate entries with a blank line
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("writeAllObjects: error creating file -> %w", err)
	}
	defer file.Close()

	if _, err := file.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("writeAllObjects: error writing to file -> %w", err)
	}

	return nil
}

func (s *CfgDatabase) ReadCfgFile(filePath string) (map[string]map[string]string, error) {
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("readCfgFile: error opening file -> %w", err)
	}
	defer file.Close()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("readCfgFile: error retrieving file info -> %w", err)
	}

	s.cacheMu.RLock()
	if modTime, exists := s.lastRead[filePath]; exists && modTime.Equal(fileInfo.ModTime()) {
		cachedData := s.cfgCache[filePath]
		s.cacheMu.RUnlock()
		return cachedData, nil
	}
	s.cacheMu.RUnlock()

	data := make(map[string]map[string]string)
	scanner := bufio.NewScanner(file)
	var currData map[string]string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if currData != nil {
				data[currData["id"]] = currData
				currData = nil
			}
			continue
		}

		if strings.HasPrefix(line, "\t") {
			parts := strings.SplitN(line[1:], " ", 2)
			if len(parts) == 2 && currData != nil {
				currData[parts[0]] = parts[1]
			}
		} else {
			if currData != nil {
				data[currData["id"]] = currData
			}
			currData = map[string]string{}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				currData["object"] = strings.TrimSpace(parts[0])
				currData["id"] = strings.TrimSpace(parts[1])
			}
		}
	}
	if currData != nil {
		data[currData["id"]] = currData
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("readCfgFile: error scanning file -> %w", err)
	}

	s.cacheMu.Lock()
	s.cfgCache[filePath] = data
	s.lastRead[filePath] = fileInfo.ModTime()
	s.cacheMu.Unlock()

	return data, nil
}

func (s *CfgDatabase) UpdateObject(filePath string, id string, updatedData map[string]string) error {
	data, err := s.ReadCfgFile(filePath)
	if err != nil {
		return fmt.Errorf("updateObject: error reading file -> %w", err)
	}

	if _, exists := data[id]; !exists {
		return fmt.Errorf("updateObject: ID %s not found", id)
	}

	data[id] = updatedData
	return s.WriteAllObjects(filePath, data)
}

func (s *CfgDatabase) DeleteObject(filePath string, id string) error {
	data, err := s.ReadCfgFile(filePath)
	if err != nil {
		return fmt.Errorf("deleteObject: error reading file -> %w", err)
	}

	if _, exists := data[id]; !exists {
		return fmt.Errorf("deleteObject: ID %s not found", id)
	}

	delete(data, id)
	return s.WriteAllObjects(filePath, data)
}
