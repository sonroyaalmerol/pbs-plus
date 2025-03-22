//go:build windows

package agentfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
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
	handle       windows.Handle
	fileSize     int64
	isDir        bool
	isEFS        bool
	efsDataCache []byte
	pvContext    uintptr
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

	isEncrypted, err := isFileEncrypted(path)
	if err != nil {
		return arpc.Response{}, err
	}

	if isEncrypted {
		return s.openEFSFile(path)
	}
	return s.openRegularFile(path)
}

// Dedicated EFS handler
func (s *AgentFSServer) openEFSFile(path string) (arpc.Response, error) {
	var pvContext uintptr
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return arpc.Response{}, mapWinError(err, "utf16Path")
	}

	// Single EFS open+read+close sequence
	if err := OpenEncryptedFileRawW(utf16Path, CREATE_FOR_EXPORT, &pvContext); err != nil {
		return arpc.Response{}, mapWinError(err, "OpenEncryptedFileRawW")
	}

	handleID := s.handleIdGen.NextID()
	s.handles.Set(handleID, &FileHandle{
		isEFS:     true,
		pvContext: pvContext,
	})

	fhID := types.FileHandleId(handleID)
	data, err := fhID.Encode()
	if err != nil {
		return arpc.Response{}, err
	}
	return arpc.Response{Status: 200, Data: data}, nil
}

// Regular file handler
func (s *AgentFSServer) openRegularFile(path string) (arpc.Response, error) {
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
		return arpc.Response{}, mapWinError(err, "CreateFile")
	}

	// Get all needed info in one syscall
	fileSize, isDir, err := getFileInfo(handle)
	if err != nil {
		windows.CloseHandle(handle)
		return arpc.Response{}, mapWinError(err, "getFileInfo")
	}

	handleID := s.handleIdGen.NextID()
	s.handles.Set(handleID, &FileHandle{
		handle:   handle,
		fileSize: fileSize,
		isDir:    isDir,
	})

	fhID := types.FileHandleId(handleID)
	data, err := fhID.Encode()
	if err != nil {
		windows.CloseHandle(handle)
		return arpc.Response{}, err
	}
	return arpc.Response{Status: 200, Data: data}, nil
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

	reader := bytes.NewReader(entries)
	streamCallback := func(stream *smux.Stream) {
		if err := binarystream.SendDataFromReader(reader, int(len(entries)), stream); err != nil {
			syslog.L.Error(err).WithMessage("failed sending data from reader via binary stream").Write()
		}
	}

	return arpc.Response{
		Status:    213,
		RawStream: streamCallback,
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

	// Check if it's an EFS handle
	if fh.isEFS {
		return s.readEFSFile(&payload, fh)
	}

	return s.readRegularFile(&payload, fh)
}

func (s *AgentFSServer) readEFSFile(payload *types.ReadAtReq, fh *FileHandle) (arpc.Response, error) {
	if fh.efsDataCache == nil {
		cbContext := &exportCallbackContext{data: make([]byte, 0)}
		callback := syscall.NewCallback(exportCallback)

		if err := ReadEncryptedFileRaw(callback, uintptr(unsafe.Pointer(cbContext)), fh.pvContext); err != nil {
			return arpc.Response{}, mapWinError(err, "ReadEncryptedFileRaw")
		}
		fh.efsDataCache = cbContext.data
	}

	if payload.Offset < 0 || payload.Offset >= int64(len(fh.efsDataCache)) {
		return arpc.Response{}, io.EOF
	}
	end := payload.Offset + int64(payload.Length)
	if end > int64(len(fh.efsDataCache)) {
		end = int64(len(fh.efsDataCache))
		payload.Length = int(end - payload.Offset)
	}
	data := fh.efsDataCache[payload.Offset:end]
	reader := bytes.NewReader(data)
	streamCallback := func(stream *smux.Stream) {
		if err := binarystream.SendDataFromReader(reader, payload.Length, stream); err != nil {
			syslog.L.Error(err).WithMessage("failed sending EFS data via binary stream").Write()
		}
	}
	return arpc.Response{
		Status:    213,
		RawStream: streamCallback,
	}, nil
}

func (s *AgentFSServer) readRegularFile(payload *types.ReadAtReq, fh *FileHandle) (arpc.Response, error) {
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

	// Align the offset down to the nearest multiple of the allocation granularity.
	alignedOffset := payload.Offset - (payload.Offset % int64(s.allocGranularity))
	offsetDiff := int(payload.Offset - alignedOffset)
	viewSize := uintptr(payload.Length + offsetDiff)

	// Attempt to create a file mapping.
	h, err := windows.CreateFileMapping(fh.handle, nil, windows.PAGE_READONLY, 0, 0, nil)
	if err == nil {
		// Map the requested view.
		addr, err := windows.MapViewOfFile(
			h,
			windows.FILE_MAP_READ,
			uint32(alignedOffset>>32),
			uint32(alignedOffset&0xFFFFFFFF),
			viewSize,
		)
		if err == nil {
			ptr := (*byte)(unsafe.Pointer(addr))
			data := unsafe.Slice(ptr, viewSize)
			// Verify we’re not slicing outside the allocated region.
			if offsetDiff+payload.Length > len(data) {
				syslog.L.Error(fmt.Errorf(
					"invalid slice bounds: offsetDiff=%d, payload.Length=%d, data len=%d",
					offsetDiff, payload.Length, len(data)),
				).WithMessage("invalid file mapping boundaries").Write()

				windows.UnmapViewOfFile(addr)
				windows.CloseHandle(h)
				return arpc.Response{}, fmt.Errorf("invalid file mapping boundaries")
			}
			result := data[offsetDiff : offsetDiff+payload.Length]
			reader := bytes.NewReader(result)

			streamCallback := func(stream *smux.Stream) {
				// Ensure we free up resources once streaming is done.
				defer func() {
					windows.UnmapViewOfFile(addr)
					windows.CloseHandle(h)
				}()
				if err := binarystream.SendDataFromReader(reader, payload.Length, stream); err != nil {
					syslog.L.Error(err).WithMessage("failed sending data from reader via binary stream").Write()
				}
			}

			return arpc.Response{
				Status:    213,
				RawStream: streamCallback,
			}, nil
		}
		// If mapping fails, clean up.
		windows.CloseHandle(h)
	}

	// Fallback to using the OVERLAPPED ReadFile method.
	var overlapped windows.Overlapped
	overlapped.Offset = uint32(payload.Offset & 0xFFFFFFFF)
	overlapped.OffsetHigh = uint32(payload.Offset >> 32)

	buffer := make([]byte, payload.Length)
	var bytesRead uint32
	err = windows.ReadFile(fh.handle, buffer, &bytesRead, &overlapped)
	if err != nil {
		return arpc.Response{}, mapWinError(err, "handleReadAt ReadFile (OVERLAPPED fallback)")
	}

	reader := bytes.NewReader(buffer[:bytesRead])
	streamCallback := func(stream *smux.Stream) {
		if err := binarystream.SendDataFromReader(reader, int(bytesRead), stream); err != nil {
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
	if handle.isEFS {
		CloseEncryptedFileRaw(handle.pvContext)
	} else {
		windows.CloseHandle(handle.handle)
	}
	s.handles.Del(uint64(payload.HandleID))

	closed := arpc.StringMsg("closed")
	data, err := closed.Encode()
	if err != nil {
		return arpc.Response{}, err
	}

	return arpc.Response{Status: 200, Data: data}, nil
}
