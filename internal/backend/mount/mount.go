//go:build linux

package mount

import (
	"encoding/base32"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/pbs-plus/internal/store"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/constants"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/proxmox"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/types"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
)

type AgentMount struct {
	Hostname string
	Drive    string
	Path     string
	Cmd      *exec.Cmd
	NSDir    string
}

func Mount(storeInstance *store.Store, target *types.Target) (*AgentMount, error) {
	// Parse target information
	splittedTargetName := strings.Split(target.Name, " - ")
	targetHostname := splittedTargetName[0]
	agentPath := strings.TrimPrefix(target.Path, "agent://")
	agentPathParts := strings.Split(agentPath, "/")
	agentHost := agentPathParts[0]
	agentDrive := agentPathParts[1]

	// Encode hostname and drive for API call
	targetHostnameEnc := base32.StdEncoding.EncodeToString([]byte(targetHostname))
	agentDriveEnc := base32.StdEncoding.EncodeToString([]byte(agentDrive))

	// Request mount from agent
	err := proxmox.Session.ProxmoxHTTPRequest(
		http.MethodPost,
		fmt.Sprintf("https://localhost:8008/plus/mount/%s/%s", targetHostnameEnc, agentDriveEnc),
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("Mount: Failed to send mount request to target '%s' -> %w", target.Name, err)
	}

	agentMount := &AgentMount{
		Hostname: targetHostname,
		Drive:    agentDrive,
	}

	// Get port for NFS connection
	agentDriveRune := []rune(agentDrive)[0]
	agentPort, err := utils.DriveLetterPort(agentDriveRune)
	if err != nil {
		agentMount.Unmount()
		return nil, fmt.Errorf("Mount: error mapping \"%c\" to network port -> %w", agentDriveRune, err)
	}

	// Setup mount path
	agentMount.Path = filepath.Join(constants.AgentMountBasePath, strings.ReplaceAll(target.Name, " ", "-"))
	agentMount.Unmount() // Ensure clean mount point

	// Create mount directory if it doesn't exist
	err = os.MkdirAll(agentMount.Path, 0700)
	if err != nil {
		return nil, fmt.Errorf("Mount: error creating directory \"%s\" -> %w", agentMount.Path, err)
	}

	// Create namespace directory
	NSDir := filepath.Join("/tmp/ns", base32.StdEncoding.EncodeToString([]byte(target.Name)))
	if err := os.MkdirAll(NSDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create ns dir: %w", err)
	}

	// Setup bind mount and make private
	bindCmd := exec.Command("mount", "--bind", NSDir, NSDir)
	if err := bindCmd.Run(); err != nil {
		os.RemoveAll(NSDir)
		return nil, fmt.Errorf("failed bind mount: %w", err)
	}

	privateCmd := exec.Command("mount", "--make-private", NSDir)
	if err := privateCmd.Run(); err != nil {
		exec.Command("umount", NSDir).Run()
		os.RemoveAll(NSDir)
		return nil, fmt.Errorf("failed make private: %w", err)
	}

	// Create namespace files
	if err := os.WriteFile(filepath.Join(NSDir, "mnt"), []byte{}, 0600); err != nil {
		exec.Command("umount", NSDir).Run()
		os.RemoveAll(NSDir)
		return nil, fmt.Errorf("failed create ns file: %w", err)
	}

	// Modify mount args to use persistent namespace
	mountArgs := []string{
		fmt.Sprintf("--mount=%s/mnt", NSDir),
		"--fork",
		"--mount-proc",
		"sh", "-c",
		fmt.Sprintf("mount -t nfs -o port=%s,mountport=%s,vers=3,ro,tcp,noacl,lookupcache=none,noac %s:/ %s",
			agentPort, agentPort, agentHost, agentMount.Path),
	}

	// Store NSDir in struct for cleanup
	agentMount.NSDir = NSDir

	mnt := exec.Command("unshare", mountArgs...)
	mnt.Env = os.Environ()
	mnt.Stdout = os.Stdout
	mnt.Stderr = os.Stderr

	agentMount.Cmd = mnt

	// Try mounting with retries
	const maxRetries = 3
	const retryDelay = 2 * time.Second

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err = mnt.Run()
		if err == nil {
			return agentMount, nil
		}
		lastErr = err
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// If all retries failed, clean up and return error
	agentMount.Unmount()
	return nil, fmt.Errorf("Mount: error mounting NFS share after %d attempts -> %w", maxRetries, lastErr)
}

func (a *AgentMount) Unmount() {
	if a.Path == "" {
		return
	}

	// Use persistent namespace for unmount
	unmountCmd := exec.Command("nsenter",
		fmt.Sprintf("--mount=%s/mnt", a.NSDir),
		"--",
		"umount", "-f", "-l", a.Path)
	unmountCmd.Run()

	// Cleanup namespace
	exec.Command("umount", a.NSDir).Run()
	os.RemoveAll(a.NSDir)

	a.Cmd.Process.Kill()
	a.Cmd.Wait()
	os.Remove(a.Path)
}

func (a *AgentMount) CloseMount() {
	targetHostnameEnc := base32.StdEncoding.EncodeToString([]byte(a.Hostname))
	agentDriveEnc := base32.StdEncoding.EncodeToString([]byte(a.Drive))

	_ = proxmox.Session.ProxmoxHTTPRequest(
		http.MethodDelete,
		fmt.Sprintf("https://localhost:8008/plus/mount/%s/%s", targetHostnameEnc, agentDriveEnc),
		nil,
		nil,
	)
}
