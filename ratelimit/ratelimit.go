package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// DefaultTTL is the default time-to-live for rate limiters
const DefaultTTL = 5 * time.Minute

// Method represents the rate limiting algorithm
type Method string

const (
	MethodTokenBucket Method = "token_bucket"
	MethodFixedWindow Method = "fixed_window"
)

// ResourceKind represents how to key the rate limit resource
type ResourceKind string

const (
	ResourceKindDomainPath ResourceKind = "domain_path"
	ResourceKindDomain     ResourceKind = "domain"
)

// Config represents a rate limit configuration from the header
type Config struct {
	Method   Method   `json:"method"`
	Rate     int      `json:"rate"`   // requests allowed
	Period   int      `json:"period"` // period in seconds
	TTL      int      `json:"ttl"`    // TTL in seconds (default 300 = 5min)
	Resource Resource `json:"resource"`
}

// GetTTL returns the TTL duration, defaulting to 5 minutes
func (c *Config) GetTTL() time.Duration {
	if c.TTL <= 0 {
		return DefaultTTL
	}
	return time.Duration(c.TTL) * time.Second
}

// Resource defines how to key the rate limit
type Resource struct {
	Kind ResourceKind `json:"kind"`
}

// Limiter interface for rate limiting
type Limiter interface {
	Allow() bool
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewTokenBucket creates a new token bucket limiter
func NewTokenBucket(rate int, periodSeconds int) *TokenBucket {
	maxTokens := float64(rate)
	refillRate := float64(rate) / float64(periodSeconds)
	return &TokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	// Refill tokens
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// FixedWindow implements a fixed window rate limiter
type FixedWindow struct {
	mu           sync.Mutex
	count        int
	maxRequests  int
	windowStart  time.Time
	windowPeriod time.Duration
}

// NewFixedWindow creates a new fixed window limiter
func NewFixedWindow(rate int, periodSeconds int) *FixedWindow {
	return &FixedWindow{
		count:        0,
		maxRequests:  rate,
		windowStart:  time.Now(),
		windowPeriod: time.Duration(periodSeconds) * time.Second,
	}
}

func (fw *FixedWindow) Allow() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	now := time.Now()

	// Check if we're in a new window
	if now.Sub(fw.windowStart) >= fw.windowPeriod {
		fw.windowStart = now
		fw.count = 0
	}

	if fw.count < fw.maxRequests {
		fw.count++
		return true
	}
	return false
}

// limiterEntry wraps a limiter with TTL tracking
type limiterEntry struct {
	limiter  Limiter
	lastUsed time.Time
	ttl      time.Duration
}

// Store holds all rate limiters, keyed by (egressIP, resourceKey)
type Store struct {
	mu       sync.RWMutex
	limiters map[string]*limiterEntry
	stopCh   chan struct{}
}

// Global store for process-wide rate limiting
var globalStore *Store

func init() {
	globalStore = NewStore()
}

// NewStore creates a new rate limiter store with cleanup goroutine
func NewStore() *Store {
	s := &Store{
		limiters: make(map[string]*limiterEntry),
		stopCh:   make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// cleanupLoop periodically removes expired limiters
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanup()
		case <-s.stopCh:
			return
		}
	}
}

// cleanup removes expired limiters
func (s *Store) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, entry := range s.limiters {
		if now.Sub(entry.lastUsed) > entry.ttl {
			delete(s.limiters, key)
		}
	}
}

// Stop stops the cleanup goroutine
func (s *Store) Stop() {
	close(s.stopCh)
}

// GetStore returns the global rate limiter store
func GetStore() *Store {
	return globalStore
}

// buildKey creates a unique key for the rate limiter
func buildKey(egressIP, resourceKey string, cfg *Config) string {
	// Include config in key so different configs get different limiters
	return fmt.Sprintf("%s|%s|%s|%d|%d", egressIP, resourceKey, cfg.Method, cfg.Rate, cfg.Period)
}

// GetOrCreate gets an existing limiter or creates a new one, resetting TTL on access
func (s *Store) GetOrCreate(egressIP, resourceKey string, cfg *Config) Limiter {
	key := buildKey(egressIP, resourceKey, cfg)
	now := time.Now()

	// Try read lock first
	s.mu.RLock()
	if entry, ok := s.limiters[key]; ok {
		s.mu.RUnlock()
		// Update lastUsed with write lock
		s.mu.Lock()
		entry.lastUsed = now
		s.mu.Unlock()
		return entry.limiter
	}
	s.mu.RUnlock()

	// Need to create - use write lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := s.limiters[key]; ok {
		entry.lastUsed = now
		return entry.limiter
	}

	var limiter Limiter
	switch cfg.Method {
	case MethodTokenBucket:
		limiter = NewTokenBucket(cfg.Rate, cfg.Period)
	case MethodFixedWindow:
		limiter = NewFixedWindow(cfg.Rate, cfg.Period)
	default:
		// Default to token bucket
		limiter = NewTokenBucket(cfg.Rate, cfg.Period)
	}

	s.limiters[key] = &limiterEntry{
		limiter:  limiter,
		lastUsed: now,
		ttl:      cfg.GetTTL(),
	}
	return limiter
}

// Len returns the number of active limiters (for testing/monitoring)
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.limiters)
}

// ExtractResourceKey extracts the resource key from host and path based on config
func ExtractResourceKey(host, path string, kind ResourceKind) string {
	switch kind {
	case ResourceKindDomainPath:
		return host + path
	case ResourceKindDomain:
		return host
	default:
		return host
	}
}
