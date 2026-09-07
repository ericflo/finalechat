// Command finalechat runs the Finalechat server: the agent API, the real-time
// event stream, push notifications, and the progressive web app.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ericflo/finalechat/internal/api"
	"github.com/ericflo/finalechat/internal/blob"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/config"
	"github.com/ericflo/finalechat/internal/db"
	"github.com/ericflo/finalechat/internal/push"
	"github.com/ericflo/finalechat/internal/store"
)

// version is stamped by the build with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Every timestamp the API emits is UTC regardless of the host timezone.
	time.Local = time.UTC
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "vapid":
			priv, pub, err := push.GenerateVAPIDKeys()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("FINALECHAT_VAPID_PUBLIC_KEY=%s\nFINALECHAT_VAPID_PRIVATE_KEY=%s\n", pub, priv)
			return
		case "version":
			fmt.Println(version)
			return
		case "serve":
		default:
			fmt.Fprintf(os.Stderr, "usage: finalechat [serve|vapid|version]\n")
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "finalechat:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(version)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting finalechat", "version", cfg.Version, "addr", cfg.Addr, "base_url", cfg.BaseURL, "push", cfg.PushEnabled())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := openDatabase(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, log); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	st := store.New(pool)
	b := bus.New(pool, log)
	go b.Run(ctx)
	sender := push.New(st, log, cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey, cfg.VAPIDSubject)
	var blobs blob.Store
	switch cfg.BlobStore {
	case "b2":
		b2 := blob.NewB2(cfg.B2KeyID, cfg.B2Key, cfg.B2Bucket)
		pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := b2.Ping(pingCtx)
		cancel()
		if err != nil {
			// Messages and questions must not depend on object storage being
			// up at the moment a pod starts; uploads authorize lazily.
			log.Error("attachment storage unreachable at start; continuing", "err", err)
		}
		blobs = b2
	case "memory":
		blobs = blob.NewMemory()
	}
	if blobs != nil {
		log.Info("attachments enabled", "store", blobs.Name())
	} else {
		log.Info("attachments disabled")
	}
	srv := api.New(cfg, st, b, sender, blobs, log)
	go srv.RunMaintenance(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Long-polls and event streams are bounded by their own deadlines.
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	srv.Shutdown()
	// Readiness now fails; give the load balancer a moment to notice before
	// connections are refused.
	select {
	case <-time.After(cfg.ShutdownDelay):
	case <-errCh:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	sender.Wait(shutdownCtx)
	return nil
}

// openDatabase retries for a while so a cold start alongside PostgreSQL does
// not crash-loop; a persistent failure still exits so the orchestrator sees it.
func openDatabase(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(2 * time.Minute)
	delay := time.Second
	for {
		pool, err := db.Open(ctx, url)
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		log.Warn("database not ready; retrying", "err", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.LogJSON {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
