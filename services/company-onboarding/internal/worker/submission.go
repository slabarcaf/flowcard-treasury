package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
)

// Dispatch step names for durable intent tracking.
const (
	StepCreateAccount = "CREATE_ACCOUNT"
	StepUpdateAccount = "UPDATE_ACCOUNT"
	StepSubmitAccount = "SUBMIT_ACCOUNT"
	StepRespondRFI    = "RESPOND_RFI"
)

// SubmissionJobPayload represents the outbox message payload for submission processing.
type SubmissionJobPayload struct {
	ApplicationID string `json:"application_id"`
	SubmissionID  string `json:"submission_id,omitempty"`
}

// ProcessSubmissionJob executes the multi-step platform submission sequence:
// 1. Pre-flight fail-closed checks (storage readiness, document existence, payload mapping).
// 2. Resolve or create platform account (fenced by lease token and durable dispatch intent).
// 3. Upload document files from private storage to the platform and record file IDs.
// 4. Branch by submission type:
//   - Initial Application: Update Account -> Submit Account -> Record Acceptance.
//   - Information Response: Retrieve RFI -> Validate KYC/text/answers -> Respond RFI -> Record Acceptance.
func (w *Worker) ProcessSubmissionJob(ctx context.Context, msg *postgres.OutboxMessage) error {
	var payload SubmissionJobPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		// Finding 9: malformed payload - deadletter by completing outbox message
		if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
			return errors.Join(err, compErr)
		}
		return fmt.Errorf("malformed submission job payload: %w", err)
	}

	cleanAppID := strings.TrimSpace(payload.ApplicationID)
	if cleanAppID == "" {
		if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
			return compErr
		}
		return errors.New("submission job payload missing application_id")
	}

	// Load application aggregate
	app, err := w.repo.GetApplication(ctx, cleanAppID)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
				return errors.Join(err, compErr)
			}
			return fmt.Errorf("application %s not found: %w", cleanAppID, err)
		}
		// DB error: release for retry
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return err
	}

	initialRevision := app.Revision()

	// Identify target submission
	var targetSub *domain.Submission
	if payload.SubmissionID != "" {
		for _, s := range app.Submissions() {
			if s.ID == payload.SubmissionID {
				subCopy := s
				targetSub = &subCopy
				break
			}
		}
	} else {
		// Pick most recent pending submission
		subs := app.Submissions()
		for i := len(subs) - 1; i >= 0; i-- {
			if subs[i].Status == domain.SubmissionStatusPending {
				subCopy := subs[i]
				targetSub = &subCopy
				break
			}
		}
	}

	if targetSub == nil {
		// No pending submission exists; complete outbox message
		if err := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); err != nil {
			return err
		}
		return nil
	}

	if targetSub.Status != domain.SubmissionStatusPending {
		// Submission already accepted, failed, or unresolved; complete outbox message
		if err := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); err != nil {
			return err
		}
		return nil
	}

	// -------------------------------------------------------------
	// PRE-FLIGHT CHECKS (Finding 5)
	// Fail closed before any account call if storage is nil, doc missing,
	// unreadable in storage, or payload mapping fails.
	// -------------------------------------------------------------
	if len(targetSub.DocumentIDs) > 0 {
		if w.storage == nil {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return relErr
			}
			return fmt.Errorf("storage client unconfigured: cannot process submission %s with %d documents", targetSub.ID, len(targetSub.DocumentIDs))
		}
		for _, docID := range targetSub.DocumentIDs {
			var foundDoc *domain.Document
			for _, d := range app.Documents() {
				if d.ID == docID {
					dCopy := d
					foundDoc = &dCopy
					break
				}
			}
			if foundDoc == nil {
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("document %s referenced in submission not found on application", docID))
			}

			rc, _, err := w.storage.Get(ctx, foundDoc.StorageKey)
			if err != nil {
				if errors.Is(err, storage.ErrStorageUnconfigured) {
					if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
						return errors.Join(err, relErr)
					}
					return err
				}
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("pre-flight storage read failed for document %s: %v", docID, err))
			}
			_ = rc.Close()
		}
	}

	var createReq *airwallex.AccountCreateRequest
	if targetSub.Type == domain.SubmissionTypeInitialApplication {
		var mapErr error
		createReq, mapErr = airwallex.MapSubmissionToAccountCreateRequest(*targetSub)
		if mapErr != nil {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, "pre-flight create mapping failed: "+mapErr.Error())
		}

		if _, err := airwallex.MapSubmissionToAccountUpdateRequest(*targetSub, app.Documents(), nil); err != nil {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, "pre-flight update mapping failed: "+err.Error())
		}
	} else if targetSub.Type == domain.SubmissionTypeInformationResponse || targetSub.InformationRequestID != "" {
		// Verify referenced RFI exists and has non-empty answers
		var foundRFI bool
		for _, r := range app.InformationRequests() {
			if r.ID == targetSub.InformationRequestID {
				foundRFI = true
				break
			}
		}
		if !foundRFI {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("information request %s not found on application", targetSub.InformationRequestID))
		}
		if len(targetSub.Answers) == 0 {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, "information response submission has no answers")
		}
	}

	// -------------------------------------------------------------
	// STEP 1: Platform Account Creation Coordinator (Findings 1, 2, 7 & Coordinator)
	// Keyed uniquely on (CompanyID, Platform, Environment) to coordinate across distinct jobs.
	// -------------------------------------------------------------
	coordinator, err := w.repo.ClaimPlatformCreationCoordinationWithLease(
		ctx,
		targetSub.CompanyCopy.ID(),
		app.Platform().Name,
		app.Platform().Environment,
		targetSub.ID,
		msg.ID,
		msg.LeaseToken,
	)
	if err != nil {
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return fmt.Errorf("failed to claim platform creation coordination: %w", err)
	}

	var externalAccountID string

	switch coordinator.State {
	case postgres.PlatformCreationStateConfirmed:
		externalAccountID = coordinator.ExternalAccountID
		if app.Platform().PlatformAccountID == "" && externalAccountID != "" {
			_, err := w.repo.SavePlatformAccountWithLease(ctx, postgres.SavePlatformAccountParams{
				ApplicationID:     app.ID(),
				ExternalAccountID: externalAccountID,
				OutboxID:          msg.ID,
				LeaseToken:        msg.LeaseToken,
			})
			if err != nil {
				if errors.Is(err, domain.ErrTerminalStatusImmutable) || errors.Is(err, domain.ErrExternalAccountIDReassignment) {
					if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
						return errors.Join(err, compErr)
					}
					return err
				}
				return err
			}
		}
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepCreateAccount, postgres.DispatchStepStateConfirmed, externalAccountID, msg.ID, msg.LeaseToken); err != nil {
			return err
		}

	case postgres.PlatformCreationStateUnresolved:
		// Reclaimed or second job observing UNRESOLVED holds/reconciles; never creates
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepCreateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.PlatformCreationStateDispatching:
		stepState, _, err := w.repo.GetDispatchStepState(ctx, targetSub.ID, StepCreateAccount)
		if err != nil {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return errors.Join(err, relErr)
			}
			return fmt.Errorf("failed to check dispatch step state for %s: %w", StepCreateAccount, err)
		}

		if !coordinator.IsNewClaim || stepState == postgres.DispatchStepStateDispatching {
			// Reclaimed or second job observing existing DISPATCHING must hold/reconcile, NEVER redispatch CREATE!
			if markErr := w.repo.MarkPlatformCreationCoordinationUnresolvedWithLease(ctx, targetSub.CompanyCopy.ID(), app.Platform().Name, app.Platform().Environment, targetSub.ID, msg.ID, msg.LeaseToken); markErr != nil {
				return markErr
			}
			if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepCreateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
				return recErr
			}
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		// Atomic transition: StartDispatchStepWithLease returns maySend = true ONLY on atomic NOT_STARTED -> DISPATCHING
		maySend, err := w.repo.StartDispatchStepWithLease(ctx, targetSub.ID, StepCreateAccount, msg.ID, msg.LeaseToken)
		if err != nil {
			return err
		}
		if !maySend {
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		createResp, err := w.platform.CreateAccount(ctx, *createReq)
		if err != nil {
			if markErr := w.repo.MarkPlatformCreationCoordinationUnresolvedWithLease(ctx, targetSub.CompanyCopy.ID(), app.Platform().Name, app.Platform().Environment, targetSub.ID, msg.ID, msg.LeaseToken); markErr != nil {
				return markErr
			}
			if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepCreateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
				return recErr
			}

			if errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
				return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
			}
			if errors.Is(err, airwallex.ErrInvalidData) {
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
			}
			if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
				if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
					return errors.Join(err, relErr)
				}
				return err
			}
			return err
		}

		externalAccountID = createResp.ID

		// Durably confirm coordinator so remote external ID is preserved even if local save fails
		if err := w.repo.ConfirmPlatformCreationCoordinationWithLease(
			ctx,
			targetSub.CompanyCopy.ID(),
			app.Platform().Name,
			app.Platform().Environment,
			externalAccountID,
			targetSub.ID,
			msg.ID,
			msg.LeaseToken,
		); err != nil {
			return err
		}

		// Save platform account and link internal aggregate ID to application
		_, err = w.repo.SavePlatformAccountWithLease(ctx, postgres.SavePlatformAccountParams{
			ApplicationID:     app.ID(),
			ExternalAccountID: externalAccountID,
			OutboxID:          msg.ID,
			LeaseToken:        msg.LeaseToken,
		})
		if err != nil {
			if errors.Is(err, domain.ErrTerminalStatusImmutable) || errors.Is(err, domain.ErrExternalAccountIDReassignment) {
				if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
					return errors.Join(err, compErr)
				}
				return err
			}
			return err
		}

		// Record CONFIRMED on step
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepCreateAccount, postgres.DispatchStepStateConfirmed, externalAccountID, msg.ID, msg.LeaseToken); err != nil {
			return err
		}
	}

	// -------------------------------------------------------------
	// STEP 2: File Uploads & Persisting Upload IDs (Finding 3)
	// -------------------------------------------------------------
	existingUploads, err := w.repo.GetDocumentUploads(ctx, targetSub.ID)
	if err != nil {
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return fmt.Errorf("failed to get document uploads: %w", err)
	}
	if existingUploads == nil {
		existingUploads = make(map[string]string)
	}

	if len(targetSub.DocumentIDs) > 0 && w.storage != nil {
		for _, docID := range targetSub.DocumentIDs {
			if existingUploads[docID] != "" {
				// Already uploaded and recorded
				continue
			}

			var doc *domain.Document
			for _, d := range app.Documents() {
				if d.ID == docID {
					dCopy := d
					doc = &dCopy
					break
				}
			}
			if doc == nil {
				continue
			}

			r, _, err := w.storage.Get(ctx, doc.StorageKey)
			if err != nil {
				if errors.Is(err, storage.ErrStorageUnconfigured) {
					if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
						return errors.Join(err, relErr)
					}
					return err
				}
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("storage read failed for doc %s: %v", docID, err))
			}

			uploadResp, err := w.platform.UploadFile(ctx, r, doc.FileName, doc.SizeBytes, doc.ContentType)
			_ = r.Close()
			if err != nil {
				if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
					if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
						return errors.Join(err, relErr)
					}
					return err
				}
				if errors.Is(err, airwallex.ErrInvalidData) {
					return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
				}
				return err
			}

			// Durably record platform_file_id fenced by lease token
			if err := w.repo.RecordDocumentUploadWithLease(ctx, targetSub.ID, doc.ID, uploadResp.FileID, msg.ID, msg.LeaseToken); err != nil {
				return err
			}
			existingUploads[doc.ID] = uploadResp.FileID
		}
	}

	// -------------------------------------------------------------
	// BRANCH: RFI Response vs Initial Application
	// -------------------------------------------------------------
	if targetSub.Type == domain.SubmissionTypeInformationResponse || targetSub.InformationRequestID != "" {
		return w.processRFIResponseJob(ctx, msg, app, targetSub, initialRevision, externalAccountID)
	}

	// -------------------------------------------------------------
	// STEP 3: Update Account (Findings 3, 4) - Initial Application Only
	// Attach files, business details, products, revenue, category, and persons
	// -------------------------------------------------------------
	updateStepState, _, err := w.repo.GetDispatchStepState(ctx, targetSub.ID, StepUpdateAccount)
	if err != nil {
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return fmt.Errorf("failed to check dispatch step state for %s: %w", StepUpdateAccount, err)
	}

	switch updateStepState {
	case postgres.DispatchStepStateDispatching:
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepUpdateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateUnresolved:
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateConfirmed:
		// Already updated

	case postgres.DispatchStepStateNotStarted:
		updateReq, err := airwallex.MapSubmissionToAccountUpdateRequest(*targetSub, app.Documents(), existingUploads)
		if err != nil {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, "failed to map update request: "+err.Error())
		}

		maySend, err := w.repo.StartDispatchStepWithLease(ctx, targetSub.ID, StepUpdateAccount, msg.ID, msg.LeaseToken)
		if err != nil {
			return err
		}
		if !maySend {
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		_, err = w.platform.UpdateAccount(ctx, externalAccountID, *updateReq)
		if err != nil {
			if errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepUpdateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
			}
			if errors.Is(err, airwallex.ErrInvalidData) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepUpdateAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
			}
			if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
				if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
					return errors.Join(err, relErr)
				}
				return err
			}
			return err
		}

		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepUpdateAccount, postgres.DispatchStepStateConfirmed, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
	}

	// -------------------------------------------------------------
	// STEP 4: Submit Account - Initial Application Only (Finding 6)
	// -------------------------------------------------------------
	submitStepState, _, err := w.repo.GetDispatchStepState(ctx, targetSub.ID, StepSubmitAccount)
	if err != nil {
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return fmt.Errorf("failed to check dispatch step state for %s: %w", StepSubmitAccount, err)
	}

	switch submitStepState {
	case postgres.DispatchStepStateDispatching:
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepSubmitAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateUnresolved:
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateConfirmed:
		// Already submitted

	case postgres.DispatchStepStateNotStarted:
		maySend, err := w.repo.StartDispatchStepWithLease(ctx, targetSub.ID, StepSubmitAccount, msg.ID, msg.LeaseToken)
		if err != nil {
			return err
		}
		if !maySend {
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		submitResp, err := w.platform.SubmitAccount(ctx, externalAccountID)
		if err != nil {
			if errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepSubmitAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
			}
			if errors.Is(err, airwallex.ErrInvalidData) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepSubmitAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
			}
			if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
				if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
					return errors.Join(err, relErr)
				}
				return err
			}
			return err
		}

		// Finding 6: verify provider status strictly against SUBMITTED or ACTIVE
		if submitResp.Status != "SUBMITTED" && submitResp.Status != "ACTIVE" {
			if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepSubmitAccount, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
				return recErr
			}
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepSubmitAccount, postgres.DispatchStepStateConfirmed, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
	}

	// -------------------------------------------------------------
	// STEP 5: Record Initial Submission Acceptance (Findings 6, 7 & D Reconstitution)
	// -------------------------------------------------------------
	err = w.repo.RecordWorkerSubmissionOutcomeWithLease(ctx, postgres.WorkerOutcomeParams{
		OutboxID:              msg.ID,
		LeaseToken:            msg.LeaseToken,
		ApplicationID:         app.ID(),
		ExpectedAppRevision:   initialRevision,
		SubmissionID:          targetSub.ID,
		SubmissionStatus:      domain.SubmissionStatusAccepted,
		DefinitiveInvalidData: false,
		ApplicationStatus:     domain.ApplicationStatusUnderReview,
		Decision:              app.Platform().Decision,
		InformationRequests:   app.InformationRequests(),
		CompleteJob:           true,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTerminalStatusImmutable) {
			if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
				return errors.Join(err, compErr)
			}
			return err
		}
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return errors.Join(err, relErr)
			}
			return err
		}
		return err
	}

	return nil
}

// processRFIResponseJob executes the verified RFI retrieve & respond sequence.
// Provenance: https://www.airwallex.com/docs/connected-accounts/handle-rfis/handle-kyc-rfi/native-kyc-rfi-api.md (2026-11-08)
//
// Invariants enforced:
// 1. MUST retrieve/check remote RFI account_id matches mapped external connected account.
// 2. MUST verify remote RFI status is ACTION_REQUIRED.
// 3. MUST verify remote RFI type is KYC; documentary type stays manual handling (UNRESOLVED).
// 4. MUST verify all active questions have type TEXT and are answered using stable frozen ID mapping.
// 5. NO generic accounts/{id}/submit for RFI responses.
// 6. Response success schema/status is not yet verified in Docs MCP; client does not assert unverified fields.
func (w *Worker) processRFIResponseJob(
	ctx context.Context,
	msg *postgres.OutboxMessage,
	app *domain.OnboardingApplication,
	targetSub *domain.Submission,
	initialRevision int,
	externalAccountID string,
) error {
	// 1. Identify target InformationRequest aggregate
	var internalRFI *domain.InformationRequest
	for _, r := range app.InformationRequests() {
		if r.ID == targetSub.InformationRequestID {
			rCopy := r
			internalRFI = &rCopy
			break
		}
	}
	if internalRFI == nil {
		return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("information request %s not found on application", targetSub.InformationRequestID))
	}

	platformRfiID := strings.TrimSpace(internalRFI.PlatformReference)
	if platformRfiID == "" {
		if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
			return recErr
		}
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
	}

	// 2. Check dispatch step state for RESPOND_RFI
	stepState, _, err := w.repo.GetDispatchStepState(ctx, targetSub.ID, StepRespondRFI)
	if err != nil {
		if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
			return errors.Join(err, relErr)
		}
		return fmt.Errorf("failed to check dispatch step state for %s: %w", StepRespondRFI, err)
	}

	switch stepState {
	case postgres.DispatchStepStateDispatching:
		// Reclaimed DISPATCHING job goes to manual hold; outcome is uncertain
		if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
			return recErr
		}
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateUnresolved:
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)

	case postgres.DispatchStepStateConfirmed:
		// Already confirmed remotely; proceed to record acceptance in DB

	case postgres.DispatchStepStateNotStarted:
		// 1. Retrieve RFI from platform: GET /api/v1/rfis/{platformRfiId}
		remoteRFI, err := w.platform.GetRFI(ctx, platformRfiID)
		if err != nil {
			if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
				if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
					return errors.Join(err, relErr)
				}
				return err
			}
			if errors.Is(err, airwallex.ErrResourceNotFound) {
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("platform RFI %s not found (HTTP 404)", platformRfiID))
			}
			return err
		}

		// 2. MUST retrieve/check account_id equals mapped EXTERNAL connected account
		if remoteRFI.AccountID != externalAccountID {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("platform RFI account_id %q does not match application external account %q", remoteRFI.AccountID, externalAccountID))
		}

		// 3. MUST check RFI status is ACTION_REQUIRED
		if remoteRFI.Status != "ACTION_REQUIRED" {
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, fmt.Sprintf("platform RFI status is %q (expected ACTION_REQUIRED)", remoteRFI.Status))
		}

		// 4. MUST check RFI type is KYC; documentary type stays manual handling
		if !strings.EqualFold(remoteRFI.Type, "KYC") {
			if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
				return recErr
			}
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		// 5. Map and validate all active questions have type TEXT and are answered
		respondReq, err := airwallex.MapSubmissionToRFIRespondRequest(*targetSub, *internalRFI, *remoteRFI)
		if err != nil {
			if strings.Contains(err.Error(), "documentary requires manual handling") {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
			}
			return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
		}

		// 6. Start dispatch atomically: returns maySend = true ONLY on atomic NOT_STARTED -> DISPATCHING
		maySend, err := w.repo.StartDispatchStepWithLease(ctx, targetSub.ID, StepRespondRFI, msg.ID, msg.LeaseToken)
		if err != nil {
			return err
		}
		if !maySend {
			return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
		}

		// 7. Dispatch POST /api/v1/rfis/{platformRfiId}/respond
		_, err = w.platform.RespondRFI(ctx, platformRfiID, *respondReq)
		if err != nil {
			if errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
			}
			if errors.Is(err, airwallex.ErrRateLimited) || errors.Is(err, airwallex.ErrServerError) {
				if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
					return errors.Join(err, relErr)
				}
				return err
			}
			if errors.Is(err, airwallex.ErrInvalidData) || errors.Is(err, airwallex.ErrInvalidStateForOperation) || errors.Is(err, airwallex.ErrAccountNotAuthorisedForOperation) {
				if recErr := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateUnresolved, "", msg.ID, msg.LeaseToken); recErr != nil {
					return recErr
				}
				return w.handleDefinitiveFailure(ctx, msg, app, targetSub, initialRevision, err.Error())
			}
			return err
		}

		// 8. Record CONFIRMED
		if err := w.repo.RecordDispatchStepStateWithLease(ctx, targetSub.ID, StepRespondRFI, postgres.DispatchStepStateConfirmed, "", msg.ID, msg.LeaseToken); err != nil {
			return err
		}
	}

	// 9. Outcome persistence: RFI acceptance gate check
	// Per Lead review: transport delivery (200/201/204) alone is NOT evidence of provider acceptance.
	// Keep default response acceptance gate false pending verified provider confirmation source.
	if !w.cfg.AllowRFIResponseAcceptance {
		// Held in UNRESOLVED / manual hold pending verified platform confirmation
		return w.handleAmbiguousOutcome(ctx, msg, app, targetSub, initialRevision)
	}

	// 10. Explicitly gated acceptance: transitions RFI to RESPONDED, app back to UNDER_REVIEW
	err = w.repo.RecordWorkerSubmissionOutcomeWithLease(ctx, postgres.WorkerOutcomeParams{
		OutboxID:              msg.ID,
		LeaseToken:            msg.LeaseToken,
		ApplicationID:         app.ID(),
		ExpectedAppRevision:   initialRevision,
		SubmissionID:          targetSub.ID,
		SubmissionStatus:      domain.SubmissionStatusAccepted,
		DefinitiveInvalidData: false,
		ApplicationStatus:     domain.ApplicationStatusUnderReview,
		Decision:              app.Platform().Decision,
		InformationRequests:   app.InformationRequests(),
		CompleteJob:           true,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTerminalStatusImmutable) {
			if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
				return errors.Join(err, compErr)
			}
			return err
		}
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return errors.Join(err, relErr)
			}
			return err
		}
		return err
	}

	return nil
}

func (w *Worker) handleDefinitiveFailure(
	ctx context.Context,
	msg *postgres.OutboxMessage,
	app *domain.OnboardingApplication,
	sub *domain.Submission,
	expectedRevision int,
	reason string,
) error {
	err := w.repo.RecordWorkerSubmissionOutcomeWithLease(ctx, postgres.WorkerOutcomeParams{
		OutboxID:              msg.ID,
		LeaseToken:            msg.LeaseToken,
		ApplicationID:         app.ID(),
		ExpectedAppRevision:   expectedRevision,
		SubmissionID:          sub.ID,
		SubmissionStatus:      domain.SubmissionStatusFailed,
		DefinitiveInvalidData: true,
		ApplicationStatus:     domain.ApplicationStatusRejected,
		Decision:              nil, // Null decision per design doc
		InformationRequests:   app.InformationRequests(),
		CompleteJob:           true,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTerminalStatusImmutable) {
			if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
				return errors.Join(err, compErr)
			}
			return err
		}
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return errors.Join(err, relErr)
			}
			return err
		}
		return err
	}
	return nil
}

func (w *Worker) handleAmbiguousOutcome(
	ctx context.Context,
	msg *postgres.OutboxMessage,
	app *domain.OnboardingApplication,
	sub *domain.Submission,
	expectedRevision int,
) error {
	err := w.repo.RecordWorkerSubmissionOutcomeWithLease(ctx, postgres.WorkerOutcomeParams{
		OutboxID:              msg.ID,
		LeaseToken:            msg.LeaseToken,
		ApplicationID:         app.ID(),
		ExpectedAppRevision:   expectedRevision,
		SubmissionID:          sub.ID,
		SubmissionStatus:      domain.SubmissionStatusUnresolved,
		DefinitiveInvalidData: false,
		ApplicationStatus:     app.Status(), // Remains SUBMISSION_PENDING or INFORMATION_REQUIRED
		Decision:              nil,
		InformationRequests:   app.InformationRequests(),
		CompleteJob:           true,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTerminalStatusImmutable) {
			if compErr := w.repo.CompleteOutboxMessage(ctx, msg.ID, msg.LeaseToken); compErr != nil {
				return errors.Join(err, compErr)
			}
			return err
		}
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			if relErr := w.repo.ReleaseOutboxMessageForRetry(ctx, msg.ID, msg.LeaseToken, w.cfg.RetryBackoff); relErr != nil {
				return errors.Join(err, relErr)
			}
			return err
		}
		return err
	}
	return nil
}
