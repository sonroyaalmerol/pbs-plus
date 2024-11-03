package store

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type PBSStatus struct {
	BootInfo      map[string]interface{} `json:"boot-info"`
	CPU           float64                `json:"cpu"`
	CPUInfo       map[string]interface{} `json:"cpuinfo"`
	CurrentKernel map[string]interface{} `json:"current-kernel"`
	Info          map[string]string      `json:"info"`
	KVersion      string                 `json:"kversion"`
	LoadAvg       []float32              `json:"loadavg"`
	Memory        map[string]int64       `json:"memory"`
	Root          map[string]int64       `json:"root"`
	Swap          map[string]int64       `json:"swap"`
	Uptime        int64                  `json:"uptime"`
	Wait          float32                `json:"wait"`
}

type PBSStatusResponse struct {
	Data PBSStatus `json:"data"`
}

func GetPBSStatus(token *Token, apiToken *APIToken) (*PBSStatus, error) {
	tasksReq, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api2/json/nodes/localhost/status",
			ProxyTargetURL,
		),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("GetPBSStatus: error creating http request -> %w", err)
	}

	if token == nil && apiToken == nil {
		return nil, fmt.Errorf("GetPBSStatus: token is required")
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
		return nil, fmt.Errorf("GetPBSStatus: error executing http request -> %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, taskResp.Body)
		taskResp.Body.Close()
	}()

	taskBody, err := io.ReadAll(taskResp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetPBSStatus: error getting body content -> %w", err)
	}

	var taskStruct PBSStatusResponse
	err = json.Unmarshal(taskBody, &taskStruct)
	if err != nil {
		return nil, fmt.Errorf("GetPBSStatus: error json unmarshal body content -> %w", err)
	}

	return &taskStruct.Data, nil
}
