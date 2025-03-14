//go:build linux

package plus

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/sonroyaalmerol/pbs-plus/internal/agent/agentfs/types"
	arpcfs "github.com/sonroyaalmerol/pbs-plus/internal/backend/arpc"
	"github.com/sonroyaalmerol/pbs-plus/internal/backend/arpc/mount"
	"github.com/sonroyaalmerol/pbs-plus/internal/store"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/constants"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
)

func MountHandler(storeInstance *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		targetHostname := utils.DecodePath(r.PathValue("target"))
		agentDrive := utils.DecodePath(r.PathValue("drive"))
		jobId := utils.DecodePath(r.PathValue("jobid"))

		job, err := storeInstance.Database.GetJob(jobId)
		if err != nil {
			http.Error(w, fmt.Sprintf("Unable to get job from id"), http.StatusNotFound)
			return
		}

		if r.Method == http.MethodPost {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			arpcSess, exists := storeInstance.ARPCSessionManager.GetSession(targetHostname)
			if !exists {
				http.Error(w, fmt.Sprintf("MountHandler: Failed to send backup request to target -> unable to reach target"), http.StatusInternalServerError)
				return
			}
			req := types.BackupReq{Drive: agentDrive, JobId: jobId, SourceMode: job.SourceMode}
			backupResp, err := arpcSess.CallContext(ctx, "backup", &req)
			if err != nil || backupResp.Status != 200 {
				if err != nil {
					err = errors.New(backupResp.Message)
					syslog.L.Error(err).Write()
				}
				http.Error(w, fmt.Sprintf("MountHandler: Failed to send backup request to target -> %v", err), http.StatusInternalServerError)
				return
			}

			backupRespSplit := strings.Split(backupResp.Message, "|")
			backupMode := backupRespSplit[0]

			if len(backupRespSplit) == 2 && backupRespSplit[1] != "" {
				job.Namespace = backupRespSplit[1]
				if err := storeInstance.Database.UpdateJob(*job); err != nil {
					syslog.L.Error(err).WithField("namespace", backupRespSplit[1]).Write()
				}
			}

			arpcFS := storeInstance.GetARPCFS(jobId)
			if arpcFS == nil {
				arpcFSRPC, exists := storeInstance.ARPCSessionManager.GetSession(targetHostname + "|" + jobId)
				if !exists {
					http.Error(w, fmt.Sprintf("MountHandler: Failed to send backup request to target -> unable to reach child target"), http.StatusInternalServerError)
					return
				}
				arpcFS = arpcfs.NewARPCFS(context.Background(), arpcFSRPC, targetHostname, jobId, backupMode)
			}

			mntPath := filepath.Join(constants.AgentMountBasePath, jobId)

			err = mount.Mount(arpcFS, mntPath)
			if err != nil {
				syslog.L.Error(err).Write()
				http.Error(w, fmt.Sprintf("MountHandler: Failed to create fuse connection for target -> %v", err), http.StatusInternalServerError)
				return
			}

			storeInstance.AddARPCFS(jobId, arpcFS)

			// Handle successful response
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]string{"status": "true"}); err != nil {
				http.Error(w, fmt.Sprintf("MountHandler: Failed to encode response -> %v", err), http.StatusInternalServerError)
				return
			}
		}

		if r.Method == http.MethodDelete {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			arpcSess, exists := storeInstance.ARPCSessionManager.GetSession(targetHostname)
			if !exists {
				storeInstance.RemoveARPCFS(jobId)
				http.Error(w, fmt.Sprintf("MountHandler: Failed to send closure request to target -> unable to reach target"), http.StatusInternalServerError)
				return
			}

			arpcFS := storeInstance.GetARPCFS(jobId)
			if arpcFS == nil {
				arpcFS = arpcfs.NewARPCFS(context.Background(), arpcSess, targetHostname, jobId, "")
				arpcFS.Unmount()
			} else {
				storeInstance.RemoveARPCFS(jobId)
			}

			req := types.BackupReq{Drive: agentDrive, JobId: jobId}
			cleanupResp, err := arpcSess.CallContext(ctx, "cleanup", &req)
			if err != nil || cleanupResp.Status != 200 {
				if err != nil {
					err = errors.New(cleanupResp.Message)
				}
				http.Error(w, fmt.Sprintf("MountHandler: Failed to send closure request to target -> %v", err), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]string{"status": "true"}); err != nil {
				http.Error(w, fmt.Sprintf("MountHandler: Failed to encode response -> %v", err), http.StatusInternalServerError)
				return
			}

			return
		}
	}
}

//go:embed install-agent.ps1
var scriptFS embed.FS

func AgentInstallScriptHandler(storeInstance *store.Store, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		// Dynamically set ServerUrl based on the incoming request's host
		// Default scheme to HTTPS, but respect X-Forwarded-Proto if available
		scheme := "https"
		if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
			scheme = forwardedProto
		} else if r.TLS == nil {
			// If no X-Forwarded-Proto and no TLS, assume HTTP
			scheme = "http"
		}

		// Use the host from the request, respecting X-Forwarded-Host if available
		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}

		baseServerUrl := fmt.Sprintf("%s://%s", scheme, host)

		config := ScriptConfig{
			ServerUrl:  baseServerUrl,
			AgentUrl:   baseServerUrl + "/api2/json/plus/binary",
			UpdaterUrl: baseServerUrl + "/api2/json/plus/updater-binary",
		}

		if token := r.URL.Query().Get("t"); token != "" {
			config.BootstrapToken = token
		}

		// Read the embedded PowerShell script
		scriptContent, err := scriptFS.ReadFile("install-agent.ps1")
		if err != nil {
			syslog.L.Error(err).Write()
			http.Error(w, "failed to write response body", http.StatusInternalServerError)
			return
		}

		// Parse the template
		tmpl, err := template.New("script").Parse(string(scriptContent))
		if err != nil {
			syslog.L.Error(err).Write()
			http.Error(w, "failed to write response body", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		err = tmpl.Execute(w, config)
		if err != nil {
			syslog.L.Error(err).Write()
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

func VersionHandler(storeInstance *store.Store, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		toReturn := VersionResponse{
			Version: version,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toReturn)
	}
}

const PBS_DOWNLOAD_BASE = "https://github.com/sonroyaalmerol/pbs-plus/releases/download/"

func DownloadBinary(storeInstance *store.Store, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		if version == "v0.0.0" {
			version = "dev"
		}

		// Construct the passthrough URL
		targetURL := fmt.Sprintf("%s%s/pbs-plus-agent-%s-windows-amd64.exe", PBS_DOWNLOAD_BASE, version, version)

		proxyUrl(targetURL, w, r)
	}
}

func DownloadUpdater(storeInstance *store.Store, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		if version == "v0.0.0" {
			version = "dev"
		}

		// Construct the passthrough URL
		targetURL := fmt.Sprintf("%s%s/pbs-plus-updater-%s-windows-amd64.exe", PBS_DOWNLOAD_BASE, version, version)

		proxyUrl(targetURL, w, r)
	}
}

func DownloadChecksum(storeInstance *store.Store, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusMethodNotAllowed)
			return
		}

		if version == "v0.0.0" {
			version = "dev"
		}

		// Construct the passthrough URL
		targetURL := fmt.Sprintf("%s%s/pbs-plus-agent-%s-windows-amd64.exe.md5", PBS_DOWNLOAD_BASE, version, version)

		proxyUrl(targetURL, w, r)
	}
}
