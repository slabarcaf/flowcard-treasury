package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

const maxCollectionCandidates = 25

// CollectionCandidate is a metadata-only hint. Every binding is rechecked
// after the provider reads and under the financial row locks.
type CollectionCandidate struct {
	Mode                                         domain.Mode
	FacilityID, CustomerID, BridgeID, DepositID  string
	AccountRef, ProviderDepositID, CardID        string
	AuthorityID                                  string
	AuthorityVersion, BridgeVersion, DebtVersion int64
	TermsHash                                    string
}

// CollectionPlanFacts contains a bounded outside-transaction upper limit.
type CollectionPlanFacts struct {
	Candidate         CollectionCandidate
	FundingAccount    string
	FundedAt          time.Time
	DepositNet        int64
	DepositConsumed   int64
	FeeDebtCents      int64
	DebtCents         int64
	AuthorityLimit    int64
	AuthorityUsed     int64
	AuthorityReserved int64
	QuotedAmount      int64
	AuthorityStatus   string
	Source            string
	Destination       string
	PermittedKinds    []string
	Purposes          []string
	ProductCode       string
	MandateRef        string
	ValidFrom         time.Time
	ExpiresAt         time.Time
}

// CollectionReadEvidence carries only canonical outside-transaction reads.
type CollectionReadEvidence struct {
	Deposit provider.DepositObservation
	Balance provider.BalanceObservation
	Fee     provider.FundingFeeQuote
}

// CollectionPlanResult reports a durable intent or a persisted no-plan result.
type CollectionPlanResult struct {
	Created     bool
	OperationID string
	BlockedCode string
}

// CollectionDispatchBindings are immutable saved facts re-read before provider
// I/O. Current mapping and mandate are still revalidated under financial locks.
type CollectionDispatchBindings struct {
	Candidate          CollectionCandidate
	Operation          provider.Operation
	Terms              domain.Terms
	AttemptID          string
	FeeContractID      string
	ReservedDebitCents int64
	QuotedProviderFee  int64
	LeaseToken         string
	LeaseExpiresAt     time.Time
}

// LoadCollectionDispatchBindings reads a queued collection intent without
// locks so the worker can fetch fresh provider facts before authorization.
func (r *Repository) LoadCollectionDispatchBindings(ctx context.Context,
	claim OperationClaim) (CollectionDispatchBindings, error) {
	var result CollectionDispatchBindings
	if claim.State != domain.OperationQueued ||
		(claim.Kind != "COLLECT_FEE" && claim.Kind != "COLLECT_PRINCIPAL") ||
		claim.LeaseToken == "" || claim.LeaseExpiresAt.IsZero() {
		return result, ErrOperationClaimStale
	}
	var mode, kind, state, operationID, bridgeID, facilityID, customerID string
	var depositID, providerDepositID, accountRef, destination, requestID, payloadHash, currency string
	var termsHash, feeContract string
	var amount, lateFee, contractFee, principal, operationVersion, bridgeVersion, debtVersion int64
	var reservedDebit, providerFee int64
	var authorityID *string
	var authorityVersion *int64
	var leaseToken *string
	var leaseExpiry *time.Time
	var termsJSON []byte
	err := r.pool.QueryRow(ctx, `SELECT o.mode,o.kind,o.state,o.id::text,o.version,o.request_id,
		o.payload_hash,o.source_account_ref,o.destination_account_ref,o.amount_cents,o.currency,
		o.terms_hash,o.late_fee_cents,o.contract_fee_cents,o.principal_cents,
		o.authority_id,o.authority_version,o.lease_token::text,o.lease_expires_at,
		b.facility_id,b.customer_id,b.id::text,b.version,b.collection_debt_version,b.terms_hash,f.funding_account_ref,
		ca.id::text,ca.reserved_debit_cents,ca.provider_fee_cents,ca.fee_contract_id,
		d.id::text,d.provider_deposit_id,d.account_ref,o.terms_hash,ofr.terms_json
		FROM servicing.operations o JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		JOIN servicing.collection_allocations a ON a.mode=o.mode AND a.operation_id=o.id
		JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
		JOIN servicing.deposits d ON d.mode=ca.mode AND d.id=ca.deposit_id
		JOIN servicing.offers ofr ON ofr.mode=b.mode AND ofr.id=b.offer_id
		WHERE o.mode=$1 AND o.id=$2 AND o.kind=$3 AND o.state='QUEUED' AND o.version=$4
		AND o.lease_token=$5::uuid AND o.lease_expires_at=$6
		ORDER BY a.allocation_index LIMIT 1`, string(claim.Mode), claim.OperationID,
		claim.Kind, claim.Version, claim.LeaseToken, claim.LeaseExpiresAt).Scan(
		&mode, &kind, &state, &operationID, &operationVersion, &requestID, &payloadHash,
		&accountRef, &destination, &amount, &currency, &termsHash, &lateFee, &contractFee,
		&principal, &authorityID, &authorityVersion, &leaseToken, &leaseExpiry,
		&facilityID, &customerID, &bridgeID, &bridgeVersion, &debtVersion, &result.Candidate.TermsHash,
		&result.Operation.DestinationAccountRef, &result.AttemptID, &reservedDebit, &providerFee,
		&feeContract, &depositID, &providerDepositID, &result.Candidate.AccountRef,
		&result.Candidate.TermsHash, &termsJSON)
	if err != nil {
		return CollectionDispatchBindings{}, ErrOperationClaimStale
	}
	var terms domain.Terms
	if json.Unmarshal(termsJSON, &terms) != nil || terms.Validate() != nil {
		return CollectionDispatchBindings{}, errors.New("collection_dispatch_terms_invalid")
	}
	termsHashCalculated, err := terms.Hash()
	if err != nil || mode != string(claim.Mode) || kind != claim.Kind || state != "QUEUED" ||
		operationID != claim.OperationID || operationVersion != claim.Version || bridgeID != claim.BridgeID ||
		bridgeVersion != claim.BridgeVersion ||
		facilityID != claim.FacilityID || customerID != claim.CustomerID || currency != "USD" || amount <= 0 ||
		termsHashCalculated != termsHash || termsHash != result.Candidate.TermsHash ||
		terms.CollectionAuthorityID == "" || authorityID == nil || authorityVersion == nil ||
		*authorityID != terms.CollectionAuthorityID || *authorityVersion != terms.CollectionAuthorityVersion ||
		depositID == "" || providerDepositID == "" || result.Candidate.AccountRef != terms.BorrowerAccountRef ||
		result.Operation.DestinationAccountRef == "" || result.Operation.DestinationAccountRef != terms.FundingAccountRef ||
		reservedDebit != amount+providerFee || providerFee < 0 || !provider.ValidCollectionFeeQuote(mode, provider.FundingFeeQuote{ContractID: feeContract, Cents: providerFee}) ||
		leaseToken == nil || *leaseToken != claim.LeaseToken || leaseExpiry == nil || !leaseExpiry.Equal(claim.LeaseExpiresAt) {
		return CollectionDispatchBindings{}, errors.New("collection_dispatch_binding_invalid")
	}
	result.Candidate = CollectionCandidate{Mode: claim.Mode, FacilityID: facilityID,
		CustomerID: customerID, BridgeID: bridgeID, DepositID: depositID,
		AccountRef: accountRef, ProviderDepositID: providerDepositID, CardID: terms.CardRef,
		AuthorityID: terms.CollectionAuthorityID, AuthorityVersion: terms.CollectionAuthorityVersion,
		BridgeVersion: bridgeVersion, DebtVersion: debtVersion, TermsHash: termsHash}
	result.Terms = terms
	result.FeeContractID = feeContract
	result.ReservedDebitCents = reservedDebit
	result.QuotedProviderFee = providerFee
	result.LeaseToken, result.LeaseExpiresAt = *leaseToken, *leaseExpiry
	result.Operation = provider.Operation{ID: operationID, Kind: provider.OperationKind(kind),
		Mode: mode, SourceAccountRef: accountRef, DestinationAccountRef: destination,
		AmountCents: amount, Currency: currency, RequestID: requestID,
		PayloadHash: payloadHash, BridgeID: bridgeID, TriggerID: providerDepositID,
		LateFeeCents: lateFee, ContractFeeCents: contractFee, PrincipalCents: principal}
	computedHash, err := provider.PayloadHash(result.Operation)
	if err != nil || computedHash != payloadHash {
		return CollectionDispatchBindings{}, errors.New("collection_dispatch_payload_invalid")
	}
	return result, nil
}

type collectionTriggerQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func authorityUsage(ctx context.Context, queryer collectionTriggerQueryer,
	mode domain.Mode, authorityID string) (int64, int64, error) {
	var consumedText, reservedText string
	err := queryer.QueryRow(ctx, `SELECT COALESCE(sum(consumed_debit_cents),0)::text,
		COALESCE(sum(reserved_debit_cents),0)::text FROM servicing.authorities
		WHERE mode=$1 AND id=$2`, string(mode), authorityID).Scan(&consumedText, &reservedText)
	if err != nil {
		return 0, 0, errors.New("authority_usage_read_failed")
	}
	consumed, consumedErr := strconv.ParseInt(consumedText, 10, 64)
	reserved, reservedErr := strconv.ParseInt(reservedText, 10, 64)
	if consumedErr != nil || reservedErr != nil || consumed < 0 || reserved < 0 ||
		consumed > math.MaxInt64-reserved {
		return 0, 0, errors.New("authority_usage_overflow")
	}
	return consumed, reserved, nil
}

// ReadAuthorityUsage applies the collection guard's checked cumulative usage
// interpretation to a read-only servicing snapshot.
func ReadAuthorityUsage(ctx context.Context, tx pgx.Tx, mode domain.Mode, authorityID string) (int64, int64, error) {
	return authorityUsage(ctx, tx, mode, authorityID)
}

// loadCollectionTrigger derives the provider trigger from immutable saved
// allocations and requires exactly one account/customer/bridge-bound attempt.
func loadCollectionTrigger(ctx context.Context, queryer collectionTriggerQueryer,
	mode domain.Mode, operationID, customerID, accountRef, bridgeID string) (string, error) {
	var attemptCount int
	var trigger, savedCustomer, savedAccount, savedBridge string
	err := queryer.QueryRow(ctx, `SELECT count(DISTINCT ca.id),COALESCE(min(d.provider_deposit_id),''),
		COALESCE(min(ca.customer_id),''),COALESCE(min(ca.account_ref),''),
		COALESCE(min(ca.bridge_id::text),'')
		FROM servicing.collection_allocations a
		JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
		JOIN servicing.deposits d ON d.mode=ca.mode AND d.id=ca.deposit_id
		WHERE a.mode=$1 AND a.operation_id=$2`, string(mode), operationID).Scan(
		&attemptCount, &trigger, &savedCustomer, &savedAccount, &savedBridge)
	if err != nil || attemptCount != 1 || trigger == "" || savedCustomer != customerID ||
		savedAccount != accountRef || savedBridge != bridgeID {
		return "", errors.New("collection_trigger_binding_invalid")
	}
	return trigger, nil
}

// DiscoverCollectionCandidates returns due deposit/bridge pairs with debt and
// no unresolved debit for the customer account.
func (r *Repository) DiscoverCollectionCandidates(ctx context.Context, mode domain.Mode,
	facilityID string, limit int) ([]CollectionCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || facilityID == "" || limit < 1 {
		return nil, errors.New("collection_discovery_request_invalid")
	}
	if limit > maxCollectionCandidates {
		limit = maxCollectionCandidates
	}
	rows, err := r.pool.Query(ctx, `SELECT b.mode,b.facility_id,b.customer_id,b.id::text,d.id::text,
		d.account_ref,d.provider_deposit_id,o.terms_json->>'card_ref',
		o.terms_json->>'collection_authority_id',
		(o.terms_json->>'collection_authority_version')::bigint,b.version,b.collection_debt_version,b.terms_hash
		FROM servicing.bridges b
		JOIN servicing.customers c ON c.mode=b.mode AND c.id=b.customer_id
		JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id
		JOIN servicing.deposits d ON d.mode=b.mode AND d.customer_id=b.customer_id
			AND d.account_ref=b.canonical_account_id AND d.status='SETTLED'
			AND d.kind='EXTERNAL_SETTLED' AND d.settled_at>b.funded_at
			AND d.net_cents>COALESCE((SELECT sum(ca.settled_debit_cents)+
				sum(CASE WHEN ca.state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')
					THEN ca.reserved_debit_cents ELSE 0 END)
				FROM servicing.collection_attempts ca
				WHERE ca.mode=d.mode AND ca.deposit_id=d.id),0)
		WHERE b.mode=$1 AND b.facility_id=$2 AND b.ever_funded
		AND b.lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD') AND NOT c.active_hold
		AND b.principal_owed_cents+b.contract_fee_owed_cents+b.late_fee_owed_cents>0
		AND b.backing_deficit_cents=0
		AND NOT (b.hold_reasons && ARRAY['DEPOSIT_SOURCE_CONFLICT','COLLECTION_AUTHORITY_BLOCKED',
			'COLLECTION_REVIEW_REQUIRED','ACCRUAL_CORRECTION_REQUIRED','ACCRUAL_CATCHUP_PENDING',
			'RECEIVABLE_BACKING_CONFLICT','REFUND_PAYABLE','REVERSAL_REVIEW']::text[])
		AND NOT EXISTS (SELECT 1 FROM servicing.collection_attempts ca
			WHERE ca.mode=b.mode AND ca.customer_id=b.customer_id AND ca.account_ref=d.account_ref
			AND ca.state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD'))
		AND NOT EXISTS (SELECT 1 FROM servicing.receivables r
			JOIN servicing.bridge_invoice_allocations a ON a.mode=r.mode
			AND a.source_system=r.source_system AND a.invoice_id=r.invoice_id
			WHERE a.mode=b.mode AND a.bridge_id=b.id AND r.backing_hold)
		AND NOT EXISTS (SELECT 1 FROM servicing.collection_plan_checks pc
			WHERE pc.mode=d.mode AND pc.deposit_id=d.id AND pc.bridge_id=b.id
			AND pc.result_code IN ('INSUFFICIENT_VERIFIED_LIQUIDITY','COLLECTION_LIQUIDITY_CHANGED')
			AND pc.recorded_at>clock_timestamp()-interval '30 seconds')
		ORDER BY b.due_on,b.created_at,d.settled_at,d.id LIMIT $3`, string(mode), facilityID, limit)
	if err != nil {
		return nil, errors.New("collection_discovery_failed")
	}
	defer rows.Close()
	var candidates []CollectionCandidate
	for rows.Next() {
		var item CollectionCandidate
		if rows.Scan(&item.Mode, &item.FacilityID, &item.CustomerID, &item.BridgeID,
			&item.DepositID, &item.AccountRef, &item.ProviderDepositID, &item.CardID,
			&item.AuthorityID, &item.AuthorityVersion, &item.BridgeVersion, &item.DebtVersion,
			&item.TermsHash) != nil {
			return nil, errors.New("collection_discovery_failed")
		}
		candidates = append(candidates, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("collection_discovery_failed")
	}
	return candidates, nil
}

// LoadCollectionPlanFacts reads saved authority and debt facts before provider
// I/O. PlanCollection repeats all material checks under row locks.
func (r *Repository) LoadCollectionPlanFacts(ctx context.Context,
	candidate CollectionCandidate) (CollectionPlanFacts, error) {
	var facts CollectionPlanFacts
	facts.Candidate = candidate
	var termsJSON []byte
	var debtPrincipal, debtContract, debtLate int64
	var borrowerAccount string
	err := r.pool.QueryRow(ctx, `SELECT f.funding_account_ref,d.net_cents,b.funded_at,
		b.collection_debt_version,b.canonical_account_id,b.principal_owed_cents,
		b.contract_fee_owed_cents,b.late_fee_owed_cents,b.terms_hash,o.terms_json
		FROM servicing.bridges b JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		JOIN servicing.deposits d ON d.mode=b.mode AND d.customer_id=b.customer_id
			AND d.account_ref=b.canonical_account_id
		JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id
		WHERE b.mode=$1 AND b.id=$2 AND d.id=$3 AND b.facility_id=$4 AND b.customer_id=$5
		AND d.status='SETTLED' AND d.kind='EXTERNAL_SETTLED'`, string(candidate.Mode), candidate.BridgeID,
		candidate.DepositID, candidate.FacilityID, candidate.CustomerID).Scan(&facts.FundingAccount,
		&facts.DepositNet, &facts.FundedAt, &facts.Candidate.DebtVersion, &borrowerAccount,
		&debtPrincipal, &debtContract, &debtLate,
		&facts.Candidate.TermsHash, &termsJSON)
	if err != nil {
		return facts, errors.New("collection_plan_scope_unavailable")
	}
	var terms domain.Terms
	if json.Unmarshal(termsJSON, &terms) != nil || terms.Validate() != nil {
		return facts, errors.New("collection_terms_binding_invalid")
	}
	termsHash, termsErr := terms.Hash()
	if termsErr != nil || termsHash != facts.Candidate.TermsHash || terms.CardRef != candidate.CardID ||
		terms.CustomerID != candidate.CustomerID || terms.FacilityID != candidate.FacilityID ||
		terms.BorrowerAccountRef != candidate.AccountRef || borrowerAccount != candidate.AccountRef ||
		terms.CollectionAuthorityID != candidate.AuthorityID ||
		terms.CollectionAuthorityVersion != candidate.AuthorityVersion {
		return facts, errors.New("collection_terms_binding_invalid")
	}
	if debtPrincipal < 0 || debtContract < 0 || debtLate < 0 ||
		debtPrincipal > math.MaxInt64-debtContract ||
		debtPrincipal+debtContract > math.MaxInt64-debtLate {
		return facts, errors.New("collection_debt_overflow")
	}
	facts.DebtCents = debtPrincipal + debtContract + debtLate
	facts.FeeDebtCents = debtContract + debtLate
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(sum(settled_debit_cents),0)+
		COALESCE(sum(CASE WHEN state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')
			THEN reserved_debit_cents ELSE 0 END),0)
		FROM servicing.collection_attempts WHERE mode=$1 AND deposit_id=$2`,
		string(candidate.Mode), candidate.DepositID).Scan(&facts.DepositConsumed); err != nil {
		return facts, errors.New("collection_budget_read_failed")
	}
	err = r.pool.QueryRow(ctx, `SELECT source_account_ref,destination_account_ref,product_code,
		mandate_reference,permitted_kinds,permitted_purposes,maximum_debit_cents,
		status,valid_from,expires_at
		FROM servicing.authorities WHERE mode=$1 AND id=$2 AND version=$3
		AND NOT EXISTS(SELECT 1 FROM servicing.authorities newer WHERE newer.mode=$1
			AND newer.id=$2 AND newer.version>$3)`, string(candidate.Mode), candidate.AuthorityID,
		candidate.AuthorityVersion).Scan(&facts.Source, &facts.Destination, &facts.ProductCode,
		&facts.MandateRef, &facts.PermittedKinds, &facts.Purposes, &facts.AuthorityLimit,
		&facts.AuthorityStatus, &facts.ValidFrom, &facts.ExpiresAt)
	if err != nil {
		return facts, errors.New("collection_authority_unavailable")
	}
	facts.AuthorityUsed, facts.AuthorityReserved, err = authorityUsage(ctx, r.pool,
		candidate.Mode, candidate.AuthorityID)
	if err != nil {
		return facts, err
	}
	return facts, nil
}

// PlanCollection atomically binds fresh provider evidence, a B-to-A authority,
// budget holds and one immutable fee/principal operation.
func (r *Repository) PlanCollection(ctx context.Context, input CollectionPlanFacts,
	evidence CollectionReadEvidence, pricing domain.Pricing, policyHash string,
	clock func() time.Time) (CollectionPlanResult, error) {
	candidate := input.Candidate
	if (candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox) || candidate.FacilityID == "" || candidate.CustomerID == "" ||
		candidate.BridgeID == "" || candidate.DepositID == "" || candidate.AccountRef == "" ||
		candidate.ProviderDepositID == "" || candidate.CardID == "" || clock == nil ||
		pricing.Validate() != nil || !validHash(policyHash) {
		return CollectionPlanResult{}, errors.New("collection_plan_request_invalid")
	}
	now := clock().UTC()
	if now.IsZero() {
		return CollectionPlanResult{}, errors.New("collection_clock_invalid")
	}
	if !validCollectionObservation(evidence, input, now) {
		return CollectionPlanResult{}, errors.New("collection_provider_evidence_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	facility, err := lockFundingFacility(txCtx, tx, string(candidate.Mode), candidate.FacilityID)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	if facility.fundingAccount != input.FundingAccount || facility.fundingAccount == candidate.AccountRef {
		return CollectionPlanResult{}, errors.New("collection_facility_binding_changed")
	}
	if _, err := lockFundingCustomer(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return CollectionPlanResult{}, errors.New("collection_customer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(txCtx, tx, string(candidate.Mode), candidate.CustomerID)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	if err := lockCustomerReservations(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return CollectionPlanResult{}, err
	}
	if err := lockCustomerInvoices(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return CollectionPlanResult{}, err
	}
	var depositCustomer, depositAccount, bridgeAccount, depositProviderID, depositStatus, depositKind string
	var depositNet int64
	var depositSettledAt, fundedAt time.Time
	err = tx.QueryRow(txCtx, `SELECT d.customer_id,d.account_ref,b.canonical_account_id,
		d.provider_deposit_id,d.net_cents,d.status,d.kind,d.settled_at,b.funded_at
		FROM servicing.deposits d JOIN servicing.bridges b ON b.mode=d.mode AND b.customer_id=d.customer_id
		WHERE d.mode=$1 AND d.id=$2 AND b.id=$3 AND b.facility_id=$4
		FOR UPDATE OF d`, string(candidate.Mode), candidate.DepositID,
		candidate.BridgeID, candidate.FacilityID).Scan(&depositCustomer, &depositAccount,
		&bridgeAccount, &depositProviderID, &depositNet, &depositStatus, &depositKind,
		&depositSettledAt, &fundedAt)
	if err != nil {
		return CollectionPlanResult{}, errors.New("collection_deposit_lock_failed")
	}
	depositObservationID, err := insertCollectionDepositObservation(txCtx, tx, candidate.Mode, evidence.Deposit)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	balanceObservationID, err := insertCollectionBalanceObservation(txCtx, tx, candidate.Mode, evidence.Balance)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	canonicalMatches := depositCustomer == candidate.CustomerID && depositAccount == candidate.AccountRef &&
		bridgeAccount == candidate.AccountRef &&
		depositProviderID == candidate.ProviderDepositID && depositStatus == "SETTLED" &&
		depositKind == "EXTERNAL_SETTLED" && evidence.Deposit.AccountRef == depositAccount &&
		evidence.Deposit.CustomerID == depositCustomer && evidence.Deposit.DepositID == depositProviderID &&
		evidence.Deposit.Currency == "USD" && evidence.Deposit.Status == "SETTLED" &&
		evidence.Deposit.Kind == "EXTERNAL_SETTLED" && evidence.Deposit.NetCents == depositNet &&
		evidence.Deposit.SettledAt != nil && evidence.Deposit.SettledAt.Equal(depositSettledAt) &&
		depositSettledAt.After(fundedAt)
	if !canonicalMatches {
		if depositStatus == "SETTLED" {
			if _, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='HELD'
				WHERE mode=$1 AND id=$2 AND status='SETTLED'`, string(candidate.Mode), candidate.DepositID); err != nil {
				return CollectionPlanResult{}, errors.New("collection_deposit_hold_failed")
			}
		}
		if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
			bridgeIDs, "DEPOSIT_SOURCE_CONFLICT", map[string]any{"deposit_id": candidate.DepositID,
				"provider_deposit_id":     evidence.Deposit.DepositID,
				"provider_observation_id": depositObservationID,
				"movement_certainty":      "VERIFIED_CHANGED_DEPOSIT"}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "DEPOSIT_SOURCE_CONFLICT"}, nil
	}
	var customerHeld bool
	if err := tx.QueryRow(txCtx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
		string(candidate.Mode), candidate.CustomerID).Scan(&customerHeld); err != nil {
		return CollectionPlanResult{}, errors.New("collection_customer_read_failed")
	}
	if customerHeld {
		return CollectionPlanResult{BlockedCode: "CUSTOMER_HELD"}, nil
	}
	var bridgeVersion, bridgeDebtVersion int64
	var lifecycle string
	var debtPrincipal, debtContract, debtLate int64
	var termsJSON []byte
	var termsHash string
	var holds []string
	err = tx.QueryRow(txCtx, `SELECT b.version,b.lifecycle,b.principal_owed_cents,
		b.contract_fee_owed_cents,b.late_fee_owed_cents,b.hold_reasons,b.terms_hash,o.terms_json
		FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id
		WHERE b.mode=$1 AND b.id=$2 AND b.customer_id=$3 AND b.facility_id=$4
		FOR UPDATE OF b`, string(candidate.Mode), candidate.BridgeID,
		candidate.CustomerID, candidate.FacilityID).Scan(&bridgeVersion, &lifecycle,
		&debtPrincipal, &debtContract, &debtLate, &holds, &termsHash, &termsJSON)
	if err != nil {
		return CollectionPlanResult{}, errors.New("collection_bridge_lock_failed")
	}
	if termsHash != candidate.TermsHash || bridgeVersion != candidate.BridgeVersion ||
		(lifecycle != "ACTIVE" && lifecycle != "COLLECTING" && lifecycle != "REOPENED_HELD") {
		return CollectionPlanResult{BlockedCode: "COLLECTION_BINDING_CHANGED"}, nil
	}
	var terms domain.Terms
	if json.Unmarshal(termsJSON, &terms) != nil || terms.Validate() != nil {
		return CollectionPlanResult{}, errors.New("collection_terms_binding_invalid")
	}
	if terms.PolicyHash != policyHash || terms.CollectionAuthorityID != candidate.AuthorityID ||
		terms.CollectionAuthorityVersion != candidate.AuthorityVersion {
		return CollectionPlanResult{}, errors.New("collection_terms_binding_invalid")
	}
	lockedMapping, fingerprint, err := LockVerifiedMapping(txCtx, tx, candidate.CustomerID,
		candidate.AccountRef, candidate.CardID)
	if err != nil || lockedMapping.CardID != candidate.CardID || fingerprint != terms.MappingFingerprint ||
		lockedMapping.RosterAccountID != candidate.AccountRef {
		if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
			bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "MAPPING_CONFLICT",
				"deposit_id": candidate.DepositID}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "MAPPING_CONFLICT"}, nil
	}
	var authoritySource, authorityDestination, productCode, mandateRef, authorityStatus string
	var permittedKinds, purposes []string
	var maximum, versionConsumed, versionReserved int64
	var validFrom, expiresAt time.Time
	err = tx.QueryRow(txCtx, `SELECT source_account_ref,destination_account_ref,product_code,
		mandate_reference,permitted_kinds,permitted_purposes,maximum_debit_cents,
		consumed_debit_cents,reserved_debit_cents,status,valid_from,expires_at
		FROM servicing.authorities WHERE mode=$1 AND id=$2 AND version=$3
		AND NOT EXISTS(SELECT 1 FROM servicing.authorities newer WHERE newer.mode=$1
			AND newer.id=$2 AND newer.version>$3) FOR UPDATE`, string(candidate.Mode),
		terms.CollectionAuthorityID, terms.CollectionAuthorityVersion).Scan(&authoritySource,
		&authorityDestination, &productCode, &mandateRef, &permittedKinds, &purposes,
		&maximum, &versionConsumed, &versionReserved, &authorityStatus, &validFrom, &expiresAt)
	if err != nil || authorityStatus != "ACTIVE" || productCode != "CONNECTED_ACCOUNT_TRANSFERS" ||
		mandateRef == "" || authoritySource != candidate.AccountRef || authorityDestination != input.FundingAccount ||
		!collectionHasKind(permittedKinds, "COLLECT_FEE") && !collectionHasKind(permittedKinds, "COLLECT_PRINCIPAL") ||
		!now.Before(expiresAt) || now.Before(validFrom) || maximum < 0 ||
		versionConsumed < 0 || versionReserved < 0 {
		if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
			bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "CURRENT_MANDATE_UNAVAILABLE",
				"deposit_id": candidate.DepositID}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
	}
	aggregateConsumed, aggregateReserved, err := authorityUsage(txCtx, tx, candidate.Mode,
		terms.CollectionAuthorityID)
	if err != nil {
		if err.Error() == "authority_usage_overflow" {
			if holdErr := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
				bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "CUMULATIVE_MANDATE_USAGE_INVALID",
					"authority_id": terms.CollectionAuthorityID}); holdErr != nil {
				return CollectionPlanResult{}, holdErr
			}
			if commitErr := tx.Commit(txCtx); commitErr != nil {
				return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
			}
			return CollectionPlanResult{BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
		}
		return CollectionPlanResult{}, err
	}
	if aggregateConsumed >= maximum || aggregateReserved >= maximum-aggregateConsumed {
		if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
			bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "CUMULATIVE_MANDATE_LIMIT_REACHED",
				"authority_id":      terms.CollectionAuthorityID,
				"authority_version": terms.CollectionAuthorityVersion}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
	}
	unresolved, err := hasUnresolvedCollectionDebit(txCtx, tx, candidate.Mode,
		candidate.CustomerID, candidate.AccountRef, "")
	if err != nil {
		return CollectionPlanResult{}, err
	}
	if unresolved {
		return CollectionPlanResult{BlockedCode: "COLLECTION_DEBIT_UNRESOLVED"}, nil
	}
	accrual, err := AccrueThrough(txCtx, tx, AccrualOptions{Mode: candidate.Mode,
		Pricing: pricing, ExpectedPolicyHash: terms.PolicyHash}, candidate.BridgeID, now)
	if err != nil {
		return CollectionPlanResult{}, err
	}
	if !accrual.Current || accrual.Pending || accrual.CorrectionRequired {
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "ACCRUAL_CATCHUP_PENDING"}, nil
	}
	if err := tx.QueryRow(txCtx, `SELECT version,collection_debt_version,principal_owed_cents,contract_fee_owed_cents,
		late_fee_owed_cents,hold_reasons FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(candidate.Mode), candidate.BridgeID).Scan(&bridgeVersion, &bridgeDebtVersion, &debtPrincipal,
		&debtContract, &debtLate, &holds); err != nil {
		return CollectionPlanResult{}, errors.New("collection_post_accrual_debt_read_failed")
	}
	candidate.DebtVersion = bridgeDebtVersion
	for _, blocker := range []string{"DEPOSIT_SOURCE_CONFLICT", "COLLECTION_AUTHORITY_BLOCKED",
		"COLLECTION_REVIEW_REQUIRED", "ACCRUAL_CORRECTION_REQUIRED", "ACCRUAL_CATCHUP_PENDING",
		"RECEIVABLE_BACKING_CONFLICT", "REFUND_PAYABLE", "REVERSAL_REVIEW"} {
		if containsReason(holds, blocker) {
			return CollectionPlanResult{BlockedCode: blocker}, nil
		}
	}
	if debtPrincipal < 0 || debtContract < 0 || debtLate < 0 || debtPrincipal > math.MaxInt64-debtContract ||
		debtPrincipal+debtContract > math.MaxInt64-debtLate {
		return CollectionPlanResult{}, errors.New("collection_debt_overflow")
	}
	debt := debtPrincipal + debtContract + debtLate
	budgetUsed, err := collectionDepositUsage(txCtx, tx, candidate.Mode, candidate.DepositID, "")
	if err != nil {
		return CollectionPlanResult{}, err
	}
	authorityRemaining := maximum - aggregateConsumed - aggregateReserved
	if budgetUsed < 0 || depositNet < budgetUsed {
		return CollectionPlanResult{}, errors.New("collection_budget_invalid")
	}
	if debt == 0 {
		return CollectionPlanResult{BlockedCode: "COLLECTION_NO_DEBT"}, nil
	}
	if !collectionFreshTime(evidence.Balance.ObservedAt, now) || evidence.Balance.AccountRef != candidate.AccountRef ||
		evidence.Balance.Currency != "USD" || evidence.Balance.AvailableCents < 0 ||
		evidence.Fee.Cents < 0 || !provider.ValidCollectionFeeQuote(string(candidate.Mode), evidence.Fee) ||
		input.FundingAccount == "" {
		return CollectionPlanResult{BlockedCode: "COLLECTION_FEE_OR_BALANCE_UNKNOWN"}, nil
	}
	rawCap := collectionMin(depositNet-budgetUsed, evidence.Balance.AvailableCents, debt, authorityRemaining)
	if rawCap <= evidence.Fee.Cents {
		return r.recordCollectionNoPlan(txCtx, tx, candidate, bridgeVersion, terms.CollectionAuthorityID,
			terms.CollectionAuthorityVersion, depositObservationID, balanceObservationID,
			evidence, "INSUFFICIENT_VERIFIED_LIQUIDITY", now)
	}
	amount := rawCap - evidence.Fee.Cents
	kind := "COLLECT_FEE"
	lateFee, contractFee, principal := int64(0), int64(0), int64(0)
	feeOwed := debtLate + debtContract
	if feeOwed > 0 {
		if !collectionHasKind(permittedKinds, kind) || !collectionPurposeAllowed(purposes, kind) {
			if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
				bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "FEE_PERMISSION_MISSING",
					"deposit_id": candidate.DepositID}); err != nil {
				return CollectionPlanResult{}, err
			}
			if err := tx.Commit(txCtx); err != nil {
				return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
			}
			return CollectionPlanResult{BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
		}
		amount = collectionMin(amount, feeOwed)
		lateFee = collectionMin(amount, debtLate)
		contractFee = amount - lateFee
	} else {
		kind, principal = "COLLECT_PRINCIPAL", collectionMin(amount, debtPrincipal)
		amount = principal
		if principal <= 0 || !collectionHasKind(permittedKinds, kind) || !collectionPurposeAllowed(purposes, kind) {
			if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
				bridgeIDs, "COLLECTION_AUTHORITY_BLOCKED", map[string]any{"reason": "PRINCIPAL_PERMISSION_MISSING",
					"deposit_id": candidate.DepositID}); err != nil {
				return CollectionPlanResult{}, err
			}
			if err := tx.Commit(txCtx); err != nil {
				return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
			}
			return CollectionPlanResult{BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
		}
	}
	if amount <= 0 || evidence.Fee.Cents > math.MaxInt64-amount {
		return r.recordCollectionNoPlan(txCtx, tx, candidate, bridgeVersion, terms.CollectionAuthorityID,
			terms.CollectionAuthorityVersion, depositObservationID, balanceObservationID,
			evidence, "INSUFFICIENT_VERIFIED_LIQUIDITY", now)
	}
	if input.QuotedAmount < amount || candidate.Mode == domain.ModeSandbox && input.QuotedAmount != amount {
		return r.recordCollectionNoPlan(txCtx, tx, candidate, bridgeVersion, terms.CollectionAuthorityID,
			terms.CollectionAuthorityVersion, depositObservationID, balanceObservationID,
			evidence, "COLLECTION_FEE_QUOTE_SCOPE_INVALID", now)
	}
	reservedDebit := amount + evidence.Fee.Cents
	key := collectionEvidenceKey(evidence.Deposit, evidence.Balance, candidate.BridgeID, bridgeDebtVersion,
		terms.CollectionAuthorityID, terms.CollectionAuthorityVersion)
	plannedOn := utcDate(now)
	var duplicate bool
	if err := tx.QueryRow(txCtx, `SELECT EXISTS(SELECT 1 FROM servicing.collection_plan_checks
		WHERE mode=$1 AND deposit_id=$2 AND planned_on=$3 AND evidence_key=$4)`,
		string(candidate.Mode), candidate.DepositID, plannedOn, key).Scan(&duplicate); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_evidence_read_failed")
	}
	if duplicate {
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_EVIDENCE_ALREADY_PLANNED"}, nil
	}
	var dailyChecks int
	if err := tx.QueryRow(txCtx, `SELECT count(*) FROM servicing.collection_plan_checks
		WHERE mode=$1 AND deposit_id=$2 AND planned_on=$3`, string(candidate.Mode),
		candidate.DepositID, plannedOn).Scan(&dailyChecks); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_limit_read_failed")
	}
	if dailyChecks >= 3 {
		if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
			bridgeIDs, "COLLECTION_REVIEW_REQUIRED", map[string]any{"reason": "REPLAN_LIMIT_REACHED",
				"deposit_id": candidate.DepositID}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_REPLAN_LIMIT_REACHED"}, nil
	}
	operationID, err := randomLeaseToken()
	if err != nil {
		return CollectionPlanResult{}, err
	}
	attemptID, err := randomLeaseToken()
	if err != nil {
		return CollectionPlanResult{}, err
	}
	requestPrefix := "fee-"
	operationKind := provider.OperationCollectFee
	if kind == "COLLECT_PRINCIPAL" {
		requestPrefix, operationKind = "principal-", provider.OperationCollectPrincipal
	}
	requestID := requestPrefix + operationID
	operation := provider.Operation{ID: operationID, Kind: operationKind, Mode: string(candidate.Mode),
		SourceAccountRef: candidate.AccountRef, DestinationAccountRef: input.FundingAccount,
		AmountCents: amount, Currency: "USD", RequestID: requestID, BridgeID: candidate.BridgeID,
		TriggerID: candidate.ProviderDepositID, LateFeeCents: lateFee,
		ContractFeeCents: contractFee, PrincipalCents: principal}
	operation.PayloadHash, err = provider.PayloadHash(operation)
	if err != nil {
		return CollectionPlanResult{}, errors.New("collection_payload_invalid")
	}
	if _, err := tx.Exec(txCtx, `INSERT INTO servicing.operations(mode,id,bridge_id,kind,state,
		request_id,payload_hash,source_account_ref,destination_account_ref,amount_cents,currency,
		terms_hash,authority_id,authority_version,late_fee_cents,contract_fee_cents,principal_cents,
		reserved_provider_fee_cents,next_check_at)
		VALUES($1,$2,$3,$4,'QUEUED',$5,$6,$7,$8,$9,'USD',$10,$11,$12,$13,$14,$15,$16,clock_timestamp())`,
		string(candidate.Mode), operationID, candidate.BridgeID, kind, requestID, operation.PayloadHash,
		candidate.AccountRef, input.FundingAccount, amount, termsHash, terms.CollectionAuthorityID,
		terms.CollectionAuthorityVersion, lateFee, contractFee, principal, evidence.Fee.Cents); err != nil {
		return CollectionPlanResult{}, errors.New("collection_operation_insert_failed")
	}
	if _, err := tx.Exec(txCtx, `INSERT INTO servicing.collection_attempts(mode,id,bridge_id,
		deposit_id,customer_id,account_ref,authority_id,authority_version,reserved_debit_cents,
		provider_fee_cents,state,deposit_observation_id,balance_observation_id,evidence_key,
		planned_on,fee_contract_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'QUEUED',$11,$12,$13,$14,$15)`,
		string(candidate.Mode), attemptID, candidate.BridgeID, candidate.DepositID,
		candidate.CustomerID, candidate.AccountRef, terms.CollectionAuthorityID,
		terms.CollectionAuthorityVersion, reservedDebit, evidence.Fee.Cents,
		depositObservationID, balanceObservationID, key, plannedOn, evidence.Fee.ContractID); err != nil {
		return CollectionPlanResult{}, errors.New("collection_attempt_insert_failed")
	}
	allocationIndex := 0
	for _, allocation := range []struct {
		bucket string
		amount int64
	}{{"LATE_FEE", lateFee}, {"CONTRACT_FEE", contractFee}, {"PRINCIPAL", principal}} {
		if allocation.amount == 0 {
			continue
		}
		if _, err := tx.Exec(txCtx, `INSERT INTO servicing.collection_allocations(mode,attempt_id,
			operation_id,allocation_index,bucket,amount_cents) VALUES($1,$2,$3,$4,$5,$6)`,
			string(candidate.Mode), attemptID, operationID, allocationIndex,
			allocation.bucket, allocation.amount); err != nil {
			return CollectionPlanResult{}, errors.New("collection_allocation_insert_failed")
		}
		allocationIndex++
	}
	reserveTag, err := tx.Exec(txCtx, `UPDATE servicing.authorities SET reserved_debit_cents=reserved_debit_cents+$4
		WHERE mode=$1 AND id=$2 AND version=$3 AND status='ACTIVE'
		AND consumed_debit_cents+reserved_debit_cents+$4<=maximum_debit_cents`,
		string(candidate.Mode), terms.CollectionAuthorityID, terms.CollectionAuthorityVersion, reservedDebit)
	if err != nil || reserveTag.RowsAffected() != 1 {
		return CollectionPlanResult{}, errors.New("collection_authority_reserve_failed")
	}
	var newBridgeVersion int64
	err = tx.QueryRow(txCtx, `UPDATE servicing.bridges SET lifecycle=CASE WHEN lifecycle='ACTIVE'
		THEN 'COLLECTING' ELSE lifecycle END,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND version=$3 RETURNING version`, string(candidate.Mode),
		candidate.BridgeID, bridgeVersion).Scan(&newBridgeVersion)
	if err != nil {
		return CollectionPlanResult{}, errors.New("collection_bridge_plan_update_failed")
	}
	intentEvidence := map[string]any{"kind": kind, "request_id": requestID,
		"payload_hash": operation.PayloadHash, "deposit_id": candidate.DepositID,
		"deposit_observation_id": depositObservationID, "balance_observation_id": balanceObservationID,
		"evidence_key": key, "authority_id": terms.CollectionAuthorityID,
		"authority_version": terms.CollectionAuthorityVersion, "reserved_debit_cents": reservedDebit,
		"provider_fee_cents": evidence.Fee.Cents, "fee_contract_id": evidence.Fee.ContractID}
	if err := AppendOperationJournal(txCtx, tx, candidate.Mode, operationID, "INTENT_CREATED",
		"", "QUEUED", "", nil, nil, nil, intentEvidence); err != nil {
		return CollectionPlanResult{}, err
	}
	var caseID string
	if err := tx.QueryRow(txCtx, `SELECT COALESCE(originating_case_id,'') FROM servicing.bridges
		WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.BridgeID).Scan(&caseID); err != nil {
		return CollectionPlanResult{}, errors.New("collection_bridge_case_read_failed")
	}
	bridgeSummary, _ := json.Marshal(map[string]any{"customer_id": candidate.CustomerID,
		"account_ref": "customer:" + candidate.CustomerID, "originating_case_id": nullableString(caseID),
		"bridge_id": candidate.BridgeID, "bridge_version": newBridgeVersion,
		"operation_id": operationID, "operation_kind": kind, "deposit_id": candidate.DepositID,
		"amount_cents": amount, "provider_fee_cents": evidence.Fee.Cents, "state": "QUEUED"})
	bridgeParent, bridgeParentVersion := candidate.BridgeID, newBridgeVersion
	if _, err := AppendChange(txCtx, tx, domain.Change{Mode: candidate.Mode, ResourceType: "BRIDGE",
		ResourceID: candidate.BridgeID, ResourceVersion: newBridgeVersion,
		ChangeType: "COLLECTION_PLANNED", Summary: bridgeSummary}, &bridgeParent, &bridgeParentVersion); err != nil {
		return CollectionPlanResult{}, err
	}
	operationSummary, _ := json.Marshal(map[string]any{"bridge_id": candidate.BridgeID,
		"bridge_version": newBridgeVersion, "operation_id": operationID, "state": "QUEUED",
		"kind": kind, "amount_cents": amount, "deposit_id": candidate.DepositID})
	if _, err := AppendChange(txCtx, tx, domain.Change{Mode: candidate.Mode, ResourceType: "OPERATION",
		ResourceID: operationID, ResourceVersion: 1, ChangeType: "COLLECTION_INTENT_CREATED",
		Summary: operationSummary}, &bridgeParent, &bridgeParentVersion); err != nil {
		return CollectionPlanResult{}, err
	}
	if _, err := tx.Exec(txCtx, `INSERT INTO servicing.collection_plan_checks(mode,deposit_id,
		bridge_id,planned_on,evidence_key,deposit_observation_id,balance_observation_id,
		authority_id,authority_version,bridge_version,bridge_debt_version,result_code,operation_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'OPERATION_QUEUED',$12)`,
		string(candidate.Mode), candidate.DepositID, candidate.BridgeID, plannedOn, key,
		depositObservationID, balanceObservationID, terms.CollectionAuthorityID,
		terms.CollectionAuthorityVersion, newBridgeVersion, bridgeDebtVersion, operationID); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_check_insert_failed")
	}
	if err := tx.Commit(txCtx); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
	}
	return CollectionPlanResult{Created: true, OperationID: operationID}, nil
}

// PrepareCollectionDispatch rechecks the exact immutable collection amount and
// split under the same lease fence before committing DISPATCHING.
func (r *Repository) PrepareCollectionDispatch(ctx context.Context, claim OperationClaim,
	bindings CollectionDispatchBindings, evidence CollectionReadEvidence,
	pricing domain.Pricing, policyHash string, clock func() time.Time) (DispatchPreparation, error) {
	if claim.State != domain.OperationQueued || claim.LeaseToken == "" ||
		bindings.Operation.ID != claim.OperationID || bindings.Candidate.BridgeID != claim.BridgeID ||
		bindings.Candidate.Mode != claim.Mode || clock == nil || pricing.Validate() != nil ||
		!validHash(policyHash) {
		return DispatchPreparation{}, ErrOperationClaimStale
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return DispatchPreparation{}, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	scope, err := lockOperationScope(txCtx, tx, claim.OperationCandidate)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if scope.leaseToken == nil || scope.leaseExpiresAt == nil || *scope.leaseToken != claim.LeaseToken ||
		!scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt) || scope.state != "QUEUED" {
		return DispatchPreparation{}, ErrOperationClaimStale
	}
	now := clock().UTC()
	if now.IsZero() {
		return DispatchPreparation{}, errors.New("collection_dispatch_clock_invalid")
	}
	lockedOperation := collectionOperationFromScope(scope, claim.Mode)
	if !matchesLockedCollectionOperation(bindings, lockedOperation, scope, claim) {
		if validCollectionObservation(evidence, CollectionPlanFacts{Candidate: bindings.Candidate,
			FundingAccount: scope.facilityAccount}, now) {
			if _, err := insertCollectionDepositObservation(txCtx, tx, claim.Mode, evidence.Deposit); err != nil {
				return DispatchPreparation{}, err
			}
			if _, err := insertCollectionBalanceObservation(txCtx, tx, claim.Mode, evidence.Balance); err != nil {
				return DispatchPreparation{}, err
			}
		}
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"COLLECTION_LOCKED_BINDING_MISMATCH", "COLLECTION_REVIEW_REQUIRED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_binding_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_LOCKED_BINDING_MISMATCH"}, nil
	}
	facts := CollectionPlanFacts{Candidate: bindings.Candidate, FundingAccount: scope.facilityAccount}
	if !validCollectionObservation(evidence, facts, now) {
		return DispatchPreparation{}, errors.New("collection_dispatch_evidence_invalid")
	}
	depositObservationID, err := insertCollectionDepositObservation(txCtx, tx, claim.Mode, evidence.Deposit)
	if err != nil {
		return DispatchPreparation{}, err
	}
	balanceObservationID, err := insertCollectionBalanceObservation(txCtx, tx, claim.Mode, evidence.Balance)
	if err != nil {
		return DispatchPreparation{}, err
	}
	var depositStatus, depositKind, depositAccount, providerDepositID, depositCustomer string
	var depositNet int64
	var depositSettledAt, fundedAt time.Time
	if err := tx.QueryRow(txCtx, `SELECT d.status,d.kind,d.account_ref,d.provider_deposit_id,
		d.customer_id,d.net_cents,d.settled_at,b.funded_at FROM servicing.deposits d
		JOIN servicing.bridges b ON b.mode=d.mode AND b.customer_id=d.customer_id
		WHERE d.mode=$1 AND d.id=$2 AND b.id=$3 FOR UPDATE OF d`, string(claim.Mode),
		bindings.Candidate.DepositID, claim.BridgeID).Scan(&depositStatus, &depositKind,
		&depositAccount, &providerDepositID, &depositCustomer, &depositNet, &depositSettledAt, &fundedAt); err != nil {
		return DispatchPreparation{}, errors.New("collection_dispatch_deposit_read_failed")
	}
	depositMatches := depositStatus == "SETTLED" && depositKind == "EXTERNAL_SETTLED" &&
		depositAccount == bindings.Candidate.AccountRef && depositCustomer == claim.CustomerID &&
		providerDepositID == bindings.Candidate.ProviderDepositID && evidence.Deposit.AccountRef == depositAccount &&
		evidence.Deposit.DepositID == providerDepositID && evidence.Deposit.CustomerID == depositCustomer &&
		evidence.Deposit.NetCents == depositNet && evidence.Deposit.SettledAt != nil &&
		evidence.Deposit.SettledAt.Equal(depositSettledAt) && depositSettledAt.After(fundedAt)
	if !depositMatches {
		if depositStatus == "SETTLED" {
			if _, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='HELD'
				WHERE mode=$1 AND id=$2 AND status='SETTLED'`, string(claim.Mode), bindings.Candidate.DepositID); err != nil {
				return DispatchPreparation{}, errors.New("collection_dispatch_deposit_hold_failed")
			}
		}
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"DEPOSIT_SOURCE_CONFLICT", "DEPOSIT_SOURCE_CONFLICT", map[string]any{
				"deposit_id":              bindings.Candidate.DepositID,
				"provider_observation_id": depositObservationID,
				"movement_certainty":      "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "DEPOSIT_SOURCE_CONFLICT"}, nil
	}
	var customerHeld bool
	if err := tx.QueryRow(txCtx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.CustomerID).Scan(&customerHeld); err != nil {
		return DispatchPreparation{}, errors.New("collection_dispatch_customer_read_failed")
	}
	if customerHeld {
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"CUSTOMER_HELD", "", map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "CUSTOMER_HELD"}, nil
	}
	unresolved, err := hasUnresolvedCollectionDebit(txCtx, tx, claim.Mode,
		claim.CustomerID, scope.sourceAccount, claim.OperationID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if unresolved {
		return r.cancelCollectionForChangedEvidence(txCtx, tx, claim, scope,
			"COLLECTION_DEBIT_UNRESOLVED", evidence, depositObservationID, balanceObservationID, now)
	}
	if scope.guardCode != "" {
		holdReason := "COLLECTION_REVIEW_REQUIRED"
		if scope.guardCode == "MAPPING_CONFLICT" || scope.guardCode == "COLLECTION_AUTHORITY_BLOCKED" {
			holdReason = "COLLECTION_AUTHORITY_BLOCKED"
		}
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope, scope.guardCode,
			holdReason, map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: scope.guardCode}, nil
	}
	if evidence.Fee.ContractID != scope.collectionFeeContract || evidence.Fee.Cents != scope.collectionQuotedFee ||
		!provider.ValidCollectionFeeQuote(string(claim.Mode), evidence.Fee) {
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"COLLECTION_FEE_CONTRACT_CHANGED", "COLLECTION_REVIEW_REQUIRED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_FEE_CONTRACT_CHANGED"}, nil
	}
	var authoritySource, authorityDestination, product, mandate, authorityStatus string
	var kinds, purposes []string
	var maximum, consumed, reserved int64
	var validFrom, expiresAt time.Time
	err = tx.QueryRow(txCtx, `SELECT source_account_ref,destination_account_ref,product_code,
		mandate_reference,permitted_kinds,permitted_purposes,maximum_debit_cents,
		consumed_debit_cents,reserved_debit_cents,status,valid_from,expires_at
		FROM servicing.authorities WHERE mode=$1 AND id=$2 AND version=$3
		AND NOT EXISTS(SELECT 1 FROM servicing.authorities newer WHERE newer.mode=$1
			AND newer.id=$2 AND newer.version>$3)`, string(claim.Mode),
		scope.terms.CollectionAuthorityID, scope.terms.CollectionAuthorityVersion).Scan(
		&authoritySource, &authorityDestination, &product, &mandate, &kinds, &purposes,
		&maximum, &consumed, &reserved, &authorityStatus, &validFrom, &expiresAt)
	if err != nil || authorityStatus != "ACTIVE" || authoritySource != scope.sourceAccount ||
		authorityDestination != scope.destinationAccount || product != "CONNECTED_ACCOUNT_TRANSFERS" ||
		mandate == "" || !now.Before(expiresAt) || now.Before(validFrom) || maximum < consumed ||
		!collectionHasKind(kinds, scope.kind) || !collectionPurposeAllowed(purposes, scope.kind) {
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"COLLECTION_AUTHORITY_BLOCKED", "COLLECTION_AUTHORITY_BLOCKED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
	}
	if reserved < scope.collectionReservedDebit || scope.collectionReservedDebit != scope.amount+scope.collectionQuotedFee {
		return DispatchPreparation{}, errors.New("collection_authority_reservation_invalid")
	}
	aggregateConsumed, aggregateReserved, err := authorityUsage(txCtx, tx, claim.Mode,
		*scope.authorityID)
	if err != nil {
		if err.Error() == "authority_usage_overflow" {
			if cancelErr := cancelCollectionIntentTx(txCtx, tx, claim, scope,
				"COLLECTION_AUTHORITY_USAGE_INVALID", "COLLECTION_AUTHORITY_BLOCKED",
				map[string]any{"movement_certainty": "PROVEN_UNSENT"}); cancelErr != nil {
				return DispatchPreparation{}, cancelErr
			}
			if commitErr := tx.Commit(txCtx); commitErr != nil {
				return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
			}
			return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
		}
		return DispatchPreparation{}, err
	}
	if aggregateReserved < scope.collectionReservedDebit {
		return DispatchPreparation{}, errors.New("collection_authority_aggregate_reservation_invalid")
	}
	accrual, err := AccrueThrough(txCtx, tx, AccrualOptions{Mode: claim.Mode,
		Pricing: pricing, ExpectedPolicyHash: policyHash}, claim.BridgeID, now)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if !accrual.Current || accrual.Pending || accrual.CorrectionRequired {
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"ACCRUAL_CATCHUP_PENDING", "COLLECTION_REVIEW_REQUIRED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "ACCRUAL_CATCHUP_PENDING"}, nil
	}
	var principalOwed, contractOwed, lateOwed int64
	var bridgeVersion, bridgeDebtVersion int64
	var holds []string
	if err := tx.QueryRow(txCtx, `SELECT version,collection_debt_version,principal_owed_cents,
		contract_fee_owed_cents,late_fee_owed_cents,hold_reasons FROM servicing.bridges
		WHERE mode=$1 AND id=$2`, string(claim.Mode), claim.BridgeID).Scan(&bridgeVersion,
		&bridgeDebtVersion, &principalOwed, &contractOwed, &lateOwed, &holds); err != nil {
		return DispatchPreparation{}, errors.New("collection_dispatch_debt_read_failed")
	}
	scope.bridgeVersion, scope.collectionDebtVersion = bridgeVersion, bridgeDebtVersion
	for _, blocker := range []string{"DEPOSIT_SOURCE_CONFLICT", "COLLECTION_AUTHORITY_BLOCKED",
		"COLLECTION_REVIEW_REQUIRED", "ACCRUAL_CORRECTION_REQUIRED", "ACCRUAL_CATCHUP_PENDING",
		"RECEIVABLE_BACKING_CONFLICT", "REFUND_PAYABLE", "REVERSAL_REVIEW"} {
		if containsReason(holds, blocker) {
			if err := cancelCollectionIntentTx(txCtx, tx, claim, scope, blocker, blocker,
				map[string]any{"movement_certainty": "PROVEN_UNSENT"}); err != nil {
				return DispatchPreparation{}, err
			}
			if err := tx.Commit(txCtx); err != nil {
				return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
			}
			return DispatchPreparation{Cancelled: true, BlockedCode: blocker}, nil
		}
	}
	otherLate, otherContract, otherPrincipal, feeOperationPending, err := collectionOtherReservations(
		txCtx, tx, claim.Mode, claim.BridgeID, claim.OperationID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if lateOwed < otherLate || contractOwed < otherContract || principalOwed < otherPrincipal {
		return DispatchPreparation{}, errors.New("collection_local_reservation_overflow")
	}
	if scope.kind == "COLLECT_FEE" {
		availableLate := lateOwed - otherLate
		availableContract := contractOwed - otherContract
		expectedLate := minInt64(scope.amount, availableLate)
		expectedContract := scope.amount - expectedLate
		if availableContract < expectedContract || expectedLate != scope.lateFeeCents ||
			expectedContract != scope.contractFeeCents {
			return r.cancelCollectionForChangedEvidence(txCtx, tx, claim, scope,
				"COLLECTION_SPLIT_CHANGED", evidence, depositObservationID, balanceObservationID, now)
		}
	} else if lateOwed != 0 || contractOwed != 0 || feeOperationPending ||
		principalOwed-otherPrincipal < scope.amount {
		return r.cancelCollectionForChangedEvidence(txCtx, tx, claim, scope,
			"COLLECTION_WATERFALL_CHANGED", evidence, depositObservationID, balanceObservationID, now)
	}
	depositUsed, err := collectionDepositUsage(txCtx, tx, claim.Mode,
		scope.collectionDepositID, scope.collectionAttemptID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	var currentDepositNet int64
	if err := tx.QueryRow(txCtx, `SELECT net_cents FROM servicing.deposits WHERE mode=$1 AND id=$2`,
		string(claim.Mode), scope.collectionDepositID).Scan(&currentDepositNet); err != nil {
		return DispatchPreparation{}, errors.New("collection_dispatch_budget_read_failed")
	}
	projectedReserved := aggregateReserved - scope.collectionReservedDebit
	var authorityProjected int64
	if aggregateConsumed <= maximum && projectedReserved <= maximum-aggregateConsumed {
		authorityProjected = maximum - aggregateConsumed - projectedReserved
	}
	if authorityProjected < scope.collectionReservedDebit {
		if err := cancelCollectionIntentTx(txCtx, tx, claim, scope,
			"COLLECTION_AUTHORITY_BLOCKED", "COLLECTION_AUTHORITY_BLOCKED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT",
				"authority_id":      *scope.authorityID,
				"authority_version": *scope.authorityVersion}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return DispatchPreparation{}, errors.New("collection_dispatch_cancel_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_AUTHORITY_BLOCKED"}, nil
	}
	if depositUsed < 0 || currentDepositNet < depositUsed ||
		currentDepositNet-depositUsed < scope.collectionReservedDebit ||
		evidence.Balance.AvailableCents < scope.collectionReservedDebit {
		return r.cancelCollectionForChangedEvidence(txCtx, tx, claim, scope,
			"COLLECTION_LIQUIDITY_CHANGED", evidence, depositObservationID, balanceObservationID, now)
	}
	key := collectionEvidenceKey(evidence.Deposit, evidence.Balance, claim.BridgeID, bridgeDebtVersion,
		scope.terms.CollectionAuthorityID, scope.terms.CollectionAuthorityVersion)
	operationVersion, err := beginCollectionProviderDispatch(txCtx, tx, claim, scope,
		bridgeVersion, key, depositObservationID, balanceObservationID, now)
	if err != nil {
		return DispatchPreparation{}, err
	}
	preparedClaim := claim
	preparedClaim.State, preparedClaim.Version = domain.OperationDispatching, operationVersion
	preparedClaim.BridgeVersion = bridgeVersion + 1
	if err := tx.Commit(txCtx); err != nil {
		return DispatchPreparation{}, errors.New("collection_dispatch_commit_failed")
	}
	return DispatchPreparation{Intent: &DispatchIntent{Operation: lockedOperation,
		Claim: preparedClaim, BridgeVersion: preparedClaim.BridgeVersion,
		SourceFeeContract: scope.collectionFeeContract, SourceFeeCents: scope.collectionQuotedFee}}, nil
}

func collectionOtherReservations(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID, excludeOperationID string) (int64, int64, int64, bool, error) {
	var late, contract, principal int64
	var feePending bool
	err := tx.QueryRow(ctx, `SELECT
		COALESCE(sum(a.amount_cents) FILTER (WHERE a.bucket='LATE_FEE'),0),
		COALESCE(sum(a.amount_cents) FILTER (WHERE a.bucket='CONTRACT_FEE'),0),
		COALESCE(sum(a.amount_cents) FILTER (WHERE a.bucket='PRINCIPAL'),0),
		COALESCE(bool_or(o.kind='COLLECT_FEE'),false)
		FROM servicing.collection_allocations a
		JOIN servicing.operations o ON o.mode=a.mode AND o.id=a.operation_id
		JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
		WHERE a.mode=$1 AND o.bridge_id=$2 AND o.id::text<>$3
		AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD')
		AND ca.state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')`,
		string(mode), bridgeID, excludeOperationID).Scan(&late, &contract, &principal, &feePending)
	if err != nil {
		return 0, 0, 0, false, errors.New("collection_other_reservation_read_failed")
	}
	return late, contract, principal, feePending, nil
}

func collectionOperationFromScope(scope lockedOperationScope, mode domain.Mode) provider.Operation {
	return provider.Operation{ID: scope.operationID, Kind: provider.OperationKind(scope.kind),
		Mode: string(mode), SourceAccountRef: scope.sourceAccount,
		DestinationAccountRef: scope.destinationAccount, AmountCents: scope.amount,
		Currency: scope.currency, RequestID: scope.requestID, PayloadHash: scope.payloadHash,
		BridgeID: scope.bridgeID, TriggerID: scope.providerDepositID,
		LateFeeCents: scope.lateFeeCents, ContractFeeCents: scope.contractFeeCents,
		PrincipalCents: scope.principalCents}
}

func matchesLockedCollectionOperation(bindings CollectionDispatchBindings,
	locked provider.Operation, scope lockedOperationScope, claim OperationClaim) bool {
	provided := bindings.Operation
	providedHash, providedErr := provider.PayloadHash(provided)
	lockedHash, lockedErr := provider.PayloadHash(locked)
	providedTermsHash, providedTermsErr := bindings.Terms.Hash()
	lockedTermsHash, lockedTermsErr := scope.terms.Hash()
	candidate := bindings.Candidate
	return providedErr == nil && lockedErr == nil && providedTermsErr == nil && lockedTermsErr == nil &&
		providedTermsHash == lockedTermsHash && providedTermsHash == scope.termsHash &&
		providedHash == provided.PayloadHash &&
		lockedHash == scope.payloadHash && providedHash == lockedHash &&
		provided.ID == locked.ID && provided.Kind == locked.Kind && provided.Mode == locked.Mode &&
		provided.SourceAccountRef == locked.SourceAccountRef &&
		provided.DestinationAccountRef == locked.DestinationAccountRef &&
		provided.RequestID == locked.RequestID && provided.AmountCents == locked.AmountCents &&
		provided.Currency == locked.Currency && provided.BridgeID == locked.BridgeID &&
		provided.TriggerID == locked.TriggerID && provided.LateFeeCents == locked.LateFeeCents &&
		provided.ContractFeeCents == locked.ContractFeeCents &&
		provided.PrincipalCents == locked.PrincipalCents &&
		candidate.Mode == claim.Mode && candidate.FacilityID == scope.facilityID &&
		candidate.CustomerID == scope.customerID && candidate.BridgeID == scope.bridgeID &&
		candidate.DepositID == scope.collectionDepositID &&
		candidate.AccountRef == scope.sourceAccount &&
		candidate.ProviderDepositID == scope.providerDepositID &&
		candidate.CardID == scope.terms.CardRef && candidate.AuthorityID == scope.terms.CollectionAuthorityID &&
		candidate.AuthorityVersion == scope.terms.CollectionAuthorityVersion &&
		candidate.TermsHash == scope.termsHash && bindings.AttemptID == scope.collectionAttemptID &&
		bindings.FeeContractID == scope.collectionFeeContract &&
		bindings.QuotedProviderFee == scope.collectionQuotedFee &&
		bindings.ReservedDebitCents == scope.collectionReservedDebit &&
		bindings.LeaseToken == claim.LeaseToken && bindings.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) &&
		claim.BridgeVersion == scope.bridgeVersion && candidate.BridgeVersion == scope.bridgeVersion
}

func beginCollectionProviderDispatch(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	scope lockedOperationScope, bridgeVersion int64, evidenceKey, depositObservationID,
	balanceObservationID string, now time.Time) (int64, error) {
	var operationVersion int64
	err := tx.QueryRow(ctx, `UPDATE servicing.operations SET state='DISPATCHING',version=version+1,
		next_check_at=NULL,sanitized_error_code=NULL,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND kind IN ('COLLECT_FEE','COLLECT_PRINCIPAL')
		AND state='QUEUED' AND version=$3 AND lease_token=$4::uuid AND lease_expires_at=$5
		AND lease_expires_at>clock_timestamp()+interval '10 seconds'
		RETURNING version`, string(claim.Mode), claim.OperationID, claim.Version,
		claim.LeaseToken, claim.LeaseExpiresAt).Scan(&operationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrOperationClaimStale
	}
	if err != nil {
		return 0, errors.New("collection_dispatch_operation_cas_failed")
	}
	var newBridgeVersion int64
	err = tx.QueryRow(ctx, `UPDATE servicing.bridges SET lifecycle=CASE WHEN lifecycle='ACTIVE'
		THEN 'COLLECTING' ELSE lifecycle END,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND version=$3 RETURNING version`, string(claim.Mode),
		claim.BridgeID, bridgeVersion).Scan(&newBridgeVersion)
	if err != nil {
		return 0, errors.New("collection_dispatch_bridge_update_failed")
	}
	leaseToken := claim.LeaseToken
	if err := AppendOperationJournal(ctx, tx, claim.Mode, claim.OperationID, "ATTEMPT_STARTED",
		"QUEUED", "DISPATCHING", "", &leaseToken, &claim.LeaseExpiresAt, nil,
		map[string]any{"movement_certainty": "POSSIBLE_SEND", "evidence_key": evidenceKey,
			"deposit_observation_id": depositObservationID,
			"balance_observation_id": balanceObservationID,
			"deposit_id":             scope.collectionDepositID,
			"reserved_debit_cents":   scope.collectionReservedDebit,
			"provider_fee_cents":     scope.collectionQuotedFee}); err != nil {
		return 0, err
	}
	details := map[string]any{"state": "DISPATCHING", "movement_certainty": "POSSIBLE_SEND",
		"evidence_key": evidenceKey, "deposit_id": scope.collectionDepositID}
	if err := appendBridgeOperationChange(ctx, tx, scope, newBridgeVersion,
		"COLLECTION_DISPATCHING", details); err != nil {
		return 0, err
	}
	scope.state, scope.version, scope.bridgeVersion = "DISPATCHING", operationVersion, newBridgeVersion
	if err := appendOperationChange(ctx, tx, scope, operationVersion, newBridgeVersion,
		"COLLECTION_DISPATCHING", details); err != nil {
		return 0, err
	}
	_ = now
	return operationVersion, nil
}

func cancelCollectionIntentTx(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	scope lockedOperationScope, reason, holdReason string, evidence map[string]any) error {
	if claim.State != domain.OperationQueued || !validJournalCode(reason) ||
		(scope.leaseToken == nil || scope.leaseExpiresAt == nil ||
			*scope.leaseToken != claim.LeaseToken || !scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt)) {
		return ErrOperationClaimStale
	}
	var operationVersion int64
	err := tx.QueryRow(ctx, `UPDATE servicing.operations SET state='CANCELLED_UNSENT',
		lease_token=NULL,lease_expires_at=NULL,next_check_at=NULL,sanitized_error_code=$5,
		version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='QUEUED' AND version=$3
		AND lease_token=$4::uuid AND lease_expires_at=$6 AND lease_expires_at>clock_timestamp()
		RETURNING version`, string(claim.Mode), claim.OperationID, claim.Version,
		claim.LeaseToken, reason, claim.LeaseExpiresAt).Scan(&operationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("collection_cancel_operation_cas_failed")
	}
	if scope.collectionReservedDebit < 0 || scope.collectionAttemptID == "" ||
		scope.authorityID == nil || scope.authorityVersion == nil {
		return errors.New("collection_cancel_reservation_invalid")
	}
	tag, err := tx.Exec(ctx, `UPDATE servicing.collection_attempts SET state='FAILED_CONFIRMED',
		reserved_debit_cents=0 WHERE mode=$1 AND id=$2 AND state='QUEUED'
		AND reserved_debit_cents=$3`, string(claim.Mode), scope.collectionAttemptID,
		scope.collectionReservedDebit)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("collection_cancel_attempt_release_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.authorities SET reserved_debit_cents=reserved_debit_cents-$4
		WHERE mode=$1 AND id=$2 AND version=$3 AND reserved_debit_cents>=$4`,
		string(claim.Mode), *scope.authorityID, *scope.authorityVersion, scope.collectionReservedDebit)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("collection_cancel_authority_release_failed")
	}
	leaseToken := claim.LeaseToken
	journalEvidence := map[string]any{"movement_certainty": "PROVEN_UNSENT", "reason": reason,
		"attempt_id":           scope.collectionAttemptID,
		"released_debit_cents": scope.collectionReservedDebit}
	for key, value := range evidence {
		journalEvidence[key] = value
	}
	if err := AppendOperationJournal(ctx, tx, claim.Mode, claim.OperationID, "STATE_TRANSITION",
		"QUEUED", "CANCELLED_UNSENT", reason, &leaseToken, &claim.LeaseExpiresAt, nil,
		journalEvidence); err != nil {
		return err
	}
	var bridgeVersion int64
	if err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET lifecycle=CASE
		WHEN lifecycle='COLLECTING' AND (principal_owed_cents+contract_fee_owed_cents+late_fee_owed_cents)>0
		AND NOT EXISTS(SELECT 1 FROM servicing.collection_attempts ca WHERE ca.mode=bridges.mode
			AND ca.bridge_id=bridges.id AND ca.state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD'))
		THEN 'ACTIVE' ELSE lifecycle END,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 RETURNING version`, string(claim.Mode), claim.BridgeID).Scan(&bridgeVersion); err != nil {
		return errors.New("collection_cancel_bridge_update_failed")
	}
	if holdReason != "" {
		var activeHold bool
		if err := tx.QueryRow(ctx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
			string(claim.Mode), claim.CustomerID).Scan(&activeHold); err != nil {
			return errors.New("collection_cancel_customer_read_failed")
		}
		if !activeHold {
			if _, err := tx.Exec(ctx, `UPDATE servicing.customers SET active_hold=true,
				version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND active_hold=false`,
				string(claim.Mode), claim.CustomerID); err != nil {
				return errors.New("collection_cancel_customer_hold_failed")
			}
		}
		if evidence == nil {
			evidence = map[string]any{}
		}
		evidence["operation_id"], evidence["movement_certainty"] = claim.OperationID, "PROVEN_UNSENT"
		if err := appendCollectionHold(ctx, tx, claim.Mode, claim.BridgeID, holdReason, evidence); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
			string(claim.Mode), claim.BridgeID).Scan(&bridgeVersion); err != nil {
			return errors.New("collection_cancel_bridge_version_read_failed")
		}
	}
	details := map[string]any{"state": "CANCELLED_UNSENT", "reason": reason,
		"movement_certainty": "PROVEN_UNSENT", "attempt_id": scope.collectionAttemptID}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion,
		"COLLECTION_INTENT_CANCELLED", details); err != nil {
		return err
	}
	scope.state, scope.version, scope.bridgeVersion = "CANCELLED_UNSENT", operationVersion, bridgeVersion
	return appendOperationChange(ctx, tx, scope, operationVersion, bridgeVersion,
		"COLLECTION_INTENT_CANCELLED", details)
}

func (r *Repository) cancelCollectionForChangedEvidence(ctx context.Context, tx pgx.Tx,
	claim OperationClaim, scope lockedOperationScope, reason string, evidence CollectionReadEvidence,
	depositObservationID, balanceObservationID string, now time.Time) (DispatchPreparation, error) {
	plannedOn := utcDate(now)
	var dailyChecks int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM servicing.collection_plan_checks
		WHERE mode=$1 AND deposit_id=$2 AND planned_on=$3`, string(claim.Mode),
		scope.collectionDepositID, plannedOn).Scan(&dailyChecks); err != nil {
		return DispatchPreparation{}, errors.New("collection_plan_limit_read_failed")
	}
	if dailyChecks >= 3 {
		if err := cancelCollectionIntentTx(ctx, tx, claim, scope,
			"COLLECTION_REPLAN_LIMIT_REACHED", "COLLECTION_REVIEW_REQUIRED",
			map[string]any{"movement_certainty": "PROVEN_UNSENT", "changed_evidence_reason": reason}); err != nil {
			return DispatchPreparation{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DispatchPreparation{}, errors.New("collection_replan_limit_commit_failed")
		}
		return DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_REPLAN_LIMIT_REACHED"}, nil
	}
	key := collectionEvidenceKey(evidence.Deposit, evidence.Balance, claim.BridgeID,
		scope.collectionDebtVersion, scope.terms.CollectionAuthorityID,
		scope.terms.CollectionAuthorityVersion)
	if err := cancelCollectionIntentTx(ctx, tx, claim, scope, reason, "", map[string]any{
		"changed_evidence_key": key, "deposit_observation_id": depositObservationID,
		"balance_observation_id": balanceObservationID,
		"bridge_debt_version":    scope.collectionDebtVersion}); err != nil {
		return DispatchPreparation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DispatchPreparation{}, errors.New("collection_changed_evidence_commit_failed")
	}
	return DispatchPreparation{Cancelled: true, BlockedCode: reason}, nil
}

// CancelCollectionUnsent retires a queued intent only while its exact lease is
// live, releasing its original debit and authority holds without resizing it.
func (r *Repository) CancelCollectionUnsent(ctx context.Context, claim OperationClaim,
	reason, holdReason, providerCode string, raw []byte) error {
	if claim.State != domain.OperationQueued || !validJournalCode(reason) ||
		(holdReason != "" && !validJournalCode(holdReason)) ||
		(providerCode != "" && !validJournalCode(providerCode)) || len(raw) > maxProtectedOperationBodyBytes {
		return ErrOperationClaimStale
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	scope, err := lockOperationScope(txCtx, tx, claim.OperationCandidate)
	if err != nil {
		return err
	}
	if scope.leaseToken == nil || scope.leaseExpiresAt == nil ||
		*scope.leaseToken != claim.LeaseToken || !scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt) {
		return ErrOperationClaimStale
	}
	if providerCode != "" {
		kind, source, objectID, status := "BALANCE", "PROVIDER_BALANCE_GET", "", "READ_HELD"
		if holdReason == "DEPOSIT_SOURCE_CONFLICT" || strings.HasPrefix(providerCode, "PROVIDER_DEPOSIT_") {
			kind, source, objectID, status = "DEPOSIT", "PROVIDER_DEPOSIT_GET",
				scope.providerDepositID, "INELIGIBLE_OR_UNAVAILABLE"
		}
		rawAvailable := len(raw) > 0
		hashInput := raw
		if len(hashInput) == 0 {
			hashInput = []byte(providerCode)
		}
		normalizedValue := map[string]any{"source": source, "code": providerCode,
			"operation_id": claim.OperationID, "deposit_id": scope.collectionDepositID,
			"raw_evidence_available": rawAvailable}
		if rawAvailable {
			normalizedValue["raw_evidence_encoding"] = "base64"
			normalizedValue["raw_evidence_base64"] = base64.StdEncoding.EncodeToString(raw)
		}
		normalized, _ := json.Marshal(normalizedValue)
		digest := sha256.Sum256(hashInput)
		if _, err := tx.Exec(txCtx, `INSERT INTO servicing.provider_observations(mode,
			observation_kind,provider_object_id,source_account_ref,status,observed_at,
			evidence_hash,normalized_evidence) VALUES($1,$2,NULLIF($3,''),$4,$5,
			clock_timestamp(),$6,$7::jsonb)`, string(claim.Mode), kind, objectID,
			scope.sourceAccount, status, hex.EncodeToString(digest[:]), string(normalized)); err != nil {
			return errors.New("collection_preflight_evidence_insert_failed")
		}
		if kind == "DEPOSIT" {
			if _, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='HELD'
				WHERE mode=$1 AND id=$2 AND status='SETTLED'`, string(claim.Mode),
				scope.collectionDepositID); err != nil {
				return errors.New("collection_preflight_deposit_hold_failed")
			}
		}
	}
	if err := cancelCollectionIntentTx(txCtx, tx, claim, scope, reason, holdReason,
		map[string]any{"provider_error_code": providerCode, "movement_certainty": "PROVEN_UNSENT"}); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("collection_unsent_cancel_commit_failed")
	}
	return nil
}

// RecordCollectionPreflightFailure keeps transient GET failures retryable while
// the original queued amount, split and reservations remain unchanged.
func (r *Repository) RecordCollectionPreflightFailure(ctx context.Context,
	claim OperationClaim, outcome OperationReadOutcome) error {
	if outcome.Source != "GUARD" || outcome.ProviderError == nil || claim.State != domain.OperationQueued {
		return errors.New("collection_preflight_failure_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := FinishOperationTx(txCtx, tx, claim, outcome); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRow(txCtx, `SELECT state FROM servicing.operations WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.OperationID).Scan(&state); err != nil {
		return errors.New("collection_preflight_state_read_failed")
	}
	attemptState := "QUEUED"
	if state == "HELD" {
		attemptState = "HELD"
	}
	if _, err := tx.Exec(txCtx, `UPDATE servicing.collection_attempts ca SET state=$3
		WHERE ca.mode=$1 AND ca.id=(SELECT a.attempt_id FROM servicing.collection_allocations a
			WHERE a.mode=$1 AND a.operation_id=$2 LIMIT 1)
		AND ca.state IN ('QUEUED','HELD')`, string(claim.Mode), claim.OperationID, attemptState); err != nil {
		return errors.New("collection_preflight_attempt_update_failed")
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("collection_preflight_failure_commit_failed")
	}
	return nil
}

func validCollectionObservation(evidence CollectionReadEvidence, input CollectionPlanFacts,
	now time.Time) bool {
	deposit := evidence.Deposit
	if len(deposit.RawEvidence) == 0 || len(deposit.RawEvidence) > 4_000_000 ||
		deposit.AccountRef != input.Candidate.AccountRef || deposit.CustomerID != input.Candidate.CustomerID ||
		deposit.DepositID != input.Candidate.ProviderDepositID || deposit.Currency != "USD" ||
		deposit.Status != "SETTLED" || deposit.Kind != "EXTERNAL_SETTLED" || deposit.SettledAt == nil ||
		!fundingTimeUTC(*deposit.SettledAt) || !collectionFreshTime(deposit.ObservedAt, now) ||
		deposit.ProviderFeeCents == nil || *deposit.ProviderFeeCents < 0 || deposit.GrossCents <= 0 ||
		deposit.NetCents <= 0 || deposit.NetCents > math.MaxInt64-*deposit.ProviderFeeCents ||
		deposit.NetCents+*deposit.ProviderFeeCents != deposit.GrossCents ||
		deposit.SettledAt.After(deposit.ObservedAt.Add(5*time.Second)) {
		return false
	}
	balance := evidence.Balance
	return len(balance.RawEvidence) > 0 && len(balance.RawEvidence) <= 4_000_000 &&
		balance.AccountRef == input.Candidate.AccountRef && balance.Currency == "USD" &&
		balance.AvailableCents >= 0 && collectionFreshTime(balance.RequestedAt, now) &&
		collectionFreshTime(balance.ObservedAt, now) && !balance.RequestedAt.After(balance.ObservedAt.Add(5*time.Second)) &&
		fundingTimeUTC(balance.RequestedAt) && fundingTimeUTC(balance.ObservedAt) &&
		evidence.Fee.Cents >= 0 && evidence.Fee.ContractID != ""
}

func insertCollectionDepositObservation(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	deposit provider.DepositObservation) (string, error) {
	fee := int64(0)
	if deposit.ProviderFeeCents != nil {
		fee = *deposit.ProviderFeeCents
	}
	body, err := json.Marshal(map[string]any{"source": "PROVIDER_DEPOSIT_GET",
		"account_ref": deposit.AccountRef, "customer_id": deposit.CustomerID,
		"deposit_id": deposit.DepositID, "currency": deposit.Currency,
		"gross_cents": deposit.GrossCents, "net_cents": deposit.NetCents,
		"provider_fee_cents": fee, "status": deposit.Status, "kind": deposit.Kind,
		"settled_at": deposit.SettledAt, "observed_at": deposit.ObservedAt.UTC(),
		"original_response_body_encoding": "base64",
		"original_response_body_base64":   base64.StdEncoding.EncodeToString(deposit.RawEvidence)})
	if err != nil {
		return "", errors.New("collection_deposit_evidence_encode_failed")
	}
	hash := sha256.Sum256(deposit.RawEvidence)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.provider_observations(mode,observation_kind,
		provider_object_id,source_account_ref,status,amount_cents,currency,provider_fee_cents,
		effective_at,observed_at,evidence_hash,normalized_evidence)
		VALUES($1,'DEPOSIT',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb) RETURNING id::text`,
		string(mode), deposit.DepositID, deposit.AccountRef, deposit.Status, deposit.GrossCents,
		deposit.Currency, fee, deposit.SettledAt.UTC(), deposit.ObservedAt.UTC(),
		hex.EncodeToString(hash[:]), string(body)).Scan(&id)
	if err != nil {
		return "", errors.New("collection_deposit_observation_insert_failed")
	}
	return id, nil
}

func insertCollectionBalanceObservation(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	balance provider.BalanceObservation) (string, error) {
	body, err := json.Marshal(map[string]any{"source": "PROVIDER_BALANCE_GET",
		"account_ref": balance.AccountRef, "currency": balance.Currency,
		"available_cents": balance.AvailableCents, "requested_at": balance.RequestedAt.UTC(),
		"observed_at": balance.ObservedAt.UTC(), "original_response_body_encoding": "base64",
		"original_response_body_base64": base64.StdEncoding.EncodeToString(balance.RawEvidence)})
	if err != nil {
		return "", errors.New("collection_balance_evidence_encode_failed")
	}
	hash := sha256.Sum256(balance.RawEvidence)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.provider_observations(mode,observation_kind,
		source_account_ref,status,amount_cents,currency,effective_at,observed_at,evidence_hash,
		normalized_evidence) VALUES($1,'BALANCE',$2,'AVAILABLE',$3,$4,$5,$6,$7,$8::jsonb)
		RETURNING id::text`, string(mode), balance.AccountRef, balance.AvailableCents,
		balance.Currency, balance.ObservedAt.UTC(), balance.ObservedAt.UTC(),
		hex.EncodeToString(hash[:]), string(body)).Scan(&id)
	if err != nil {
		return "", errors.New("collection_balance_observation_insert_failed")
	}
	return id, nil
}

func collectionDepositUsage(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	depositID, excludeAttemptID string) (int64, error) {
	var textValue string
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(settled_debit_cents)+sum(
		CASE WHEN state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')
		THEN reserved_debit_cents ELSE 0 END),0)::text
		FROM servicing.collection_attempts WHERE mode=$1 AND deposit_id=$2
		AND ($3='' OR id::text<>$3)`, string(mode), depositID, excludeAttemptID).Scan(&textValue)
	if err != nil {
		return 0, errors.New("collection_budget_read_failed")
	}
	value, err := strconv.ParseInt(textValue, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("collection_budget_invalid")
	}
	return value, nil
}

func hasUnresolvedCollectionDebit(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	customerID, accountRef, excludeOperationID string) (bool, error) {
	var unresolved bool
	err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM servicing.collection_attempts ca
			WHERE ca.mode=$1 AND ca.customer_id=$2 AND ca.account_ref=$3
			AND ca.state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')
			AND ($4='' OR ca.id<>(SELECT a.attempt_id FROM servicing.collection_allocations a
				WHERE a.mode=$1 AND a.operation_id=$4::uuid LIMIT 1))) OR
		EXISTS(SELECT 1 FROM servicing.operations o JOIN servicing.bridges b
			ON b.mode=o.mode AND b.id=o.bridge_id
			WHERE o.mode=$1 AND b.customer_id=$2 AND o.source_account_ref=$3
			AND o.kind IN ('COLLECT_FEE','COLLECT_PRINCIPAL')
			AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD')
			AND ($4='' OR o.id::text<>$4))`, string(mode), customerID, accountRef,
		excludeOperationID).Scan(&unresolved)
	if err != nil {
		return false, errors.New("collection_customer_debit_read_failed")
	}
	return unresolved, nil
}

func holdCustomerCollections(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	customerID string, lockedBridgeIDs []string, reason string, evidence map[string]any) error {
	var activeHold bool
	if err := tx.QueryRow(ctx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
		string(mode), customerID).Scan(&activeHold); err != nil {
		return errors.New("collection_customer_hold_read_failed")
	}
	if !activeHold {
		tag, err := tx.Exec(ctx, `UPDATE servicing.customers SET active_hold=true,
			version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND active_hold=false`,
			string(mode), customerID)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("collection_customer_hold_update_failed")
		}
	}
	for _, bridgeID := range lockedBridgeIDs {
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`,
			string(mode), bridgeID).Scan(&lifecycle); err != nil {
			return errors.New("collection_hold_bridge_read_failed")
		}
		if lifecycle != "RESERVED" && lifecycle != "FUNDING_PENDING" && lifecycle != "ACTIVE" && lifecycle != "COLLECTING" && lifecycle != "REOPENED_HELD" {
			continue
		}
		clone := make(map[string]any, len(evidence))
		for key, value := range evidence {
			clone[key] = value
		}
		if err := appendCollectionHold(ctx, tx, mode, bridgeID, reason, clone); err != nil {
			return err
		}
	}
	return nil
}

func lockCollectionCustomerBridges(ctx context.Context, tx pgx.Tx, candidate OperationCandidate) ([]string, error) {
	if _, err := lockFundingFacility(ctx, tx, string(candidate.Mode), candidate.FacilityID); err != nil {
		return nil, err
	}
	if _, err := lockFundingCustomer(ctx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return nil, errors.New("collection_customer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(ctx, tx, string(candidate.Mode), candidate.CustomerID)
	if err != nil {
		return nil, err
	}
	return bridgeIDs, nil
}

// RecordCollectionHold makes a blocked collection reason visible on the
// customer and every existing active bridge without creating new bridges.
func (r *Repository) RecordCollectionHold(ctx context.Context, candidate CollectionCandidate,
	reason string, details map[string]any) error {
	if (candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox) ||
		candidate.FacilityID == "" || candidate.CustomerID == "" || candidate.BridgeID == "" ||
		!validJournalCode(reason) {
		return errors.New("collection_hold_request_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if _, err := lockFundingFacility(txCtx, tx, string(candidate.Mode), candidate.FacilityID); err != nil {
		return err
	}
	if _, err := lockFundingCustomer(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return errors.New("collection_customer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(txCtx, tx, string(candidate.Mode), candidate.CustomerID)
	if err != nil {
		return err
	}
	if err := lockCustomerReservations(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return err
	}
	if err := lockCustomerInvoices(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return err
	}
	if details == nil {
		details = map[string]any{}
	}
	details["deposit_id"] = candidate.DepositID
	if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
		bridgeIDs, reason, details); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("collection_hold_commit_failed")
	}
	return nil
}

// RecordCollectionEvidenceHold stores protected read evidence and invalidates
// the old budget when the provider definitively rejects its canonical deposit.
func (r *Repository) RecordCollectionEvidenceHold(ctx context.Context,
	candidate CollectionCandidate, code string, raw []byte) error {
	return r.recordCollectionEvidenceHold(ctx, candidate, code, raw,
		strings.HasPrefix(code, "PROVIDER_DEPOSIT_"))
}

// RecordCollectionDepositEvidenceHold invalidates the old budget after a
// definitive canonical deposit read failure or changed/ineligible response.
func (r *Repository) RecordCollectionDepositEvidenceHold(ctx context.Context,
	candidate CollectionCandidate, code string, raw []byte) error {
	return r.recordCollectionEvidenceHold(ctx, candidate, code, raw, true)
}

// RecordCollectionBalanceEvidenceHold persists a definitive balance-read hold.
func (r *Repository) RecordCollectionBalanceEvidenceHold(ctx context.Context,
	candidate CollectionCandidate, code string, raw []byte) error {
	return r.recordCollectionEvidenceHold(ctx, candidate, code, raw, false)
}

func (r *Repository) recordCollectionEvidenceHold(ctx context.Context,
	candidate CollectionCandidate, code string, raw []byte, depositFailure bool) error {
	if (candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox) ||
		candidate.FacilityID == "" || candidate.CustomerID == "" || candidate.DepositID == "" ||
		!validJournalCode(code) || len(raw) > maxProtectedOperationBodyBytes {
		return errors.New("collection_evidence_hold_invalid")
	}
	observationKind, source, observationStatus := "BALANCE", "PROVIDER_BALANCE_GET", "READ_HELD"
	if depositFailure {
		observationKind, source, observationStatus = "DEPOSIT", "PROVIDER_DEPOSIT_GET", "INELIGIBLE_OR_UNAVAILABLE"
	}
	rawAvailable := len(raw) > 0
	hashInput := raw
	if len(hashInput) == 0 {
		hashInput = []byte(code)
	}
	normalizedValue := map[string]any{"source": source, "code": code,
		"customer_id": candidate.CustomerID, "deposit_id": candidate.DepositID,
		"account_ref": candidate.AccountRef, "raw_evidence_available": rawAvailable}
	if rawAvailable {
		normalizedValue["raw_evidence_encoding"] = "base64"
		normalizedValue["raw_evidence_base64"] = base64.StdEncoding.EncodeToString(raw)
	}
	normalized, _ := json.Marshal(normalizedValue)
	digest := sha256.Sum256(hashInput)
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if _, err := lockFundingFacility(txCtx, tx, string(candidate.Mode), candidate.FacilityID); err != nil {
		return err
	}
	if _, err := lockFundingCustomer(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return errors.New("collection_customer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(txCtx, tx, string(candidate.Mode), candidate.CustomerID)
	if err != nil {
		return err
	}
	if err := lockCustomerReservations(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return err
	}
	if err := lockCustomerInvoices(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return err
	}
	var depositStatus, depositAccount, providerDepositID string
	err = tx.QueryRow(txCtx, `SELECT status,account_ref,provider_deposit_id FROM servicing.deposits
		WHERE mode=$1 AND id=$2 AND customer_id=$3 FOR UPDATE`, string(candidate.Mode),
		candidate.DepositID, candidate.CustomerID).Scan(&depositStatus, &depositAccount, &providerDepositID)
	if err != nil {
		return errors.New("collection_deposit_lock_failed")
	}
	var observationID string
	err = tx.QueryRow(txCtx, `INSERT INTO servicing.provider_observations(mode,observation_kind,
		provider_object_id,source_account_ref,status,observed_at,evidence_hash,normalized_evidence)
		VALUES($1,$2,$3,$4,$5,clock_timestamp(),$6,$7::jsonb) RETURNING id::text`,
		string(candidate.Mode), observationKind, providerDepositID, depositAccount,
		observationStatus, hex.EncodeToString(digest[:]), string(normalized)).Scan(&observationID)
	if err != nil {
		return errors.New("collection_provider_evidence_insert_failed")
	}
	reason := "COLLECTION_REVIEW_REQUIRED"
	if depositFailure {
		reason = "DEPOSIT_SOURCE_CONFLICT"
		if depositStatus == "SETTLED" {
			if _, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='HELD'
				WHERE mode=$1 AND id=$2 AND status='SETTLED'`, string(candidate.Mode), candidate.DepositID); err != nil {
				return errors.New("collection_deposit_hold_failed")
			}
		}
	}
	if err := holdCustomerCollections(txCtx, tx, candidate.Mode, candidate.CustomerID,
		bridgeIDs, reason, map[string]any{"provider_observation_id": observationID,
			"provider_error_code": code, "deposit_id": candidate.DepositID,
			"movement_certainty": "VERIFIED_PROVIDER_HOLD"}); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("collection_evidence_hold_commit_failed")
	}
	return nil
}

func (r *Repository) recordCollectionNoPlan(ctx context.Context, tx pgx.Tx,
	candidate CollectionCandidate, bridgeVersion int64, authorityID string,
	authorityVersion int64, depositObservationID, balanceObservationID string,
	evidence CollectionReadEvidence, reason string, now time.Time) (CollectionPlanResult, error) {
	key := collectionEvidenceKey(evidence.Deposit, evidence.Balance, candidate.BridgeID, candidate.DebtVersion,
		authorityID, authorityVersion)
	plannedOn := utcDate(now)
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.collection_plan_checks
		WHERE mode=$1 AND deposit_id=$2 AND planned_on=$3 AND evidence_key=$4)`,
		string(candidate.Mode), candidate.DepositID, plannedOn, key).Scan(&duplicate); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_evidence_read_failed")
	}
	if duplicate {
		if err := tx.Commit(ctx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_EVIDENCE_ALREADY_PLANNED"}, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM servicing.collection_plan_checks
		WHERE mode=$1 AND deposit_id=$2 AND planned_on=$3`, string(candidate.Mode),
		candidate.DepositID, plannedOn).Scan(&count); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_limit_read_failed")
	}
	if count >= 3 {
		var activeHold bool
		if err := tx.QueryRow(ctx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
			string(candidate.Mode), candidate.CustomerID).Scan(&activeHold); err != nil {
			return CollectionPlanResult{}, errors.New("collection_replan_customer_read_failed")
		}
		if !activeHold {
			if _, err := tx.Exec(ctx, `UPDATE servicing.customers SET active_hold=true,
				version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND active_hold=false`,
				string(candidate.Mode), candidate.CustomerID); err != nil {
				return CollectionPlanResult{}, errors.New("collection_replan_customer_hold_failed")
			}
		}
		if err := appendCollectionHold(ctx, tx, candidate.Mode, candidate.BridgeID,
			"COLLECTION_REVIEW_REQUIRED", map[string]any{"reason": "REPLAN_LIMIT_REACHED",
				"deposit_id": candidate.DepositID, "movement_certainty": "PROVEN_UNSENT"}); err != nil {
			return CollectionPlanResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CollectionPlanResult{}, errors.New("collection_replan_limit_commit_failed")
		}
		return CollectionPlanResult{BlockedCode: "COLLECTION_REPLAN_LIMIT_REACHED"}, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.collection_plan_checks(mode,deposit_id,
		bridge_id,planned_on,evidence_key,deposit_observation_id,balance_observation_id,
		authority_id,authority_version,bridge_version,bridge_debt_version,result_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, string(candidate.Mode),
		candidate.DepositID, candidate.BridgeID, plannedOn, key, depositObservationID,
		balanceObservationID, authorityID, authorityVersion, bridgeVersion, candidate.DebtVersion, reason); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_check_insert_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return CollectionPlanResult{}, errors.New("collection_plan_commit_failed")
	}
	return CollectionPlanResult{BlockedCode: reason}, nil
}

func collectionFreshTime(value, now time.Time) bool {
	return !value.IsZero() && !value.After(now.Add(5*time.Second)) && !now.After(value.Add(30*time.Second))
}

func collectionEvidenceKey(deposit provider.DepositObservation,
	balance provider.BalanceObservation, bridgeID string, debtVersion int64, authorityID string,
	authorityVersion int64) string {
	var depositFee int64
	var settledAt string
	if deposit.ProviderFeeCents != nil {
		depositFee = *deposit.ProviderFeeCents
	}
	if deposit.SettledAt != nil {
		settledAt = deposit.SettledAt.UTC().Format(time.RFC3339Nano)
	}
	body, _ := json.Marshal(struct {
		DepositAccount  string `json:"deposit_account"`
		CustomerID      string `json:"customer_id"`
		DepositID       string `json:"deposit_id"`
		Currency        string `json:"currency"`
		GrossCents      int64  `json:"gross_cents"`
		NetCents        int64  `json:"net_cents"`
		ProviderFee     int64  `json:"provider_fee_cents"`
		Status          string `json:"status"`
		Kind            string `json:"kind"`
		SettledAt       string `json:"settled_at"`
		BalanceAccount  string `json:"balance_account"`
		BalanceCurrency string `json:"balance_currency"`
		BalanceCents    int64  `json:"balance_cents"`
		BridgeID        string `json:"bridge_id"`
		DebtVersion     int64  `json:"bridge_debt_version"`
		AuthorityID     string `json:"authority_id"`
		AuthorityVer    int64  `json:"authority_version"`
	}{deposit.AccountRef, deposit.CustomerID, deposit.DepositID, deposit.Currency,
		deposit.GrossCents, deposit.NetCents, depositFee, deposit.Status, deposit.Kind,
		settledAt, balance.AccountRef, balance.Currency, balance.AvailableCents, bridgeID,
		debtVersion, authorityID, authorityVersion})
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func appendCollectionHold(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID, reason string, evidence map[string]any) error {
	var version int64
	var customerID, caseID, evaluationID, offerID string
	var holds []string
	if err := tx.QueryRow(ctx, `SELECT b.version,b.customer_id,COALESCE(b.originating_case_id,''),
		b.evaluation_id,b.offer_id::text,b.hold_reasons FROM servicing.bridges b
		WHERE b.mode=$1 AND b.id=$2`, string(mode), bridgeID).Scan(&version, &customerID,
		&caseID, &evaluationID, &offerID, &holds); err != nil {
		return errors.New("collection_hold_bridge_read_failed")
	}
	updated := appendUniqueString(holds, reason)
	if len(updated) == len(holds) {
		return nil
	}
	var nextVersion int64
	if err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET hold_reasons=$3,
		version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2
		AND version=$4 RETURNING version`, string(mode), bridgeID, updated, version).Scan(&nextVersion); err != nil {
		return errors.New("collection_hold_bridge_update_failed")
	}
	var caseRef any
	if caseID != "" {
		caseRef = caseID
	}
	evidence["customer_id"], evidence["account_ref"] = customerID, "customer:"+customerID
	evidence["originating_case_id"], evidence["evaluation_id"] = caseRef, evaluationID
	evidence["offer_id"], evidence["bridge_id"] = offerID, bridgeID
	evidence["bridge_version"], evidence["hold_code"] = nextVersion, reason
	summary, _ := json.Marshal(evidence)
	parent, parentVersion := bridgeID, nextVersion
	_, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "BRIDGE",
		ResourceID: bridgeID, ResourceVersion: nextVersion, ChangeType: reason,
		Summary: summary}, &parent, &parentVersion)
	return err
}

func collectionHasKind(kinds []string, value string) bool {
	for _, kind := range kinds {
		if kind == value {
			return true
		}
	}
	return false
}

// CollectionAuthorityAllows reports whether saved grant evidence includes the
// requested kind and its paired collection purpose.
func CollectionAuthorityAllows(kinds, purposes []string, kind string) bool {
	return collectionHasKind(kinds, kind) && collectionPurposeAllowed(purposes, kind)
}

func collectionPurposeAllowed(purposes []string, kind string) bool {
	purpose := "loan_credit_repayment"
	if kind == "COLLECT_FEE" {
		purpose = "professional_business_services"
	}
	for _, value := range purposes {
		if value == purpose {
			return true
		}
	}
	return false
}

func collectionMin(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func collectionStringSet(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
