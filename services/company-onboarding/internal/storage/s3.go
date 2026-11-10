package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Config defines configuration parameters for the S3-compatible encrypted object store.
// Injected via constructor; no environment variables are read automatically.
type S3Config struct {
	Endpoint             string
	Bucket               string
	Region               string
	AccessKeyID          string
	SecretAccessKey      string
	ServerSideEncryption string // "aws:kms" or "AES256"; required
	KMSKeyID             string // Required when ServerSideEncryption is "aws:kms"
	HTTPClient           *http.Client
}

// S3Store is an S3-compatible private object store adapter enforcing server-side encryption at-rest.
type S3Store struct {
	cfg    S3Config
	client *s3.Client
}

// NewS3Store initializes an S3Store using the official AWS SDK v2 with strict validation
// of credentials, URL security (strict HTTPS, no userinfo/path/query, redirect refusal),
// and mandatory server-side at-rest encryption. Fails closed on any misconfiguration.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	accessKey := strings.TrimSpace(cfg.AccessKeyID)
	secretKey := strings.TrimSpace(cfg.SecretAccessKey)

	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, ErrStorageUnconfigured
	}

	sse := strings.TrimSpace(cfg.ServerSideEncryption)
	if sse == "" {
		return nil, ErrMissingEncryption
	}
	if sse != "AES256" && sse != "aws:kms" {
		return nil, fmt.Errorf("%w: unsupported encryption algorithm %q, must be AES256 or aws:kms", ErrMissingEncryption, sse)
	}
	if sse == "aws:kms" && strings.TrimSpace(cfg.KMSKeyID) == "" {
		return nil, fmt.Errorf("%w: kms_key_id is required when server_side_encryption is aws:kms", ErrMissingEncryption)
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || !parsed.IsAbs() {
			return nil, fmt.Errorf("%w: invalid endpoint URL", ErrInvalidEndpoint)
		}
		if parsed.User != nil {
			return nil, fmt.Errorf("%w: endpoint must not contain userinfo/credentials", ErrInvalidEndpoint)
		}
		if parsed.Fragment != "" {
			return nil, fmt.Errorf("%w: endpoint must not contain URL fragments", ErrInvalidEndpoint)
		}
		if parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, fmt.Errorf("%w: endpoint must not contain path or query components", ErrInvalidEndpoint)
		}
		host := parsed.Hostname()
		if host == "" {
			return nil, fmt.Errorf("%w: endpoint must have a non-empty hostname", ErrInvalidEndpoint)
		}
		isLocal := host == "127.0.0.1" || host == "localhost"
		switch parsed.Scheme {
		case "https":
			// strict HTTPS is always permitted
		case "http":
			if !isLocal {
				return nil, fmt.Errorf("%w: strict HTTPS is required for object storage endpoint", ErrInsecureEndpoint)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported scheme %q, must be https (or http for local endpoints)", ErrInvalidEndpoint, parsed.Scheme)
		}
		endpoint = strings.TrimRight(endpoint, "/")
	}

	// Refuse HTTP redirects to prevent credential or document transport leaks
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("security: storage endpoint redirects are strictly prohibited")
			},
		}
	} else {
		copied := *httpClient
		copied.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return errors.New("security: storage endpoint redirects are strictly prohibited")
		}
		httpClient = &copied
	}

	opts := s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:   httpClient,
		UsePathStyle: true,
	}
	if endpoint != "" {
		opts.BaseEndpoint = aws.String(endpoint)
	}

	client := s3.New(opts)

	return &S3Store{
		cfg:    cfg,
		client: client,
	}, nil
}

// Put writes object bytes to S3 using an atomic conditional PutObject with If-None-Match: *.
// Eliminates TOCTOU overwrite race conditions and verifies server encryption response.
func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, sizeBytes int64, contentType string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
		// ATOMIC CONDITIONAL PUT: prevents TOCTOU overwrites via single atomic request
		IfNoneMatch: aws.String("*"),
	}

	if sizeBytes > 0 {
		input.ContentLength = aws.Int64(sizeBytes)
	}

	switch s.cfg.ServerSideEncryption {
	case "AES256":
		input.ServerSideEncryption = types.ServerSideEncryptionAes256
	case "aws:kms":
		input.ServerSideEncryption = types.ServerSideEncryptionAwsKms
		if s.cfg.KMSKeyID != "" {
			input.SSEKMSKeyId = aws.String(s.cfg.KMSKeyID)
		}
	default:
		return fmt.Errorf("%w: unsupported encryption algorithm %q", ErrMissingEncryption, s.cfg.ServerSideEncryption)
	}

	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			code := apiErr.ErrorCode()
			if code == "PreconditionFailed" || code == "Conflict" {
				return ErrObjectExists
			}
		}
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) {
			if respErr.HTTPStatusCode() == http.StatusPreconditionFailed || respErr.HTTPStatusCode() == http.StatusConflict {
				return ErrObjectExists
			}
		}
		return redactError("put", err)
	}

	// Verify server confirmed at-rest encryption in response (adapter capability verification)
	expectedSSE := types.ServerSideEncryption(s.cfg.ServerSideEncryption)
	if out.ServerSideEncryption != expectedSSE {
		// Compensate by deleting the unencrypted object immediately using an independent bounded context
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, delErr := s.client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.cfg.Bucket),
			Key:    aws.String(key),
		})
		if delErr != nil {
			sanitizedErr := redactError("delete", delErr)
			log.Printf("audit: failed to delete unencrypted object during encryption mismatch compensation: bucket=%s key=%s err=%v",
				s.cfg.Bucket, key, sanitizedErr)
			return fmt.Errorf("%w: server did not confirm requested encryption %s (got %s); manual remediation required: cleanup deletion failed: %v",
				ErrMissingEncryption, s.cfg.ServerSideEncryption, out.ServerSideEncryption, sanitizedErr)
		}

		return fmt.Errorf("%w: server did not confirm requested encryption %s (got %s)",
			ErrMissingEncryption, s.cfg.ServerSideEncryption, out.ServerSideEncryption)
	}

	return nil
}

// Get streams an object from S3.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error) {
	if err := ValidateKey(key); err != nil {
		return nil, ObjectMetadata{}, err
	}

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound" {
				return nil, ObjectMetadata{}, ErrObjectNotFound
			}
		}
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, redactError("get", err)
	}

	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	var ct string
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	var sse string
	if out.ServerSideEncryption != "" {
		sse = string(out.ServerSideEncryption)
	}
	var etag string
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}

	meta := ObjectMetadata{
		Key:                  key,
		SizeBytes:            size,
		ContentType:          ct,
		ServerSideEncryption: sse,
		ETag:                 etag,
	}

	return out.Body, meta, nil
}

// Delete removes an object from S3.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return redactError("delete", err)
	}
	return nil
}

// Exists checks if an object exists in S3 using HeadObject.
func (s *S3Store) Exists(ctx context.Context, key string) (bool, error) {
	if err := ValidateKey(key); err != nil {
		return false, err
	}

	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey" {
				return false, nil
			}
		}
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
			return false, nil
		}
		return false, redactError("head", err)
	}
	return true, nil
}

// redactError sanitizes and redacts upstream errors, preventing leakage of raw XML or internal endpoints.
func redactError(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("s3 %s failed: request rejected (code %s)", op, apiErr.ErrorCode())
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		return fmt.Errorf("s3 %s failed with status %d: request rejected by storage provider", op, respErr.HTTPStatusCode())
	}
	return fmt.Errorf("s3 %s failed: request rejected by storage provider", op)
}
