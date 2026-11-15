// Package discovery coordinates bounded polling of shortfall and servicing feeds.
package discovery

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
)

const staleAfter = 60 * time.Second
const fullScanInterval = 5 * time.Second

type Poller struct {
	Repo            *postgres.Repository
	Shortfall       *shortfall.Client
	Servicing       *servicing.Client
	BindingID, Mode string
	Now             func() time.Time
	servicingMore   bool
	preferServicing bool
}
type PollResult struct {
	DidWork bool
	Err     error
}
type FeedHealth struct {
	ShortfallStale             bool
	ServicingStale             bool
	ShortfallHeld              bool
	ServicingHeld              bool
	DiscoveryDegraded          bool
	ShortfallAge, ServicingAge time.Duration
	OldestUnfinishedScanAge    time.Duration
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
func (p *Poller) Step(ctx context.Context) PollResult {
	now := p.now()
	state, err := p.Repo.ReadDiscoveryState(ctx, p.BindingID)
	if err != nil {
		return PollResult{Err: err}
	}
	servicingDue := p.Servicing != nil && state.ServicingHoldReason == nil && (state.ServicingRetryAt == nil || !now.Before(*state.ServicingRetryAt))
	if servicingDue {
		age, never, e := p.Repo.ServicingAge(ctx, p.BindingID, now)
		if e != nil {
			return PollResult{Err: e}
		}
		servicingDue = p.servicingMore || never || age >= 5*time.Second
	}
	shortfallDue := p.Shortfall != nil && state.ShortfallHoldReason == nil && (state.ShortfallRetryAt == nil || !now.Before(*state.ShortfallRetryAt)) && !(state.Cursor == nil && state.ScanCompletedAt != nil && now.Sub(*state.ScanCompletedAt) < fullScanInterval)
	if servicingDue && shortfallDue {
		if p.preferServicing {
			p.preferServicing = false
			return p.pollServicing(ctx, state, now)
		}
		p.preferServicing = true
		return p.pollShortfall(ctx, state, now)
	}
	if servicingDue {
		return p.pollServicing(ctx, state, now)
	}
	if shortfallDue {
		return p.pollShortfall(ctx, state, now)
	}
	return PollResult{}
}
func (p *Poller) pollServicing(ctx context.Context, state postgres.DiscoveryState, now time.Time) PollResult {
	page, _, err := p.Servicing.Changes(ctx, state.ServicingSequence)
	if err != nil {
		return p.failed(ctx, "servicing", state.ServicingFailedAttempts, err, now)
	}
	changes := make([]postgres.ServicingChange, 0, len(page.Items))
	for _, v := range page.Items {
		changes = append(changes, postgres.ServicingChange{Sequence: v.Sequence, Mode: page.Mode, ResourceType: v.ResourceType, ResourceID: v.ResourceID, ResourceVersion: v.ResourceVersion, BridgeID: v.BridgeID, BridgeVersion: v.BridgeVersion, CustomerID: v.CustomerID, AccountRef: v.AccountRef, OriginatingCaseID: v.OriginatingCaseID, EvaluationID: v.EvaluationID, OfferID: v.OfferID, ChangeType: v.ChangeType, RecordedAt: v.RecordedAt, Summary: v.Summary})
	}
	if err := p.Repo.SaveServicingPage(ctx, p.BindingID, page.Mode, state.ServicingSequence, page.NextAfterSeq, changes); err != nil {
		return PollResult{Err: err}
	}
	p.servicingMore = page.HasMore
	return PollResult{DidWork: true}
}
func (p *Poller) pollShortfall(ctx context.Context, state postgres.DiscoveryState, now time.Time) PollResult {
	page, status, err := p.Shortfall.List(ctx, state.Cursor)
	if err != nil {
		if status == 400 && state.Cursor != nil {
			if e := p.Repo.RestartShortfallScan(ctx, p.BindingID, state.Cursor, now); e == nil {
				return PollResult{DidWork: true}
			}
		}
		return p.failed(ctx, "shortfall", state.ShortfallFailedAttempts, err, now)
	}
	cases, err := p.makeCases(page.Items)
	if err != nil {
		return p.hold(ctx, "shortfall", "SHORTFALL_CONTRACT_INVALID", err)
	}
	var greatest *time.Time
	var greatestID *string
	for i := range page.Items {
		record := page.Items[i]
		if greatest == nil || record.CreatedAt.After(*greatest) || record.CreatedAt.Equal(*greatest) && record.ID > *greatestID {
			t := record.CreatedAt
			id := record.ID
			greatest = &t
			greatestID = &id
		}
	}
	_, err = p.Repo.SaveShortfallPage(ctx, postgres.DiscoveryPage{BindingID: p.BindingID, ExpectedCursor: state.Cursor, NextCursor: page.NextCursor, StartedAt: now, SucceededAt: now, GreatestCreatedAt: greatest, GreatestDetectionID: greatestID, Cases: cases})
	if err != nil {
		return PollResult{Err: err}
	}
	return PollResult{DidWork: true}
}
func (p *Poller) makeCases(records []shortfall.Record) ([]postgres.DiscoveryCase, error) {
	cases := make([]postgres.DiscoveryCase, 0, len(records))
	for _, r := range records {
		status, stage := "NEW", "LOAD_DETAIL"
		caseID, err := domain.StableCaseID(p.BindingID, domain.CaseShortfall, r.ID)
		if err != nil {
			return nil, err
		}
		customer := r.CustomerID
		rawAccountID := r.AccountID
		var accountRef *string
		if customer != nil && strings.TrimSpace(*customer) != "" {
			alias := domain.InternalAccountRef(*customer)
			accountRef = &alias
		}
		var reason *string
		if hold := shortfall.HoldReason(r); hold != "" {
			status, stage = "HELD", "HELD"
			reason = &hold
		}
		cases = append(cases, postgres.DiscoveryCase{CaseID: caseID, DetectionID: r.ID, CreatedAt: r.CreatedAt, CustomerID: customer, AccountRef: accountRef, RawAccountID: &rawAccountID, Status: status, Stage: stage, HeldReason: reason})
	}
	return cases, nil
}
func (p *Poller) failed(ctx context.Context, feed string, attempt int, cause error, now time.Time) PollResult {
	if permanentSourceError(cause) {
		reason := sourceErrorCode(cause)
		if err := p.Repo.HoldFeed(ctx, p.BindingID, feed, reason); err != nil {
			return PollResult{Err: err}
		}
		return PollResult{Err: cause}
	}
	delay := retryDelay(attempt)
	var upstream interface{ RetryAfter() time.Duration }
	if errors.As(cause, &upstream) && upstream.RetryAfter() > delay {
		delay = upstream.RetryAfter()
	}
	retryAt := now.Add(delay)
	if err := p.Repo.RecordFeedFailure(ctx, p.BindingID, feed, retryAt); err != nil {
		return PollResult{Err: err}
	}
	return PollResult{Err: cause}
}
func (p *Poller) hold(ctx context.Context, feed, reason string, cause error) PollResult {
	if err := p.Repo.HoldFeed(ctx, p.BindingID, feed, reason); err != nil {
		return PollResult{Err: err}
	}
	return PollResult{Err: cause}
}
func retryDelay(attempt int) time.Duration {
	base := 5 * time.Second
	if attempt > 0 {
		base = 20 * time.Second
	}
	if attempt > 1 {
		base = time.Minute
	}
	return base + time.Duration(rand.Int64N(int64(base/5+1)))
}
func permanentSourceError(err error) bool {
	var protocol interface{ Permanent() bool }
	if errors.As(err, &protocol) && protocol.Permanent() {
		return true
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		code := status.HTTPStatus()
		return code >= 400 && code < 500 && code != 429
	}
	return false
}
func sourceErrorCode(err error) string {
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		switch status.HTTPStatus() {
		case 401:
			return "UPSTREAM_UNAUTHORIZED"
		case 403:
			return "UPSTREAM_FORBIDDEN"
		case 410:
			return "FEED_RETENTION_GAP"
		case 409:
			return "UPSTREAM_VERSION_CONFLICT"
		default:
			return "UPSTREAM_CONTRACT_STATUS"
		}
	}
	return "UPSTREAM_CONTRACT_INVALID"
}
func (p *Poller) Health(ctx context.Context) (FeedHealth, error) {
	now := p.now()
	s, err := p.Repo.ReadDiscoveryState(ctx, p.BindingID)
	if err != nil {
		return FeedHealth{}, err
	}
	h := FeedHealth{ShortfallHeld: s.ShortfallHoldReason != nil, ServicingHeld: s.ServicingHoldReason != nil}
	if s.LastPageSucceededAt == nil {
		h.ShortfallStale = true
	} else {
		h.ShortfallAge = now.Sub(*s.LastPageSucceededAt)
		h.ShortfallStale = h.ShortfallAge > staleAfter
	}
	if s.ScanStartedAt != nil {
		h.OldestUnfinishedScanAge = now.Sub(*s.ScanStartedAt)
	}
	h.DiscoveryDegraded = h.ShortfallStale || h.ShortfallHeld
	age, never, err := p.Repo.ServicingAge(ctx, p.BindingID, now)
	if err != nil {
		return FeedHealth{}, err
	}
	h.ServicingAge = age
	h.ServicingStale = never || age > staleAfter
	return h, nil
}
