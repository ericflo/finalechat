package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ericflo/finalechat/internal/push"
	"github.com/ericflo/finalechat/internal/store"
)

// GET /api/v1/push/vapid
func (s *Server) handleVAPID(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.push.Enabled(), "public_key": s.push.PublicKey()})
}

type subscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// POST /api/v1/push/subscriptions
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Subscription *subscriptionRequest `json:"subscription"`
		subscriptionRequest
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	sub := req.subscriptionRequest
	if req.Subscription != nil {
		sub = *req.Subscription
	}
	if !strings.HasPrefix(sub.Endpoint, "https://") || len(sub.Endpoint) > 2048 || sub.Keys.P256DH == "" || sub.Keys.Auth == "" {
		writeError(w, errValidation("A PushSubscription with endpoint and keys is required."))
		return
	}
	if !knownPushService(sub.Endpoint) {
		// The server POSTs to this URL on every notification; only the
		// browsers' push services may be on the receiving end.
		writeError(w, errValidation("The push endpoint is not a known browser push service."))
		return
	}
	if !s.push.Enabled() {
		writeError(w, &apiError{Status: http.StatusServiceUnavailable, Code: "push_disabled", Message: "Push notifications are not configured on this server."})
		return
	}
	stored, err := s.store.UpsertPushSubscription(r.Context(), p.user.ID, sub.Endpoint, sub.Keys.P256DH, sub.Keys.Auth, r.UserAgent())
	if err == store.ErrConflict {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "conflict", Message: "That push endpoint is registered to another account."})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"subscription": stored})
}

// pushServiceHosts are the browsers' push services; endpoints elsewhere are
// refused.
var pushServiceHosts = []string{
	".push.apple.com",            // Safari and iOS
	"fcm.googleapis.com",         // Chrome, Edge, Brave, Android
	".push.services.mozilla.com", // Firefox
	".notify.windows.com",        // Edge (WNS)
	".push.samsungosp.com",       // Samsung Internet
	".pushsvc.duckduckgo.com",    // DuckDuckGo
	".webpush.duckduckgo.com",    //
}

func knownPushService(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushServiceHosts {
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) || host == h[1:] {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// DELETE /api/v1/push/subscriptions
func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeletePushSubscription(r.Context(), p.user.ID, req.Endpoint); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/v1/push/subscriptions
func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	subs, err := s.store.ListPushSubscriptions(r.Context(), p.user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// POST /api/v1/push/test
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !s.push.Enabled() {
		writeError(w, &apiError{Status: http.StatusServiceUnavailable, Code: "push_disabled", Message: "Push notifications are not configured on this server."})
		return
	}
	subs, err := s.store.ListPushSubscriptions(r.Context(), p.user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(subs) == 0 {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "no_subscriptions", Message: "This account has no devices subscribed to notifications yet."})
		return
	}
	s.push.Send(p.user.ID, push.Notification{
		Type:  "test",
		Title: "Finalechat is connected",
		Body:  "This is what a notification from an agent looks like.",
		URL:   s.cfg.BaseURL + "/",
		Tag:   "test",
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "devices": len(subs)})
}
