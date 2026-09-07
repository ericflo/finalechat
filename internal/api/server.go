package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ericflo/finalechat/internal/blob"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/config"
	"github.com/ericflo/finalechat/internal/push"
	"github.com/ericflo/finalechat/internal/store"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	cfg         config.Config
	store       *store.Store
	bus         *bus.Bus
	push        *push.Sender
	blobs       blob.Store
	log         *slog.Logger
	authLimiter *rateLimiter
	started     time.Time
	shutdown    chan struct{}
}

// New wires a server. blobs may be nil, which disables attachments.
func New(cfg config.Config, st *store.Store, b *bus.Bus, p *push.Sender, blobs blob.Store, log *slog.Logger) *Server {
	return &Server{
		cfg:         cfg,
		store:       st,
		bus:         b,
		push:        p,
		blobs:       blobs,
		log:         log,
		authLimiter: newRateLimiter(20),
		started:     time.Now().UTC().Truncate(time.Second),
		shutdown:    make(chan struct{}),
	}
}

// Shutdown tells long-lived connections to reconnect elsewhere.
func (s *Server) Shutdown() {
	select {
	case <-s.shutdown:
	default:
		close(s.shutdown)
	}
}

// Handler builds the HTTP routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		if err := s.store.Pool().Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})

	// Agent-facing documents and tooling.
	mux.HandleFunc("GET /AGENTS.md", s.document("AGENTS.md", "text/markdown; charset=utf-8", false))
	mux.HandleFunc("GET /agents.md", s.document("AGENTS.md", "text/markdown; charset=utf-8", false))
	mux.HandleFunc("GET /llms.txt", s.document("AGENTS.md", "text/plain; charset=utf-8", false))
	mux.HandleFunc("GET /api", s.apiIndex)
	mux.HandleFunc("GET /api/{$}", s.apiIndex)
	mux.HandleFunc("GET /api/openapi.json", s.document("docs/openapi.json", "application/json; charset=utf-8", false))
	mux.HandleFunc("GET /openapi.json", s.document("docs/openapi.json", "application/json; charset=utf-8", false))
	mux.HandleFunc("GET /install.sh", s.document("cli/install.sh", "text/x-sh; charset=utf-8", false))
	mux.HandleFunc("GET /cli/finalechat", s.document("cli/finalechat", "text/x-python; charset=utf-8", true))
	mux.HandleFunc("GET /skill/SKILL.md", s.document("skill/finalechat/SKILL.md", "text/markdown; charset=utf-8", false))
	mux.HandleFunc("GET /skill/finalechat/SKILL.md", s.document("skill/finalechat/SKILL.md", "text/markdown; charset=utf-8", false))

	// Unauthenticated API.
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/push/vapid", s.handleVAPID)

	// Authenticated API.
	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/v1/me", s.handleMe)
	authed.HandleFunc("PATCH /api/v1/me", s.handleUpdateMe)
	authed.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	authed.HandleFunc("PATCH /api/v1/settings", s.handleUpdateSettings)
	authed.HandleFunc("GET /api/v1/counts", s.handleCounts)
	authed.HandleFunc("GET /api/v1/events", s.handleEvents)

	authed.HandleFunc("GET /api/v1/tokens", s.handleListTokens)
	authed.HandleFunc("POST /api/v1/tokens", s.handleCreateToken)
	authed.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleRevokeToken)

	authed.HandleFunc("GET /api/v1/threads", s.handleListThreads)
	authed.HandleFunc("POST /api/v1/threads", s.handleCreateThread)
	authed.HandleFunc("GET /api/v1/threads/{thread}", s.handleGetThread)
	authed.HandleFunc("PATCH /api/v1/threads/{thread}", s.handleUpdateThread)
	authed.HandleFunc("DELETE /api/v1/threads/{thread}", s.handleDeleteThread)
	authed.HandleFunc("POST /api/v1/threads/{thread}/read", s.handleMarkRead)
	authed.HandleFunc("GET /api/v1/threads/{thread}/messages", s.handleListMessages)
	authed.HandleFunc("POST /api/v1/threads/{thread}/messages", s.handleCreateMessage)
	authed.HandleFunc("GET /api/v1/threads/{thread}/questions", s.handleListThreadQuestions)
	authed.HandleFunc("POST /api/v1/threads/{thread}/questions", s.handleCreateQuestion)

	authed.HandleFunc("GET /api/v1/messages/{id}", s.handleGetMessage)

	authed.HandleFunc("POST /api/v1/threads/{thread}/attachments", s.handleUpload)
	authed.HandleFunc("GET /api/v1/attachments/{id}", s.serveAttachment(false))
	authed.HandleFunc("HEAD /api/v1/attachments/{id}", s.serveAttachment(false))
	authed.HandleFunc("GET /api/v1/attachments/{id}/thumb", s.serveAttachment(true))

	authed.HandleFunc("GET /api/v1/questions", s.handleListQuestions)
	authed.HandleFunc("GET /api/v1/questions/{id}", s.handleGetQuestion)
	authed.HandleFunc("POST /api/v1/questions/{id}/answer", s.handleAnswerQuestion)
	authed.HandleFunc("POST /api/v1/questions/{id}/cancel", s.handleCancelQuestion)

	authed.HandleFunc("GET /api/v1/push/subscriptions", s.handleListSubscriptions)
	authed.HandleFunc("POST /api/v1/push/subscriptions", s.handleSubscribe)
	authed.HandleFunc("DELETE /api/v1/push/subscriptions", s.handleUnsubscribe)
	authed.HandleFunc("POST /api/v1/push/test", s.handlePushTest)

	authed.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Unknown API route. See " + s.cfg.BaseURL + "/api/ for the reference."})
	})
	mux.Handle("/api/v1/", s.requireAuth(authed))

	// Anything else under /api/ is unknown.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Unknown API route. See " + s.cfg.BaseURL + "/api/ for the reference."})
	})

	// The app.
	mux.HandleFunc("/", s.spa)

	var h http.Handler = mux
	h = s.authenticate(h)
	h = gzipMiddleware(h)
	h = s.securityHeaders(h)
	h = s.canonicalRedirect(h)
	h = s.logging(h)
	return h
}

// RunMaintenance expires overdue questions and prunes sessions until ctx ends.
func (s *Server) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		expired, err := s.store.ExpireQuestions(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("expire questions", "err", err)
			}
			continue
		}
		for _, q := range expired {
			s.bus.Publish(ctx, bus.Event{Type: bus.QuestionExpired, UserID: q.UserID.String(), ThreadID: q.ThreadID.String(), QuestionID: q.ID.String()})
		}
		if _, err := s.store.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("prune sessions", "err", err)
		}
		s.pruneOrphanAttachments(ctx)
	}
}
