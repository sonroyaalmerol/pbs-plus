package targets

import libstore "sgl.com/pbs-ui/store"

type TargetsResponse struct {
	Data   []libstore.Target `json:"data"`
	Digest string            `json:"digest"`
}

type TargetConfigResponse struct {
	Errors  map[string]string `json:"errors"`
	Message string            `json:"message"`
	Data    *libstore.Target  `json:"data"`
	Status  int               `json:"status"`
	Success bool              `json:"success"`
}
