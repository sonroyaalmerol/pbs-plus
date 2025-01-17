//go:build linux

package store

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
)

func generateTimer(job *Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s Backup Job Timer

[Timer]
OnCalendar=%s
Persistent=false

[Install]
WantedBy=timers.target`, job.ID, job.Schedule)

	filePath := fmt.Sprintf("pbs-plus-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(TimerBasePath, filePath)

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("generateTimer: error opening timer file -> %w", err)
	}
	defer file.Close()

	// Write the text to the file
	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("generateTimer: error writing content to timer file -> %w", err)
	}

	return nil
}

func generateService(job *Job) error {
	content := fmt.Sprintf(`[Unit]
Description=%s Backup Job Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/pbs-plus -job="%s"`, job.ID, job.ID)

	filePath := fmt.Sprintf("pbs-plus-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullPath := filepath.Join(TimerBasePath, filePath)

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("generateService: error opening service file -> %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("generateService: error writing content to service file -> %w", err)
	}

	return nil
}

func deleteSchedule(id string) {
	svcFilePath := fmt.Sprintf("pbs-plus-job-%s.service", strings.ReplaceAll(id, " ", "-"))
	svcFullPath := filepath.Join(TimerBasePath, svcFilePath)

	timerFilePath := fmt.Sprintf("pbs-plus-job-%s.timer", strings.ReplaceAll(id, " ", "-"))
	timerFullPath := filepath.Join(TimerBasePath, timerFilePath)

	_ = os.Remove(svcFullPath)
	_ = os.Remove(timerFullPath)

	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	cmd.Env = os.Environ()
	_ = cmd.Run()
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

	timerUnit := fmt.Sprintf("pbs-plus-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))

	cmd := exec.Command("systemctl", "list-timers", "--all", "|", "grep", timerUnit)
	cmd.Env = os.Environ()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("getNextSchedule: error running systemctl command -> %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	layout := "Mon 2006-01-02 15:04:05 MST"

	scanner.Scan()

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 10 {
			continue
		}

		nextStr := strings.Join(fields[0:4], " ")
		if strings.TrimSpace(nextStr) == "-" {
			return nil, nil
		}

		nextTime, err := time.Parse(layout, nextStr)
		if err != nil {
			return nil, fmt.Errorf("getNextSchedule: error parsing Next time -> %w", err)
		}

		return &nextTime, nil
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("getNextSchedule: error reading command output -> %w", err)
	}

	return nil, nil
}

func (store *Store) SetSchedule(job Job) {
	svcPath := fmt.Sprintf("pbs-plus-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullSvcPath := filepath.Join(TimerBasePath, svcPath)

	timerPath := fmt.Sprintf("pbs-plus-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullTimerPath := filepath.Join(TimerBasePath, timerPath)

	if job.Schedule == "" {
		cmd := exec.Command("/usr/bin/systemctl", "disable", "--now", timerPath)
		cmd.Env = os.Environ()
		_ = cmd.Run()

		_ = os.Remove(fullSvcPath)
		_ = os.Remove(fullTimerPath)
	} else {
		err := generateService(&job)
		if err != nil {
			syslog.L.Errorf("SetSchedule: error generating service -> %v", err)
			return
		}

		err = generateTimer(&job)
		if err != nil {
			syslog.L.Errorf("SetSchedule: error generating timer -> %v", err)
			return
		}
	}

	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	cmd.Env = os.Environ()
	err := cmd.Run()
	if err != nil {
		syslog.L.Errorf("SetSchedule: error running daemon reload -> %v", err)
		return
	}

	if job.Schedule == "" {
		return
	}

	cmd = exec.Command("/usr/bin/systemctl", "enable", "--now", timerPath)
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err != nil {
		syslog.L.Errorf("SetSchedule: error running enable -> %v", err)
		return
	}

	return
}
