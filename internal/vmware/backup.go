package vmware

import (
	"context"
	"fmt"
	"log"
	"path"

	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
	pbsclientgo "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_lib"
	pbsclientstream "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_stream"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

func backup(ctx context.Context, s *VMwareSession, vm *object.VirtualMachine, pbsClient *pbsclientgo.PBSClient) error {
	vmName, err := vm.ObjectName(ctx)
	if err != nil {
		return err
	}

	snapshotName := "pbs-backup-snapshot"
	log.Printf("Creating snapshot %s for VM %s...", snapshotName, vmName)
	if err = s.CreateSnapshot(ctx, vm, snapshotName, "Backup snapshot", false, false); err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	log.Printf("Snapshot %s created.", snapshotName)

	defer func() {
		log.Printf("Deleting snapshot %s...", snapshotName)
		if err = s.DeleteSnapshot(ctx, vm, snapshotName, true); err != nil {
			log.Printf("Warning: failed to delete snapshot: %v", err)
		} else {
			log.Printf("Snapshot %s deleted.", snapshotName)
		}
	}()

	pbsStream := pbsclientstream.New(pbsClient)

	pbsStream.OpenConnection()
	defer pbsStream.Close()

	var vmMo mo.VirtualMachine
	pc := property.DefaultCollector(s.Client.Client)
	err = pc.RetrieveOne(ctx, vm.Reference(), []string{"config"}, &vmMo)
	if err != nil {
		return fmt.Errorf("failed to retrieve VM config: %w", err)
	}

	finder := find.NewFinder(s.Client.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		return fmt.Errorf("failed to get default datacenter: %w", err)
	}
	dcName := dc.Name()

	log.Printf("Backing up VM configuration file...")
	vmxRaw := vmMo.Config.Files.VmPathName
	dsName, relPath, err := parseDatastorePath(vmxRaw)
	if err != nil {
		return fmt.Errorf("failed to parse vmx file path: %w", err)
	}
	vmx, err := s.DownloadDatastoreFile(ctx, relPath, dcName, dsName)
	if err != nil {
		return fmt.Errorf("failed to backup vmx file: %w", err)
	}

	filename := path.Base(relPath)
	err = pbsStream.UploadFile(utils.Slugify(filename), vmx)
	if err != nil {
		vmx.Close()
		return err
	}
	vmx.Close()

	for _, device := range vmMo.Config.Hardware.Device {
		disk, ok := device.(*types.VirtualDisk)
		if !ok {
			continue
		}
		// Only use disks that use the VirtualDiskFlatVer2 backing (common case).
		backing, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		if !ok {
			continue
		}

		dsName, relPath, err := parseDatastorePath(backing.FileName)
		if err != nil {
			log.Printf("Warning: failed to parse disk file path: %v", err)
			continue
		}

		vmdk, err := s.DownloadDatastoreFile(ctx, relPath, dcName, dsName)
		if err != nil {
			log.Printf("Warning: failed to backup disk %s: %v", relPath, err)
			continue
		}

		filename := path.Base(relPath)
		err = pbsStream.UploadFile(utils.Slugify(filename), vmdk)
		if err != nil {
			vmdk.Close()
			return err
		}
		vmdk.Close()
	}

	log.Printf("Backup for VM %s completed successfully.", vmName)
	return nil
}

func RunBackup(ctx context.Context, vmwareSess *VMwareSession, vm *object.VirtualMachine, pbsClient *pbsclientgo.PBSClient) error {
	vmName, err := vm.ObjectName(ctx)
	if err != nil {
		return fmt.Errorf("Skipping VM (failed to get name): %v", err)
	}
	if err = backup(ctx, vmwareSess, vm, pbsClient); err != nil {
		return fmt.Errorf("Backup for VM %s failed: %v", vmName, err)
	}

	return nil
}
