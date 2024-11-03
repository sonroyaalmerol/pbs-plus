//go:build windows

package utils

import (
	"golang.org/x/sys/windows/registry"
)

func SetEnvironment(key string, value string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\ControlSet001\Control\Session Manager\Environment`, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()

	err = k.SetStringValue(key, value)
	if err != nil {
		return err
	}

	return nil
}
