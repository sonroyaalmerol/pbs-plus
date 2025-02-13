package pbsclientgo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dchest/siphash"
)

const (
	PXAR_ENTRY               uint64 = 0xd5956474e588acef
	PXAR_ENTRY_V1            uint64 = 0x11da850a1c1cceff
	PXAR_FILENAME            uint64 = 0x16701121063917b3
	PXAR_SYMLINK             uint64 = 0x27f971e7dbf5dc5f
	PXAR_DEVICE              uint64 = 0x9fc9e906586d5ce9
	PXAR_XATTR               uint64 = 0x0dab0229b57dcd03
	PXAR_ACL_USER            uint64 = 0x2ce8540a457d55b8
	PXAR_ACL_GROUP           uint64 = 0x136e3eceb04c03ab
	PXAR_ACL_GROUP_OBJ       uint64 = 0x10868031e9582876
	PXAR_ACL_DEFAULT         uint64 = 0xbbbb13415a6896f5
	PXAR_ACL_DEFAULT_USER    uint64 = 0xc89357b40532cd1f
	PXAR_ACL_DEFAULT_GROUP   uint64 = 0xf90a8a5816038ffe
	PXAR_FCAPS               uint64 = 0x2da9dd9db5f7fb67
	PXAR_QUOTA_PROJID        uint64 = 0xe07540e82f7d1cbb
	PXAR_HARDLINK            uint64 = 0x51269c8422bd7275
	PXAR_PAYLOAD             uint64 = 0x28147a1b0b7c1a25
	PXAR_GOODBYE             uint64 = 0x2fec4fa642d5731d
	PXAR_GOODBYE_TAIL_MARKER uint64 = 0xef5eed5b753e1555
)

var catalogMagic = []byte{145, 253, 96, 249, 196, 103, 88, 213}

const (
	IFMT   uint64 = 0o0170000
	IFSOCK uint64 = 0o0140000
	IFLNK  uint64 = 0o0120000
	IFREG  uint64 = 0o0100000
	IFBLK  uint64 = 0o0060000
	IFDIR  uint64 = 0o0040000
	IFCHR  uint64 = 0o0020000
	IFIFO  uint64 = 0o0010000

	ISUID uint64 = 0o0004000
	ISGID uint64 = 0o0002000
	ISVTX uint64 = 0o0001000
)

type MTime struct {
	secs    uint64
	nanos   uint32
	padding uint32
}

type PXARFileEntry struct {
	hdr   uint64
	len   uint64
	mode  uint64
	flags uint64
	uid   uint32
	gid   uint32
	mtime MTime
}

type PXARFilenameEntry struct {
	hdr uint64
	len uint64
}

type GoodByeItem struct {
	hash   uint64
	offset uint64
	len    uint64
}

type GoodByeBST struct {
	self  *GoodByeItem
	left  *GoodByeBST
	right *GoodByeBST
}

func (bst *GoodByeBST) AddNode(i *GoodByeItem) {
	if i.hash < bst.self.hash {
		if bst.left == nil {
			bst.left = &GoodByeBST{self: i}
		} else {
			bst.left.AddNode(i)
		}
	} else if i.hash > bst.self.hash {
		if bst.right == nil {
			bst.right = &GoodByeBST{self: i}
		} else {
			bst.right.AddNode(i)
		}
	}
}

// Callback type for writing out slices of bytes.
type PXAROutCB func([]byte)

// PXARArchive holds the state for building a PXAR archive.
type PXARArchive struct {
	writeCB        PXAROutCB
	catalogWriteCB PXAROutCB
	buffer         bytes.Buffer
	pos            uint64
	ArchiveName    string
	catalogPos     uint64
}

// Flush the internal buffer to the output callback and update the position.
func (a *PXARArchive) Flush() {
	b := make([]byte, 64*1024)
	for {
		n, _ := a.buffer.Read(b)
		if n <= 0 {
			break
		}
		if a.writeCB != nil {
			a.writeCB(b[:n])
		}
		a.pos += uint64(n)
	}
}

// Create resets the archive state.
func (a *PXARArchive) Create() {
	a.pos = 0
	a.catalogPos = 8
}

// Helper for binary writing that writes data to the internal buffer.
func (a *PXARArchive) writeBinary(data interface{}) error {
	return binary.Write(&a.buffer, binary.LittleEndian, data)
}

// CatalogDir and CatalogFile are used for building a final catalog.
type CatalogDir struct {
	Pos  uint64 // Points to the next table; parent's table must be written before children
	Name string
}

type CatalogFile struct {
	Name  string
	MTime uint64
	Size  uint64
}

// appendU647bit encodes a uint64 in 7–bit chunks.
func appendU647bit(a []byte, v uint64) []byte {
	for {
		if v < 128 {
			a = append(a, byte(v&0x7f))
			break
		}
		a = append(a, byte(v&0x7f)|0x80)
		v = v >> 7
	}
	return a
}

// WriteDir writes a directory to the archive. If toplevel is true, additional
// catalog information is written. It returns a CatalogDir structure or an error.
func (a *PXARArchive) WriteDir(path, dirname string, toplevel bool) (CatalogDir, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return CatalogDir{}, fmt.Errorf("reading directory %q: %w", path, err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		return CatalogDir{}, fmt.Errorf("stat %q: %w", path, err)
	}

	// Write a filename entry for non–toplevel directories.
	if !toplevel {
		fnameEntry := PXARFilenameEntry{
			hdr: PXAR_FILENAME,
			len: uint64(16 + len(dirname) + 1),
		}
		if err := a.writeBinary(fnameEntry); err != nil {
			return CatalogDir{}, err
		}
		a.buffer.WriteString(dirname)
		a.buffer.WriteByte(0x00)
	} else if a.catalogWriteCB != nil {
		a.catalogWriteCB(catalogMagic)
		a.catalogPos = 8
	}

	a.Flush()

	dirStartPos := a.pos

	// Write the directory file entry.
	entry := PXARFileEntry{
		hdr:   PXAR_ENTRY,
		len:   56,
		mode:  IFDIR | 0o777,
		flags: 0,
		uid:   1000, // fixed UID/GID for Windows targets
		gid:   1000,
		mtime: MTime{
			secs:    uint64(fi.ModTime().Unix()),
			nanos:   0,
			padding: 0,
		},
	}
	if err := a.writeBinary(entry); err != nil {
		return CatalogDir{}, err
	}
	a.Flush()

	// Prepare slices for catalog and goodbye items.
	var goodByeItems []GoodByeItem
	var catalogFiles []CatalogFile
	var catalogDirs []CatalogDir

	// Process all files and subdirectories.
	for _, entry := range entries {
		startPos := a.pos
		name := entry.Name()
		fullPath := filepath.Join(path, name)
		if entry.IsDir() {
			dirCat, err := a.WriteDir(fullPath, name, false)
			if err != nil {
				return CatalogDir{}, err
			}
			catalogDirs = append(catalogDirs, dirCat)
			goodByeItems = append(goodByeItems, GoodByeItem{
				offset: startPos,
				hash:   siphash.Hash(0x83ac3f1cfbb450db, 0xaa4f1b6879369fbd, []byte(name)),
				len:    a.pos - startPos,
			})
		} else {
			catFile, err := a.WriteFile(fullPath, name)
			if err != nil {
				return CatalogDir{}, err
			}
			catalogFiles = append(catalogFiles, catFile)
			goodByeItems = append(goodByeItems, GoodByeItem{
				offset: startPos,
				hash:   siphash.Hash(0x83ac3f1cfbb450db, 0xaa4f1b6879369fbd, []byte(name)),
				len:    a.pos - startPos,
			})
		}
	}

	// Write the catalog table.
	oldCatalogPos := a.catalogPos
	var tableData []byte
	count := uint64(len(catalogFiles) + len(catalogDirs))
	tableData = appendU647bit(tableData, count)
	for _, d := range catalogDirs {
		tableData = append(tableData, 'd')
		tableData = appendU647bit(tableData, uint64(len(d.Name)))
		tableData = append(tableData, []byte(d.Name)...)
		tableData = appendU647bit(tableData, oldCatalogPos-d.Pos)
	}
	for _, f := range catalogFiles {
		tableData = append(tableData, 'f')
		tableData = appendU647bit(tableData, uint64(len(f.Name)))
		tableData = append(tableData, []byte(f.Name)...)
		tableData = appendU647bit(tableData, f.Size)
		tableData = appendU647bit(tableData, f.MTime)
	}
	var catalogOut []byte
	catalogOut = appendU647bit(catalogOut, uint64(len(tableData)))
	catalogOut = append(catalogOut, tableData...)
	if a.catalogWriteCB != nil {
		a.catalogWriteCB(catalogOut)
	}
	a.catalogPos += uint64(len(catalogOut))
	a.Flush()

	// Build goodbye BST tree.
	sort.Slice(goodByeItems, func(i, j int) bool {
		return goodByeItems[i].hash < goodByeItems[j].hash
	})
	goodByeNew := make([]GoodByeItem, len(goodByeItems))
	caMakeBst(goodByeItems, &goodByeNew)
	goodByeItems = goodByeNew

	a.Flush()
	goodByeStart := a.pos

	if err := a.writeBinary(PXAR_GOODBYE); err != nil {
		return CatalogDir{}, err
	}
	goodByeLen := uint64(16 + 24*uint64(len(goodByeItems)+1))
	if err := a.writeBinary(goodByeLen); err != nil {
		return CatalogDir{}, err
	}

	// Write each goodbye item after adjusting its offset.
	for i := range goodByeItems {
		goodByeItems[i].offset = a.pos - goodByeItems[i].offset
		if err := a.writeBinary(goodByeItems[i]); err != nil {
			return CatalogDir{}, err
		}
	}

	// Tail marker.
	tail := GoodByeItem{
		offset: goodByeStart - dirStartPos,
		len:    goodByeLen,
		hash:   PXAR_GOODBYE_TAIL_MARKER,
	}
	if err := a.writeBinary(tail); err != nil {
		return CatalogDir{}, err
	}
	a.Flush()

	// If this is the top–level directory, update the catalog pointer.
	if toplevel {
		var ptrTable []byte
		ptrTable = appendU647bit(ptrTable, 1)
		ptrTable = append(ptrTable, 'd')
		ptrTable = appendU647bit(ptrTable, uint64(len(a.ArchiveName)))
		ptrTable = append(ptrTable, []byte(a.ArchiveName)...)
		ptrTable = appendU647bit(ptrTable, a.catalogPos-oldCatalogPos)
		var catalogPtr []byte
		catalogPtr = appendU647bit(catalogPtr, uint64(len(ptrTable)))
		catalogPtr = append(catalogPtr, ptrTable...)
		// Write pointer to catalog position.
		ptr := make([]byte, 8)
		binary.LittleEndian.PutUint64(ptr, a.catalogPos)
		if a.catalogWriteCB != nil {
			a.catalogWriteCB(catalogPtr)
			a.catalogWriteCB(ptr)
		}
	}

	return CatalogDir{
		Name: dirname,
		Pos:  oldCatalogPos,
	}, nil
}

// WriteFile writes a file entry (including its payload) into the archive.
// It returns a CatalogFile structure or an error.
func (a *PXARArchive) WriteFile(path, basename string) (CatalogFile, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return CatalogFile{}, fmt.Errorf("stat %q: %w", path, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return CatalogFile{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	// Write the filename entry.
	fnameEntry := PXARFilenameEntry{
		hdr: PXAR_FILENAME,
		len: uint64(16 + len(basename) + 1),
	}
	if err := a.writeBinary(fnameEntry); err != nil {
		return CatalogFile{}, err
	}
	a.buffer.WriteString(basename)
	a.buffer.WriteByte(0x00)

	// Write the file entry.
	entry := PXARFileEntry{
		hdr:   PXAR_ENTRY,
		len:   56,
		mode:  IFREG | 0o777,
		flags: 0,
		uid:   1000,
		gid:   1000,
		mtime: MTime{
			secs:    uint64(fi.ModTime().Unix()),
			nanos:   0,
			padding: 0,
		},
	}
	if err := a.writeBinary(entry); err != nil {
		return CatalogFile{}, err
	}

	// Write payload header.
	if err := a.writeBinary(PXAR_PAYLOAD); err != nil {
		return CatalogFile{}, err
	}
	filesize := uint64(fi.Size()) + 16
	if err := a.writeBinary(filesize); err != nil {
		return CatalogFile{}, err
	}
	a.Flush()

	// Copy file contents in 64KiB chunks.
	readBuffer := make([]byte, 64*1024)
	for {
		n, err := file.Read(readBuffer)
		if n > 0 {
			a.buffer.Write(readBuffer[:n])
			a.Flush()
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return CatalogFile{}, fmt.Errorf("reading file %q: %w", path, err)
		}
	}
	a.Flush()

	return CatalogFile{
		Name:  basename,
		MTime: uint64(fi.ModTime().Unix()),
		Size:  uint64(fi.Size()),
	}, nil
}
