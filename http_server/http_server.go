package http_server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danthegoodman1/specificproxy/config"
	"github.com/danthegoodman1/specificproxy/gologger"
	"github.com/danthegoodman1/specificproxy/ratelimit"
)

var logger = gologger.NewLogger()

type HTTPServer struct {
	server *http.Server
	config *config.Config
}

// StartHTTPServer starts the HTTP server on the given address
// proxyAddr is currently unused but kept for API compatibility
func StartHTTPServer(addr, proxyAddr string, cfg *config.Config) *http.Server {
	mux := http.NewServeMux()

	hs := &HTTPServer{
		config: cfg,
	}

	// Health check endpoint
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// List available IPs endpoint
	mux.HandleFunc("GET /ips", hs.handleListIPs)

	// The proxy handles both CONNECT (for HTTPS) and regular requests
	// We use a custom handler that wraps the mux
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a proxy request (has full URL or CONNECT method)
		if r.Method == http.MethodConnect || r.URL.Host != "" {
			hs.handleProxy(w, r)
			return
		}
		// Otherwise, route to regular endpoints
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0, // No timeout for proxy connections
		IdleTimeout:  120 * time.Second,
	}

	hs.server = server

	go func() {
		logger.Info().Str("addr", addr).Msg("starting HTTP server")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	return server
}

// handleListIPs returns the list of available egress IP addresses
func (hs *HTTPServer) handleListIPs(w http.ResponseWriter, r *http.Request) {
	if hs.config == nil {
		http.Error(w, "server not configured", http.StatusInternalServerError)
		return
	}

	ips, err := hs.config.GetAvailableIPs()
	if err != nil {
		logger.Error().Err(err).Msg("failed to get available IPs")
		http.Error(w, "failed to get available IPs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ips": ips,
	})
}

// handleProxy handles HTTP CONNECT requests and regular proxy requests
// The egress IP is specified via the X-Egress-IP header, or a random one is chosen
func (hs *HTTPServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	egressIP := r.Header.Get("X-Egress-IP")

	// If no egress IP specified, pick a random one from available IPs
	if egressIP == "" {
		if hs.config == nil {
			http.Error(w, "server not configured", http.StatusInternalServerError)
			return
		}
		ips, err := hs.config.GetAvailableIPs()
		if err != nil || len(ips) == 0 {
			http.Error(w, "no available egress IPs", http.StatusInternalServerError)
			return
		}
		egressIP = ips[rand.Intn(len(ips))].IP
		logger.Debug().Str("egress_ip", egressIP).Msg("randomly selected egress IP")
	} else {
		// Validate the specified egress IP is allowed
		if hs.config != nil && !hs.config.IsIPAllowed(egressIP) {
			http.Error(w, "specified egress IP is not allowed", http.StatusForbidden)
			return
		}
	}

	// Parse the egress IP
	localIP := net.ParseIP(egressIP)
	if localIP == nil {
		http.Error(w, "invalid egress IP format", http.StatusBadRequest)
		return
	}

	// Check rate limiting if configured
	if rateLimitHeader := r.Header.Get("X-Rate-Limit"); rateLimitHeader != "" {
		var rlConfig ratelimit.Config
		if err := json.Unmarshal([]byte(rateLimitHeader), &rlConfig); err != nil {
			http.Error(w, "invalid X-Rate-Limit header format", http.StatusBadRequest)
			return
		}

		// Extract host and path for resource keying
		host := r.Host
		path := r.URL.Path
		if r.Method == http.MethodConnect {
			// For CONNECT, host is in r.Host, path is empty
			path = ""
		}

		resourceKey := ratelimit.ExtractResourceKey(host, path, rlConfig.Resource.Kind)
		limiter := ratelimit.GetStore().GetOrCreate(egressIP, resourceKey, &rlConfig)

		if !limiter.Allow() {
			w.Header().Set("X-RateLimit-Source", "specificproxy")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	if r.Method == http.MethodConnect {
		hs.handleConnect(w, r, localIP)
	} else {
		hs.handleHTTPProxy(w, r, localIP)
	}
}

// handleConnect handles HTTPS proxy via CONNECT method
func (hs *HTTPServer) handleConnect(w http.ResponseWriter, r *http.Request, localIP net.IP) {
	logger.Debug().Str("host", r.Host).Str("egress_ip", localIP.String()).Msg("handling CONNECT request")

	// Create a dialer that binds to the specified local IP
	dialer := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: localIP},
		Timeout:   10 * time.Second,
	}

	// Connect to the target
	targetConn, err := dialer.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		logger.Error().Err(err).Str("host", r.Host).Msg("failed to connect to target")
		http.Error(w, fmt.Sprintf("failed to connect to target: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		logger.Error().Err(err).Msg("failed to hijack connection")
		http.Error(w, fmt.Sprintf("failed to hijack connection: %v", err), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection Established
	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if err != nil {
		logger.Error().Err(err).Msg("failed to send connection established response")
		return
	}

	// Bidirectional copy
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		io.Copy(targetConn, clientConn)
		cancel()
	}()

	go func() {
		io.Copy(clientConn, targetConn)
		cancel()
	}()

	<-ctx.Done()
}

// handleHTTPProxy handles regular HTTP proxy requests
func (hs *HTTPServer) handleHTTPProxy(w http.ResponseWriter, r *http.Request, localIP net.IP) {
	logger.Debug().Str("url", r.URL.String()).Str("egress_ip", localIP.String()).Msg("handling HTTP proxy request")

	// Create a custom transport with the specified local IP
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{
				LocalAddr: &net.TCPAddr{IP: localIP},
				Timeout:   10 * time.Second,
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}

	// Create the outgoing request
	outReq := r.Clone(r.Context())
	outReq.RequestURI = "" // Must be empty for client requests

	// Remove proxy-specific headers to make proxy invisible
	outReq.Header.Del("X-Egress-IP")
	outReq.Header.Del("X-Rate-Limit")
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	// Make the request
	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		logger.Error().Err(err).Str("url", r.URL.String()).Msg("failed to make proxy request")
		http.Error(w, fmt.Sprintf("proxy request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Remove hop-by-hop headers
	removeHopByHopHeaders(w.Header())

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// Hop-by-hop headers that should not be forwarded
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(h http.Header) {
	// Remove headers listed in Connection header
	if c := h.Get("Connection"); c != "" {
		for _, f := range strings.Split(c, ",") {
			h.Del(strings.TrimSpace(f))
		}
	}
	// Remove standard hop-by-hop headers
	for _, hdr := range hopByHopHeaders {
		h.Del(hdr)
	}
}
