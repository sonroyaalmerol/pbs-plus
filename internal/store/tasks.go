package store

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func GetMostRecentTask(job *Job, token *Token, apiToken *APIToken) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks?store=%s&typefilter=backup&limit=1",
			ProxyTargetURL,
			job.Store,
		),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentTask: error creating http request -> %w", err)
	}

	if token == nil && apiToken == nil {
		return nil, fmt.Errorf("GetMostRecentTask: token is required")
	}

	if token != nil {
		tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

		tasksReq.AddCookie(&http.Cookie{
			Name:  "PBSAuthCookie",
			Value: token.Ticket,
			Path:  "/",
		})
	} else if apiToken != nil {
		tasksReq.Header.Set("Authorization", fmt.Sprintf("PBSAPIToken=%s:%s", apiToken.TokenId, apiToken.Value))
	}

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	tasksResp, err := client.Do(tasksReq)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentTask: error executing http request -> %w", err)
	}

	tasksBody, err := io.ReadAll(tasksResp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentTask: error getting body content -> %w", err)
	}

	var tasksStruct TasksResponse
	err = json.Unmarshal(tasksBody, &tasksStruct)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentTask: error json unmarshal body content -> %w", err)
	}

	if len(tasksStruct.Data) == 0 {
		return nil, fmt.Errorf("GetMostRecentTask: error getting tasks: not found")
	}

	return &tasksStruct.Data[0], nil
}

func GetTaskByUPID(upid string, token *Token, apiToken *APIToken) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks/%s/status",
			ProxyTargetURL,
			upid,
		),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("GetTaskByUPID: error creating http request -> %w", err)
	}

	if token == nil && apiToken == nil {
		return nil, fmt.Errorf("GetTaskByUPID: token is required")
	}

	if token != nil {
		tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

		tasksReq.AddCookie(&http.Cookie{
			Name:  "PBSAuthCookie",
			Value: token.Ticket,
			Path:  "/",
		})
	} else if apiToken != nil {
		tasksReq.Header.Set("Authorization", fmt.Sprintf("PBSAPIToken=%s:%s", apiToken.TokenId, apiToken.Value))
	}

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	taskResp, err := client.Do(tasksReq)
	if err != nil {
		return nil, fmt.Errorf("GetTaskByUPID: error executing http request -> %w", err)
	}

	taskBody, err := io.ReadAll(taskResp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetTaskByUPID: error getting body content -> %w", err)
	}

	var taskStruct TaskResponse
	err = json.Unmarshal(taskBody, &taskStruct)
	if err != nil {
		return nil, fmt.Errorf("GetTaskByUPID: error json unmarshal body content -> %w", err)
	}

	if taskStruct.Data.Status == "stopped" {
		endTime, err := GetTaskEndTime(&taskStruct.Data, token, apiToken)
		if err != nil {
			return nil, fmt.Errorf("GetTaskByUPID: error getting task end time -> %w", err)
		}

		taskStruct.Data.EndTime = endTime
	}
	return &taskStruct.Data, nil
}

func GetTaskEndTime(task *Task, token *Token, apiToken *APIToken) (int64, error) {
	nextPage := true
	var tasksStruct TasksResponse

	if token == nil && apiToken == nil {
		return -1, fmt.Errorf("GetTaskEndTime: token is required")
	}

	page := 1
	for nextPage {
		start := (page - 1) * 50
		tasksReq, err := http.NewRequest(
			http.MethodGet,
			fmt.Sprintf(
				"%s/api2/json/nodes/localhost/tasks?typefilter=backup&running=false&start=%d&since=%d",
				ProxyTargetURL,
				start,
				task.StartTime,
			),
			nil,
		)
		if err != nil {
			return -1, fmt.Errorf("GetTaskEndTime: error creating http request -> %w", err)
		}

		if token != nil {
			tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

			tasksReq.AddCookie(&http.Cookie{
				Name:  "PBSAuthCookie",
				Value: token.Ticket,
				Path:  "/",
			})
		} else if apiToken != nil {
			tasksReq.Header.Set("Authorization", fmt.Sprintf("PBSAPIToken=%s:%s", apiToken.TokenId, apiToken.Value))
		}

		client := http.Client{
			Timeout: time.Second * 10,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		tasksResp, err := client.Do(tasksReq)
		if err != nil {
			return -1, fmt.Errorf("GetTaskEndTime: error executing http request -> %w", err)
		}

		tasksBody, err := io.ReadAll(tasksResp.Body)
		if err != nil {
			return -1, fmt.Errorf("GetTaskEndTime: error reading body response -> %w", err)
		}

		err = json.Unmarshal(tasksBody, &tasksStruct)
		if err != nil {
			return -1, fmt.Errorf("GetTaskEndTime: error json unmarshal body response -> %w", err)
		}

		for _, taskStruct := range tasksStruct.Data {
			if taskStruct.UPID == task.UPID {
				return taskStruct.EndTime, nil
			}
		}

		if tasksStruct.Total <= page*50 {
			nextPage = false
		}
	}

	return -1, fmt.Errorf("GetTaskEndTime: error getting tasks: not found")
}
