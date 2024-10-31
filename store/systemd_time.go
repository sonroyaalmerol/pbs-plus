package store

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

type TimerInfo struct {
	Next      time.Time
	Left      string
	Last      time.Time
	Passed    string
	Unit      string
	Activates string
}

func getNextSchedule(job *Job) (*time.Time, error) {
	if job.Schedule == "" {
		return nil, nil
	}

	cmd := exec.Command("systemctl", "list-timers", "--all")
	cmd.Env = os.Environ()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error running systemctl command: %v", err)
	}

	timerUnit := fmt.Sprintf("proxmox-d2d-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	layout := "Mon 2006-01-02 15:04:05 MST"

	scanner.Scan()

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 10 {
			continue
		}

		// Extract `NEXT` time with timezone
		nextStr := strings.Join(fields[0:4], " ")
		nextTime, err := time.Parse(layout, nextStr)
		if err != nil {
			return nil, fmt.Errorf("error parsing Next time: %v", err)
		}

		timer := TimerInfo{
			Next: nextTime,
			Unit: fields[8],
		}

		if timer.Unit == timerUnit {
			return &timer.Next, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading command output: %v", err)
	}

	return nil, nil
}
