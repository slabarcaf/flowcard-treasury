// Package workflow runs the coordinator's deterministic, serial scheduler.
package workflow

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/discovery"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

const workLimit = 45 * time.Second
const leaseLength = 60 * time.Second
const stepLimit = 50 * time.Second

type Scheduler struct {
	Repo      *postgres.Repository
	Poller    *discovery.Poller
	Executor  Executor
	BindingID string
	Mode      string
	Now       func() time.Time
	feedTurn  bool
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s.Now == nil {
		s.Now = time.Now
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		stepCtx, cancel := context.WithTimeout(ctx, stepLimit)
		did, err := s.Step(stepCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("scheduler step failed: %v", err)
		}
		delay := time.Second
		if !did {
			delay = 200 * time.Millisecond
		}
		timer.Reset(delay)
	}
}

func (s *Scheduler) Step(ctx context.Context) (bool, error) {
	if s.Poller == nil {
		return s.stepCase(ctx)
	}
	if s.feedTurn {
		s.feedTurn = false
		result := s.Poller.Step(ctx)
		if result.Err != nil {
			return result.DidWork, result.Err
		}
		if result.DidWork {
			return true, nil
		}
	}
	if did, err := s.stepCase(ctx); did || err != nil {
		s.feedTurn = true
		return did, err
	}
	result := s.Poller.Step(ctx)
	s.feedTurn = true
	if result.Err != nil {
		return result.DidWork, result.Err
	}
	return result.DidWork, nil
}

func (s *Scheduler) stepCase(ctx context.Context) (bool, error) {
	if s.BindingID == "" || (s.Mode != "DEMO" && s.Mode != "SANDBOX") {
		return false, errors.New("scheduler_scope_invalid")
	}
	caseRow, err := s.Repo.ClaimNext(ctx, leaseLength, s.BindingID, s.Mode)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.runClaimed(ctx, caseRow)
}

func (s *Scheduler) runClaimed(ctx context.Context, c domain.Case) (bool, error) {
	if s.Executor == nil {
		reason := "SCHEDULER_EXECUTOR_UNAVAILABLE"
		err := s.Repo.DisposeCase(ctx, c.ID, *c.LeaseToken, "HELD", leaseStage(c), nil, &reason)
		return true, err
	}
	workCtx, cancel := context.WithTimeout(ctx, workLimit)
	defer cancel()
	result, err := s.Executor.Execute(workCtx, c, *c.LeaseToken)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return true, s.Repo.ReleaseInterrupted(persistCtx, c.ID, *c.LeaseToken)
		}
		if isPermanent(err) {
			reason := safeErrorCode(err)
			return true, s.Repo.DisposeCase(ctx, c.ID, *c.LeaseToken, "HELD", leaseStage(c), nil, &reason)
		}
		delay := retryDelay(c.TransientFailures)
		var upstream interface{ RetryAfter() time.Duration }
		if errors.As(err, &upstream) && upstream.RetryAfter() > delay {
			delay = upstream.RetryAfter()
		}
		retryAt := s.Now().Add(delay)
		reason := safeErrorCode(err)
		_, retryErr := s.Repo.RecordCaseFailure(ctx, c.ID, *c.LeaseToken, retryAt, reason, 3)
		return true, retryErr
	}
	if result.DispositionCommitted {
		return true, nil
	}
	return true, s.Repo.DisposeCase(ctx, c.ID, *c.LeaseToken, result.Status, result.NextStage, result.RetryAt, result.HeldReason)
}

func retryDelay(attempt int) time.Duration {
	var base time.Duration
	if attempt > 0 {
		base = 20 * time.Second
	} else {
		base = 5 * time.Second
	}
	return base + time.Duration(rand.Int64N(int64(base/5+1)))
}
func isPermanent(err error) bool {
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return true
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		code := status.HTTPStatus()
		return code >= 400 && code < 500 && code != 429
	}
	return false
}
func safeErrorCode(err error) string {
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		switch status.HTTPStatus() {
		case 401:
			return "UPSTREAM_UNAUTHORIZED"
		case 403:
			return "UPSTREAM_FORBIDDEN"
		case 404:
			return "UPSTREAM_NOT_FOUND"
		default:
			return "UPSTREAM_CONTRACT_REJECTED"
		}
	}
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return "UPSTREAM_CONTRACT_INVALID"
	}
	switch {
	case errors.Is(err, domain.ErrLeaseLost):
		return "LEASE_LOST"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEPENDENCY_TIMEOUT"
	default:
		return "DEPENDENCY_UNAVAILABLE"
	}
}
