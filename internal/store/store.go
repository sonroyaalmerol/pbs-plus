//go:build linux

package store

import (
	"fmt"
	"sync"

	"github.com/sonroyaalmerol/pbs-plus/internal/arpc"
	"github.com/sonroyaalmerol/pbs-plus/internal/auth/certificates"
	"github.com/sonroyaalmerol/pbs-plus/internal/store/database"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
)

// Store holds the configuration system
type Store struct {
	CertGenerator *certificates.Generator
	Database      *database.Database
	aRPCs         map[string]*arpc.Session
	arpcsMux      sync.RWMutex
}

func Initialize(paths map[string]string) (*Store, error) {
	database, err := database.Initialize(paths)
	if err != nil {
		return nil, fmt.Errorf("Initialize: error initializing database -> %w", err)
	}

	store := &Store{
		Database: database,
		aRPCs:    make(map[string]*arpc.Session),
	}

	return store, nil
}

func (s *Store) AddARPC(client string, arpc *arpc.Session) {
	s.arpcsMux.Lock()
	defer s.arpcsMux.Unlock()

	syslog.L.Infof("Client %s added via aRPC", client)

	s.aRPCs[client] = arpc

	syslog.L.Infof("Total aRPC clients: %d", len(s.aRPCs))
}

func (s *Store) GetARPC(client string) *arpc.Session {
	s.arpcsMux.RLock()
	defer s.arpcsMux.RUnlock()

	arpc, ok := s.aRPCs[client]
	if !ok {
		return nil
	}

	return arpc
}

func (s *Store) RemoveARPC(client string) {
	s.arpcsMux.Lock()
	defer s.arpcsMux.Unlock()

	if clientA, ok := s.aRPCs[client]; ok {
		_ = clientA.Close()
		syslog.L.Infof("Client %s removed via aRPC", client)
		delete(s.aRPCs, client)
	}
}
