package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/rajibmitra/llm-token-gateway/internal/config"
)

// Cache provides hash-based prompt caching to avoid redundant API calls.
type Cache struct {
	backend    Backend
	enabled    bool
	defaultTTL time.Duration
}

// CacheEntry represents a cached response.
type CacheEntry struct {
	Key        string
	Response   []byte
	Provider   string
	Model      string
	TokensSaved int
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// Backend is the cache storage interface.
type Backend interface {
	Get(ctx context.Context, key string) (*CacheEntry, error)
	Set(ctx context.Context, entry *CacheEntry) error
	Delete(ctx context.Context, key string) error
	Flush(ctx context.Context) error
	Stats(ctx context.Context) CacheStats
	Close() error
}

// CacheStats holds cache performance metrics.
type CacheStats struct {
	Hits       int64
	Misses     int64
	Entries    int64
	SizeBytes  int64
	HitRate    float64
}

// New creates a new cache instance.
func New(cfg config.CacheConfig) (*Cache, error) {
	ttl, _ := time.ParseDuration(cfg.DefaultTTL)
	if ttl == 0 {
		ttl = 5 * time.Minute
	}

	c := &Cache{
		enabled:    cfg.Enabled,
		defaultTTL: ttl,
	}

	switch cfg.Backend {
	case "redis":
		backend, err := newRedisBackend(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("redis init: %w", err)
		}
		c.backend = backend
	case "memory", "":
		c.backend = newMemoryBackend(cfg.MaxSize)
	default:
		return nil, fmt.Errorf("unknown cache backend: %s", cfg.Backend)
	}

	return c, nil
}

// HashKey generates a cache key from the request content.
// Uses SHA-256 of the normalized message payload + model identifier.
func HashKey(model string, messages []byte) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte("|"))
	h.Write(messages)
	return hex.EncodeToString(h.Sum(nil))
}

// Get retrieves a cached response.
func (c *Cache) Get(ctx context.Context, key string) (*CacheEntry, error) {
	if !c.enabled {
		return nil, nil
	}
	return c.backend.Get(ctx, key)
}

// Set stores a response in the cache.
func (c *Cache) Set(ctx context.Context, key string, response []byte, provider, model string, tokensSaved int, ttl time.Duration) error {
	if !c.enabled {
		return nil
	}
	if ttl == 0 {
		ttl = c.defaultTTL
	}
	entry := &CacheEntry{
		Key:         key,
		Response:    response,
		Provider:    provider,
		Model:       model,
		TokensSaved: tokensSaved,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(ttl),
	}
	return c.backend.Set(ctx, entry)
}

// Flush clears all cache entries.
func (c *Cache) Flush(ctx context.Context) error {
	if c.backend == nil {
		return nil
	}
	return c.backend.Flush(ctx)
}

// Stats returns cache performance statistics.
func (c *Cache) Stats(ctx context.Context) CacheStats {
	if c.backend == nil {
		return CacheStats{}
	}
	return c.backend.Stats(ctx)
}

// Close shuts down the cache backend.
func (c *Cache) Close() error {
	if c.backend == nil {
		return nil
	}
	return c.backend.Close()
}

// --- In-Memory Backend ---

type memoryBackend struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	maxSize int // MB
	hits    int64
	misses  int64
}

func newMemoryBackend(maxSizeMB int) *memoryBackend {
	if maxSizeMB == 0 {
		maxSizeMB = 256
	}
	return &memoryBackend{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSizeMB,
	}
}

func (m *memoryBackend) Get(ctx context.Context, key string) (*CacheEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok {
		m.misses++
		return nil, nil
	}
	if time.Now().After(entry.ExpiresAt) {
		m.misses++
		delete(m.entries, key)
		return nil, nil
	}
	m.hits++
	return entry, nil
}

func (m *memoryBackend) Set(ctx context.Context, entry *CacheEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[entry.Key] = entry
	return nil
}

func (m *memoryBackend) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return nil
}

func (m *memoryBackend) Flush(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*CacheEntry)
	m.hits = 0
	m.misses = 0
	return nil
}

func (m *memoryBackend) Stats(ctx context.Context) CacheStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := m.hits + m.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(m.hits) / float64(total)
	}
	var size int64
	for _, e := range m.entries {
		size += int64(len(e.Response))
	}
	return CacheStats{
		Hits:      m.hits,
		Misses:    m.misses,
		Entries:   int64(len(m.entries)),
		SizeBytes: size,
		HitRate:   hitRate,
	}
}

func (m *memoryBackend) Close() error {
	return nil
}

// --- Redis Backend (stub — implement with go-redis) ---

type redisBackend struct {
	// client *redis.Client
}

func newRedisBackend(url string) (*redisBackend, error) {
	// TODO: Initialize redis client
	// client := redis.NewClient(&redis.Options{Addr: url})
	// if err := client.Ping(context.Background()).Err(); err != nil {
	//     return nil, err
	// }
	return &redisBackend{}, nil
}

func (r *redisBackend) Get(ctx context.Context, key string) (*CacheEntry, error) {
	// TODO: Implement with go-redis
	return nil, nil
}

func (r *redisBackend) Set(ctx context.Context, entry *CacheEntry) error {
	// TODO: Implement with go-redis
	return nil
}

func (r *redisBackend) Delete(ctx context.Context, key string) error {
	return nil
}

func (r *redisBackend) Flush(ctx context.Context) error {
	return nil
}

func (r *redisBackend) Stats(ctx context.Context) CacheStats {
	return CacheStats{}
}

func (r *redisBackend) Close() error {
	return nil
}
