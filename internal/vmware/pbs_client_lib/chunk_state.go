package pbsclientgo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sync/atomic"

	"github.com/cornelk/hashmap"
)

var DIDX_MAGIC = []byte{28, 145, 78, 165, 25, 186, 179, 205}

type ChunkState struct {
	assignments       []string
	assignmentsOffset []uint64
	Pos               uint64
	Wrid              uint64
	ChunkCount        uint64
	ChunkDigests      hash.Hash
	currentChunk      []byte
	C                 Chunker
	newchunk          *atomic.Uint64
	reusechunk        *atomic.Uint64
	knownChunks       *hashmap.Map[string, bool]
}

type DidxEntry struct {
	Offset uint64
	Digest []byte
}

func (c *ChunkState) Init(newchunk *atomic.Uint64, reusechunk *atomic.Uint64, knownChunks *hashmap.Map[string, bool]) {
	c.assignments = make([]string, 0)
	c.assignmentsOffset = make([]uint64, 0)
	c.Pos = 0
	c.ChunkCount = 0
	c.ChunkDigests = sha256.New()
	c.currentChunk = make([]byte, 0)
	c.C = Chunker{}
	c.C.New(1024 * 1024 * 4)
	c.reusechunk = reusechunk
	c.newchunk = newchunk
	c.knownChunks = knownChunks
}

func (c *ChunkState) HandleData(b []byte, client *PBSClient) {
	chunkpos := c.C.Scan(b)

	if chunkpos == 0 {
		//No break happened, just append data
		c.currentChunk = append(c.currentChunk, b...)
	} else {

		for chunkpos > 0 {
			//Append data until break position
			c.currentChunk = append(c.currentChunk, b[:chunkpos]...)

			h := sha256.New()
			// TODO: error handling inside callback
			h.Write(c.currentChunk)
			bindigest := h.Sum(nil)
			shahash := hex.EncodeToString(bindigest)

			if _, ok := c.knownChunks.GetOrInsert(shahash, true); !ok {
				fmt.Printf("New chunk[%s] %d bytes\n", shahash, len(c.currentChunk))
				c.newchunk.Add(1)

				client.UploadCompressedChunk(c.Wrid, shahash, c.currentChunk)
			} else {
				fmt.Printf("Reuse chunk[%s] %d bytes\n", shahash, len(c.currentChunk))
				c.reusechunk.Add(1)
			}

			// TODO: error handling inside callback
			binary.Write(c.ChunkDigests, binary.LittleEndian, (c.Pos + uint64(len(c.currentChunk))))
			// TODO: error handling inside callback
			c.ChunkDigests.Write(h.Sum(nil))

			c.assignmentsOffset = append(c.assignmentsOffset, c.Pos)
			c.assignments = append(c.assignments, shahash)
			c.Pos += uint64(len(c.currentChunk))
			c.ChunkCount += 1

			c.currentChunk = make([]byte, 0)
			b = b[chunkpos:] //Take remainder of data
			chunkpos = c.C.Scan(b)

		}

		//No further break happened, append remaining data
		c.currentChunk = append(c.currentChunk, b...)
	}
}

func (c *ChunkState) Eof(client *PBSClient) {
	//Here we write the remainder of data for which cyclic hash did not trigger
	if len(c.currentChunk) > 0 {
		h := sha256.New()
		_, err := h.Write(c.currentChunk)
		if err != nil {
			panic(err)
		}

		shahash := hex.EncodeToString(h.Sum(nil))
		binary.Write(c.ChunkDigests, binary.LittleEndian, (c.Pos + uint64(len(c.currentChunk))))
		c.ChunkDigests.Write(h.Sum(nil))

		if _, ok := c.knownChunks.GetOrInsert(shahash, true); !ok {
			fmt.Printf("New chunk[%s] %d bytes\n", shahash, len(c.currentChunk))
			client.UploadCompressedChunk(c.Wrid, shahash, c.currentChunk)
			c.newchunk.Add(1)
		} else {
			fmt.Printf("Reuse chunk[%s] %d bytes\n", shahash, len(c.currentChunk))
			c.reusechunk.Add(1)
		}
		c.assignmentsOffset = append(c.assignmentsOffset, c.Pos)
		c.assignments = append(c.assignments, shahash)
		c.Pos += uint64(len(c.currentChunk))
		c.ChunkCount += 1

	}

	//Avoid incurring in request entity too large by chunking assignment PUT requests in blocks of at most 128 chunks
	for k := 0; k < len(c.assignments); k += 128 {
		k2 := k + 128
		if k2 > len(c.assignments) {
			k2 = len(c.assignments)
		}
		client.AssignChunks(c.Wrid, c.assignments[k:k2], c.assignmentsOffset[k:k2])
	}

	client.CloseDynamicIndex(c.Wrid, hex.EncodeToString(c.ChunkDigests.Sum(nil)), c.Pos, c.ChunkCount)
}
