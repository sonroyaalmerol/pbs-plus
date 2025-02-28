//go:build windows

package vssfs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/sonroyaalmerol/pbs-plus/internal/arpc"
	"golang.org/x/sys/windows"
)

// --- Types ---

type FileHandle struct {
	handle   windows.Handle
	path     string
	isDir    bool
	isClosed bool
}

type VSSFSServer struct {
	ctx        context.Context
	ctxCancel  context.CancelFunc
	jobId      string
	rootDir    string
	handles    map[uint64]*FileHandle
	nextHandle uint64
	mu         sync.RWMutex
	arpcRouter *arpc.Router
	fsCache    *FileCache
}

const InvalidHandleError = arpc.StringMsg("invalid handle")
const CannotReadDirError = arpc.StringMsg("cannot read from directory")

func NewVSSFSServer(jobId string, root string) *VSSFSServer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &VSSFSServer{
		rootDir:   root,
		jobId:     jobId,
		handles:   make(map[uint64]*FileHandle),
		fsCache:   NewFileCache(ctx, root, 2),
		ctx:       ctx,
		ctxCancel: cancel,
	}
	return s
}

func (s *VSSFSServer) RegisterHandlers(r *arpc.Router) {
	r.Handle(s.jobId+"/OpenFile", s.handleOpenFile)
	r.Handle(s.jobId+"/Stat", s.handleStat)
	r.Handle(s.jobId+"/ReadDir", s.handleReadDir)
	r.Handle(s.jobId+"/Read", s.handleRead)
	r.Handle(s.jobId+"/ReadAt", s.handleReadAt)
	r.Handle(s.jobId+"/Close", s.handleClose)
	r.Handle(s.jobId+"/Fstat", s.handleFstat)
	r.Handle(s.jobId+"/FSstat", s.handleFsStat)

	s.arpcRouter = r
}

// Update the Close method so that the cache is stopped:
func (s *VSSFSServer) Close() {
	if s.arpcRouter != nil {
		r := s.arpcRouter
		r.CloseHandle(s.jobId + "/OpenFile")
		r.CloseHandle(s.jobId + "/Stat")
		r.CloseHandle(s.jobId + "/ReadDir")
		r.CloseHandle(s.jobId + "/Read")
		r.CloseHandle(s.jobId + "/ReadAt")
		r.CloseHandle(s.jobId + "/Close")
		r.CloseHandle(s.jobId + "/Fstat")
		r.CloseHandle(s.jobId + "/FSstat")
	}
	s.mu.Lock()
	for k := range s.handles {
		delete(s.handles, k)
	}
	s.nextHandle = 0
	s.mu.Unlock()

	s.ctxCancel()

	// Stop the file cache.
	if s.fsCache != nil {
		s.fsCache.Cancel()
		s.fsCache = nil
	}
}

// --- Handlers ---

func (s *VSSFSServer) handleFsStat(req arpc.Request) (arpc.Response, error) {
	// No payload expected.
	var totalBytes uint64
	err := windows.GetDiskFreeSpaceEx(
		windows.StringToUTF16Ptr(s.rootDir),
		nil,
		&totalBytes,
		nil,
	)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	fsStat := &FSStat{
		TotalSize:      int64(totalBytes),
		FreeSize:       0,
		AvailableSize:  0,
		TotalFiles:     1 << 20,
		FreeFiles:      0,
		AvailableFiles: 0,
		CacheHint:      time.Minute,
	}

	fsStatBytes, err := fsStat.MarshalMsg(nil)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	return arpc.Response{
		Status: 200,
		Data:   fsStatBytes,
	}, nil
}

func (s *VSSFSServer) handleOpenFile(req arpc.Request) (arpc.Response, error) {
	var payload OpenFileReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	// Disallow write operations.
	if payload.Flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		errStr := arpc.StringMsg("write operations not allowed")
		errBytes, err := errStr.MarshalMsg(nil)
		if err != nil {
			return s.respondError(req.Method, s.jobId, err), nil
		}

		return arpc.Response{
			Status: 403,
			Data:   errBytes,
		}, nil
	}

	path, err := s.abs(payload.Path)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return s.respondError(req.Method, s.jobId, mapWinError(err, path)), nil
	}

	handle, err := windows.CreateFile(
		pathp,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_SEQUENTIAL_SCAN|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return s.respondError(req.Method, s.jobId, mapWinError(err, path)), nil
	}

	fileHandle := &FileHandle{
		handle: handle,
		path:   path,
	}

	s.mu.Lock()
	s.nextHandle++
	handleID := s.nextHandle
	s.handles[handleID] = fileHandle
	s.mu.Unlock()

	data := FileHandleId(handleID)
	dataBytes, err := data.MarshalMsg(nil)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	// Return the handle ID.
	return arpc.Response{
		Status: 200,
		Data:   dataBytes,
	}, nil
}

// handleStat first checks the cache. If an entry is available it pops (removes)
// the CachedEntry and returns the stat info. Otherwise, it falls back to the
// Windows API‐based lookup.
func (s *VSSFSServer) handleStat(req arpc.Request) (arpc.Response, error) {
	var payload StatReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	fullPath, err := s.abs(payload.Path)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}
	if payload.Path == "." || payload.Path == "" {
		fullPath = s.rootDir
	}

	// Try to get the cached result (which includes stat).
	if s.fsCache != nil {
		if entry, ok := s.fsCache.PopStat(fullPath); ok {
			data, err := entry.MarshalMsg(nil)
			if err != nil {
				return s.respondError(req.Method, s.jobId, err), nil
			}

			return arpc.Response{
				Status: 200,
				Data:   data,
			}, nil
		}
	}

	info, err := stat(fullPath)
	if err != nil {
		return s.respondError(req.Method, s.jobId, mapWinError(err, fullPath)), nil
	}

	data, err := info.MarshalMsg(nil)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

// handleReadDir first attempts to serve the directory listing from the cache.
// It returns the cached DirEntries for that directory.
func (s *VSSFSServer) handleReadDir(req arpc.Request) (arpc.Response, error) {
	var payload ReadDirReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}
	windowsDir := filepath.FromSlash(payload.Path)
	fullDirPath, err := s.abs(windowsDir)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}
	if payload.Path == "." || payload.Path == "" {
		windowsDir = "."
		fullDirPath = s.rootDir
	}

	// Check the cache for this directory.
	if s.fsCache != nil {
		if entry, ok := s.fsCache.PopReaddir(fullDirPath); ok {
			entryBytes, err := entry.MarshalMsg(nil)
			if err != nil {
				return s.respondError(req.Method, s.jobId, err), nil
			}
			return arpc.Response{
				Status: 200,
				Data:   entryBytes,
			}, nil
		}
	}

	entries, err := readDir(fullDirPath)
	if err != nil {
		return s.respondError(req.Method, s.jobId, mapWinError(err, fullDirPath)), nil
	}

	entryBytes, err := entries.MarshalMsg(nil)
	if err != nil {
		return s.respondError(req.Method, s.jobId, err), nil
	}

	return arpc.Response{
		Status: 200,
		Data:   entryBytes,
	}, nil
}

func (s *VSSFSServer) handleRead(req arpc.Request) (arpc.Response, error) {
	var payload ReadReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	s.mu.RLock()
	handle, exists := s.handles[uint64(payload.HandleID)]
	s.mu.RUnlock()
	if !exists || handle.isClosed {
		invalidBytes, err := InvalidHandleError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 404, Data: invalidBytes}, nil
	}
	if handle.isDir {
		invalidDir, err := CannotReadDirError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 400, Data: invalidDir}, nil
	}

	isDirectBuffer := false
	if req.Headers != nil && req.Headers["X-Direct-Buffer"] == "true" {
		isDirectBuffer = true
	}

	buf := make([]byte, payload.Length)
	var bytesRead uint32
	err := windows.ReadFile(handle.handle, buf, &bytesRead, nil)
	isEOF := false
	if err != nil && err != windows.ERROR_HANDLE_EOF {
		return s.respondError(req.Method, s.jobId, mapWinError(err, handle.path)), nil
	}
	if err == windows.ERROR_HANDLE_EOF {
		isEOF = true
	}

	if isDirectBuffer {
		meta := arpc.BufferMetadata{BytesAvailable: int(bytesRead), EOF: isEOF}
		data, err := meta.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{
			Status: 200,
			Data:   data,
		}, &arpc.DirectBufferWrite{Data: buf[:bytesRead]}
	}

	dataStruct := DataResponse{
		Data: buf[:bytesRead],
		EOF:  isEOF,
	}
	data, err := dataStruct.MarshalMsg(nil)
	if err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

func (s *VSSFSServer) handleReadAt(req arpc.Request) (arpc.Response, error) {
	var payload ReadAtReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	s.mu.RLock()
	handle, exists := s.handles[uint64(payload.HandleID)]
	s.mu.RUnlock()
	if !exists || handle.isClosed {
		invalidBytes, err := InvalidHandleError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 404, Data: invalidBytes}, nil
	}
	if handle.isDir {
		invalidDir, err := CannotReadDirError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 400, Data: invalidDir}, nil
	}

	isDirectBuffer := false
	if req.Headers != nil && req.Headers["X-Direct-Buffer"] == "true" {
		isDirectBuffer = true
	}

	buf := make([]byte, payload.Length)
	var bytesRead uint32
	var overlapped windows.Overlapped
	overlapped.Offset = uint32(payload.Offset & 0xFFFFFFFF)
	overlapped.OffsetHigh = uint32(payload.Offset >> 32)

	err := windows.ReadFile(handle.handle, buf, &bytesRead, &overlapped)
	isEOF := false
	if err != nil && err != windows.ERROR_HANDLE_EOF {
		return s.respondError(req.Method, s.jobId, mapWinError(err, handle.path)), nil
	}
	if err == windows.ERROR_HANDLE_EOF {
		isEOF = true
	}

	if isDirectBuffer {
		meta := arpc.BufferMetadata{BytesAvailable: int(bytesRead), EOF: isEOF}
		data, err := meta.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{
			Status: 200,
			Data:   data,
		}, &arpc.DirectBufferWrite{Data: buf[:bytesRead]}
	}

	dataStruct := DataResponse{
		Data: buf[:bytesRead],
		EOF:  isEOF,
	}
	data, err := dataStruct.MarshalMsg(nil)
	if err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

func (s *VSSFSServer) handleClose(req arpc.Request) (arpc.Response, error) {
	var payload CloseReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	s.mu.Lock()
	handle, exists := s.handles[uint64(payload.HandleID)]
	if exists {
		delete(s.handles, uint64(payload.HandleID))
	}
	s.mu.Unlock()

	if !exists || handle.isClosed {
		invalidBytes, err := InvalidHandleError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 404, Data: invalidBytes}, nil
	}

	windows.CloseHandle(handle.handle)
	handle.isClosed = true

	closed := arpc.StringMsg("closed")
	data, err := closed.MarshalMsg(nil)
	if err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	return arpc.Response{Status: 200, Data: data}, nil
}

func (s *VSSFSServer) handleFstat(req arpc.Request) (arpc.Response, error) {
	var payload FstatReq
	if _, err := payload.UnmarshalMsg(req.Payload); err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}

	s.mu.RLock()
	handle, exists := s.handles[uint64(payload.HandleID)]
	s.mu.RUnlock()
	if !exists || handle.isClosed {
		invalidBytes, err := InvalidHandleError.MarshalMsg(nil)
		if err != nil {
			return s.invalidRequest(req.Method, s.jobId, err), nil
		}
		return arpc.Response{Status: 404, Data: invalidBytes}, nil
	}

	var fileInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle.handle, &fileInfo); err != nil {
		return s.respondError(req.Method, s.jobId, mapWinError(err, handle.path)), nil
	}

	info := createFileInfoFromHandleInfo(handle.path, &fileInfo)
	data, err := info.MarshalMsg(nil)
	if err != nil {
		return s.invalidRequest(req.Method, s.jobId, err), nil
	}
	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

func (s *VSSFSServer) abs(filename string) (string, error) {
	if filename == "" || filename == "." {
		return s.rootDir, nil
	}
	path, err := securejoin.SecureJoin(s.rootDir, filename)
	if err != nil {
		return "", err
	}
	return path, nil
}
