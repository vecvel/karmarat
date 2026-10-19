// Package proxy forwards authenticated requests to backend services.
package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
)

// Pool holds one reverse proxy per upstream, built lazily and reused.
//
// Reusing the transport is what keeps connections pooled; a new proxy per
// request would open a fresh TCP connection every time.
type Pool struct {
	mu      sync.RWMutex
	proxies map[string]*httputil.ReverseProxy
	logger  *slog.Logger
}

// NewPool builds an empty proxy pool.
func NewPool(logger *slog.Logger) *Pool {
	return &Pool{
		proxies: make(map[string]*httputil.ReverseProxy),
		logger:  logger,
	}
}

var transport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   50,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
}

// For returns the proxy for an upstream target.
func (p *Pool) For(target string) (*httputil.ReverseProxy, error) {
	p.mu.RLock()
	existing, ok := p.proxies[target]
	p.mu.RUnlock()
	if ok {
		return existing, nil
	}

	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	reverse := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(parsed)
			// Preserve the portal hostname so a service can tell which front door
			// a request came from without a separate header.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.logger.Error("upstream request failed",
				slog.String("target", target),
				slog.String("path", r.URL.Path),
				slog.Any("error", err),
			)
			httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusBadGateway, "upstream_unavailable",
				"Bad Gateway", "The service handling this request is temporarily unavailable."))
		},
		FlushInterval: -1, // stream server-sent events without buffering
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.proxies[target]; ok {
		return existing, nil
	}
	p.proxies[target] = reverse

	return reverse, nil
}

// StripAPIPrefix removes the gateway's `/api` prefix before forwarding.
//
// Services expose `/v1/...`; the public surface is `/api/v1/...`, so a service
// can be called directly in tests without mirroring the gateway's layout.
func StripAPIPrefix(r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/api")
		}
	}
}
