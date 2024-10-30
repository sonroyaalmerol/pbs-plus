package logging

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type Task struct {
	UPID       string
	Status     string
	TaskType   string
	UserName   string
	Node       string
	PID        int
	PStart     int
	UnknownHex string
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	Target     string
	BaseDir    string

	file *os.File
}

func Initialize(pid int, target, username, node string) *Task {
	return &Task{
		Node:      node,
		PID:       pid,
		PStart:    os.Getpid(),
		StartTime: time.Now(),
		Target:    target,
		TaskType:  "d2dbackup",
		UserName:  username,
		BaseDir:   "/var/log/proxmox-backup/tasks",
	}
}

// GenerateUPID creates a UPID for a new task.
func (task *Task) generateUPID() {
	startTimeHex := strconv.FormatInt(time.Now().Unix(), 16)
	pidHex := fmt.Sprintf("%08X", task.PID)
	pstartHex := fmt.Sprintf("%08X", task.PStart)
	unknownHex := "00000000"

	task.UPID = fmt.Sprintf("UPID:%s:%s:%s:%s:%s:%s:%s:%s:", task.Node, pidHex, pstartHex, unknownHex, startTimeHex, task.TaskType, task.Target, task.UserName)
}

func (task *Task) getLogFolder() string {
	pstartHex := fmt.Sprintf("%08X", task.PStart)
	return pstartHex[len(pstartHex)-2:]
}

func (task *Task) GetLogger() (*log.Logger, error) {
	task.generateUPID()
	folder := task.getLogFolder()
	logPath := filepath.Join(task.BaseDir, folder, task.UPID)

	if err := os.MkdirAll(filepath.Join(task.BaseDir, folder), 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	var err error
	task.file, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file for update: %w", err)
	}

	logger := log.New(task.file, "", log.LstdFlags)

	activePath := filepath.Join(task.BaseDir, "active")
	active, err := os.OpenFile(activePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open active: %w", err)
	}
	defer active.Close()

	active.WriteString(fmt.Sprintf("\n%s", task.UPID))

	return logger, nil
}

func (task *Task) Close() error {
	task.file.Close()

	activePath := filepath.Join(task.BaseDir, "active")

	cmd := exec.Command("/usr/bin/sed", "-i", fmt.Sprintf("/%s/d", task.UPID), activePath)
	_, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to update active: %w", err)
	}

	archivePath := filepath.Join(task.BaseDir, "archive")
	archive, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archive.Close()

	task.EndTime = time.Now()

	endTimeHex := strconv.FormatInt(task.EndTime.Unix(), 16)

	archive.WriteString(fmt.Sprintf("\n%s %s %s", task.UPID, endTimeHex, task.Status))

	return nil
}
