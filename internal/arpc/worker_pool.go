package arpc

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

var workItemPool = sync.Pool{
	New: func() interface{} {
		return &WorkItem{}
	},
}

// WorkItem represents a unit of work to be processed by the worker pool
type WorkItem struct {
	stream *smux.Stream
	router *Router
}

// WorkerPoolConfig provides configuration options for the worker pool
type WorkerPoolConfig struct {
	Workers int
	QueueSize int
}

// WorkerPoolMetrics contains operational metrics about the worker pool
type WorkerPoolMetrics struct {
	QueueDepth    int64 // Current number of items waiting in queue
	ProcessedJobs int64 // Total number of jobs processed
	ActiveWorkers int32 // Number of workers currently processing jobs
	TotalWorkers  int64 // Total number of workers in the pool
}

// Improved WorkerPool for heavy client loads
type WorkerPool struct {
	workers int
	queue   chan *WorkItem
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc

	// Monitoring metrics
	queueDepth    atomic.Int64
	processedJobs atomic.Int64
	activeWorkers atomic.Int32
}

// NewWorkerPool with configurable settings
func NewWorkerPool(ctx context.Context, config WorkerPoolConfig) *WorkerPool {
	workers := config.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	// For heavy workloads, we may want more queue capacity
	queueSize := config.QueueSize
	if queueSize <= 0 {
		queueSize = workers * 8 // Larger buffer for bursty workloads
	}

	ctx, cancel := context.WithCancel(ctx)
	pool := &WorkerPool{
		workers: workers,
		queue:   make(chan *WorkItem, queueSize),
		ctx:     ctx,
		cancel:  cancel,
	}

	// Start the workers
	pool.wg.Add(workers)
	for range workers {
		go pool.worker()
	}

	return pool
}

// Submit with backpressure handling
func (p *WorkerPool) Submit(stream *smux.Stream, router *Router) bool {
	// Get item from pool
	item := workItemPool.Get().(*WorkItem)
	item.stream = stream
	item.router = router

	// Update metrics before adding to queue
	currentDepth := p.queueDepth.Add(1)

	// Check if we're approaching capacity - implement progressive backpressure
	// Queue is getting full, apply more aggressive timeouts as it fills up
	var timeout time.Duration
	queueCapacity := cap(p.queue)
	queueUtilization := float64(currentDepth) / float64(queueCapacity)

	switch {
	case queueUtilization > 0.9:
		// Queue is >90% full - very short timeout
		timeout = 100 * time.Millisecond
	case queueUtilization > 0.7:
		// Queue is >70% full - reduced timeout
		timeout = 500 * time.Millisecond
	case queueUtilization > 0.5:
		// Queue is >50% full - standard timeout
		timeout = 1 * time.Second
	default:
		// Queue has capacity - longer timeout
		timeout = 5 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-p.ctx.Done():
		// Pool is shutting down
		p.queueDepth.Add(-1)
		stream.Close()
		return false
	case p.queue <- item:
		// Successfully queued
		return true
	case <-timer.C:
		// Timeout - apply backpressure
		p.queueDepth.Add(-1)
		stream.Close()
		return false
	}
}

func (p *WorkerPool) worker() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case item := <-p.queue:
			p.activeWorkers.Add(1)
			// Process the stream
			item.router.ServeStream(item.stream)
			p.processedJobs.Add(1)
			p.activeWorkers.Add(-1)

			item.stream = nil
			item.router = nil
			workItemPool.Put(item)
		}
	}
}

// Shutdown gracefully shuts down the worker pool
func (p *WorkerPool) Shutdown() {
	p.cancel()
	p.wg.Wait()
	close(p.queue)
}
