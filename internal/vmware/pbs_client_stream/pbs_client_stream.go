package pbsclientstream

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"github.com/cornelk/hashmap"
	pbsclientgo "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_lib"
	"github.com/sonroyaalmerol/pbs-plus/internal/vmware/shared"
)

type PBSStream struct {
	client       *pbsclientgo.PBSClient
	newchunk     *atomic.Uint64
	reusechunk   *atomic.Uint64
	streamChunks map[string]*pbsclientgo.ChunkState
}

func New(client *pbsclientgo.PBSClient) *PBSStream {
	return &PBSStream{
		client:       client,
		newchunk:     new(atomic.Uint64),
		reusechunk:   new(atomic.Uint64),
		streamChunks: make(map[string]*pbsclientgo.ChunkState),
	}
}

func (s *PBSStream) Upload(name string, vfs map[string]*shared.DatastoreFileInfo) error {
	knownChunks := hashmap.New[string, bool]()
	s.client.Connect(false)

	archive := &pbsclientgo.PXARArchive{}
	archive.ArchiveName = name + ".pxar.didx"

	previousDidx, err := s.client.DownloadPreviousToBytes(archive.ArchiveName)
	if err != nil {
		return err
	}

	fmt.Printf("Downloaded previous DIDX: %d bytes\n", len(previousDidx))

	/*
		Here we download the previous dynamic index to figure out which chunks are the same of what
		we are going to upload to avoid unnecessary traffic and compression cpu usage
	*/

	if !bytes.HasPrefix(previousDidx, pbsclientgo.DIDX_MAGIC) {
		fmt.Printf("Previous index has wrong magic (%s)!\n", previousDidx[:8])
	} else {
		//Header as per proxmox documentation is fixed size of 4096 bytes,
		//then offset of type uint64 and sha256 digests follow , so 40 byte each record until EOF
		previousDidx = previousDidx[4096:]
		for i := 0; i*40 < len(previousDidx); i += 1 {
			e := pbsclientgo.DidxEntry{}
			// e.Offset = binary.LittleEndian.Uint64(previousDidx[i*40 : i*40+8])
			e.Digest = previousDidx[i*40+8 : i*40+40]
			shahash := hex.EncodeToString(e.Digest)
			fmt.Printf("Previous: %s\n", shahash)
			knownChunks.Set(shahash, true)
		}
	}

	fmt.Printf("Known chunks: %d!\n", knownChunks.Len())

	pxarChunk := pbsclientgo.ChunkState{}
	pxarChunk.Init(s.newchunk, s.reusechunk, knownChunks)

	pcat1Chunk := pbsclientgo.ChunkState{}
	pcat1Chunk.Init(s.newchunk, s.reusechunk, knownChunks)

	pxarChunk.Wrid, err = s.client.CreateDynamicIndex(archive.ArchiveName)
	if err != nil {
		return err
	}
	pcat1Chunk.Wrid, err = s.client.CreateDynamicIndex("catalog.pcat1.didx")
	if err != nil {
		return err
	}

	archive.WriteCB = func(b []byte) {
		pxarChunk.HandleData(b, s.client)
	}

	archive.CatalogWriteCB = func(b []byte) {
		pcat1Chunk.HandleData(b, s.client)
	}

	//This is the entry point of backup job which will start streaming with the PCAT and PXAR write callback
	//Data to be hashed and eventually uploaded
	archive.WriteVirtualDir(vfs, "", true)

	pxarChunk.Eof(s.client)
	pcat1Chunk.Eof(s.client)

	return nil
}

func (s *PBSStream) Close() error {
	err := s.client.UploadManifest()
	if err != nil {
		return err
	}

	return s.client.Finish()
}
