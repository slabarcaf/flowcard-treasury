// Package storage provides private object storage abstractions and adapters
// for encrypted document content, enforcing server-side encryption at-rest
// and strict object immutability without public URL exposure.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Standard storage errors.
var (
	// ErrObjectNotFound indicates the requested object does not exist in the store.
	ErrObjectNotFound = errors.New("storage: object not found")

	// ErrObjectExists indicates an object already exists at the target key and overwrite was rejected.
	ErrObjectExists = errors.New("storage: object already exists; overwrite rejected")

	// ErrStorageUnconfigured indicates that production storage credentials, bucket, or KMS keys are missing.
	ErrStorageUnconfigured = errors.New("storage: object store unconfigured: production credentials or bucket missing")

	// ErrMissingEncryption indicates that server-side encryption at-rest was not configured.
	ErrMissingEncryption = errors.New("storage: server-side encryption at-rest explicitly required via adapter config")

	// ErrInvalidKey indicates that a storage key is empty, malformed, or attempts path traversal.
	ErrInvalidKey = errors.New("storage: invalid storage key")

	// ErrInvalidEndpoint indicates that the storage endpoint URL contains credentials, query, or path components.
	ErrInvalidEndpoint = errors.New("storage: invalid storage endpoint URL")

	// ErrInsecureEndpoint indicates that the storage endpoint is not using strict HTTPS.
	ErrInsecureEndpoint = errors.New("storage: strict HTTPS is required for object storage endpoint")
)

// ObjectMetadata describes a stored object.
type ObjectMetadata struct {
	Key                  string
	SizeBytes            int64
	ContentType          string
	ServerSideEncryption string
	ETag                 string
}

// ObjectStore defines the consumer interface for private document object storage.
type ObjectStore interface {
	// Put writes object bytes to the store with the specified content type.
	// Overwriting an existing key must be rejected with ErrObjectExists.
	Put(ctx context.Context, key string, r io.Reader, sizeBytes int64, contentType string) error

	// Get retrieves a reader for the object bytes and its metadata.
	// Caller must close the returned ReadCloser.
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error)

	// Delete removes the object at key. Used primarily for compensation on DB failure.
	Delete(ctx context.Context, key string) error

	// Exists checks if an object exists at key.
	Exists(ctx context.Context, key string) (bool, error)
}

// GenerateKey constructs an opaque, server-controlled storage key scoped by application ID and document ID.
// Prevents directory traversal and cross-application access.
func GenerateKey(appID, docID string) (string, error) {
	cleanAppID := strings.TrimSpace(appID)
	cleanDocID := strings.TrimSpace(docID)
	if cleanAppID == "" || cleanDocID == "" {
		return "", ErrInvalidKey
	}
	if strings.Contains(cleanAppID, "..") || strings.Contains(cleanDocID, "..") {
		return "", ErrInvalidKey
	}
	if strings.ContainsAny(cleanAppID, "/\\") || strings.ContainsAny(cleanDocID, "/\\") {
		return "", ErrInvalidKey
	}
	return path.Join("apps", cleanAppID, "docs", cleanDocID), nil
}

// ValidateKey validates that a storage key matches the expected scoped pattern and contains no path traversal.
func ValidateKey(key string) error {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return ErrInvalidKey
	}
	if strings.Contains(clean, "..") || strings.Contains(clean, "\\") {
		return ErrInvalidKey
	}
	parts := strings.Split(clean, "/")
	// Expected: apps/<app_id>/docs/<doc_id>
	if len(parts) != 4 || parts[0] != "apps" || parts[2] != "docs" {
		return ErrInvalidKey
	}
	if parts[1] == "" || parts[3] == "" {
		return ErrInvalidKey
	}
	return nil
}

// UnavailableStore is the default fail-closed storage implementation used when
// production storage credentials or bucket are not configured.
type UnavailableStore struct {
	reason string
}

// NewUnavailableStore initializes a store that rejects all operations with ErrStorageUnconfigured.
func NewUnavailableStore(reason string) *UnavailableStore {
	if reason == "" {
		reason = "production object store credentials or configuration unavailable"
	}
	return &UnavailableStore{reason: reason}
}

// Put always fails closed with ErrStorageUnconfigured.
func (u *UnavailableStore) Put(ctx context.Context, key string, r io.Reader, sizeBytes int64, contentType string) error {
	return fmt.Errorf("%w: %s", ErrStorageUnconfigured, u.reason)
}

// Get always fails closed with ErrStorageUnconfigured.
func (u *UnavailableStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error) {
	return nil, ObjectMetadata{}, fmt.Errorf("%w: %s", ErrStorageUnconfigured, u.reason)
}

// Delete always fails closed with ErrStorageUnconfigured.
func (u *UnavailableStore) Delete(ctx context.Context, key string) error {
	return fmt.Errorf("%w: %s", ErrStorageUnconfigured, u.reason)
}

// Exists always fails closed with ErrStorageUnconfigured.
func (u *UnavailableStore) Exists(ctx context.Context, key string) (bool, error) {
	return false, fmt.Errorf("%w: %s", ErrStorageUnconfigured, u.reason)
}
