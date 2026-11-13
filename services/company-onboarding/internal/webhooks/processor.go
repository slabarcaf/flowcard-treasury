package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

// PlatformAccountClient defines the interface for fetching fresh authoritative account details.
type PlatformAccountClient interface {
	GetAccount(ctx context.Context, platformAccountID string) (*airwallex.AccountDetailsResponse, error)
}

// ProcessorRepository defines the database operations required for webhook processing.
type ProcessorRepository interface {
	ClaimPendingWebhooks(ctx context.Context, limit int, leaseDuration time.Duration) ([]postgres.WebhookInboxItem, error)
	MarkWebhookProcessed(ctx context.Context, eventID, leaseToken string, status postgres.WebhookInboxStatus, lastError string) error
	GetApplicationByPlatformAccountID(ctx context.Context, platformAccountID string) (*domain.OnboardingApplication, error)
	GetPlatformAccountByExternalID(ctx context.Context, externalAccountID string) (*domain.PlatformAccount, error)
	GetPlatformAccountByExternalIDAndEnv(ctx context.Context, externalAccountID, platformName, environment string) (*domain.PlatformAccount, error)
	ApplyWebhookObservation(ctx context.Context, params postgres.ApplyWebhookObservationParams) error
	ListTrackedPlatformAccounts(ctx context.Context, limit int) ([]domain.PlatformAccount, error)
	RecordReconciliationOutcome(ctx context.Context, paID string, nextCheckIn time.Duration) error
}

// Processor processes durable webhook events from onboarding.webhook_inbox.
type Processor struct {
	repo           ProcessorRepository
	platformClient PlatformAccountClient
	batchLimit     int
	leaseDuration  time.Duration
}

// NewProcessor constructs a Webhook Processor.
func NewProcessor(repo ProcessorRepository, platformClient PlatformAccountClient) *Processor {
	return &Processor{
		repo:           repo,
		platformClient: platformClient,
		batchLimit:     10,
		leaseDuration:  30 * time.Second,
	}
}

// ProcessPending claims and processes a batch of pending webhook events.
func (p *Processor) ProcessPending(ctx context.Context) (int, error) {
	items, err := p.repo.ClaimPendingWebhooks(ctx, p.batchLimit, p.leaseDuration)
	if err != nil {
		return 0, fmt.Errorf("failed to claim pending webhooks: %w", err)
	}

	processedCount := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return processedCount, err
		}

		err := p.processItem(ctx, item)
		if err != nil {
			// Transient processing failure: mark failed with unexpired lease check
			_ = p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusFailed, err.Error())
		}
		processedCount++
	}

	return processedCount, nil
}

func (p *Processor) processItem(ctx context.Context, item postgres.WebhookInboxItem) error {
	accountID := strings.TrimSpace(item.AccountID)
	if accountID == "" {
		// Attempt extraction from raw payload
		var payload InboundWebhookPayload
		if err := json.Unmarshal(item.RawPayload, &payload); err == nil {
			accountID = payload.ExtractAccountID()
		}
	}

	if accountID == "" {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, "no platform account id in payload")
	}

	// Default must fail closed if platform client is missing
	if p.platformClient == nil {
		_ = p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusFailed, "platform client is required; failing closed")
		return errors.New("platform client is required; failing closed")
	}

	// Look up platform account scoped by platform and environment
	pa, err := p.repo.GetPlatformAccountByExternalIDAndEnv(ctx, accountID, "AIRWALLEX", item.Environment)
	if err != nil {
		if errors.Is(err, postgres.ErrPlatformAccountNotFound) {
			return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("unrecognized platform account %q in %s: no matching platform account", accountID, item.Environment))
		}
		return fmt.Errorf("failed to look up platform account for %s: %w", accountID, err)
	}

	// Cross-environment isolation check
	if item.Environment != "" && pa.Environment() != "" && !strings.EqualFold(item.Environment, pa.Environment()) {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("cross-environment event mismatch: event=%s pa=%s", item.Environment, pa.Environment()))
	}

	eventType := strings.ToLower(strings.TrimSpace(item.EventType))

	switch eventType {
	case "account.active":
		return p.handleAccountActive(ctx, item, pa, accountID)

	case "account.suspended":
		return p.handleAccountSuspended(ctx, item, pa, accountID)

	case "account.action_required", "rfi.created", "rfi.updated":
		// Guard: Do not blindly map action_required to RFI. Quarantined until verified RFI questions can be correlated.
		return p.handleActionRequired(ctx, item)

	case "account.created", "account.updated", "account.submitted":
		// Observed informative lifecycle events; no state regression
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusProcessed, "")

	default:
		// Unknown event type quarantined
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("unsupported event type %q", item.EventType))
	}
}

func (p *Processor) handleAccountActive(ctx context.Context, item postgres.WebhookInboxItem, pa *domain.PlatformAccount, accountID string) error {
	// Mandatory fail-closed on nil platform client
	if p.platformClient == nil {
		return errors.New("platform client is required; failing closed")
	}

	// Reserve expected revision and observation sequence timestamp BEFORE HTTP read!
	expectedRev := pa.Revision()
	observedTime := time.Now().UTC()

	// Fresh authoritative read
	details, err := p.platformClient.GetAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("authoritative platform read failed for account %s: %w", accountID, err)
	}

	// Returned details.ID must not be empty and must match target account ID exactly
	if strings.TrimSpace(details.ID) == "" {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, "authoritative account ID is empty")
	}
	if details.ID != accountID {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("authoritative account ID mismatch: expected %q, got %q", accountID, details.ID))
	}

	// If platform fresh read indicates SUSPENDED, never complete blindly; handle suspension instead and quarantine contradictory active event
	if strings.EqualFold(details.Status, "SUSPENDED") {
		return p.repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
			EventID:            item.EventID,
			LeaseToken:         item.LeaseToken,
			AccountID:          accountID,
			ExternalAccountID:  accountID,
			PlatformName:       "AIRWALLEX",
			Environment:        item.Environment,
			ExpectedPARevision: expectedRev,
			ObservedStatus:     domain.PlatformAccountStatusSuspended,
			SyncTime:           observedTime,
			InboxStatus:        postgres.WebhookStatusQuarantined,
			InboxError:         "authoritative account status is SUSPENDED; quarantined contradictory active event",
		})
	}

	// Strictly require authoritative ACTIVE status
	if !strings.EqualFold(details.Status, "ACTIVE") {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("authoritative account status %q is not ACTIVE", details.Status))
	}

	// Apply authoritative ACTIVE observation atomically across PlatformAccount, Application, and WebhookInbox
	return p.repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
		EventID:            item.EventID,
		LeaseToken:         item.LeaseToken,
		AccountID:          accountID,
		ExternalAccountID:  accountID,
		PlatformName:       "AIRWALLEX",
		Environment:        item.Environment,
		ExpectedPARevision: expectedRev,
		ObservedStatus:     domain.PlatformAccountStatusActive,
		SyncTime:           observedTime,
		InboxStatus:        postgres.WebhookStatusProcessed,
		InboxError:         "",
	})
}

func (p *Processor) handleAccountSuspended(ctx context.Context, item postgres.WebhookInboxItem, pa *domain.PlatformAccount, accountID string) error {
	// Mandatory fail-closed on nil platform client
	if p.platformClient == nil {
		return errors.New("platform client is required; failing closed")
	}

	// Reserve expected revision and observation sequence timestamp BEFORE HTTP read!
	expectedRev := pa.Revision()
	observedTime := time.Now().UTC()

	// Fresh authoritative read
	details, err := p.platformClient.GetAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("authoritative platform read failed for account %s: %w", accountID, err)
	}

	// Returned details.ID must not be empty and must match target account ID
	if strings.TrimSpace(details.ID) == "" {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, "authoritative account ID is empty")
	}
	if details.ID != accountID {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, fmt.Sprintf("authoritative account ID mismatch: expected %q, got %q", accountID, details.ID))
	}

	// If fresh read indicates ACTIVE, do not suspend on stale suspended event!
	if strings.EqualFold(details.Status, "ACTIVE") {
		return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, "stale account.suspended event: authoritative account status is ACTIVE")
	}

	// Apply authoritative SUSPENDED observation atomically across PlatformAccount, Application, and WebhookInbox
	return p.repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
		EventID:            item.EventID,
		LeaseToken:         item.LeaseToken,
		AccountID:          accountID,
		ExternalAccountID:  accountID,
		PlatformName:       "AIRWALLEX",
		Environment:        item.Environment,
		ExpectedPARevision: expectedRev,
		ObservedStatus:     domain.PlatformAccountStatusSuspended,
		SyncTime:           observedTime,
		InboxStatus:        postgres.WebhookStatusProcessed,
		InboxError:         "",
	})
}

func (p *Processor) handleActionRequired(ctx context.Context, item postgres.WebhookInboxItem) error {
	// Guard: Do NOT blindly map action_required to an RFI nor guess questions.
	// Per specification: "RFI type/account correlation, unknown mapping remains quarantined, no name match."
	// Quarantine unverified or uncorrelatable RFI events.
	return p.repo.MarkWebhookProcessed(ctx, item.EventID, item.LeaseToken, postgres.WebhookStatusQuarantined, "action_required requires authoritative verified RFI correlation; quarantined to prevent ungrounded status change")
}

// ReconcileAccounts enumerates tracked non-terminal accounts and reconciles authoritative status.
func (p *Processor) ReconcileAccounts(ctx context.Context) (int, error) {
	if p.platformClient == nil {
		return 0, errors.New("platform client is required for account reconciliation")
	}

	accounts, err := p.repo.ListTrackedPlatformAccounts(ctx, p.batchLimit)
	if err != nil {
		return 0, fmt.Errorf("failed to list tracked platform accounts: %w", err)
	}

	reconciledCount := 0
	for _, pa := range accounts {
		if err := ctx.Err(); err != nil {
			return reconciledCount, err
		}

		extID := pa.ExternalAccountID()
		if extID == "" {
			continue
		}

		// Reserve per-account expected revision and observation timestamp BEFORE HTTP call!
		expectedRev := pa.Revision()
		observedTime := time.Now().UTC()

		details, err := p.platformClient.GetAccount(ctx, extID)
		if err != nil {
			// Failing read: back off next check to avoid starving other accounts beyond batch size!
			_ = p.repo.RecordReconciliationOutcome(ctx, pa.ID(), 30*time.Second)
			continue
		}

		if strings.TrimSpace(details.ID) == "" || details.ID != extID {
			_ = p.repo.RecordReconciliationOutcome(ctx, pa.ID(), 30*time.Second)
			continue
		}

		var targetStatus domain.PlatformAccountStatus
		switch strings.ToUpper(strings.TrimSpace(details.Status)) {
		case "ACTIVE":
			targetStatus = domain.PlatformAccountStatusActive
		case "SUSPENDED":
			targetStatus = domain.PlatformAccountStatusSuspended
		case "CLOSED":
			targetStatus = domain.PlatformAccountStatusClosed
		case "PENDING":
			targetStatus = domain.PlatformAccountStatusPending
		case "SUBMITTED":
			targetStatus = domain.PlatformAccountStatusSubmitted
		default:
			targetStatus = domain.PlatformAccountStatus(details.Status)
		}

		err = p.repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
			InternalAccountID:  pa.ID(),
			ExternalAccountID:  extID,
			PlatformName:       pa.PlatformName(),
			Environment:        pa.Environment(),
			ExpectedPARevision: expectedRev,
			ObservedStatus:     targetStatus,
			SyncTime:           observedTime,
		})
		if err != nil {
			if errors.Is(err, postgres.ErrOptimisticLockConflict) {
				// Concurrently modified: back off shortly to re-read fresh later without overwriting
				_ = p.repo.RecordReconciliationOutcome(ctx, pa.ID(), 5*time.Second)
			} else {
				_ = p.repo.RecordReconciliationOutcome(ctx, pa.ID(), 30*time.Second)
			}
			continue
		}

		// Advance next check on successful reconciliation
		_ = p.repo.RecordReconciliationOutcome(ctx, pa.ID(), 5*time.Minute)
		reconciledCount++
	}

	return reconciledCount, nil
}
