package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
)

func testRepository(t *testing.T) (*postgres.Repository, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	databaseURL := strings.TrimSpace(os.Getenv("COORDINATOR_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("COORDINATOR_TEST_DATABASE_URL is required")
	}
	repo, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect isolated coordinator database: %v", err)
	}
	t.Cleanup(repo.Close)
	stamp := time.Now().UTC().Format("150405.000000000")
	binding := "g_" + strings.ReplaceAll(stamp, ".", "")
	fingerprint := sha256.Sum256([]byte(binding))
	if err := repo.EnsureBinding(ctx, binding, "DEMO", hex.EncodeToString(fingerprint[:]), "fixture-http"); err != nil {
		t.Fatalf("ensure test binding: %v", err)
	}
	return repo, ctx, binding
}

func TestDiscoveryRecovery(t *testing.T) {
	repo, ctx, binding := testRepository(t)
	checkLifecycle := func(sourceLifecycle string, wantValid bool) {
		t.Helper()
		bridgeID := "bridge-lifecycle"
		bridge := servicing.Bridge{
			ObligationID: bridgeID, ID: bridgeID, Mode: "DEMO", SourceBindingID: binding,
			CustomerID: "cust-lifecycle", AccountRef: domain.InternalAccountRef("cust-lifecycle"),
			Version: 1, AsOf: time.Now().UTC(), Currency: "USD", Lifecycle: sourceLifecycle,
			HoldReasons: []string{}, AllowedNextActions: []string{}, Operations: []servicing.Operation{},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(bridge)
		}))
		defer server.Close()
		client, err := servicing.New(server.URL, strings.Repeat("s", 32), "DEMO", binding, server.Client())
		if err != nil {
			t.Fatalf("servicing.New() = %v", err)
		}
		got, _, err := client.ReadBridge(ctx, bridgeID)
		if !wantValid {
			if err == nil {
				t.Fatalf("ReadBridge(lifecycle=%q) accepted an unknown value", sourceLifecycle)
			}
			return
		}
		if err != nil || got.Lifecycle != sourceLifecycle || got.PrincipalOwedCents != nil || got.ContractFeeOwedCents != nil || got.LateFeeOwedCents != nil {
			t.Fatalf("ReadBridge(lifecycle=%q) = lifecycle %q debt %+v/%+v/%+v, err %v", sourceLifecycle, got.Lifecycle, got.PrincipalOwedCents, got.ContractFeeOwedCents, got.LateFeeOwedCents, err)
		}
	}
	checkLifecycle("VOID_UNFUNDED", true)
	checkLifecycle("REOPENED_HELD", true)
	checkLifecycle("UNKNOWN_FUTURE_STATE", false)

	customerID := "cust-g-discovery"
	accountRef := domain.InternalAccountRef(customerID)
	rawAccountID := customerID
	detectionID := "detection-g-discovery"
	caseID, err := domain.StableCaseID(binding, domain.CaseShortfall, detectionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	page := postgres.DiscoveryPage{BindingID: binding, NextCursor: stringPointer("next"), StartedAt: now, SucceededAt: now, GreatestCreatedAt: &now, GreatestDetectionID: &detectionID, Cases: []postgres.DiscoveryCase{{CaseID: caseID, DetectionID: detectionID, CreatedAt: now, CustomerID: &customerID, AccountRef: &accountRef, RawAccountID: &rawAccountID, Status: "HELD", Stage: "HELD"}}}
	created, err := repo.SaveShortfallPage(ctx, page)
	if err != nil || created != 1 {
		t.Fatalf("first shortfall page: created=%d err=%v", created, err)
	}
	if err := repo.RestartShortfallScan(ctx, binding, page.NextCursor, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	lateDetectionID := "detection-g-late-older"
	lateCaseID, err := domain.StableCaseID(binding, domain.CaseShortfall, lateDetectionID)
	if err != nil {
		t.Fatal(err)
	}
	lateCreatedAt := now.Add(-time.Hour)
	page.ExpectedCursor, page.NextCursor = nil, nil
	page.StartedAt, page.SucceededAt = now.Add(time.Second), now.Add(2*time.Second)
	page.Cases = append(page.Cases, postgres.DiscoveryCase{CaseID: lateCaseID, DetectionID: lateDetectionID, CreatedAt: lateCreatedAt, CustomerID: &customerID, AccountRef: &accountRef, RawAccountID: &rawAccountID, Status: "HELD", Stage: "HELD"})
	created, err = repo.SaveShortfallPage(ctx, page)
	if err != nil || created != 1 {
		t.Fatalf("late-visible older shortfall page: created=%d err=%v", created, err)
	}
	state, err := repo.ReadDiscoveryState(ctx, binding)
	if err != nil || state.GreatestCreatedAt == nil || !state.GreatestCreatedAt.Equal(now) || state.GreatestDetectionID == nil || *state.GreatestDetectionID != detectionID {
		t.Fatalf("late older row moved the discovery high-watermark: state=%#v err=%v", state, err)
	}
	items, err := repo.ListCases(ctx, binding, "DEMO", nil, nil, "", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("late older shortfall was not persisted once: count=%d err=%v", len(items), err)
	}
	caseCreatedAt := map[string]time.Time{}
	for _, item := range items {
		if item.DetectionID != nil {
			caseCreatedAt[*item.DetectionID] = item.CreatedAt
		}
	}
	if !caseCreatedAt[lateDetectionID].Equal(lateCreatedAt) || !caseCreatedAt[detectionID].Equal(now) {
		t.Fatalf("shortfall source creation times were not retained: got late=%v original=%v", caseCreatedAt[lateDetectionID], caseCreatedAt[detectionID])
	}
	created, err = repo.SaveShortfallPage(ctx, page)
	if err != nil || created != 0 {
		t.Fatalf("replayed older shortfall page: created=%d err=%v", created, err)
	}
	items, err = repo.ListCases(ctx, binding, "DEMO", nil, nil, "", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("shortfall replay created duplicate cases: count=%d err=%v", len(items), err)
	}
	t.Logf("case_id=%s detection_id=%s late_case_id=%s late_detection_id=%s high_watermark_preserved=true source_mode=DEMO", caseID, detectionID, lateCaseID, lateDetectionID)

	bridgeID := "bridge-g-discovery"
	bridgeVersion := int64(1)
	bridgeCustomer := "cust-g-collection"
	bridgeAccount := domain.InternalAccountRef(bridgeCustomer)
	if err := repo.SaveServicingPage(ctx, binding, "DEMO", 0, 1, []postgres.ServicingChange{{Sequence: 1, Mode: "DEMO", ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: bridgeVersion, BridgeID: &bridgeID, BridgeVersion: &bridgeVersion, CustomerID: &bridgeCustomer, AccountRef: &bridgeAccount, ChangeType: "CREATED", RecordedAt: now, Summary: json.RawMessage(`{"status":"created"}`)}}); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	reopenedRepo, err := postgres.Connect(ctx, os.Getenv("COORDINATOR_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("reopen isolated repository: %v", err)
	}
	t.Cleanup(reopenedRepo.Close)
	repo = reopenedRepo
	state, err = repo.ReadDiscoveryState(ctx, binding)
	if err != nil || state.ServicingSequence != 1 {
		t.Fatalf("servicing checkpoint did not survive repository reconnect: state=%#v err=%v", state, err)
	}
	persistedCases, err := repo.ListCases(ctx, binding, "DEMO", nil, nil, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	dirtyCaseFound := false
	for _, item := range persistedCases {
		if item.BridgeID != nil && *item.BridgeID == bridgeID {
			dirtyCaseFound = item.RefreshNeeded
		}
	}
	if !dirtyCaseFound {
		t.Fatal("dirty refresh was lost after repository reconnect")
	}
	claimed, err := repo.ClaimNext(ctx, 2*time.Second, binding, "DEMO")
	if err != nil {
		t.Fatalf("claim dirty bridge: %v", err)
	}
	if claimed.Kind != domain.CaseCollection || !claimed.RefreshNeeded || claimed.LeaseStage == nil || *claimed.LeaseStage != "REFRESH_BRIDGE" {
		t.Fatalf("first bridge event did not retain refresh work: %#v", claimed)
	}
	refreshBody, _ := json.Marshal(map[string]any{"bridge": map[string]any{"id": bridgeID, "version": bridgeVersion}})
	if err := repo.CompleteRefreshDisposition(ctx, claimed.ID, *claimed.LeaseToken, bridgeVersion, bridgeID, now, refreshBody, postgres.RefreshDisposition{Status: "MONITORING", NextStage: "MONITORING"}); err != nil {
		t.Fatalf("commit bridge refresh: %v", err)
	}
	snapshot, err := repo.ReadCollectionSnapshot(ctx, claimed.ID, binding, "DEMO")
	if err != nil || snapshot.BridgeVersion != bridgeVersion {
		t.Fatalf("read committed bridge snapshot: snapshot=%#v err=%v", snapshot, err)
	}
	committedView, err := repo.ReadCaseScoped(ctx, claimed.ID, binding, "DEMO")
	if err != nil || committedView.Case.RefreshNeeded || committedView.Case.BridgeVersion == nil || *committedView.Case.BridgeVersion != bridgeVersion {
		t.Fatalf("refresh disposition did not atomically clear dirty work: view=%#v err=%v", committedView, err)
	}
	t.Logf("case_id=%s bridge_id=%s bridge_version=%d source_mode=DEMO", claimed.ID, bridgeID, bridgeVersion)
}

func TestAgentAuthority(t *testing.T) {
	repo, ctx, binding := testRepository(t)
	customerID := "cust-g-agent"
	accountRef := domain.InternalAccountRef(customerID)
	rawAccountID := customerID
	detectionID := "detection-g-agent"
	caseID, _ := domain.StableCaseID(binding, domain.CaseShortfall, detectionID)
	if _, err := repo.CreateCase(ctx, domain.Case{ID: caseID, BindingID: binding, Kind: domain.CaseShortfall, DetectionID: &detectionID, CustomerID: &customerID, AccountRef: &accountRef, RawAccountID: &rawAccountID, Status: domain.CaseNew, NextStage: "INVESTIGATE"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimNext(ctx, 30*time.Second, binding, "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifestDir := filepath.Join(root, "retained")
	if err := os.Mkdir(manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	text := []byte("Invoice due date: 2026-12-01\n")
	if err := os.WriteFile(filepath.Join(manifestDir, "invoice.txt"), text, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(text)
	manifest, _ := json.Marshal(sources.Manifest{Version: 1, Mode: "DEMO", CustomerID: customerID, AccountRef: accountRef, BindingID: binding, Sources: []sources.Source{{Handle: "invoice-1", Type: "INVOICE", RecordID: "invoice-g-agent", Version: "v1", ExportVersion: "run-g", Redacted: true, Sections: []sources.Section{{ID: "payment-terms", Locator: "invoice/terms", Path: "retained/invoice.txt", SHA256: hex.EncodeToString(hash[:])}}}}})
	if err := os.WriteFile(filepath.Join(root, caseID+".json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := sources.LoadForCase(root, caseID, sources.Scope{Mode: "DEMO", CustomerID: customerID, AccountRef: accountRef, BindingID: binding})
	if err != nil {
		t.Fatalf("load retained invoice: %v", err)
	}
	approval := false
	assessment := &agent.SavedAssessment{ID: "eval-g-agent", Status: "REVIEW_REQUIRED", ShortfallID: detectionID, TermDays: 30, ApprovalRequired: &approval, RequiresRecheck: true, RuleReasons: []agent.Rule{{ID: "RECEIVABLE_DUE_DATE", Status: "UNKNOWN", Reason: "RECEIVABLE_DUE_DATE_UNKNOWN"}}}
	transactionID, cardID := "private-transaction", "private-card"
	originalContext := customercontext.Response{
		SchemaVersion: 1, SourceMode: "DEMO", CustomerID: customerID, SnapshotID: "snapshot-g-agent",
		SnapshotVersion: 1, CapturedAt: time.Now().UTC(), PolicyID: "fixture-context-v1",
		PolicyHash: strings.Repeat("a", 64), Coverage: map[string]bool{"liability": true}, Fresh: true,
		Data:       json.RawMessage(`{"account_id":"cust-g-agent","card_id":"private-card","customer_id":"cust-g-agent","account_ref":"customer:cust-g-agent","liability":12}`),
		SourceRefs: []customercontext.SourceRef{{System: "underwriting", RecordID: accountRef, Version: "snapshot-v1"}},
	}
	privateContextData, err := json.Marshal(originalContext)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := contextForAgent(originalContext, customerID, rawAccountID, cardID, transactionID, accountRef)
	if err != nil {
		t.Fatalf("project account/customer overlap: %v", err)
	}
	packet := agent.Packet{CaseID: caseID, SourceBindingID: binding, RunID: "run-g-agent", Kind: agent.Shortfall, Mode: "DEMO", CustomerID: customerID, AccountRef: accountRef, ShortfallID: detectionID, Assessment: assessment, Context: &projected, PrivateContextData: privateContextData, RequiredContextFields: []string{"liability"}, Rules: assessment.RuleReasons, EffectiveActions: agent.ActionsForAssessment(*assessment), SourceReader: reader, AllowedClaimFields: []string{"receivable_due_date"}, PromptVersion: agent.PromptVersion, ModelVersion: "fixture-v1", AgentMode: "FIXTURE"}
	packet.RunID, err = domain.StableRunID(caseID, 1)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := agent.RunContextRefs(packet)
	if err != nil {
		t.Fatalf("build packet: %v", err)
	}
	row := domain.AssessmentRequest{ID: "assessment-row-g-agent", Revision: 1, SourceTransactionID: &transactionID, SourceCardID: &cardID}
	evaluation := underwriting.Evaluation{ID: assessment.ID, ShortfallID: detectionID, CustomerID: customerID, AccountID: rawAccountID, TransactionID: transactionID, SourceMode: "DEMO"}
	privateRefs, err := shortfallPrivateContextRefs(claimed, packet.RunID, packet, evaluation, row, "")
	if err != nil {
		t.Fatalf("pin private context: %v", err)
	}
	if _, err := repo.StartRun(ctx, caseID, *claimed.LeaseToken, packet.RunID, 1, "INVESTIGATE", packet.PromptVersion, packet.ModelVersion, refs, privateRefs); err != nil {
		t.Fatal(err)
	}
	executor := &StageExecutor{Repo: repo, Mode: "DEMO", SourceRoot: root}
	restored, limitation := executor.restorePinnedShortfallPacket(claimed, packet.RunID, refs, privateRefs, evaluation, row)
	if limitation != "" {
		t.Fatalf("restore pinned context packet: %s", limitation)
	}
	restoredRefs, err := agent.RunContextRefs(restored)
	if err != nil || !sameJSON(restoredRefs, refs) || !sameJSON(restored.PrivateContextData, privateContextData) {
		t.Fatalf("context packet did not round-trip semantically: err=%v", err)
	}
	var publicPacket struct {
		Context *struct {
			Data       map[string]json.RawMessage  `json:"data"`
			SourceRefs []customercontext.SourceRef `json:"source_refs"`
		} `json:"customer_context"`
	}
	if json.Unmarshal(refs, &publicPacket) != nil || publicPacket.Context == nil {
		t.Fatalf("pinned model references have no context: %s", refs)
	}
	publicData := publicPacket.Context.Data
	if string(publicData["customer_id"]) != `"cust-g-agent"` || string(publicData["account_ref"]) != `"customer:cust-g-agent"` || string(publicData["liability"]) != "12" {
		t.Fatalf("public context lost verified facts or host alias: %s", refs)
	}
	for _, privateKey := range []string{"account_id", "card_id", "transaction_id"} {
		if _, ok := publicData[privateKey]; ok {
			t.Fatalf("public context retained private field %q: %s", privateKey, refs)
		}
	}
	if strings.Contains(string(refs), cardID) || strings.Contains(string(refs), transactionID) {
		t.Fatalf("public context retained private card/transaction identifier: %s", refs)
	}
	aliasRefRetained := false
	for _, sourceRef := range publicPacket.Context.SourceRefs {
		if sourceRef.RecordID == accountRef {
			aliasRefRetained = true
		}
	}
	if !aliasRefRetained {
		t.Fatalf("verified alias source reference was lost: %s", refs)
	}
	packet = restored

	wrong := packet
	wrong.CustomerID = "cust-other"
	var providerCalls int
	provider := countingProvider{calls: &providerCalls}
	engine := &agent.Engine{Repo: repo, Provider: provider, Mode: "FIXTURE"}
	if _, err := engine.Step(ctx, wrong, *claimed.LeaseToken); err == nil || providerCalls != 0 {
		t.Fatalf("foreign packet reached provider: err=%v calls=%d", err, providerCalls)
	}
	fixture := &agent.Engine{Repo: repo, Provider: &agent.Fixture{Packet: packet}, Mode: "FIXTURE"}
	var completed bool
	for call := 0; call < 6; call++ {
		result, err := fixture.Step(ctx, packet, *claimed.LeaseToken)
		if err != nil {
			t.Fatalf("fixture agent step %d: %v", call, err)
		}
		if result.Finished {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("fixture did not finish read-submit-finish flow")
	}
	evidence, err := repo.ListEvidence(ctx, caseID)
	if err != nil || len(evidence) != 1 || evidence[0].State != "PENDING_REVIEW" {
		t.Fatalf("fixture evidence is not pending review: count=%d err=%v", len(evidence), err)
	}
	t.Logf("case_id=%s evaluation_id=%s evidence_id=%s evidence_state=%s agent_mode=FIXTURE", caseID, assessment.ID, evidence[0].ID, evidence[0].State)

	// A separate saved decision exercises durable model-call exhaustion and the
	// RULE_TEMPLATE completion path without changing underwriting bytes.
	fallbackDetection := "detection-g-limit-fallback"
	fallbackCaseID, _ := domain.StableCaseID(binding, domain.CaseShortfall, fallbackDetection)
	fallbackRawAccount := "raw-g-limit-fallback"
	if _, err := repo.CreateCase(ctx, domain.Case{ID: fallbackCaseID, BindingID: binding, Kind: domain.CaseShortfall, DetectionID: &fallbackDetection, CustomerID: &customerID, AccountRef: &accountRef, RawAccountID: &fallbackRawAccount, Status: domain.CaseNew, NextStage: "INVESTIGATE"}); err != nil {
		t.Fatal(err)
	}
	fallbackClaim, err := repo.ClaimNext(ctx, 30*time.Second, binding, "DEMO")
	if err != nil || fallbackClaim.ID != fallbackCaseID {
		t.Fatalf("claim fallback case: %#v %v", fallbackClaim, err)
	}
	fallbackResponse := evaluationResponse(fallbackDetection, customerID, fallbackRawAccount, "txn-g-limit-fallback")
	fallbackEval, err := underwriting.DecodeEvaluation(fallbackResponse)
	if err != nil {
		t.Fatal(err)
	}
	fallbackBody, _ := underwriting.EvaluationRequestBody(fallbackDetection, 30)
	fallbackRowID, err := repo.PrepareAssessment(ctx, fallbackCaseID, *fallbackClaim.LeaseToken, 1, "coordinator:"+fallbackCaseID+":r1", "txn-g-limit-fallback", "card-g-limit-fallback", fallbackBody)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAssessmentResult(ctx, fallbackCaseID, *fallbackClaim.LeaseToken, fallbackRowID, fallbackEval.ID, fallbackResponse); err != nil {
		t.Fatal(err)
	}
	fallbackRunID, _ := domain.StableRunID(fallbackCaseID, 1)
	fallbackSaved := &agent.SavedAssessment{ID: fallbackEval.ID, Status: fallbackEval.Status, ShortfallID: fallbackDetection, TermDays: fallbackEval.TermDays, ApprovalRequired: &fallbackEval.ApprovalRequired, RequiresRecheck: fallbackEval.RequiresRecheck}
	for _, rule := range fallbackEval.Rules {
		fallbackSaved.RuleReasons = append(fallbackSaved.RuleReasons, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
	}
	fallbackPacket := agent.Packet{CaseID: fallbackCaseID, SourceBindingID: binding, RunID: fallbackRunID, Kind: agent.Shortfall, Mode: "DEMO", CustomerID: customerID, AccountRef: accountRef, ShortfallID: fallbackDetection, Assessment: fallbackSaved, Rules: fallbackSaved.RuleReasons, EffectiveActions: agent.ActionsForAssessment(*fallbackSaved), PromptVersion: agent.PromptVersion, ModelVersion: "limit-test-v1", AgentMode: "FIXTURE"}
	fallbackRefs, err := agent.RunContextRefs(fallbackPacket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.StartRun(ctx, fallbackCaseID, *fallbackClaim.LeaseToken, fallbackRunID, 1, "INVESTIGATE", fallbackPacket.PromptVersion, fallbackPacket.ModelVersion, fallbackRefs, []byte(`{"scope":"saved-assessment"}`)); err != nil {
		t.Fatal(err)
	}
	loopProvider := &repeatingToolProvider{}
	limitedEngine := &agent.Engine{Repo: repo, Provider: loopProvider, Mode: "FIXTURE"}
	var limited agent.StepResult
	for call := 0; call < agent.MaxModelCalls; call++ {
		limited, err = limitedEngine.Step(ctx, fallbackPacket, *fallbackClaim.LeaseToken)
		if err != nil {
			t.Fatalf("bounded model attempt %d: %v", call+1, err)
		}
		if limited.Finished {
			break
		}
	}
	if !limited.Finished || limited.Finish != nil {
		t.Fatalf("provider call limit did not force fallback: %#v", limited)
	}
	fallbackExecutor := &StageExecutor{Repo: repo}
	if _, err := fallbackExecutor.completeRuleTemplate(ctx, fallbackClaim, *fallbackClaim.LeaseToken, fallbackRunID, domain.AssessmentRequest{ID: fallbackRowID, Revision: 1}, fallbackEval, limited.Limitation); err != nil {
		t.Fatalf("publish saved-result fallback: %v", err)
	}
	afterFallback, err := repo.ReadAssessment(ctx, fallbackCaseID, 1)
	if err != nil || string(afterFallback.Response) != string(fallbackResponse) {
		t.Fatalf("rule template changed saved assessment: err=%v", err)
	}
	t.Logf("case_id=%s evaluation_id=%s fallback=RULE_TEMPLATE saved_response_unchanged=true", fallbackCaseID, fallbackEval.ID)
}

type countingProvider struct{ calls *int }

func (p countingProvider) CreateMessage(context.Context, agent.Request) (agent.Response, error) {
	*p.calls++
	return agent.Response{}, errors.New("unexpected provider call")
}

type repeatingToolProvider struct{ calls int }

func (p *repeatingToolProvider) CreateMessage(_ context.Context, request agent.Request) (agent.Response, error) {
	p.calls++
	input := json.RawMessage(`{"handle":"missing","section":"missing"}`)
	block, _ := json.Marshal(agent.ContentBlock{Type: "tool_use", ID: fmt.Sprintf("loop-call-%d", p.calls), Name: "read_selected_source", Input: input})
	return agent.Response{ID: fmt.Sprintf("loop-response-%d", p.calls), Type: "message", Role: "assistant", Model: request.Model, Content: []json.RawMessage{block}, StopReason: "tool_use"}, nil
}

func TestCoordinatorRecovery(t *testing.T) {
	repo, ctx, binding := testRepository(t)
	customerID, accountID, transactionID, cardID := "cust-g-replay", "raw-g-replay", "txn-g-replay", "card-g-replay"
	detectionID := "detection-g-replay"
	caseID, _ := domain.StableCaseID(binding, domain.CaseShortfall, detectionID)
	if _, err := repo.CreateCase(ctx, domain.Case{ID: caseID, BindingID: binding, Kind: domain.CaseShortfall, DetectionID: &detectionID, CustomerID: &customerID, AccountRef: ptr(domain.InternalAccountRef(customerID)), RawAccountID: &accountID, Status: domain.CaseNew, NextStage: "EVALUATE"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimNext(ctx, 30*time.Second, binding, "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := underwriting.EvaluationRequestBody(detectionID, 30)
	key := "coordinator:" + caseID + ":r1"
	rowID, err := repo.PrepareAssessment(ctx, caseID, *claimed.LeaseToken, 1, key, transactionID, cardID, body)
	if err != nil {
		t.Fatal(err)
	}
	response := evaluationResponse(detectionID, customerID, accountID, transactionID)
	var mu sync.Mutex
	var calls int
	var receivedKey string
	var receivedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		receivedKey = r.Header.Get("Idempotency-Key")
		receivedBody, _ = ioReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(response)
	}))
	defer upstream.Close()
	client, err := underwriting.New(upstream.URL, strings.Repeat("k", 32), "DEMO", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	linkage := underwriting.Linkage{ShortfallID: detectionID, CustomerID: customerID, AccountID: accountID, TransactionID: transactionID, SourceMode: "DEMO", TermDays: 30}
	if _, _, _, err := client.EvaluateBody(ctx, body, key, linkage); err != nil {
		t.Fatalf("simulate upstream durable save: %v", err)
	}
	executor := &StageExecutor{Repo: repo, Underwriting: client, Mode: "DEMO"}
	evaluation, row, _, err := executor.savedEvaluation(ctx, claimed, *claimed.LeaseToken, linkage)
	if err != nil || evaluation == nil || row.ID != rowID {
		t.Fatalf("recover same upstream assessment: evaluation=%#v err=%v", evaluation, err)
	}
	mu.Lock()
	if calls != 2 || receivedKey != key || string(receivedBody) != string(body) {
		t.Fatalf("replay changed request identity: calls=%d key=%s", calls, receivedKey)
	}
	mu.Unlock()
	t.Logf("case_id=%s evaluation_id=%s underwriting_http=httptest replay_count=%d source_mode=DEMO", caseID, evaluation.ID, calls)

	shortDetection := "lease-expiry-detection"
	shortCaseID, _ := domain.StableCaseID(binding, domain.CaseShortfall, shortDetection)
	if _, err := repo.CreateCase(ctx, domain.Case{ID: shortCaseID, BindingID: binding, Kind: domain.CaseShortfall, DetectionID: &shortDetection, CustomerID: &customerID, AccountRef: ptr(domain.InternalAccountRef(customerID)), RawAccountID: &accountID, Status: domain.CaseNew, NextStage: "EVALUATE"}); err != nil {
		t.Fatal(err)
	}
	shortClaim, err := repo.ClaimNext(ctx, 40*time.Millisecond, binding, "DEMO")
	if err != nil || shortClaim.ID != shortCaseID {
		t.Fatalf("claim expiry case: %#v %v", shortClaim, err)
	}
	time.Sleep(60 * time.Millisecond)
	if err := repo.DisposeCase(ctx, shortCaseID, *shortClaim.LeaseToken, "HELD", "HELD", nil, ptr("expired")); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("expired lease write accepted: %v", err)
	}
	t.Logf("case_id=%s expired_lease_write=rejected", shortCaseID)
	bridgeID := "bridge-g-stale"
	bridgeVersion := int64(1)
	collectionCustomer := "cust-g-collection-stale"
	collectionAccount := domain.InternalAccountRef(collectionCustomer)
	if err := repo.SaveServicingPage(ctx, binding, "DEMO", 0, 1, []postgres.ServicingChange{{Sequence: 1, Mode: "DEMO", ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: bridgeVersion, BridgeID: &bridgeID, BridgeVersion: &bridgeVersion, CustomerID: &collectionCustomer, AccountRef: &collectionAccount, ChangeType: "CREATED", RecordedAt: time.Now().UTC(), Summary: json.RawMessage(`{"status":"created"}`)}}); err != nil {
		t.Fatal(err)
	}
	collectionClaim, err := repo.ClaimNext(ctx, 30*time.Second, binding, "DEMO")
	if err != nil || collectionClaim.Kind != domain.CaseCollection {
		t.Fatalf("claim collection case: %#v %v", collectionClaim, err)
	}
	state := json.RawMessage(`{"bridge":{"id":"bridge-g-stale","version":1},"allowed_next_actions":[]}`)
	if err := repo.CompleteRefreshDisposition(ctx, collectionClaim.ID, *collectionClaim.LeaseToken, 1, bridgeID, time.Now().UTC(), state, postgres.RefreshDisposition{Status: "MONITORING", NextStage: "MONITORING"}); err != nil {
		t.Fatal(err)
	}
	version2 := int64(2)
	if err := repo.SaveServicingPage(ctx, binding, "DEMO", 1, 2, []postgres.ServicingChange{{Sequence: 2, Mode: "DEMO", ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: version2, BridgeID: &bridgeID, BridgeVersion: &version2, CustomerID: &collectionCustomer, AccountRef: &collectionAccount, ChangeType: "UPDATED", RecordedAt: time.Now().UTC(), Summary: json.RawMessage(`{"status":"updated"}`)}}); err != nil {
		t.Fatal(err)
	}
	collectionClaim, err = repo.ClaimNext(ctx, 30*time.Second, binding, "DEMO")
	if err != nil || collectionClaim.Kind != domain.CaseCollection || collectionClaim.LeaseStage == nil || *collectionClaim.LeaseStage != "REFRESH_BRIDGE" {
		t.Fatalf("claim newer dirty collection version: %#v %v", collectionClaim, err)
	}
	collectionRunID, err := domain.StableRunID(collectionClaim.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	collectionRefs := json.RawMessage(`{"case":"stale-version"}`)
	collectionPrivate := json.RawMessage(`{"scope":"stale-version"}`)
	if _, err := repo.StartRun(ctx, collectionClaim.ID, *collectionClaim.LeaseToken, collectionRunID, 1, "INVESTIGATE", "fixture-prompt", "fixture-v1", collectionRefs, collectionPrivate); err != nil {
		t.Fatal(err)
	}
	version1 := int64(1)
	if err := repo.CompleteCollectionRun(ctx, collectionClaim.ID, *collectionClaim.LeaseToken, collectionRunID, bridgeID, version1, []string{}, json.RawMessage(`{"kind":"FINISH"}`)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("older collection base version published: %v", err)
	}
	t.Logf("case_id=%s bridge_id=%s base_version=%d current_version=%d stale_publication=rejected", collectionClaim.ID, bridgeID, version1, version2)
}

func evaluationResponse(shortfallID, customerID, accountID, transactionID string) []byte {
	evaluated := time.Now().UTC().Truncate(time.Second)
	rules := make([]underwriting.Rule, 10)
	reasons := []string{"HUMAN_APPROVAL_REQUIRED"}
	for i := range rules {
		id := fmt.Sprintf("RULE_%02d", i+1)
		status, suffix := "PASS", "_PASSED"
		if i == 0 {
			status, suffix = "UNKNOWN", "_UNKNOWN"
			reasons = append(reasons, id+suffix)
		}
		rules[i] = underwriting.Rule{ID: id, Status: status, ReasonCode: id + suffix, Actual: json.RawMessage(`{}`), Threshold: json.RawMessage(`{}`), SourceRefs: []customercontext.SourceRef{}}
	}
	evaluation := underwriting.Evaluation{ID: "eval-g-replay", ShortfallID: shortfallID, CustomerID: customerID, AccountID: accountID, TransactionID: transactionID, SourceMode: "DEMO", EvaluatedAt: evaluated, CreatedAt: evaluated, PolicyID: "policy-g", PolicyHash: strings.Repeat("a", 64), Status: "REVIEW_REQUIRED", ApprovalRequired: true, FundingAuthorized: false, RequiresRecheck: true, TermDays: 30, Rules: rules, ReasonCodes: reasons}
	body, _ := json.Marshal(evaluation)
	return body
}

func ptr(value string) *string                   { return &value }
func stringPointer(value string) *string         { return &value }
func ioReadAll(reader io.Reader) ([]byte, error) { return io.ReadAll(reader) }
