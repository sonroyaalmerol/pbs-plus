//go:build linux

package backup

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/alexflint/go-filemutex"
	"github.com/sonroyaalmerol/pbs-plus/internal/backend/mount"
	"github.com/sonroyaalmerol/pbs-plus/internal/store"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/proxmox"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/types"
)

func cleanupResources(stdout, stderr io.ReadCloser, agentMount *mount.AgentMount) {
	if stdout != nil {
		stdout.Close()
	}
	if stderr != nil {
		stderr.Close()
	}
	if agentMount != nil {
		agentMount.Unmount()
		agentMount.CloseMount()
	}
}

func RunBackup(job *types.Job, storeInstance *store.Store, skipCheck bool) (*proxmox.Task, error) {
	backupMutex, err := filemutex.New("/tmp/pbs-plus-mutex-lock")
	if err != nil {
		return nil, fmt.Errorf("RunBackup: failed to create mutex lock: %w", err)
	}
	defer backupMutex.Close() // Ensure mutex is always closed

	if err := backupMutex.Lock(); err != nil {
		return nil, fmt.Errorf("RunBackup: failed to acquire lock: %w", err)
	}
	defer backupMutex.Unlock()

	if proxmox.Session.APIToken == nil {
		return nil, fmt.Errorf("RunBackup: api token is required")
	}

	// Validate and setup target
	target, err := storeInstance.Database.GetTarget(job.Target)
	if err != nil {
		return nil, fmt.Errorf("RunBackup: failed to get target: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("RunBackup: Target '%s' does not exist", job.Target)
	}

	if !skipCheck && !storeInstance.WSHub.AgentPing(target) {
		return nil, fmt.Errorf("RunBackup: Target '%s' is unreachable or does not exist", job.Target)
	}

	srcPath := target.Path
	isAgent := strings.HasPrefix(target.Path, "agent://")

	var agentMount *mount.AgentMount
	if isAgent {
		if agentMount, err = mount.Mount(context.Background(), storeInstance, target); err != nil {
			return nil, fmt.Errorf("RunBackup: mount initialization error: %w", err)
		}
		srcPath = agentMount.Path
	}

	cmd, err := prepareBackupCommand(job, storeInstance, srcPath, isAgent)
	if err != nil {
		cleanupResources(nil, nil, agentMount)
		return nil, err
	}

	if isAgent {
		// Set up nsenter to enter the mount namespace
		nsenterCmd := exec.Command("nsenter",
			"--mount=/proc/"+fmt.Sprintf("%d", agentMount.Cmd.Process.Pid)+"/ns/mnt",
			"--",
			cmd.Path,
		)
		nsenterCmd.Args = append(nsenterCmd.Args, cmd.Args[1:]...)
		nsenterCmd.Env = cmd.Env
		nsenterCmd.Stdout = cmd.Stdout
		nsenterCmd.Stderr = cmd.Stderr

		// Replace original command with nsenter wrapped version
		cmd = nsenterCmd
	}

	// Setup command pipes
	stdout, stderr, err := setupCommandPipes(cmd)
	if err != nil {
		cleanupResources(nil, nil, agentMount)
		return nil, err
	}

	// Start monitoring in background first
	monitorCtx, monitorCancel := context.WithTimeout(context.Background(), time.Minute)

	var task *proxmox.Task
	var monitorErr error

	readyChan := make(chan struct{})
	go func() {
		defer monitorCancel()
		task, monitorErr = proxmox.Session.GetJobTask(monitorCtx, readyChan, job, target)
	}()

	select {
	case <-readyChan:
	case <-monitorCtx.Done():
		cleanupResources(stdout, stderr, agentMount)

		return nil, fmt.Errorf("RunBackup: task monitoring crashed -> %w", monitorErr)
	}

	currOwner, _ := GetCurrentOwner(job, storeInstance)
	_ = FixDatastore(job, storeInstance)

	// Start collecting logs and wait for backup completion
	var logLines []string
	var logGlobalMu sync.Mutex

	go func() {
		logGlobalMu.Lock()
		defer logGlobalMu.Unlock()

		logLines = collectLogs(stdout, stderr)
	}()

	// Now start the backup process
	if err := cmd.Start(); err != nil {
		monitorCancel() // Cancel monitoring since backup failed to start
		cleanupResources(stdout, stderr, agentMount)

		if currOwner != "" {
			_ = SetDatastoreOwner(job, storeInstance, currOwner)
		}

		return nil, fmt.Errorf("RunBackup: proxmox-backup-client start error (%s): %w", cmd.String(), err)
	}

	// Wait for either monitoring to complete or timeout
	select {
	case <-monitorCtx.Done():
		if task == nil {
			cleanupResources(stdout, stderr, agentMount)

			if currOwner != "" {
				_ = SetDatastoreOwner(job, storeInstance, currOwner)
			}

			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("RunBackup: no task created")
		}
	}

	if err := updateJobStatus(job, task, storeInstance); err != nil {
		cleanupResources(stdout, stderr, agentMount)

		if currOwner != "" {
			_ = SetDatastoreOwner(job, storeInstance, currOwner)
		}

		return task, fmt.Errorf("RunBackup: failed to update job status: %w", err)
	}

	go func() {
		defer cleanupResources(stdout, stderr, agentMount)

		_ = cmd.Wait()

		logGlobalMu.Lock()
		defer logGlobalMu.Unlock()

		if err := writeLogsToFile(task.UPID, logLines); err != nil {
			log.Printf("Failed to write logs: %v", err)
		}

		if err := updateJobStatus(job, task, storeInstance); err != nil {
			log.Printf("RunBackup: failed to update job status: %v", err)
		}

		if currOwner != "" {
			_ = SetDatastoreOwner(job, storeInstance, currOwner)
		}
	}()

	return task, nil
}
