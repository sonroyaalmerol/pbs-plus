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

func GetMostRecentTask(job *Job, token *Token) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks?store=%s&typefilter=backup&limit=1",
			ProxyTargetURL,
			job.Store,
		),
		nil,
	)
	tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

	tasksReq.AddCookie(&http.Cookie{
		Name:  "PBSAuthCookie",
		Value: token.Ticket,
		Path:  "/",
	})

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	tasksResp, err := client.Do(tasksReq)
	if err != nil {
		return nil, err
	}

	tasksBody, err := io.ReadAll(tasksResp.Body)
	if err != nil {
		return nil, err
	}

	var tasksStruct TasksResponse
	err = json.Unmarshal(tasksBody, &tasksStruct)
	if err != nil {
		fmt.Println(tasksBody)
		return nil, err
	}

	if len(tasksStruct.Data) == 0 {
		return nil, fmt.Errorf("error getting tasks: not found")
	}

	return &tasksStruct.Data[0], nil
}

func GetTaskByUPID(upid string, token *Token) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks/%s/status",
			ProxyTargetURL,
			upid,
		),
		nil,
	)
	tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

	tasksReq.AddCookie(&http.Cookie{
		Name:  "PBSAuthCookie",
		Value: token.Ticket,
		Path:  "/",
	})

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	tasksResp, err := client.Do(tasksReq)
	if err != nil {
		return nil, err
	}

	tasksBody, err := io.ReadAll(tasksResp.Body)
	if err != nil {
		return nil, err
	}

	var taskStruct TaskResponse
	err = json.Unmarshal(tasksBody, &taskStruct)
	if err != nil {
		fmt.Println(tasksBody)
		return nil, err
	}

	if taskStruct.Data.Status == "stopped" {
		endTime, err := GetTaskEndTime(&taskStruct.Data, token)
		if err != nil {
			return nil, err
		}

		taskStruct.Data.EndTime = endTime
	}
	return &taskStruct.Data, nil
}

func GetTaskEndTime(task *Task, token *Token) (int64, error) {
	nextPage := true
	var tasksStruct TasksResponse

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
		tasksReq.Header.Set("Csrfpreventiontoken", token.CSRFToken)

		tasksReq.AddCookie(&http.Cookie{
			Name:  "PBSAuthCookie",
			Value: token.Ticket,
			Path:  "/",
		})

		client := http.Client{
			Timeout: time.Second * 10,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		tasksResp, err := client.Do(tasksReq)
		if err != nil {
			return -1, err
		}

		tasksBody, err := io.ReadAll(tasksResp.Body)
		if err != nil {
			return -1, err
		}

		err = json.Unmarshal(tasksBody, &tasksStruct)
		if err != nil {
			fmt.Println(tasksBody)
			return -1, err
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

	return -1, fmt.Errorf("error getting tasks: not found")
}
