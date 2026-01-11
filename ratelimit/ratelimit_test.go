package ratelimit

import (
	"testing"
	"time"
)

func TestTokenBucket_Allow(t *testing.T) {
	// 5 requests per second
	tb := NewTokenBucket(5, 1)

	// Should allow first 5 requests
	for i := 0; i < 5; i++ {
		if !tb.Allow() {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 6th request should be denied
	if tb.Allow() {
		t.Error("6th request should be denied")
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	// 2 requests per second
	tb := NewTokenBucket(2, 1)

	// Use all tokens
	tb.Allow()
	tb.Allow()

	if tb.Allow() {
		t.Error("should be denied after exhausting tokens")
	}

	// Wait for refill
	time.Sleep(600 * time.Millisecond)

	// Should have at least 1 token now
	if !tb.Allow() {
		t.Error("should be allowed after partial refill")
	}
}

func TestFixedWindow_Allow(t *testing.T) {
	// 3 requests per 1 second window
	fw := NewFixedWindow(3, 1)

	// Should allow first 3 requests
	for i := 0; i < 3; i++ {
		if !fw.Allow() {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	if fw.Allow() {
		t.Error("4th request should be denied")
	}
}

func TestFixedWindow_NewWindow(t *testing.T) {
	// 2 requests per 100ms window
	fw := NewFixedWindow(2, 1)
	fw.windowPeriod = 100 * time.Millisecond // Override for faster test

	fw.Allow()
	fw.Allow()

	if fw.Allow() {
		t.Error("should be denied in same window")
	}

	// Wait for new window
	time.Sleep(150 * time.Millisecond)

	if !fw.Allow() {
		t.Error("should be allowed in new window")
	}
}

func TestStore_GetOrCreate(t *testing.T) {
	store := NewStore()
	defer store.Stop()

	cfg := &Config{
		Method: MethodTokenBucket,
		Rate:   10,
		Period: 60,
		Resource: Resource{
			Kind: ResourceKindDomain,
		},
	}

	// First call creates limiter
	limiter1 := store.GetOrCreate("192.168.1.1", "example.com", cfg)
	if limiter1 == nil {
		t.Fatal("expected limiter to be created")
	}

	// Second call returns same limiter
	limiter2 := store.GetOrCreate("192.168.1.1", "example.com", cfg)
	if limiter1 != limiter2 {
		t.Error("expected same limiter instance")
	}

	// Different egress IP gets different limiter
	limiter3 := store.GetOrCreate("192.168.1.2", "example.com", cfg)
	if limiter1 == limiter3 {
		t.Error("expected different limiter for different egress IP")
	}

	// Different resource gets different limiter
	limiter4 := store.GetOrCreate("192.168.1.1", "other.com", cfg)
	if limiter1 == limiter4 {
		t.Error("expected different limiter for different resource")
	}

	if store.Len() != 3 {
		t.Errorf("expected 3 limiters, got %d", store.Len())
	}
}

func TestStore_TTLCleanup(t *testing.T) {
	store := NewStore()
	defer store.Stop()

	cfg := &Config{
		Method: MethodTokenBucket,
		Rate:   10,
		Period: 60,
		TTL:    1, // 1 second TTL
		Resource: Resource{
			Kind: ResourceKindDomain,
		},
	}

	store.GetOrCreate("192.168.1.1", "example.com", cfg)

	if store.Len() != 1 {
		t.Fatalf("expected 1 limiter, got %d", store.Len())
	}

	// Wait for TTL to expire
	time.Sleep(1200 * time.Millisecond)

	// Trigger cleanup manually
	store.cleanup()

	if store.Len() != 0 {
		t.Errorf("expected 0 limiters after cleanup, got %d", store.Len())
	}
}

func TestStore_TTLReset(t *testing.T) {
	store := NewStore()
	defer store.Stop()

	cfg := &Config{
		Method: MethodTokenBucket,
		Rate:   100,
		Period: 60,
		TTL:    1, // 1 second TTL
		Resource: Resource{
			Kind: ResourceKindDomain,
		},
	}

	store.GetOrCreate("192.168.1.1", "example.com", cfg)

	// Use it again after 500ms (before TTL expires)
	time.Sleep(500 * time.Millisecond)
	store.GetOrCreate("192.168.1.1", "example.com", cfg)

	// Wait another 700ms (total 1200ms from start, but only 700ms from last use)
	time.Sleep(700 * time.Millisecond)

	store.cleanup()

	// Should still exist because TTL was reset
	if store.Len() != 1 {
		t.Errorf("expected 1 limiter (TTL should have reset), got %d", store.Len())
	}
}

func TestExtractResourceKey(t *testing.T) {
	tests := []struct {
		host     string
		path     string
		kind     ResourceKind
		expected string
	}{
		{"example.com", "/api/v1", ResourceKindDomain, "example.com"},
		{"example.com", "/api/v1", ResourceKindDomainPath, "example.com/api/v1"},
		{"example.com:443", "/", ResourceKindDomain, "example.com:443"},
		{"example.com", "", ResourceKindDomainPath, "example.com"},
	}

	for _, tt := range tests {
		result := ExtractResourceKey(tt.host, tt.path, tt.kind)
		if result != tt.expected {
			t.Errorf("ExtractResourceKey(%q, %q, %q) = %q, want %q",
				tt.host, tt.path, tt.kind, result, tt.expected)
		}
	}
}

func TestConfig_GetTTL(t *testing.T) {
	// Default TTL
	cfg := &Config{}
	if cfg.GetTTL() != DefaultTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultTTL, cfg.GetTTL())
	}

	// Custom TTL
	cfg = &Config{TTL: 120}
	if cfg.GetTTL() != 120*time.Second {
		t.Errorf("expected 120s, got %v", cfg.GetTTL())
	}

	// Zero TTL should use default
	cfg = &Config{TTL: 0}
	if cfg.GetTTL() != DefaultTTL {
		t.Errorf("expected default TTL for zero value, got %v", cfg.GetTTL())
	}
}
