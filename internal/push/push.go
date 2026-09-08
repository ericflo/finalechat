// Package push delivers Web Push notifications to the user's installed app.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/store"
)

// Notification is the payload the service worker renders.
type Notification struct {
	// Type is "message", "question", or "test".
	Type string `json:"type"`
	// Title and Body are the notification text.
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is opened when the notification is tapped.
	URL string `json:"url"`
	// Tag groups notifications so a thread replaces its previous one.
	Tag string `json:"tag"`
	// ThreadID, MessageID and QuestionID let the service worker refresh state.
	ThreadID   string `json:"thread_id,omitempty"`
	MessageID  string `json:"message_id,omitempty"`
	QuestionID string `json:"question_id,omitempty"`
	// Options are offered as notification actions where the platform allows.
	Options []string `json:"options,omitempty"`
	// Important asks for a more insistent presentation.
	Important bool `json:"important,omitempty"`
	// Badge is the app icon badge count after this notification; nil when
	// unknown, which leaves the badge alone.
	Badge *int `json:"badge,omitempty"`
}

// Sender pushes notifications.
type Sender struct {
	store      *store.Store
	log        *slog.Logger
	publicKey  string
	privateKey string
	subject    string
	client     *http.Client
	enabled    bool

	mu       sync.Mutex
	inflight sync.WaitGroup
}

// New creates a sender. When keys are empty the sender is disabled and Send
// becomes a no-op.
func New(st *store.Store, log *slog.Logger, publicKey, privateKey, subject string) *Sender {
	return &Sender{
		store:      st,
		log:        log,
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    subject,
		client:     &http.Client{Timeout: 15 * time.Second},
		enabled:    publicKey != "" && privateKey != "",
	}
}

// Enabled reports whether push is configured.
func (s *Sender) Enabled() bool { return s.enabled }

// PublicKey is the VAPID public key for the browser subscription call.
func (s *Sender) PublicKey() string { return s.publicKey }

// Send delivers a notification to every subscription of the user. Delivery
// happens in the background; the call returns immediately.
func (s *Sender) Send(userID uuid.UUID, n Notification) {
	if !s.enabled {
		return
	}
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		subs, err := s.store.ListPushSubscriptions(ctx, userID)
		if err != nil {
			s.log.Error("list push subscriptions", "err", err)
			return
		}
		if len(subs) == 0 {
			return
		}
		payload, err := encode(n)
		if err != nil {
			s.log.Error("marshal push payload", "err", err)
			return
		}
		urgency := webpush.UrgencyNormal
		ttl := 3600
		if n.Type == "question" || n.Important {
			urgency = webpush.UrgencyHigh
			ttl = 6 * 3600
		}
		var wg sync.WaitGroup
		for _, sub := range subs {
			wg.Add(1)
			go func(sub *store.PushSubscription) {
				defer wg.Done()
				s.deliver(ctx, sub, payload, urgency, ttl)
			}(sub)
		}
		wg.Wait()
	}()
}

func (s *Sender) deliver(ctx context.Context, sub *store.PushSubscription, payload []byte, urgency webpush.Urgency, ttl int) {
	// Push services have transient bad moments; a question notification is
	// worth a few retries before it is given up on.
	delay := time.Second
	for attempt := 1; ; attempt++ {
		outcome, retryAfter := s.attempt(ctx, sub, payload, urgency, ttl)
		if outcome != retry || attempt >= 3 || ctx.Err() != nil {
			if outcome == retry {
				s.log.Warn("push undelivered", "endpoint", shorten(sub.Endpoint), "attempts", attempt)
			}
			return
		}
		wait := delay
		if retryAfter > wait {
			wait = retryAfter
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay *= 3
	}
}

type outcome int

const (
	delivered outcome = iota
	rejected          // the subscription is bad; counted toward removal
	gone              // the service says it no longer exists; removed
	retry             // transient; try again
)

func (s *Sender) attempt(ctx context.Context, sub *store.PushSubscription, payload []byte, urgency webpush.Urgency, ttl int) (outcome, time.Duration) {
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256DH, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             ttl,
		Urgency:         urgency,
	})
	if err != nil {
		if errors.Is(err, webpush.ErrMaxPadExceeded) {
			// Deterministic: encrypting it again three times changes nothing.
			s.log.Error("push payload too large for one record", "endpoint", shorten(sub.Endpoint), "bytes", len(payload))
			return rejected, 0
		}
		s.log.Warn("push delivery failed", "endpoint", shorten(sub.Endpoint), "err", err)
		return retry, 0
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(http.MaxBytesReader(nil, resp.Body, 4096))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_ = s.store.RecordPushResult(ctx, sub.ID, true)
		return delivered, 0
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		s.log.Info("push subscription gone; removing", "endpoint", shorten(sub.Endpoint), "status", resp.StatusCode)
		_ = s.store.DeletePushSubscriptionByID(ctx, sub.ID)
		return gone, 0
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		s.log.Warn("push service busy", "endpoint", shorten(sub.Endpoint), "status", resp.StatusCode, "body", body.String())
		var after time.Duration
		if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 && v <= 60 {
			after = time.Duration(v) * time.Second
		}
		return retry, after
	default:
		s.log.Warn("push service rejected notification", "endpoint", shorten(sub.Endpoint), "status", resp.StatusCode, "body", body.String())
		_ = s.store.RecordPushResult(ctx, sub.ID, false)
		if sub.FailureCount+1 >= 20 {
			_ = s.store.DeletePushSubscriptionByID(ctx, sub.ID)
		}
		return rejected, 0
	}
}

// maxPayload keeps a notification inside the single 4096-byte record push
// services accept; the encryption adds about a hundred bytes of overhead.
const maxPayload = 3900

// encode marshals a notification, shedding the option actions and then
// shortening the body until it fits one record. A question with twenty long
// options still arrives; it is answered in the app rather than from the
// notification's buttons.
func encode(n Notification) ([]byte, error) {
	payload, err := json.Marshal(n)
	if err != nil || len(payload) <= maxPayload {
		return payload, err
	}
	n.Options = nil
	if payload, err = json.Marshal(n); err != nil || len(payload) <= maxPayload {
		return payload, err
	}
	n.Body = truncateRunes(n.Body, 120)
	n.Title = truncateRunes(n.Title, 80)
	return json.Marshal(n)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Wait blocks until in-flight deliveries finish, bounded by the context.
func (s *Sender) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// GenerateVAPIDKeys mints a new key pair (base64url, as used by browsers).
func GenerateVAPIDKeys() (privateKey, publicKey string, err error) {
	return webpush.GenerateVAPIDKeys()
}

func shorten(endpoint string) string {
	if len(endpoint) > 60 {
		return endpoint[:60] + "…"
	}
	return endpoint
}
