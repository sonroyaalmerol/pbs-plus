//go:build windows

package vssfs

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
)

const (
	RootHandleID = uint64(0) // Reserved ID for root directory
)

var keyPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 8)
	},
}

// VSSIDHandler uses VSSFS's stable file IDs for handle management
type VSSIDHandler struct {
	nfs.Handler
	vssFS           *VSSFS
	activeHandlesMu sync.RWMutex
	activeHandles   map[uint64]string
	db              *badger.DB
	pregenPauseCh   chan struct{}
	rootSlashPath   string // Cached root path in slash format
}

func NewVSSIDHandler(vssFS *VSSFS, underlyingHandler nfs.Handler, pregenPauseCh chan struct{}) (*VSSIDHandler, error) {
	dbPath, err := getDBPath(vssFS.snapshot.DriveLetter)
	if err != nil {
		return nil, fmt.Errorf("failed to get handler db path: %w", err)
	}

	if pregenPauseCh != nil {
		pregenPauseCh <- struct{}{}
	}

	// Configure Badger for better performance
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	opts.CompactL0OnClose = true // Compact L0 on close for faster opens

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open handle database: %w", err)
	}

	h := &VSSIDHandler{
		Handler:       underlyingHandler,
		vssFS:         vssFS,
		activeHandles: make(map[uint64]string),
		db:            db,
		pregenPauseCh: pregenPauseCh,
		rootSlashPath: filepath.ToSlash(vssFS.Root()) + "/",
	}

	if err := h.recoverHandles(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to recover handles: %w", err)
	}

	if pregenPauseCh != nil {
		<-pregenPauseCh
	}

	return h, nil
}

func (h *VSSIDHandler) recoverHandles() error {
	return h.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // Keys-only iteration
		it := txn.NewIterator(opts)
		defer it.Close()

		h.activeHandlesMu.Lock()
		defer h.activeHandlesMu.Unlock()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := binary.BigEndian.Uint64(item.Key())
			err := item.Value(func(val []byte) error {
				h.activeHandles[key] = string(val)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *VSSIDHandler) Close() error {
	return h.db.Close()
}

func (h *VSSIDHandler) getHandle(key uint64) (string, bool) {
	h.activeHandlesMu.RLock()
	defer h.activeHandlesMu.RUnlock()
	handle, ok := h.activeHandles[key]
	return handle, ok
}

func (h *VSSIDHandler) storeHandle(key uint64, path string) error {
	// Use pooled buffer for key
	keyBytes := keyPool.Get().([]byte)
	defer keyPool.Put(keyBytes)
	binary.BigEndian.PutUint64(keyBytes, key)

	// Store persistently first
	err := h.db.Update(func(txn *badger.Txn) error {
		return txn.Set(keyBytes, []byte(path))
	})
	if err != nil {
		return err
	}

	// Then update in-memory
	h.activeHandlesMu.Lock()
	h.activeHandles[key] = path
	h.activeHandlesMu.Unlock()

	return nil
}

func (h *VSSIDHandler) ToHandle(f billy.Filesystem, path []string) []byte {
	vssFS, ok := f.(*VSSFS)
	if !ok || vssFS != h.vssFS {
		return nil
	}

	// Handle root directory specially
	if len(path) == 0 || (len(path) == 1 && path[0] == "") {
		return h.createHandle(RootHandleID, vssFS.Root())
	}

	winPath := filepath.Join(path...)
	fullPath := filepath.Join(vssFS.Root(), winPath)

	info, err := vssFS.Stat(winPath)
	if err != nil {
		return nil
	}
	fileID := info.(*VSSFileInfo).stableID
	return h.createHandle(fileID, fullPath)
}

func (h *VSSIDHandler) createHandle(fileID uint64, fullPath string) []byte {
	// Double-checked locking pattern
	if _, exists := h.getHandle(fileID); !exists {
		h.activeHandlesMu.Lock()
		if _, exists := h.activeHandles[fileID]; !exists {
			if err := h.storeHandle(fileID, fullPath); err != nil {
				h.activeHandlesMu.Unlock()
				return nil
			}
		}
		h.activeHandlesMu.Unlock()
	}

	// Use pooled buffer for handle
	handleBytes := keyPool.Get().([]byte)
	defer keyPool.Put(handleBytes)
	binary.BigEndian.PutUint64(handleBytes, fileID)
	return append([]byte{}, handleBytes...)
}

func (h *VSSIDHandler) FromHandle(handle []byte) (billy.Filesystem, []string, error) {
	if len(handle) != 8 {
		return nil, nil, fmt.Errorf("invalid handle length")
	}

	fileID := binary.BigEndian.Uint64(handle)
	if fileID == RootHandleID {
		return h.vssFS, []string{}, nil
	}

	fullPath, exists := h.getHandle(fileID)
	if !exists {
		return nil, nil, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
	}

	// Use cached root path for conversion
	relativePath := strings.TrimPrefix(filepath.ToSlash(fullPath), h.rootSlashPath)
	var parts []string
	if relativePath != "" {
		parts = strings.Split(relativePath, "/")
	}
	return h.vssFS, parts, nil
}

func (h *VSSIDHandler) HandleLimit() int {
	return math.MaxInt
}

func (h *VSSIDHandler) InvalidateHandle(fs billy.Filesystem, handle []byte) error {
	if len(handle) != 8 {
		return fmt.Errorf("invalid handle length")
	}

	fileID := binary.BigEndian.Uint64(handle)
	keyBytes := keyPool.Get().([]byte)
	defer keyPool.Put(keyBytes)
	binary.BigEndian.PutUint64(keyBytes, fileID)

	// Delete from Badger first
	err := h.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(keyBytes)
	})
	if err != nil {
		return err
	}

	// Delete from in-memory
	h.activeHandlesMu.Lock()
	delete(h.activeHandles, fileID)
	h.activeHandlesMu.Unlock()

	return nil
}

func (h *VSSIDHandler) ClearHandles() {
	h.activeHandlesMu.Lock()
	h.activeHandles = make(map[uint64]string)
	h.activeHandlesMu.Unlock()

	h.db.DropAll()
}
