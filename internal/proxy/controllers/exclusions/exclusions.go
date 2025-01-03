//go:build linux

package exclusions

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/sonroyaalmerol/pbs-plus/internal/proxy"
	"github.com/sonroyaalmerol/pbs-plus/internal/proxy/controllers"
	"github.com/sonroyaalmerol/pbs-plus/internal/store"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
)

func D2DExclusionHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request, map[string]string) {
	return func(w http.ResponseWriter, r *http.Request, pathVar map[string]string) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		if err := storeInstance.CheckProxyAuth(r); err != nil {
			http.Error(w, "authentication failed - no authentication credentials provided", http.StatusUnauthorized)
			return
		}

		if r.Method == http.MethodGet {
			all, err := storeInstance.GetAllGlobalExclusions()
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			digest, err := utils.CalculateDigest(all)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			toReturn := ExclusionsResponse{
				Data:   all,
				Digest: digest,
			}

			proxy.ExtractTokenFromRequest(r, storeInstance)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(toReturn)

			return
		}
	}
}

func ExtJsExclusionHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request, map[string]string) {
	return func(w http.ResponseWriter, r *http.Request, pathVar map[string]string) {
		response := ExclusionConfigResponse{}
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		if err := storeInstance.CheckProxyAuth(r); err != nil {
			http.Error(w, "authentication failed - no authentication credentials provided", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		err := r.ParseForm()
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		newExclusion := store.Exclusion{
			Path:    r.FormValue("path"),
			Comment: r.FormValue("comment"),
		}

		err = storeInstance.CreateExclusion(newExclusion)
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		proxy.ExtractTokenFromRequest(r, storeInstance)

		response.Status = http.StatusOK
		response.Success = true
		json.NewEncoder(w).Encode(response)
	}
}

func ExtJsExclusionSingleHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request, map[string]string) {
	return func(w http.ResponseWriter, r *http.Request, pathVar map[string]string) {
		response := ExclusionConfigResponse{}
		if r.Method != http.MethodPut && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		if err := storeInstance.CheckProxyAuth(r); err != nil {
			http.Error(w, "authentication failed - no authentication credentials provided", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPut {
			err := r.ParseForm()
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			pathDecoded, err := url.QueryUnescape(pathVar["exclusion"])
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}
			exclusion, err := storeInstance.GetExclusion(pathDecoded)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			if r.FormValue("path") != "" {
				exclusion.Path = r.FormValue("path")
			}
			if r.FormValue("comment") != "" {
				exclusion.Comment = r.FormValue("comment")
			}

			if delArr, ok := r.Form["delete"]; ok {
				for _, attr := range delArr {
					switch attr {
					case "path":
						exclusion.Path = ""
					case "comment":
						exclusion.Comment = ""
					}
				}
			}

			err = storeInstance.UpdateExclusion(*exclusion)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			proxy.ExtractTokenFromRequest(r, storeInstance)

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodGet {
			pathDecoded, err := url.QueryUnescape(pathVar["exclusion"])
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			exclusion, err := storeInstance.GetExclusion(pathDecoded)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			proxy.ExtractTokenFromRequest(r, storeInstance)

			response.Status = http.StatusOK
			response.Success = true
			response.Data = exclusion
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodDelete {
			pathDecoded, err := url.QueryUnescape(pathVar["exclusion"])
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			err = storeInstance.DeleteExclusion(pathDecoded)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			proxy.ExtractTokenFromRequest(r, storeInstance)

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)
			return
		}
	}
}
