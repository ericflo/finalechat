// Package config loads Finalechat's runtime configuration from the process
// environment. Every value has a documented name and a safe default so the
// binary can be started with only DATABASE_URL set.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	// Addr is the listen address, for example ":8080".
	Addr string
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// BaseURL is the public origin of this deployment, used in documentation,
	// installation snippets and push notification links.
	BaseURL string
	// CanonicalHost, when set, is the host that browser navigations on any of
	// RedirectHosts are redirected to.
	CanonicalHost string
	// RedirectHosts are hosts whose navigations redirect to CanonicalHost.
	RedirectHosts []string
	// VAPIDPublicKey and VAPIDPrivateKey are the Web Push keys. Push is
	// disabled when either is empty.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	// VAPIDSubject is the contact URL sent to push services (mailto: or https:).
	VAPIDSubject string
	// InviteCode gates registration once the first account exists. When empty,
	// registration closes after the first account.
	InviteCode string
	// SecureCookies controls the Secure attribute on session cookies.
	SecureCookies bool
	// TrustProxy controls whether X-Forwarded-* headers are honoured.
	TrustProxy bool
	// SessionTTL is the sliding lifetime of a browser session.
	SessionTTL time.Duration
	// LogJSON switches the logger to JSON output.
	LogJSON bool
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// Version is stamped at build time.
	Version string
}

// Load reads configuration from the environment.
func Load(version string) (Config, error) {
	cfg := Config{
		Addr:            getenv("FINALECHAT_ADDR", ":8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		BaseURL:         strings.TrimRight(getenv("FINALECHAT_BASE_URL", "http://localhost:8080"), "/"),
		CanonicalHost:   os.Getenv("FINALECHAT_CANONICAL_HOST"),
		VAPIDPublicKey:  os.Getenv("FINALECHAT_VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey: os.Getenv("FINALECHAT_VAPID_PRIVATE_KEY"),
		VAPIDSubject:    getenv("FINALECHAT_VAPID_SUBJECT", "mailto:hello@finalechat.com"),
		InviteCode:      os.Getenv("FINALECHAT_INVITE_CODE"),
		SecureCookies:   getenvBool("FINALECHAT_SECURE_COOKIES", true),
		TrustProxy:      getenvBool("FINALECHAT_TRUST_PROXY", true),
		SessionTTL:      getenvDuration("FINALECHAT_SESSION_TTL", 90*24*time.Hour),
		LogJSON:         getenvBool("FINALECHAT_LOG_JSON", true),
		LogLevel:        getenv("FINALECHAT_LOG_LEVEL", "info"),
		Version:         version,
	}
	if hosts := os.Getenv("FINALECHAT_REDIRECT_HOSTS"); hosts != "" {
		for _, h := range strings.Split(hosts, ",") {
			if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
				cfg.RedirectHosts = append(cfg.RedirectHosts, h)
			}
		}
	}
	if cfg.DatabaseURL == "" {
		return cfg, errors.New("DATABASE_URL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return cfg, fmt.Errorf("FINALECHAT_BASE_URL is invalid: %w", err)
	}
	if (cfg.VAPIDPublicKey == "") != (cfg.VAPIDPrivateKey == "") {
		return cfg, errors.New("FINALECHAT_VAPID_PUBLIC_KEY and FINALECHAT_VAPID_PRIVATE_KEY must be set together")
	}
	if len(cfg.RedirectHosts) > 0 && cfg.CanonicalHost == "" {
		return cfg, errors.New("FINALECHAT_CANONICAL_HOST is required when FINALECHAT_REDIRECT_HOSTS is set")
	}
	return cfg, nil
}

// PushEnabled reports whether Web Push can be used.
func (c Config) PushEnabled() bool {
	return c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != ""
}

func getenv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func getenvBool(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func getenvDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
