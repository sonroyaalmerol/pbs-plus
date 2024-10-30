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
	Node       string `json:"node"`
	PID        int    `json:"pid"`
	PStart     int    `json:"pstart"`
	StartTime  int64  `json:"starttime"`
	EndTime    int64  `json:"endtime"`
	UPID       string `json:"upid"`
	User       string `json:"user"`
	WorkerType string `json:"worker_type"`
	Status     string `json:"status"`
}

func GetMostRecentTask(job *Job, r *http.Request) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks?store=%s&typefilter=backup&limit=1",
			ProxyTargetURL,
			job.Store,
		),
		nil,
	)
	tasksReq.Header.Set("Csrfpreventiontoken", r.Header.Get("Csrfpreventiontoken"))
	tasksReq.Header.Set("User-Agent", r.Header.Get("User-Agent"))

	for _, cookie := range r.Cookies() {
		tasksReq.AddCookie(cookie)
	}

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

func GetTaskByUPID(upid string, r *http.Request) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks/%s/status",
			ProxyTargetURL,
			upid,
		),
		nil,
	)
	tasksReq.Header.Set("Csrfpreventiontoken", r.Header.Get("Csrfpreventiontoken"))
	tasksReq.Header.Set("User-Agent", r.Header.Get("User-Agent"))

	for _, cookie := range r.Cookies() {
		tasksReq.AddCookie(cookie)
	}

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

	return &taskStruct.Data, nil
}
