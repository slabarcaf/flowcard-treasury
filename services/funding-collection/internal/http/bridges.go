package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

type bridgeDTO struct {
	ObligationID              string         `json:"obligation_id"`
	ID                        string         `json:"id"`
	Mode                      string         `json:"mode"`
	SourceBindingID           string         `json:"source_binding_id"`
	CustomerID                string         `json:"customer_id"`
	AccountRef                string         `json:"account_ref"`
	Version                   int64          `json:"version"`
	AsOf                      time.Time      `json:"as_of"`
	OriginatingCaseID         *string        `json:"originating_case_id"`
	EvaluationID              *string        `json:"evaluation_id"`
	OfferID                   *string        `json:"offer_id"`
	TermsHash                 *string        `json:"terms_hash"`
	Currency                  string         `json:"currency"`
	PrincipalOwedCents        *int64         `json:"principal_owed_cents"`
	ContractFeeOwedCents      *int64         `json:"contract_fee_owed_cents"`
	LateFeeOwedCents          *int64         `json:"late_fee_owed_cents"`
	DueOn                     *string        `json:"due_on"`
	FundedAt                  *time.Time     `json:"funded_at"`
	Lifecycle                 string         `json:"lifecycle"`
	Delinquency               string         `json:"delinquency"`
	DaysPastDue               *int           `json:"days_past_due"`
	HoldReasons               []string       `json:"hold_reasons"`
	LastAttemptID             *string        `json:"last_attempt_id"`
	LastAttemptState          *string        `json:"last_attempt_state"`
	LastProviderObservedAt    *time.Time     `json:"last_provider_observed_at"`
	NextEligibleRetryAt       *time.Time     `json:"next_eligible_retry_at"`
	AccrualCurrent            *bool          `json:"accrual_current"`
	AccrualCurrentThrough     *string        `json:"accrual_current_through"`
	DebtAsOf                  *time.Time     `json:"debt_as_of"`
	AllowedNextActions        []string       `json:"allowed_next_actions"`
	AcceptanceStatus          *string        `json:"acceptance_status"`
	ApprovalStatus            *string        `json:"approval_status"`
	CollectionAuthorityStatus *string        `json:"collection_authority_status"`
	AcceptanceReason          *string        `json:"acceptance_reason"`
	ApprovalReason            *string        `json:"approval_reason"`
	CollectionAuthorityReason *string        `json:"collection_authority_reason"`
	Operations                []operationDTO `json:"operations"`
}

type operationDTO struct {
	ID                  string     `json:"id"`
	Version             int64      `json:"version"`
	Kind                string     `json:"kind"`
	State               string     `json:"state"`
	ReservedAmountCents *int64     `json:"reserved_amount_cents"`
	SettlementReference *string    `json:"last_verified_settlement_reference"`
	RetryCount          int        `json:"retry_count"`
	NextCheck           *time.Time `json:"next_check"`
	HoldReason          *string    `json:"hold_reason"`
}

const maxServicingCursorBytes = 2048
const maxServicingStateRows = 1000

func stringPointer(value string) *string { return &value }

func domainDate(value time.Time) time.Time {
	date := value.UTC()
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
}

func readBoundCollectionAuthority(ctx context.Context, tx pgx.Tx, mode, bridgeID string, now time.Time) (string, string, error) {
	var authorityID, borrowerAccount, fundingAccount string
	var boundVersion int64
	var status, product, mandate, currency *string
	var version *int64
	var validFrom, expiresAt *time.Time
	var kinds, purposes []string
	var maximum int64
	var latest bool
	err := tx.QueryRow(ctx, `SELECT o.terms_json->>'collection_authority_id',(o.terms_json->>'collection_authority_version')::bigint,
		a.status,a.version,a.valid_from,a.expires_at,a.product_code,a.mandate_reference,a.permitted_kinds,a.permitted_purposes,a.currency,a.maximum_debit_cents,
		NOT EXISTS(SELECT 1 FROM servicing.authorities newer WHERE newer.mode=a.mode AND newer.id=a.id AND newer.version>a.version),
		b.canonical_account_id,f.funding_account_ref
		FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id AND o.terms_hash=b.terms_hash
		JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		LEFT JOIN servicing.authorities a ON a.mode=b.mode AND a.id=o.terms_json->>'collection_authority_id' AND a.version=(o.terms_json->>'collection_authority_version')::bigint
		WHERE b.mode=$1 AND b.id=$2`, mode, bridgeID).Scan(&authorityID, &boundVersion, &status, &version, &validFrom, &expiresAt, &product, &mandate, &kinds, &purposes, &currency, &maximum, &latest, &borrowerAccount, &fundingAccount)
	if err != nil {
		return "", "", errors.New("bridge_authority_read_failed")
	}
	if authorityID == "" || boundVersion <= 0 || version == nil {
		return "MISSING", "BOUND_AUTHORITY_MISSING", nil
	}
	if !latest {
		return "UNKNOWN", "BOUND_AUTHORITY_VERSION_SUPERSEDED", nil
	}
	if status == nil {
		return "MISSING", "BOUND_AUTHORITY_MISSING", nil
	}
	if *status == "REVOKED" {
		return "REVOKED", "BOUND_AUTHORITY_REVOKED", nil
	}
	if *status != "ACTIVE" {
		return "UNKNOWN", "BOUND_AUTHORITY_STATUS_UNKNOWN", nil
	}
	if expiresAt != nil && !now.Before(*expiresAt) {
		return "EXPIRED", "BOUND_AUTHORITY_EXPIRED", nil
	}
	if validFrom == nil || now.Before(*validFrom) {
		return "UNKNOWN", "BOUND_AUTHORITY_NOT_YET_VALID", nil
	}
	if product == nil || *product != "CONNECTED_ACCOUNT_TRANSFERS" || mandate == nil || strings.TrimSpace(*mandate) == "" || borrowerAccount == "" || fundingAccount == "" {
		return "UNKNOWN", "BOUND_AUTHORITY_BINDING_UNVERIFIED", nil
	}
	if currency == nil || *currency != "USD" {
		return "UNKNOWN", "BOUND_AUTHORITY_CURRENCY_INVALID", nil
	}
	if kinds == nil || purposes == nil || !postgres.CollectionAuthorityAllows(kinds, purposes, "COLLECT_FEE") || !postgres.CollectionAuthorityAllows(kinds, purposes, "COLLECT_PRINCIPAL") {
		return "UNKNOWN", "BOUND_AUTHORITY_PERMISSION_INCOMPLETE", nil
	}
	used, reserved, err := postgres.ReadAuthorityUsage(ctx, tx, domain.Mode(mode), authorityID)
	if err != nil {
		if err.Error() == "authority_usage_overflow" {
			return "UNKNOWN", "BOUND_AUTHORITY_USAGE_INVALID", nil
		}
		return "", "", err
	}
	if used >= maximum || reserved >= maximum-used {
		return "UNKNOWN", "BOUND_AUTHORITY_LIMIT_UNAVAILABLE", nil
	}
	// The saved grant must still authorize the same B-to-A account pair.
	var source, destination string
	if err = tx.QueryRow(ctx, `SELECT source_account_ref,destination_account_ref FROM servicing.authorities WHERE mode=$1 AND id=$2 AND version=$3`, mode, authorityID, boundVersion).Scan(&source, &destination); err != nil {
		return "", "", errors.New("bridge_authority_binding_read_failed")
	}
	if source != borrowerAccount || destination != fundingAccount {
		return "UNKNOWN", "BOUND_AUTHORITY_ACCOUNTS_CHANGED", nil
	}
	return "VALID", "", nil
}

func (h *handler) readBridge(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		writeError(w, 400, "INVALID_REQUEST", "Bridge ID is invalid")
		return
	}
	identity, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	b, err := loadBridgeDTO(r.Context(), tx, h.cfg.Mode, h.cfg.SourceBindingID, r.PathValue("id"), h.now().UTC())
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && identity.Role == "ACTOR" && identity.ActorType == "CUSTOMER" && identity.CustomerID != b.CustomerID) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Bridge not found")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	writeJSON(w, 200, b)
}

func loadBridgeDTO(ctx context.Context, tx pgx.Tx, mode, binding, id string, now time.Time) (bridgeDTO, error) {
	b := bridgeDTO{ID: id, ObligationID: id, Mode: mode, SourceBindingID: binding, Currency: "USD", HoldReasons: []string{}, AllowedNextActions: []string{}, Operations: []operationDTO{}}
	var caseID, evaluationID, offerID, termsHash *string
	var principal, fee, late int64
	var due time.Time
	var funded, closedAt, lastAccrued *time.Time
	var everFunded, customerHold bool
	err := tx.QueryRow(ctx, `SELECT b.customer_id,b.version,b.originating_case_id,b.evaluation_id,b.offer_id::text,b.terms_hash,b.principal_owed_cents,b.contract_fee_owed_cents,b.late_fee_owed_cents,b.due_on,b.funded_at,b.closed_at,b.ever_funded,b.lifecycle,b.delinquency_stage,b.hold_reasons,b.last_accrued_on,c.active_hold
		FROM servicing.bridges b JOIN servicing.customers c ON c.mode=b.mode AND c.id=b.customer_id WHERE b.mode=$1 AND b.id=$2`, mode, id).Scan(&b.CustomerID, &b.Version, &caseID, &evaluationID, &offerID, &termsHash, &principal, &fee, &late, &due, &funded, &closedAt, &everFunded, &b.Lifecycle, &b.Delinquency, &b.HoldReasons, &lastAccrued, &customerHold)
	if err != nil {
		return b, err
	}
	b.AccountRef = "customer:" + b.CustomerID
	b.AsOf = now
	b.OriginatingCaseID = caseID
	b.EvaluationID = evaluationID
	b.OfferID = offerID
	b.TermsHash = termsHash
	b.PrincipalOwedCents = &principal
	b.ContractFeeOwedCents = &fee
	b.LateFeeOwedCents = &late
	dueText := due.Format("2006-01-02")
	b.DueOn = &dueText
	b.FundedAt = funded
	if customerHold && !containsString(b.HoldReasons, "CUSTOMER_ACTIVE_HOLD") {
		b.HoldReasons = append(b.HoldReasons, "CUSTOMER_ACTIVE_HOLD")
	}
	knownDebt, err := domain.AddMoney(domain.Money(principal), domain.Money(fee))
	if err == nil {
		knownDebt, err = domain.AddMoney(knownDebt, domain.Money(late))
	}
	if err != nil {
		return b, errors.New("bridge_debt_overflow")
	}
	daysPastDue, err := domain.DaysPastDue(now, due, knownDebt)
	if err != nil {
		return b, errors.New("bridge_delinquency_unavailable")
	}
	b.DaysPastDue = &daysPastDue
	var accepted, approved, approvalRequired bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.actor_decisions d WHERE d.mode=o.mode AND d.offer_id=o.id AND d.offer_revision=o.revision AND d.terms_hash=o.terms_hash AND d.decision='ACCEPT'), EXISTS(SELECT 1 FROM servicing.actor_decisions d WHERE d.mode=o.mode AND d.offer_id=o.id AND d.offer_revision=o.revision AND d.terms_hash=o.terms_hash AND d.decision IN ('APPROVE','AUTO_APPROVE')), COALESCE((o.candidate_evidence->'evaluation'->>'approval_required')::boolean,false) FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id AND o.terms_hash=b.terms_hash WHERE b.mode=$1 AND b.id=$2`, mode, id).Scan(&accepted, &approved, &approvalRequired); err != nil {
		return b, err
	}
	if accepted {
		b.AcceptanceStatus = stringPointer("VALID")
	} else {
		b.AcceptanceStatus = stringPointer("MISSING")
		b.AcceptanceReason = stringPointer("ACCEPTANCE_RECORD_MISSING")
	}
	if approved {
		b.ApprovalStatus = stringPointer("VALID")
	} else if !approvalRequired {
		b.ApprovalStatus = stringPointer("NOT_REQUIRED")
	} else {
		b.ApprovalStatus = stringPointer("MISSING")
		b.ApprovalReason = stringPointer("APPROVAL_RECORD_MISSING")
	}
	authorityStatus, authorityReason, err := readBoundCollectionAuthority(ctx, tx, mode, id, now)
	if err != nil {
		return b, err
	}
	b.CollectionAuthorityStatus = stringPointer(authorityStatus)
	if authorityReason != "" {
		b.CollectionAuthorityReason = stringPointer(authorityReason)
	}
	current := true
	if everFunded && b.Lifecycle != "VOID_UNFUNDED" {
		postingThrough := domainDate(now)
		if b.Lifecycle == "REPAID" && closedAt != nil && domainDate(*closedAt).Before(postingThrough) {
			postingThrough = domainDate(*closedAt)
		}
		accrualStart := domainDate(due).AddDate(0, 0, 1)
		if !postingThrough.Before(accrualStart) {
			current = lastAccrued != nil && !domainDate(*lastAccrued).Before(postingThrough)
		}
	}
	var through *string
	if lastAccrued != nil {
		value := domainDate(*lastAccrued).Format("2006-01-02")
		through = &value
	}
	b.AccrualCurrentThrough = through
	// debt_as_of identifies the latest persisted ledger posting, not response time.
	var debtAsOf *time.Time
	if err = tx.QueryRow(ctx, `SELECT max(recorded_at) FROM servicing.ledger_transactions WHERE mode=$1 AND bridge_id=$2`, mode, id).Scan(&debtAsOf); err != nil {
		return b, errors.New("bridge_posting_checkpoint_read_failed")
	}
	b.DebtAsOf = debtAsOf
	b.AccrualCurrent = &current
	var attemptID, attemptState *string
	var observed, next *time.Time
	err = tx.QueryRow(ctx, `SELECT ca.id::text,o.state,p.observed_at,o.next_check_at
		FROM servicing.collection_attempts ca
		JOIN LATERAL (SELECT op.id,op.state,op.next_check_at FROM servicing.collection_allocations a
		JOIN servicing.operations op ON op.mode=a.mode AND op.id=a.operation_id
		WHERE a.mode=ca.mode AND a.attempt_id=ca.id ORDER BY a.allocation_index LIMIT 1) o ON true
		LEFT JOIN LATERAL (SELECT po.observed_at FROM servicing.provider_observations po
		WHERE po.mode=ca.mode AND po.operation_id=o.id ORDER BY po.observed_at DESC LIMIT 1) p ON true
		WHERE ca.mode=$1 AND ca.bridge_id=$2 ORDER BY ca.created_at DESC,ca.id DESC LIMIT 1`, mode, id).Scan(&attemptID, &attemptState, &observed, &next)
	if err == nil {
		if attemptID == nil || attemptState == nil {
			return b, errors.New("bridge_collection_attempt_incomplete")
		}
		b.LastAttemptID = attemptID
		b.LastAttemptState = attemptState
		b.LastProviderObservedAt = observed
		b.NextEligibleRetryAt = next
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id::text,o.version,o.kind,o.state,
		CASE WHEN o.kind='FUND' AND r.state IN ('NEVER_SENT','DISPATCHED') THEN r.facility_principal_cents
		WHEN o.kind<>'FUND' AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') THEN o.amount_cents ELSE NULL END,
		lt.id::text,o.retry_count,o.next_check_at,o.sanitized_error_code,o.created_at,
		count(*) FILTER (WHERE o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') OR o.kind='FUND') OVER(),
		(o.kind='FUND' AND o.state='HELD' AND r.state='NEVER_SENT' AND b.lifecycle='RESERVED' AND NOT b.ever_funded)
		FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		LEFT JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		LEFT JOIN LATERAL (SELECT t.id FROM servicing.ledger_transactions t WHERE t.mode=o.mode AND t.operation_id=o.id
		AND ((o.kind='FUND' AND t.transaction_kind='FUNDING_SETTLEMENT' AND t.effect_key='fund-settlement:'||o.id::text)
		OR (o.kind IN ('COLLECT_FEE','COLLECT_PRINCIPAL') AND t.transaction_kind='COLLECTION_SETTLEMENT' AND t.effect_key='collection-settlement:'||o.id::text)) LIMIT 1) lt ON true
		WHERE o.mode=$1 AND o.bridge_id=$2 AND (o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD','SETTLED','FAILED_CONFIRMED','CANCELLED_UNSENT') OR o.kind='FUND')
		ORDER BY CASE WHEN o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') OR o.kind='FUND' THEN 0 ELSE 1 END,
		CASE WHEN o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') OR o.kind='FUND' THEN o.created_at END ASC NULLS LAST,
		CASE WHEN o.state NOT IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') AND o.kind<>'FUND' THEN o.created_at END DESC NULLS LAST,o.id LIMIT 26`, mode, id)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	type operationSummary struct {
		dto          operationDTO
		createdAt    time.Time
		provenUnsent bool
	}
	var summaries []operationSummary
	var mandatoryCount int64
	for rows.Next() {
		var op operationDTO
		var reserved *int64
		var settlement *string
		var hold *string
		var created time.Time
		var provenUnsent bool
		if err = rows.Scan(&op.ID, &op.Version, &op.Kind, &op.State, &reserved, &settlement, &op.RetryCount, &op.NextCheck, &hold, &created, &mandatoryCount, &provenUnsent); err != nil {
			return b, err
		}
		op.ReservedAmountCents = reserved
		op.SettlementReference = settlement
		op.HoldReason = hold
		summaries = append(summaries, operationSummary{dto: op, createdAt: created, provenUnsent: provenUnsent})
	}
	if rows.Err() != nil {
		return b, rows.Err()
	}
	if mandatoryCount > 25 {
		return b, errors.New("too_many_unresolved_operations")
	}
	if len(summaries) > 25 {
		summaries = summaries[:25]
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].createdAt.Equal(summaries[j].createdAt) {
			return summaries[i].dto.ID < summaries[j].dto.ID
		}
		return summaries[i].createdAt.Before(summaries[j].createdAt)
	})
	for _, summary := range summaries {
		b.Operations = append(b.Operations, summary.dto)
	}
	if len(b.Operations) > 25 {
		return b, errors.New("operation_summary_bound_invalid")
	}
	accrualCorrection := containsString(b.HoldReasons, "ACCRUAL_CORRECTION_REQUIRED")
	if !current && !accrualCorrection {
		b.AllowedNextActions = []string{"WAIT_FOR_ACCRUAL"}
	} else if len(b.HoldReasons) > 0 {
		b.AllowedNextActions = []string{"REQUEST_OPERATOR_REVIEW"}
	} else {
		heldOperation, unsentHeldFunding, automaticLookup := false, false, false
		for _, summary := range summaries {
			switch summary.dto.State {
			case "HELD":
				if summary.provenUnsent {
					unsentHeldFunding = true
				} else {
					heldOperation = true
				}
			case "QUEUED", "DISPATCHING", "PENDING", "UNKNOWN":
				automaticLookup = true
			}
		}
		if heldOperation {
			b.AllowedNextActions = []string{"RECONCILE_OPERATION"}
		} else if unsentHeldFunding {
			b.AllowedNextActions = []string{"REQUEST_OPERATOR_REVIEW"}
		} else if !current {
			b.AllowedNextActions = []string{"WAIT_FOR_ACCRUAL"}
		} else if b.Lifecycle == "FUNDING_PENDING" || automaticLookup {
			b.AllowedNextActions = []string{"WAIT_FOR_SETTLEMENT"}
		} else if !everFunded || b.Lifecycle == "REPAID" || b.Lifecycle == "VOID_UNFUNDED" || knownDebt == 0 {
			b.AllowedNextActions = []string{}
		} else if knownDebt > 0 && (b.Lifecycle == "ACTIVE" || b.Lifecycle == "COLLECTING" || b.Lifecycle == "REOPENED_HELD") {
			if b.CollectionAuthorityStatus != nil && *b.CollectionAuthorityStatus == "VALID" {
				b.AllowedNextActions = []string{"WAIT_FOR_ELIGIBLE_DEPOSIT"}
			} else {
				b.AllowedNextActions = []string{"VERIFY_COLLECTION_AUTHORITY"}
			}
		}
	}
	return b, nil
}

func (h *handler) readOperation(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		writeError(w, 400, "INVALID_REQUEST", "Operation ID is invalid")
		return
	}
	id, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	var op operationDTO
	var customer string
	err := h.pool.QueryRow(r.Context(), `SELECT o.id::text,o.version,o.kind,o.state,
		CASE WHEN o.kind='FUND' AND r.state IN ('NEVER_SENT','DISPATCHED') THEN r.facility_principal_cents
		WHEN o.kind<>'FUND' AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD') THEN o.amount_cents ELSE NULL END,
		lt.id::text,o.retry_count,o.next_check_at,o.sanitized_error_code,b.customer_id
		FROM servicing.operations o JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		LEFT JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		LEFT JOIN LATERAL (SELECT t.id FROM servicing.ledger_transactions t WHERE t.mode=o.mode AND t.operation_id=o.id
		AND ((o.kind='FUND' AND t.transaction_kind='FUNDING_SETTLEMENT' AND t.effect_key='fund-settlement:'||o.id::text)
		OR (o.kind IN ('COLLECT_FEE','COLLECT_PRINCIPAL') AND t.transaction_kind='COLLECTION_SETTLEMENT' AND t.effect_key='collection-settlement:'||o.id::text)) LIMIT 1) lt ON true
		WHERE o.mode=$1 AND o.id=$2`, h.cfg.Mode, r.PathValue("id")).Scan(&op.ID, &op.Version, &op.Kind, &op.State, &op.ReservedAmountCents, &op.SettlementReference, &op.RetryCount, &op.NextCheck, &op.HoldReason, &customer)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && id.Role == "ACTOR" && id.ActorType == "CUSTOMER" && id.CustomerID != customer) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Operation not found")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	writeJSON(w, 200, op)
}

type collectionCursor struct {
	Mode    string    `json:"mode"`
	Bridge  string    `json:"bridge_id"`
	Version int64     `json:"bridge_version"`
	Created time.Time `json:"created_at"`
	ID      string    `json:"id"`
}
type collectionPageDTO struct {
	BridgeVersion int64             `json:"bridge_version"`
	Items         []json.RawMessage `json:"items"`
	NextCursor    *string           `json:"next_cursor"`
}

type collectionAllocationDTO struct {
	Index       int    `json:"allocation_index"`
	Bucket      string `json:"bucket"`
	AmountCents int64  `json:"amount_cents"`
}

type collectionItemDTO struct {
	ID                 string                    `json:"id"`
	OperationID        string                    `json:"operation_id"`
	DepositID          string                    `json:"deposit_id"`
	State              string                    `json:"state"`
	CreatedAt          time.Time                 `json:"created_at"`
	DepositSettledAt   time.Time                 `json:"deposit_settled_at"`
	SettledAt          *time.Time                `json:"settled_at"`
	ReservedDebitCents int64                     `json:"reserved_debit_cents"`
	SettledDebitCents  int64                     `json:"settled_debit_cents"`
	ProviderFeeCents   int64                     `json:"provider_fee_cents"`
	PlannedAllocations []collectionAllocationDTO `json:"planned_allocations"`
	Allocations        []collectionAllocationDTO `json:"allocations"`
}

func (h *handler) encodeCollectionCursor(c collectionCursor) string {
	raw, _ := json.Marshal(c)
	enc := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(h.cfg.CoordinatorSecret))
	_, _ = mac.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (h *handler) decodeCollectionCursor(value string) (collectionCursor, error) {
	var c collectionCursor
	if len(value) == 0 || len(value) > maxServicingCursorBytes {
		return c, errors.New("cursor_invalid")
	}
	p := strings.Split(value, ".")
	if len(p) != 2 {
		return c, errors.New("cursor_invalid")
	}
	decoder := base64.RawURLEncoding.Strict()
	sig, e := decoder.DecodeString(p[1])
	if e != nil || len(sig) != sha256.Size || base64.RawURLEncoding.EncodeToString(sig) != p[1] {
		return c, e
	}
	mac := hmac.New(sha256.New, []byte(h.cfg.CoordinatorSecret))
	_, _ = mac.Write([]byte(p[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("cursor_invalid")
	}
	raw, e := decoder.DecodeString(p[0])
	if e != nil || len(raw) > 1024 || base64.RawURLEncoding.EncodeToString(raw) != p[0] || rejectDuplicateCommandKeys(raw) != nil {
		return c, errors.New("cursor_invalid")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || (c.Mode != "DEMO" && c.Mode != "SANDBOX") || !validUUID(c.Bridge) || c.Version <= 0 || !validUUID(c.ID) || c.Created.IsZero() {
		return collectionCursor{}, errors.New("cursor_invalid")
	}
	_, offset := c.Created.Zone()
	if offset != 0 {
		return collectionCursor{}, errors.New("cursor_invalid")
	}
	return c, nil
}

func readPlannedCollectionAllocations(ctx context.Context, tx pgx.Tx, mode, attemptID string) ([]collectionAllocationDTO, error) {
	rows, err := tx.Query(ctx, `SELECT allocation_index,bucket,amount_cents FROM servicing.collection_allocations WHERE mode=$1 AND attempt_id=$2 ORDER BY allocation_index`, mode, attemptID)
	if err != nil {
		return nil, errors.New("collection_plan_read_failed")
	}
	defer rows.Close()
	allocations := []collectionAllocationDTO{}
	for rows.Next() {
		var item collectionAllocationDTO
		if err = rows.Scan(&item.Index, &item.Bucket, &item.AmountCents); err != nil {
			return nil, errors.New("collection_plan_read_failed")
		}
		allocations = append(allocations, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("collection_plan_read_failed")
	}
	return allocations, nil
}

func readSettledCollectionAllocations(ctx context.Context, tx pgx.Tx, mode, operationID string) ([]collectionAllocationDTO, error) {
	rows, err := tx.Query(ctx, `SELECT l.account,l.debit_cents,l.credit_cents FROM servicing.ledger_transactions t JOIN servicing.ledger_lines l ON l.mode=t.mode AND l.transaction_id=t.id WHERE t.mode=$1 AND t.operation_id=$2 AND t.transaction_kind='COLLECTION_SETTLEMENT' AND t.effect_key='collection-settlement:'||$2 ORDER BY l.line_number`, mode, operationID)
	if err != nil {
		return nil, errors.New("collection_settlement_read_failed")
	}
	defer rows.Close()
	allocations := []collectionAllocationDTO{}
	for rows.Next() {
		var account string
		var debit, credit int64
		if err = rows.Scan(&account, &debit, &credit); err != nil {
			return nil, errors.New("collection_settlement_read_failed")
		}
		bucket := ""
		switch account {
		case "LATE_FEE_RECEIVABLE":
			bucket = "LATE_FEE"
		case "CONTRACT_FEE_RECEIVABLE":
			bucket = "CONTRACT_FEE"
		case "PRINCIPAL_RECEIVABLE":
			bucket = "PRINCIPAL"
		case "CUSTOMER_REFUND_PAYABLE":
			bucket = "REFUND_PAYABLE"
		default:
			continue
		}
		if debit != 0 || credit <= 0 {
			return nil, errors.New("collection_settlement_allocation_invalid")
		}
		allocations = append(allocations, collectionAllocationDTO{Index: len(allocations), Bucket: bucket, AmountCents: credit})
	}
	if rows.Err() != nil {
		return nil, errors.New("collection_settlement_read_failed")
	}
	return allocations, nil
}

func (h *handler) readCollections(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		writeError(w, 400, "INVALID_REQUEST", "Bridge ID is invalid")
		return
	}
	identity, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var version int64
	var customer string
	err = tx.QueryRow(r.Context(), `SELECT version,customer_id FROM servicing.bridges WHERE mode=$1 AND id=$2`, h.cfg.Mode, r.PathValue("id")).Scan(&version, &customer)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && identity.Role == "ACTOR" && identity.ActorType == "CUSTOMER" && identity.CustomerID != customer) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Bridge not found")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	page := collectionPageDTO{BridgeVersion: version, Items: []json.RawMessage{}}
	var cursor *collectionCursor
	queryParams, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil || len(queryParams) > 1 || (len(queryParams) == 1 && len(queryParams["cursor"]) != 1) {
		writeError(w, 400, "INVALID_QUERY", "Collection query is invalid")
		return
	} else if vals := queryParams["cursor"]; len(vals) == 1 {
		c, e := h.decodeCollectionCursor(vals[0])
		if e != nil || c.Mode != h.cfg.Mode || c.Bridge != r.PathValue("id") {
			writeError(w, 400, "INVALID_QUERY", "Collection cursor is invalid")
			return
		}
		if c.Version != version {
			writeError(w, 409, "VERSION_CONFLICT", "Bridge version changed")
			return
		}
		cursor = &c
	}
	query := `SELECT a.id::text,o.id::text,d.id::text,a.state,a.reserved_debit_cents,a.settled_debit_cents,a.provider_fee_cents,a.created_at,d.settled_at,settlement.effective_at FROM servicing.collection_attempts a JOIN servicing.deposits d ON d.mode=a.mode AND d.id=a.deposit_id LEFT JOIN LATERAL (SELECT op.id FROM servicing.collection_allocations ca JOIN servicing.operations op ON op.mode=ca.mode AND op.id=ca.operation_id WHERE ca.mode=a.mode AND ca.attempt_id=a.id ORDER BY ca.allocation_index LIMIT 1) o ON true LEFT JOIN LATERAL (SELECT t.effective_at FROM servicing.ledger_transactions t WHERE t.mode=a.mode AND t.operation_id=o.id AND t.transaction_kind='COLLECTION_SETTLEMENT' AND t.effect_key='collection-settlement:'||o.id::text LIMIT 1) settlement ON true WHERE a.mode=$1 AND a.bridge_id=$2 ORDER BY a.created_at DESC,a.id DESC LIMIT 26`
	args := []any{h.cfg.Mode, r.PathValue("id")}
	if cursor != nil {
		query = `SELECT a.id::text,o.id::text,d.id::text,a.state,a.reserved_debit_cents,a.settled_debit_cents,a.provider_fee_cents,a.created_at,d.settled_at,settlement.effective_at FROM servicing.collection_attempts a JOIN servicing.deposits d ON d.mode=a.mode AND d.id=a.deposit_id LEFT JOIN LATERAL (SELECT op.id FROM servicing.collection_allocations ca JOIN servicing.operations op ON op.mode=ca.mode AND op.id=ca.operation_id WHERE ca.mode=a.mode AND ca.attempt_id=a.id ORDER BY ca.allocation_index LIMIT 1) o ON true LEFT JOIN LATERAL (SELECT t.effective_at FROM servicing.ledger_transactions t WHERE t.mode=a.mode AND t.operation_id=o.id AND t.transaction_kind='COLLECTION_SETTLEMENT' AND t.effect_key='collection-settlement:'||o.id::text LIMIT 1) settlement ON true WHERE a.mode=$1 AND a.bridge_id=$2 AND (a.created_at,a.id)<($3,$4::uuid) ORDER BY a.created_at DESC,a.id DESC LIMIT 26`
		args = append(args, cursor.Created, cursor.ID)
	}
	rows, err := tx.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer rows.Close()
	type attemptRow struct {
		cursor collectionCursor
		item   collectionItemDTO
	}
	var all []attemptRow
	for rows.Next() {
		var item attemptRow
		c := &item.cursor
		c.Mode = h.cfg.Mode
		c.Bridge = r.PathValue("id")
		c.Version = version
		item.item.PlannedAllocations = []collectionAllocationDTO{}
		item.item.Allocations = []collectionAllocationDTO{}
		var operationID *string
		if err = rows.Scan(&c.ID, &operationID, &item.item.DepositID, &item.item.State, &item.item.ReservedDebitCents, &item.item.SettledDebitCents, &item.item.ProviderFeeCents, &c.Created, &item.item.DepositSettledAt, &item.item.SettledAt); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		if operationID == nil {
			writeError(w, 503, "COLLECTION_HISTORY_INCOMPLETE", "A collection attempt has no operation")
			return
		}
		item.item.ID = c.ID
		item.item.OperationID = *operationID
		item.item.CreatedAt = c.Created
		all = append(all, item)
	}
	if rows.Err() != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	rows.Close()
	for i, item := range all {
		if i >= 25 {
			break
		}
		planned, e := readPlannedCollectionAllocations(r.Context(), tx, h.cfg.Mode, item.cursor.ID)
		if e != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Collection plan facts are unavailable")
			return
		}
		item.item.PlannedAllocations = planned
		allocations, e := readSettledCollectionAllocations(r.Context(), tx, h.cfg.Mode, item.item.OperationID)
		if e != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Collection settlement facts are unavailable")
			return
		}
		item.item.Allocations = allocations
		raw, _ := json.Marshal(item.item)
		page.Items = append(page.Items, raw)
	}
	if len(all) > 25 {
		next := h.encodeCollectionCursor(all[24].cursor)
		page.NextCursor = &next
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	writeJSON(w, 200, page)
}

func (h *handler) readServicingState(w http.ResponseWriter, r *http.Request) {
	id, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	if id.Role != "UNDERWRITING_READER" {
		writeError(w, 403, "FORBIDDEN", "Scope does not allow this read")
		return
	}
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil || len(query) != 1 || len(query["customer_id"]) != 1 {
		writeError(w, 400, "INVALID_QUERY", "Exactly one customer_id is required")
		return
	}
	customerID := strings.TrimSpace(query["customer_id"][0])
	if customerID == "" || customerID != query["customer_id"][0] || len(customerID) > 128 {
		writeError(w, 400, "INVALID_QUERY", "customer_id is invalid")
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var principal, fees, reservedPrincipal, reservedFees int64
	var historyComplete, activeHold bool
	var defaultedAt, customerCaptured *time.Time
	var customerVersion int64
	err = tx.QueryRow(r.Context(), `SELECT principal_owed_cents,unpaid_fee_cents,reserved_principal_cents,reserved_fee_cents,history_complete,defaulted_at,active_hold,version,updated_at FROM servicing.customers WHERE mode=$1 AND id=$2`, h.cfg.Mode, customerID).Scan(&principal, &fees, &reservedPrincipal, &reservedFees, &historyComplete, &defaultedAt, &activeHold, &customerVersion, &customerCaptured)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Customer not found")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	var facilityTotal, facilityPrincipal, facilityReserved, facilityOther int64
	var facilityVersion, facilityConfigVersion int64
	var reserveBps int
	var facilityCaptured time.Time
	err = tx.QueryRow(r.Context(), `SELECT total_cents,principal_owed_cents,reserved_principal_cents,other_commitments_cents,version,configuration_version,reserve_bps,updated_at FROM servicing.facilities WHERE mode=$1 AND id=$2`, h.cfg.Mode, h.cfg.FacilityID).Scan(&facilityTotal, &facilityPrincipal, &facilityReserved, &facilityOther, &facilityVersion, &facilityConfigVersion, &reserveBps, &facilityCaptured)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	facility := map[string]any{"facility_id": h.cfg.FacilityID, "currency": "USD", "total_cents": facilityTotal, "principal_owed_cents": facilityPrincipal, "reserved_principal_cents": facilityReserved, "other_commitments_cents": facilityOther, "configuration_version": facilityConfigVersion, "reserve_bps": reserveBps, "version": facilityVersion, "captured_at": facilityCaptured}
	customer := map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID, "principal_owed_cents": principal, "unpaid_fee_cents": fees, "reserved_principal_cents": reservedPrincipal, "reserved_fee_cents": reservedFees, "history_complete": historyComplete, "defaulted_at": defaultedAt, "active_hold": activeHold, "version": customerVersion, "captured_at": customerCaptured}
	invoiceRows, err := tx.Query(r.Context(), `SELECT customer_id,source_system,invoice_id,source_revision,currency,original_cents,unpaid_cents,reserved_cents,due_on,due_date_verified,allocation_complete,disputed,source_complete,version,imported_at FROM servicing.receivables WHERE mode=$1 AND customer_id=$2 ORDER BY source_system,invoice_id LIMIT $3`, h.cfg.Mode, customerID, maxServicingStateRows+1)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	invoices := []map[string]any{}
	for invoiceRows.Next() {
		var customer, system, invoice, revision, currency string
		var orig, unpaid, reserved, version int64
		var due *time.Time
		var verified, complete, disputed, sourceComplete bool
		var captured time.Time
		if invoiceRows.Scan(&customer, &system, &invoice, &revision, &currency, &orig, &unpaid, &reserved, &due, &verified, &complete, &disputed, &sourceComplete, &version, &captured) != nil {
			invoiceRows.Close()
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		invoices = append(invoices, map[string]any{"customer_id": customer, "source_system": system, "invoice_id": invoice, "source_revision": revision, "currency": currency, "original_cents": orig, "unpaid_cents": unpaid, "reserved_cents": reserved, "due_on": due, "due_date_verified": verified, "allocation_complete": complete, "disputed": disputed, "source_complete": sourceComplete, "version": version, "captured_at": captured})
	}
	if invoiceRows.Err() != nil {
		invoiceRows.Close()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	invoiceRows.Close()
	if len(invoices) > maxServicingStateRows {
		writeError(w, 503, "SERVICING_STATE_TOO_LARGE", "Customer invoice snapshot exceeds the bounded read limit")
		return
	}
	historyRows, err := tx.Query(r.Context(), `SELECT id::text,version,lifecycle,principal_original_cents,contract_fee_original_cents,principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,principal_paid_cents,contract_fee_paid_cents,late_fee_paid_cents,due_on,funded_at,closed_at,first_defaulted_on,terms_hash,originating_case_id,evaluation_id,offer_id::text,updated_at FROM servicing.bridges WHERE mode=$1 AND customer_id=$2 AND ever_funded AND lifecycle<>'VOID_UNFUNDED' ORDER BY created_at DESC,id DESC LIMIT $3`, h.cfg.Mode, customerID, maxServicingStateRows+1)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	history := []map[string]any{}
	for historyRows.Next() {
		var bid, lifecycle, terms, evaluation, offer string
		var v int64
		var originalPrincipal, originalFee, principalOwed, feeOwed, lateOwed, principalPaid, feePaid, latePaid int64
		var due time.Time
		var fundedAt, closedAt, defaulted *time.Time
		var caseID *string
		var updated time.Time
		if historyRows.Scan(&bid, &v, &lifecycle, &originalPrincipal, &originalFee, &principalOwed, &feeOwed, &lateOwed, &principalPaid, &feePaid, &latePaid, &due, &fundedAt, &closedAt, &defaulted, &terms, &caseID, &evaluation, &offer, &updated) != nil {
			historyRows.Close()
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		history = append(history, map[string]any{"bridge_id": bid, "customer_id": customerID, "account_ref": "customer:" + customerID, "version": v, "lifecycle": lifecycle, "ever_funded": true, "principal_original_cents": originalPrincipal, "contract_fee_original_cents": originalFee, "principal_owed_cents": principalOwed, "contract_fee_owed_cents": feeOwed, "late_fee_owed_cents": lateOwed, "principal_paid_cents": principalPaid, "contract_fee_paid_cents": feePaid, "late_fee_paid_cents": latePaid, "due_on": due.Format("2006-01-02"), "funded_at": fundedAt, "closed_at": closedAt, "first_defaulted_on": defaulted, "terms_hash": terms, "originating_case_id": caseID, "evaluation_id": evaluation, "offer_id": offer, "captured_at": updated})
	}
	if historyRows.Err() != nil {
		historyRows.Close()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	historyRows.Close()
	if len(history) > maxServicingStateRows {
		writeError(w, 503, "SERVICING_STATE_TOO_LARGE", "Customer funding history exceeds the bounded read limit")
		return
	}
	var ledgerVersion int64
	if err = tx.QueryRow(r.Context(), `SELECT last_sequence FROM servicing.change_counters WHERE mode=$1`, h.cfg.Mode).Scan(&ledgerVersion); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if tx.Commit(r.Context()) != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"mode": h.cfg.Mode, "source_binding_id": h.cfg.SourceBindingID, "as_of": h.now().UTC(), "ledger_version": ledgerVersion, "facility": facility, "customer": customer, "invoices": invoices, "history": history, "funding_authorized": false})
}
