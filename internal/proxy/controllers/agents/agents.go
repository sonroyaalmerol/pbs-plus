//go:build linux

package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/logger"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/proxy/controllers"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
	"golang.org/x/crypto/ssh"
)

type LogRequest struct {
	Hostname string `json:"hostname"`
	Message  string `json:"message"`
}

func AgentLogHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		syslogger, err := logger.InitializeSyslogger()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}

		var reqParsed LogRequest
		err = json.NewDecoder(r.Body).Decode(&reqParsed)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}

		syslogger.Info(fmt.Sprintf("PBS Agent Log [%s]: %s", reqParsed.Hostname, reqParsed.Message))

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}
	}
}

func AgentPingHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		agentId := r.PathValue("agent_id")
		agentTarget, err := storeInstance.GetTarget(agentId)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			controllers.WriteErrorResponse(w, err)
			return
		}

		privKeyDir := filepath.Join(store.DbBasePath, "agent_keys")
		privKeyFile := filepath.Join(privKeyDir, strings.ReplaceAll(fmt.Sprintf("%s.key", agentTarget.Name), " ", "-"))

		pemBytes, err := os.ReadFile(privKeyFile)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}

		signer, err := ssh.ParsePrivateKey(pemBytes)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}

		agentPath := strings.TrimPrefix(agentTarget.Path, "agent://")
		agentPathParts := strings.Split(agentPath, "/")
		agentHost := agentPathParts[0]
		agentDrive := agentPathParts[1]
		agentDriveRune := []rune(agentDrive)[0]
		agentPort, err := utils.DriveLetterPort(agentDriveRune)

		sshConfig := &ssh.ClientConfig{
			User:            "proxmox",
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}

		client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", agentHost, agentPort), sshConfig)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}
		defer client.Close()

		session, err := client.NewSession()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}
		defer session.Close()

		pong, err := session.SendRequest("ping", true, []byte{})
		if err != nil || !pong {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			controllers.WriteErrorResponse(w, err)
			return
		}
	}
}
