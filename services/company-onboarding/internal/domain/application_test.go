package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

func helperValidConsent() domain.CustomerAgreements {
	return domain.CustomerAgreements{
		AgreedToTermsAndConditions: true,
		AgreedToDataUsage:          true,
		TermsAndConditions: &domain.TermsAndConditionsAgreement{
			AgreedAt:             time.Now().UTC(),
			ServiceAgreementType: "FULL",
			DeviceData: &domain.DeviceData{
				IPAddress: "192.168.1.1",
				UserAgent: "Mozilla/5.0 TestBrowser",
			},
		},
	}
}

func helperValidCompany(t *testing.T, id string) *domain.Company {
	t.Helper()
	comp, err := domain.NewDraftCompany(id, "Test Corp")
	if err != nil {
		t.Fatalf("failed to create test company: %v", err)
	}
	return comp
}

func helperValidDocument(id string) domain.Document {
	return domain.Document{
		ID:          id,
		Type:        "CERTIFICATE_OF_INCORPORATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024 * 100, // 100 KiB
		StorageKey:  "storage/keys/doc_001.pdf",
		UploadedBy:  "compliance_user",
		UploadedAt:  time.Now().UTC(),
	}
}

func helperReconstituteAppForMatrix(
	t *testing.T,
	appStatus domain.ApplicationStatus,
	subStatus domain.SubmissionStatus,
	definitiveFailed bool,
) *domain.OnboardingApplication {
	t.Helper()
	comp := helperValidCompany(t, "comp_matrix")
	baseTime := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	doc := helperValidDocument("doc_m1")

	var closedAt *time.Time
	var decision *domain.PlatformDecision
	switch appStatus {
	case domain.ApplicationStatusCompleted:
		tClosed := baseTime
		closedAt = &tClosed
		d := domain.PlatformDecisionApproved
		decision = &d
	case domain.ApplicationStatusRejected:
		tClosed := baseTime
		closedAt = &tClosed
		d := domain.PlatformDecisionRejected
		decision = &d
	case domain.ApplicationStatusCancelled:
		tClosed := baseTime
		closedAt = &tClosed
	}

	var acceptedAt *time.Time
	if subStatus == domain.SubmissionStatusAccepted {
		tAcc := baseTime
		acceptedAt = &tAcc
	}

	sub := domain.Submission{
		ID:                    "sub_matrix",
		Type:                  domain.SubmissionTypeInitialApplication,
		Status:                subStatus,
		DefinitiveInvalidData: definitiveFailed,
		CompanyCopy:           comp.DeepCopy(),
		OnboardingDetailsCopy: domain.PlatformOnboardingDetails{
			CustomerAgreements: helperValidConsent(),
		},
		DocumentIDs:     []string{"doc_m1"},
		SubmittingActor: "actor_matrix",
		CreatedAt:       baseTime,
		AcceptedAt:      acceptedAt,
	}

	rfis := make([]domain.InformationRequest, 0)
	if appStatus == domain.ApplicationStatusInformationRequired {
		rfis = append(rfis, domain.InformationRequest{
			ID:     "rfi_matrix",
			Status: domain.InformationRequestStatusOpen,
			Questions: []domain.RFIQuestion{
				{ID: "q_m", Text: "Clarification?"},
			},
			ReceivedAt: baseTime,
		})
	}

	app, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:      "app_matrix",
		Company: comp,
		Platform: domain.ApplicationPlatform{
			Name:        "AIRWALLEX",
			Environment: "sandbox",
			Decision:    decision,
			OnboardingDetails: domain.PlatformOnboardingDetails{
				CustomerAgreements: helperValidConsent(),
			},
		},
		Status:              appStatus,
		Documents:           []domain.Document{doc},
		Submissions:         []domain.Submission{sub},
		InformationRequests: rfis,
		Creator:             "creator_matrix",
		CreatedAt:           baseTime,
		UpdatedAt:           baseTime,
		ClosedAt:            closedAt,
		Revision:            5,
	})
	if err != nil {
		t.Fatalf("ReconstituteOnboardingApplication for matrix failed: %v", err)
	}
	return app
}

type matrixSubState struct {
	status           domain.SubmissionStatus
	definitiveFailed bool
	desc             string
}

type expectedOutcomeType int

const (
	outcomeError expectedOutcomeType = iota
	outcomeDuplicate
	outcomeSuccess
)

type expectedOutcomeSpec struct {
	outcome           expectedOutcomeType
	expectedAppStatus domain.ApplicationStatus // for outcomeSuccess
	expectedSubStatus domain.SubmissionStatus  // for outcomeSuccess
}

// getIndependentExpectedOutcome defines the authoritative expected transition behavior
// independently of the aggregate's returned error.
func getIndependentExpectedOutcome(appSt domain.ApplicationStatus, subSt matrixSubState, cmdName string) expectedOutcomeSpec {
	isTerminal := appSt == domain.ApplicationStatusCompleted ||
		appSt == domain.ApplicationStatusRejected ||
		appSt == domain.ApplicationStatusCancelled

	switch cmdName {
	case "RecordSubmissionAccepted":
		if subSt.status == domain.SubmissionStatusAccepted {
			return expectedOutcomeSpec{outcome: outcomeDuplicate}
		}
		if subSt.status == domain.SubmissionStatusFailed {
			return expectedOutcomeSpec{outcome: outcomeError} // contradiction: cannot accept failed
		}
		if isTerminal || appSt == domain.ApplicationStatusDraft {
			return expectedOutcomeSpec{outcome: outcomeError}
		}
		// Valid transition from PENDING or UNRESOLVED
		expectedApp := appSt
		if appSt == domain.ApplicationStatusSubmissionPending {
			expectedApp = domain.ApplicationStatusUnderReview
		}
		return expectedOutcomeSpec{
			outcome:           outcomeSuccess,
			expectedAppStatus: expectedApp,
			expectedSubStatus: domain.SubmissionStatusAccepted,
		}

	case "RecordSubmissionFailed_Transport":
		if subSt.status == domain.SubmissionStatusFailed {
			if !subSt.definitiveFailed {
				return expectedOutcomeSpec{outcome: outcomeDuplicate} // duplicate exact failure
			}
			return expectedOutcomeSpec{outcome: outcomeError} // contradictory definitive flag
		}
		if subSt.status == domain.SubmissionStatusAccepted {
			return expectedOutcomeSpec{outcome: outcomeError} // contradiction: cannot fail accepted
		}
		if isTerminal || appSt == domain.ApplicationStatusDraft {
			return expectedOutcomeSpec{outcome: outcomeError}
		}
		// Valid transition: transport failure leaves app status as-is
		return expectedOutcomeSpec{
			outcome:           outcomeSuccess,
			expectedAppStatus: appSt,
			expectedSubStatus: domain.SubmissionStatusFailed,
		}

	case "RecordSubmissionFailed_Definitive":
		if subSt.status == domain.SubmissionStatusFailed {
			if subSt.definitiveFailed {
				return expectedOutcomeSpec{outcome: outcomeDuplicate} // duplicate exact failure
			}
			return expectedOutcomeSpec{outcome: outcomeError} // contradictory definitive flag
		}
		if subSt.status == domain.SubmissionStatusAccepted {
			return expectedOutcomeSpec{outcome: outcomeError} // contradiction: cannot fail accepted
		}
		if isTerminal || appSt == domain.ApplicationStatusDraft {
			return expectedOutcomeSpec{outcome: outcomeError}
		}
		// Valid transition: definitive invalid data rejects application with null decision
		return expectedOutcomeSpec{
			outcome:           outcomeSuccess,
			expectedAppStatus: domain.ApplicationStatusRejected,
			expectedSubStatus: domain.SubmissionStatusFailed,
		}

	case "RecordSubmissionUnresolved":
		if subSt.status == domain.SubmissionStatusUnresolved {
			return expectedOutcomeSpec{outcome: outcomeDuplicate}
		}
		if subSt.status == domain.SubmissionStatusAccepted || subSt.status == domain.SubmissionStatusFailed {
			return expectedOutcomeSpec{outcome: outcomeError} // contradiction: terminal submission states cannot unresolve
		}
		if isTerminal || appSt == domain.ApplicationStatusDraft {
			return expectedOutcomeSpec{outcome: outcomeError}
		}
		// Valid transition from PENDING to UNRESOLVED
		return expectedOutcomeSpec{
			outcome:           outcomeSuccess,
			expectedAppStatus: appSt,
			expectedSubStatus: domain.SubmissionStatusUnresolved,
		}
	}

	panic("unrecognized command: " + cmdName)
}

// TestExhaustiveTransitionMatrix runs an exhaustive cross-product test of:
// All 7 Application statuses x All 5 Submission statuses x 4 Submission Commands = 140 test cases.
// It independently specifies expected outcomes BEFORE execution and asserts:
// 1. ExpectedError: error returned, complete JSON/revision/timestamps 100% unchanged.
// 2. ExpectedDuplicate: nil returned, complete JSON/revision/timestamps 100% unchanged.
// 3. ExpectedSuccess: nil returned, revision advances, updatedAt advances, exact statuses match expectations.
func TestExhaustiveTransitionMatrix(t *testing.T) {
	t.Parallel()

	appStatuses := []domain.ApplicationStatus{
		domain.ApplicationStatusDraft,
		domain.ApplicationStatusSubmissionPending,
		domain.ApplicationStatusUnderReview,
		domain.ApplicationStatusInformationRequired,
		domain.ApplicationStatusCompleted,
		domain.ApplicationStatusRejected,
		domain.ApplicationStatusCancelled,
	}

	subStates := []matrixSubState{
		{status: domain.SubmissionStatusPending, definitiveFailed: false, desc: "PENDING"},
		{status: domain.SubmissionStatusAccepted, definitiveFailed: false, desc: "ACCEPTED"},
		{status: domain.SubmissionStatusFailed, definitiveFailed: false, desc: "FAILED_TRANSPORT"},
		{status: domain.SubmissionStatusFailed, definitiveFailed: true, desc: "FAILED_DEFINITIVE"},
		{status: domain.SubmissionStatusUnresolved, definitiveFailed: false, desc: "UNRESOLVED"},
	}

	type commandSpec struct {
		name string
		run  func(a *domain.OnboardingApplication) error
	}

	commands := []commandSpec{
		{
			name: "RecordSubmissionAccepted",
			run: func(a *domain.OnboardingApplication) error {
				return a.RecordSubmissionAccepted("sub_matrix")
			},
		},
		{
			name: "RecordSubmissionFailed_Transport",
			run: func(a *domain.OnboardingApplication) error {
				return a.RecordSubmissionFailed("sub_matrix", false)
			},
		},
		{
			name: "RecordSubmissionFailed_Definitive",
			run: func(a *domain.OnboardingApplication) error {
				return a.RecordSubmissionFailed("sub_matrix", true)
			},
		},
		{
			name: "RecordSubmissionUnresolved",
			run: func(a *domain.OnboardingApplication) error {
				return a.RecordSubmissionUnresolved("sub_matrix")
			},
		},
	}

	for _, appSt := range appStatuses {
		for _, subSt := range subStates {
			for _, cmd := range commands {
				testName := fmt.Sprintf("%s_x_%s_x_%s", appSt, subSt.desc, cmd.name)
				t.Run(testName, func(t *testing.T) {
					// 1. Determine independent expected outcome BEFORE execution
					expected := getIndependentExpectedOutcome(appSt, subSt, cmd.name)

					app := helperReconstituteAppForMatrix(t, appSt, subSt.status, subSt.definitiveFailed)

					bytesBefore, err := json.Marshal(app)
					if err != nil {
						t.Fatalf("json.Marshal before failed: %v", err)
					}
					revBefore := app.Revision()
					updatedBefore := app.UpdatedAt()

					// 2. Execute command
					runErr := cmd.run(app)

					bytesAfter, err := json.Marshal(app)
					if err != nil {
						t.Fatalf("json.Marshal after failed: %v", err)
					}

					// 3. Verify against independent expectation
					switch expected.outcome {
					case outcomeError:
						if runErr == nil {
							t.Fatalf("expected error for %s, but command succeeded (nil)", testName)
						}
						// Full serialized state, revision, and timestamps MUST BE 100% untouched
						if string(bytesAfter) != string(bytesBefore) {
							t.Fatalf("state mutated on error: before=\n%s\nafter=\n%s", string(bytesBefore), string(bytesAfter))
						}
						if app.Revision() != revBefore {
							t.Fatalf("revision changed on error: %d -> %d", revBefore, app.Revision())
						}
						if !app.UpdatedAt().Equal(updatedBefore) {
							t.Fatalf("updatedAt changed on error: %v -> %v", updatedBefore, app.UpdatedAt())
						}

					case outcomeDuplicate:
						if runErr != nil {
							t.Fatalf("expected duplicate nil for %s, but got error: %v", testName, runErr)
						}
						// Full serialized state, revision, and timestamps MUST BE 100% untouched
						if string(bytesAfter) != string(bytesBefore) {
							t.Fatalf("state mutated on duplicate: before=\n%s\nafter=\n%s", string(bytesBefore), string(bytesAfter))
						}
						if app.Revision() != revBefore {
							t.Fatalf("revision changed on duplicate: %d -> %d", revBefore, app.Revision())
						}
						if !app.UpdatedAt().Equal(updatedBefore) {
							t.Fatalf("updatedAt changed on duplicate: %v -> %v", updatedBefore, app.UpdatedAt())
						}

					case outcomeSuccess:
						if runErr != nil {
							t.Fatalf("expected success for %s, but got error: %v", testName, runErr)
						}
						// Revision and updatedAt must advance
						if app.Revision() <= revBefore {
							t.Fatalf("revision failed to advance on valid transition: %d <= %d", app.Revision(), revBefore)
						}
						if !app.UpdatedAt().After(updatedBefore) {
							t.Fatalf("updatedAt failed to advance on valid transition")
						}
						// Resulting statuses must match independent expectations exactly
						if app.Status() != expected.expectedAppStatus {
							t.Fatalf("app status = %q, want %q", app.Status(), expected.expectedAppStatus)
						}
						subs := app.Submissions()
						if len(subs) != 1 || subs[0].Status != expected.expectedSubStatus {
							t.Fatalf("submission status = %q, want %q", subs[0].Status, expected.expectedSubStatus)
						}
						if cmd.name == "RecordSubmissionFailed_Definitive" {
							if app.Platform().Decision != nil {
								t.Fatalf("decision = %v, want nil on definitive invalid data refusal", *app.Platform().Decision)
							}
							if !subs[0].DefinitiveInvalidData {
								t.Fatalf("DefinitiveInvalidData = false, want true")
							}
						}
					}
				})
			}
		}
	}
}

// TestVerificationRejectionAcrossAllAppStatuses independently specifies expected outcomes
// across all 7 Application statuses and asserts exact status outcomes and immutability.
func TestVerificationRejectionAcrossAllAppStatuses(t *testing.T) {
	t.Parallel()

	appStatuses := []domain.ApplicationStatus{
		domain.ApplicationStatusDraft,
		domain.ApplicationStatusSubmissionPending,
		domain.ApplicationStatusUnderReview,
		domain.ApplicationStatusInformationRequired,
		domain.ApplicationStatusCompleted,
		domain.ApplicationStatusRejected,
		domain.ApplicationStatusCancelled,
	}

	for _, st := range appStatuses {
		t.Run(string(st), func(t *testing.T) {
			app := helperReconstituteAppForMatrix(t, st, domain.SubmissionStatusAccepted, false)

			bytesBefore, err := json.Marshal(app)
			if err != nil {
				t.Fatalf("json.Marshal before failed: %v", err)
			}
			revBefore := app.Revision()
			updatedBefore := app.UpdatedAt()

			runErr := app.RecordPlatformVerificationRejection("reviewer_1")

			bytesAfter, err := json.Marshal(app)
			if err != nil {
				t.Fatalf("json.Marshal after failed: %v", err)
			}

			switch st {
			case domain.ApplicationStatusDraft:
				// Must reject DRAFT applications!
				if runErr == nil {
					t.Fatalf("RecordPlatformVerificationRejection succeeded on DRAFT, want error")
				}
				if string(bytesAfter) != string(bytesBefore) {
					t.Fatalf("state mutated on DRAFT verification rejection error")
				}
				if app.Revision() != revBefore {
					t.Fatalf("revision changed on DRAFT verification rejection error")
				}
				if !app.UpdatedAt().Equal(updatedBefore) {
					t.Fatalf("updatedAt changed on DRAFT verification rejection error")
				}

			case domain.ApplicationStatusCompleted, domain.ApplicationStatusCancelled:
				// Must reject terminal applications with ErrTerminalStatusImmutable!
				if runErr == nil || !errors.Is(runErr, domain.ErrTerminalStatusImmutable) {
					t.Fatalf("error = %v, want %v", runErr, domain.ErrTerminalStatusImmutable)
				}
				if string(bytesAfter) != string(bytesBefore) {
					t.Fatalf("state mutated on terminal verification rejection error")
				}
				if app.Revision() != revBefore {
					t.Fatalf("revision changed on terminal verification rejection error")
				}
				if !app.UpdatedAt().Equal(updatedBefore) {
					t.Fatalf("updatedAt changed on terminal verification rejection error")
				}

			case domain.ApplicationStatusRejected:
				// Duplicate rejection on already rejected application: idempotent no-op, zero touch
				if runErr != nil {
					t.Fatalf("duplicate verification rejection error = %v, want nil", runErr)
				}
				if string(bytesAfter) != string(bytesBefore) {
					t.Fatalf("state mutated on duplicate verification rejection")
				}
				if app.Revision() != revBefore {
					t.Fatalf("revision changed on duplicate verification rejection")
				}
				if !app.UpdatedAt().Equal(updatedBefore) {
					t.Fatalf("updatedAt changed on duplicate verification rejection")
				}

			case domain.ApplicationStatusSubmissionPending, domain.ApplicationStatusUnderReview, domain.ApplicationStatusInformationRequired:
				// Legal post-submission transitions to REJECTED with Decision = REJECTED
				if runErr != nil {
					t.Fatalf("valid verification rejection failed: %v", runErr)
				}
				if app.Status() != domain.ApplicationStatusRejected {
					t.Fatalf("status = %q, want REJECTED", app.Status())
				}
				if app.Platform().Decision == nil || *app.Platform().Decision != domain.PlatformDecisionRejected {
					t.Fatalf("decision = %v, want REJECTED", app.Platform().Decision)
				}
				if app.Revision() <= revBefore {
					t.Fatalf("revision failed to advance")
				}
				if !app.UpdatedAt().After(updatedBefore) {
					t.Fatalf("updatedAt failed to advance")
				}
			}
		})
	}
}

func TestApplicationSuccessLifecycle(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_success_1")
	app, err := domain.NewDraftApplication("app_success_1", comp, "creator_user")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}

	if app.Status() != domain.ApplicationStatusDraft {
		t.Errorf("Status() = %q, want DRAFT", app.Status())
	}

	// 1. Setup valid onboarding details and attach document
	details := domain.PlatformOnboardingDetails{
		IndustryCategoryCode: "FINANCIAL_SERVICES",
		IntendedProducts:     []string{"PAYMENTS"},
		CustomerAgreements:   helperValidConsent(),
	}
	if err := app.UpdateOnboardingDetails(details); err != nil {
		t.Fatalf("UpdateOnboardingDetails() failed: %v", err)
	}
	doc := helperValidDocument("doc_1")
	if err := app.AttachDocument(doc); err != nil {
		t.Fatalf("AttachDocument() failed: %v", err)
	}

	// 2. Submit initial application (freezes Company and details snapshot)
	sub, err := app.InitiateInitialSubmission("sub_1", "actor_1", []string{"doc_1"})
	if err != nil {
		t.Fatalf("InitiateInitialSubmission() failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusSubmissionPending {
		t.Errorf("Status() = %q, want SUBMISSION_PENDING", app.Status())
	}
	if sub.Status != domain.SubmissionStatusPending {
		t.Errorf("sub.Status = %q, want PENDING", sub.Status)
	}

	// Invariant check: Mutating live company externally does not alter frozen company in application
	_ = comp.UpdateTradingName("Mutated Outside")
	if app.Company().TradingName() == "Mutated Outside" {
		t.Errorf("app.Company() was mutated by external company edit after submission freeze")
	}
	if sub.CompanyCopy.TradingName() == "Mutated Outside" {
		t.Errorf("sub.CompanyCopy was mutated by external company edit")
	}

	// 3. Platform accepts submission for processing -> moves to UNDER_REVIEW
	if err := app.RecordSubmissionAccepted("sub_1"); err != nil {
		t.Fatalf("RecordSubmissionAccepted() failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("Status() = %q, want UNDER_REVIEW", app.Status())
	}

	// 4. Confirmed PlatformAccount activation completes application
	pa, err := domain.NewPlatformAccount("pa_1", "comp_success_1", "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("NewPlatformAccount() failed: %v", err)
	}
	if err := pa.MarkActive(time.Now().UTC()); err != nil {
		t.Fatalf("pa.MarkActive() failed: %v", err)
	}

	if err := app.RecordPlatformAccountActivation(pa); err != nil {
		t.Fatalf("RecordPlatformAccountActivation() failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("Status() = %q, want COMPLETED", app.Status())
	}
	if app.Platform().Decision == nil || *app.Platform().Decision != domain.PlatformDecisionApproved {
		t.Errorf("Decision = %v, want APPROVED", app.Platform().Decision)
	}
	if app.ClosedAt() == nil {
		t.Errorf("ClosedAt() is nil, want non-nil on completion")
	}
}

func TestInvalidDataRefusalVsVerificationRejection(t *testing.T) {
	t.Parallel()

	// Scenario A: Definitive invalid-data platform refusal
	// Produces FAILED Submission, closes application REJECTED, platform decision remains NULL.
	compA := helperValidCompany(t, "comp_refusal")
	appA, err := domain.NewDraftApplication("app_refusal", compA, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = appA.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = appA.AttachDocument(helperValidDocument("doc_a"))
	_, _ = appA.InitiateInitialSubmission("sub_a", "actor", []string{"doc_a"})

	if err := appA.RecordSubmissionFailed("sub_a", true); err != nil {
		t.Fatalf("RecordSubmissionFailed() failed: %v", err)
	}
	if appA.Status() != domain.ApplicationStatusRejected {
		t.Errorf("appA Status() = %q, want REJECTED", appA.Status())
	}
	if appA.Platform().Decision != nil {
		t.Errorf("appA Decision = %v, want nil (null) on invalid data refusal", *appA.Platform().Decision)
	}
	subsA := appA.Submissions()
	if subsA[0].Status != domain.SubmissionStatusFailed {
		t.Errorf("subsA[0].Status = %q, want FAILED", subsA[0].Status)
	}

	// Scenario B: Verification rejection
	// Original submission remains ACCEPTED; application closed REJECTED with decision REJECTED.
	compB := helperValidCompany(t, "comp_verif_rej")
	appB, err := domain.NewDraftApplication("app_verif_rej", compB, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = appB.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = appB.AttachDocument(helperValidDocument("doc_b"))
	_, _ = appB.InitiateInitialSubmission("sub_b", "actor", []string{"doc_b"})
	_ = appB.RecordSubmissionAccepted("sub_b")

	if err := appB.RecordPlatformVerificationRejection("compliance_lead"); err != nil {
		t.Fatalf("RecordPlatformVerificationRejection() failed: %v", err)
	}
	if appB.Status() != domain.ApplicationStatusRejected {
		t.Errorf("appB Status() = %q, want REJECTED", appB.Status())
	}
	if appB.Platform().Decision == nil || *appB.Platform().Decision != domain.PlatformDecisionRejected {
		t.Errorf("appB Decision = %v, want REJECTED", appB.Platform().Decision)
	}
	subsB := appB.Submissions()
	if subsB[0].Status != domain.SubmissionStatusAccepted {
		t.Errorf("subsB[0].Status = %q, want ACCEPTED", subsB[0].Status)
	}
}

func TestSubmissionUncertaintyPreservesState(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_uncertain")
	app, err := domain.NewDraftApplication("app_uncertain", comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_u"))
	_, _ = app.InitiateInitialSubmission("sub_u", "actor", []string{"doc_u"})

	// Network timeout / transport uncertainty during initial submission
	if err := app.RecordSubmissionUnresolved("sub_u"); err != nil {
		t.Fatalf("RecordSubmissionUnresolved() failed: %v", err)
	}
	// Initial application must remain SUBMISSION_PENDING (neither rejected nor advanced)
	if app.Status() != domain.ApplicationStatusSubmissionPending {
		t.Errorf("Status() = %q, want SUBMISSION_PENDING", app.Status())
	}
	if app.Submissions()[0].Status != domain.SubmissionStatusUnresolved {
		t.Errorf("submission status = %q, want UNRESOLVED", app.Submissions()[0].Status)
	}

	// Invariant: Initial accepted cannot later become unresolved via stale observation
	_ = app.RecordSubmissionAccepted("sub_u")
	err = app.RecordSubmissionUnresolved("sub_u")
	if err == nil || !errors.Is(err, domain.ErrInvalidSubmissionStatus) {
		t.Errorf("RecordSubmissionUnresolved on accepted submission error = %v, want %v", err, domain.ErrInvalidSubmissionStatus)
	}
}

func TestCancellationPermittedOnlyBeforeSubmission(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_cancel")
	app, err := domain.NewDraftApplication("app_cancel", comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}

	// 1. Cancellation in DRAFT succeeds
	if err := app.Cancel("operator_1"); err != nil {
		t.Fatalf("Cancel() in DRAFT failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCancelled {
		t.Errorf("Status() = %q, want CANCELLED", app.Status())
	}

	// 2. Cancellation after submission is strictly rejected
	app2, err := domain.NewDraftApplication("app_cancel_2", comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = app2.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app2.AttachDocument(helperValidDocument("doc_c2"))
	_, _ = app2.InitiateInitialSubmission("sub_c2", "actor", []string{"doc_c2"})

	err = app2.Cancel("operator_1")
	if err == nil || !errors.Is(err, domain.ErrCancellationNotPermitted) {
		t.Errorf("Cancel() after submission error = %v, want %v", err, domain.ErrCancellationNotPermitted)
	}
}

func TestParallelRFIsAndSeparateResponseFreeze(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_rfi")
	app, err := domain.NewDraftApplication("app_rfi", comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_init"))
	initSub, _ := app.InitiateInitialSubmission("sub_init", "actor", []string{"doc_init"})
	_ = app.RecordSubmissionAccepted("sub_init")

	// 1. Receive two distinct RFIs in parallel
	rfi1 := domain.InformationRequest{
		ID:                "rfi_1",
		PlatformReference: "req_airwallex_1",
		Questions: []domain.RFIQuestion{
			{ID: "q1", Text: "Please clarify source of funds"},
		},
	}
	rfi2 := domain.InformationRequest{
		ID:                "rfi_2",
		PlatformReference: "req_airwallex_2",
		Questions: []domain.RFIQuestion{
			{ID: "q2", Text: "Please provide updated operating address"},
		},
	}

	if err := app.ReceiveInformationRequest(rfi1); err != nil {
		t.Fatalf("ReceiveInformationRequest(rfi1) failed: %v", err)
	}
	if err := app.ReceiveInformationRequest(rfi2); err != nil {
		t.Fatalf("ReceiveInformationRequest(rfi2) failed: %v", err)
	}

	if app.Status() != domain.ApplicationStatusInformationRequired {
		t.Errorf("Status() = %q, want INFORMATION_REQUIRED", app.Status())
	}
	if len(app.InformationRequests()) != 2 {
		t.Fatalf("len(InformationRequests()) = %d, want 2", len(app.InformationRequests()))
	}

	// 2. Respond to RFI 1 with amended Company
	amendedCompany := helperValidCompany(t, "comp_rfi")
	_ = amendedCompany.UpdateTradingName("Amended Name LLC")

	answers1 := map[string]string{
		"q1": "Funds originate from venture equity investment",
	}
	subRFI1, err := app.RespondToInformationRequest("sub_rfi_1", "rfi_1", "actor", amendedCompany, answers1, nil)
	if err != nil {
		t.Fatalf("RespondToInformationRequest(rfi_1) failed: %v", err)
	}
	if subRFI1.Type != domain.SubmissionTypeInformationResponse {
		t.Errorf("subRFI1.Type = %q, want INFORMATION_RESPONSE", subRFI1.Type)
	}
	// Invariant: Response submission freezes amended company snapshot
	if subRFI1.CompanyCopy.TradingName() != "Amended Name LLC" {
		t.Errorf("subRFI1.CompanyCopy.TradingName() = %q, want Amended Name LLC", subRFI1.CompanyCopy.TradingName())
	}
	// Invariant: Initial submission snapshot remains unchanged
	if initSub.CompanyCopy.TradingName() == "Amended Name LLC" {
		t.Errorf("initSub.CompanyCopy was mutated by RFI response")
	}
	if len(initSub.Answers) != 0 {
		t.Errorf("initSub.Answers was mutated by RFI response")
	}

	// Invariant: Cannot submit a new response while subRFI1 is in flight (PENDING)
	answers2 := map[string]string{
		"q2": "100 New St, San Francisco, CA",
	}
	_, err = app.RespondToInformationRequest("sub_rfi_2", "rfi_2", "actor", nil, answers2, nil)
	if err == nil || !errors.Is(err, domain.ErrSubmissionInFlight) {
		t.Errorf("RespondToInformationRequest while in flight error = %v, want %v", err, domain.ErrSubmissionInFlight)
	}

	// 3. Platform accepts response for RFI 1
	if err := app.RecordSubmissionAccepted("sub_rfi_1"); err != nil {
		t.Fatalf("RecordSubmissionAccepted(sub_rfi_1) failed: %v", err)
	}
	// Application MUST REMAIN in INFORMATION_REQUIRED because rfi_2 is still open!
	if app.Status() != domain.ApplicationStatusInformationRequired {
		t.Errorf("Status() = %q, want INFORMATION_REQUIRED while rfi_2 is pending", app.Status())
	}

	// 4. Respond to and resolve RFI 2
	_, err = app.RespondToInformationRequest("sub_rfi_2", "rfi_2", "actor", nil, answers2, nil)
	if err != nil {
		t.Fatalf("RespondToInformationRequest(rfi_2) failed: %v", err)
	}
	_ = app.RecordSubmissionAccepted("sub_rfi_2")

	// Platform confirms both RFIs resolved -> transitions back to UNDER_REVIEW
	if err := app.ResolveInformationRequest("rfi_1"); err != nil {
		t.Fatalf("ResolveInformationRequest(rfi_1) failed: %v", err)
	}
	if err := app.ResolveInformationRequest("rfi_2"); err != nil {
		t.Fatalf("ResolveInformationRequest(rfi_2) failed: %v", err)
	}

	if app.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("Status() = %q, want UNDER_REVIEW after all RFIs resolved", app.Status())
	}
}

func TestTerminalStatusImmutabilityGuards(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_term")
	app, _ := domain.NewDraftApplication("app_term", comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_t"))
	_, _ = app.InitiateInitialSubmission("sub_t", "actor", []string{"doc_t"})
	_ = app.RecordSubmissionAccepted("sub_t")

	pa, _ := domain.NewPlatformAccount("pa_term", "comp_term", "AIRWALLEX", "sandbox")
	_ = pa.MarkActive(time.Now().UTC())
	if err := app.RecordPlatformAccountActivation(pa); err != nil {
		t.Fatalf("RecordPlatformAccountActivation failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("app status = %q, want COMPLETED", app.Status())
	}

	// Invariant: Late RFI acceptance, late RFI arrival, or failed submissions cannot regress COMPLETED
	err := app.ReceiveInformationRequest(domain.InformationRequest{
		ID:        "rfi_late",
		Questions: []domain.RFIQuestion{{ID: "q_late", Text: "Question?"}},
	})
	if err == nil || !errors.Is(err, domain.ErrTerminalStatusImmutable) {
		t.Errorf("ReceiveInformationRequest on completed error = %v, want %v", err, domain.ErrTerminalStatusImmutable)
	}

	err = app.RecordPlatformVerificationRejection("late_actor")
	if err == nil || !errors.Is(err, domain.ErrTerminalStatusImmutable) {
		t.Errorf("RecordPlatformVerificationRejection on completed error = %v, want %v", err, domain.ErrTerminalStatusImmutable)
	}

	err = app.LinkPlatformAccount("pa_new")
	if err == nil || !errors.Is(err, domain.ErrTerminalStatusImmutable) {
		t.Errorf("LinkPlatformAccount on completed error = %v, want %v", err, domain.ErrTerminalStatusImmutable)
	}

	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("app status regressed to %q", app.Status())
	}
	if *app.Platform().Decision != domain.PlatformDecisionApproved {
		t.Errorf("decision regressed to %v", *app.Platform().Decision)
	}
}

func TestActivationValidationGuards(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_act")
	app, _ := domain.NewDraftApplication("app_act", comp, "creator")

	paActive, _ := domain.NewPlatformAccount("pa_act_1", "comp_act", "AIRWALLEX", "sandbox")
	_ = paActive.MarkActive(time.Now().UTC())

	// 1. Activation on DRAFT app rejected
	err := app.RecordPlatformAccountActivation(paActive)
	if err == nil {
		t.Fatalf("RecordPlatformAccountActivation on DRAFT succeeded, want error")
	}

	// Advance app to UNDER_REVIEW
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_act"))
	_, _ = app.InitiateInitialSubmission("sub_act", "actor", []string{"doc_act"})
	_ = app.RecordSubmissionAccepted("sub_act")

	// 2. Activation with wrong company ID rejected
	paWrongComp, _ := domain.NewPlatformAccount("pa_wrong_comp", "comp_other", "AIRWALLEX", "sandbox")
	_ = paWrongComp.MarkActive(time.Now().UTC())
	err = app.RecordPlatformAccountActivation(paWrongComp)
	if err == nil {
		t.Errorf("RecordPlatformAccountActivation with mismatched company ID succeeded, want error")
	}

	// 3. Activation with non-active account rejected
	paPending, _ := domain.NewPlatformAccount("pa_pending", "comp_act", "AIRWALLEX", "sandbox")
	err = app.RecordPlatformAccountActivation(paPending)
	if err == nil || !errors.Is(err, domain.ErrPlatformAccountNotActive) {
		t.Errorf("RecordPlatformAccountActivation with non-active account error = %v, want %v", err, domain.ErrPlatformAccountNotActive)
	}

	// 4. Activation with mismatched environment rejected
	paProd, _ := domain.NewPlatformAccount("pa_prod", "comp_act", "AIRWALLEX", "production")
	_ = paProd.MarkActive(time.Now().UTC())
	err = app.RecordPlatformAccountActivation(paProd)
	if err == nil {
		t.Errorf("RecordPlatformAccountActivation with mismatched environment succeeded, want error")
	}

	// 5. Activation with mismatched linked platform account ID rejected
	_ = app.LinkPlatformAccount("pa_linked_id")
	err = app.RecordPlatformAccountActivation(paActive) // ID is pa_act_1 != pa_linked_id
	if err == nil {
		t.Errorf("RecordPlatformAccountActivation with mismatched linked ID succeeded, want error")
	}
}

func TestLinkPlatformAccountGuards(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_link")
	app, _ := domain.NewDraftApplication("app_link", comp, "creator")

	// Empty ID rejected
	err := app.LinkPlatformAccount("  ")
	if err == nil || !errors.Is(err, domain.ErrEmptyPlatformAccountID) {
		t.Errorf("LinkPlatformAccount(empty) error = %v, want %v", err, domain.ErrEmptyPlatformAccountID)
	}

	// First link succeeds
	if err := app.LinkPlatformAccount("pa_100"); err != nil {
		t.Fatalf("LinkPlatformAccount(pa_100) failed: %v", err)
	}

	// Idempotent re-link succeeds without touch
	revBefore := app.Revision()
	if err := app.LinkPlatformAccount("pa_100"); err != nil {
		t.Fatalf("LinkPlatformAccount(pa_100) idempotent failed: %v", err)
	}
	if app.Revision() != revBefore {
		t.Errorf("Revision bumped on idempotent re-link: %d -> %d", revBefore, app.Revision())
	}

	// Changing to different ID rejected
	err = app.LinkPlatformAccount("pa_200")
	if err == nil {
		t.Fatalf("LinkPlatformAccount(pa_200) reassignment succeeded, want error")
	}
}

func TestRFIResponseValidationGuards(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_rfi_val")
	app, _ := domain.NewDraftApplication("app_rfi_val", comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_rv"))
	_, _ = app.InitiateInitialSubmission("sub_rv", "actor", []string{"doc_rv"})
	_ = app.RecordSubmissionAccepted("sub_rv")

	rfi := domain.InformationRequest{
		ID: "rfi_val",
		Questions: []domain.RFIQuestion{
			{ID: "q_required", Text: "Provide details"},
		},
	}
	_ = app.ReceiveInformationRequest(rfi)

	// 1. Missing answer rejected
	_, err := app.RespondToInformationRequest("sub_r1", "rfi_val", "actor", nil, map[string]string{}, nil)
	if err == nil || !errors.Is(err, domain.ErrIncompleteAnswers) {
		t.Errorf("RespondToInformationRequest with missing answer error = %v, want %v", err, domain.ErrIncompleteAnswers)
	}

	// 2. Extraneous answer ID rejected
	answersExtra := map[string]string{
		"q_required":   "Valid answer",
		"q_extraneous": "Invalid extra",
	}
	_, err = app.RespondToInformationRequest("sub_r2", "rfi_val", "actor", nil, answersExtra, nil)
	if err == nil {
		t.Errorf("RespondToInformationRequest with extraneous question ID succeeded, want error")
	}

	// 3. Mismatched amended company rejected
	otherComp := helperValidCompany(t, "comp_wrong")
	answersValid := map[string]string{"q_required": "Valid answer"}
	_, err = app.RespondToInformationRequest("sub_r3", "rfi_val", "actor", otherComp, answersValid, nil)
	if err == nil {
		t.Errorf("RespondToInformationRequest with mismatched company ID succeeded, want error")
	}
}

func TestSuspensionInvariants(t *testing.T) {
	t.Parallel()

	// 1. Later suspension on COMPLETED application NEVER changes completed history
	comp := helperValidCompany(t, "comp_susp")
	app, err := domain.NewDraftApplication("app_susp", comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication() failed: %v", err)
	}
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = app.AttachDocument(helperValidDocument("doc_s"))
	_, _ = app.InitiateInitialSubmission("sub_s", "actor", []string{"doc_s"})
	_ = app.RecordSubmissionAccepted("sub_s")

	pa, _ := domain.NewPlatformAccount("pa_s", "comp_susp", "AIRWALLEX", "sandbox")
	_ = pa.MarkActive(time.Now().UTC())
	_ = app.RecordPlatformAccountActivation(pa)

	revBefore := app.Revision()
	// Trigger suspension on completed application
	if err := app.HandlePlatformSuspension(false); err != nil {
		t.Fatalf("HandlePlatformSuspension() failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("COMPLETED status was rewritten by suspension: %q", app.Status())
	}
	if *app.Platform().Decision != domain.PlatformDecisionApproved {
		t.Errorf("Decision was rewritten by suspension: %v", *app.Platform().Decision)
	}
	if app.Revision() != revBefore {
		t.Errorf("Revision changed on completed application suspension: %d -> %d", revBefore, app.Revision())
	}

	// 2. Mid-review suspension without explicit terminal refusal leaves app open and flags review
	appMid, _ := domain.NewDraftApplication("app_mid", comp, "creator")
	_ = appMid.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = appMid.AttachDocument(helperValidDocument("doc_m"))
	_, _ = appMid.InitiateInitialSubmission("sub_m", "actor", []string{"doc_m"})
	_ = appMid.RecordSubmissionAccepted("sub_m")

	if err := appMid.HandlePlatformSuspension(false); err != nil {
		t.Fatalf("HandlePlatformSuspension(false) failed: %v", err)
	}
	if appMid.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("Status = %q, want UNDER_REVIEW (not rejected)", appMid.Status())
	}
	if !appMid.OperatorReviewFlagged() {
		t.Errorf("OperatorReviewFlagged() = false, want true")
	}

	// 3. Mid-review suspension WITH explicit terminal refusal rejects application
	if err := appMid.HandlePlatformSuspension(true); err != nil {
		t.Fatalf("HandlePlatformSuspension(true) failed: %v", err)
	}
	if appMid.Status() != domain.ApplicationStatusRejected {
		t.Errorf("Status = %q, want REJECTED", appMid.Status())
	}
	if *appMid.Platform().Decision != domain.PlatformDecisionRejected {
		t.Errorf("Decision = %v, want REJECTED", *appMid.Platform().Decision)
	}
}

func TestDocumentPolicyConstraints(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_doc_policy")
	app, _ := domain.NewDraftApplication("app_doc_policy", comp, "creator")

	// Over 10 MiB rejected
	hugeDoc := domain.Document{
		ID:          "doc_huge",
		StorageKey:  "keys/huge.pdf",
		ContentType: "application/pdf",
		SizeBytes:   10*1024*1024 + 1, // 10 MiB + 1 byte
	}
	if err := app.AttachDocument(hugeDoc); err == nil || !errors.Is(err, domain.ErrDocumentExceedsSizeLimit) {
		t.Errorf("AttachDocument() for huge doc error = %v, want %v", err, domain.ErrDocumentExceedsSizeLimit)
	}

	// Invalid MIME type rejected
	badMimeDoc := domain.Document{
		ID:          "doc_zip",
		StorageKey:  "keys/bad.zip",
		ContentType: "application/zip",
		SizeBytes:   1024,
	}
	if err := app.AttachDocument(badMimeDoc); err == nil || !errors.Is(err, domain.ErrInvalidDocumentMimeType) {
		t.Errorf("AttachDocument() for zip error = %v, want %v", err, domain.ErrInvalidDocumentMimeType)
	}

	// Document referencing non-existent person rejected
	ghostPersonDoc := domain.Document{
		ID:          "doc_ghost_p",
		PersonID:    "non_existent_person",
		StorageKey:  "keys/ghost.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
	}
	if err := app.AttachDocument(ghostPersonDoc); err == nil {
		t.Errorf("AttachDocument() referencing non-existent person succeeded, want error")
	}

	// Valid PDF/JPEG/PNG accepted
	for _, mime := range []string{"application/pdf", "image/jpeg", "image/png"} {
		validDoc := domain.Document{
			ID:          "doc_" + strings.ReplaceAll(mime, "/", "_"),
			StorageKey:  "keys/valid",
			ContentType: mime,
			SizeBytes:   1024,
		}
		if err := app.AttachDocument(validDoc); err != nil {
			t.Errorf("AttachDocument() failed for MIME %q: %v", mime, err)
		}
	}
}

func TestApplicationDeepCopyAndAliasingSafety(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_app_alias")
	// Add person to company so PersonPlatformDetails references are valid
	_ = comp.AddPerson(domain.Person{
		ID:          "person_1",
		FirstName:   "Alice",
		LastName:    "Director",
		Role:        "DIRECTOR",
		DateOfBirth: "1980-01-01",
	})

	app, _ := domain.NewDraftApplication("app_alias", comp, "creator")

	roles := []string{"DIRECTOR"}
	personDetails := []domain.PersonPlatformDetails{
		{
			PersonID:      "person_1",
			PlatformRoles: roles,
			ResidentialAddress: domain.Address{
				Line1:   "123 Main St",
				Country: "US",
			},
		},
	}
	details := domain.PlatformOnboardingDetails{
		People:             personDetails,
		CustomerAgreements: helperValidConsent(),
	}
	_ = app.UpdateOnboardingDetails(details)

	// External mutation of person details after UpdateOnboardingDetails must not affect application
	roles[0] = "HACKED_ROLE"
	gotDetails, ok := app.Platform().OnboardingDetails.FindPersonDetails("person_1")
	if !ok || gotDetails.PlatformRoles[0] == "HACKED_ROLE" {
		t.Errorf("PlatformRoles was aliased to external slice")
	}

	// Modifying getter return value must not affect internal state
	gotDetails.PlatformRoles[0] = "MUTATED_FROM_GETTER"
	gotAgain, _ := app.Platform().OnboardingDetails.FindPersonDetails("person_1")
	if gotAgain.PlatformRoles[0] == "MUTATED_FROM_GETTER" {
		t.Errorf("PlatformRoles was mutated via getter return slice")
	}
}

func TestApplicationJSONRoundTrip(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_json_app")
	app, _ := domain.NewDraftApplication("app_json_rt", comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		IndustryCategoryCode: "TECH",
		CustomerAgreements:   helperValidConsent(),
	})
	_ = app.AttachDocument(helperValidDocument("doc_json"))
	_, _ = app.InitiateInitialSubmission("sub_json", "actor", []string{"doc_json"})

	data, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("json.Marshal() failed: %v", err)
	}

	var restored domain.OnboardingApplication
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() failed: %v", err)
	}

	if restored.ID() != app.ID() {
		t.Errorf("restored.ID() = %q, want %q", restored.ID(), app.ID())
	}
	if restored.Status() != app.Status() {
		t.Errorf("restored.Status() = %q, want %q", restored.Status(), app.Status())
	}
	if len(restored.Submissions()) != 1 {
		t.Errorf("len(restored.Submissions()) = %d, want 1", len(restored.Submissions()))
	}

	// Corrupted JSON leaves receiver unmutated
	corrupted := []byte(`{"application_id": "", "status": "INVALID_STATUS"}`)
	err = restored.UnmarshalJSON(corrupted)
	if err == nil {
		t.Fatalf("UnmarshalJSON with corrupted JSON succeeded, want error")
	}
	if restored.ID() != "app_json_rt" {
		t.Errorf("restored.ID() was mutated by failed UnmarshalJSON: %q", restored.ID())
	}
}

func TestReconstituteOnboardingApplication_ForeignCompanyValidation(t *testing.T) {
	t.Parallel()

	comp := helperValidCompany(t, "comp_orig")
	foreignComp := helperValidCompany(t, "comp_foreign")

	baseParams := domain.ReconstituteApplicationParams{
		ID:       "app_recon_test",
		Company:  comp,
		Status:   domain.ApplicationStatusDraft,
		Creator:  "test_actor",
		Revision: 1,
	}

	// 1. Foreign LiveCompany is rejected
	paramsBadLive := baseParams
	paramsBadLive.LiveCompany = foreignComp
	_, err := domain.ReconstituteOnboardingApplication(paramsBadLive)
	if err == nil || !strings.Contains(err.Error(), "live company ID does not match application company ID") {
		t.Fatalf("expected foreign live company rejection, got: %v", err)
	}

	// 2. Foreign Submission.CompanyCopy is rejected
	paramsBadSub := baseParams
	paramsBadSub.Submissions = []domain.Submission{
		{
			ID:          "sub_foreign_comp",
			Status:      domain.SubmissionStatusPending,
			CompanyCopy: foreignComp,
		},
	}
	_, err = domain.ReconstituteOnboardingApplication(paramsBadSub)
	if err == nil || !strings.Contains(err.Error(), "company copy ID does not match application company ID") {
		t.Fatalf("expected foreign submission company copy rejection, got: %v", err)
	}

	// 3. Document referencing person on foreign company cannot validate
	_ = foreignComp.AddPerson(domain.Person{
		ID:          "p_foreign",
		FirstName:   "Foreign",
		LastName:    "Person",
		Role:        "Director",
		DateOfBirth: domain.Date("1980-01-01"),
	})
	foreignDoc := domain.Document{
		ID:          "doc_foreign_ref",
		Type:        "IDENTITY_DOCUMENT",
		PersonID:    "p_foreign",
		FileName:    "foreign.pdf",
		ContentType: "application/pdf",
		SizeBytes:   100,
		StorageKey:  "apps/app_recon_test/docs/doc_foreign_ref",
		UploadedBy:  "test_actor",
		UploadedAt:  time.Now().UTC(),
	}
	paramsForeignDoc := baseParams
	paramsForeignDoc.Documents = []domain.Document{foreignDoc}
	_, err = domain.ReconstituteOnboardingApplication(paramsForeignDoc)
	if err == nil || !strings.Contains(err.Error(), "not found in application company, live company, or submission snapshots") {
		t.Fatalf("expected document referencing foreign person to fail validation, got: %v", err)
	}
}
