//go:build windows

package vssfs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/sonroyaalmerol/pbs-plus/internal/agent/snapshots"
	"github.com/sonroyaalmerol/pbs-plus/internal/arpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// createLargeTestFile creates a test file of the specified size with deterministic content
func createLargeTestFile(t *testing.T, path string, size int) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	// Create a buffer with some pattern data
	const bufferSize = 64 * 1024 // 64KB buffer for writing
	buffer := make([]byte, bufferSize)

	// Fill buffer with deterministic pattern
	// We'll use a simple pattern that includes the position to make verification easier
	for i := 0; i < bufferSize; i++ {
		buffer[i] = byte(i % 251) // Use prime number to create a longer repeating pattern
	}

	// Write the buffer repeatedly until we reach the desired size
	bytesWritten := 0
	for bytesWritten < size {
		writeSize := bufferSize
		if bytesWritten+writeSize > size {
			writeSize = size - bytesWritten
		}

		n, err := file.Write(buffer[:writeSize])
		require.NoError(t, err)
		require.Equal(t, writeSize, n)

		bytesWritten += writeSize
	}

	// Ensure all data is flushed to disk
	require.NoError(t, file.Sync())
}

// logHandleMap logs information about the handles in the VSSFSServer
func dumpHandleMap(server *VSSFSServer) string {
	if server == nil || server.handles == nil {
		return "Server or handles map is nil"
	}

	var info strings.Builder
	info.WriteString(fmt.Sprintf("Current handles map contains %d entries:\n", server.handles.Len()))

	server.handles.ForEach(func(key string, fh *FileHandle) bool {
		info.WriteString(fmt.Sprintf("  - Handle ID: %s, Path: %s, IsDir: %v\n", key, fh.path, fh.isDir))
		return true
	})

	return info.String()
}

func TestVSSFSServer(t *testing.T) {
	// Setup test directory structure
	testDir, err := os.MkdirTemp("", "vssfs-test")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	// Create test files
	testFile1Path := filepath.Join(testDir, "test1.txt")
	err = os.WriteFile(testFile1Path, []byte("test file 1 content"), 0644)
	require.NoError(t, err)

	testFile2Path := filepath.Join(testDir, "test2.txt")
	err = os.WriteFile(testFile2Path, []byte("test file 2 content with more data"), 0644)
	require.NoError(t, err)

	// Create a large file to test memory mapping
	largePath := filepath.Join(testDir, "large_file.bin")
	createLargeTestFile(t, largePath, 1024*1024) // 1MB file

	// Create a medium file just below the mmap threshold
	mediumPath := filepath.Join(testDir, "medium_file.bin")
	createLargeTestFile(t, mediumPath, 100*1024) // 100KB file (below 128KB threshold)

	// Create subdirectory with files
	subDir := filepath.Join(testDir, "subdir")
	err = os.Mkdir(subDir, 0755)
	require.NoError(t, err)

	subFilePath := filepath.Join(subDir, "subfile.txt")
	err = os.WriteFile(subFilePath, []byte("content in subdirectory"), 0644)
	require.NoError(t, err)

	// Setup arpc server and client using in-memory connection
	serverConn, clientConn := net.Pipe()

	// Context for the test with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start the server
	serverRouter := arpc.NewRouter()
	vssServer := NewVSSFSServer("vss", &snapshots.WinVSSSnapshot{SnapshotPath: testDir, DriveLetter: ""})
	vssServer.RegisterHandlers(serverRouter)

	serverSession, err := arpc.NewServerSession(serverConn, nil)
	require.NoError(t, err)

	// Start server in a goroutine
	go func() {
		err := serverSession.Serve(serverRouter)
		// Ignore "closed pipe" errors during shutdown.
		if err != nil && ctx.Err() == nil && err != io.EOF && !strings.Contains(err.Error(), "closed pipe") {
			t.Errorf("Server error: %v", err)
		}
	}()
	defer serverSession.Close()

	// Setup client
	clientSession, err := arpc.NewClientSession(clientConn, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	t.Run("Stat", func(t *testing.T) {
		payload := StatReq{Path: "test1.txt"}
		payloadBytes, _ := payload.MarshalMsg(nil)
		var result VSSFileInfo
		raw, err := clientSession.CallMsg(ctx, "vss/Stat", payloadBytes)
		result.UnmarshalMsg(raw)
		assert.NoError(t, err)
		assert.NotNil(t, result.Size)
		assert.EqualValues(t, 19, result.Size)
	})

	t.Run("ReadDir", func(t *testing.T) {
		payload := ReadDirReq{Path: "/"}
		payloadBytes, _ := payload.MarshalMsg(nil)
		var result ReadDirEntries
		raw, err := clientSession.CallMsg(ctx, "vss/ReadDir", payloadBytes)
		result.UnmarshalMsg(raw)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 3) // Should have at least test1.txt, test2.txt, and subdir

		// Verify we can find our test files
		foundTest1 := false
		foundSubdir := false
		for _, entry := range result {
			name := entry.Name
			if name == "test1.txt" {
				foundTest1 = true
			} else if name == "subdir" {
				foundSubdir = true
				assert.True(t, os.FileMode(entry.Mode).IsDir(), "subdir should be identified as a directory")
			}
		}
		assert.True(t, foundTest1, "test1.txt should be found in directory listing")
		assert.True(t, foundSubdir, "subdir should be found in directory listing")
	})

	t.Run("OpenFile_ReadAt_Close", func(t *testing.T) {
		// Log handles before open
		t.Log("Before OpenFile:", dumpHandleMap(vssServer))

		// Open file
		payload := OpenFileReq{Path: "test2.txt", Flag: 0, Perm: 0644}
		payloadBytes, _ := payload.MarshalMsg(nil)
		var openResult FileHandleId
		raw, err := clientSession.CallMsg(ctx, "vss/OpenFile", payloadBytes)
		require.NoError(t, err, "OpenFile should succeed")
		openResult.UnmarshalMsg(raw)

		// Log handle ID and handles map after open
		t.Logf("After OpenFile - Handle ID received: %s", string(openResult))
		t.Log(dumpHandleMap(vssServer))

		// Verify the handle exists in the server's map
		exists := false
		vssServer.handles.ForEach(func(key string, fh *FileHandle) bool {
			if key == string(openResult) {
				exists = true
				return false // stop iteration
			}
			return true
		})
		require.True(t, exists, "Handle ID should exist in server's handles map")

		// Read at offset
		readAtPayload := ReadAtReq{
			HandleID: openResult,
			Offset:   10,
			Length:   100,
		}
		readAtPayloadBytes, _ := readAtPayload.MarshalMsg(nil)

		// Log before ReadAt
		t.Logf("Before ReadAt - Using Handle ID: %s", string(readAtPayload.HandleID))
		t.Log(dumpHandleMap(vssServer))

		p := make([]byte, 100)
		bytesRead, err := clientSession.CallMsgWithBuffer(ctx, "vss/ReadAt", readAtPayloadBytes, p)
		if err != nil {
			t.Logf("ReadAt error: %v - Current handle map: %s", err, dumpHandleMap(vssServer))
			t.FailNow()
		}

		assert.Equal(t, "2 content with more data", string(p[:bytesRead]))

		// Log before Close
		t.Logf("Before Close - Using Handle ID: %s", string(openResult))
		t.Log(dumpHandleMap(vssServer))

		// Close file
		closePayload := CloseReq{HandleID: openResult}
		closePayloadBytes, _ := closePayload.MarshalMsg(nil)
		resp, err := clientSession.Call("vss/Close", closePayloadBytes)
		if err != nil {
			t.Logf("Close error: %v - Current handle map: %s", err, dumpHandleMap(vssServer))
			t.FailNow()
		}
		assert.Equal(t, 200, resp.Status)

		// Log after Close
		t.Log("After Close:", dumpHandleMap(vssServer))
	})

	// New test to stress handle management with multiple files
	t.Run("MultipleFiles_HandleManagement", func(t *testing.T) {
		t.Log("Initial handle map:", dumpHandleMap(vssServer))

		// Store handles to verify them later
		handles := make([]FileHandleId, 0, 5)

		// Open multiple files
		files := []string{"test1.txt", "test2.txt", "large_file.bin", "medium_file.bin", "subdir/subfile.txt"}
		for i, fileName := range files {
			t.Logf("Opening file %d: %s", i, fileName)

			payload := OpenFileReq{Path: fileName, Flag: 0, Perm: 0644}
			payloadBytes, _ := payload.MarshalMsg(nil)
			var openResult FileHandleId
			raw, err := clientSession.CallMsg(ctx, "vss/OpenFile", payloadBytes)
			require.NoError(t, err, "OpenFile should succeed for %s", fileName)
			openResult.UnmarshalMsg(raw)

			t.Logf("Received handle ID: %s for file: %s", string(openResult), fileName)
			handles = append(handles, openResult)

			// Verify handle was added correctly
			t.Log(dumpHandleMap(vssServer))
		}

		// Verify all handles can be read from
		for i, handle := range handles {
			t.Logf("Reading from file %d with handle: %s", i, string(handle))

			readAtPayload := ReadAtReq{
				HandleID: handle,
				Offset:   0,
				Length:   10, // Just read a small amount
			}
			readAtPayloadBytes, _ := readAtPayload.MarshalMsg(nil)

			p := make([]byte, 10)
			_, err := clientSession.CallMsgWithBuffer(ctx, "vss/ReadAt", readAtPayloadBytes, p)
			if err != nil {
				t.Logf("ReadAt error for handle %s: %v - Current handle map: %s",
					string(handle), err, dumpHandleMap(vssServer))
				t.FailNow()
			}
		}

		// Now close all handles
		for i, handle := range handles {
			t.Logf("Closing file %d with handle: %s", i, string(handle))

			closePayload := CloseReq{HandleID: handle}
			closePayloadBytes, _ := closePayload.MarshalMsg(nil)

			t.Log("Before Close:", dumpHandleMap(vssServer))
			resp, err := clientSession.Call("vss/Close", closePayloadBytes)
			if err != nil {
				t.Logf("Close error for handle %s: %v - Current handle map: %s",
					string(handle), err, dumpHandleMap(vssServer))
				t.FailNow()
			}
			assert.Equal(t, 200, resp.Status)
			t.Log("After Close:", dumpHandleMap(vssServer))
		}
	})

	// Test specifically for memory-mapped file reading
	t.Run("LargeFile_MemoryMapped_Read", func(t *testing.T) {
		// Open large file
		payload := OpenFileReq{Path: "large_file.bin", Flag: 0, Perm: 0644}
		payloadBytes, _ := payload.MarshalMsg(nil)
		var openResult FileHandleId
		raw, err := clientSession.CallMsg(ctx, "vss/OpenFile", payloadBytes)
		openResult.UnmarshalMsg(raw)
		assert.NoError(t, err)

		t.Logf("Large file open, handle ID: %s", string(openResult))
		t.Log(dumpHandleMap(vssServer))

		// Read a large chunk that should trigger memory mapping
		// (assuming threshold is 128KB)
		readSize := 256 * 1024 // 256KB - well above threshold
		readAtPayload := ReadAtReq{
			HandleID: openResult,
			Offset:   1024, // Start at 1KB offset
			Length:   readSize,
		}
		readAtPayloadBytes, _ := readAtPayload.MarshalMsg(nil)

		buffer := make([]byte, readSize)
		bytesRead, err := clientSession.CallMsgWithBuffer(ctx, "vss/ReadAt", readAtPayloadBytes, buffer)
		if err != nil {
			t.Logf("Large file ReadAt error: %v - Current handle map: %s", err, dumpHandleMap(vssServer))
			t.FailNow()
		}
		assert.Equal(t, readSize, bytesRead, "Should read the full requested size")

		// Verify the data matches what we expect
		// We'll check the first few bytes against the original file
		originalFile, err := os.Open(largePath)
		require.NoError(t, err)
		defer originalFile.Close()

		_, err = originalFile.Seek(1024, 0) // Same offset as in the test
		require.NoError(t, err)

		compareBuffer := make([]byte, 1024) // Check first 1KB of the read
		_, err = io.ReadFull(originalFile, compareBuffer)
		require.NoError(t, err)

		assert.Equal(t, compareBuffer, buffer[:1024], "First 1KB of read data should match original file")

		// Close file
		closePayload := CloseReq{HandleID: openResult}
		closePayloadBytes, _ := closePayload.MarshalMsg(nil)
		resp, err := clientSession.Call("vss/Close", closePayloadBytes)
		if err != nil {
			t.Logf("Large file Close error: %v - Current handle map: %s", err, dumpHandleMap(vssServer))
			t.FailNow()
		}
		assert.Equal(t, 200, resp.Status)
	})

	// Test for error conditions with invalid handles
	t.Run("InvalidHandle_Operations", func(t *testing.T) {
		// Try to read with a non-existent handle
		readAtPayload := ReadAtReq{
			HandleID: "nonexistent_handle_id",
			Offset:   0,
			Length:   100,
		}
		readAtPayloadBytes, _ := readAtPayload.MarshalMsg(nil)

		t.Log("Current handle map before invalid ReadAt:", dumpHandleMap(vssServer))
		resp, err := clientSession.Call("vss/ReadAt", readAtPayloadBytes)
		assert.NoError(t, err, "Call should succeed but response should indicate error")
		assert.Equal(t, 500, resp.Status, "ReadAt with invalid handle should return 500 status")

		// Try to close a non-existent handle
		closePayload := CloseReq{HandleID: "nonexistent_handle_id"}
		closePayloadBytes, _ := closePayload.MarshalMsg(nil)

		t.Log("Current handle map before invalid Close:", dumpHandleMap(vssServer))
		resp, err = clientSession.Call("vss/Close", closePayloadBytes)
		assert.NoError(t, err, "Call should succeed but response should indicate error")
		assert.Equal(t, 500, resp.Status, "Close with invalid handle should return 500 status")
	})

	// Test for double close behavior
	t.Run("DoubleClose", func(t *testing.T) {
		// Open file
		payload := OpenFileReq{Path: "test1.txt", Flag: 0, Perm: 0644}
		payloadBytes, _ := payload.MarshalMsg(nil)
		var openResult FileHandleId
		raw, err := clientSession.CallMsg(ctx, "vss/OpenFile", payloadBytes)
		require.NoError(t, err)
		openResult.UnmarshalMsg(raw)

		t.Logf("File opened with handle ID: %s", string(openResult))
		t.Log(dumpHandleMap(vssServer))

		// First close - should succeed
		closePayload := CloseReq{HandleID: openResult}
		closePayloadBytes, _ := closePayload.MarshalMsg(nil)
		resp, err := clientSession.Call("vss/Close", closePayloadBytes)
		assert.NoError(t, err)
		assert.Equal(t, 200, resp.Status)

		t.Log("After first close:", dumpHandleMap(vssServer))

		// Second close - should fail
		resp, err = clientSession.Call("vss/Close", closePayloadBytes)
		assert.NoError(t, err, "Call should succeed but response should indicate error")
		assert.NotEqual(t, 200, resp.Status, "Second close with same handle should return error status")

		t.Log("After second close:", dumpHandleMap(vssServer))
	})
}

func TestHandleLseek(t *testing.T) {
	// Setup test directory structure
	testDir, err := os.MkdirTemp("", "vssfs-lseek-test")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	// Create a sparse file
	sparseFilePath := filepath.Join(testDir, "sparse.bin")
	f, err := os.OpenFile(sparseFilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Define the FileZeroDataInformation structure
	type FileZeroDataInformation struct {
		FileOffset      int64
		BeyondFinalZero int64
	}

	// Get Windows handle for the file
	winFile := windows.Handle(f.Fd())

	// Set sparse file attribute
	var bytesReturned uint32
	err = windows.DeviceIoControl(
		winFile,
		windows.FSCTL_SET_SPARSE,
		nil,
		0,
		nil,
		0,
		&bytesReturned,
		nil,
	)
	require.NoError(t, err, "Failed to set sparse attribute")

	// First, set the file size to create a sparse region
	err = f.Truncate(20480)
	require.NoError(t, err)

	// Create data for our blocks
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 251)
	}

	// Define our data regions
	type dataRegion struct {
		offset int64
		data   []byte
	}

	regions := []dataRegion{
		{offset: 0, data: data},     // First block at 0
		{offset: 8192, data: data},  // Second block at 8k
		{offset: 16384, data: data}, // Third block at 16k
	}

	// Track the end of the previous data region
	var prevEnd int64 = 0

	// Write data regions and mark the holes explicitly
	for _, region := range regions {
		// Define the region to mark as sparse (from end of previous data to start of this data)
		if region.offset > prevEnd {
			zeroRegion := FileZeroDataInformation{
				FileOffset:      prevEnd,
				BeyondFinalZero: region.offset,
			}

			err = windows.DeviceIoControl(
				winFile,
				windows.FSCTL_SET_ZERO_DATA,
				(*byte)(unsafe.Pointer(&zeroRegion)),
				uint32(unsafe.Sizeof(zeroRegion)),
				nil,
				0,
				&bytesReturned,
				nil,
			)
			require.NoError(t, err, "Failed to mark region as sparse")
		}

		// Write the data
		_, err = f.WriteAt(region.data, region.offset)
		require.NoError(t, err)

		prevEnd = region.offset + int64(len(region.data))
	}

	// Mark the final region as sparse (if needed)
	if prevEnd < 20480 {
		zeroRegion := FileZeroDataInformation{
			FileOffset:      prevEnd,
			BeyondFinalZero: 20480,
		}

		err = windows.DeviceIoControl(
			winFile,
			windows.FSCTL_SET_ZERO_DATA,
			(*byte)(unsafe.Pointer(&zeroRegion)),
			uint32(unsafe.Sizeof(zeroRegion)),
			nil,
			0,
			&bytesReturned,
			nil,
		)
		require.NoError(t, err, "Failed to mark final region as sparse")
	}

	// Force flush to disk
	err = f.Sync()
	require.NoError(t, err)

	// Verify the file is sparse and has the correct ranges
	ranges, err := queryAllocatedRanges(winFile, 20480)
	require.NoError(t, err)
	t.Log("Initial allocated ranges:")
	for i, r := range ranges {
		t.Logf("Range %d: offset=%d, length=%d", i, r.FileOffset, r.Length)
	}
	require.Equal(t, 3, len(ranges), "File should have exactly three allocated ranges")

	// Verify the ranges are at the expected positions
	if len(ranges) == 3 {
		assert.Equal(t, int64(0), ranges[0].FileOffset, "First range should start at 0")
		assert.Equal(t, int64(4096), ranges[0].Length, "First range should be 4KB")

		assert.Equal(t, int64(8192), ranges[1].FileOffset, "Second range should start at 8KB")
		assert.Equal(t, int64(4096), ranges[1].Length, "Second range should be 4KB")

		assert.Equal(t, int64(16384), ranges[2].FileOffset, "Third range should start at 16KB")
		assert.Equal(t, int64(4096), ranges[2].Length, "Third range should be 4KB")
	}

	// Close and reopen the file to ensure ranges are persisted
	f.Close()

	// Setup arpc server and client
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start the server
	serverRouter := arpc.NewRouter()
	vssServer := NewVSSFSServer("vss", &snapshots.WinVSSSnapshot{SnapshotPath: testDir, DriveLetter: ""})
	vssServer.RegisterHandlers(serverRouter)

	serverSession, err := arpc.NewServerSession(serverConn, nil)
	require.NoError(t, err)

	go func() {
		err := serverSession.Serve(serverRouter)
		if err != nil && ctx.Err() == nil && !strings.Contains(err.Error(), "closed pipe") {
			t.Errorf("Server error: %v", err)
		}
	}()
	defer serverSession.Close()

	// Setup client
	clientSession, err := arpc.NewClientSession(clientConn, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	// Helper function to perform Lseek
	doLseek := func(handleID FileHandleId, offset int64, whence int) (int64, error) {
		payload := LseekReq{
			HandleID: handleID,
			Offset:   offset,
			Whence:   whence,
		}
		payloadBytes, _ := payload.MarshalMsg(nil)
		raw, err := clientSession.CallMsg(ctx, "vss/Lseek", payloadBytes)
		if err != nil {
			return 0, err
		}
		var result LseekResp
		_, err = result.UnmarshalMsg(raw)
		if err != nil {
			return 0, err
		}
		return result.NewOffset, nil
	}

	// Open the sparse file
	openPayload := OpenFileReq{Path: "sparse.bin", Flag: 0, Perm: 0644}
	payloadBytes, _ := openPayload.MarshalMsg(nil)
	var openResult FileHandleId
	raw, err := clientSession.CallMsg(ctx, "vss/OpenFile", payloadBytes)
	require.NoError(t, err)
	openResult.UnmarshalMsg(raw)

	t.Run("StandardSeek", func(t *testing.T) {
		tests := []struct {
			name        string
			offset      int64
			whence      int
			expectedPos int64
			expectError bool
		}{
			{"SeekStart", 1000, io.SeekStart, 1000, false},
			{"SeekCurrent", 500, io.SeekCurrent, 1500, false},
			{"SeekEnd", -1000, io.SeekEnd, 19480, false}, // 20480 - 1000
			{"SeekNegative", -30000, io.SeekStart, 0, true},
			{"SeekPastEnd", 30000, io.SeekStart, 30000, false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				pos, err := doLseek(openResult, tt.offset, tt.whence)
				if tt.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					assert.Equal(t, tt.expectedPos, pos)
				}
			})
		}
	})

	t.Run("SparseSeek", func(t *testing.T) {
		tests := []struct {
			name       string
			offset     int64
			whence     int
			validateFn func(t *testing.T, offset int64, err error)
		}{
			{
				name:   "SeekDataStart",
				offset: 0,
				whence: SeekData,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(0), offset)
				},
			},
			{
				name:   "SeekDataInHole",
				offset: 5000,
				whence: SeekData,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(8192), offset)
				},
			},
			{
				name:   "SeekDataEnd",
				offset: 17000,
				whence: SeekData,
				validateFn: func(t *testing.T, offset int64, err error) {
					if err != nil {
						assert.ErrorIs(t, err, syscall.ENXIO)
					} else {
						// Some filesystems might return the last data block
						assert.GreaterOrEqual(t, offset, int64(16384))
					}
				},
			},
			{
				name:   "SeekHoleStart",
				offset: 0,
				whence: SeekHole,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(4096), offset, "Should find hole after first data block")
				},
			},
			{
				name:   "SeekHoleInData",
				offset: 2000,
				whence: SeekHole,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(4096), offset, "Should find next hole from within data block")
				},
			},
			{
				name:   "SeekHoleInHole",
				offset: 6000,
				whence: SeekHole,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(6000), offset, "Should return same offset when already in hole")
				},
			},
			{
				name:   "SeekDataInLastBlock",
				offset: 16500,
				whence: SeekData,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(16500), offset, "Should return same offset when in last data block")
				},
			},
			{
				name:   "SeekHoleAfterLastBlock",
				offset: 19000,
				whence: SeekHole,
				validateFn: func(t *testing.T, offset int64, err error) {
					require.NoError(t, err)
					assert.Equal(t, int64(20480), offset, "Should return EOF when seeking hole after last data")
				},
			},
			{
				name:   "SeekDataPastEOF",
				offset: 21000,
				whence: SeekData,
				validateFn: func(t *testing.T, offset int64, err error) {
					assert.ErrorIs(t, err, syscall.ENXIO, "Should return ENXIO when seeking data past EOF")
				},
			},
			{
				name:   "SeekHolePastEOF",
				offset: 21000,
				whence: SeekHole,
				validateFn: func(t *testing.T, offset int64, err error) {
					assert.ErrorIs(t, err, syscall.ENXIO, "Should return ENXIO when seeking hole past EOF")
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				offset, err := doLseek(openResult, tt.offset, tt.whence)
				tt.validateFn(t, offset, err)
			})
		}
	})

	t.Run("InvalidHandle", func(t *testing.T) {
		_, err := doLseek(FileHandleId("invalid"), 0, io.SeekStart)
		assert.Error(t, err)
	})

	t.Run("InvalidWhence", func(t *testing.T) {
		_, err := doLseek(openResult, 0, 999)
		assert.Error(t, err)
	})

	// Close the file
	closePayload := CloseReq{HandleID: openResult}
	closePayloadBytes, _ := closePayload.MarshalMsg(nil)
	resp, err := clientSession.Call("vss/Close", closePayloadBytes)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
}
