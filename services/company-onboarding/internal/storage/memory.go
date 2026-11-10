package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

type storedItem struct {
	data     []byte
	metadata ObjectMetadata
}

// MemoryStore provides a thread-safe in-memory ObjectStore implementation for unit tests
// and local fixture development, enforcing key validation and object immutability.
type MemoryStore struct {
	mu          sync.RWMutex
	items       map[string]storedItem
	deletedKeys []string
	writtenKeys []string
	failPutErr  error
	failGetErr  error
	failDelErr  error
	putHook     func(key string) error
	deleteHook  func(key string) error
}

// NewMemoryStore creates a clean, initialized MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: make(map[string]storedItem),
	}
}

// Put stores bytes in memory. Returns ErrObjectExists if key is already occupied.
func (m *MemoryStore) Put(ctx context.Context, key string, r io.Reader, sizeBytes int64, contentType string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failPutErr != nil {
		err := m.failPutErr
		m.failPutErr = nil
		return err
	}
	if m.putHook != nil {
		if err := m.putHook(key); err != nil {
			return err
		}
	}

	if _, exists := m.items[key]; exists {
		return ErrObjectExists
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read object data: %w", err)
	}

	if sizeBytes > 0 && int64(len(data)) != sizeBytes {
		return fmt.Errorf("actual data size %d does not match expected size %d", len(data), sizeBytes)
	}

	h := sha256.Sum256(data)
	etag := hex.EncodeToString(h[:])

	m.items[key] = storedItem{
		data: data,
		metadata: ObjectMetadata{
			Key:                  key,
			SizeBytes:            int64(len(data)),
			ContentType:          contentType,
			ServerSideEncryption: "AES256",
			ETag:                 etag,
		},
	}
	m.writtenKeys = append(m.writtenKeys, key)
	return nil
}

// Get returns a reader for the stored object data and its metadata.
func (m *MemoryStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error) {
	if err := ValidateKey(key); err != nil {
		return nil, ObjectMetadata{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.failGetErr != nil {
		return nil, ObjectMetadata{}, m.failGetErr
	}

	item, exists := m.items[key]
	if !exists {
		return nil, ObjectMetadata{}, ErrObjectNotFound
	}

	// Return an independent copy of data to prevent external mutation
	copied := make([]byte, len(item.data))
	copy(copied, item.data)

	return io.NopCloser(bytes.NewReader(copied)), item.metadata, nil
}

// Delete removes an object from the store.
func (m *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failDelErr != nil {
		err := m.failDelErr
		m.failDelErr = nil
		return err
	}
	if m.deleteHook != nil {
		if err := m.deleteHook(key); err != nil {
			return err
		}
	}

	delete(m.items, key)
	m.deletedKeys = append(m.deletedKeys, key)
	return nil
}

// Exists checks if the key is present.
func (m *MemoryStore) Exists(ctx context.Context, key string) (bool, error) {
	if err := ValidateKey(key); err != nil {
		return false, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.items[key]
	return exists, nil
}

// SetFailPut configures a one-shot error to return on next Put call.
func (m *MemoryStore) SetFailPut(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPutErr = err
}

// SetFailDelete configures a one-shot error to return on next Delete call.
func (m *MemoryStore) SetFailDelete(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failDelErr = err
}

// DeletedKeys returns a copy of all keys deleted during testing.
func (m *MemoryStore) DeletedKeys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]string, len(m.deletedKeys))
	copy(cp, m.deletedKeys)
	return cp
}

// WrittenKeys returns a copy of all keys written during testing.
func (m *MemoryStore) WrittenKeys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]string, len(m.writtenKeys))
	copy(cp, m.writtenKeys)
	return cp
}
