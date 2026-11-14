package worker

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
)

// Store owns atomic claims, immutable observation lookup, and fenced dispositions.
type Store interface {
	Claim(context.Context, string, string) (*domain.Job, error)
	FindObservation(context.Context, string, string, string) (*domain.StoredObservation, error)
	Complete(context.Context, domain.Job, *domain.Observation, string) (string, error)
	RecordFailure(context.Context, domain.Job, domain.Failure, time.Duration) (domain.InboxState, error)
}

// Worker processes one item at a time, with no provider calls inside DB transactions.
type Worker struct {
	store       Store
	resolver    *provider.Resolver
	client      *provider.Client
	environment string
	source      string
}

// New creates a serial worker. Resolver injection is a local Go dependency;
// runtime constructs UnsupportedReferences, never a request/env-selected bypass.
func New(store Store, resolver *provider.Resolver, client *provider.Client, environment, source string) (*Worker, error) {
	environment, source = strings.TrimSpace(environment), strings.TrimSpace(source)
	if store == nil || environment == "" || source == "" {
		return nil, errors.New("worker_configuration_invalid")
	}
	if resolver == nil {
		resolver = &provider.Resolver{References: provider.UnsupportedReferences{}}
	}
	return &Worker{store: store, resolver: resolver, client: client, environment: environment, source: source}, nil
}

// Run polls every second without spawning jobs or renewing their leases.
// Shutdown leaves unfinished work recoverable after lease expiry.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := w.ProcessNext(ctx); err != nil && ctx.Err() == nil {
				// Do not log arbitrary errors from dependency boundaries.
				log.Print("shortfall worker iteration_unavailable")
			}
		}
	}
}

// ProcessNext handles one committed claim. The bool reports whether an item was
// claimed, not whether it completed. Unresolved evidence never completes a result.
func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	claimCtx, cancelClaim := context.WithTimeout(ctx, 5*time.Second)
	job, err := w.store.Claim(claimCtx, w.environment, w.source)
	cancelClaim()
	if err != nil || job == nil {
		return false, err
	}
	started := time.Now()
	jobCtx, cancelJob := context.WithTimeout(ctx, 45*time.Second)
	defer cancelJob()
	// Reserve the last five seconds within the overall deadline for durable
	// failure/completion recording after slow lookups exhaust their work budget.
	workCtx, cancelWork := context.WithTimeout(jobCtx, 40*time.Second)
	observation, processErr := w.evaluate(workCtx, *job)
	cancelWork()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	finishCtx, cancelFinish := context.WithTimeout(jobCtx, 5*time.Second)
	defer cancelFinish()
	if processErr != nil {
		return true, w.recordFailure(finishCtx, *job, processErr, started)
	}
	reason := observation.Reason
	// Confirmed unrelated/noncanonical ignores remain inbox-only, preserving bytes.
	if observation.Facts.CanonicalEventID == "" {
		observation = nil
	}
	detectionID, err := w.store.Complete(finishCtx, *job, observation, reason)
	if err != nil {
		var failure *domain.Failure
		if errors.As(err, &failure) {
			// If commit was ambiguous but succeeded, this write loses its lease
			// fence. If storage is still unavailable, expiry provides recovery.
			retryCtx, cancel := context.WithTimeout(jobCtx, 5*time.Second)
			defer cancel()
			return true, w.recordFailure(retryCtx, *job, err, started)
		}
		return true, err
	}
	log.Printf("shortfall inbox=%s state=PROCESSED detection=%s duration_ms=%d", job.ID, detectionID, time.Since(started).Milliseconds())
	return true, nil
}

func (w *Worker) recordFailure(ctx context.Context, job domain.Job, err error, started time.Time) error {
	failure := failureFor(err)
	delay := retryDelay(job.FailedAttempts+1, failure.RetryAfter, rand.Float64())
	state, err := w.store.RecordFailure(ctx, job, failure, delay)
	if err != nil {
		return err
	}
	reason := failure.Reason
	if failure.Retryable && job.FailedAttempts+1 >= 8 {
		reason = "RETRIES_EXHAUSTED:" + failure.Reason
	}
	log.Printf("shortfall inbox=%s state=%s reason=%s duration_ms=%d", job.ID, state, reason, time.Since(started).Milliseconds())
	return nil
}

func (w *Worker) evaluate(ctx context.Context, job domain.Job) (*domain.Observation, error) {
	var attempt *provider.Attempt
	var reader provider.EventReader
	if w.client != nil {
		attempt = w.client.BeginAttempt()
		reader = attempt
	}
	facts, resolveErr := w.resolver.Resolve(ctx, job.Delivery, reader)
	// Resolved identity is sufficient to reuse a final stored observation, even
	// if newly retrieved optional evidence is unavailable. Never read a new balance.
	if facts.Mapping.AccountID != "" && facts.CanonicalEventID != "" {
		stored, err := w.store.FindObservation(ctx, job.Delivery.Environment, facts.Mapping.AccountID, facts.CanonicalEventID)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			if stored.Observation.Outcome == domain.Unresolved {
				return nil, &domain.Failure{Reason: "OBSERVATION_NOT_FINAL"}
			}
			return &stored.Observation, nil
		}
	}
	if resolveErr != nil {
		failure := failureFor(resolveErr)
		if failure.Ignored && !failure.Retryable {
			return &domain.Observation{Facts: facts, Outcome: domain.Ignored, Reason: failure.Reason}, nil
		}
		return nil, &failure
	}
	observation, eligible := domain.Prepare(facts)
	if !eligible {
		if observation.Outcome == domain.Ignored {
			return &observation, nil
		}
		return nil, &domain.Failure{Reason: observation.Reason}
	}
	if attempt == nil {
		return nil, &domain.Failure{Reason: "PROVIDER_UNCONFIGURED"}
	}
	balance, err := attempt.Balance(ctx, facts.Mapping.AccountID)
	if err != nil {
		return nil, err
	}
	observation = domain.Observe(facts, balance.AvailableCents, balance.RequestedAt, balance.ObservedAt)
	if observation.Outcome == domain.Unresolved {
		return nil, &domain.Failure{Reason: observation.Reason}
	}
	return &observation, nil
}

func failureFor(err error) domain.Failure {
	failure := domain.Failure{Reason: "PROCESSING_FAILED"}
	var known *domain.Failure
	if errors.As(err, &known) {
		failure = *known
	} else if errors.Is(err, context.DeadlineExceeded) {
		failure = domain.Failure{Reason: "PROCESSING_TIMEOUT", Retryable: true}
	}
	if failure.Reason == "" || len(failure.Reason) > 80 {
		failure.Reason = "PROCESSING_FAILED"
	}
	for _, ch := range failure.Reason {
		if !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '_' {
			failure.Reason = "PROCESSING_FAILED"
			break
		}
	}
	return failure
}

func retryDelay(failedAttempt int, minimum time.Duration, jitter float64) time.Duration {
	delays := [...]time.Duration{5, 10, 20, 40, 80, 160, 300}
	index := min(max(failedAttempt-1, 0), len(delays)-1)
	delay := time.Duration(float64(delays[index]*time.Second) * (0.8 + 0.4*jitter))
	if minimum > delay {
		delay = minimum
	}
	return delay
}
