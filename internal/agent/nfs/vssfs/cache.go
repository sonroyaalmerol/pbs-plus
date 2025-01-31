//go:build windows

package vssfs

import (
	"hash/fnv"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type cacheEntry struct {
	info       os.FileInfo
	err        error
	size       uint64
	lastAccess int64
}

type cacheShard struct {
	sync.Mutex
	entries map[string]*cacheEntry
	order   []string
}

type ConcurrentLRUCache struct {
	shards    []*cacheShard
	shardMask uint64
	totalSize atomic.Uint64
	maxSize   atomic.Uint64
}

func (c *ConcurrentLRUCache) Get(key string) (os.FileInfo, error) {
	shard := c.getShard(key)
	shard.Lock()
	defer shard.Unlock()

	entry, exists := shard.entries[key]
	if !exists {
		return nil, os.ErrNotExist
	}

	// Update access time and move to front
	atomic.StoreInt64(&entry.lastAccess, time.Now().UnixNano())
	c.moveToFront(shard, key)
	return entry.info, entry.err
}

func (c *ConcurrentLRUCache) Set(key string, info os.FileInfo, err error) {
	shard := c.getShard(key)
	shard.Lock()
	defer shard.Unlock()

	entrySize := uint64(len(key) + approxEntryOverhead)
	if existing, exists := shard.entries[key]; exists {
		c.totalSize.Add(^(existing.size - 1)) // Subtract old size
	}

	entry := &cacheEntry{
		info:       info,
		err:        err,
		size:       entrySize,
		lastAccess: time.Now().UnixNano(),
	}

	shard.entries[key] = entry
	c.totalSize.Add(entrySize)
	c.moveToFront(shard, key)

	// Trigger eviction if needed
	if c.totalSize.Load() > c.maxSize.Load() {
		go c.Evict(c.maxSize.Load() / 2) // Evict down to 50% of max size
	}
}

func (c *ConcurrentLRUCache) Evict(targetSize uint64) {
	for i := range c.shards {
		shard := c.shards[i]
		shard.Lock()

		for len(shard.order) > 0 && c.totalSize.Load() > targetSize {
			// Remove oldest entry (last in order)
			oldestKey := shard.order[len(shard.order)-1]
			if entry, exists := shard.entries[oldestKey]; exists {
				delete(shard.entries, oldestKey)
				c.totalSize.Add(^(entry.size - 1))
				shard.order = shard.order[:len(shard.order)-1]
			}
		}

		shard.Unlock()

		if c.totalSize.Load() <= targetSize {
			break
		}
	}
}

func (c *ConcurrentLRUCache) moveToFront(shard *cacheShard, key string) {
	// Remove existing occurrences
	for i, k := range shard.order {
		if k == key {
			shard.order = append(shard.order[:i], shard.order[i+1:]...)
			break
		}
	}
	// Add to front
	shard.order = append([]string{key}, shard.order...)
}

func (c *ConcurrentLRUCache) getShard(key string) *cacheShard {
	hasher := fnv.New64a()
	hasher.Write([]byte(key))
	return c.shards[hasher.Sum64()&c.shardMask]
}
