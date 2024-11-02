//go:build windows

package snapshots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	vss "github.com/jeromehadorn/vss"
)

type WinVSSSnapshot struct {
	SnapshotPath string
	Snapshotter  *vss.Snapshotter
	Id           string
	Used         bool
}

func symlinkSnapshot(symlinkPath string, id string, deviceObjectPath string) (string, error) {
	snapshotSymLinkFolder := symlinkPath + "\\" + id + "\\"

	snapshotSymLinkFolder = filepath.Clean(snapshotSymLinkFolder)
	os.RemoveAll(snapshotSymLinkFolder)
	if err := os.MkdirAll(snapshotSymLinkFolder, 0700); err != nil {
		return "", fmt.Errorf("symlinkSnapshot: failed to create snapshot symlink folder for snapshot: %s -> %w", id, err)
	}

	os.Remove(snapshotSymLinkFolder)

	fmt.Println("Symlink from: ", deviceObjectPath, " to: ", snapshotSymLinkFolder)

	if err := os.Symlink(deviceObjectPath, snapshotSymLinkFolder); err != nil {
		return "", fmt.Errorf("symlinkSnapshot: failed to create symlink from: %s to: %s -> %w", deviceObjectPath, snapshotSymLinkFolder, err)
	}

	return snapshotSymLinkFolder, nil
}

func getAppDataFolder() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("getAppDataFolder: failed to get user config directory -> %w", err)
	}

	configBasePath := filepath.Join(configDir, "proxmox-agent")

	err = os.MkdirAll(configBasePath, os.ModePerm)
	if err != nil {
		return "", fmt.Errorf("getAppDataFolder: failed to create directory \"%s\" -> %w", configBasePath, err)
	}

	return configBasePath, nil
}

func Snapshot(path string) (*WinVSSSnapshot, error) {
	volName := filepath.VolumeName(path)
	volName += "\\"

	appDataFolder, err := getAppDataFolder()
	if err != nil {
		return nil, fmt.Errorf("Snapshot: error getting app data folder -> %w", err)
	}

	sn := vss.Snapshotter{}

	snapshot, err := sn.CreateSnapshot(volName, 180, true)
	if err != nil {
		return nil, fmt.Errorf("Snapshot: error creating snapshot -> %w", err)
	}

	_, err = symlinkSnapshot(filepath.Join(appDataFolder, "VSS"), snapshot.Id, snapshot.DeviceObjectPath)
	if err != nil {
		sn.DeleteSnapshot(snapshot.Id)
		return nil, fmt.Errorf("Snapshot: error symlinking snapshot -> %w", err)
	}

	newSnapshot := WinVSSSnapshot{
		SnapshotPath: filepath.Join(appDataFolder, "VSS", snapshot.Id),
		Snapshotter:  &sn,
		Id:           snapshot.Id,
	}

	go newSnapshot.closeOnStale()

	return &newSnapshot, nil
}

func (instance *WinVSSSnapshot) closeOnStale() {
	ctx, cancel := context.WithCancel(context.Background())

	for {
		select {
		case <-ctx.Done():
			_ = instance.Close()
		default:
			time.Sleep(2 * time.Minute)

			if !instance.Used {
				cancel()
			}
		}
	}
}

func (instance *WinVSSSnapshot) Close() error {
	err := instance.Snapshotter.DeleteSnapshot(instance.Id)
	if err != nil {
		return fmt.Errorf("Close: error deleting snapshot %s -> %w", instance.Id, err)
	}

	return nil
}
