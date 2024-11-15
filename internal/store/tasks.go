//go:build linux

package store

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TasksResponse struct {
	Data  []Task `json:"data"`
	Total int    `json:"total"`
}

type TaskResponse struct {
	Data  Task `json:"data"`
	Total int  `json:"total"`
}

type Task struct {
	WID        string `json:"id"`
	Node       string `json:"node"`
	PID        int    `json:"pid"`
	PStart     int    `json:"pstart"`
	StartTime  int64  `json:"starttime"`
	EndTime    int64  `json:"endtime"`
	UPID       string `json:"upid"`
	User       string `json:"user"`
	WorkerType string `json:"worker_type"`
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
}

func (storeInstance *Store) GetMostRecentTask(job *Job, since *time.Time) (*Task, error) {
	var resp TasksResponse
	err := storeInstance.ProxmoxHTTPRequest(
		http.MethodGet,
		fmt.Sprintf("/api2/json/nodes/localhost/tasks?store=%s&typefilter=backup&start=0&limit=1&since=%d", job.Store, since.Unix()),
		nil,
		&resp,
	)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentTask: error creating http request -> %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("GetMostRecentTask: error getting tasks: not found")
	}

	if strings.Contains(resp.Data[0].UPID, ":reader:") {
		resp.Data[0].UPID = strings.ReplaceAll(resp.Data[0].UPID, ":reader:", ":backup:")
		splittedUPID := strings.Split(resp.Data[0].UPID, ":")
		currInVal, err := strconv.ParseInt(splittedUPID[4], 16, 64)
		if err != nil {
			return nil, fmt.Errorf("GetMostRecentTask: error converting hex -> %w", err)
		}
		splittedUPID[4] = fmt.Sprintf("%08X", currInVal-1)
		idx := strings.LastIndex(splittedUPID[7], "-")
		if idx != -1 && len(splittedUPID[7][idx+1:]) == 8 {
			splittedUPID[7] = splittedUPID[7][:idx]
		}

		resp.Data[0].UPID = strings.Join(splittedUPID, ":")
		resp.Data[0].WorkerType = "backup"
	}

	return &resp.Data[0], nil
}

func (storeInstance *Store) GetTaskByUPID(upid string) (*Task, error) {
	var resp TaskResponse
	err := storeInstance.ProxmoxHTTPRequest(
		http.MethodGet,
		fmt.Sprintf("/api2/json/nodes/localhost/tasks/%s/status", upid),
		nil,
		&resp,
	)
	if err != nil {
		return nil, fmt.Errorf("GetTaskByUPID: error creating http request -> %w", err)
	}

	if resp.Data.Status == "stopped" {
		endTime, err := storeInstance.GetTaskEndTime(&resp.Data)
		if err != nil {
			return nil, fmt.Errorf("GetTaskByUPID: error getting task end time -> %w", err)
		}

		resp.Data.EndTime = endTime
	}

	return &resp.Data, nil
}

func (storeInstance *Store) GetTaskEndTime(task *Task) (int64, error) {
	nextPage := true
	var resp TasksResponse

	if storeInstance.LastToken == nil && storeInstance.APIToken == nil {
		return -1, fmt.Errorf("GetTaskEndTime: token is required")
	}

	page := 1
	for nextPage {
		start := (page - 1) * 50
		err := storeInstance.ProxmoxHTTPRequest(
			http.MethodGet,
			fmt.Sprintf("/api2/json/nodes/localhost/tasks?typefilter=backup&running=false&start=%d&since=%d", start, task.StartTime),
			nil,
			&resp,
		)
		if err != nil {
			return -1, fmt.Errorf("GetTaskEndTime: error creating http request -> %w", err)
		}

		for _, respTask := range resp.Data {
			if respTask.UPID == task.UPID {
				return respTask.EndTime, nil
			}
		}

		if resp.Total <= page*50 {
			nextPage = false
		}
	}

	return -1, fmt.Errorf("GetTaskEndTime: error getting tasks: not found")
}
