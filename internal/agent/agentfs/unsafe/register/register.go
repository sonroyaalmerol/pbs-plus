//go:build windows

package register

import (
	"log"
	"strconv"

	unsafefs "github.com/sonroyaalmerol/pbs-plus/internal/agent/agentfs/unsafe"
	"github.com/sonroyaalmerol/pbs-plus/internal/childgoroutine"
)

func init() {
	// Register with childgoroutine while using a session obtained here.
	childgoroutine.Register("unsafefs_readat", func(args string) {
		alloc, err := strconv.Atoi(args)
		if err != nil {
			alloc = 0
		}
		session := childgoroutine.SMux()
		if session == nil {
			log.Printf("failed to obtain smux session")
			return
		}

		server := unsafefs.Initialize(session, uint32(alloc))
		if server != nil {
			if err := server.ServeReadAt(); err != nil {
				log.Printf("error serving readat: %v", err)
			}
		} else {
			log.Printf("unsafefs.Initialize returned nil")
		}
	})
}
