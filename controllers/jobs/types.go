package jobs

import "sgl.com/pbs-ui/store"

type JobsResponse struct {
	Data   []store.Job `json:"data"`
	Digest string      `json:"digest"`
}

type JobConfigResponse struct {
	Errors  map[string]string `json:"errors"`
	Message string            `json:"message"`
	Data    *store.Job        `json:"data"`
	Status  int               `json:"status"`
	Success bool              `json:"success"`
}

type JobRunResponse struct {
	Errors  map[string]string `json:"errors"`
	Message string            `json:"message"`
	Data    string            `json:"data"`
	Status  int               `json:"status"`
	Success bool              `json:"success"`
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
