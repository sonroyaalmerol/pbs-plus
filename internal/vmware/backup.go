package vmware

import (
	"context"
	"fmt"
	"io"
	"log"
	"path"
	"regexp"

	// Import the virtual-disk library. Adjust the import path as needed.
	disklib "github.com/vmware/virtual-disks/pkg/disklib"
	virtualDisks "github.com/vmware/virtual-disks/pkg/virtual_disks"

	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
	pbsclientgo "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_lib"
	pbsclientstream "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_stream"
	"github.com/sonroyaalmerol/pbs-plus/internal/vmware/shared"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

var snapshotDiffRegex = regexp.MustCompile(`-\d+\.vmdk$`)

// init initializes the virtual-disk library. It must be called once per process.
// Adjust the version numbers and library directory as required.
func init() {
	// For example, using VDDK version 7.0.0 installed under /usr/local/VMware-vix-disklib
	if err := disklib.Init(7, 0, "/usr/local/VMware-vix-disklib"); err != nil {
		log.Fatalf("Failed to initialize virtual disk library: %v", err)
	}
}

func backup(ctx context.Context, s *VMwareSession, vm *object.VirtualMachine,
	pbsClient *pbsclientgo.PBSClient) error {

	vmName, err := vm.ObjectName(ctx)
	if err != nil {
		return err
	}

	snapshotName := "pbs-backup-snapshot"
	log.Printf("Creating snapshot %s for VM %s...", snapshotName, vmName)
	_ = s.DeleteSnapshot(ctx, vm, snapshotName, true)
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

	vfs := make(map[string]*shared.DatastoreFileInfo)
	defer func() {
		for key := range vfs {
			vfs[key].Close()
		}
	}()

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
	vfs[filename] = vmx

	for _, device := range vmMo.Config.Hardware.Device {
		disk, ok := device.(*types.VirtualDisk)
		if !ok {
			continue
		}

		// Traverse the backing chain to find the root/base VMDK
		var rootBacking *types.VirtualDiskFlatVer2BackingInfo
		currentBacking := disk.Backing
		for {
			backing, ok := currentBacking.(*types.VirtualDiskFlatVer2BackingInfo)
			if !ok {
				break // Unsupported backing type, exit traversal
			}
			rootBacking = backing
			if backing.Parent == nil {
				break // Reached the root of the backing chain
			}
			currentBacking = backing.Parent
		}

		if rootBacking == nil {
			log.Printf("Skipping disk with unsupported backing type")
			continue
		}

		_, relPath, err := parseDatastorePath(rootBacking.FileName)
		if err != nil {
			log.Printf("Warning: failed to parse disk file path: %v", err)
			continue
		}

		filename := path.Base(relPath)

		vmdk, err := s.DownloadDatastoreFile(ctx, relPath, dcName, dsName)
		if err != nil {
			return fmt.Errorf("failed to backup vmdk file: %w", err)
		}

		vfs[filename] = vmdk
	}

	err = pbsStream.Upload(utils.Slugify(vmName), vfs)
	if err != nil {
		return err
	}

	log.Printf("Backup for VM %s completed successfully.", vmName)
	return nil
}

// getDisklibConnectParams builds the VDDK connection parameters.
// You'll need to replace the placeholder values (such as username/password)
// with actual values from your VMwareSession or configuration.
func getDisklibConnectParams(s *VMwareSession, diskPath string) disklib.ConnectParams {
	return disklib.NewConnectParams(
		"vmxSpec",
		"serverName",
		"thumbPrint",
		s.Username,
		s.Password,
		"fcdId",
		"ds",
		"fcdssId",
		"cookie",
		"identity",
		"path",
		0,    // flag
		true, // readonly
		"mode",
	)
}

// diskReadCloser wraps disklib.DiskReaderWriter to satisfy io.ReadCloser.
type diskReadCloser struct {
	disk virtualDisks.DiskReaderWriter
}

// Read forwards the read to the disk reader.
func (d *diskReadCloser) Read(p []byte) (int, error) {
	return d.disk.Read(p)
}

// Close releases the disk and any associated resources.
func (d *diskReadCloser) Close() error {
	return d.disk.Close()
}

// newDiskReadCloser converts a disklib.DiskReaderWriter to an io.ReadCloser.
func newDiskReadCloser(disk virtualDisks.DiskReaderWriter) io.ReadCloser {
	return &diskReadCloser{disk: disk}
}
