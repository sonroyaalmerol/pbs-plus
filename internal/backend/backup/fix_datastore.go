//go:build linux

package backup

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
)

func FixDatastore(job *store.Job, storeInstance *store.Store) error {
	if storeInstance == nil {
		return fmt.Errorf("FixDatastore: store is required")
	}

	if storeInstance.APIToken == nil {
		return fmt.Errorf("FixDatastore: api token is required")
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	cmdArgs := []string{
		"change-owner",
		fmt.Sprintf("host/%s", hostname),
		storeInstance.APIToken.TokenId,
		"--repository",
		job.Store,
	}

	if job.Namespace != "" {
		cmdArgs = append(cmdArgs, "--ns")
		cmdArgs = append(cmdArgs, job.Namespace)
	}

	cmd := exec.Command("/usr/bin/proxmox-backup-client", cmdArgs...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PBS_PASSWORD=%s", storeInstance.APIToken.Value))

	pbsStatus, err := store.GetPBSStatus(storeInstance.LastToken, storeInstance.APIToken)
	if err == nil {
		if fingerprint, ok := pbsStatus.Info["fingerprint"]; ok {
			cmd.Env = append(cmd.Env, fmt.Sprintf("PBS_FINGERPRINT=%s", fingerprint))
		}
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("FixDatastore: proxmox-backup-client change-owner error -> %w", err)
	}
	return nil
}
