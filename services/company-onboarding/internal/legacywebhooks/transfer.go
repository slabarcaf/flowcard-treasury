// Package legacywebhooks transfers trusted historical Node receipts into the Go
// inbox. It never verifies invented provider signatures or mutates legacy rows.
package legacywebhooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

var (
	ErrTarget   = errors.New("legacy_webhook_database_target_mismatch")
	ErrSource   = errors.New("legacy_webhook_source_unavailable")
	ErrChanged  = errors.New("legacy_webhook_source_changed")
	ErrEvidence = errors.New("legacy_webhook_evidence_requires_review")
	ErrStorage  = errors.New("legacy_webhook_target_unavailable")
)

// Options binds an audited source snapshot to both expected database names.
type Options struct {
	SourceDatabase     string
	TargetDatabase     string
	Environment        string
	Apply              bool
	ExpectedSourceHash string
}

// Report exposes counts and hashes only. Receipt bodies, account IDs, secrets
// and driver errors remain private. Complete means durable receipt, not business
// processing, account activation or a resolved RFI.
type Report struct {
	Environment       string `json:"environment"`
	SourceHash        string `json:"source_hash"`
	SourceHighWater   int64  `json:"source_high_water"`
	Events            int    `json:"events"`
	Existing          int    `json:"already_received"`
	Missing           int    `json:"missing_receipts"`
	Invalid           int    `json:"invalid_receipts"`
	Conflicts         int    `json:"conflicting_receipts"`
	Transferred       int    `json:"transferred_receipts"`
	Complete          bool   `json:"all_durably_received"`
	LegacyRowsChanged bool   `json:"legacy_rows_changed"`
}

type receipt struct {
	id                         int64
	eventID, eventType, status string
	raw                        []byte
	item                       postgres.WebhookInboxItem
	valid                      bool
}

// Transfer defaults to an audit. Apply requires the exact prior source hash;
// every mutation uses the existing durable Go inbox writer and deduplication.
func Transfer(ctx context.Context, source, target *pgxpool.Pool, options Options) (Report, error) {
	report := Report{Environment: options.Environment}
	if options.SourceDatabase == "" || options.TargetDatabase == "" || (options.Environment != "sandbox" && options.Environment != "production") || source == nil || target == nil {
		return report, ErrTarget
	}
	for _, binding := range []struct {
		pool *pgxpool.Pool
		name string
	}{{source, options.SourceDatabase}, {target, options.TargetDatabase}} {
		var actual string
		if binding.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&actual) != nil {
			return report, ErrStorage
		}
		if actual != binding.name {
			return report, ErrTarget
		}
	}
	if postgres.VerifySchemaReadiness(ctx, target) != nil {
		return report, ErrStorage
	}
	receipts, hash, err := readSource(ctx, source, options)
	if err != nil {
		return report, err
	}
	report.SourceHash = hash
	report.Events = len(receipts)
	if options.Apply && (options.ExpectedSourceHash == "" || options.ExpectedSourceHash != hash) {
		return report, ErrChanged
	}
	for _, r := range receipts {
		if r.id > report.SourceHighWater {
			report.SourceHighWater = r.id
		}
		if !r.valid {
			report.Invalid++
			continue
		}
		found, same, err := targetReceipt(ctx, target, r.item)
		if err != nil {
			return report, ErrStorage
		}
		if found {
			if same {
				report.Existing++
			} else {
				report.Conflicts++
			}
		} else {
			report.Missing++
		}
	}
	if report.Invalid > 0 || report.Conflicts > 0 {
		return report, ErrEvidence
	}
	report.Complete = report.Missing == 0
	if !options.Apply || report.Complete {
		return report, nil
	}
	repo := postgres.NewRepository(target)
	for _, r := range receipts {
		inserted, err := repo.InsertWebhookOnce(ctx, r.item)
		if errors.Is(err, postgres.ErrWebhookEventConflict) {
			return report, ErrEvidence
		}
		if err != nil {
			return report, ErrStorage
		}
		if inserted {
			report.Transferred++
		}
	}
	_, after, err := readSource(ctx, source, options)
	if err != nil {
		return report, err
	}
	if after != hash {
		return report, ErrChanged
	}
	for _, r := range receipts {
		found, same, err := targetReceipt(ctx, target, r.item)
		if err != nil {
			return report, ErrStorage
		}
		if !found || !same {
			return report, ErrEvidence
		}
	}
	report.Missing = 0
	report.Complete = true
	return report, nil
}

func targetReceipt(ctx context.Context, pool *pgxpool.Pool, item postgres.WebhookInboxItem) (bool, bool, error) {
	var same bool
	err := pool.QueryRow(ctx, `SELECT raw_payload=$2::jsonb AND account_id=$3 AND event_type=$4 AND (environment=$5 OR environment='') FROM onboarding.webhook_inbox WHERE event_id=$1`, item.EventID, item.RawPayload, item.AccountID, item.EventType, item.Environment).Scan(&same)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, same, err
}

func readSource(ctx context.Context, pool *pgxpool.Pool, options Options) ([]receipt, string, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", ErrSource
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inbox, forwarding bool
	if tx.QueryRow(ctx, `SELECT to_regclass('public.webhook_inbox') IS NOT NULL,to_regclass('public.onboarding_webhook_forwarding') IS NOT NULL`).Scan(&inbox, &forwarding) != nil || !inbox {
		return nil, "", ErrSource
	}
	if forwarding {
		var orphan bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.onboarding_webhook_forwarding f LEFT JOIN public.webhook_inbox i ON i.provider_event_id=f.provider_event_id WHERE i.provider_event_id IS NULL)`).Scan(&orphan) != nil {
			return nil, "", ErrSource
		}
		if orphan {
			return nil, "", ErrEvidence
		}
	}
	query := `SELECT i.id,i.provider_event_id,i.event_type,i.raw_body,'UNENROLLED' FROM public.webhook_inbox i WHERE (i.event_type LIKE 'account.%' OR i.event_type LIKE 'rfi.%') ORDER BY i.id LIMIT 10001`
	if forwarding {
		query = `SELECT i.id,i.provider_event_id,i.event_type,i.raw_body,COALESCE(f.status,'UNENROLLED') FROM public.webhook_inbox i LEFT JOIN public.onboarding_webhook_forwarding f ON f.provider_event_id=i.provider_event_id WHERE (i.event_type LIKE 'account.%' OR i.event_type LIKE 'rfi.%') ORDER BY i.id LIMIT 10001`
	}
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, "", ErrSource
	}
	defer rows.Close()
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "%q:%q:%q\n", options.SourceDatabase, options.TargetDatabase, options.Environment)
	var receipts []receipt
	totalBytes := 0
	for rows.Next() {
		var r receipt
		if rows.Scan(&r.id, &r.eventID, &r.eventType, &r.raw, &r.status) != nil {
			return nil, "", ErrSource
		}
		totalBytes += len(r.raw)
		if len(receipts) >= 10000 || totalBytes > 64<<20 {
			return nil, "", ErrSource
		}
		digest := sha256.Sum256(r.raw)
		_, _ = fmt.Fprintf(hasher, "%d:%q:%q:%q:%x\n", r.id, r.eventID, r.eventType, r.status, digest)
		var payload webhooks.InboundWebhookPayload
		if len(r.raw) > 0 && len(r.raw) <= 1<<20 && json.Unmarshal(r.raw, &payload) == nil && payload.ID == r.eventID && payload.Name == r.eventType && (payload.Environment == "" || payload.Environment == options.Environment) && strings.TrimSpace(r.eventID) != "" && r.eventID == strings.TrimSpace(r.eventID) && r.eventType == strings.TrimSpace(r.eventType) {
			r.item = postgres.WebhookInboxItem{EventID: r.eventID, EventType: r.eventType, AccountID: strings.TrimSpace(payload.ExtractAccountID()), Environment: options.Environment, RawPayload: json.RawMessage(r.raw)}
			r.valid = true
		}
		receipts = append(receipts, r)
	}
	if rows.Err() != nil {
		return nil, "", ErrSource
	}
	return receipts, hex.EncodeToString(hasher.Sum(nil)), nil
}
