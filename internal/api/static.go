package api

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	finalechat "github.com/ericflo/finalechat"
	"github.com/ericflo/finalechat/internal/webassets"
)

// productionBaseURL is the origin written into the committed documents; it is
// rewritten to the configured base URL so local and preview deployments hand
// out working snippets.
const productionBaseURL = "https://www.finalechat.com"

// document serves an embedded repository file with the base URL rewritten.
func (s *Server) document(name, contentType string, download bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := fs.ReadFile(finalechat.Assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body := raw
		if s.cfg.BaseURL != productionBaseURL {
			body = bytes.ReplaceAll(raw, []byte(productionBaseURL), []byte(s.cfg.BaseURL))
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if download {
			w.Header().Set("Content-Disposition", "inline; filename=\""+path.Base(name)+"\"")
		}
		http.ServeContent(w, r, path.Base(name), s.started, bytes.NewReader(body))
	}
}

// apiIndex serves the API reference: markdown for agents and tools, the app's
// documentation page for browsers.
func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" {
		http.Redirect(w, r, "/api/", http.StatusMovedPermanently)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "md" && format != "markdown" && wantsHTML(r) && webassets.Built() {
		s.serveIndex(w, r)
		return
	}
	s.document("docs/API.md", "text/markdown; charset=utf-8", false)(w, r)
}

func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	// Browsers list text/html first; curl and HTTP clients send */*.
	for _, part := range strings.Split(accept, ",") {
		mt := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch mt {
		case "text/html", "application/xhtml+xml":
			return true
		case "text/markdown", "text/plain", "application/json", "*/*":
			return false
		}
	}
	return false
}

// serveIndex serves the app shell.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(webassets.FS(), "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("<!doctype html><title>Finalechat</title><p>The web app has not been built. Run <code>make web</code>.</p><p>The API is available at <a href=\"/api/\">/api/</a>.</p>"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", s.started, bytes.NewReader(raw))
}

// spa serves built assets with appropriate caching and falls back to the app
// shell for client-side routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, &apiError{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "Method not allowed."})
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" || p == "index.html" {
		s.serveIndex(w, r)
		return
	}
	if strings.HasPrefix(path.Base(p), ".") {
		http.NotFound(w, r)
		return
	}
	f, err := webassets.FS().Open(p)
	if err != nil {
		// Client-side route (or missing file); the app decides.
		if path.Ext(p) == "" || strings.HasPrefix(p, "t/") {
			s.serveIndex(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		s.serveIndex(w, r)
		return
	}
	data, err := fs.ReadFile(webassets.FS(), p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(p, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case p == "sw.js":
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
	case p == "manifest.webmanifest":
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/manifest+json")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	http.ServeContent(w, r, path.Base(p), s.started, bytes.NewReader(data))
}

var _ = time.Now
