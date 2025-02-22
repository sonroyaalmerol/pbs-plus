//go:build windows

package syslog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/kardianos/service"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent"
)

type Logger struct {
	LogWriter service.Logger
}

type LogRemoteRequest struct {
	Hostname string `json:"hostname"`
	Message  string `json:"message"`
	Level    string `json:"level"`
}

var L *Logger

func InitializeLogger(s service.Service) error {
	logger, err := s.Logger(nil)
	if err != nil {
		return fmt.Errorf("InitializeLogger: %w", err)
	}

	L = &Logger{
		LogWriter: logger,
	}

	return nil
}

func (l *Logger) logToServer(level string, msg string) {
	hostname, _ := os.Hostname()
	reqBody, err := json.Marshal(&LogRemoteRequest{
		Hostname: hostname,
		Message:  msg,
		Level:    level,
	})
	if err != nil {
		return
	}

	if level == "warn" || level == "error" {
		body, err := agent.ProxmoxHTTPRequest(
			http.MethodPost,
			"/api2/json/d2d/agent-log",
			bytes.NewBuffer(reqBody),
			nil,
		)
		if err == nil {
			_, _ = io.Copy(io.Discard, body)
			body.Close()
		}
	}
}

func (l *Logger) Infof(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Info(fmt.Sprintf(format, v...))
	l.logToServer("info", fmt.Sprintf(format, v...))
}

func (l *Logger) Info(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Info(fmt.Sprint(v...))
	l.logToServer("info", fmt.Sprint(v...))
}

func (l *Logger) Errorf(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Error(fmt.Sprintf(format, v...))
	l.logToServer("error", fmt.Sprintf(format, v...))
}

func (l *Logger) Error(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Error(fmt.Sprint(v...))
	l.logToServer("error", fmt.Sprint(v...))
}

func (l *Logger) Warnf(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Warning(fmt.Sprintf(format, v...))
	l.logToServer("warn", fmt.Sprintf(format, v...))
}

func (l *Logger) Warn(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Warning(fmt.Sprint(v...))
	l.logToServer("warn", fmt.Sprint(v...))
}
