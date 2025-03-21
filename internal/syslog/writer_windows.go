//go:build windows

package syslog

import (
	"log"
	"strings"

	"github.com/kardianos/service"
)

// EventLogWriter is a custom io.Writer that sends formatted log
// output to the Windows Event Log.
type LogWriter struct {
	logger service.Logger
}

// Write implements io.Writer. It converts the provided bytes into a string
// and sends it via LogWriter.
func (ew *LogWriter) Write(p []byte) (n int, err error) {
	message := string(p)
	if ew.logger != nil {
		if strings.Contains(message, "ERR") {
			err = ew.logger.Error(message)
		} else {
			err = ew.logger.Info(message)
		}
	} else {
		log.Print(message)
	}
	return len(p), err
}
