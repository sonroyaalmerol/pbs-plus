//go:build windows

package utils

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	vss "github.com/jeromehadorn/vss"
)

type WinVSSSnapshot struct {
	SnapshotPath string
	Snapshotter  *vss.Snapshotter
	Id           string
}

func symlinkSnapshot(symlinkPath string, id string, deviceObjectPath string) (string, error) {
	snapshotSymLinkFolder := symlinkPath + "\\" + id + "\\"

	snapshotSymLinkFolder = filepath.Clean(snapshotSymLinkFolder)
	os.RemoveAll(snapshotSymLinkFolder)
	if err := os.MkdirAll(snapshotSymLinkFolder, 0700); err != nil {
		return "", fmt.Errorf("failed to create snapshot symlink folder for snapshot: %s, err: %s", id, err)
	}

	os.Remove(snapshotSymLinkFolder)

	fmt.Println("Symlink from: ", deviceObjectPath, " to: ", snapshotSymLinkFolder)

	if err := os.Symlink(deviceObjectPath, snapshotSymLinkFolder); err != nil {
		return "", fmt.Errorf("failed to create symlink from: %s to: %s, error: %s", deviceObjectPath, snapshotSymLinkFolder, err)
	}

	return snapshotSymLinkFolder, nil
}

func getAppDataFolder() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Println("Failed to get user config dir.")
		log.Println(err)
		return "", err
	}

	configBasePath := filepath.Join(configDir, "proxmox-agent")

	err = os.MkdirAll(configBasePath, os.ModePerm)
	if err != nil {
		return "", err
	}

	return configBasePath, nil
}

func Snapshot(path string) (*WinVSSSnapshot, error) {
	volName := filepath.VolumeName(path)
	volName += "\\"

	appDataFolder, err := getAppDataFolder()
	if err != nil {
		return nil, err
	}

	sn := vss.Snapshotter{}

	fmt.Printf("Creating VSS Snapshot...")
	snapshot, err := sn.CreateSnapshot(volName, 180, true)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Snapshot created: %s\n", snapshot.Id)

	_, err = symlinkSnapshot(filepath.Join(appDataFolder, "VSS"), snapshot.Id, snapshot.DeviceObjectPath)
	if err != nil {
		sn.DeleteSnapshot(snapshot.Id)
		return nil, err
	}

	return &WinVSSSnapshot{
		SnapshotPath: filepath.Join(appDataFolder, "VSS", snapshot.Id),
		Snapshotter:  &sn,
		Id:           snapshot.Id,
	}, nil
}

// TODO: Properly close snapshots
func (instance *WinVSSSnapshot) Close() error {
	err := instance.Snapshotter.DeleteSnapshot(instance.Id)

	return err
}
