package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"sgl.com/pbs-ui/store"
	"sgl.com/pbs-ui/utils"
)

func D2DJobHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		allJobs, err := storeInstance.GetAllJobs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		digest, err := utils.CalculateDigest(allJobs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		toReturn := JobsResponse{
			Data:   allJobs,
			Digest: digest,
		}

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
			response.Message = err.Error()
			response.Status = http.StatusNotFound
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		fmt.Printf("Job started: %s\n", job.ID)

		target, err := storeInstance.GetTarget(job.Target)
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusNotFound
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		fmt.Printf("Target found: %s\n", target.Name)

		cmd := exec.Command(
			"/usr/bin/proxmox-backup-client",
			"backup",
			fmt.Sprintf("%s.pxar:%s", strings.ReplaceAll(job.Target, " ", "-"), target.Path),
			"--repository",
			job.Store,
			"--change-detection-mode=metadata",
			"--exclude",
			"System Volume Information",
			"--exclude",
			"$RECYCLE.BIN",
		)
		cmd.Env = os.Environ()

		logBuffer := bytes.Buffer{}
		writer := io.MultiWriter(os.Stdout, &logBuffer)

		cmd.Stdout = writer
		cmd.Stderr = writer

		fmt.Printf("Command composed: %s\n", cmd.String())

		err = cmd.Start()
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		for {
			line, err := logBuffer.ReadString('\n')
			if err != nil && line != "" {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			if strings.Contains(line, "Starting backup protocol") {
				break
			}

			time.Sleep(time.Millisecond * 500)
		}

		task, err := GetMostRecentTask(job, r)
		if err != nil {
			fmt.Printf("error getting task: %v\n", err)

			_ = cmd.Process.Kill()

			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		job.LastRunUpid = &task.UPID
		job.LastRunState = &task.Status

		err = storeInstance.UpdateJob(*job)
		if err != nil {
			fmt.Printf("error updating job: %v\n", err)

			_ = cmd.Process.Kill()

			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}
		fmt.Printf("Updated job: %s\n", job.ID)

		go func() {
			fmt.Printf("cmd wait goroutine started\n")
			err = cmd.Wait()
			if err != nil {
				log.Printf("%s\n", err)
			}

			fmt.Printf("done waiting, closing task\n")

			taskFound, err := GetTaskByUPID(task.UPID, r)
			if err != nil {
				fmt.Printf("error updating job: %v\n", err)
				return
			}

			job.LastRunState = &taskFound.Status
			job.LastRunEndtime = &taskFound.EndTime

			fmt.Printf("Updated job: %s\n", job.ID)
			err = storeInstance.UpdateJob(*job)
			if err != nil {
				fmt.Printf("error updating job: %v\n", err)
				return
			}
		}()

		w.Header().Set("Content-Type", "application/json")
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
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		newJob := store.Job{
			ID:               r.FormValue("id"),
			Store:            r.FormValue("store"),
			Target:           r.FormValue("target"),
			Schedule:         r.FormValue("schedule"),
			Comment:          r.FormValue("comment"),
			NotificationMode: r.FormValue("notification-mode"),
		}

		err = storeInstance.CreateJob(newJob)
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
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			err = r.ParseForm()
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
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
					case "notification-mode":
						job.NotificationMode = ""
					}
				}
			}

			err = storeInstance.UpdateJob(*job)
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
			job, err := storeInstance.GetJob(r.PathValue("job"))
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			response.Status = http.StatusOK
			response.Success = true
			response.Data = job
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodDelete {
			err := storeInstance.DeleteJob(r.PathValue("job"))
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
