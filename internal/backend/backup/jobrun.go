//go:build linux

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
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/logger"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
)

func RunBackup(job *store.Job, storeInstance *store.Store) (*store.Task, error) {
	target, err := storeInstance.GetTarget(job.Target)
	if err != nil {
		return nil, fmt.Errorf("RunBackup -> %w", err)
	}

	if target == nil {
		return nil, fmt.Errorf("RunBackup: Target '%s' does not exist.", job.Target)
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

	jobStore := job.Store

	if storeInstance.APIToken != nil {
		jobStore = fmt.Sprintf(
			"%s@localhost:%s",
			storeInstance.APIToken.TokenId,
			job.Store,
		)
	}

	err = FixDatastore(job, storeInstance)
	if err != nil {
		return nil, fmt.Errorf("RunBackup: failed to fix datastore permissions -> %w", err)
	}

	cmdArgs := []string{
		"backup",
		fmt.Sprintf("%s.pxar:%s", strings.ReplaceAll(job.Target, " ", "-"), srcPath),
		"--repository",
		jobStore,
		"--change-detection-mode=metadata",
	}

	if job.Namespace != "" {
		cmdArgs = append(cmdArgs, "--ns")
		cmdArgs = append(cmdArgs, job.Namespace)
	}

	cmd := exec.Command("/usr/bin/proxmox-backup-client", cmdArgs...)
	cmd.Env = os.Environ()
	if storeInstance.APIToken != nil {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PBS_PASSWORD=%s", storeInstance.APIToken.Value))
	}

	pbsStatus, err := storeInstance.GetPBSStatus()
	if err == nil {
		if fingerprint, ok := pbsStatus.Info["fingerprint"]; ok {
			cmd.Env = append(cmd.Env, fmt.Sprintf("PBS_FINGERPRINT=%s", fingerprint))
		}
	}

	logBuffer := bytes.Buffer{}
	writer := io.MultiWriter(os.Stdout, &logBuffer)

	cmd.Stdout = writer
	cmd.Stderr = writer

	err = cmd.Start()
	if err != nil {
		if agentMount != nil {
			agentMount.Unmount()
		}
		return nil, fmt.Errorf("RunBackup: proxmox-backup-client start error (%s) -> %w", cmd.String(), err)
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

	task, err := storeInstance.GetMostRecentTask(job)
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
		syslogger, _ := logger.InitializeSyslogger()

		if agentMount != nil {
			defer agentMount.Unmount()
		}
		err = cmd.Wait()
		if err != nil {
			errI := fmt.Sprintf("RunBackup (goroutine): error waiting for backup -> %v", err)
			log.Println(errI)
			if syslogger != nil {
				syslogger.Err(errI)
			}
		}

		taskFound, err := storeInstance.GetTaskByUPID(task.UPID)
		if err != nil {
			errI := fmt.Sprintf("RunBackup (goroutine): unable to get task by UPID -> %v", err)
			log.Println(errI)
			if syslogger != nil {
				syslogger.Err(errI)
			}
			return
		}

		job.LastRunState = &taskFound.Status
		job.LastRunEndtime = &taskFound.EndTime

		err = storeInstance.UpdateJob(*job)
		if err != nil {
			errI := fmt.Sprintf("RunBackup (goroutine): unable to update job -> %v", err)
			log.Println(errI)
			if syslogger != nil {
				syslogger.Err(errI)
			}
			return
		}
	}()

	return task, nil
}
