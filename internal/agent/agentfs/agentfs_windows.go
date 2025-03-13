//go:build windows

package agentfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/pkg/errors"
	"github.com/sonroyaalmerol/pbs-plus/internal/agent/agentfs/types"
	"github.com/sonroyaalmerol/pbs-plus/internal/arpc"
	binarystream "github.com/sonroyaalmerol/pbs-plus/internal/arpc/binary"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"github.com/xtaci/smux"
	"golang.org/x/sys/windows"
)

type FileHandle struct {
	handle   windows.Handle
	fileSize int64
	isDir    bool
}

type FileStandardInfo struct {
	AllocationSize, EndOfFile int64
	NumberOfLinks             uint32
	DeletePending, Directory  bool
}

func (s *AgentFSServer) closeFileHandles() {
	s.handles.ForEach(func(u uint64, fh *FileHandle) bool {
		windows.CloseHandle(fh.handle)

		return true
	})

	s.handles.Clear()
}

func (s *AgentFSServer) initializeStatFS() error {
	var err error

	if s.snapshot.SourcePath != "" {
		driveLetter := s.snapshot.SourcePath[:1]
		s.statFs, err = getStatFS(driveLetter)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *AgentFSServer) handleStatFS(req arpc.Request) (arpc.Response, error) {
	enc, err := s.statFs.Encode()
	if err != nil {
		return arpc.Response{}, err
	}
	return arpc.Response{
		Status: 200,
		Data:   enc,
	}, nil
}

func (s *AgentFSServer) handleOpenFile(req arpc.Request) (arpc.Response, error) {
	var payload types.OpenFileReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	// Disallow write operations.
	if payload.Flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		errStr := arpc.StringMsg("write operations not allowed")
		errBytes, err := errStr.Encode()
		if err != nil {
			return arpc.Response{}, err
		}
		return arpc.Response{
			Status: 403,
			Data:   errBytes,
		}, nil
	}

	path, err := s.abs(payload.Path)
	if err != nil {
		return arpc.Response{}, err
	}

	// Check file status to mark directories.
	stat, err := os.Stat(path)
	if err != nil {
		return arpc.Response{}, err
	}

	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_SEQUENTIAL_SCAN|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return arpc.Response{}, err
	}

	fileSize, err := getFileSize(handle)
	if err != nil {
		windows.CloseHandle(handle)
		return arpc.Response{}, err
	}

	handleId := s.handleIdGen.NextID()
	fh := &FileHandle{
		handle:   handle,
		fileSize: fileSize,
		isDir:    stat.IsDir(),
	}
	s.handles.Set(handleId, fh)

	// Return the handle ID to the client.
	fhId := types.FileHandleId(handleId)
	dataBytes, err := fhId.Encode()
	if err != nil {
		windows.CloseHandle(handle)
		return arpc.Response{}, err
	}

	return arpc.Response{
		Status: 200,
		Data:   dataBytes,
	}, nil
}

func (s *AgentFSServer) handleAttr(req arpc.Request) (arpc.Response, error) {
	var payload types.StatReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	fullPath, err := s.abs(payload.Path)
	if err != nil {
		return arpc.Response{}, err
	}

	rawInfo, err := os.Stat(fullPath)
	if err != nil {
		return arpc.Response{}, err
	}

	blocks := uint64(0)
	if !rawInfo.IsDir() {
		file, err := os.Open(fullPath)
		if err != nil {
			return arpc.Response{}, err
		}
		defer file.Close()

		var blockSize uint64
		if s.statFs != (types.StatFS{}) {
			blockSize = s.statFs.Bsize
		}
		if blockSize == 0 {
			blockSize = 4096 // default 4KB block size
		}

		standardInfo, err := winio.GetFileStandardInfo(file)
		if err == nil {
			blocks = uint64((standardInfo.AllocationSize + int64(blockSize) - 1) / int64(blockSize))
		}
	}

	info := types.AgentFileInfo{
		Name:    rawInfo.Name(),
		Size:    rawInfo.Size(),
		Mode:    uint32(rawInfo.Mode()),
		ModTime: rawInfo.ModTime(),
		IsDir:   rawInfo.IsDir(),
		Blocks:  blocks,
	}

	data, err := info.Encode()
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

// handleStatx populates extended file statistics including Windows-specific
// creation time, last access time, group/owner and file attributes.
func (s *AgentFSServer) handleXattr(req arpc.Request) (arpc.Response, error) {
	var payload types.StatReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	fullPath, err := s.abs(payload.Path)
	if err != nil {
		return arpc.Response{}, err
	}

	// Use windows.GetFileAttributesEx to retrieve Win32FileAttributeData directly
	var fileAttrData windows.Win32FileAttributeData
	utf16, err := windows.UTF16FromString(fullPath)
	if err != nil {
		return arpc.Response{}, mapWinError(err, "handleXattr")
	}

	err = windows.GetFileAttributesEx(&utf16[0], windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&fileAttrData)))
	if err != nil {
		return arpc.Response{}, errors.Wrap(err, "failed to get file attributes")
	}

	// Extract extended attributes from Win32FileAttributeData
	creationTime := filetimeToUnix(fileAttrData.CreationTime)
	lastAccessTime := filetimeToUnix(fileAttrData.LastAccessTime)
	lastWriteTime := filetimeToUnix(fileAttrData.LastWriteTime)
	fileAttributes := parseFileAttributes(fileAttrData.FileAttributes)

	// Retrieve owner, group, and ACL info
	owner := ""
	group := ""
	var acls []types.WinACL
	owner, group, acls, _ = GetWinACLs(fullPath)

	// Populate AgentFileInfo
	info := types.AgentFileInfo{
		Name:           fullPath,
		CreationTime:   creationTime,
		LastAccessTime: lastAccessTime,
		LastWriteTime:  lastWriteTime,
		FileAttributes: fileAttributes,
		Owner:          owner,
		Group:          group,
		WinACLs:        acls,
	}

	data, err := info.Encode()
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{
		Status: 200,
		Data:   data,
	}, nil
}

// handleReadDir first attempts to serve the directory listing from the cache.
// It returns the cached DirEntries for that directory.
func (s *AgentFSServer) handleReadDir(req arpc.Request) (arpc.Response, error) {
	var payload types.ReadDirReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	windowsDir := filepath.FromSlash(payload.Path)
	fullDirPath, err := s.abs(windowsDir)
	if err != nil {
		return arpc.Response{}, err
	}

	// If the payload is empty (or "."), use the root.
	if payload.Path == "." || payload.Path == "" {
		fullDirPath = s.snapshot.Path
	}

	entries, err := readDirBulk(fullDirPath)
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{
		Status: 200,
		Data:   entries,
	}, nil
}

// handleReadAt now duplicates the file handle, opens a backup reading session,
// and then uses backupSeek to skip to the desired offset without copying bytes.
func (s *AgentFSServer) handleReadAt(req arpc.Request) (arpc.Response, error) {
	var payload types.ReadAtReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}
	// Validate the payload parameters.
	if payload.Length < 0 {
		return arpc.Response{}, fmt.Errorf("invalid negative length requested: %d", payload.Length)
	}

	// Retrieve the file handle.
	fh, exists := s.handles.Get(uint64(payload.HandleID))
	if !exists {
		return arpc.Response{}, os.ErrNotExist
	}
	if fh.isDir {
		return arpc.Response{}, os.ErrInvalid
	}

	// If the requested offset is at or beyond EOF, stream nothing.
	if payload.Offset >= fh.fileSize {
		emptyReader := bytes.NewReader([]byte{})
		streamCallback := func(stream *smux.Stream) {
			if err := binarystream.SendDataFromReader(emptyReader, payload.Length, stream); err != nil {
				syslog.L.Error(err).
					WithMessage("failed sending empty reader via binary stream").Write()
			}
		}
		return arpc.Response{
			Status:    213,
			RawStream: streamCallback,
		}, nil
	}

	// Clamp length if the requested region goes beyond EOF.
	if payload.Offset+int64(payload.Length) > fh.fileSize {
		payload.Length = int(fh.fileSize - payload.Offset)
	}

	// Open a handle to the target process with the PROCESS_DUP_HANDLE right
	targetProcess, err := s.unsafeExecutor.ProcessHandle()
	if err != nil {
		return arpc.Response{}, os.ErrInvalid
	}

	var dupHandle windows.Handle

	// Duplicate the handle from our process into the target process
	err = windows.DuplicateHandle(
		windows.CurrentProcess(),
		fh.handle,
		targetProcess,
		&dupHandle,
		0,     // requested access (0 means same access as the source handle)
		false, // do not make the handle inheritable
		windows.DUPLICATE_SAME_ACCESS,
	)
	if err != nil {
		return arpc.Response{}, os.ErrInvalid
	}

	childStream, err := s.unsafeExecutor.Mux.OpenStream()
	if err != nil {
		return arpc.Response{}, os.ErrInvalid
	}

	unsafeReq := types.UnsafeReq{
		Handle:  uintptr(dupHandle),
		Request: req.Payload,
	}

	unsafeReqBytes, err := unsafeReq.Encode()
	if err != nil {
		return arpc.Response{}, os.ErrInvalid
	}

	_, err = childStream.Write(unsafeReqBytes)
	if err != nil {
		return arpc.Response{}, os.ErrInvalid
	}

	streamCallback := func(stream *smux.Stream) {
		// Ensure we free up resources once streaming is done.
		defer func() {
			windows.CloseHandle(targetProcess)
			childStream.Close()
			windows.CloseHandle(dupHandle)
		}()

		if err := binarystream.SendDataFromReader(childStream, payload.Length, stream); err != nil {
			syslog.L.Error(err).WithMessage("failed sending data from reader via binary stream").Write()
		}
	}

	return arpc.Response{
		Status:    213,
		RawStream: streamCallback,
	}, nil
}

func (s *AgentFSServer) handleLseek(req arpc.Request) (arpc.Response, error) {
	var payload types.LseekReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	// Validate whence
	if payload.Whence != io.SeekStart &&
		payload.Whence != io.SeekCurrent &&
		payload.Whence != io.SeekEnd &&
		payload.Whence != SeekData &&
		payload.Whence != SeekHole {
		return arpc.Response{}, os.ErrInvalid
	}

	// Retrieve the file handle
	fh, exists := s.handles.Get(uint64(payload.HandleID))
	if !exists {
		return arpc.Response{}, os.ErrNotExist
	}
	if fh.isDir {
		return arpc.Response{}, os.ErrInvalid
	}

	// Query the file size
	fileSize, err := getFileSize(fh.handle)
	if err != nil {
		return arpc.Response{}, err
	}

	var newOffset int64

	// Handle sparse file operations
	if payload.Whence == SeekData || payload.Whence == SeekHole {
		newOffset, err = sparseSeek(fh.handle, payload.Offset, payload.Whence, fileSize)
		if err != nil {
			return arpc.Response{}, err
		}
	} else {
		// Handle standard seek operations
		switch payload.Whence {
		case io.SeekStart:
			if payload.Offset < 0 {
				return arpc.Response{}, os.ErrInvalid
			}
			newOffset = payload.Offset

		case io.SeekCurrent:
			currentPos, err := windows.SetFilePointer(fh.handle, 0, nil, windows.FILE_CURRENT)
			if err != nil {
				return arpc.Response{}, mapWinError(err, "handleLseek SetFilePointer (FILE_CURRENT)")
			}
			newOffset = int64(currentPos) + payload.Offset
			if newOffset < 0 {
				return arpc.Response{}, os.ErrInvalid
			}

		case io.SeekEnd:
			newOffset = fileSize + payload.Offset
			if newOffset < 0 {
				return arpc.Response{}, os.ErrInvalid
			}
		}
	}

	// Validate the new offset
	if newOffset > fileSize {
		return arpc.Response{}, os.ErrInvalid
	}

	// Set the new position
	_, err = windows.SetFilePointer(fh.handle, int32(newOffset), nil, windows.FILE_BEGIN)
	if err != nil {
		return arpc.Response{}, mapWinError(err, "handleLseek SetFilePointer (FILE_BEGIN)")
	}

	// Prepare the response
	resp := types.LseekResp{
		NewOffset: newOffset,
	}
	respBytes, err := resp.Encode()
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{
		Status: 200,
		Data:   respBytes,
	}, nil
}

func (s *AgentFSServer) handleClose(req arpc.Request) (arpc.Response, error) {
	var payload types.CloseReq
	if err := payload.Decode(req.Payload); err != nil {
		return arpc.Response{}, err
	}

	handle, exists := s.handles.Get(uint64(payload.HandleID))
	if !exists {
		return arpc.Response{}, os.ErrNotExist
	}

	// Close the Windows handle directly
	windows.CloseHandle(handle.handle)
	s.handles.Del(uint64(payload.HandleID))

	closed := arpc.StringMsg("closed")
	data, err := closed.Encode()
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{Status: 200, Data: data}, nil
}
