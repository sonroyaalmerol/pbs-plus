package pbsclientstream

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"sync/atomic"

	"github.com/cornelk/hashmap"
	pbsclientgo "github.com/sonroyaalmerol/pbs-plus/internal/vmware/pbs_client_lib"
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

func (s *PBSStream) OpenConnection() {
	s.client.Connect(false)
}

func (s *PBSStream) UploadFile(filename string, stream io.Reader) error {
	knownChunks := hashmap.New[string, bool]()

	previousDidx, err := s.client.DownloadPreviousToBytes(filename + ".didx")
	if err != nil {
		log.Println("Previous DIDX not found.")
	} else {
		fmt.Printf("Downloaded previous DIDX: %d bytes\n", len(previousDidx))

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
	}

	streamChunk := pbsclientgo.ChunkState{}
	streamChunk.Init(s.newchunk, s.reusechunk, knownChunks)

	streamChunk.Wrid, err = s.client.CreateDynamicIndex(filename + ".didx")
	if err != nil {
		return err
	}

	B := make([]byte, 65536)
	for {
		n, err := stream.Read(B)
		b := B[:n]
		streamChunk.HandleData(b, s.client)

		if err == io.EOF {
			break
		}
	}

	streamChunk.Eof(s.client)
	s.client.CloseDynamicIndex(streamChunk.Wrid, hex.EncodeToString(streamChunk.ChunkDigests.Sum(nil)), streamChunk.Pos, streamChunk.ChunkCount)

	return nil
}

func (s *PBSStream) Close() error {
	err := s.client.UploadManifest()
	if err != nil {
		return err
	}

	return s.client.Finish()
}
