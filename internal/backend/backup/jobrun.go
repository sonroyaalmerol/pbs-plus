package backup

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/backend/mount"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
)

func RunBackup(job *store.Job, storeInstance *store.Store, token *store.Token) (*store.Task, error) {
	if token != nil {
		storeInstance.LastToken = token
	}

	target, err := storeInstance.GetTarget(job.Target)
	if err != nil {
		return nil, fmt.Errorf("RunBackup -> %w", err)
	}

	srcPath := target.Path

	var agentMount *mount.AgentMount
	if strings.HasPrefix(target.Path, "agent://") {
		agentMount, err = mount.Mount(target)
		if err != nil {
			return nil, fmt.Errorf("RunBackup: mount initialization error -> %w", err)
		}
		err = agentMount.Cmd.Wait()
		if err != nil {
			return nil, fmt.Errorf("RunBackup: mount wait error -> %w", err)
		}

		srcPath = agentMount.Path
	}

	cmdArgs := []string{
		"backup",
		fmt.Sprintf("%s.pxar:%s", strings.ReplaceAll(job.Target, " ", "-"), srcPath),
		"--repository",
		job.Store,
		"--change-detection-mode=metadata",
	}

	if job.Namespace != "" {
		cmdArgs = append(cmdArgs, "--ns")
		cmdArgs = append(cmdArgs, job.Namespace)
	}

	cmd := exec.Command("/usr/bin/proxmox-backup-client", cmdArgs...)
	cmd.Env = os.Environ()

	logBuffer := bytes.Buffer{}
	writer := io.MultiWriter(os.Stdout, &logBuffer)

	cmd.Stdout = writer
	cmd.Stderr = writer

	err = cmd.Start()
	if err != nil {
		if agentMount != nil {
			agentMount.Unmount()
		}
		return nil, fmt.Errorf("RunBackup: proxmox-backup-client start error -> %w", err)
	}

	for {
		line, err := logBuffer.ReadString('\n')
		if err != nil && line != "" {
			return nil, fmt.Errorf("RunBackup: log buffer readString error -> %w", err)
		}

		if strings.Contains(line, "Starting backup protocol") {
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	task, err := store.GetMostRecentTask(job, storeInstance.LastToken)
	if err != nil {
		_ = cmd.Process.Kill()
		if agentMount != nil {
			agentMount.Unmount()
		}

		return nil, fmt.Errorf("RunBackup: unable to get most recent task -> %w", err)
	}

	job.LastRunUpid = &task.UPID
	job.LastRunState = &task.Status

	err = storeInstance.UpdateJob(*job)
	if err != nil {
		_ = cmd.Process.Kill()
		if agentMount != nil {
			agentMount.Unmount()
		}

		return nil, fmt.Errorf("RunBackup: unable to update job -> %w", err)
	}

	go func() {
		if agentMount != nil {
			defer agentMount.Unmount()
		}
		err = cmd.Wait()
		if err != nil {
			log.Printf("RunBackup (goroutine): error waiting for backup -> %v\n", err)
		}

		taskFound, err := store.GetTaskByUPID(task.UPID, storeInstance.LastToken)
		if err != nil {
			log.Printf("RunBackup (goroutine): unable to get task by UPID -> %v\n", err)
			return
		}

		job.LastRunState = &taskFound.Status
		job.LastRunEndtime = &taskFound.EndTime

		err = storeInstance.UpdateJob(*job)
		if err != nil {
			log.Printf("RunBackup (goroutine): unable to update job -> %v\n", err)
			return
		}
	}()

	return task, nil
}
