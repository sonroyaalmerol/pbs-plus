package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sgl.com/pbs-ui/store"
)

func generateTimer(job *store.Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s D2D Backup Job Timer

[Timer]
OnCalendar=%s
Persistent=true

[Install]
WantedBy=timers.target`, job.ID, job.Schedule)

	filePath := fmt.Sprintf("proxmox-d2d-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(store.TimerBasePath, filePath)

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

func generateService(job *store.Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s D2D Backup Job Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/pbs-d2d-backup -job="%s"`, job.ID, job.ID)

	filePath := fmt.Sprintf("proxmox-d2d-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(store.TimerBasePath, filePath)

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

func SetSchedule(job *store.Job) error {
	svcPath := fmt.Sprintf("proxmox-d2d-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullSvcPath := filepath.Join(store.TimerBasePath, svcPath)

	timerPath := fmt.Sprintf("proxmox-d2d-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullTimerPath := filepath.Join(store.TimerBasePath, timerPath)

	if job.Schedule == "" {
		err := os.Remove(fullSvcPath)
		if err != nil {
			return err
		}

		err = os.Remove(fullTimerPath)
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	cmd.Env = os.Environ()
	err := cmd.Run()
	if err != nil {
		return err
	}

	if job.Schedule == "" {
		return nil
	}

	cmd = exec.Command("/usr/bin/systemctl", "enable", timerPath)
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}
