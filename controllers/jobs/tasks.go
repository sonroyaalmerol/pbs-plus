package jobs

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"sgl.com/pbs-ui/store"
)

func GetMostRecentTask(job *store.Job, r *http.Request) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks?store=%s&typefilter=backup&limit=1",
			store.ProxyTargetURL,
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

	var tasks []Task
	err = json.Unmarshal(tasksBody, &tasks)
	if err != nil {
		fmt.Println(tasksBody)
		return nil, err
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("error getting tasks: not found")
	}

	return &tasks[0], nil
}

func GetTaskByUPID(upid string, r *http.Request) (*Task, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/tasks/%s/status",
			store.ProxyTargetURL,
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

	var task Task
	err = json.Unmarshal(tasksBody, &task)
	if err != nil {
		fmt.Println(tasksBody)
		return nil, err
	}

	return &task, nil
}
