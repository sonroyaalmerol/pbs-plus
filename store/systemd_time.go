package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func generateTimer(job *Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s D2D Backup Job Timer

[Timer]
OnCalendar=%s
Persistent=true

[Install]
WantedBy=timers.target`, job.ID, job.Schedule)

	filePath := fmt.Sprintf("proxmox-d2d-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(TimerBasePath, filePath)

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the text to the file
	_, err = file.WriteString(content)
	if err != nil {
		return err
	}

	return nil
}

func generateService(job *Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s D2D Backup Job Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/pbs-d2d-backup -job="%s"`, job.ID, job.ID)

	filePath := fmt.Sprintf("proxmox-d2d-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(TimerBasePath, filePath)

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return err
	}

	return nil
}
