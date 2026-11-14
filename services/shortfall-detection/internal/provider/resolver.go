package provider

import (
	"context"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// Reference contains identifiers extracted by a version-verified adapter.
// It contains no eligibility decision, amount, balance, or calculated result.
type Reference struct {
	AccountID     string
	CardID        string
	EventID       string
	TransactionID string
	Version       string
	Provenance    string
}

// ReferenceExtractor interprets only a separately verified delivery contract.
type ReferenceExtractor interface {
	Extract(context.Context, domain.Delivery) (Reference, error)
}

// MappingResolver verifies active shared mappings before account-scoped reads.
type MappingResolver interface {
	ResolveMapping(context.Context, string, string) (domain.Mapping, error)
}

// EvidenceReader supplies trusted card funding and versioned amount conventions.
// The local harness may inject synthetic evidence; runtime has no bypass switch.
type EvidenceReader interface {
	Evidence(context.Context, domain.Mapping, string) (domain.ContractEvidence, error)
}

// EventReader retrieves documented event facts independently of reference extraction.
type EventReader interface {
	Event(context.Context, string, string) (Event, error)
	Events(context.Context, string, string) ([]Event, error)
}

// UnsupportedReferences holds modern declines until their actual contract is verified.
// It is the default live behavior, independent of configured candidate API version.
type UnsupportedReferences struct{}

// Extract never guesses provider JSON paths or aliases webhook IDs to event IDs.
func (UnsupportedReferences) Extract(_ context.Context, delivery domain.Delivery) (Reference, error) {
	switch delivery.EventName {
	case "issuing.card_transaction.declined", "issuing.card_transaction_event.failed":
		return Reference{}, &domain.Failure{Reason: "UNSUPPORTED_WEBHOOK_VERSION"}
	default:
		return Reference{}, &domain.Failure{Reason: "UNRELATED_EVENT", Ignored: true}
	}
}

// Resolver binds extracted references to trusted mappings and authoritative facts.
// Injection is a Go dependency used by the local test harness, never request data.
type Resolver struct {
	References ReferenceExtractor
	Mappings   MappingResolver
	Evidence   EvidenceReader
}

// Resolve obtains one canonical event without selecting by amount or latest time.
// On normalization/evidence errors, returned partial facts may be retained for audit.
func (r *Resolver) Resolve(ctx context.Context, delivery domain.Delivery, reader EventReader) (domain.Facts, error) {
	extractor := r.References
	if extractor == nil {
		extractor = UnsupportedReferences{}
	}
	ref, err := extractor.Extract(ctx, delivery)
	if err != nil {
		return domain.Facts{}, err
	}
	if ref.Version == "" || ref.Provenance == "" {
		return domain.Facts{}, &domain.Failure{Reason: "REFERENCE_CONTRACT_UNVERIFIED"}
	}
	if r.Mappings == nil || ref.AccountID == "" || ref.CardID == "" {
		return domain.Facts{}, &domain.Failure{Reason: "MAPPING_UNAVAILABLE"}
	}
	mapping, err := r.Mappings.ResolveMapping(ctx, ref.AccountID, ref.CardID)
	if err != nil {
		return domain.Facts{}, err
	}
	if mapping.AccountID != ref.AccountID || mapping.CardID != ref.CardID || mapping.CustomerID == "" {
		return domain.Facts{}, &domain.Failure{Reason: "MAPPING_CONFLICT"}
	}
	if reader == nil {
		return domain.Facts{}, &domain.Failure{Reason: "PROVIDER_UNCONFIGURED"}
	}
	var event Event
	var events []Event
	path := "GET /api/v1/issuing/card_transaction_events/{id}#$"
	if ref.EventID != "" {
		event, err = reader.Event(ctx, mapping.AccountID, ref.EventID)
		if err != nil {
			return domain.Facts{}, err
		}
		if event.ID != ref.EventID {
			return domain.Facts{}, &domain.Failure{Reason: "EVENT_REFERENCE_MISMATCH"}
		}
		events = []Event{event}
	} else if ref.TransactionID != "" {
		events, err = reader.Events(ctx, mapping.AccountID, ref.TransactionID)
		if err != nil {
			return domain.Facts{}, err
		}
		path = "GET /api/v1/issuing/card_transaction_events#$.items[*]"
	} else {
		return domain.Facts{}, &domain.Failure{Reason: "TRANSACTION_REFERENCE_MISSING"}
	}
	seen := make(map[string]bool)
	for _, item := range events {
		if item.ID == "" || item.CardID == "" || item.TransactionID == "" {
			return domain.Facts{}, &domain.Failure{Reason: "EVENT_IDENTITY_MISSING"}
		}
		if item.CardID != mapping.CardID || (ref.TransactionID != "" && item.TransactionID != ref.TransactionID) {
			return domain.Facts{}, &domain.Failure{Reason: "EVENT_OWNERSHIP_MISMATCH"}
		}
		if seen[item.ID] {
			return domain.Facts{}, &domain.Failure{Reason: "EVENT_IDENTITY_AMBIGUOUS"}
		}
		seen[item.ID] = true
	}
	if ref.EventID == "" {
		event, err = canonicalAuthorization(events)
		if err != nil {
			return domain.Facts{}, err
		}
	}
	facts, err := normalizedFacts(event, mapping, path, ref.Provenance)
	if err != nil {
		return facts, err
	}
	facts.OrderingReason = orderingReason(event, events)
	if r.Evidence != nil {
		facts.Contract, err = r.Evidence.Evidence(ctx, mapping, ref.Version)
		if err != nil {
			return facts, err
		}
		facts.Provenance["funding_model"] = facts.Contract.Provenance
		facts.Provenance["amount_convention"] = facts.Contract.Provenance
	}
	return facts, nil
}

func canonicalAuthorization(events []Event) (Event, error) {
	var candidates []Event
	for _, event := range events {
		if event.Type != "" && event.Type != "UNKNOWN" && event.Type != "AUTHORIZATION" &&
			event.Type != "CLEARING" && event.Type != "REVERSAL_AUTH" {
			return Event{}, &domain.Failure{Reason: "AUTHORIZATION_FACTS_UNSUPPORTED"}
		}
		possibleType := event.Type == "AUTHORIZATION" || event.Type == "" || event.Type == "UNKNOWN"
		possibleSubtype := event.Subtype == "AUTHORIZATION" || event.Subtype == "" || event.Subtype == "UNKNOWN"
		if possibleType && possibleSubtype && event.ProcessResult != "APPROVED" {
			candidates = append(candidates, event)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return Event{}, &domain.Failure{Reason: "AUTHORIZATION_AMBIGUOUS"}
	}
	if len(events) == 1 {
		return events[0], nil
	}
	if len(events) == 0 {
		return Event{}, &domain.Failure{Reason: "AUTHORIZATION_NOT_FOUND"}
	}
	return Event{}, &domain.Failure{Reason: "NO_INITIAL_DECLINED_AUTHORIZATION", Ignored: true}
}

func normalizedFacts(event Event, mapping domain.Mapping, path, referenceProvenance string) (domain.Facts, error) {
	facts := domain.Facts{Mapping: mapping, CanonicalEventID: event.ID,
		TransactionID: event.TransactionID, LifecycleID: event.LifecycleID,
		Type: event.Type, Subtype: event.Subtype, Category: event.Category,
		ProcessResult: event.ProcessResult, FailureReason: event.FailureReason,
		TransactionAmount: event.TransactionAmount, TransactionCurrency: event.TransactionCurrency,
		BillingAmount: event.BillingAmount, BillingCurrency: event.BillingCurrency,
		Provenance: map[string]string{
			"reference":   referenceProvenance,
			"account_id":  "active card/roster join; request x-on-behalf-of",
			"customer_id": "active card/roster join",
		},
	}
	for _, field := range strings.Fields("id card_id card_transaction_id lifecycle_id type subtype transaction_category process_result failure_reason transaction_amount transaction_currency billing_amount billing_currency created_at transacted_at updated_at") {
		facts.Provenance[field] = path + "." + field + " [event=" + event.ID + "]"
	}
	for _, field := range []struct {
		value  string
		target **time.Time
	}{
		{event.CreatedAt, &facts.CreatedAt}, {event.TransactedAt, &facts.TransactedAt},
		{event.UpdatedAt, &facts.UpdatedAt},
	} {
		if field.value == "" {
			continue
		}
		parsed, err := parseProviderTime(field.value)
		if err != nil {
			return facts, &domain.Failure{Reason: "PROVIDER_TIME_INVALID"}
		}
		*field.target = &parsed
	}
	return facts, nil
}

func orderingReason(candidate Event, events []Event) string {
	ambiguous, succeededLater := false, false
	for _, other := range events {
		if other.ID == candidate.ID || other.TransactionID != candidate.TransactionID {
			continue
		}
		relevant := other.Type == "AUTHORIZATION" || other.Type == "CLEARING" || other.Type == "REVERSAL_AUTH"
		if other.Type == "" || other.Type == "UNKNOWN" ||
			(relevant && other.ProcessResult != "APPROVED" && other.ProcessResult != "DECLINED") {
			ambiguous = true
			continue
		}
		if !relevant || other.ProcessResult != "APPROVED" {
			continue
		}
		if other.Type == "AUTHORIZATION" {
			if other.Subtype == "VERIFICATION" {
				continue
			}
			if other.Subtype != "AUTHORIZATION" && other.Subtype != "INCREMENTAL_AUTHORIZATION" {
				ambiguous = true
				continue
			}
		}
		candidateTime, otherTime := candidate.CreatedAt, other.CreatedAt
		if candidate.TransactedAt != "" && other.TransactedAt != "" {
			candidateTime, otherTime = candidate.TransactedAt, other.TransactedAt
		}
		before, err1 := parseProviderTime(candidateTime)
		after, err2 := parseProviderTime(otherTime)
		if err1 != nil || err2 != nil || before.Equal(after) {
			ambiguous = true
			continue
		}
		if after.After(before) {
			succeededLater = true
		}
	}
	if succeededLater {
		return "PAYMENT_NO_LONGER_ACTIONABLE"
	}
	if ambiguous {
		return "OUTCOME_UNRESOLVED"
	}
	return ""
}
