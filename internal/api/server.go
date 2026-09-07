package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
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
	// Per-token write budgets (tokens per minute); sessions are not metered.
	messageLimiter  *rateLimiter
	questionLimiter *rateLimiter
	uploadLimiter   *rateLimiter
	activityLimiter *rateLimiter
	started         time.Time
	shutdown        chan struct{}
	draining        atomic.Bool
}

// Features lists the optional capabilities agents can feature-detect on
// GET /me and GET /auth/status.
func (s *Server) Features() []string {
	f := []string{"activity", "idempotency", "dismiss"}
	if s.push.Enabled() {
		f = append(f, "push")
	}
	if s.blobs != nil {
		f = append(f, "attachments")
	}
	return f
}

// New wires a server. blobs may be nil, which disables attachments.
func New(cfg config.Config, st *store.Store, b *bus.Bus, p *push.Sender, blobs blob.Store, log *slog.Logger) *Server {
	return &Server{
		cfg:             cfg,
		store:           st,
		bus:             b,
		push:            p,
		blobs:           blobs,
		log:             log,
		authLimiter:     newRateLimiter(20),
		messageLimiter:  newRateLimiter(120),
		questionLimiter: newRateLimiter(30),
		uploadLimiter:   newRateLimiter(30),
		activityLimiter: newRateLimiter(300),
		started:         time.Now().UTC().Truncate(time.Second),
		shutdown:        make(chan struct{}),
	}
}

// Shutdown tells long-lived connections to reconnect elsewhere and makes the
// readiness probe fail so the load balancer stops sending new traffic.
func (s *Server) Shutdown() {
	s.draining.Store(true)
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
		if s.draining.Load() {
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		if err := s.store.Pool().Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		// Without a live LISTEN connection this replica would serve stale
		// silence: no events, long-polls that only end on their deadline.
		if !s.bus.Healthy() {
			http.Error(w, "event listener down", http.StatusServiceUnavailable)
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
	authed.HandleFunc("POST /api/v1/threads/{thread}/activity", s.handleSetActivity)
	authed.HandleFunc("PUT /api/v1/threads/{thread}/activity", s.handleSetActivity)
	authed.HandleFunc("DELETE /api/v1/threads/{thread}/activity", s.handleClearActivity)
	authed.HandleFunc("GET /api/v1/threads/{thread}/messages", s.handleListMessages)
	authed.HandleFunc("POST /api/v1/threads/{thread}/messages", s.handleCreateMessage)
	authed.HandleFunc("GET /api/v1/threads/{thread}/questions", s.handleListThreadQuestions)
	authed.HandleFunc("POST /api/v1/threads/{thread}/questions", s.handleCreateQuestion)

	authed.HandleFunc("GET /api/v1/messages/{id}", s.handleGetMessage)
	authed.HandleFunc("DELETE /api/v1/messages/{id}", s.handleDeleteMessage)

	authed.HandleFunc("POST /api/v1/threads/{thread}/attachments", s.handleUpload)
	authed.HandleFunc("GET /api/v1/attachments/{id}", s.serveAttachment(false))
	authed.HandleFunc("HEAD /api/v1/attachments/{id}", s.serveAttachment(false))
	authed.HandleFunc("GET /api/v1/attachments/{id}/thumb", s.serveAttachment(true))

	authed.HandleFunc("GET /api/v1/questions", s.handleListQuestions)
	authed.HandleFunc("GET /api/v1/questions/{id}", s.handleGetQuestion)
	authed.HandleFunc("POST /api/v1/questions/{id}/answer", s.handleAnswerQuestion)
	authed.HandleFunc("POST /api/v1/questions/{id}/cancel", s.handleCancelQuestion)
	authed.HandleFunc("POST /api/v1/questions/{id}/dismiss", s.handleDismissQuestion)

	// Device registration is the app's business; a leaked agent token must
	// not be able to enroll a device that receives the user's notifications.
	authed.HandleFunc("GET /api/v1/push/subscriptions", s.sessionOnly(s.handleListSubscriptions))
	authed.HandleFunc("POST /api/v1/push/subscriptions", s.sessionOnly(s.handleSubscribe))
	authed.HandleFunc("DELETE /api/v1/push/subscriptions", s.sessionOnly(s.handleUnsubscribe))
	authed.HandleFunc("POST /api/v1/push/test", s.sessionOnly(s.handlePushTest))

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

// RunMaintenance runs the housekeeping loops until ctx ends. Each job has
// its own cadence and time budget so a slow one (object storage) cannot
// delay another (question expiry).
func (s *Server) RunMaintenance(ctx context.Context) {
	go s.every(ctx, 10*time.Second, 10*time.Second, func(ctx context.Context) {
		expired, err := s.store.ExpireQuestions(ctx)
		if err != nil {
			s.log.Error("expire questions", "err", err)
			return
		}
		for _, q := range expired {
			s.bus.Publish(ctx, bus.Event{Type: bus.QuestionExpired, UserID: q.UserID.String(), ThreadID: q.ThreadID.String(), QuestionID: q.ID.String()})
		}
	})
	go s.every(ctx, 5*time.Minute, 30*time.Second, func(ctx context.Context) {
		if _, err := s.store.DeleteExpiredSessions(ctx); err != nil {
			s.log.Error("prune sessions", "err", err)
		}
	})
	go s.every(ctx, 5*time.Minute, 4*time.Minute, s.pruneOrphanAttachments)
	<-ctx.Done()
}

// every runs fn on a ticker with a per-run timeout until ctx ends.
func (s *Server) every(ctx context.Context, interval, budget time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		runCtx, cancel := context.WithTimeout(ctx, budget)
		fn(runCtx)
		cancel()
	}
}
