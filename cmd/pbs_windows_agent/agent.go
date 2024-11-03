//go:build windows
// +build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/getlantern/systray"
	"github.com/kardianos/service"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/agent/sftp"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/agent/snapshots"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
)

//go:embed icon/logo.png
var icon []byte

var logger service.Logger

type AgentProgram struct {
	Exec func()
}

func (p *AgentProgram) Start(s service.Service) error {
	go p.Exec()
	return nil
}
func (p *AgentProgram) Stop(s service.Service) error {
	return nil
}

var svc service.Service

func main() {
	if !isAdmin() {
		fmt.Println("This program needs to be run as administrator.")
		runAsAdmin()
		os.Exit(0)
	}

	svcConfig := &service.Config{
		Name:        "PBSAgent",
		DisplayName: "Proxmox Backup Agent",
		Description: "Orchestrating backups with Proxmox Backup Server",
	}

	serverUrl, ok := os.LookupEnv("PBS_AGENT_SERVER")
	if !ok {
		for {
			serverUrl = promptInput("PBS Agent", "Server URL")

			_, err := url.ParseRequestURI(serverUrl)
			if err == nil {
				serverUrl = strings.TrimSuffix(serverUrl, "/")
				break
			}
		}

		utils.SetEnvironment("PBS_AGENT_SERVER", serverUrl)
	}

	var err error
	svc, err = service.New(
		&AgentProgram{Exec: run(serverUrl)},
		svcConfig,
	)
	if err != nil {
		showMessageBox("Error", err.Error())
		os.Exit(1)
	}

	logger, err = svc.Logger(nil)
	if err != nil {
		showMessageBox("Error", err.Error())
		os.Exit(1)
	}

	svc.Run()
}

func run(serverUrl string) func() {
	return func() {
		_, err := url.ParseRequestURI(serverUrl)
		if err != nil {
			showMessageBox("Error", fmt.Sprintf("Invalid server URL: %s", err))
			os.Exit(1)
		}

		// Reserve port 33450-33476
		drives := utils.GetLocalDrives()
		ctx := context.Background()

		var wg sync.WaitGroup
		for _, driveLetter := range drives {
			rune := []rune(driveLetter)[0]

			sftpConfig, err := sftp.InitializeSFTPConfig(serverUrl, driveLetter)
			if err != nil {
				showMessageBox("Error", fmt.Sprintf("Unable to initialize SFTP: %s", err))
				os.Exit(1)
			}

			err = sftpConfig.PopulateKeys()
			if err != nil {
				showMessageBox("Error", fmt.Sprintf("Unable to populate SFTP keys: %s", err))
				os.Exit(1)
			}

			port, err := utils.DriveLetterPort(rune)
			if err != nil {
				showMessageBox("Error", fmt.Sprintf("Unable to map letter to port: %s", err))
				os.Exit(1)
			}

			wg.Add(1)
			go sftp.Serve(ctx, &wg, sftpConfig, "0.0.0.0", port, fmt.Sprintf("%s:\\", driveLetter))
		}

		defer snapshots.CloseAllSnapshots()

		systray.Run(onReady(serverUrl), onExit)
		defer systray.Quit()

		wg.Wait()
	}
}

func showMessageBox(title, message string) {
	windows.MessageBox(0,
		windows.StringToUTF16Ptr(message),
		windows.StringToUTF16Ptr(title),
		windows.MB_OK|windows.MB_ICONERROR)
}

func promptInput(title, prompt string) string {
	cmd := exec.Command("powershell", "-Command", fmt.Sprintf(`
		[void][Reflection.Assembly]::LoadWithPartialName('Microsoft.VisualBasic');
		$input = [Microsoft.VisualBasic.Interaction]::InputBox('%s', '%s');
    $input`, prompt, title))

	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Failed to get input:", err)
		return ""
	}

	return strings.TrimSpace(string(output))
}

func onReady(serverUrl string) func() {
	return func() {
		systray.SetIcon(icon)
		systray.SetTitle("Proxmox Backup Agent")
		systray.SetTooltip("Orchestrating backups with Proxmox Backup Server")

		url, err := url.Parse(serverUrl)
		if err != nil {
			showMessageBox("Error", fmt.Sprintf("Failed to parse server URL: %s", err))
			os.Exit(1)
		}

		serverIP := systray.AddMenuItem(fmt.Sprintf("Server: %s", url), "Proxmox Backup Server address")
		serverIP.Disable()

		mQuit := systray.AddMenuItem("Quit", "Quit service")

		go func() {
			<-mQuit.ClickedCh
			systray.Quit()
		}()
	}
}

func onExit() {
	snapshots.CloseAllSnapshots()
	_ = svc.Stop()
}

func isAdmin() bool {
	processHandle := windows.CurrentProcess()
	var token windows.Token
	err := windows.OpenProcessToken(processHandle, windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	isMember, err := token.IsMember(sid)
	return err == nil && isMember
}

func runAsAdmin() {
	exe, err := os.Executable()
	if err != nil {
		showMessageBox("Error", fmt.Sprintf("Failed to get executable path: %s", err))
		return
	}
	cmd := exec.Command("runas", "/user:Administrator", exe)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
	err = cmd.Run()
	if err != nil {
		showMessageBox("Error", fmt.Sprintf("Failed to run as administrator: %s", err))
	}
}
