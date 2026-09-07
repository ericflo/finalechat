// Package blob stores attachment bytes. Production uses Backblaze B2 through
// its native API; tests and local development can use an in-memory store.
package blob

import (
	"context"
	"errors"
	"io"
	"sync"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

// Object is a stored blob.
type Object struct {
	// Key is the object name.
	Key string
	// ID is the storage-specific version id needed to delete the object.
	ID string
	// Size is the byte length.
	Size int64
}

// Store is the minimal object storage contract Finalechat needs.
type Store interface {
	// Put stores data under key with the given content type.
	Put(ctx context.Context, key, contentType string, data []byte) (Object, error)
	// Get streams the object. The caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
	// Delete removes the object version; a missing object is not an error.
	Delete(ctx context.Context, key, id string) error
	// Name describes the backend for logs and health output.
	Name() string
}

// Memory keeps objects in process memory.
type Memory struct {
	mu   sync.RWMutex
	data map[string]memObject
}

type memObject struct {
	contentType string
	data        []byte
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory { return &Memory{data: map[string]memObject{}} }

func (m *Memory) Put(_ context.Context, key, contentType string, data []byte) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[key] = memObject{contentType: contentType, data: cp}
	return Object{Key: key, ID: "mem", Size: int64(len(data))}, nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.data[key]
	if !ok {
		return nil, "", 0, ErrNotFound
	}
	return io.NopCloser(bytesReader(o.data)), o.contentType, int64(len(o.data)), nil
}

func (m *Memory) Delete(_ context.Context, key, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *Memory) Name() string { return "memory" }
