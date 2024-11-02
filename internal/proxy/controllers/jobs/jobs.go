//go:build linux

package jobs

import (
	"encoding/json"
	"net/http"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/backend/backup"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/proxy"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/proxy/controllers"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
)

func D2DJobHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		allJobs, err := storeInstance.GetAllJobs()
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		digest, err := utils.CalculateDigest(allJobs)
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		toReturn := JobsResponse{
			Data:   allJobs,
			Digest: digest,
		}

		storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toReturn)
	}
}

func ExtJsJobRunHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := JobRunResponse{}
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		job, err := storeInstance.GetJob(r.PathValue("job"))
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		task, err := backup.RunBackup(job, storeInstance)
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

		response.Data = task.UPID
		response.Status = http.StatusOK
		response.Success = true
		json.NewEncoder(w).Encode(response)
	}
}

func ExtJsJobHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := JobConfigResponse{}
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		err := r.ParseForm()
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		newJob := store.Job{
			ID:               r.FormValue("id"),
			Store:            r.FormValue("store"),
			Target:           r.FormValue("target"),
			Schedule:         r.FormValue("schedule"),
			Comment:          r.FormValue("comment"),
			Namespace:        r.FormValue("namespace"),
			NotificationMode: r.FormValue("notification-mode"),
		}

		err = storeInstance.CreateJob(newJob)
		if err != nil {
			controllers.WriteErrorResponse(w, err)
			return
		}

		storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

		response.Status = http.StatusOK
		response.Success = true
		json.NewEncoder(w).Encode(response)
	}
}

func ExtJsJobSingleHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := JobConfigResponse{}
		if r.Method != http.MethodPut && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPut {
			job, err := storeInstance.GetJob(r.PathValue("job"))
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			err = r.ParseForm()
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			if r.FormValue("store") != "" {
				job.Store = r.FormValue("store")
			}
			if r.FormValue("target") != "" {
				job.Target = r.FormValue("target")
			}
			if r.FormValue("schedule") != "" {
				job.Schedule = r.FormValue("schedule")
			}
			if r.FormValue("comment") != "" {
				job.Comment = r.FormValue("comment")
			}
			if r.FormValue("namespace") != "" {
				job.Namespace = r.FormValue("namespace")
			}
			if r.FormValue("notification-mode") != "" {
				job.NotificationMode = r.FormValue("notification-mode")
			}

			if delArr, ok := r.Form["delete"]; ok {
				for _, attr := range delArr {
					switch attr {
					case "store":
						job.Store = ""
					case "target":
						job.Target = ""
					case "schedule":
						job.Schedule = ""
					case "comment":
						job.Comment = ""
					case "namespace":
						job.Namespace = ""
					case "notification-mode":
						job.NotificationMode = ""
					}
				}
			}

			err = storeInstance.UpdateJob(*job)
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodGet {
			job, err := storeInstance.GetJob(r.PathValue("job"))
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

			response.Status = http.StatusOK
			response.Success = true
			response.Data = job
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodDelete {
			err := storeInstance.DeleteJob(r.PathValue("job"))
			if err != nil {
				controllers.WriteErrorResponse(w, err)
				return
			}

			storeInstance.LastToken = proxy.ExtractTokenFromRequest(r)

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)
			return
		}
	}
}
