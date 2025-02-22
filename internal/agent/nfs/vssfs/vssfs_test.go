//go:build windows
// +build windows

package vssfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonroyaalmerol/pbs-plus/internal/agent/snapshots"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs/file"
	"golang.org/x/sys/windows"
)

func setupTestEnvironment(t *testing.T) (string, *snapshots.WinVSSSnapshot, func()) {
	tempDir, err := os.MkdirTemp("", "vssfs-test-")
	require.NoError(t, err)

	// Create test directory structure using Windows paths
	dirs := []string{
		"testdata",
		"testdata/subdir",
		"testdata/excluded_dir",
	}

	files := []string{
		"testdata/regular_file.txt",
		"testdata/subdir/file_in_subdir.txt",
		"testdata/system_file.txt",
	}

	for _, dir := range dirs {
		err := os.MkdirAll(filepath.Join(tempDir, dir), 0755)
		require.NoError(t, err)
	}

	for _, file := range files {
		err := os.WriteFile(filepath.Join(tempDir, file), []byte("test"), 0644)
		require.NoError(t, err)
	}

	// Set system attribute on test file
	systemFile := filepath.Join(tempDir, "testdata/system_file.txt")
	err = windows.SetFileAttributes(
		windows.StringToUTF16Ptr(systemFile),
		windows.FILE_ATTRIBUTE_SYSTEM,
	)
	require.NoError(t, err)

	snapshot := &snapshots.WinVSSSnapshot{
		SnapshotPath: tempDir,
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return tempDir, snapshot, cleanup
}

func TestStat(t *testing.T) {
	_, snapshot, cleanup := setupTestEnvironment(t)
	defer cleanup()

	fs := NewVSSFS(snapshot, "testdata").(*VSSFS)

	t.Run("regular file", func(t *testing.T) {
		info, err := fs.Stat("regular_file.txt")
		require.NoError(t, err)
		assert.False(t, info.IsDir())
		assert.Equal(t, "regular_file.txt", info.Name())
	})

	t.Run("directory", func(t *testing.T) {
		info, err := fs.Stat("subdir")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("root directory", func(t *testing.T) {
		info, err := fs.Stat("/")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, "/", info.Name())
	})

	t.Run("current directory", func(t *testing.T) {
		info, err := fs.Stat(".")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, ".", info.Name())
	})
}

func TestReadDir(t *testing.T) {
	_, snapshot, cleanup := setupTestEnvironment(t)
	defer cleanup()

	fs := NewVSSFS(snapshot, "testdata").(*VSSFS)

	t.Run("root directory listing", func(t *testing.T) {
		entries, err := fs.ReadDir("/")
		require.NoError(t, err)

		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}

		assert.ElementsMatch(t, []string{"regular_file.txt", "subdir", "system_file.txt", "excluded_dir"}, names)
	})
}

func TestPathHandling(t *testing.T) {
	_, snapshot, cleanup := setupTestEnvironment(t)
	defer cleanup()
	fs := NewVSSFS(snapshot, "testdata").(*VSSFS)

	t.Run("mixed slashes in path", func(t *testing.T) {
		info, err := fs.Stat("subdir\\file_in_subdir.txt")
		require.NoError(t, err)
		assert.Equal(t, "file_in_subdir.txt", info.Name())
	})

	t.Run("relative path resolution", func(t *testing.T) {
		info, err := fs.Stat("./subdir/../regular_file.txt")
		require.NoError(t, err)
		assert.Equal(t, "regular_file.txt", info.Name())
	})
}

func TestNFSMetadata(t *testing.T) {
	_, snapshot, cleanup := setupTestEnvironment(t)
	defer cleanup()
	fs := NewVSSFS(snapshot, "testdata").(*VSSFS)

	t.Run("file metadata", func(t *testing.T) {
		info, err := fs.Stat("regular_file.txt")
		require.NoError(t, err)
		sys := info.(*VSSFileInfo).Sys().(file.FileInfo)
		assert.NotZero(t, sys.Fileid)
	})

	t.Run("directory metadata", func(t *testing.T) {
		info, err := fs.Stat("subdir")
		require.NoError(t, err)
		sys := info.(*VSSFileInfo).Sys().(file.FileInfo)
		assert.Equal(t, uint32(2), sys.Nlink)
	})
}
