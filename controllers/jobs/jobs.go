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

	"github.com/sonroyaalmerol/pbs-d2d-backup/store"
	"github.com/sonroyaalmerol/pbs-d2d-backup/utils"
)

func RunJob(job *store.Job, storeInstance *store.Store, token *store.Token) (*store.Task, error) {
	if token != nil {
		storeInstance.LastToken = token
	}

	fmt.Printf("Job started: %s\n", job.ID)

	target, err := storeInstance.GetTarget(job.Target)
	if err != nil {
		return nil, err
	}

	srcPath := target.Path

	if strings.HasPrefix(target.Path, "smb://") {
		smbPath := strings.TrimPrefix(target.Path, "smb:")

		srcPath = fmt.Sprintf("/mnt/pbs-d2d-mounts/%s", strings.ReplaceAll(target.Name, " ", "-"))

		err := os.MkdirAll(srcPath, 0700)
		if err != nil {
			return nil, err
		}

		mountArgs := []string{
			"-t",
			"cifs",
			smbPath,
			srcPath,
			"-o",
			fmt.Sprintf("domain=%s,username=%s,password=%s,mfsymlinks,ro,mapchars", os.Getenv("DOMAIN"), os.Getenv("DOMAIN_USER"), os.Getenv("DOMAIN_PASS")),
		}

		mnt := exec.Command("mount", mountArgs...)
		mnt.Env = os.Environ()

		mnt.Stdout = os.Stdout
		mnt.Stderr = os.Stderr

		fmt.Printf("Mount command composed: %s\n", mnt.String())

		err = mnt.Start()
		if err != nil {
			return nil, err
		}
	}

	cmdArgs := []string{
		"backup",
		fmt.Sprintf("%s.pxar:%s", strings.ReplaceAll(job.Target, " ", "-"), srcPath),
		"--repository",
		job.Store,
		"--change-detection-mode=metadata",
		"--exclude",
		"System Volume Information",
		"--exclude",
		"$RECYCLE.BIN",
	}

	if job.Namespace != "" {
		cmdArgs = append(cmdArgs, "--ns")
		cmdArgs = append(cmdArgs, job.Namespace)
	}

	cmd := exec.Command("/usr/bin/proxmox-backup-client", cmdArgs...)
	cmd.Env = os.Environ()

	logBuffer := bytes.Buffer{}
	writer := io.MultiWriter(os.Stdout, &logBuffer)

	cmd.Stdout = writer
	cmd.Stderr = writer

	fmt.Printf("Command composed: %s\n", cmd.String())

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	for {
		line, err := logBuffer.ReadString('\n')
		if err != nil && line != "" {
			return nil, err
		}

		if strings.Contains(line, "Starting backup protocol") {
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	task, err := store.GetMostRecentTask(job, storeInstance.LastToken)
	if err != nil {
		fmt.Printf("error getting task: %v\n", err)

		_ = cmd.Process.Kill()

		return nil, err
	}

	job.LastRunUpid = &task.UPID
	job.LastRunState = &task.Status

	err = storeInstance.UpdateJob(*job)
	if err != nil {
		fmt.Printf("error updating job: %v\n", err)

		_ = cmd.Process.Kill()

		return nil, err
	}
	fmt.Printf("Updated job: %s\n", job.ID)

	go func() {
		fmt.Printf("cmd wait goroutine started\n")
		err = cmd.Wait()
		if err != nil {
			log.Printf("%s\n", err)
		}

		fmt.Printf("done waiting, closing task\n")

		taskFound, err := store.GetTaskByUPID(task.UPID, storeInstance.LastToken)
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

	return task, nil
}

func D2DJobHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		storeInstance.LastToken = utils.ExtractTokenFromRequest(r)

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
			w.Header().Set("Content-Type", "application/json")
			response.Message = err.Error()
			response.Status = http.StatusBadRequest
			response.Success = false
			json.NewEncoder(w).Encode(response)

			return
		}

		task, err := RunJob(job, storeInstance, utils.ExtractTokenFromRequest(r))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			response.Message = err.Error()
			response.Status = http.StatusBadRequest
			response.Success = false
			json.NewEncoder(w).Encode(response)

			return
		}

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
			Namespace:        r.FormValue("namespace"),
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

		storeInstance.LastToken = utils.ExtractTokenFromRequest(r)

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
		storeInstance.LastToken = utils.ExtractTokenFromRequest(r)

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
