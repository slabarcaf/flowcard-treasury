package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
)

// Repository defines the worker database access interface.
type Repository interface {
	ClaimOutboxMessage(ctx context.Context, leaseDuration time.Duration) (*postgres.OutboxMessage, error)
	CompleteOutboxMessage(ctx context.Context, id, leaseToken string) error
	ReleaseOutboxMessageForRetry(ctx context.Context, id, leaseToken string, backoff time.Duration) error
	SavePlatformAccountIDWithLease(ctx context.Context, appID string, platformAccountID string, outboxID string, leaseToken string) error
	SavePlatformAccountWithLease(ctx context.Context, params postgres.SavePlatformAccountParams) (string, error)
	RecordWorkerSubmissionOutcomeWithLease(ctx context.Context, params postgres.WorkerOutcomeParams) error
	GetApplication(ctx context.Context, id string) (*domain.OnboardingApplication, error)
	GetPlatformAccount(ctx context.Context, id string) (*domain.PlatformAccount, error)
	GetPlatformAccountByCompany(ctx context.Context, companyID, platform, env string) (*domain.PlatformAccount, error)
	GetDispatchStepState(ctx context.Context, submissionID, stepName string) (postgres.DispatchStepState, string, error)
	StartDispatchStepWithLease(ctx context.Context, submissionID, stepName, outboxID, leaseToken string) (bool, error)
	RecordDispatchStepStateWithLease(ctx context.Context, submissionID, stepName string, state postgres.DispatchStepState, externalID string, outboxID, leaseToken string) error
	GetDocumentUploads(ctx context.Context, submissionID string) (map[string]string, error)
	RecordDocumentUploadWithLease(ctx context.Context, submissionID, documentID, platformFileID, outboxID, leaseToken string) error
	ClaimPlatformCreationCoordinationWithLease(ctx context.Context, companyID, platformName, environment, submissionID, outboxID, leaseToken string) (*postgres.PlatformCreationCoordinator, error)
	ConfirmPlatformCreationCoordinationWithLease(ctx context.Context, companyID, platformName, environment, externalAccountID, submissionID, outboxID, leaseToken string) error
	MarkPlatformCreationCoordinationUnresolvedWithLease(ctx context.Context, companyID, platformName, environment, submissionID, outboxID, leaseToken string) error
}

// Config holds runtime configuration for the background worker.
type Config struct {
	LeaseDuration              time.Duration
	RetryBackoff               time.Duration
	AllowRFIResponseAcceptance bool // Default false: transport 200/201/204 is not proof of acceptance until provider response contract verified
}

// Worker executes asynchronous background jobs claimed from the outbox table.
type Worker struct {
	repo     Repository
	storage  storage.ObjectStore
	platform airwallex.PlatformClient
	cfg      Config
}

// NewWorker initializes a background worker with injected dependencies.
func NewWorker(repo Repository, store storage.ObjectStore, platform airwallex.PlatformClient, cfg Config) *Worker {
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 5 * time.Second
	}
	return &Worker{
		repo:     repo,
		storage:  store,
		platform: platform,
		cfg:      cfg,
	}
}

// ProcessNextJob claims and processes a single outbox message.
// Returns true if a job was found and processed, false if no pending work was available.
func (w *Worker) ProcessNextJob(ctx context.Context) (bool, error) {
	msg, err := w.repo.ClaimOutboxMessage(ctx, w.cfg.LeaseDuration)
	if err != nil {
		return false, fmt.Errorf("failed to claim outbox message: %w", err)
	}
	if msg == nil {
		return false, nil // No jobs waiting
	}

	switch msg.Topic {
	case "onboarding.submission.v1":
		err = w.ProcessSubmissionJob(ctx, msg)
	default:
		// Unknown topic: mark complete to avoid poisoning the queue
		_ = w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken)
		return true, fmt.Errorf("unknown outbox topic %q", msg.Topic)
	}

	return true, err
}
