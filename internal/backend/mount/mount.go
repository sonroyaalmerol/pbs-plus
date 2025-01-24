//go:build linux

package mount

import (
	"context"
	"encoding/base32"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	ctx      context.Context
	cancel   context.CancelFunc
}

func Mount(ctx context.Context, storeInstance *store.Store, target *types.Target) (*AgentMount, error) {
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

	mountCtx, cancel := context.WithCancel(ctx)
	agentMount := &AgentMount{
		Hostname: targetHostname,
		Drive:    agentDrive,
		ctx:      mountCtx,
		cancel:   cancel,
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

	// Create pipe for synchronization
	r, w, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("Mount: failed to create pipe: %w", err)
	}
	defer r.Close()
	defer w.Close()

	mountArgs := []string{
		"-m", "--propagation", "private",
		"sh", "-c",
		fmt.Sprintf("mount -t nfs -o port=%s,mountport=%s,vers=3,ro,tcp,noacl,lookupcache=none,noac %s:/ %s && echo ready > /dev/fd/3 && while true; do sleep 86400; done",
			agentPort, agentPort, agentHost, agentMount.Path),
	}

	mnt := exec.CommandContext(mountCtx, "unshare", mountArgs...)
	mnt.Env = os.Environ()
	mnt.Stdout = os.Stdout
	mnt.Stderr = os.Stderr
	mnt.ExtraFiles = []*os.File{w}
	mnt.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS,
	}

	agentMount.Cmd = mnt

	// Start the mount process
	if err := mnt.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("Mount: failed to start mount process: %w", err)
	}

	// Wait for mount with timeout
	readyChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 5)
		_, err := r.Read(buf)
		readyChan <- err
	}()

	select {
	case err := <-readyChan:
		if err != nil {
			mnt.Process.Kill()
			return nil, fmt.Errorf("Mount: mount failed: %w", err)
		}
	case <-mountCtx.Done():
		return nil, fmt.Errorf("Mount: context canceled")
	case <-time.After(10 * time.Second):
		mnt.Process.Kill()
		return nil, fmt.Errorf("Mount: timeout waiting for mount")
	}

	return agentMount, nil
}

func (a *AgentMount) Unmount() {
	if a.Path == "" || a.Cmd == nil || a.Cmd.Process == nil {
		return
	}

	unmountCmd := exec.Command("nsenter",
		"--mount=/proc/"+fmt.Sprintf("%d", a.Cmd.Process.Pid)+"/ns/mnt",
		"--",
		"umount", "-f", "-l", a.Path)
	unmountCmd.Run()

	if a.cancel != nil {
		a.cancel()
	}

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
