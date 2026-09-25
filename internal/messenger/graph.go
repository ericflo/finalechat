// Package messenger speaks Facebook Messenger's side of the Messenger
// connector: the Send API, webhook payloads and signatures, and the text
// shaping that fits an agent's markdown into Messenger's plain chat bubbles.
// It knows nothing about Finalechat's threads; internal/api owns that.
package messenger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultGraphURL is the Graph API version the connector is written against.
const DefaultGraphURL = "https://graph.facebook.com/v25.0"

// Client sends messages as a Facebook Page.
type Client struct {
	// GraphURL is the Graph API base, DefaultGraphURL outside tests.
	GraphURL  string
	PageID    string
	PageToken string
	HTTP      *http.Client
}

// NewClient returns a client for the page.
func NewClient(graphURL, pageID, pageToken string) *Client {
	if graphURL == "" {
		graphURL = DefaultGraphURL
	}
	return &Client{GraphURL: strings.TrimRight(graphURL, "/"), PageID: pageID, PageToken: pageToken, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// QuickReply is a button under a message; tapping it sends Title back as a
// message carrying Payload.
type QuickReply struct {
	Title   string `json:"title"`
	Payload string `json:"payload"`
}

// Limits Messenger places on what the page sends.
const (
	MaxTextRunes       = 2000
	MaxQuickReplies    = 13
	MaxQuickReplyTitle = 20
	MaxPayloadBytes    = 1000
)

// Error is a Graph API refusal.
type Error struct {
	Status  int
	Code    int    `json:"code"`
	Subcode int    `json:"error_subcode"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("messenger: %s (code %d/%d, HTTP %d)", e.Message, e.Code, e.Subcode, e.Status)
}

// WindowClosed reports whether the page may not message the person because
// their last message is more than 24 hours old.
func (e *Error) WindowClosed() bool {
	return e.Code == 10 && e.Subcode == 2018278 || e.Subcode == 2018109 || e.Code == 551
}

// RateLimited reports a throttling refusal worth retrying later.
func (e *Error) RateLimited() bool {
	return e.Code == 4 || e.Code == 17 || e.Code == 32 || e.Code == 613 || e.Status == http.StatusTooManyRequests
}

// IsWindowClosed reports whether err is a 24-hour-window refusal.
func IsWindowClosed(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.WindowClosed()
}

// SendText sends one text message (at most MaxTextRunes) with optional
// quick replies, replying to replyTo when set, and returns the message id.
func (c *Client) SendText(ctx context.Context, psid, text string, quick []QuickReply, replyTo string) (string, error) {
	msg := map[string]any{"text": text}
	if len(quick) > 0 {
		qr := make([]map[string]string, 0, len(quick))
		for _, q := range quick {
			qr = append(qr, map[string]string{"content_type": "text", "title": Clip(q.Title, MaxQuickReplyTitle), "payload": q.Payload})
		}
		msg["quick_replies"] = qr
	}
	if replyTo != "" {
		msg["reply_to"] = map[string]string{"mid": replyTo}
	}
	var out struct {
		MessageID string `json:"message_id"`
	}
	err := c.post(ctx, map[string]any{"recipient": map[string]string{"id": psid}, "messaging_type": "RESPONSE", "message": msg}, &out)
	return out.MessageID, err
}

// SenderAction shows "typing_on", "typing_off" or "mark_seen".
func (c *Client) SenderAction(ctx context.Context, psid, action string) error {
	return c.post(ctx, map[string]any{"recipient": map[string]string{"id": psid}, "sender_action": action}, nil)
}

func (c *Client) post(ctx context.Context, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	page := c.PageID
	if page == "" {
		page = "me"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GraphURL+"/"+page+"/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// A header keeps the token out of URLs and therefore out of logs.
	req.Header.Set("Authorization", "Bearer "+c.PageToken)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		var env struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			env.Error.Status = res.StatusCode
			return env.Error
		}
		return &Error{Status: res.StatusCode, Message: strings.TrimSpace(Clip(string(data), 300))}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ValidSignature checks Meta's X-Hub-Signature-256 header over the raw body.
func ValidSignature(appSecret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || appSecret == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// Sign renders the X-Hub-Signature-256 value for body (used by tests).
func Sign(appSecret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Webhook is the body Meta posts for page events.
type Webhook struct {
	Object string `json:"object"`
	Entry  []struct {
		ID        string  `json:"id"`
		Time      int64   `json:"time"`
		Messaging []Event `json:"messaging"`
	} `json:"entry"`
}

// Event is one messaging event: a message, a postback, or something the
// connector ignores (reads, deliveries, reactions).
type Event struct {
	Sender    struct{ ID string } `json:"sender"`
	Recipient struct{ ID string } `json:"recipient"`
	Timestamp int64               `json:"timestamp"`
	Message   *struct {
		MID        string `json:"mid"`
		Text       string `json:"text"`
		IsEcho     bool   `json:"is_echo"`
		QuickReply *struct {
			Payload string `json:"payload"`
		} `json:"quick_reply"`
		ReplyTo *struct {
			MID string `json:"mid"`
		} `json:"reply_to"`
		Attachments []struct {
			Type    string `json:"type"`
			Payload struct {
				URL string `json:"url"`
			} `json:"payload"`
		} `json:"attachments"`
	} `json:"message"`
	Postback *struct {
		MID     string `json:"mid"`
		Title   string `json:"title"`
		Payload string `json:"payload"`
	} `json:"postback"`
}

// Clip shortens s to at most n characters, marking the cut.
func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
