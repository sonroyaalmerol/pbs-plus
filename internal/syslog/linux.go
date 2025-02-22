//go:build linux

package syslog

import (
	"fmt"
	"log"
	"log/syslog"
)

type Logger struct {
	LogWriter *syslog.Writer
}

var L *Logger

func InitializeLogger() error {
	syslogger, err := syslog.New(syslog.LOG_ERR|syslog.LOG_LOCAL7, "pbs-plus")
	if err != nil {
		return err
	}

	L = &Logger{
		LogWriter: syslogger,
	}

	return nil
}

func (l *Logger) Infof(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Info(fmt.Sprintf(format, v...))
}

func (l *Logger) Info(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Info(fmt.Sprint(v...))
}

func (l *Logger) Errorf(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Err(fmt.Sprintf(format, v...))
}

func (l *Logger) Error(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Err(fmt.Sprint(v...))
}

func (l *Logger) Warnf(format string, v ...any) {
	log.Printf(format, v...)
	_ = l.LogWriter.Warning(fmt.Sprintf(format, v...))
}

func (l *Logger) Warn(v ...any) {
	log.Print(v...)
	_ = l.LogWriter.Warning(fmt.Sprint(v...))
}
