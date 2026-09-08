package api

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// limitWrite applies a per-token write budget. Sessions (the app) are not
// metered: an agent loop is the failure mode, not a thumb.
func (s *Server) limitWrite(p *principal, rl *rateLimiter) error {
	if p == nil || p.token == nil {
		return nil
	}
	if !rl.allow(p.token.ID.String()) {
		return &apiError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "This token is posting too fast. Slow down and retry.", RetryAfter: 5}
	}
	return nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// requestIDKey carries the request id through the context.
const requestIDKey ctxKey = 2

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		// A request id ties a log line to an error body the founder pastes back.
		rid := r.Header.Get("X-Request-Id")
		if rid == "" || len(rid) > 64 {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-Id", rid)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, rid))
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.log.Error("panic serving request", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
				if sw.status == 0 {
					writeError(sw, errInternal)
				}
			}
			// Health probes are noisy and uninteresting.
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				return
			}
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelError
			} else if r.URL.Path == "/api/v1/events" || strings.HasPrefix(r.URL.Path, "/assets/") {
				level = slog.LevelDebug
			}
			attrs := []any{
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"bytes", sw.bytes, "duration_ms", time.Since(start).Milliseconds(),
				"ip", s.clientIP(r), "ua", truncateUA(r.UserAgent()), "request_id", rid,
			}
			if p := principalFrom(r.Context()); p != nil {
				attrs = append(attrs, "user_id", p.user.ID.String())
				if p.token != nil {
					attrs = append(attrs, "token", p.token.Prefix)
				}
			}
			s.log.Log(r.Context(), level, "request", attrs...)
		}()
		next.ServeHTTP(sw, r)
	})
}

var errInternal = &apiError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Something went wrong on our side."}

func truncateUA(ua string) string {
	if len(ua) > 120 {
		return ua[:120]
	}
	return ua
}

// appCSP is the policy for the app shell and every other document the
// server renders itself. The app is a self-contained bundle: no third-party
// scripts, styles or connections. Inline style attributes come from React;
// images may come from markdown posted by agents.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// API responses are JSON, or attachments that set their own sandbox
		// policy; everything else, including the app shell served at /api/
		// to a browser, gets the app's policy.
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			h.Set("Content-Security-Policy", appCSP)
		}
		if s.cfg.SecureCookies {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalRedirect sends browser navigations on alias hosts to the canonical
// host so the PWA is always installed from one origin.
func (s *Server) canonicalRedirect(next http.Handler) http.Handler {
	if len(s.cfg.RedirectHosts) == 0 {
		return next
	}
	redirect := map[string]bool{}
	for _, h := range s.cfg.RedirectHosts {
		redirect[h] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if redirect[host] && !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			target := "https://" + s.cfg.CanonicalHost + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address of the peer that reached the proxy. Each trusted
// proxy appends one X-Forwarded-For entry, so the client is the Nth entry
// from the right for N trusted hops; anything further left was supplied by
// the client and cannot be trusted (it is the key of the login rate
// limiter). A header with fewer entries than hops did not come through the
// proxies, so the socket peer is used instead.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy && s.cfg.TrustedProxyHops > 0 {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) >= s.cfg.TrustedProxyHops {
				if ip := strings.TrimSpace(parts[len(parts)-s.cfg.TrustedProxyHops]); ip != "" {
					return ip
				}
			}
		} else if rip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); rip != "" && s.cfg.TrustedProxyHops == 1 {
			return rip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter is a small per-key token bucket for authentication endpoints.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64 // tokens per second
	capacity float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	rl := &rateLimiter{buckets: map[string]*bucket{}, rate: float64(perMinute) / 60, capacity: float64(perMinute)}
	go rl.sweep()
	return rl
}

// maxBuckets bounds the limiter's memory against attacker-chosen keys.
const maxBuckets = 10000

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		if len(rl.buckets) >= maxBuckets {
			// Evict the stalest entry rather than grow without bound.
			var oldest string
			var oldestAt time.Time
			for k, v := range rl.buckets {
				if oldest == "" || v.last.Before(oldestAt) {
					oldest, oldestAt = k, v.last
				}
			}
			delete(rl.buckets, oldest)
		}
		b = &bucket{tokens: rl.capacity, last: now}
		rl.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *rateLimiter) sweep() {
	for range time.Tick(5 * time.Minute) {
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if time.Since(b.last) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// gzipWriter compresses compressible responses for clients that accept it.
type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	compress    bool
}

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

func (g *gzipWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	ct := h.Get("Content-Type")
	if code >= 200 && code < 300 && h.Get("Content-Encoding") == "" && isCompressible(ct) && ct != "text/event-stream" {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
		g.compress = true
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.compress {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	if g.compress {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) close() {
	if g.compress {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
	}
}

func isCompressible(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "svg") || strings.Contains(ct, "manifest") || strings.Contains(ct, "x-sh")
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.URL.Path == "/api/v1/events" {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}
