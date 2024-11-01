package targets

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/pbs-d2d-backup/store"
	"github.com/sonroyaalmerol/pbs-d2d-backup/utils"
)

type NewAgentRequest struct {
	PublicKey string `json:"public_key"`
	BasePath  string `json:"base_path"`
	Hostname  string `json:"hostname"`
}

func D2DTargetHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		if r.Method == http.MethodGet {
			all, err := storeInstance.GetAllTargets()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}

			digest, err := utils.CalculateDigest(all)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}

			toReturn := TargetsResponse{
				Data:   all,
				Digest: digest,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(toReturn)

			return
		}

		if r.Method == http.MethodPost {
			var reqParsed NewAgentRequest
			err := json.NewDecoder(r.Body).Decode(&reqParsed)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			err = utils.AddHostToKnownHosts(reqParsed.Hostname, reqParsed.BasePath, reqParsed.PublicKey)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			clientIP := r.RemoteAddr

			forwarded := r.Header.Get("X-FORWARDED-FOR")
			if forwarded != "" {
				clientIP = forwarded
			}

			clientIP = strings.Split(clientIP, ":")[0]

			privKey, pubKey, err := utils.GenerateKeyPair(4096)
			privKeyDir := filepath.Join(store.DbBasePath, "agent_keys")

			err = os.MkdirAll(privKeyDir, 0700)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			newTarget := store.Target{
				Name: fmt.Sprintf("%s - %s", reqParsed.Hostname, reqParsed.BasePath),
				Path: fmt.Sprintf("agent://%s/%s", clientIP, reqParsed.BasePath),
			}

			privKeyFile, err := os.OpenFile(
				filepath.Join(
					privKeyDir,
					strings.ReplaceAll(fmt.Sprintf("%s.key", newTarget.Name), " ", "-"),
				),
				os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)

			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer privKeyFile.Close()

			_, err = privKeyFile.Write(privKey)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			err = storeInstance.CreateTarget(newTarget)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				err = json.NewEncoder(w).Encode(map[string]string{
					"public_key": string(pubKey),
				})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				return
			}
		}
	}
}

func ExtJsTargetHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := TargetConfigResponse{}
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		err := r.ParseForm()
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		if !utils.IsValid(r.FormValue("path")) {
			response.Message = fmt.Sprintf("Invalid path: %s", r.FormValue("path"))
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		newTarget := store.Target{
			Name: r.FormValue("name"),
			Path: r.FormValue("path"),
		}

		err = storeInstance.CreateTarget(newTarget)
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		response.Status = http.StatusOK
		response.Success = true
		json.NewEncoder(w).Encode(response)
	}
}

func ExtJsTargetSingleHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := TargetConfigResponse{}
		if r.Method != http.MethodPut && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPut {
			err := r.ParseForm()
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			if !utils.IsValid(r.FormValue("path")) {
				response.Message = fmt.Sprintf("Invalid path: %s", r.FormValue("path"))
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			target, err := storeInstance.GetTarget(r.PathValue("target"))
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			if r.FormValue("name") != "" {
				target.Name = r.FormValue("name")
			}
			if r.FormValue("path") != "" {
				target.Path = r.FormValue("path")
			}

			if delArr, ok := r.Form["delete"]; ok {
				for _, attr := range delArr {
					switch attr {
					case "name":
						target.Name = ""
					case "path":
						target.Path = ""
					}
				}
			}

			err = storeInstance.UpdateTarget(*target)
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodGet {
			target, err := storeInstance.GetTarget(r.PathValue("target"))
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			response.Status = http.StatusOK
			response.Success = true
			response.Data = target
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodDelete {
			err := storeInstance.DeleteTarget(r.PathValue("target"))
			if err != nil {

				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}
			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)
			return
		}
	}
}
