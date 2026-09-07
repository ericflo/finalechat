package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// TestB2Live exercises the real API when credentials are provided:
//
//	FINALECHAT_B2_TEST_KEY_ID, FINALECHAT_B2_TEST_KEY, FINALECHAT_B2_TEST_BUCKET
func TestB2Live(t *testing.T) {
	keyID, key, bucket := os.Getenv("FINALECHAT_B2_TEST_KEY_ID"), os.Getenv("FINALECHAT_B2_TEST_KEY"), os.Getenv("FINALECHAT_B2_TEST_BUCKET")
	if keyID == "" || key == "" || bucket == "" {
		t.Skip("FINALECHAT_B2_TEST_* not set")
	}
	b := NewB2(keyID, key, bucket)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := b.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	const name = "a/selftest/hello world.txt"
	obj, err := b.Put(ctx, name, "text/plain", []byte("finalechat b2 self-test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if obj.ID == "" || obj.Size != 24 {
		t.Fatalf("unexpected object %+v", obj)
	}
	r, ct, size, err := b.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if ct != "text/plain" || size != 24 || string(data) != "finalechat b2 self-test\n" {
		t.Fatalf("unexpected download ct=%q size=%d data=%q", ct, size, data)
	}
	if err := b.Delete(ctx, name, obj.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := b.Get(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Deleting again is a no-op.
	if err := b.Delete(ctx, name, obj.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestMemory(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if _, err := m.Put(ctx, "k", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}
	r, ct, size, err := m.Get(ctx, "k")
	if err != nil || ct != "text/plain" || size != 1 {
		t.Fatalf("get: %v %q %d", err, ct, size)
	}
	r.Close()
	_ = m.Delete(ctx, "k", "mem")
	if _, _, _, err := m.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
