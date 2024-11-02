//go:build linux

package logger

import "log/syslog"

func InitializeSyslogger() (*syslog.Writer, error) {
	return syslog.New(syslog.LOG_ERR|syslog.LOG_LOCAL7, "pbs-d2d")
}
