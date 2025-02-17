package vmware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/sonroyaalmerol/pbs-plus/internal/vmware/shared"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	virtualDisks "github.com/vmware/virtual-disks/pkg/virtual_disks"
)

// VMwareSession wraps the govmomi Client.
type VMwareSession struct {
	Client   *govmomi.Client
	VcURL    string
	Username string
	Password string
}

// NewVMwareSession logs in to vCenter/ESXi and returns a VMwareSession.
// vcURL should be like "https://vcenter/sdk" (or ESXi URL),
// and insecure indicates whether SSL certificate verification should be skipped.
func NewVMwareSession(ctx context.Context, vcURL, username, password string, insecure bool) (*VMwareSession, error) {
	u, err := url.Parse(vcURL)
	if err != nil {
		return nil, err
	}
	u.User = url.UserPassword(username, password)

	client, err := govmomi.NewClient(ctx, u, insecure)
	if err != nil {
		return nil, err
	}

	return &VMwareSession{Client: client, VcURL: vcURL, Username: username, Password: password}, nil
}

// ListVMs returns all VirtualMachine objects matching "*" in the inventory.
func (s *VMwareSession) ListVMs(ctx context.Context) ([]*object.VirtualMachine, error) {
	finder := find.NewFinder(s.Client.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		return nil, err
	}

	finder.SetDatacenter(dc)
	vms, err := finder.VirtualMachineList(ctx, "*")
	if err != nil {
		return nil, err
	}
	return vms, nil
}

// CreateSnapshot creates a snapshot for the given VM.
func (s *VMwareSession) CreateSnapshot(ctx context.Context, vm *object.VirtualMachine, snapshotName, description string, memory, quiesce bool) error {
	task, err := vm.CreateSnapshot(ctx, snapshotName, description, memory, quiesce)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// DeleteSnapshot deletes (removes) a snapshot by name.
// It uses the property collector to retrieve the snapshot tree.
func (s *VMwareSession) DeleteSnapshot(ctx context.Context, vm *object.VirtualMachine, snapshotName string, removeChildren bool) error {
	var vmMo mo.VirtualMachine
	pc := property.DefaultCollector(s.Client.Client)
	err := pc.RetrieveOne(ctx, vm.Reference(), []string{"snapshot"}, &vmMo)
	if err != nil {
		return err
	}
	if vmMo.Snapshot == nil {
		return errors.New("the VM has no snapshots")
	}

	task, err := vm.RemoveSnapshot(ctx, snapshotName, removeChildren, nil)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

func (s *VMwareSession) FindSnapshot(ctx context.Context, vm *object.VirtualMachine, snapshotName string) *types.ManagedObjectReference {
	var vmMo mo.VirtualMachine
	pc := property.DefaultCollector(s.Client.Client)
	err := pc.RetrieveOne(ctx, vm.Reference(), []string{"snapshot"}, &vmMo)
	if err != nil {
		return nil
	}
	if vmMo.Snapshot == nil {
		return nil
	}

	return findSnapshotInTree(vmMo.Snapshot.RootSnapshotList, snapshotName)
}

// findSnapshotInTree recursively searches for a snapshot by name within a snapshot tree.
func findSnapshotInTree(snapshots []types.VirtualMachineSnapshotTree, name string) *types.ManagedObjectReference {
	for _, snap := range snapshots {
		if snap.Name == name {
			return &snap.Snapshot
		}
		if child := findSnapshotInTree(snap.ChildSnapshotList, name); child != nil {
			return child
		}
	}
	return nil
}

// DownloadDatastoreFile downloads a file from a datastore via the vSphere HTTP file access
// API. The URL is constructed using the known format:
//
//	https://<vcenter>/folder/<fileRelPath>?dcPath=<dcName>&dsName=<dsName>
//
// fileRelPath should be the path relative to the datastore root.

// DownloadDatastoreFile downloads a file from a datastore via the vSphere HTTP file access API,
// and uses govmomi’s datastore browser to obtain metadata (e.g. file size and modification time).
//
// fileRelPath is the path relative to the datastore root (e.g. "folder/vm.vmx").
// dcName is the datacenter name and dsName is the datastore name.
func (s *VMwareSession) DownloadDatastoreFile(
	ctx context.Context,
	fileRelPath, dcName, dsName string,
) (*shared.DatastoreFileInfo, error) {
	// Locate datacenter and datastore
	finder := find.NewFinder(s.Client.Client, true)
	dc, err := finder.Datacenter(ctx, dcName)
	if err != nil {
		return nil, fmt.Errorf("cannot find datacenter %q: %w", dcName, err)
	}
	finder.SetDatacenter(dc)

	ds, err := finder.Datastore(ctx, dsName)
	if err != nil {
		return nil, fmt.Errorf("cannot find datastore %q: %w", dsName, err)
	}

	// Get file metadata directly
	fileInfo, err := ds.Stat(ctx, fileRelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if strings.HasSuffix(fileRelPath, "/") || fileInfo.GetFileInfo().FileSize == 0 {
		return nil, fmt.Errorf("path appears to be a directory")
	}

	filename := path.Base(fileRelPath)

	var rc io.ReadCloser

	// Use virtual disk API for VMDK files
	if path.Ext(filename) == ".vmdk" || snapshotDiffRegex.MatchString(filename) {
		log.Printf("Opening %s via virtual disk library...", filename)
		// Build connection parameters for the VDDK. Adjust as needed.
		params := getDisklibConnectParams(s, fileInfo.GetFileInfo().Path)
		// Create a logger for the virtual-disk library (using logrus)
		logger := logrus.New()

		// Open the disk using the high-level API provided by the library.
		diskRW, err := virtualDisks.Open(params, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to open vmdk %s via virtual disk library: %w", filename, err)
		}
		rc = newDiskReadCloser(diskRW)
	} else {
		// For non-VMDK files (e.g. .ovf, manifest, etc) continue to download using HTTP GET.
		// Open file for downloading
		rc, _, err = ds.Download(ctx, fileRelPath, &soap.DefaultDownload)
		if err != nil {
			return nil, fmt.Errorf("download failed: %w", err)
		}
	}

	return &shared.DatastoreFileInfo{
		ReadCloser: rc,
		Size:       fileInfo.GetFileInfo().FileSize,
		ModTime:    *fileInfo.GetFileInfo().Modification,
	}, nil
}

// parseDatastorePath takes a string (e.g. "[datastore1] folder/vm.vmx")
// and returns the datastore name and the relative path.
func parseDatastorePath(raw string) (string, string, error) {
	re := regexp.MustCompile(`\[(.*?)\]\s*(.*)`)
	matches := re.FindStringSubmatch(raw)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("failed to parse datastore path from: %s", raw)
	}
	dsName := matches[1]
	relPath := strings.TrimSpace(matches[2])
	return dsName, relPath, nil
}
