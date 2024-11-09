package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
)

var httpClient *http.Client

func ProxmoxHTTPRequest(method, url string, body io.Reader, respBody any) error {
	serverUrl := os.Getenv("PBS_AGENT_SERVER")
	req, err := http.NewRequest(
		method,
		fmt.Sprintf(
			"%s%s",
			strings.TrimSuffix(serverUrl, "/"),
			url,
		),
		body,
	)

	if err != nil {
		return fmt.Errorf("ProxmoxHTTPRequest: error creating http request -> %w", err)
	}

	req.Header.Add("Content-Type", "application/json")

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   time.Second * 30,
			Transport: utils.BaseTransport,
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ProxmoxHTTPRequest: error executing http request -> %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ProxmoxHTTPRequest: error getting body content -> %w", err)
	}

	if respBody != nil {
		err = json.Unmarshal(rawBody, respBody)
		if err != nil {
			return fmt.Errorf("ProxmoxHTTPRequest: error json unmarshal body content (%s) -> %w", string(rawBody), err)
		}
	}

	return nil
}