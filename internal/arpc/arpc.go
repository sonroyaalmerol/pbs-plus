package arpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

// Session wraps an underlying smux.Session with improved connection management.
type Session struct {
	// muxSess holds a *smux.Session.
	muxSess atomic.Value

	// Connection state management
	reconnectConfig *ReconnectConfig
	reconnectMu     sync.Mutex

	// Connection state tracking
	state atomic.Int32 // Stores ConnectionState

	// Circuit breaker and notification
	circuitOpen    atomic.Bool
	circuitResetAt atomic.Int64
	reconnectChan  chan struct{} // Notifies waiters when reconnection completes

	// Context for coordinating shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc

	streamQueue chan *smux.Stream
	workerPool  *WorkerPool
}

// NewServerSession creates a new Session for a server connection.
func NewServerSession(conn net.Conn, config *smux.Config) (*Session, error) {
	if config == nil {
		config = defaultSmuxConfig()
	}

	s, err := smux.Server(conn, config)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	// For CPU-bound workloads
	workerPool := NewWorkerPool(ctx, WorkerPoolConfig{
		Workers:   runtime.GOMAXPROCS(0) * 4, // More workers than cores for I/O tasks
		QueueSize: 200 * 8,                   // Larger queue for I/O tasks
	})

	session := &Session{
		reconnectConfig: nil,
		reconnectChan:   make(chan struct{}, 1), // Buffer of 1 to prevent blocking
		ctx:             ctx,
		cancelFunc:      cancel,
		workerPool:      workerPool,
	}
	session.muxSess.Store(s)
	session.state.Store(int32(StateConnected))

	return session, nil
}

// NewClientSession creates a new Session for a client connection.
func NewClientSession(conn net.Conn, config *smux.Config) (*Session, error) {
	if config == nil {
		config = defaultSmuxConfig()
	}

	s, err := smux.Client(conn, config)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	workerPool := NewWorkerPool(ctx, WorkerPoolConfig{
		Workers:   runtime.GOMAXPROCS(0) * 4,
		QueueSize: 8,
	})

	session := &Session{
		reconnectConfig: nil,
		reconnectChan:   make(chan struct{}, 1), // Buffer of 1 to prevent blocking
		ctx:             ctx,
		cancelFunc:      cancel,
		workerPool:      workerPool,
	}
	session.muxSess.Store(s)
	session.state.Store(int32(StateConnected))

	return session, nil
}

// Serve accepts streams and processes them using the worker pool instead of creating a new goroutine for each stream
func (s *Session) Serve(router *Router) error {
	for {
		curSession := s.muxSess.Load().(*smux.Session)
		rc := s.reconnectConfig

		stream, err := curSession.AcceptStream()
		if err != nil {
			s.state.Store(int32(StateDisconnected))
			if rc != nil && rc.AutoReconnect {
				if err2 := s.attemptReconnect(); err2 != nil {
					s.workerPool.Shutdown() // Shutdown worker pool on error
					return err2
				}
				continue
			}
			s.workerPool.Shutdown() // Shutdown worker pool on error
			return err
		}

		// Submit the stream to the worker pool instead of spawning a new goroutine
		s.workerPool.Submit(stream, router)
	}
}

// defaultSmuxConfig returns a default smux configuration
func defaultSmuxConfig() *smux.Config {
	defaults := smux.DefaultConfig()
	return defaults
}

func ConnectToServer(ctx context.Context, serverAddr string, headers http.Header, tlsConfig *tls.Config) (*Session, error) {
	dialFunc := func() (net.Conn, error) {
		return tls.Dial("tcp", serverAddr, tlsConfig)
	}

	upgradeFunc := func(conn net.Conn) (*Session, error) {
		return upgradeHTTPClient(conn, "/plus/arpc", serverAddr, headers, nil)
	}

	// Use DialWithBackoff for the initial connection
	session, err := dialWithBackoff(
		ctx,
		dialFunc,
		upgradeFunc,
		100*time.Millisecond, // Initial backoff
		30*time.Second,       // Max backoff
	)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	// Configure auto-reconnect with the same parameters
	session.EnableAutoReconnect(&ReconnectConfig{
		AutoReconnect:    true,
		DialFunc:         dialFunc,
		UpgradeFunc:      upgradeFunc,
		InitialBackoff:   100 * time.Millisecond,
		MaxBackoff:       30 * time.Second,
		MaxRetries:       10,
		BackoffJitter:    0.2,
		CircuitBreakTime: 60 * time.Second,
		ReconnectCtx:     ctx,
	})

	return session, nil
}

// Close closes the session and stops the reconnection monitor
func (s *Session) Close() error {
	s.cancelFunc() // Stop the connection monitor

	sess := s.muxSess.Load().(*smux.Session)
	if sess != nil {
		return sess.Close()
	}
	return nil
}
