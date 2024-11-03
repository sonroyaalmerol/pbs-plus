package store

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	ProxyTargetURL     = "https://127.0.0.1:8007"        // The target server URL
	ModifiedFilePath   = "/js/proxmox-backup-gui.js"     // The specific JS file to modify
	CertFile           = "/etc/proxmox-backup/proxy.pem" // Path to generated SSL certificate
	KeyFile            = "/etc/proxmox-backup/proxy.key" // Path to generated private key
	TimerBasePath      = "/lib/systemd/system"
	DbBasePath         = "/var/lib/proxmox-backup"
	AgentMountBasePath = "/mnt/pbs-d2d-mounts"
)

var BaseTransport = &http.Transport{
	MaxIdleConns:        200,              // Max idle connections across all hosts
	MaxIdleConnsPerHost: 20,               // Max idle connections per host
	IdleConnTimeout:     15 * time.Second, // Timeout for idle connections
	DisableKeepAlives:   false,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second, // Connection timeout
		KeepAlive: 30 * time.Second, // TCP keep-alive
	}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	ExpectContinueTimeout: 1 * time.Second, // Timeout for expect-continue responses
}
