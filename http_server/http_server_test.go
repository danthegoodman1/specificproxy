package http_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danthegoodman1/specificproxy/config"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		AllowedInterfaces: []string{"lo"},
	}

	// Create the server handler directly
	mux := http.NewServeMux()
	hs := &HTTPServer{config: cfg}

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /ips", hs.handleListIPs)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "ok" {
		t.Errorf("expected 'ok', got %q", w.Body.String())
	}
}

func TestIPsEndpoint(t *testing.T) {
	cfg := &config.Config{
		AllowedInterfaces: []string{"lo"},
	}

	mux := http.NewServeMux()
	hs := &HTTPServer{config: cfg}

	mux.HandleFunc("GET /ips", hs.handleListIPs)

	req := httptest.NewRequest("GET", "/ips", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Should return valid JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("expected valid JSON response: %v", err)
	}

	if _, ok := resp["ips"]; !ok {
		t.Error("expected 'ips' field in response")
	}
}

func TestIPsEndpoint_NoConfig(t *testing.T) {
	mux := http.NewServeMux()
	hs := &HTTPServer{config: nil}

	mux.HandleFunc("GET /ips", hs.handleListIPs)

	req := httptest.NewRequest("GET", "/ips", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestProxy_ForbiddenIP(t *testing.T) {
	cfg := &config.Config{
		AllowedInterfaces: []string{"lo"},
	}

	hs := &HTTPServer{config: cfg}

	// Create a handler that wraps handleProxy
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a proxy request by setting the Host
		r.URL.Host = "example.com"
		hs.handleProxy(w, r)
	})

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("X-Egress-IP", "10.255.255.255") // Not on lo interface
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestProxy_InvalidIP(t *testing.T) {
	// Use nil config to skip IP validation and test IP parsing
	hs := &HTTPServer{config: nil}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Host = "example.com"
		hs.handleProxy(w, r)
	})

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("X-Egress-IP", "not-an-ip")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestProxy_InvalidRateLimitHeader(t *testing.T) {
	// Use nil config to skip IP validation
	hs := &HTTPServer{config: nil}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Host = "example.com"
		hs.handleProxy(w, r)
	})

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("X-Egress-IP", "127.0.0.1")
	req.Header.Set("X-Rate-Limit", "invalid json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestProxy_RateLimitExceeded(t *testing.T) {
	// Use nil config to skip IP validation
	hs := &HTTPServer{config: nil}

	// Use a unique domain for this test to avoid interference from other tests
	testDomain := "ratelimit-test-exceeded.example.com"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Host = testDomain
		r.Host = testDomain
		hs.handleProxy(w, r)
	})

	rateLimitConfig := `{"method":"token_bucket","rate":1,"period":60,"resource":{"kind":"domain"}}`

	// Make 2 requests - second should be rate limited
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "http://"+testDomain+"/", nil)
		req.Header.Set("X-Egress-IP", "127.0.0.1")
		req.Header.Set("X-Rate-Limit", rateLimitConfig)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if i == 1 {
			// Second request should be rate limited
			if w.Code != http.StatusTooManyRequests {
				t.Errorf("request %d: expected status 429, got %d", i+1, w.Code)
			}
			// Check for our custom header
			if w.Header().Get("X-RateLimit-Source") != "specificproxy" {
				t.Error("expected X-RateLimit-Source header")
			}
		}
	}
}

func TestRemoveHopByHopHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive, X-Custom")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("X-Custom", "value")
	h.Set("Content-Type", "application/json")

	removeHopByHopHeaders(h)

	if h.Get("Connection") != "" {
		t.Error("Connection header should be removed")
	}
	if h.Get("Keep-Alive") != "" {
		t.Error("Keep-Alive header should be removed")
	}
	if h.Get("X-Custom") != "" {
		t.Error("X-Custom header should be removed (listed in Connection)")
	}
	if h.Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be preserved")
	}
}
