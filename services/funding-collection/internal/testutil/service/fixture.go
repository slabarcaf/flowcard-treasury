// Package servicefixture wires one marked DEMO database, the real API/worker,
// real provider adapter and explicitly synthetic upstream/provider facts.
package servicefixture

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	fundinghttp "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/database"
	providerfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/provider"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/worker"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const (
	FacilityID      = "synthetic-facility"
	FundingAccount  = "synthetic-funding-account"
	CustomerID      = "synthetic-customer"
	BorrowerAccount = "synthetic-account"
	CardID          = "synthetic-card"
	TransactionID   = "synthetic-transaction"
	ShortfallID     = "synthetic-shortfall"
	EvaluationID    = "synthetic-evaluation"
	InvoiceID       = "invoice-1"
	InvoiceSystem   = "synthetic-invoicing-fixture"
)

const maxFixtureResponseBytes = 4_000_000

const (
	CustomerSubject = "synthetic-customer-subject"
	OperatorSubject = "synthetic-operator-subject"
	CustomerActorID = "synthetic-customer-actor"
	OperatorActorID = "synthetic-operator-actor"
	SystemActorID   = "synthetic-system-actor"
	CustomerSecret  = "synthetic-customer-secret-00000000000000000000000000000000"
	OperatorSecret  = "synthetic-operator-secret-00000000000000000000000000000000"
	CoordinatorKey  = "synthetic-coordinator-secret-00000000000000000000000000000000"
	ReaderKey       = "synthetic-reader-secret-00000000000000000000000000000000"
	UnderwritingKey = "synthetic-underwriting-secret-00000000000000000000000000000000"
	ShortfallKey    = "synthetic-shortfall-secret-00000000000000000000000000000000"
	WebhookKey      = "synthetic-webhook-secret-00000000000000000000000000000000"
)

// Harness owns the service components for one source-only final-test fixture.
type Harness struct {
	Pool             *pgxpool.Pool
	Maintenance      *pgxpool.Pool
	Config           config.Config
	Evaluator        *underwriting.Evaluator
	Provider         *provider.AirwallexAdapter
	ProviderHTTP     *providerfixture.Double
	API              *httptest.Server
	Worker           *worker.Worker
	Clock            func() time.Time
	UpstreamBehavior *UpstreamBehavior
}

// UpstreamBehavior changes only local synthetic reader responses.
type UpstreamBehavior struct {
	mu                sync.RWMutex
	evaluationMissing bool
}

// SetEvaluationMissing makes the next saved-assessment reads return 404.
func (b *UpstreamBehavior) SetEvaluationMissing(missing bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evaluationMissing = missing
}

func (b *UpstreamBehavior) isEvaluationMissing() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.evaluationMissing
}

// New creates the reviewed synthetic facility with either ABUNDANT (2000 bps)
// or TIGHT (8000 bps) configuration, then uses typed imports for initial facts.
func New(t testing.TB, now time.Time, reserveBPS int) (*Harness, error) {
	t.Helper()
	if reserveBPS != 2000 && reserveBPS != 8000 {
		return nil, errors.New("service_fixture_reserve_invalid")
	}
	now = now.UTC()
	book, err := database.Open(t)
	if err != nil {
		return nil, err
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()
	pool, err := book.OpenRuntimePool(setupCtx)
	if err != nil {
		return nil, err
	}
	synthetic, err := upstreamfixture.New(now, config.ModeDemo)
	if err != nil {
		return nil, err
	}
	synthetic.Input.Facility.ReserveFloorBPS = int64(reserveBPS)
	if reserveBPS == 8000 {
		synthetic.Input.Facility.FundedPrincipalCents = 0
		synthetic.Input.Facility.ReservedPrincipalCents = 0
	}
	providerDouble, adapter, err := providerfixture.NewWithFundingAccount(t, now, FundingAccount)
	if err != nil {
		return nil, err
	}
	providerDouble.SetBalance(BorrowerAccount, 200_000)
	providerDouble.SetPayment(provider.PaymentObservation{
		Mode: config.ModeDemo, CustomerID: CustomerID, CanonicalAccountID: BorrowerAccount,
		ParentTransactionID: TransactionID, AuthorizationEventID: "synthetic-event",
		AttemptedAmountCents: 900_000, ShortfallCents: 700_000, Currency: "USD",
		ObservedAt: now.Add(-5 * time.Second), AvailableCents: 200_000,
		BalanceObservedAt: now.Add(-10 * time.Second),
	})
	behavior := &UpstreamBehavior{}
	clock := func() time.Time { return time.Now().UTC() }
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveUpstream(w, r, synthetic, now, behavior)
	}))
	t.Cleanup(upstreamServer.Close)

	if err := book.SeedMapping(setupCtx, database.Mapping{
		CustomerID: CustomerID, AccountID: BorrowerAccount, CardID: CardID,
	}); err != nil {
		return nil, err
	}
	repository := postgres.NewRepository(pool)
	if err := importInitialFacts(setupCtx, repository, now, reserveBPS); err != nil {
		return nil, err
	}
	policyBinding := &fundinghttp.PolicyBinding{ID: synthetic.Evaluator.PolicyID(),
		Hash: synthetic.Evaluator.PolicyHash(), Pricing: synthetic.Evaluator.Pricing(), Verified: true}
	cfg := config.Config{
		DatabaseURL: book.DatabaseURL, Mode: config.ModeDemo, HTTPAddress: "127.0.0.1:0",
		FacilityID: FacilityID, SourceBindingID: "synthetic-fixture-binding",
		PolicyID: policyBinding.ID, PolicyHash: policyBinding.Hash,
		CoordinatorSecret: CoordinatorKey, OperatorSecret: OperatorSecret,
		CustomerSecret: CustomerSecret, ReaderSecret: ReaderKey,
		UnderwritingURL: upstreamServer.URL, UnderwritingSecret: UnderwritingKey,
		ShortfallURL: upstreamServer.URL, ShortfallSecret: ShortfallKey,
		ProviderMode: config.ProviderMock, ProviderBaseURL: providerDouble.BaseURL(),
		AirwallexWebhookSecret: WebhookKey, AirwallexClientID: config.DemoClientID,
		AirwallexAPIKey: config.DemoAPIKey, HTTPTimeout: 5 * time.Second,
	}
	handler := fundinghttp.NewHandlerWithFunding(pool, cfg, policyBinding,
		synthetic.Evaluator, adapter, clock)
	api := httptest.NewServer(handler)
	t.Cleanup(api.Close)
	preparer, err := worker.NewDispatchPreparer(cfg, repository, adapter, synthetic.Evaluator,
		synthetic.Evaluator.Pricing(), true, clock)
	if err != nil {
		return nil, err
	}
	serialWorker, err := worker.New(cfg, repository, preparer, adapter, clock)
	if err != nil {
		return nil, err
	}
	return &Harness{Pool: pool, Maintenance: book.MaintenancePool(), Config: cfg, Evaluator: synthetic.Evaluator,
		Provider: adapter, ProviderHTTP: providerDouble,
		API: api, Worker: serialWorker, Clock: clock, UpstreamBehavior: behavior}, nil
}

// NewWorker builds a fresh serial worker over the same durable repository.
func (h *Harness) NewWorker() (*worker.Worker, error) {
	repository := postgres.NewRepository(h.Pool)
	preparer, err := worker.NewDispatchPreparer(h.Config, repository, h.Provider, h.Evaluator,
		h.Evaluator.Pricing(), true, h.Clock)
	if err != nil {
		return nil, err
	}
	return worker.New(h.Config, repository, preparer, h.Provider, h.Clock)
}

func importInitialFacts(ctx context.Context, repository *postgres.Repository, now time.Time, reserveBPS int) error {
	ref := domain.SourceRef{System: "synthetic-funding-fixture", RecordID: "fixture-v1", Version: "1", EffectiveAt: now}
	metadata := func(sourceID string) domain.ImportMetadata {
		return domain.ImportMetadata{SchemaVersion: 1, SourceMode: config.ModeDemo, SourceID: sourceID,
			SourceVersion: "1", CapturedAt: now, SourceRefs: []domain.SourceRef{ref}}
	}
	_, err := repository.ImportFacility(ctx, domain.FacilityImport{
		ImportMetadata: metadata("fixture-facility"), FacilityID: FacilityID,
		FundingAccountRef: FundingAccount, Currency: "USD", FacilityTotalCents: 100_000_000,
		ReserveFloorBPS: reserveBPS, KnownEmptyBooks: true,
		Customers: []domain.FacilityCustomerImport{{CustomerID: CustomerID, AccountID: BorrowerAccount,
			CardID: CardID, HistoryComplete: true}},
	}, FacilityID, now)
	if err != nil {
		return errors.New("service_fixture_facility_import_failed")
	}
	dueDate := now.AddDate(0, 0, 20).Format("2006-01-02")
	invoiceRef := domain.SourceRef{System: InvoiceSystem, RecordID: InvoiceID, Version: "1", EffectiveAt: now}
	_, err = repository.ImportReceivables(ctx, domain.ReceivablesImport{
		ImportMetadata: metadata("fixture-receivables"),
		Receivables: []domain.ReceivableImport{{CustomerID: CustomerID,
			SourceSystem: InvoiceSystem, InvoiceID: InvoiceID, PayerID: "synthetic-payer",
			Currency: "USD", EligibleUnpaidCents: 800_000, DueOn: &dueDate,
			DueDateVerified: true, AllocationComplete: true, SourceComplete: true,
			SourceRef: invoiceRef}},
	}, FacilityID, now)
	if err != nil {
		return errors.New("service_fixture_receivables_import_failed")
	}
	customerRef := domain.SourceRef{System: "synthetic-actor-fixture", RecordID: CustomerActorID, Version: "1", EffectiveAt: now}
	operatorRef := domain.SourceRef{System: "synthetic-actor-fixture", RecordID: OperatorActorID, Version: "1", EffectiveAt: now}
	systemRef := domain.SourceRef{System: "synthetic-actor-fixture", RecordID: SystemActorID, Version: "1", EffectiveAt: now}
	customer := CustomerID
	_, err = repository.ImportActors(ctx, domain.ActorsImport{
		ImportMetadata: metadata("fixture-actors"), Actors: []domain.ActorImport{
			{ActorID: CustomerActorID, ActorType: "CUSTOMER", AuthenticationSubject: CustomerSubject,
				CustomerID: &customer, Active: true, SourceRef: customerRef},
			{ActorID: OperatorActorID, ActorType: "OPERATOR", AuthenticationSubject: OperatorSubject,
				Active: true, SourceRef: operatorRef},
			{ActorID: SystemActorID, ActorType: "SYSTEM", AuthenticationSubject: "system:funding-collection",
				Active: true, SourceRef: systemRef},
		},
	}, FacilityID, now)
	if err != nil {
		return errors.New("service_fixture_actors_import_failed")
	}
	mandateRef := domain.SourceRef{System: "synthetic-authority-fixture", RecordID: "mandate-1", Version: "1", EffectiveAt: now}
	_, err = repository.ImportAuthorities(ctx, domain.AuthoritiesImport{
		ImportMetadata: metadata("fixture-authorities"), Authorities: []domain.AuthorityImport{{
			ID: "authority-1", Version: 1, CustomerID: CustomerID,
			SourceAccountRef: BorrowerAccount, DestinationAccountRef: FundingAccount,
			ProductCode: "CONNECTED_ACCOUNT_TRANSFERS", MandateReference: "mandate-1", Currency: "USD",
			PermittedKinds:    []string{"COLLECT_FEE", "COLLECT_PRINCIPAL"},
			PermittedPurposes: []string{"professional_business_services", "loan_credit_repayment"},
			MaximumDebitCents: 2_000_000, ValidFrom: now.Add(-time.Hour), ExpiresAt: now.Add(90 * 24 * time.Hour),
			Status: "ACTIVE", DisclosuresVersion: "disclosure-v1", EvidenceRefs: []domain.SourceRef{mandateRef},
		}},
	}, FacilityID, now)
	if err != nil {
		return errors.New("service_fixture_authority_import_failed")
	}
	return nil
}

func serveUpstream(w http.ResponseWriter, r *http.Request, fixture *upstreamfixture.Synthetic, now time.Time, behavior *UpstreamBehavior) {
	if r.Method != http.MethodGet || r.Header.Get("Authorization") == "" {
		writeFixture(w, http.StatusUnauthorized, map[string]string{"error": "fixture_auth_required"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/underwriting/evaluations/"+EvaluationID:
		if behavior.isEvaluationMissing() {
			writeFixture(w, http.StatusNotFound, map[string]string{"error": "fixture_evaluation_unavailable"})
			return
		}
		result, err := fixture.Evaluate()
		if err != nil {
			writeFixture(w, http.StatusServiceUnavailable, map[string]string{"error": "fixture_evaluation_unavailable"})
			return
		}
		evaluation, err := savedEvaluation(result, fixture, now)
		if err != nil {
			writeFixture(w, http.StatusServiceUnavailable, map[string]string{"error": "fixture_evaluation_invalid"})
			return
		}
		writeFixture(w, http.StatusOK, evaluation)
	case path == "/api/underwriting/customers/"+CustomerID+"/context":
		writeFixture(w, http.StatusOK, customerContext(fixture))
	case strings.HasPrefix(path, "/api/shortfalls/") && strings.TrimPrefix(path, "/api/shortfalls/") == ShortfallID:
		writeFixture(w, http.StatusOK, shortfallResponse(fixture, now))
	default:
		writeFixture(w, http.StatusNotFound, map[string]string{"error": "fixture_resource_not_found"})
	}
}

func savedEvaluation(result policy.Result, fixture *upstreamfixture.Synthetic, now time.Time) (map[string]any, error) {
	var principal, fee, due int64
	if result.PrincipalCents != nil {
		principal = *result.PrincipalCents
	}
	if result.EstimatedFeeCents != nil {
		fee = *result.EstimatedFeeCents
	}
	if result.TotalDueCents != nil {
		due = *result.TotalDueCents
	}
	rules := make([]map[string]any, 0, len(result.Rules))
	for _, rule := range result.Rules {
		rules = append(rules, map[string]any{"rule_id": rule.RuleID, "status": rule.Status,
			"reason_code": rule.ReasonCode, "actual": rule.Actual, "threshold": rule.Threshold,
			"source_refs": rule.SourceRefs})
	}
	approval := result.ApprovalRequired
	funding := result.FundingAuthorized
	recheck := result.RequiresRecheck
	return map[string]any{"id": EvaluationID, "shortfall_id": ShortfallID,
		"customer_id": CustomerID, "account_id": BorrowerAccount, "transaction_id": TransactionID,
		"source_mode": config.ModeDemo, "policy_id": result.PolicyID, "policy_hash": result.PolicyHash,
		"status": result.Status, "approval_required": &approval, "funding_authorized": &funding,
		"requires_recheck": &recheck, "principal_cents": &principal,
		"term_days": &result.TermDays, "estimated_fee_cents": &fee, "total_due_cents": &due,
		"reason_codes": result.ReasonCodes, "evaluated_at": result.EvaluatedAt,
		"created_at": now, "rules": rules}, nil
}

func customerContext(fixture *upstreamfixture.Synthetic) map[string]any {
	input := fixture.Input.Customer
	data := map[string]any{"account_id": input.AccountID, "card_id": input.CardID,
		"standing": input.Standing, "history_window": input.HistoryWindow,
		"balance_history": input.BalanceHistory, "inflow_history": input.InflowHistory,
		"bridges": input.Bridges, "reservations": input.Reservations,
		"receivables": input.Receivables, "classifications": input.Classifications}
	return map[string]any{"schema_version": 1, "source_mode": config.ModeDemo,
		"customer_id": CustomerID, "snapshot_id": input.ID, "snapshot_version": input.Version,
		"captured_at": input.CapturedAt, "policy_id": fixture.Evaluator.PolicyID(),
		"policy_hash": fixture.Evaluator.PolicyHash(), "fresh": true,
		"coverage": input.Coverage, "data": data, "source_refs": input.SourceRefs}
}

func shortfallResponse(fixture *upstreamfixture.Synthetic, now time.Time) map[string]any {
	value := fixture.Input.Shortfall
	created := now.Add(-2 * time.Second)
	requested := now.Add(-10 * time.Second)
	observed := value.BalanceObservedAt
	card := value.CardID
	currency := value.Currency
	attempted, available, shortfall := value.AttemptedAmountCents, value.AvailableBalanceCents, value.ShortfallCents
	transaction := value.TransactionID
	return map[string]any{"id": value.ID, "customer_id": value.CustomerID, "account_id": value.AccountID,
		"card_id": &card, "outcome": value.Outcome, "currency": &currency,
		"attempted_amount_cents": &attempted, "available_balance_cents": &available,
		"shortfall_cents": &shortfall, "canonical_event_id": value.CanonicalEventID,
		"transaction_id": &transaction, "created_at": created,
		"balance_requested_at": &requested, "balance_observed_at": &observed,
		"requires_recheck": true, "amount_semantics": "current_need_at_balance_observation"}
}

func writeFixture(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Token creates a short-lived local fixture assertion using the service's
// verified issuer/audience and one explicitly synthetic role secret.
func Token(subject, secret string, now time.Time) string {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": "flowcard-go", "aud": "flowcard-funding-collection",
		"sub": subject, "jti": "synthetic-" + subject, "iat": now.Unix(), "exp": now.Add(20 * time.Second).Unix()})
	encode := base64.RawURLEncoding.EncodeToString
	input := encode(header) + "." + encode(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + encode(mac.Sum(nil))
}

// Request performs one local API request with a synthetic bearer assertion.
func (h *Harness) Request(method, path, subject, secret, key string, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.API.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+Token(subject, secret, time.Now().UTC()))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := h.API.Client().Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, errors.New("service_fixture_http_request_failed")
	}
	if err := BufferResponse(response); err != nil {
		return nil, err
	}
	return response, nil
}

// BufferResponse reads and closes a local HTTP response before its request
// context is released, then returns a bounded in-memory body to the caller.
func BufferResponse(response *http.Response) error {
	if response == nil || response.Body == nil {
		return errors.New("service_fixture_http_response_missing")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxFixtureResponseBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(body) > maxFixtureResponseBytes {
		return errors.New("service_fixture_http_response_read_failed")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return nil
}
