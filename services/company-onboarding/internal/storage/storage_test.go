package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
)

func TestStorageKeyGenerationAndValidation(t *testing.T) {
	// Valid key generation
	key, err := storage.GenerateKey("app_123", "doc_456")
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if key != "apps/app_123/docs/doc_456" {
		t.Errorf("GenerateKey = %q, want apps/app_123/docs/doc_456", key)
	}
	if err := storage.ValidateKey(key); err != nil {
		t.Errorf("ValidateKey on valid key failed: %v", err)
	}

	// Path traversal attempts must be rejected
	traversals := []struct {
		appID string
		docID string
	}{
		{"../app", "doc"},
		{"app", "../doc"},
		{"app/sub", "doc"},
		{"app", "doc/sub"},
		{"", "doc"},
		{"app", ""},
		{"app\\win", "doc"},
	}

	for _, tc := range traversals {
		_, err := storage.GenerateKey(tc.appID, tc.docID)
		if !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("GenerateKey(%q, %q) expected ErrInvalidKey, got %v", tc.appID, tc.docID, err)
		}
	}

	// Invalid key validation
	invalidKeys := []string{
		"../../etc/passwd",
		"apps/app1/doc1",          // missing docs segment
		"other/app1/docs/doc1",    // wrong root
		"apps//docs/doc1",         // empty app id
		"apps/app1/docs/",         // empty doc id
		"apps/app1/docs/doc1/sub", // extra segments
	}
	for _, ik := range invalidKeys {
		if err := storage.ValidateKey(ik); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("ValidateKey(%q) expected ErrInvalidKey, got %v", ik, err)
		}
	}
}

func TestMemoryStore_LifecycleAndOverwriteRejection(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	key, _ := storage.GenerateKey("app_test", "doc_1")
	payload := []byte("%PDF-1.4 test document content")

	// 1. Put object
	err := store.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/pdf")
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// 2. Exists
	exists, err := store.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true, nil", exists, err)
	}

	// 3. Overwrite must be rejected with ErrObjectExists
	err = store.Put(ctx, key, bytes.NewReader([]byte("replacement")), 11, "application/pdf")
	if !errors.Is(err, storage.ErrObjectExists) {
		t.Fatalf("Put overwrite expected ErrObjectExists, got: %v", err)
	}

	// 4. Get object
	rc, meta, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer rc.Close()

	if meta.SizeBytes != int64(len(payload)) {
		t.Errorf("meta.SizeBytes = %d, want %d", meta.SizeBytes, len(payload))
	}
	if meta.ContentType != "application/pdf" {
		t.Errorf("meta.ContentType = %q, want application/pdf", meta.ContentType)
	}
	readData, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(readData, payload) {
		t.Fatalf("readData mismatch: got %q, want %q", string(readData), string(payload))
	}

	// 5. Delete object (compensation)
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	exists, _ = store.Exists(ctx, key)
	if exists {
		t.Fatalf("object still exists after delete")
	}

	// 6. Get non-existent returns ErrObjectNotFound
	_, _, err = store.Get(ctx, key)
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Get after delete expected ErrObjectNotFound, got: %v", err)
	}

	// Verify tracked deleted keys
	delKeys := store.DeletedKeys()
	if len(delKeys) != 1 || delKeys[0] != key {
		t.Errorf("DeletedKeys = %v, want [%s]", delKeys, key)
	}
}

func TestUnavailableStore_FailsClosed(t *testing.T) {
	ctx := context.Background()
	store := storage.NewUnavailableStore("missing AWS credentials")

	key, _ := storage.GenerateKey("app_un", "doc_un")

	if err := store.Put(ctx, key, strings.NewReader("data"), 4, "text/plain"); !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Errorf("Put expected ErrStorageUnconfigured, got: %v", err)
	}
	if _, _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Errorf("Get expected ErrStorageUnconfigured, got: %v", err)
	}
	if err := store.Delete(ctx, key); !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Errorf("Delete expected ErrStorageUnconfigured, got: %v", err)
	}
	if _, err := store.Exists(ctx, key); !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Errorf("Exists expected ErrStorageUnconfigured, got: %v", err)
	}
}

type fakeS3Transport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (f *fakeS3Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.roundTripFunc(req)
}

func TestS3Store_ConfigurationValidation(t *testing.T) {
	// Missing credentials or bucket fails closed
	_, err := storage.NewS3Store(storage.S3Config{})
	if !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Fatalf("empty config expected ErrStorageUnconfigured, got %v", err)
	}

	// Missing server-side encryption fails closed (HTTPS alone is not encryption-at-rest)
	_, err = storage.NewS3Store(storage.S3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		// ServerSideEncryption omitted!
	})
	if !errors.Is(err, storage.ErrMissingEncryption) {
		t.Fatalf("missing SSE expected ErrMissingEncryption, got %v", err)
	}

	// aws:kms without KMSKeyID fails closed
	_, err = storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "aws:kms",
		// KMSKeyID omitted!
	})
	if !errors.Is(err, storage.ErrMissingEncryption) {
		t.Fatalf("aws:kms without KMSKeyID expected ErrMissingEncryption, got %v", err)
	}

	// Valid AES256 configuration succeeds
	store, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "AES256",
	})
	if err != nil || store == nil {
		t.Fatalf("valid config failed: %v", err)
	}

	// Injected CredentialsProvider without static keys succeeds
	mockProvider := credentials.NewStaticCredentialsProvider("AKIAMOCKPROVIDER1234", "mockSecretKeyForProviderInjection12345678", "")
	storeWithProvider, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		CredentialsProvider:  mockProvider,
		ServerSideEncryption: "aws:kms",
		KMSKeyID:             "arn:aws:kms:us-west-2:123456789012:key/test",
	})
	if err != nil || storeWithProvider == nil {
		t.Fatalf("config with CredentialsProvider failed: %v", err)
	}

	// Missing both CredentialsProvider and static keys fails closed with ErrStorageUnconfigured
	_, err = storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		ServerSideEncryption: "AES256",
	})
	if !errors.Is(err, storage.ErrStorageUnconfigured) {
		t.Fatalf("missing credentials expected ErrStorageUnconfigured, got %v", err)
	}

	// Endpoint security validations
	invalidEndpoints := []struct {
		url string
		err error
	}{
		{"https://user:pass@s3.amazonaws.com", storage.ErrInvalidEndpoint},
		{"https://s3.amazonaws.com?foo=bar", storage.ErrInvalidEndpoint},
		{"https://s3.amazonaws.com/extra/path", storage.ErrInvalidEndpoint},
		{"https://s3.amazonaws.com#fragment", storage.ErrInvalidEndpoint},
		{"ftp://s3.amazonaws.com", storage.ErrInvalidEndpoint},
		{"ftp://localhost:9000", storage.ErrInvalidEndpoint},
		{"https://", storage.ErrInvalidEndpoint},
		{"http://s3.amazonaws.com", storage.ErrInsecureEndpoint},
	}
	for _, tc := range invalidEndpoints {
		_, err := storage.NewS3Store(storage.S3Config{
			Bucket:               "my-bucket",
			AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			ServerSideEncryption: "AES256",
			Endpoint:             tc.url,
		})
		if !errors.Is(err, tc.err) {
			t.Errorf("endpoint %q expected error %v, got %v", tc.url, tc.err, err)
		}
	}

	// Localhost HTTP endpoints allowed for testing
	localEndpoints := []string{"http://localhost:9000", "http://127.0.0.1:9000"}
	for _, ep := range localEndpoints {
		localStore, err := storage.NewS3Store(storage.S3Config{
			Bucket:               "my-bucket",
			AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			ServerSideEncryption: "AES256",
			Endpoint:             ep,
		})
		if err != nil || localStore == nil {
			t.Errorf("local endpoint %q expected success, got %v", ep, err)
		}
	}
}

func TestS3Store_PutGetDeleteOperationsWithFakeTransport(t *testing.T) {
	ctx := context.Background()
	objects := make(map[string][]byte)

	fakeTransport := &fakeS3Transport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path // e.g. /my-bucket/apps/app_1/docs/doc_1

			// Assert that Auth and SigV4 signature are present
			if !strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader("Missing SigV4 Authorization")),
				}, nil
			}

			switch req.Method {
			case http.MethodHead:
				if _, ok := objects[path]; ok {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader(nil)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil

			case http.MethodPut:
				// Critical assertion: verify If-None-Match header is * (atomic conditional overwrite guard)
				if req.Header.Get("If-None-Match") != "*" {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(strings.NewReader("Missing required If-None-Match: * header")),
					}, nil
				}

				// If object already exists, return 412 Precondition Failed
				if _, exists := objects[path]; exists {
					respHeader := make(http.Header)
					respHeader.Set("x-amz-error-code", "PreconditionFailed")
					return &http.Response{
						StatusCode: http.StatusPreconditionFailed,
						Header:     respHeader,
						Body:       io.NopCloser(strings.NewReader("<Error><Code>PreconditionFailed</Code></Error>")),
					}, nil
				}

				// Critical assertion: verify server-side encryption header is present
				sseHeader := req.Header.Get("x-amz-server-side-encryption")
				if sseHeader != "aws:kms" {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(strings.NewReader("Missing required encryption header")),
					}, nil
				}
				kmsKey := req.Header.Get("x-amz-server-side-encryption-aws-kms-key-id")
				if kmsKey != "arn:aws:kms:us-east-1:123456789012:key/test-key" {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(strings.NewReader("Invalid KMS Key ID header")),
					}, nil
				}

				body, _ := io.ReadAll(req.Body)
				objects[path] = body

				respHeader := make(http.Header)
				respHeader.Set("x-amz-server-side-encryption", "aws:kms")
				respHeader.Set("ETag", `"etag-test-12345"`)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     respHeader,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil

			case http.MethodGet:
				body, ok := objects[path]
				if !ok {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(bytes.NewReader(nil)),
					}, nil
				}
				resp := &http.Response{
					StatusCode:    http.StatusOK,
					Body:          io.NopCloser(bytes.NewReader(body)),
					ContentLength: int64(len(body)),
					Header:        make(http.Header),
				}
				resp.Header.Set("Content-Type", "application/pdf")
				resp.Header.Set("x-amz-server-side-encryption", "aws:kms")
				resp.Header.Set("ETag", `"etag-test-12345"`)
				return resp, nil

			case http.MethodDelete:
				delete(objects, path)
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil

			default:
				return &http.Response{StatusCode: http.StatusMethodNotAllowed}, nil
			}
		},
	}

	store, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "aws:kms",
		KMSKeyID:             "arn:aws:kms:us-east-1:123456789012:key/test-key",
		HTTPClient:           &http.Client{Transport: fakeTransport},
	})
	if err != nil {
		t.Fatalf("NewS3Store failed: %v", err)
	}

	key, _ := storage.GenerateKey("app_s3", "doc_s3")
	docPayload := []byte("%PDF-1.4 encrypted s3 document")

	// 1. Put document
	if err := store.Put(ctx, key, bytes.NewReader(docPayload), int64(len(docPayload)), "application/pdf"); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// 2. Overwrite rejection (emulating 412 Precondition Failed)
	err = store.Put(ctx, key, bytes.NewReader([]byte("overwrite attempt")), 17, "application/pdf")
	if !errors.Is(err, storage.ErrObjectExists) {
		t.Fatalf("expected ErrObjectExists on duplicate Put, got %v", err)
	}

	// 3. Get document
	rc, meta, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if !bytes.Equal(data, docPayload) {
		t.Fatalf("data mismatch")
	}
	if meta.ServerSideEncryption != "aws:kms" {
		t.Errorf("meta.ServerSideEncryption = %q, want aws:kms", meta.ServerSideEncryption)
	}

	// 4. Delete document
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 5. Exists returns false after delete
	exists, err := store.Exists(ctx, key)
	if err != nil || exists {
		t.Fatalf("expected exists=false after delete, got %v, %v", exists, err)
	}
}

func TestS3Store_EncryptionMismatchCompensatesAndDelete(t *testing.T) {
	// 1. Normal cleanup on unencrypted response
	deletedPaths := make([]string, 0)
	fakeTransport := &fakeS3Transport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodPut:
				// Return OK but WITHOUT server-side encryption confirmation header
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header), // Missing x-amz-server-side-encryption
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			case http.MethodDelete:
				deletedPaths = append(deletedPaths, req.URL.Path)
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			default:
				return &http.Response{StatusCode: http.StatusNotFound}, nil
			}
		},
	}

	store, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "aws:kms",
		KMSKeyID:             "arn:aws:kms:us-east-1:123456789012:key/test-key",
		HTTPClient:           &http.Client{Transport: fakeTransport},
	})
	if err != nil {
		t.Fatalf("NewS3Store failed: %v", err)
	}

	key, _ := storage.GenerateKey("app_enc", "doc_enc")
	err = store.Put(context.Background(), key, strings.NewReader("payload"), 7, "text/plain")
	if !errors.Is(err, storage.ErrMissingEncryption) {
		t.Fatalf("expected ErrMissingEncryption, got %v", err)
	}
	if len(deletedPaths) != 1 || deletedPaths[0] != "/my-bucket/"+key {
		t.Fatalf("expected 1 delete compensation call for /my-bucket/%s, got %v", key, deletedPaths)
	}

	// 2. Cleanup succeeds even when request context is canceled during response handling (uses independent bounded cleanup context)
	deletedPaths = nil
	ctxReq, cancelReq := context.WithCancel(context.Background())
	key2, _ := storage.GenerateKey("app_enc2", "doc_enc2")

	fakeTransport.roundTripFunc = func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPut:
			cancelReq() // cancel request context as response arrives
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header), // Missing x-amz-server-side-encryption
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, req.URL.Path)
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound}, nil
		}
	}

	err = store.Put(ctxReq, key2, strings.NewReader("payload"), 7, "text/plain")
	if !errors.Is(err, storage.ErrMissingEncryption) {
		t.Fatalf("expected ErrMissingEncryption despite canceled request ctx, got %v", err)
	}
	if len(deletedPaths) != 1 || deletedPaths[0] != "/my-bucket/"+key2 {
		t.Fatalf("expected independent cleanup context to complete delete for %s, got %v", key2, deletedPaths)
	}
}

func TestS3Store_EncryptionMismatchFailedDeleteRequiresManualRemediation(t *testing.T) {
	fakeTransport := &fakeS3Transport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodPut:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header), // Missing x-amz-server-side-encryption
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			case http.MethodDelete:
				// DeleteObject fails upstream
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("Internal S3 Delete Error")),
				}, nil
			default:
				return &http.Response{StatusCode: http.StatusNotFound}, nil
			}
		},
	}

	store, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "aws:kms",
		KMSKeyID:             "arn:aws:kms:us-east-1:123456789012:key/test-key",
		HTTPClient:           &http.Client{Transport: fakeTransport},
	})
	if err != nil {
		t.Fatalf("NewS3Store failed: %v", err)
	}

	key, _ := storage.GenerateKey("app_fail_del", "doc_fail_del")
	err = store.Put(context.Background(), key, strings.NewReader("payload"), 7, "text/plain")
	if !errors.Is(err, storage.ErrMissingEncryption) {
		t.Fatalf("expected ErrMissingEncryption, got %v", err)
	}
	if !strings.Contains(err.Error(), "manual remediation required: cleanup deletion failed") {
		t.Errorf("expected error to highlight manual remediation required, got: %v", err)
	}
}

func TestS3Store_ConcurrentPutAtomicBackend(t *testing.T) {
	var mu sync.Mutex
	objects := make(map[string][]byte)

	fakeTransport := &fakeS3Transport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path

			switch req.Method {
			case http.MethodPut:
				// Verify conditional If-None-Match header
				if req.Header.Get("If-None-Match") != "*" {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(strings.NewReader("Missing If-None-Match")),
					}, nil
				}

				mu.Lock()
				defer mu.Unlock()

				// Atomic check-and-insert simulation
				if _, exists := objects[path]; exists {
					respHeader := make(http.Header)
					respHeader.Set("x-amz-error-code", "PreconditionFailed")
					return &http.Response{
						StatusCode: http.StatusPreconditionFailed,
						Header:     respHeader,
						Body:       io.NopCloser(strings.NewReader("<Error><Code>PreconditionFailed</Code></Error>")),
					}, nil
				}

				body, _ := io.ReadAll(req.Body)
				objects[path] = body

				respHeader := make(http.Header)
				respHeader.Set("x-amz-server-side-encryption", "aws:kms")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     respHeader,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil

			case http.MethodGet:
				mu.Lock()
				defer mu.Unlock()
				body, ok := objects[path]
				if !ok {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(bytes.NewReader(nil)),
					}, nil
				}
				resp := &http.Response{
					StatusCode:    http.StatusOK,
					Body:          io.NopCloser(bytes.NewReader(body)),
					ContentLength: int64(len(body)),
					Header:        make(http.Header),
				}
				resp.Header.Set("Content-Type", "application/pdf")
				resp.Header.Set("x-amz-server-side-encryption", "aws:kms")
				return resp, nil

			default:
				return &http.Response{StatusCode: http.StatusMethodNotAllowed}, nil
			}
		},
	}

	store, err := storage.NewS3Store(storage.S3Config{
		Bucket:               "my-bucket",
		AccessKeyID:          "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ServerSideEncryption: "aws:kms",
		KMSKeyID:             "arn:aws:kms:us-east-1:123456789012:key/test-key",
		HTTPClient:           &http.Client{Transport: fakeTransport},
	})
	if err != nil {
		t.Fatalf("NewS3Store failed: %v", err)
	}

	key, _ := storage.GenerateKey("app_concurrent", "doc_concurrent")
	payloadA := []byte("%PDF-1.4 PAYLOAD A FROM WRITER 1")
	payloadB := []byte("%PDF-1.4 PAYLOAD B FROM WRITER 2")

	var wg sync.WaitGroup
	var errA, errB error

	wg.Add(2)
	go func() {
		defer wg.Done()
		errA = store.Put(context.Background(), key, bytes.NewReader(payloadA), int64(len(payloadA)), "application/pdf")
	}()
	go func() {
		defer wg.Done()
		errB = store.Put(context.Background(), key, bytes.NewReader(payloadB), int64(len(payloadB)), "application/pdf")
	}()
	wg.Wait()

	// Exactly one must succeed and the other must fail with ErrObjectExists
	successCount := 0
	conflictCount := 0

	if errA == nil {
		successCount++
	} else if errors.Is(errA, storage.ErrObjectExists) {
		conflictCount++
	} else {
		t.Errorf("unexpected error for writer A: %v", errA)
	}

	if errB == nil {
		successCount++
	} else if errors.Is(errB, storage.ErrObjectExists) {
		conflictCount++
	} else {
		t.Errorf("unexpected error for writer B: %v", errB)
	}

	if successCount != 1 || conflictCount != 1 {
		t.Fatalf("expected exactly 1 success and 1 conflict, got success=%d conflict=%d (errA=%v, errB=%v)",
			successCount, conflictCount, errA, errB)
	}

	// Verify that the stored bytes in the atomic backend match the winner and were never overwritten
	mu.Lock()
	storedBytes := objects["/my-bucket/"+key]
	mu.Unlock()

	if errA == nil {
		if !bytes.Equal(storedBytes, payloadA) {
			t.Fatalf("stored bytes do not match winning writer A: got %q, want %q", string(storedBytes), string(payloadA))
		}
	} else {
		if !bytes.Equal(storedBytes, payloadB) {
			t.Fatalf("stored bytes do not match winning writer B: got %q, want %q", string(storedBytes), string(payloadB))
		}
	}
}

func TestResolveRuntimeCredentials(t *testing.T) {
	t.Parallel()

	t.Run("static_credentials_with_session_token", func(t *testing.T) {
		t.Parallel()
		lookup := func(k string) string {
			switch k {
			case "AWS_ACCESS_KEY_ID":
				return "AKIAIOSFODNN7EXAMPLE"
			case "AWS_SECRET_ACCESS_KEY":
				return "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
			case "AWS_SESSION_TOKEN":
				return "fake-session-token"
			default:
				return ""
			}
		}
		provider, err := storage.ResolveRuntimeCredentials(lookup, nil)
		if err != nil {
			t.Fatalf("unexpected error resolving static credentials: %v", err)
		}
		creds, err := provider.Retrieve(context.Background())
		if err != nil {
			t.Fatalf("unexpected error retrieving static credentials: %v", err)
		}
		if creds.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" || creds.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" || creds.SessionToken != "fake-session-token" {
			t.Errorf("unexpected credentials retrieved: %+v", creds)
		}
	})

	t.Run("valid_relative_uri_resolves_ecs_provider", func(t *testing.T) {
		t.Parallel()
		lookup := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI" {
				return "/v2/credentials/fake-role"
			}
			return ""
		}
		provider, err := storage.ResolveRuntimeCredentials(lookup, nil)
		if err != nil {
			t.Fatalf("unexpected error resolving relative URI: %v", err)
		}
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
	})

	t.Run("malformed_relative_uris_rejected", func(t *testing.T) {
		t.Parallel()
		badURIs := []string{"no-leading-slash", "/../traversal", "http://attacker.com/creds"}
		for _, bad := range badURIs {
			lookup := func(k string) string {
				if k == "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI" {
					return bad
				}
				return ""
			}
			_, err := storage.ResolveRuntimeCredentials(lookup, nil)
			if err == nil {
				t.Errorf("expected error for relative URI %q, got nil", bad)
			}
		}
	})

	t.Run("full_uri_security_restrictions", func(t *testing.T) {
		t.Parallel()
		// Plain HTTP to arbitrary host must be rejected
		lookupArbitrary := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
				return "http://attacker.com/creds"
			}
			return ""
		}
		_, err := storage.ResolveRuntimeCredentials(lookupArbitrary, nil)
		if err == nil {
			t.Fatal("expected error for plain HTTP to arbitrary host, got nil")
		}

		// Userinfo in full URI must be rejected
		lookupUserinfo := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
				return "https://user:pass@127.0.0.1/creds"
			}
			return ""
		}
		_, err = storage.ResolveRuntimeCredentials(lookupUserinfo, nil)
		if err == nil {
			t.Fatal("expected error for userinfo in full URI, got nil")
		}

		// Loopback HTTP permitted
		lookupLoopback := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
				return "http://127.0.0.1:8080/creds"
			}
			return ""
		}
		provider, err := storage.ResolveRuntimeCredentials(lookupLoopback, nil)
		if err != nil || provider == nil {
			t.Fatalf("expected loopback full URI to succeed, got %v", err)
		}

		// Link-local HTTP permitted
		lookupLinkLocal := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
				return "http://169.254.170.2/v2/credentials/fake-role"
			}
			return ""
		}
		provider, err = storage.ResolveRuntimeCredentials(lookupLinkLocal, nil)
		if err != nil || provider == nil {
			t.Fatalf("expected link-local full URI to succeed, got %v", err)
		}

		// HTTPS to remote host permitted
		lookupHTTPS := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
				return "https://creds.internal.company.com/creds"
			}
			return ""
		}
		provider, err = storage.ResolveRuntimeCredentials(lookupHTTPS, nil)
		if err != nil || provider == nil {
			t.Fatalf("expected HTTPS full URI to succeed, got %v", err)
		}
	})

	t.Run("unreadable_token_file_returns_error", func(t *testing.T) {
		t.Parallel()
		lookup := func(k string) string {
			if k == "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI" {
				return "/v2/credentials/fake-role"
			}
			if k == "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE" {
				return "/nonexistent/path/to/token.txt"
			}
			return ""
		}
		_, err := storage.ResolveRuntimeCredentials(lookup, nil)
		if err == nil {
			t.Fatal("expected error when token file is unreadable, got nil")
		}
		if !strings.Contains(err.Error(), "failed to read container authorization token file") {
			t.Errorf("expected token file read error message, got: %v", err)
		}
	})

	t.Run("unconfigured_returns_err_storage_unconfigured", func(t *testing.T) {
		t.Parallel()
		lookup := func(k string) string { return "" }
		_, err := storage.ResolveRuntimeCredentials(lookup, nil)
		if !errors.Is(err, storage.ErrStorageUnconfigured) {
			t.Errorf("expected ErrStorageUnconfigured, got: %v", err)
		}
	})
}
