package vmware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// VMwareSession wraps the govmomi Client.
type VMwareSession struct {
	Client *govmomi.Client
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

	return &VMwareSession{Client: client}, nil
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
func (s *VMwareSession) DownloadDatastoreFile(ctx context.Context, fileRelPath, dcName, dsName string) (io.ReadCloser, error) {
	// Use the scheme and host from the vCenter URL (the API URL)
	baseURL := s.Client.URL()
	u := &url.URL{
		Scheme: baseURL.Scheme,
		Host:   baseURL.Host,
		// Use the standard folder URL format.
		Path: path.Join("/folder", fileRelPath),
	}
	q := u.Query()
	q.Set("dcPath", dcName)
	q.Set("dsName", dsName)
	u.RawQuery = q.Encode()

	log.Printf("Downloading file from %s", u.String())
	resp, err := s.Client.Client.Client.Get(u.String())
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	return resp.Body, nil
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
