//go:build windows

package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
)

func (p *agentService) ensureTempDir() (string, error) {
	ex, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	tempDir := filepath.Join(filepath.Dir(ex), tempUpdateDir)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	return tempDir, nil
}

func (p *agentService) downloadAndVerifyMD5() (string, error) {
	md5Resp, err := agent.ProxmoxHTTPRequest(
		http.MethodGet,
		"/api2/json/plus/binary/checksum",
		nil,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("failed to download MD5: %w", err)
	}
	defer md5Resp.Close()

	md5Bytes, err := io.ReadAll(md5Resp)
	if err != nil {
		return "", fmt.Errorf("failed to read MD5: %w", err)
	}

	return strings.TrimSpace(string(md5Bytes)), nil
}

func (p *agentService) calculateMD5(filepath string) (string, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for MD5 calculation: %w", err)
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate MD5: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (p *agentService) downloadUpdate() (string, io.ReadCloser, error) {
	tempDir, err := p.ensureTempDir()
	if err != nil {
		return "", nil, err
	}

	tempFile := filepath.Join(tempDir, fmt.Sprintf("update-%s.tmp", time.Now().Format("20060102150405")))
	file, err := os.Create(tempFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temporary file: %w", err)
	}

	dlResp, err := agent.ProxmoxHTTPRequest(http.MethodGet, "/api2/json/plus/binary", nil, nil)
	if err != nil {
		file.Close()
		os.Remove(tempFile)
		return "", nil, fmt.Errorf("failed to download update: %w", err)
	}

	return tempFile, dlResp, nil
}

func (p *agentService) verifyAndApplyUpdate(tempFile string) error {
	expectedMD5, err := p.downloadAndVerifyMD5()
	if err != nil {
		return fmt.Errorf("failed to get expected MD5: %w", err)
	}

	actualMD5, err := p.calculateMD5(tempFile)
	if err != nil {
		return fmt.Errorf("failed to calculate actual MD5: %w", err)
	}

	if !strings.EqualFold(actualMD5, expectedMD5) {
		return fmt.Errorf("MD5 mismatch: expected %s, got %s", expectedMD5, actualMD5)
	}

	ex, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	backupPath := ex + ".backup"
	if err := os.Link(ex, backupPath); err != nil && !os.IsExist(err) {
		syslog.L.Errorf("Failed to create backup: %v", err)
	}

	updateFile, err := os.Open(tempFile)
	if err != nil {
		return fmt.Errorf("failed to open update file: %w", err)
	}
	defer updateFile.Close()

	if err := selfupdate.Apply(updateFile, selfupdate.Options{}); err != nil {
		if backupErr := os.Rename(backupPath, ex); backupErr != nil {
			syslog.L.Errorf("Failed to restore backup after failed update: %v", backupErr)
		}
		return fmt.Errorf("failed to apply update: %w", err)
	}

	os.Remove(backupPath)
	os.Remove(tempFile)

	return nil
}

func (p *agentService) performUpdate() error {
	tempFile, dlResp, err := p.downloadUpdate()
	if err != nil {
		return err
	}
	defer func() {
		if dlResp != nil {
			_, _ = io.Copy(io.Discard, dlResp)
			dlResp.Close()
		}
	}()

	file, err := os.OpenFile(tempFile, os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	if _, err := io.Copy(file, dlResp); err != nil {
		file.Close()
		os.Remove(tempFile)
		return fmt.Errorf("failed to save update file: %w", err)
	}
	file.Close()

	if err := p.verifyAndApplyUpdate(tempFile); err != nil {
		os.Remove(tempFile)
		return err
	}

	ex, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	restartCmd := exec.Command(ex, "restart")
	return restartCmd.Start()
}

func (p *agentService) cleanupOldUpdates() error {
	tempDir, err := p.ensureTempDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return fmt.Errorf("failed to read temp directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			path := filepath.Join(tempDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				syslog.L.Errorf("Failed to get file info for %s: %v", path, err)
				continue
			}

			if time.Since(info.ModTime()) > 24*time.Hour {
				if err := os.Remove(path); err != nil {
					syslog.L.Errorf("Failed to remove old update file %s: %v", path, err)
				}
			}
		}
	}

	return nil
}

func (p *agentService) versionCheck() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	if err := p.cleanupOldUpdates(); err != nil {
		syslog.L.Errorf("Failed to clean up old updates: %v", err)
	}

	checkAndUpdate := func() {
		// Check if there are any active backups
		backupStatus := agent.GetBackupStatus()
		if backupStatus.HasActiveBackups() {
			syslog.L.Info("Skipping version check - backup in progress")
			return
		}

		var versionResp VersionResp
		_, err := agent.ProxmoxHTTPRequest(http.MethodGet, "/api2/json/plus/version", nil, &versionResp)
		if err != nil {
			syslog.L.Errorf("Version check failed: %v", err)
			return
		}

		if versionResp.Version != Version {
			syslog.L.Infof("New version %s available, current version: %s", versionResp.Version, Version)

			// Double check for active backups before starting update
			if backupStatus.HasActiveBackups() {
				syslog.L.Info("Postponing update - backup started during version check")
				return
			}

			for retry := 0; retry < maxUpdateRetries; retry++ {
				// Check again before each retry
				if backupStatus.HasActiveBackups() {
					syslog.L.Info("Postponing update retry - backup in progress")
					return
				}

				if err := p.performUpdate(); err != nil {
					syslog.L.Errorf("Update attempt %d failed: %v", retry+1, err)
					time.Sleep(updateRetryDelay)
					continue
				}
				syslog.L.Infof("Successfully updated to version %s", versionResp.Version)
				return
			}
			syslog.L.Errorf("Failed to update after %d attempts", maxUpdateRetries)
		}
	}

	checkAndUpdate()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			checkAndUpdate()
		}
	}
}
