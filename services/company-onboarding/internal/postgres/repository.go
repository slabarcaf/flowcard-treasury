package postgres

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

// Standard sentinel domain errors for persistence layer.
var (
	ErrCompanyNotFound                    = errors.New("company not found")
	ErrApplicationNotFound                = errors.New("application not found")
	ErrPlatformAccountNotFound            = errors.New("platform account not found")
	ErrSubmissionNotFound                 = errors.New("submission not found")
	ErrCompanyDeletionIneligible          = errors.New("company is ineligible for deletion")
	ErrInvalidCursor                      = errors.New("invalid pagination cursor")
	ErrOpenApplicationExists              = errors.New("an open application already exists for this company and platform")
	ErrCannotArchiveWithOpenApplications  = errors.New("company has open applications and cannot be archived")
	ErrOptimisticLockConflict             = errors.New("optimistic lock conflict: record revision mismatch")
	ErrPlatformAccountExists              = errors.New("a platform account already exists for this company, platform, and environment")
	ErrExternalAccountIDAlreadyLinked     = errors.New("this external account ID is already linked to another platform account in this environment")
	ErrIdempotencyKeyPayloadMismatch      = errors.New("idempotency key was previously called with a different request payload")
	ErrConcurrentOperationInProgress      = errors.New("a concurrent operation for this idempotency key is already in progress")
	ErrStaleLeaseToken                    = errors.New("outbox lease token is stale, expired, or invalid; message was reclaimed by another worker")
	ErrInvalidIdempotencyReservationToken = errors.New("invalid or expired idempotency reservation token")
	ErrInvalidDispatchTransition          = errors.New("invalid dispatch step transition: cannot reset confirmed or unresolved step to dispatching")
	ErrOutboxJobLinkageMismatch           = errors.New("outbox job linkage mismatch: message does not belong to application/submission")
	ErrDocumentFileIDMismatch             = errors.New("cannot overwrite existing platform file id with a different value")
)

// Repository provides PostgreSQL persistence for company onboarding aggregates.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL repository instance.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// OutboxMessage represents a queued asynchronous platform job.
type OutboxMessage struct {
	ID          string
	Topic       string
	Payload     []byte
	Status      string
	LeaseToken  string
	LeasedUntil *time.Time
	RetryCount  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IdempotencyReservation represents the outcome of an idempotency check or claim.
type IdempotencyReservation struct {
	Reserved           bool
	ReservationToken   string
	ResponseStatusCode int
	ResponseBody       []byte
}

func generateSecureToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- Company Operations ---

// CreateCompany persists a new Company aggregate.
func (r *Repository) CreateCompany(ctx context.Context, c *domain.Company) error {
	sql := `
		INSERT INTO onboarding.companies (
			id, legal_name, trading_name, entity_type, registration_country,
			registration_state, status, has_submitted_history, business_profile,
			registration_identifiers, people, audit_trail, revision, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
	`
	regIDsJSON, err := json.Marshal(c.RegistrationIdentifiers())
	if err != nil {
		return fmt.Errorf("failed to marshal registration identifiers: %w", err)
	}
	peopleJSON, err := json.Marshal(c.People())
	if err != nil {
		return fmt.Errorf("failed to marshal people: %w", err)
	}
	auditJSON, err := json.Marshal(c.LegalIdentifierAuditTrail())
	if err != nil {
		return fmt.Errorf("failed to marshal audit trail: %w", err)
	}
	profileJSON, err := json.Marshal(c.BusinessProfile())
	if err != nil {
		return fmt.Errorf("failed to marshal business profile: %w", err)
	}

	var tradingName *string
	if c.TradingName() != "" {
		tn := c.TradingName()
		tradingName = &tn
	}

	var legalName *string
	if c.LegalName() != "" {
		ln := c.LegalName()
		legalName = &ln
	}

	var entityType *string
	if c.EntityType() != "" {
		et := c.EntityType()
		entityType = &et
	}

	var regCountry *string
	if c.RegistrationCountry() != "" {
		rc := c.RegistrationCountry()
		regCountry = &rc
	}

	var regState *string
	if c.RegistrationState() != "" {
		rs := c.RegistrationState()
		regState = &rs
	}

	_, err = r.pool.Exec(ctx, sql,
		c.ID(),
		legalName,
		tradingName,
		entityType,
		regCountry,
		regState,
		string(c.Status()),
		c.HasSubmittedHistory(),
		profileJSON,
		regIDsJSON,
		peopleJSON,
		auditJSON,
		c.Revision(),
		c.CreatedAt(),
		c.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert company: %w", err)
	}

	return nil
}

// GetCompany retrieves a Company aggregate by ID.
func (r *Repository) GetCompany(ctx context.Context, id string) (*domain.Company, error) {
	sql := `
		SELECT
			id, legal_name, trading_name, entity_type, registration_country,
			registration_state, status, has_submitted_history, business_profile,
			registration_identifiers, people, audit_trail, revision, created_at, updated_at
		FROM onboarding.companies
		WHERE id = $1
	`
	var (
		compID              string
		legalName           *string
		tradingName         *string
		entityType          *string
		regCountry          *string
		regState            *string
		status              string
		hasSubmittedHistory bool
		profileData         []byte
		regIDsData          []byte
		peopleData          []byte
		auditData           []byte
		revision            int
		createdAt           time.Time
		updatedAt           time.Time
	)

	err := r.pool.QueryRow(ctx, sql, strings.TrimSpace(id)).Scan(
		&compID,
		&legalName,
		&tradingName,
		&entityType,
		&regCountry,
		&regState,
		&status,
		&hasSubmittedHistory,
		&profileData,
		&regIDsData,
		&peopleData,
		&auditData,
		&revision,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("failed to query company: %w", err)
	}

	var profile domain.BusinessProfile
	if len(profileData) > 0 {
		if err := json.Unmarshal(profileData, &profile); err != nil {
			return nil, fmt.Errorf("failed to unmarshal business profile: %w", err)
		}
	}

	var regIDs []domain.RegistrationIdentifier
	if len(regIDsData) > 0 {
		if err := json.Unmarshal(regIDsData, &regIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal registration identifiers: %w", err)
		}
	}

	var people []domain.Person
	if len(peopleData) > 0 {
		if err := json.Unmarshal(peopleData, &people); err != nil {
			return nil, fmt.Errorf("failed to unmarshal people: %w", err)
		}
	}

	var audit []domain.LegalIdentifierAuditRecord
	if len(auditData) > 0 {
		if err := json.Unmarshal(auditData, &audit); err != nil {
			return nil, fmt.Errorf("failed to unmarshal audit trail: %w", err)
		}
	}

	lName := ""
	if legalName != nil {
		lName = *legalName
	}
	tName := ""
	if tradingName != nil {
		tName = *tradingName
	}
	eType := ""
	if entityType != nil {
		eType = *entityType
	}
	rCountry := ""
	if regCountry != nil {
		rCountry = *regCountry
	}
	rState := ""
	if regState != nil {
		rState = *regState
	}

	comp, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                      compID,
		Status:                  domain.CompanyStatus(status),
		Revision:                revision,
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
		HasSubmittedHistory:     hasSubmittedHistory,
		LegalName:               lName,
		TradingName:             tName,
		EntityType:              eType,
		RegistrationCountry:     rCountry,
		RegistrationState:       rState,
		RegistrationIdentifiers: regIDs,
		BusinessProfile:         profile,
		People:                  people,
		AuditTrail:              audit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute company: %w", err)
	}

	return comp, nil
}

// UpdateCompany updates a Company aggregate using optimistic CAS verification against expectedRevision.
// Enforces monotonic has_submitted_history: once true, it can never be cleared back to false.
func (r *Repository) UpdateCompany(ctx context.Context, c *domain.Company, expectedRevision int) error {
	sql := `
		UPDATE onboarding.companies
		SET
			legal_name = $1,
			trading_name = $2,
			entity_type = $3,
			registration_country = $4,
			registration_state = $5,
			status = $6,
			has_submitted_history = (has_submitted_history OR $7),
			business_profile = $8,
			registration_identifiers = $9,
			people = $10,
			audit_trail = $11,
			revision = $12,
			updated_at = $13
		WHERE id = $14 AND revision = $15
	`
	regIDsJSON, err := json.Marshal(c.RegistrationIdentifiers())
	if err != nil {
		return fmt.Errorf("failed to marshal registration identifiers: %w", err)
	}
	peopleJSON, err := json.Marshal(c.People())
	if err != nil {
		return fmt.Errorf("failed to marshal people: %w", err)
	}
	auditJSON, err := json.Marshal(c.LegalIdentifierAuditTrail())
	if err != nil {
		return fmt.Errorf("failed to marshal audit trail: %w", err)
	}
	profileJSON, err := json.Marshal(c.BusinessProfile())
	if err != nil {
		return fmt.Errorf("failed to marshal business profile: %w", err)
	}

	var tradingName *string
	if c.TradingName() != "" {
		tn := c.TradingName()
		tradingName = &tn
	}

	var legalName *string
	if c.LegalName() != "" {
		ln := c.LegalName()
		legalName = &ln
	}

	var entityType *string
	if c.EntityType() != "" {
		et := c.EntityType()
		entityType = &et
	}

	var regCountry *string
	if c.RegistrationCountry() != "" {
		rc := c.RegistrationCountry()
		regCountry = &rc
	}

	var regState *string
	if c.RegistrationState() != "" {
		rs := c.RegistrationState()
		regState = &rs
	}

	tag, err := r.pool.Exec(ctx, sql,
		legalName,
		tradingName,
		entityType,
		regCountry,
		regState,
		string(c.Status()),
		c.HasSubmittedHistory(),
		profileJSON,
		regIDsJSON,
		peopleJSON,
		auditJSON,
		c.Revision(),
		c.UpdatedAt(),
		c.ID(),
		expectedRevision,
	)
	if err != nil {
		return fmt.Errorf("failed to update company: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrOptimisticLockConflict
	}

	return nil
}

// ArchiveCompany locks the Company row FOR UPDATE, verifies no open applications exist,
// and updates the status to ARCHIVED.
func (r *Repository) ArchiveCompany(ctx context.Context, companyID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock Company row
	var status string
	var revision int
	err = tx.QueryRow(ctx, `
		SELECT status, revision
		FROM onboarding.companies
		WHERE id = $1
		FOR UPDATE
	`, companyID).Scan(&status, &revision)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCompanyNotFound
		}
		return fmt.Errorf("failed to lock company: %w", err)
	}

	if status == string(domain.CompanyStatusArchived) {
		return nil // idempotent duplicate
	}

	// Verify no open applications exist
	var openCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM onboarding.applications
		WHERE company_id = $1 AND status IN ('DRAFT', 'SUBMISSION_PENDING', 'UNDER_REVIEW', 'INFORMATION_REQUIRED')
	`, companyID).Scan(&openCount)
	if err != nil {
		return fmt.Errorf("failed to check open applications: %w", err)
	}

	if openCount > 0 {
		return ErrCannotArchiveWithOpenApplications
	}

	// Update to ARCHIVED
	_, err = tx.Exec(ctx, `
		UPDATE onboarding.companies
		SET status = $1, revision = revision + 1, updated_at = $2
		WHERE id = $3 AND revision = $4
	`, string(domain.CompanyStatusArchived), time.Now().UTC(), companyID, revision)
	if err != nil {
		return fmt.Errorf("failed to archive company: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit archive transaction: %w", err)
	}

	return nil
}

// --- Application Operations ---

// CreateApplication serializes against the Company row (FOR UPDATE) to ensure it is not archived,
// and inserts the application aggregate. The partial unique index enforces at most one open application.
func (r *Repository) CreateApplication(ctx context.Context, app *domain.OnboardingApplication) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize with Company-row lock
	var compStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM onboarding.companies WHERE id = $1 FOR UPDATE
	`, app.Company().ID()).Scan(&compStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCompanyNotFound
		}
		return fmt.Errorf("failed to lock company for application creation: %w", err)
	}

	if compStatus == string(domain.CompanyStatusArchived) {
		return domain.ErrCompanyArchived
	}

	sql := `
		INSERT INTO onboarding.applications (
			id, company_id, status, platform_name, platform_environment,
			platform_account_id, decision, onboarding_details, company_snapshot,
			documents, information_requests, creator, operator_review_flagged,
			revision, created_at, updated_at, closed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
	`
	onboardingDetailsJSON, err := json.Marshal(app.Platform().OnboardingDetails)
	if err != nil {
		return fmt.Errorf("failed to marshal onboarding details: %w", err)
	}
	companySnapshotJSON, err := json.Marshal(app.Company())
	if err != nil {
		return fmt.Errorf("failed to marshal company snapshot: %w", err)
	}
	docsJSON, err := json.Marshal(app.Documents())
	if err != nil {
		return fmt.Errorf("failed to marshal documents: %w", err)
	}
	rfisJSON, err := json.Marshal(app.InformationRequests())
	if err != nil {
		return fmt.Errorf("failed to marshal information requests: %w", err)
	}

	var decision *string
	if app.Platform().Decision != nil {
		d := string(*app.Platform().Decision)
		decision = &d
	}

	var platformAccountID *string
	if app.Platform().PlatformAccountID != "" {
		paid := app.Platform().PlatformAccountID
		platformAccountID = &paid
	}

	env := app.Platform().Environment
	if env == "" {
		env = "sandbox"
	}

	_, err = tx.Exec(ctx, sql,
		app.ID(),
		app.Company().ID(),
		string(app.Status()),
		app.Platform().Name,
		env,
		platformAccountID,
		decision,
		onboardingDetailsJSON,
		companySnapshotJSON,
		docsJSON,
		rfisJSON,
		"creator",
		app.OperatorReviewFlagged(),
		app.Revision(),
		app.CreatedAt(),
		app.UpdatedAt(),
		app.ClosedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "idx_applications_one_open_per_company_platform") {
				return ErrOpenApplicationExists
			}
		}
		return fmt.Errorf("failed to insert application: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit application creation: %w", err)
	}

	return nil
}

// GetApplication retrieves an application aggregate with all child submissions, documents, and RFIs.
func (r *Repository) GetApplication(ctx context.Context, id string) (*domain.OnboardingApplication, error) {
	appSQL := `
		SELECT
			id, company_id, status, platform_name, platform_environment,
			platform_account_id, decision, onboarding_details, company_snapshot,
			documents, information_requests, creator, operator_review_flagged,
			revision, created_at, updated_at, closed_at
		FROM onboarding.applications
		WHERE id = $1
	`
	var (
		appID             string
		companyID         string
		status            string
		platformName      string
		platformEnv       string
		platformAccountID *string
		decision          *string
		detailsData       []byte
		companyData       []byte
		docsData          []byte
		rfisData          []byte
		creator           string
		flagged           bool
		revision          int
		createdAt         time.Time
		updatedAt         time.Time
		closedAt          *time.Time
	)

	err := r.pool.QueryRow(ctx, appSQL, strings.TrimSpace(id)).Scan(
		&appID,
		&companyID,
		&status,
		&platformName,
		&platformEnv,
		&platformAccountID,
		&decision,
		&detailsData,
		&companyData,
		&docsData,
		&rfisData,
		&creator,
		&flagged,
		&revision,
		&createdAt,
		&updatedAt,
		&closedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("failed to query application: %w", err)
	}

	var company domain.Company
	if err := json.Unmarshal(companyData, &company); err != nil {
		return nil, fmt.Errorf("failed to unmarshal company snapshot: %w", err)
	}

	var details domain.PlatformOnboardingDetails
	if len(detailsData) > 0 {
		if err := json.Unmarshal(detailsData, &details); err != nil {
			return nil, fmt.Errorf("failed to unmarshal onboarding details: %w", err)
		}
	}

	var docs []domain.Document
	if len(docsData) > 0 {
		if err := json.Unmarshal(docsData, &docs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal documents: %w", err)
		}
	}

	var rfis []domain.InformationRequest
	if len(rfisData) > 0 {
		if err := json.Unmarshal(rfisData, &rfis); err != nil {
			return nil, fmt.Errorf("failed to unmarshal information requests: %w", err)
		}
	}

	// Query Submissions
	subSQL := `
		SELECT
			id, submission_type, status, definitive_invalid_data, company_copy,
			onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		FROM onboarding.submissions
		WHERE application_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, subSQL, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to query submissions: %w", err)
	}
	defer rows.Close()

	var submissions []domain.Submission
	for rows.Next() {
		var (
			subID            string
			subType          string
			subStatus        string
			definitiveFailed bool
			subCompData      []byte
			subDetailsData   []byte
			subDocIDsData    []byte
			rfiID            *string
			answersData      []byte
			actor            string
			subCreatedAt     time.Time
			acceptedAt       *time.Time
		)
		if err := rows.Scan(
			&subID,
			&subType,
			&subStatus,
			&definitiveFailed,
			&subCompData,
			&subDetailsData,
			&subDocIDsData,
			&rfiID,
			&answersData,
			&actor,
			&subCreatedAt,
			&acceptedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan submission: %w", err)
		}

		var subComp domain.Company
		if len(subCompData) > 0 {
			if err := json.Unmarshal(subCompData, &subComp); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission company copy: %w", err)
			}
		}

		var subDetails domain.PlatformOnboardingDetails
		if len(subDetailsData) > 0 {
			if err := json.Unmarshal(subDetailsData, &subDetails); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission onboarding details copy: %w", err)
			}
		}

		var docIDs []string
		if len(subDocIDsData) > 0 {
			if err := json.Unmarshal(subDocIDsData, &docIDs); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission doc IDs: %w", err)
			}
		}

		var answers map[string]string
		if len(answersData) > 0 {
			if err := json.Unmarshal(answersData, &answers); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission answers: %w", err)
			}
		}

		rfiIDStr := ""
		if rfiID != nil {
			rfiIDStr = *rfiID
		}

		submissions = append(submissions, domain.Submission{
			ID:                    subID,
			Type:                  domain.SubmissionType(subType),
			Status:                domain.SubmissionStatus(subStatus),
			DefinitiveInvalidData: definitiveFailed,
			CompanyCopy:           &subComp,
			OnboardingDetailsCopy: subDetails,
			DocumentIDs:           docIDs,
			InformationRequestID:  rfiIDStr,
			Answers:               answers,
			SubmittingActor:       actor,
			CreatedAt:             subCreatedAt,
			AcceptedAt:            acceptedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error querying submissions: %w", err)
	}

	var dPlatform *domain.PlatformDecision
	if decision != nil {
		dec := domain.PlatformDecision(*decision)
		dPlatform = &dec
	}

	paid := ""
	if platformAccountID != nil {
		paid = *platformAccountID
	}

	// Load live company from onboarding.companies to provide live reference context for documents attached post-draft
	var liveCompany *domain.Company
	if liveComp, lErr := r.GetCompany(ctx, companyID); lErr == nil {
		liveCompany = liveComp
	}

	reconApp, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:          appID,
		Company:     &company,
		LiveCompany: liveCompany,
		Platform: domain.ApplicationPlatform{
			Name:              platformName,
			Environment:       platformEnv,
			PlatformAccountID: paid,
			Decision:          dPlatform,
			OnboardingDetails: details,
		},
		Status:                domain.ApplicationStatus(status),
		Documents:             docs,
		Submissions:           submissions,
		InformationRequests:   rfis,
		Creator:               creator,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		ClosedAt:              closedAt,
		Revision:              revision,
		OperatorReviewFlagged: flagged,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute application: %w", err)
	}

	return reconApp, nil
}

// UpdateApplication performs an optimistic CAS update on the application aggregate
// and persists any updated submission status without overwriting immutable submission history.
func (r *Repository) UpdateApplication(ctx context.Context, app *domain.OnboardingApplication, expectedRevision int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sql := `
		UPDATE onboarding.applications
		SET
			status = $1,
			platform_account_id = $2,
			decision = $3,
			onboarding_details = $4,
			company_snapshot = $5,
			documents = $6,
			information_requests = $7,
			operator_review_flagged = $8,
			revision = $9,
			updated_at = $10,
			closed_at = $11
		WHERE id = $12 AND revision = $13
	`
	onboardingDetailsJSON, err := json.Marshal(app.Platform().OnboardingDetails)
	if err != nil {
		return fmt.Errorf("failed to marshal onboarding details: %w", err)
	}
	companySnapshotJSON, err := json.Marshal(app.Company())
	if err != nil {
		return fmt.Errorf("failed to marshal company snapshot: %w", err)
	}
	docsJSON, err := json.Marshal(app.Documents())
	if err != nil {
		return fmt.Errorf("failed to marshal documents: %w", err)
	}
	rfisJSON, err := json.Marshal(app.InformationRequests())
	if err != nil {
		return fmt.Errorf("failed to marshal information requests: %w", err)
	}

	var decision *string
	if app.Platform().Decision != nil {
		d := string(*app.Platform().Decision)
		decision = &d
	}

	var paid *string
	if app.Platform().PlatformAccountID != "" {
		p := app.Platform().PlatformAccountID
		paid = &p
	}

	tag, err := tx.Exec(ctx, sql,
		string(app.Status()),
		paid,
		decision,
		onboardingDetailsJSON,
		companySnapshotJSON,
		docsJSON,
		rfisJSON,
		app.OperatorReviewFlagged(),
		app.Revision(),
		app.UpdatedAt(),
		app.ClosedAt(),
		app.ID(),
		expectedRevision,
	)
	if err != nil {
		return fmt.Errorf("failed to update application: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrOptimisticLockConflict
	}

	// Persist child submission status updates (lifecycle status and decision timestamps only; snapshots remain immutable)
	subUpdateSQL := `
		UPDATE onboarding.submissions
		SET
			status = $1,
			definitive_invalid_data = $2,
			accepted_at = $3
		WHERE id = $4 AND application_id = $5
	`
	for _, sub := range app.Submissions() {
		_, err := tx.Exec(ctx, subUpdateSQL,
			string(sub.Status),
			sub.DefinitiveInvalidData,
			sub.AcceptedAt,
			sub.ID,
			app.ID(),
		)
		if err != nil {
			return fmt.Errorf("failed to update child submission %s: %w", sub.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit update transaction: %w", err)
	}

	return nil
}

// SubmissionReadinessCheck runs against the persisted Company and Application
// locked by this transaction.
type SubmissionReadinessCheck func(*domain.Company, *domain.OnboardingApplication) error

// InitiateSubmissionParams carries parameters for the atomic submission transaction.
// All fields are explicit command inputs without backwards-compatibility fallback objects.
type InitiateSubmissionParams struct {
	ApplicationID        string
	ExpectedAppRevision  int
	SubmissionID         string
	SubmittingActor      string
	DocumentIDs          []string
	InformationRequestID string            // Optional: if set, executes RespondToInformationRequest; if empty, executes InitiateInitialSubmission
	Answers              map[string]string // Optional: required if InformationRequestID is set
	OutboxTopic          string
	OutboxPayload        []byte
	IdempotencyScope     string
	IdempotencyKey       string
	RequestFingerprint   string
	ResponseStatusCode   int
	ResponseBody         []byte
	ReadinessCheck       SubmissionReadinessCheck
}

// InitiateSubmissionResult contains the durable response of InitiateSubmission.
type InitiateSubmissionResult struct {
	Replayed           bool
	ResponseStatusCode int
	ResponseBody       []byte
}

// InitiateSubmission atomically verifies idempotency, locks the Company row (first in global lock hierarchy),
// reads the actual persisted Company to freeze into the submission, advances Company revision and sets
// has_submitted_history monotonically, locks Application (second in hierarchy), executes the reviewed
// domain aggregate command (InitiateInitialSubmission or RespondToInformationRequest), performs CAS update,
// inserts the authentic domain submission record, appends the outbox message, and marks idempotency completed.
//
// NOTE on Company Revision Semantics:
// The frozen Company copy preserved within the Submission and the Application's company_snapshot
// reflects the authentic pre-submission content revision. The subsequent update to onboarding.companies
// (setting has_submitted_history = TRUE and revision = revision + 1) is internal persistence bookkeeping;
// the frozen historical submission snapshot intentionally does not share this post-commit live revision.
func (r *Repository) InitiateSubmission(ctx context.Context, params InitiateSubmissionParams) (*InitiateSubmissionResult, error) {
	// Validate required parameters: explicit command inputs only
	if strings.TrimSpace(params.ApplicationID) == "" {
		return nil, errors.New("application ID must not be empty")
	}

	submissionID := strings.TrimSpace(params.SubmissionID)
	if submissionID == "" {
		return nil, errors.New("submission ID must not be empty")
	}

	submittingActor := strings.TrimSpace(params.SubmittingActor)
	if submittingActor == "" {
		return nil, errors.New("submitting actor must not be empty")
	}

	if strings.TrimSpace(params.OutboxTopic) == "" || len(params.OutboxPayload) == 0 {
		return nil, errors.New("outbox topic and non-empty payload are required")
	}
	if strings.TrimSpace(params.IdempotencyScope) == "" || strings.TrimSpace(params.IdempotencyKey) == "" || strings.TrimSpace(params.RequestFingerprint) == "" {
		return nil, errors.New("idempotency scope, key, and request fingerprint are required")
	}
	if params.ResponseStatusCode <= 0 || len(params.ResponseBody) == 0 {
		return nil, errors.New("response status code and non-empty response body are required")
	}

	docIDs := params.DocumentIDs
	rfiID := strings.TrimSpace(params.InformationRequestID)
	answers := params.Answers

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Reserve idempotency row via INSERT ON CONFLICT DO NOTHING to eliminate fresh key race conditions,
	// then SELECT ... FOR UPDATE to lock the row and serialize concurrent executions.
	idempInsertSQL := `
		INSERT INTO onboarding.idempotency_keys (
			key_scope, idempotency_key, request_fingerprint, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'STARTED', now(), now()
		)
		ON CONFLICT (key_scope, idempotency_key) DO NOTHING
	`
	idempInsertTag, err := tx.Exec(ctx, idempInsertSQL, params.IdempotencyScope, params.IdempotencyKey, params.RequestFingerprint)
	if err != nil {
		return nil, fmt.Errorf("failed to reserve idempotency key: %w", err)
	}

	var (
		existingFingerprint string
		existingStatus      string
		existingStatusCode  *int
		existingBody        []byte
	)
	idempSelectSQL := `
		SELECT request_fingerprint, status, response_status_code, response_body
		FROM onboarding.idempotency_keys
		WHERE key_scope = $1 AND idempotency_key = $2
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, idempSelectSQL, params.IdempotencyScope, params.IdempotencyKey).Scan(
		&existingFingerprint,
		&existingStatus,
		&existingStatusCode,
		&existingBody,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to lock idempotency key row: %w", err)
	}

	// Verify request fingerprint match
	if existingFingerprint != params.RequestFingerprint {
		return nil, ErrIdempotencyKeyPayloadMismatch
	}

	// If already completed: return immutable original response without modifying domain state or outbox
	if existingStatus == "COMPLETED" {
		sc := 200
		if existingStatusCode != nil {
			sc = *existingStatusCode
		}
		return &InitiateSubmissionResult{
			Replayed:           true,
			ResponseStatusCode: sc,
			ResponseBody:       existingBody,
		}, nil
	}
	if idempInsertTag.RowsAffected() == 0 || existingStatus != "STARTED" {
		return nil, ErrConcurrentOperationInProgress
	}

	// 2. Fetch company_id for the application
	var companyID string
	err = tx.QueryRow(ctx, `SELECT company_id FROM onboarding.applications WHERE id = $1`, params.ApplicationID).Scan(&companyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("failed to query application company_id: %w", err)
	}

	// 3. Lock Company row FIRST (global lock ordering: Company -> Application)
	var (
		compLegalName   *string
		compTradingName *string
		compEntityType  *string
		compRegCountry  *string
		compRegState    *string
		compStatus      string
		compHasHistory  bool
		compProfileData []byte
		compRegIDsData  []byte
		compPeopleData  []byte
		compAuditData   []byte
		compRevision    int
		compCreatedAt   time.Time
		compUpdatedAt   time.Time
	)
	compLockSQL := `
		SELECT
			legal_name, trading_name, entity_type, registration_country,
			registration_state, status, has_submitted_history, business_profile,
			registration_identifiers, people, audit_trail, revision, created_at, updated_at
		FROM onboarding.companies
		WHERE id = $1
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, compLockSQL, companyID).Scan(
		&compLegalName,
		&compTradingName,
		&compEntityType,
		&compRegCountry,
		&compRegState,
		&compStatus,
		&compHasHistory,
		&compProfileData,
		&compRegIDsData,
		&compPeopleData,
		&compAuditData,
		&compRevision,
		&compCreatedAt,
		&compUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("failed to lock company for submission: %w", err)
	}

	if compStatus == string(domain.CompanyStatusArchived) {
		return nil, domain.ErrCompanyArchived
	}

	// Reconstitute authentic persisted Company snapshot from the locked DB row
	var profile domain.BusinessProfile
	if len(compProfileData) > 0 {
		if err := json.Unmarshal(compProfileData, &profile); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company profile: %w", err)
		}
	}
	var regIDs []domain.RegistrationIdentifier
	if len(compRegIDsData) > 0 {
		if err := json.Unmarshal(compRegIDsData, &regIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company regIDs: %w", err)
		}
	}
	var people []domain.Person
	if len(compPeopleData) > 0 {
		if err := json.Unmarshal(compPeopleData, &people); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company people: %w", err)
		}
	}
	var audit []domain.LegalIdentifierAuditRecord
	if len(compAuditData) > 0 {
		if err := json.Unmarshal(compAuditData, &audit); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company audit: %w", err)
		}
	}
	lName := ""
	if compLegalName != nil {
		lName = *compLegalName
	}
	tName := ""
	if compTradingName != nil {
		tName = *compTradingName
	}
	eType := ""
	if compEntityType != nil {
		eType = *compEntityType
	}
	rCountry := ""
	if compRegCountry != nil {
		rCountry = *compRegCountry
	}
	rState := ""
	if compRegState != nil {
		rState = *compRegState
	}

	persistedCompany, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                      companyID,
		Status:                  domain.CompanyStatus(compStatus),
		Revision:                compRevision,
		CreatedAt:               compCreatedAt,
		UpdatedAt:               compUpdatedAt,
		HasSubmittedHistory:     compHasHistory,
		LegalName:               lName,
		TradingName:             tName,
		EntityType:              eType,
		RegistrationCountry:     rCountry,
		RegistrationState:       rState,
		RegistrationIdentifiers: regIDs,
		BusinessProfile:         profile,
		People:                  people,
		AuditTrail:              audit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute persisted company: %w", err)
	}

	// 4. Lock Application row SECOND (consistent lock order: Company -> Application)
	appLockSQL := `
		SELECT
			id, company_id, status, platform_name, platform_environment,
			platform_account_id, decision, onboarding_details, company_snapshot,
			documents, information_requests, creator, operator_review_flagged,
			revision, created_at, updated_at, closed_at
		FROM onboarding.applications
		WHERE id = $1
		FOR UPDATE
	`
	var (
		appID             string
		appCompanyID      string
		appStatus         string
		platformName      string
		platformEnv       string
		platformAccountID *string
		decision          *string
		detailsData       []byte
		companyData       []byte
		docsData          []byte
		rfisData          []byte
		creator           string
		flagged           bool
		appRevision       int
		appCreatedAt      time.Time
		appUpdatedAt      time.Time
		closedAt          *time.Time
	)
	err = tx.QueryRow(ctx, appLockSQL, params.ApplicationID).Scan(
		&appID,
		&appCompanyID,
		&appStatus,
		&platformName,
		&platformEnv,
		&platformAccountID,
		&decision,
		&detailsData,
		&companyData,
		&docsData,
		&rfisData,
		&creator,
		&flagged,
		&appRevision,
		&appCreatedAt,
		&appUpdatedAt,
		&closedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("failed to lock application: %w", err)
	}

	if appRevision != params.ExpectedAppRevision {
		return nil, ErrOptimisticLockConflict
	}
	if appCompanyID != companyID {
		return nil, fmt.Errorf("application company ID %q does not match locked company ID %q", appCompanyID, companyID)
	}

	var details domain.PlatformOnboardingDetails
	if len(detailsData) > 0 {
		if err := json.Unmarshal(detailsData, &details); err != nil {
			return nil, fmt.Errorf("failed to unmarshal onboarding details: %w", err)
		}
	}
	var docs []domain.Document
	if len(docsData) > 0 {
		if err := json.Unmarshal(docsData, &docs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal documents: %w", err)
		}
	}
	var rfis []domain.InformationRequest
	if len(rfisData) > 0 {
		if err := json.Unmarshal(rfisData, &rfis); err != nil {
			return nil, fmt.Errorf("failed to unmarshal information requests: %w", err)
		}
	}

	// Query existing submissions for reconstituting application aggregate
	subSQL := `
		SELECT
			id, submission_type, status, definitive_invalid_data, company_copy,
			onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		FROM onboarding.submissions
		WHERE application_id = $1
		ORDER BY created_at ASC
	`
	rows, err := tx.Query(ctx, subSQL, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to query existing submissions: %w", err)
	}
	defer rows.Close()

	var existingSubmissions []domain.Submission
	for rows.Next() {
		var (
			sID              string
			sType            string
			sStatus          string
			definitiveFailed bool
			sCompData        []byte
			sDetailsData     []byte
			sDocIDsData      []byte
			rfiIDPtr         *string
			answersData      []byte
			actor            string
			sCreatedAt       time.Time
			acceptedAt       *time.Time
		)
		if err := rows.Scan(
			&sID,
			&sType,
			&sStatus,
			&definitiveFailed,
			&sCompData,
			&sDetailsData,
			&sDocIDsData,
			&rfiIDPtr,
			&answersData,
			&actor,
			&sCreatedAt,
			&acceptedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan submission: %w", err)
		}

		var sComp domain.Company
		if len(sCompData) > 0 {
			if err := json.Unmarshal(sCompData, &sComp); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission company copy: %w", err)
			}
		}

		var sDetails domain.PlatformOnboardingDetails
		if len(sDetailsData) > 0 {
			if err := json.Unmarshal(sDetailsData, &sDetails); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission onboarding details copy: %w", err)
			}
		}

		var sDocIDs []string
		if len(sDocIDsData) > 0 {
			if err := json.Unmarshal(sDocIDsData, &sDocIDs); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission doc IDs: %w", err)
			}
		}

		var sAnswers map[string]string
		if len(answersData) > 0 {
			if err := json.Unmarshal(answersData, &sAnswers); err != nil {
				return nil, fmt.Errorf("failed to unmarshal submission answers: %w", err)
			}
		}

		rfiIDStr := ""
		if rfiIDPtr != nil {
			rfiIDStr = *rfiIDPtr
		}

		existingSubmissions = append(existingSubmissions, domain.Submission{
			ID:                    sID,
			Type:                  domain.SubmissionType(sType),
			Status:                domain.SubmissionStatus(sStatus),
			DefinitiveInvalidData: definitiveFailed,
			CompanyCopy:           &sComp,
			OnboardingDetailsCopy: sDetails,
			DocumentIDs:           sDocIDs,
			InformationRequestID:  rfiIDStr,
			Answers:               sAnswers,
			SubmittingActor:       actor,
			CreatedAt:             sCreatedAt,
			AcceptedAt:            acceptedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error querying submissions: %w", err)
	}

	// For DRAFT applications, reconstitute with live persisted Company details so latest edits are frozen.
	// For post-draft states, reconstitute with persisted company_snapshot to preserve historical initial snapshot.
	var appCompany *domain.Company
	if appStatus == string(domain.ApplicationStatusDraft) {
		appCompany = persistedCompany.DeepCopy()
	} else {
		var snap domain.Company
		if len(companyData) > 0 {
			if err := json.Unmarshal(companyData, &snap); err != nil {
				return nil, fmt.Errorf("failed to unmarshal company snapshot: %w", err)
			}
		}
		appCompany = &snap
	}

	var dPlatform *domain.PlatformDecision
	if decision != nil {
		dec := domain.PlatformDecision(*decision)
		dPlatform = &dec
	}

	paid := ""
	if platformAccountID != nil {
		paid = *platformAccountID
	}

	lockedApp, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:          appID,
		Company:     appCompany,
		LiveCompany: persistedCompany,
		Platform: domain.ApplicationPlatform{
			Name:              platformName,
			Environment:       platformEnv,
			PlatformAccountID: paid,
			Decision:          dPlatform,
			OnboardingDetails: details,
		},
		Status:                domain.ApplicationStatus(appStatus),
		Documents:             docs,
		Submissions:           existingSubmissions,
		InformationRequests:   rfis,
		Creator:               creator,
		CreatedAt:             appCreatedAt,
		UpdatedAt:             appUpdatedAt,
		ClosedAt:              closedAt,
		Revision:              appRevision,
		OperatorReviewFlagged: flagged,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute application: %w", err)
	}

	if rfiID == "" {
		for _, submission := range lockedApp.Submissions() {
			if submission.Status == domain.SubmissionStatusPending || submission.Status == domain.SubmissionStatusUnresolved {
				return nil, domain.ErrSubmissionInFlight
			}
		}
		if lockedApp.Status() != domain.ApplicationStatusDraft {
			return nil, domain.ErrApplicationNotDraft
		}
	}
	if params.ReadinessCheck != nil {
		if err := params.ReadinessCheck(persistedCompany, lockedApp); err != nil {
			return nil, err
		}
	}
	if rfiID == "" && params.DocumentIDs == nil {
		lockedDocuments := lockedApp.Documents()
		docIDs = make([]string, len(lockedDocuments))
		for i, document := range lockedDocuments {
			docIDs[i] = document.ID
		}
	}

	// 5. Execute reviewed domain aggregate command inside the transaction.
	// If application is terminal (or in invalid state for submission), command returns domain error immediately,
	// transaction rolls back, and zero mutations occur.
	var createdSubmission *domain.Submission
	if rfiID == "" {
		createdSubmission, err = lockedApp.InitiateInitialSubmission(
			submissionID,
			submittingActor,
			docIDs,
		)
		if err != nil {
			return nil, err
		}
	} else {
		createdSubmission, err = lockedApp.RespondToInformationRequest(
			submissionID,
			rfiID,
			submittingActor,
			persistedCompany,
			answers,
			docIDs,
		)
		if err != nil {
			return nil, err
		}
	}

	// 6. Advance Company revision and atomically enforce monotonic has_submitted_history
	compUpdateSQL := `
		UPDATE onboarding.companies
		SET has_submitted_history = TRUE,
		    revision = revision + 1,
		    updated_at = now()
		WHERE id = $1 AND revision = $2
	`
	tag, err := tx.Exec(ctx, compUpdateSQL, companyID, compRevision)
	if err != nil {
		return nil, fmt.Errorf("failed to update company submitted history: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrOptimisticLockConflict
	}

	// 7. Persist updated Application state from the domain command result (including company snapshot,
	// details, documents, and information requests)
	newCompJSON, err := json.Marshal(lockedApp.Company())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application company snapshot: %w", err)
	}
	newDetailsJSON, err := json.Marshal(lockedApp.Platform().OnboardingDetails)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application onboarding details: %w", err)
	}
	newDocsJSON, err := json.Marshal(lockedApp.Documents())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application documents: %w", err)
	}
	newRFIsJSON, err := json.Marshal(lockedApp.InformationRequests())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application information requests: %w", err)
	}

	appUpdateSQL := `
		UPDATE onboarding.applications
		SET
			status = $1,
			company_snapshot = $2,
			onboarding_details = $3,
			documents = $4,
			information_requests = $5,
			revision = $6,
			updated_at = $7
		WHERE id = $8 AND revision = $9
	`
	tag, err = tx.Exec(ctx, appUpdateSQL,
		string(lockedApp.Status()),
		newCompJSON,
		newDetailsJSON,
		newDocsJSON,
		newRFIsJSON,
		lockedApp.Revision(),
		lockedApp.UpdatedAt(),
		params.ApplicationID,
		params.ExpectedAppRevision,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update application in submission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrOptimisticLockConflict
	}

	// 8. Insert authentic domain Submission record
	subCompJSON, err := json.Marshal(createdSubmission.CompanyCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal submission company snapshot: %w", err)
	}
	subDetailsJSON, err := json.Marshal(createdSubmission.OnboardingDetailsCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal submission onboarding details copy: %w", err)
	}
	subDocIDsJSON, err := json.Marshal(createdSubmission.DocumentIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal submission document IDs: %w", err)
	}
	answersJSON, err := json.Marshal(createdSubmission.Answers)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal submission answers: %w", err)
	}

	var rfiIDPtr *string
	if createdSubmission.InformationRequestID != "" {
		rid := createdSubmission.InformationRequestID
		rfiIDPtr = &rid
	}

	subInsertSQL := `
		INSERT INTO onboarding.submissions (
			id, application_id, submission_type, status, definitive_invalid_data,
			company_copy, onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`
	_, err = tx.Exec(ctx, subInsertSQL,
		createdSubmission.ID,
		params.ApplicationID,
		string(createdSubmission.Type),
		string(createdSubmission.Status),
		createdSubmission.DefinitiveInvalidData,
		subCompJSON,
		subDetailsJSON,
		subDocIDsJSON,
		rfiIDPtr,
		answersJSON,
		createdSubmission.SubmittingActor,
		createdSubmission.CreatedAt,
		createdSubmission.AcceptedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert submission: %w", err)
	}

	// 9. Insert Outbox message for asynchronous dispatch
	outboxInsertSQL := `
		INSERT INTO onboarding.outbox_messages (
			id, topic, payload, status, retry_count, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'PENDING', 0, $4, $5
		)
	`
	outboxID, err := generateSecureToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate outbox ID: %w", err)
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, outboxInsertSQL,
		outboxID,
		params.OutboxTopic,
		params.OutboxPayload,
		now,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert outbox message: %w", err)
	}

	// 10. Update idempotency key record to COMPLETED (immutable original response)
	idempCompleteSQL := `
		UPDATE onboarding.idempotency_keys
		SET status = 'COMPLETED',
			response_status_code = $1,
			response_body = $2,
			updated_at = now()
		WHERE key_scope = $3 AND idempotency_key = $4 AND status = 'STARTED'
	`
	tag, err = tx.Exec(ctx, idempCompleteSQL,
		params.ResponseStatusCode,
		params.ResponseBody,
		params.IdempotencyScope,
		params.IdempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to complete idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("idempotency reservation row could not be completed")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit submission transaction: %w", err)
	}

	return &InitiateSubmissionResult{
		Replayed:           false,
		ResponseStatusCode: params.ResponseStatusCode,
		ResponseBody:       params.ResponseBody,
	}, nil
}

// --- PlatformAccount Operations ---

// CreatePlatformAccount persists a PlatformAccount, enforcing unique constraints per company/platform/env
// and unique external_account_id.
func (r *Repository) CreatePlatformAccount(ctx context.Context, pa *domain.PlatformAccount) error {
	sql := `
		INSERT INTO onboarding.platform_accounts (
			id, company_id, platform_name, environment, external_account_id,
			status, last_sync_time, revision, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
	`
	var extID *string
	if pa.ExternalAccountID() != "" {
		e := pa.ExternalAccountID()
		extID = &e
	}

	_, err := r.pool.Exec(ctx, sql,
		pa.ID(),
		pa.CompanyID(),
		pa.PlatformName(),
		pa.Environment(),
		extID,
		string(pa.Status()),
		pa.LastSyncTime(),
		pa.Revision(),
		pa.CreatedAt(),
		pa.UpdatedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "idx_platform_accounts_company_platform_env") {
				return ErrPlatformAccountExists
			}
			if strings.Contains(pgErr.ConstraintName, "idx_platform_accounts_ext_id_platform_env") {
				return ErrExternalAccountIDAlreadyLinked
			}
		}
		return fmt.Errorf("failed to insert platform account: %w", err)
	}

	return nil
}

// GetPlatformAccount retrieves a PlatformAccount aggregate by ID.
func (r *Repository) GetPlatformAccount(ctx context.Context, id string) (*domain.PlatformAccount, error) {
	sql := `
		SELECT
			id, company_id, platform_name, environment, external_account_id,
			status, last_sync_time, revision, created_at, updated_at
		FROM onboarding.platform_accounts
		WHERE id = $1
	`
	var (
		paID         string
		companyID    string
		platformName string
		env          string
		extID        *string
		status       string
		lastSync     *time.Time
		revision     int
		createdAt    time.Time
		updatedAt    time.Time
	)

	err := r.pool.QueryRow(ctx, sql, strings.TrimSpace(id)).Scan(
		&paID,
		&companyID,
		&platformName,
		&env,
		&extID,
		&status,
		&lastSync,
		&revision,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformAccountNotFound
		}
		return nil, fmt.Errorf("failed to query platform account: %w", err)
	}

	extIDStr := ""
	if extID != nil {
		extIDStr = *extID
	}

	pa, err := domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:                paID,
		CompanyID:         companyID,
		PlatformName:      platformName,
		Environment:       env,
		ExternalAccountID: extIDStr,
		Status:            domain.PlatformAccountStatus(status),
		LastSyncTime:      lastSync,
		Revision:          revision,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute platform account: %w", err)
	}

	return pa, nil
}

// GetPlatformAccountByCompany queries a platform account for a specific company, platform, and environment.
func (r *Repository) GetPlatformAccountByCompany(ctx context.Context, companyID, platform, env string) (*domain.PlatformAccount, error) {
	sql := `
		SELECT id
		FROM onboarding.platform_accounts
		WHERE company_id = $1 AND platform_name = $2 AND environment = $3
	`
	var id string
	err := r.pool.QueryRow(ctx, sql, companyID, platform, env).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformAccountNotFound
		}
		return nil, fmt.Errorf("failed to query platform account by company: %w", err)
	}
	return r.GetPlatformAccount(ctx, id)
}

// UpdatePlatformAccount updates a PlatformAccount with optimistic CAS revision verification against expectedRevision.
func (r *Repository) UpdatePlatformAccount(ctx context.Context, pa *domain.PlatformAccount, expectedRevision int) error {
	sql := `
		UPDATE onboarding.platform_accounts
		SET
			external_account_id = $1,
			status = $2,
			last_sync_time = $3,
			revision = $4,
			updated_at = $5
		WHERE id = $6 AND revision = $7
	`
	var extID *string
	if pa.ExternalAccountID() != "" {
		e := pa.ExternalAccountID()
		extID = &e
	}

	tag, err := r.pool.Exec(ctx, sql,
		extID,
		string(pa.Status()),
		pa.LastSyncTime(),
		pa.Revision(),
		pa.UpdatedAt(),
		pa.ID(),
		expectedRevision,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "idx_platform_accounts_ext_id_platform_env") {
				return ErrExternalAccountIDAlreadyLinked
			}
		}
		return fmt.Errorf("failed to update platform account: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrOptimisticLockConflict
	}

	return nil
}

// --- Fenced Idempotency Operations ---

// ReserveIdempotencyKey checks or claims an idempotency reservation row with a lease expiration.
// If already COMPLETED with matching fingerprint, returns the cached response.
// If active STARTED, returns ErrConcurrentOperationInProgress.
// If expired STARTED or non-existent, claims a new reservation with a secure token.
func (r *Repository) ReserveIdempotencyKey(ctx context.Context, scope, key, fingerprint string, ttl time.Duration) (*IdempotencyReservation, error) {
	if ttl <= 0 {
		return nil, errors.New("idempotency reservation TTL must be positive")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin reservation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	selectSQL := `
		SELECT request_fingerprint, status, response_status_code, response_body, expires_at
		FROM onboarding.idempotency_keys
		WHERE key_scope = $1 AND idempotency_key = $2
		FOR UPDATE
	`
	var (
		existingFingerprint string
		status              string
		statusCode          *int
		responseBody        []byte
		expiresAt           *time.Time
	)

	err = tx.QueryRow(ctx, selectSQL, scope, key).Scan(
		&existingFingerprint,
		&status,
		&statusCode,
		&responseBody,
		&expiresAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to query idempotency key: %w", err)
	}

	now := time.Now().UTC()

	// Existing row found
	if err == nil {
		if existingFingerprint != fingerprint {
			return nil, ErrIdempotencyKeyPayloadMismatch
		}

		if status == "COMPLETED" {
			sc := 200
			if statusCode != nil {
				sc = *statusCode
			}
			return &IdempotencyReservation{
				Reserved:           false,
				ResponseStatusCode: sc,
				ResponseBody:       responseBody,
			}, nil
		}

		// Row is STARTED: check if lease is still active
		if expiresAt != nil && expiresAt.After(now) {
			return nil, ErrConcurrentOperationInProgress
		}

		// Reservation expired: reclaim with new token
		token, err := generateSecureToken()
		if err != nil {
			return nil, err
		}
		newExpiry := now.Add(ttl)

		updateSQL := `
			UPDATE onboarding.idempotency_keys
			SET status = 'STARTED',
				reservation_token = $1,
				expires_at = $2,
				updated_at = $3
			WHERE key_scope = $4 AND idempotency_key = $5
		`
		if _, err := tx.Exec(ctx, updateSQL, token, newExpiry, now, scope, key); err != nil {
			return nil, fmt.Errorf("failed to reclaim expired idempotency reservation: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("failed to commit reservation: %w", err)
		}

		return &IdempotencyReservation{
			Reserved:         true,
			ReservationToken: token,
		}, nil
	}

	// Row does not exist: insert fresh reservation
	token, err := generateSecureToken()
	if err != nil {
		return nil, err
	}
	newExpiry := now.Add(ttl)

	insertSQL := `
		INSERT INTO onboarding.idempotency_keys (
			key_scope, idempotency_key, request_fingerprint, status,
			reservation_token, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'STARTED', $4, $5, $6, $7
		)
	`
	_, err = tx.Exec(ctx, insertSQL, scope, key, fingerprint, token, newExpiry, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert idempotency reservation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit reservation: %w", err)
	}

	return &IdempotencyReservation{
		Reserved:         true,
		ReservationToken: token,
	}, nil
}

// CompleteIdempotencyKey records the final response status code and body for a key,
// fenced by the active reservation token and unexpired lease.
func (r *Repository) CompleteIdempotencyKey(ctx context.Context, scope, key, reservationToken string, statusCode int, responseBody []byte) error {
	if strings.TrimSpace(reservationToken) == "" {
		return errors.New("reservation token is required to complete idempotency key")
	}

	sql := `
		UPDATE onboarding.idempotency_keys
		SET status = 'COMPLETED',
			response_status_code = $1,
			response_body = $2,
			reservation_token = NULL,
			expires_at = NULL,
			updated_at = now()
		WHERE key_scope = $3
		  AND idempotency_key = $4
		  AND status = 'STARTED'
		  AND reservation_token = $5
		  AND (expires_at IS NULL OR expires_at >= now())
	`
	tag, err := r.pool.Exec(ctx, sql, statusCode, responseBody, scope, key, reservationToken)
	if err != nil {
		return fmt.Errorf("failed to complete idempotency key: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrInvalidIdempotencyReservationToken
	}

	return nil
}

// --- Outbox and Worker Lease Operations ---

// ClaimOutboxMessage performs a short claim transaction using FOR UPDATE SKIP LOCKED
// with durable lease token fencing and server DB clock.
func (r *Repository) ClaimOutboxMessage(ctx context.Context, leaseDuration time.Duration) (*OutboxMessage, error) {
	if leaseDuration <= 0 {
		return nil, errors.New("lease duration must be positive")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	selectSQL := `
		SELECT id, topic, payload, retry_count
		FROM onboarding.outbox_messages
		WHERE (status = 'PENDING' AND (leased_until IS NULL OR leased_until < now()))
		   OR (status = 'CLAIMED' AND leased_until < now())
		ORDER BY created_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`
	var (
		id         string
		topic      string
		payload    []byte
		retryCount int
	)
	err = tx.QueryRow(ctx, selectSQL).Scan(&id, &topic, &payload, &retryCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // No pending jobs
		}
		return nil, fmt.Errorf("failed to select pending outbox message: %w", err)
	}

	leaseToken, err := generateSecureToken()
	if err != nil {
		return nil, err
	}

	intervalStr := fmt.Sprintf("%d milliseconds", leaseDuration.Milliseconds())
	updateSQL := `
		UPDATE onboarding.outbox_messages
		SET status = 'CLAIMED',
			lease_token = $1,
			leased_until = now() + $2::interval,
			retry_count = retry_count + 1,
			updated_at = now()
		WHERE id = $3
		RETURNING leased_until, updated_at
	`
	var leasedUntil time.Time
	var updatedAt time.Time
	err = tx.QueryRow(ctx, updateSQL, leaseToken, intervalStr, id).Scan(&leasedUntil, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to claim outbox message: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit claim transaction: %w", err)
	}

	return &OutboxMessage{
		ID:          id,
		Topic:       topic,
		Payload:     payload,
		Status:      "CLAIMED",
		LeaseToken:  leaseToken,
		LeasedUntil: &leasedUntil,
		RetryCount:  retryCount + 1,
		UpdatedAt:   updatedAt,
	}, nil
}

// CompleteOutboxMessage marks an outbox message as DELIVERED only if the worker holds the active lease token,
// status is CLAIMED, and leased_until is unexpired according to the server DB clock.
func (r *Repository) CompleteOutboxMessage(ctx context.Context, id, leaseToken string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(leaseToken) == "" {
		return errors.New("message ID and lease token must not be empty")
	}

	sql := `
		UPDATE onboarding.outbox_messages
		SET status = 'DELIVERED',
			lease_token = NULL,
			processed_at = now(),
			updated_at = now()
		WHERE id = $1
		  AND lease_token = $2
		  AND status = 'CLAIMED'
		  AND leased_until >= now()
	`
	tag, err := r.pool.Exec(ctx, sql, id, leaseToken)
	if err != nil {
		return fmt.Errorf("failed to complete outbox message: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrStaleLeaseToken
	}

	return nil
}

// ReleaseOutboxMessageForRetry releases a claimed outbox message so it can be retried
// after a backoff duration, fenced by active lease token.
func (r *Repository) ReleaseOutboxMessageForRetry(ctx context.Context, id, leaseToken string, backoff time.Duration) error {
	cleanID := strings.TrimSpace(id)
	cleanToken := strings.TrimSpace(leaseToken)
	if cleanID == "" || cleanToken == "" {
		return errors.New("message ID and lease token must not be empty")
	}

	intervalStr := fmt.Sprintf("%d milliseconds", backoff.Milliseconds())
	sql := `
		UPDATE onboarding.outbox_messages
		SET status = 'PENDING',
			lease_token = NULL,
			leased_until = now() + $1::interval,
			updated_at = now()
		WHERE id = $2
		  AND lease_token = $3
		  AND status = 'CLAIMED'
		  AND leased_until >= now()
	`
	tag, err := r.pool.Exec(ctx, sql, intervalStr, cleanID, cleanToken)
	if err != nil {
		return fmt.Errorf("failed to release outbox message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleLeaseToken
	}
	return nil
}

// DispatchStepState represents the durable intent state of an outbound platform dispatch step.
type DispatchStepState string

const (
	DispatchStepStateNotStarted  DispatchStepState = "NOT_STARTED"
	DispatchStepStateDispatching DispatchStepState = "DISPATCHING"
	DispatchStepStateConfirmed   DispatchStepState = "CONFIRMED"
	DispatchStepStateUnresolved  DispatchStepState = "UNRESOLVED"
)

// RecordDispatchStepStateWithLease atomically records the step state in onboarding.submission_dispatch_steps,
// fenced by the worker's active outbox lease token and CAS expected-state transitions.
// CONFIRMED and UNRESOLVED step states are terminal for dispatch and can NEVER be reset to DISPATCHING.
// Enforces that outbox message payload application_id matches the target submission's application_id.
func (r *Repository) RecordDispatchStepStateWithLease(
	ctx context.Context,
	submissionID string,
	stepName string,
	state DispatchStepState,
	externalID string,
	outboxID string,
	leaseToken string,
) error {
	cleanSubID := strings.TrimSpace(submissionID)
	cleanStep := strings.TrimSpace(stepName)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanSubID == "" || cleanStep == "" || cleanOutboxID == "" || cleanToken == "" {
		return errors.New("submissionID, stepName, outboxID, and leaseToken must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin dispatch step transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Linkage and lease verification: outbox payload application_id AND submission_id must match
	linkSQL := `
		SELECT om.payload->>'application_id', s.application_id, COALESCE(om.payload->>'submission_id', '')
		FROM onboarding.outbox_messages om
		JOIN onboarding.submissions s ON s.id = $1
		WHERE om.id = $2
		  AND om.lease_token = $3
		  AND om.status = 'CLAIMED'
		  AND om.leased_until >= now()
		FOR UPDATE OF om
	`
	var payloadAppID, subAppID, payloadSubID string
	err = tx.QueryRow(ctx, linkSQL, cleanSubID, cleanOutboxID, cleanToken).Scan(&payloadAppID, &subAppID, &payloadSubID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Check if outbox exists to distinguish stale lease from linkage mismatch
			var outboxExists int
			chkErr := tx.QueryRow(ctx, `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()`, cleanOutboxID, cleanToken).Scan(&outboxExists)
			if chkErr == nil {
				return ErrOutboxJobLinkageMismatch
			}
			return ErrStaleLeaseToken
		}
		return fmt.Errorf("failed to verify lease and linkage: %w", err)
	}

	if payloadAppID == "" || payloadAppID != subAppID || payloadSubID != cleanSubID {
		return ErrOutboxJobLinkageMismatch
	}

	var extID *string
	if strings.TrimSpace(externalID) != "" {
		s := strings.TrimSpace(externalID)
		extID = &s
	}

	// CAS Expected-State Transition check
	stepCheckSQL := `
		SELECT state, external_id
		FROM onboarding.submission_dispatch_steps
		WHERE submission_id = $1 AND step_name = $2
		FOR UPDATE
	`
	var currentStateStr string
	var currentExtID *string
	err = tx.QueryRow(ctx, stepCheckSQL, cleanSubID, cleanStep).Scan(&currentStateStr, &currentExtID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Step does not exist yet: CONFIRMED and UNRESOLVED are valid recorded states (e.g. from coordinator)
			if state != DispatchStepStateConfirmed && state != DispatchStepStateUnresolved {
				return ErrInvalidDispatchTransition
			}
			insertSQL := `
				INSERT INTO onboarding.submission_dispatch_steps (
					submission_id, step_name, state, external_id, created_at, updated_at
				) VALUES ($1, $2, $3, $4, now(), now())
			`
			if _, err := tx.Exec(ctx, insertSQL, cleanSubID, cleanStep, string(state), extID); err != nil {
				return fmt.Errorf("failed to insert dispatch step: %w", err)
			}
			return tx.Commit(ctx)
		}
		return fmt.Errorf("failed to check existing dispatch step: %w", err)
	}

	currentState := DispatchStepState(currentStateStr)

	// Invariant: DISPATCHING -> DISPATCHING is prohibited (no duplicate authorization to redispatch)
	if currentState == DispatchStepStateDispatching && state == DispatchStepStateDispatching {
		return ErrInvalidDispatchTransition
	}

	// Invariant: CONFIRMED or UNRESOLVED cannot be reset to DISPATCHING
	if currentState == DispatchStepStateConfirmed {
		if state == DispatchStepStateDispatching {
			return ErrInvalidDispatchTransition
		}
		if state == DispatchStepStateConfirmed {
			// Idempotent: allow populating external_id if previously empty
			if extID != nil && (currentExtID == nil || *currentExtID == "") {
				updateExtSQL := `UPDATE onboarding.submission_dispatch_steps SET external_id = $1, updated_at = now() WHERE submission_id = $2 AND step_name = $3`
				if _, err := tx.Exec(ctx, updateExtSQL, extID, cleanSubID, cleanStep); err != nil {
					return fmt.Errorf("failed to update external ID: %w", err)
				}
			}
			return tx.Commit(ctx)
		}
		return ErrInvalidDispatchTransition
	}

	if currentState == DispatchStepStateUnresolved {
		if state == DispatchStepStateDispatching || state == DispatchStepStateConfirmed {
			return ErrInvalidDispatchTransition
		}
		if state == DispatchStepStateUnresolved {
			return tx.Commit(ctx)
		}
		return ErrInvalidDispatchTransition
	}

	// Transition from DISPATCHING to CONFIRMED or UNRESOLVED
	updateSQL := `
		UPDATE onboarding.submission_dispatch_steps
		SET state = $1,
			external_id = COALESCE($2, onboarding.submission_dispatch_steps.external_id),
			updated_at = now()
		WHERE submission_id = $3 AND step_name = $4
	`
	if _, err := tx.Exec(ctx, updateSQL, string(state), extID, cleanSubID, cleanStep); err != nil {
		return fmt.Errorf("failed to update dispatch step: %w", err)
	}

	return tx.Commit(ctx)
}

// StartDispatchStepWithLease atomically transitions a step from NOT_STARTED to DISPATCHING.
// Returns maySend = true ONLY if this call atomically performed the transition.
// Reclaimed or competing workers observing an existing DISPATCHING, CONFIRMED, or UNRESOLVED state
// receive maySend = false (and no error), signaling that they must NOT send/redispatch the remote call.
func (r *Repository) StartDispatchStepWithLease(
	ctx context.Context,
	submissionID string,
	stepName string,
	outboxID string,
	leaseToken string,
) (bool, error) {
	cleanSubID := strings.TrimSpace(submissionID)
	cleanStep := strings.TrimSpace(stepName)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanSubID == "" || cleanStep == "" || cleanOutboxID == "" || cleanToken == "" {
		return false, errors.New("submissionID, stepName, outboxID, and leaseToken must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin start dispatch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Linkage and lease verification: exact application_id AND submission_id
	linkSQL := `
		SELECT om.payload->>'application_id', s.application_id, COALESCE(om.payload->>'submission_id', '')
		FROM onboarding.outbox_messages om
		JOIN onboarding.submissions s ON s.id = $1
		WHERE om.id = $2
		  AND om.lease_token = $3
		  AND om.status = 'CLAIMED'
		  AND om.leased_until >= now()
		FOR UPDATE OF om
	`
	var payloadAppID, subAppID, payloadSubID string
	err = tx.QueryRow(ctx, linkSQL, cleanSubID, cleanOutboxID, cleanToken).Scan(&payloadAppID, &subAppID, &payloadSubID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var outboxExists int
			chkErr := tx.QueryRow(ctx, `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()`, cleanOutboxID, cleanToken).Scan(&outboxExists)
			if chkErr == nil {
				return false, ErrOutboxJobLinkageMismatch
			}
			return false, ErrStaleLeaseToken
		}
		return false, fmt.Errorf("failed to verify lease and linkage: %w", err)
	}

	if payloadAppID == "" || payloadAppID != subAppID || payloadSubID != cleanSubID {
		return false, ErrOutboxJobLinkageMismatch
	}

	// CAS: check existing step
	stepCheckSQL := `
		SELECT state
		FROM onboarding.submission_dispatch_steps
		WHERE submission_id = $1 AND step_name = $2
		FOR UPDATE
	`
	var currentStateStr string
	err = tx.QueryRow(ctx, stepCheckSQL, cleanSubID, cleanStep).Scan(&currentStateStr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Atomically insert DISPATCHING - this worker holds the authorization to send!
			insertSQL := `
				INSERT INTO onboarding.submission_dispatch_steps (
					submission_id, step_name, state, external_id, created_at, updated_at
				) VALUES ($1, $2, 'DISPATCHING', NULL, now(), now())
			`
			if _, err := tx.Exec(ctx, insertSQL, cleanSubID, cleanStep); err != nil {
				return false, fmt.Errorf("failed to insert dispatch step: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("failed to commit start dispatch step: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("failed to check existing dispatch step: %w", err)
	}

	// Existing row exists (DISPATCHING, CONFIRMED, or UNRESOLVED)
	// Reclaimed or competing call: do NOT authorize redispatch!
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit query transaction: %w", err)
	}
	return false, nil
}

// GetDispatchStepState retrieves the durable intent state of a submission dispatch step.
// If no record exists, returns DispatchStepStateNotStarted.
func (r *Repository) GetDispatchStepState(ctx context.Context, submissionID, stepName string) (DispatchStepState, string, error) {
	cleanSubID := strings.TrimSpace(submissionID)
	cleanStep := strings.TrimSpace(stepName)
	if cleanSubID == "" || cleanStep == "" {
		return "", "", errors.New("submissionID and stepName must not be empty")
	}

	sql := `
		SELECT state, COALESCE(external_id, '')
		FROM onboarding.submission_dispatch_steps
		WHERE submission_id = $1 AND step_name = $2
	`
	var stateStr string
	var extID string
	err := r.pool.QueryRow(ctx, sql, cleanSubID, cleanStep).Scan(&stateStr, &extID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DispatchStepStateNotStarted, "", nil
		}
		return "", "", fmt.Errorf("failed to get dispatch step state: %w", err)
	}

	return DispatchStepState(stateStr), extID, nil
}

// RecordDocumentUploadWithLease durably records the platform_file_id for an uploaded document,
// fenced by the worker's active outbox lease token and validating job-to-submission linkage.
func (r *Repository) RecordDocumentUploadWithLease(
	ctx context.Context,
	submissionID string,
	documentID string,
	platformFileID string,
	outboxID string,
	leaseToken string,
) error {
	cleanSubID := strings.TrimSpace(submissionID)
	cleanDocID := strings.TrimSpace(documentID)
	cleanFileID := strings.TrimSpace(platformFileID)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanSubID == "" || cleanDocID == "" || cleanFileID == "" || cleanOutboxID == "" || cleanToken == "" {
		return errors.New("submissionID, documentID, platformFileID, outboxID, and leaseToken must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin document upload transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	linkSQL := `
		SELECT om.payload->>'application_id', s.application_id, COALESCE(om.payload->>'submission_id', '')
		FROM onboarding.outbox_messages om
		JOIN onboarding.submissions s ON s.id = $1
		WHERE om.id = $2
		  AND om.lease_token = $3
		  AND om.status = 'CLAIMED'
		  AND om.leased_until >= now()
		FOR UPDATE OF om
	`
	var payloadAppID, subAppID, payloadSubID string
	err = tx.QueryRow(ctx, linkSQL, cleanSubID, cleanOutboxID, cleanToken).Scan(&payloadAppID, &subAppID, &payloadSubID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var outboxExists int
			chkErr := tx.QueryRow(ctx, `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()`, cleanOutboxID, cleanToken).Scan(&outboxExists)
			if chkErr == nil {
				return ErrOutboxJobLinkageMismatch
			}
			return ErrStaleLeaseToken
		}
		return fmt.Errorf("failed to verify lease and linkage: %w", err)
	}
	if payloadAppID == "" || payloadAppID != subAppID || payloadSubID != cleanSubID {
		return ErrOutboxJobLinkageMismatch
	}

	checkSQL := `
		SELECT platform_file_id
		FROM onboarding.submission_document_uploads
		WHERE submission_id = $1 AND document_id = $2
		FOR UPDATE
	`
	var existingFileID string
	err = tx.QueryRow(ctx, checkSQL, cleanSubID, cleanDocID).Scan(&existingFileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			insertSQL := `
				INSERT INTO onboarding.submission_document_uploads (
					submission_id, document_id, platform_file_id, created_at
				) VALUES ($1, $2, $3, now())
			`
			if _, err := tx.Exec(ctx, insertSQL, cleanSubID, cleanDocID, cleanFileID); err != nil {
				return fmt.Errorf("failed to insert document upload: %w", err)
			}
			return tx.Commit(ctx)
		}
		return fmt.Errorf("failed to check existing document upload: %w", err)
	}

	if existingFileID == cleanFileID {
		// Idempotent: same ID already recorded
		return tx.Commit(ctx)
	}

	return ErrDocumentFileIDMismatch
}

// PlatformCreationCoordinatorState represents the durable state of platform creation for a company.
type PlatformCreationCoordinatorState string

const (
	PlatformCreationStateDispatching PlatformCreationCoordinatorState = "DISPATCHING"
	PlatformCreationStateConfirmed   PlatformCreationCoordinatorState = "CONFIRMED"
	PlatformCreationStateUnresolved  PlatformCreationCoordinatorState = "UNRESOLVED"
)

// PlatformCreationCoordinator tracks intent and state for creating a platform account for a company.
type PlatformCreationCoordinator struct {
	CompanyID          string
	PlatformName       string
	Environment        string
	State              PlatformCreationCoordinatorState
	ExternalAccountID  string
	OwningSubmissionID string
	OwningOutboxID     string
	IsNewClaim         bool
}

// ClaimPlatformCreationCoordinationWithLease atomically claims or retrieves the platform creation coordinator
// for a Company and platform/env, fenced by the worker's outbox lease token.
// Does NOT hold a DB lock during subsequent HTTP calls.
func (r *Repository) ClaimPlatformCreationCoordinationWithLease(
	ctx context.Context,
	companyID string,
	platformName string,
	environment string,
	submissionID string,
	outboxID string,
	leaseToken string,
) (*PlatformCreationCoordinator, error) {
	cleanCompID := strings.TrimSpace(companyID)
	cleanPlatform := strings.TrimSpace(platformName)
	cleanEnv := strings.TrimSpace(environment)
	cleanSubID := strings.TrimSpace(submissionID)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanCompID == "" || cleanPlatform == "" || cleanEnv == "" || cleanSubID == "" || cleanOutboxID == "" || cleanToken == "" {
		return nil, errors.New("companyID, platformName, environment, submissionID, outboxID, and leaseToken must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin coordinator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Verify lease token and exact linkage (application_id AND submission_id)
	linkSQL := `
		SELECT 1
		FROM onboarding.outbox_messages om
		JOIN onboarding.submissions s ON s.id = $1
		JOIN onboarding.applications a ON a.id = s.application_id
		WHERE om.id = $2
		  AND om.lease_token = $3
		  AND om.status = 'CLAIMED'
		  AND om.leased_until >= now()
		  AND om.payload->>'application_id' = a.id
		  AND COALESCE(om.payload->>'submission_id', '') = $1
		  AND a.company_id = $4
		FOR UPDATE OF om
	`
	var dummy int
	err = tx.QueryRow(ctx, linkSQL, cleanSubID, cleanOutboxID, cleanToken, cleanCompID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var outboxExists int
			chkErr := tx.QueryRow(ctx, `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()`, cleanOutboxID, cleanToken).Scan(&outboxExists)
			if chkErr == nil {
				return nil, ErrOutboxJobLinkageMismatch
			}
			return nil, ErrStaleLeaseToken
		}
		return nil, fmt.Errorf("failed to verify lease and linkage: %w", err)
	}

	// 2. Lock the Company row FOR UPDATE to serialize coordination across applications for the same Company
	var lockedCompID string
	if err := tx.QueryRow(ctx, `SELECT id FROM onboarding.companies WHERE id = $1 FOR UPDATE`, cleanCompID).Scan(&lockedCompID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("failed to lock company for creation coordination: %w", err)
	}

	// 3. Query existing coordinator for (company_id, platform_name, environment) FOR UPDATE
	selectSQL := `
		SELECT state, COALESCE(external_account_id, ''), owning_submission_id, owning_outbox_id
		FROM onboarding.platform_creation_coordinators
		WHERE company_id = $1 AND platform_name = $2 AND environment = $3
		FOR UPDATE
	`
	var stateStr, extID, owningSub, owningOutbox string
	err = tx.QueryRow(ctx, selectSQL, cleanCompID, cleanPlatform, cleanEnv).Scan(&stateStr, &extID, &owningSub, &owningOutbox)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Check if an existing platform account already exists with external_account_id
			var existingExtID *string
			paSQL := `SELECT external_account_id FROM onboarding.platform_accounts WHERE company_id = $1 AND platform_name = $2 AND environment = $3`
			paErr := tx.QueryRow(ctx, paSQL, cleanCompID, cleanPlatform, cleanEnv).Scan(&existingExtID)
			if paErr == nil && existingExtID != nil && *existingExtID != "" {
				insertSQL := `
					INSERT INTO onboarding.platform_creation_coordinators (
						company_id, platform_name, environment, state, external_account_id,
						owning_submission_id, owning_outbox_id, created_at, updated_at
					) VALUES ($1, $2, $3, 'CONFIRMED', $4, $5, $6, now(), now())
				`
				if _, err := tx.Exec(ctx, insertSQL, cleanCompID, cleanPlatform, cleanEnv, *existingExtID, cleanSubID, cleanOutboxID); err != nil {
					return nil, fmt.Errorf("failed to insert coordinator claim from existing platform account: %w", err)
				}
				if err := tx.Commit(ctx); err != nil {
					return nil, fmt.Errorf("failed to commit coordinator claim: %w", err)
				}
				return &PlatformCreationCoordinator{
					CompanyID:          cleanCompID,
					PlatformName:       cleanPlatform,
					Environment:        cleanEnv,
					State:              PlatformCreationStateConfirmed,
					ExternalAccountID:  *existingExtID,
					OwningSubmissionID: cleanSubID,
					OwningOutboxID:     cleanOutboxID,
					IsNewClaim:         false,
				}, nil
			} else if paErr != nil && !errors.Is(paErr, pgx.ErrNoRows) {
				return nil, fmt.Errorf("failed to query existing platform account during coordinator claim: %w", paErr)
			}

			// No existing coordinator; atomically claim DISPATCHING
			insertSQL := `
				INSERT INTO onboarding.platform_creation_coordinators (
					company_id, platform_name, environment, state, external_account_id,
					owning_submission_id, owning_outbox_id, created_at, updated_at
				) VALUES ($1, $2, $3, 'DISPATCHING', NULL, $4, $5, now(), now())
			`
			if _, err := tx.Exec(ctx, insertSQL, cleanCompID, cleanPlatform, cleanEnv, cleanSubID, cleanOutboxID); err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23505" {
					// Unique violation loser: another worker just inserted; read the existing row!
					if readErr := tx.QueryRow(ctx, selectSQL, cleanCompID, cleanPlatform, cleanEnv).Scan(&stateStr, &extID, &owningSub, &owningOutbox); readErr == nil {
						if err := tx.Commit(ctx); err != nil {
							return nil, fmt.Errorf("failed to commit read after collision: %w", err)
						}
						return &PlatformCreationCoordinator{
							CompanyID:          cleanCompID,
							PlatformName:       cleanPlatform,
							Environment:        cleanEnv,
							State:              PlatformCreationCoordinatorState(stateStr),
							ExternalAccountID:  extID,
							OwningSubmissionID: owningSub,
							OwningOutboxID:     owningOutbox,
							IsNewClaim:         false,
						}, nil
					}
				}
				return nil, fmt.Errorf("failed to insert coordinator claim: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("failed to commit coordinator claim: %w", err)
			}
			return &PlatformCreationCoordinator{
				CompanyID:          cleanCompID,
				PlatformName:       cleanPlatform,
				Environment:        cleanEnv,
				State:              PlatformCreationStateDispatching,
				ExternalAccountID:  "",
				OwningSubmissionID: cleanSubID,
				OwningOutboxID:     cleanOutboxID,
				IsNewClaim:         true,
			}, nil
		}
		return nil, fmt.Errorf("failed to query platform creation coordinator: %w", err)
	}

	// Existing coordinator found
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit query transaction: %w", err)
	}

	return &PlatformCreationCoordinator{
		CompanyID:          cleanCompID,
		PlatformName:       cleanPlatform,
		Environment:        cleanEnv,
		State:              PlatformCreationCoordinatorState(stateStr),
		ExternalAccountID:  extID,
		OwningSubmissionID: owningSub,
		OwningOutboxID:     owningOutbox,
		IsNewClaim:         false,
	}, nil
}

// ConfirmPlatformCreationCoordinationWithLease durably updates the coordinator to CONFIRMED with the external account ID.
func (r *Repository) ConfirmPlatformCreationCoordinationWithLease(
	ctx context.Context,
	companyID string,
	platformName string,
	environment string,
	externalAccountID string,
	submissionID string,
	outboxID string,
	leaseToken string,
) error {
	cleanCompID := strings.TrimSpace(companyID)
	cleanPlatform := strings.TrimSpace(platformName)
	cleanEnv := strings.TrimSpace(environment)
	cleanExtID := strings.TrimSpace(externalAccountID)
	cleanSubID := strings.TrimSpace(submissionID)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanCompID == "" || cleanPlatform == "" || cleanEnv == "" || cleanExtID == "" || cleanSubID == "" || cleanOutboxID == "" || cleanToken == "" {
		return errors.New("all parameters must be non-empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin confirm coordinator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	leaseSQL := `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now() FOR UPDATE`
	var dummy int
	if err := tx.QueryRow(ctx, leaseSQL, cleanOutboxID, cleanToken).Scan(&dummy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleLeaseToken
		}
		return fmt.Errorf("failed to verify lease token: %w", err)
	}

	updateSQL := `
		INSERT INTO onboarding.platform_creation_coordinators (
			company_id, platform_name, environment, state, external_account_id,
			owning_submission_id, owning_outbox_id, created_at, updated_at
		) VALUES ($1, $2, $3, 'CONFIRMED', $4, $5, $6, now(), now())
		ON CONFLICT (company_id, platform_name, environment) DO UPDATE
		SET state = 'CONFIRMED',
			external_account_id = EXCLUDED.external_account_id,
			updated_at = now()
	`
	_, err = tx.Exec(ctx, updateSQL, cleanCompID, cleanPlatform, cleanEnv, cleanExtID, cleanSubID, cleanOutboxID)
	if err != nil {
		return fmt.Errorf("failed to update coordinator to CONFIRMED: %w", err)
	}

	return tx.Commit(ctx)
}

// MarkPlatformCreationCoordinationUnresolvedWithLease durably updates the coordinator to UNRESOLVED.
func (r *Repository) MarkPlatformCreationCoordinationUnresolvedWithLease(
	ctx context.Context,
	companyID string,
	platformName string,
	environment string,
	submissionID string,
	outboxID string,
	leaseToken string,
) error {
	cleanCompID := strings.TrimSpace(companyID)
	cleanPlatform := strings.TrimSpace(platformName)
	cleanEnv := strings.TrimSpace(environment)
	cleanSubID := strings.TrimSpace(submissionID)
	cleanOutboxID := strings.TrimSpace(outboxID)
	cleanToken := strings.TrimSpace(leaseToken)

	if cleanCompID == "" || cleanPlatform == "" || cleanEnv == "" || cleanSubID == "" || cleanOutboxID == "" || cleanToken == "" {
		return errors.New("all parameters must be non-empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin unresolved coordinator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	leaseSQL := `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now() FOR UPDATE`
	var dummy int
	if err := tx.QueryRow(ctx, leaseSQL, cleanOutboxID, cleanToken).Scan(&dummy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleLeaseToken
		}
		return fmt.Errorf("failed to verify lease token: %w", err)
	}

	updateSQL := `
		INSERT INTO onboarding.platform_creation_coordinators (
			company_id, platform_name, environment, state, external_account_id,
			owning_submission_id, owning_outbox_id, created_at, updated_at
		) VALUES ($1, $2, $3, 'UNRESOLVED', NULL, $4, $5, now(), now())
		ON CONFLICT (company_id, platform_name, environment) DO UPDATE
		SET state = 'UNRESOLVED',
			updated_at = now()
	`
	_, err = tx.Exec(ctx, updateSQL, cleanCompID, cleanPlatform, cleanEnv, cleanSubID, cleanOutboxID)
	if err != nil {
		return fmt.Errorf("failed to update coordinator to UNRESOLVED: %w", err)
	}

	return tx.Commit(ctx)
}

// GetDocumentUploads retrieves all uploaded document file IDs for a submission.
func (r *Repository) GetDocumentUploads(ctx context.Context, submissionID string) (map[string]string, error) {
	cleanSubID := strings.TrimSpace(submissionID)
	if cleanSubID == "" {
		return nil, errors.New("submissionID must not be empty")
	}

	sql := `
		SELECT document_id, platform_file_id
		FROM onboarding.submission_document_uploads
		WHERE submission_id = $1
	`
	rows, err := r.pool.Query(ctx, sql, cleanSubID)
	if err != nil {
		return nil, fmt.Errorf("failed to query document uploads: %w", err)
	}
	defer rows.Close()

	uploads := make(map[string]string)
	for rows.Next() {
		var docID, fileID string
		if err := rows.Scan(&docID, &fileID); err != nil {
			return nil, fmt.Errorf("failed to scan document upload row: %w", err)
		}
		uploads[docID] = fileID
	}
	return uploads, rows.Err()
}

// SavePlatformAccountParams carries parameters for saving and linking platform account.
type SavePlatformAccountParams struct {
	ApplicationID     string
	ExternalAccountID string
	OutboxID          string
	LeaseToken        string
}

// SavePlatformAccountWithLease atomically creates or confirms a PlatformAccount aggregate,
// and links its internal aggregate ID to the application, strictly fenced by the active outbox lease token.
// Rejects reassignment of different external account IDs and rejects updates on terminal applications.
func (r *Repository) SavePlatformAccountWithLease(
	ctx context.Context,
	params SavePlatformAccountParams,
) (string, error) {
	cleanAppID := strings.TrimSpace(params.ApplicationID)
	cleanExtID := strings.TrimSpace(params.ExternalAccountID)
	cleanOutboxID := strings.TrimSpace(params.OutboxID)
	cleanToken := strings.TrimSpace(params.LeaseToken)

	if cleanAppID == "" || cleanExtID == "" || cleanOutboxID == "" || cleanToken == "" {
		return "", errors.New("applicationID, externalAccountID, outboxID, and leaseToken must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Lease Token Fencing
	leaseCheckSQL := `
		SELECT 1
		FROM onboarding.outbox_messages
		WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()
		FOR UPDATE
	`
	var dummy int
	if err := tx.QueryRow(ctx, leaseCheckSQL, cleanOutboxID, cleanToken).Scan(&dummy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrStaleLeaseToken
		}
		return "", fmt.Errorf("failed to verify outbox lease token: %w", err)
	}

	// 2. Lock application row FOR UPDATE and verify not terminal
	appSelectSQL := `
		SELECT company_id, platform_name, platform_environment, status, platform_account_id
		FROM onboarding.applications
		WHERE id = $1
		FOR UPDATE
	`
	var (
		companyID        string
		platName         string
		platEnv          string
		appStatus        string
		existingLinkedID *string
	)
	err = tx.QueryRow(ctx, appSelectSQL, cleanAppID).Scan(&companyID, &platName, &platEnv, &appStatus, &existingLinkedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("application not found")
		}
		return "", fmt.Errorf("failed to select application: %w", err)
	}

	if appStatus == string(domain.ApplicationStatusRejected) ||
		appStatus == string(domain.ApplicationStatusCompleted) ||
		appStatus == string(domain.ApplicationStatusCancelled) {
		return "", domain.ErrTerminalStatusImmutable
	}

	// 3. Check existing platform account row in onboarding.platform_accounts FOR UPDATE
	paSelectSQL := `
		SELECT id, COALESCE(external_account_id, '')
		FROM onboarding.platform_accounts
		WHERE company_id = $1 AND platform_name = $2 AND environment = $3
		FOR UPDATE
	`
	var internalPAID string
	var existingExtID string
	err = tx.QueryRow(ctx, paSelectSQL, companyID, platName, platEnv).Scan(&internalPAID, &existingExtID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("failed to query existing platform account: %w", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		// Insert brand new PlatformAccount aggregate with distinct internal ID
		internalPAID, err = generateSecureToken()
		if err != nil {
			return "", fmt.Errorf("failed to generate platform account ID: %w", err)
		}
		insertPASQL := `
			INSERT INTO onboarding.platform_accounts (
				id, company_id, platform_name, environment,
				external_account_id, status, last_sync_time,
				revision, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4,
				$5, 'CREATED', now(),
				1, now(), now()
			)
		`
		if _, err := tx.Exec(ctx, insertPASQL, internalPAID, companyID, platName, platEnv, cleanExtID); err != nil {
			return "", fmt.Errorf("failed to insert platform account: %w", err)
		}
	} else {
		// Existing PlatformAccount found
		if existingExtID != "" && existingExtID != cleanExtID {
			// Finding 7: cannot reassign external account or create phantom relationships!
			return "", domain.ErrExternalAccountIDReassignment
		}
		if existingExtID == "" {
			updatePASQL := `
				UPDATE onboarding.platform_accounts
				SET external_account_id = $1,
					status = 'CREATED',
					last_sync_time = now(),
					revision = revision + 1,
					updated_at = now()
				WHERE id = $2
			`
			if _, err := tx.Exec(ctx, updatePASQL, cleanExtID, internalPAID); err != nil {
				return "", fmt.Errorf("failed to update existing platform account external ID: %w", err)
			}
		}
	}

	// 4. Update application with internal PlatformAccount ID
	if existingLinkedID != nil && *existingLinkedID != "" && *existingLinkedID != internalPAID {
		return "", errors.New("application is already linked to a different platform account")
	}

	appUpdateSQL := `
		UPDATE onboarding.applications
		SET platform_account_id = $1,
			updated_at = now()
		WHERE id = $2
	`
	if _, err := tx.Exec(ctx, appUpdateSQL, internalPAID, cleanAppID); err != nil {
		return "", fmt.Errorf("failed to update application platform account link: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit platform account save: %w", err)
	}

	return internalPAID, nil
}

// SavePlatformAccountIDWithLease wraps SavePlatformAccountWithLease for compatibility.
func (r *Repository) SavePlatformAccountIDWithLease(
	ctx context.Context,
	appID string,
	platformAccountID string,
	outboxID string,
	leaseToken string,
) error {
	_, err := r.SavePlatformAccountWithLease(ctx, SavePlatformAccountParams{
		ApplicationID:     appID,
		ExternalAccountID: platformAccountID,
		OutboxID:          outboxID,
		LeaseToken:        leaseToken,
	})
	return err
}

// WorkerOutcomeParams carries parameters for the fenced submission outcome persistence.
type WorkerOutcomeParams struct {
	OutboxID              string
	LeaseToken            string
	ApplicationID         string
	ExpectedAppRevision   int
	SubmissionID          string
	SubmissionStatus      domain.SubmissionStatus // ACCEPTED, FAILED, UNRESOLVED
	DefinitiveInvalidData bool
	ApplicationStatus     domain.ApplicationStatus // UNDER_REVIEW, REJECTED, SUBMISSION_PENDING (hold)
	Decision              *domain.PlatformDecision
	InformationRequests   []domain.InformationRequest
	CompleteJob           bool
}

// loadAndLockApplicationTx locks both the application and live company rows FOR UPDATE,
// loading child submissions, documents, and information requests, and reconstitutes the domain.OnboardingApplication aggregate.
func (r *Repository) loadAndLockApplicationTx(ctx context.Context, tx pgx.Tx, appID string) (*domain.OnboardingApplication, error) {
	appLockSQL := `
		SELECT
			id, company_id, status, platform_name, platform_environment, platform_account_id,
			decision, onboarding_details, company_snapshot, documents, information_requests,
			creator, operator_review_flagged, revision, created_at, updated_at, closed_at
		FROM onboarding.applications
		WHERE id = $1
		FOR UPDATE
	`
	var (
		id                string
		companyID         string
		appStatus         string
		platformName      string
		platformEnv       string
		platformAccountID *string
		decision          *string
		detailsData       []byte
		companyData       []byte
		docsData          []byte
		rfisData          []byte
		creator           string
		flagged           bool
		appRevision       int
		appCreatedAt      time.Time
		appUpdatedAt      time.Time
		closedAt          *time.Time
	)
	err := tx.QueryRow(ctx, appLockSQL, appID).Scan(
		&id,
		&companyID,
		&appStatus,
		&platformName,
		&platformEnv,
		&platformAccountID,
		&decision,
		&detailsData,
		&companyData,
		&docsData,
		&rfisData,
		&creator,
		&flagged,
		&appRevision,
		&appCreatedAt,
		&appUpdatedAt,
		&closedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("failed to lock application: %w", err)
	}

	compLockSQL := `
		SELECT
			id, legal_name, entity_type, registration_country, registration_state,
			registration_identifiers, business_profile, people, status, revision,
			created_at, updated_at
		FROM onboarding.companies
		WHERE id = $1
		FOR UPDATE
	`
	var (
		cID       string
		legalName *string
		entType   *string
		regCtry   *string
		regState  *string
		regIDs    []byte
		bizProf   []byte
		people    []byte
		cStatus   string
		cRev      int
		cCreated  time.Time
		cUpdated  time.Time
	)
	err = tx.QueryRow(ctx, compLockSQL, companyID).Scan(
		&cID,
		&legalName,
		&entType,
		&regCtry,
		&regState,
		&regIDs,
		&bizProf,
		&people,
		&cStatus,
		&cRev,
		&cCreated,
		&cUpdated,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("failed to lock company: %w", err)
	}

	var legalNameStr, entTypeStr, regCtryStr, regStateStr string
	if legalName != nil {
		legalNameStr = *legalName
	}
	if entType != nil {
		entTypeStr = *entType
	}
	if regCtry != nil {
		regCtryStr = *regCtry
	}
	if regState != nil {
		regStateStr = *regState
	}

	var parsedRegIDs []domain.RegistrationIdentifier
	if len(regIDs) > 0 {
		_ = json.Unmarshal(regIDs, &parsedRegIDs)
	}
	var parsedBizProf domain.BusinessProfile
	if len(bizProf) > 0 {
		_ = json.Unmarshal(bizProf, &parsedBizProf)
	}
	var parsedPeople []domain.Person
	if len(people) > 0 {
		_ = json.Unmarshal(people, &parsedPeople)
	}

	persistedCompany, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                      cID,
		LegalName:               legalNameStr,
		EntityType:              entTypeStr,
		RegistrationCountry:     regCtryStr,
		RegistrationState:       regStateStr,
		RegistrationIdentifiers: parsedRegIDs,
		BusinessProfile:         parsedBizProf,
		People:                  parsedPeople,
		Status:                  domain.CompanyStatus(cStatus),
		Revision:                cRev,
		CreatedAt:               cCreated,
		UpdatedAt:               cUpdated,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconstitute live company: %w", err)
	}

	var details domain.PlatformOnboardingDetails
	if len(detailsData) > 0 {
		if err := json.Unmarshal(detailsData, &details); err != nil {
			return nil, fmt.Errorf("failed to unmarshal application onboarding details: %w", err)
		}
	}
	var docs []domain.Document
	if len(docsData) > 0 {
		if err := json.Unmarshal(docsData, &docs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal application documents: %w", err)
		}
	}
	var rfis []domain.InformationRequest
	if len(rfisData) > 0 {
		if err := json.Unmarshal(rfisData, &rfis); err != nil {
			return nil, fmt.Errorf("failed to unmarshal application information requests: %w", err)
		}
	}

	subSQL := `
		SELECT
			id, submission_type, status, definitive_invalid_data, company_copy,
			onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		FROM onboarding.submissions
		WHERE application_id = $1
		ORDER BY created_at ASC
	`
	rows, err := tx.Query(ctx, subSQL, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to query submissions: %w", err)
	}
	defer rows.Close()

	var existingSubmissions []domain.Submission
	for rows.Next() {
		var (
			sID              string
			sType            string
			sStatus          string
			definitiveFailed bool
			sCompData        []byte
			sDetailsData     []byte
			sDocIDsData      []byte
			rfiIDPtr         *string
			answersData      []byte
			actor            string
			sCreatedAt       time.Time
			acceptedAt       *time.Time
		)
		if err := rows.Scan(
			&sID, &sType, &sStatus, &definitiveFailed, &sCompData, &sDetailsData,
			&sDocIDsData, &rfiIDPtr, &answersData, &actor, &sCreatedAt, &acceptedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan submission: %w", err)
		}

		var sComp domain.Company
		if len(sCompData) > 0 {
			_ = json.Unmarshal(sCompData, &sComp)
		}
		var sDetails domain.PlatformOnboardingDetails
		if len(sDetailsData) > 0 {
			_ = json.Unmarshal(sDetailsData, &sDetails)
		}
		var sDocIDs []string
		if len(sDocIDsData) > 0 {
			_ = json.Unmarshal(sDocIDsData, &sDocIDs)
		}
		var sAnswers map[string]string
		if len(answersData) > 0 {
			_ = json.Unmarshal(answersData, &sAnswers)
		}
		var rfiID string
		if rfiIDPtr != nil {
			rfiID = *rfiIDPtr
		}

		existingSubmissions = append(existingSubmissions, domain.Submission{
			ID:                    sID,
			Type:                  domain.SubmissionType(sType),
			Status:                domain.SubmissionStatus(sStatus),
			DefinitiveInvalidData: definitiveFailed,
			CompanyCopy:           &sComp,
			OnboardingDetailsCopy: sDetails,
			DocumentIDs:           sDocIDs,
			InformationRequestID:  rfiID,
			Answers:               sAnswers,
			SubmittingActor:       actor,
			CreatedAt:             sCreatedAt,
			AcceptedAt:            acceptedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error querying submissions: %w", err)
	}

	var appCompany *domain.Company
	if appStatus == string(domain.ApplicationStatusDraft) {
		appCompany = persistedCompany.DeepCopy()
	} else {
		var snap domain.Company
		if len(companyData) > 0 {
			_ = json.Unmarshal(companyData, &snap)
		}
		appCompany = &snap
	}

	var dPlatform *domain.PlatformDecision
	if decision != nil {
		dec := domain.PlatformDecision(*decision)
		dPlatform = &dec
	}

	paid := ""
	if platformAccountID != nil {
		paid = *platformAccountID
	}

	return domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:          id,
		Company:     appCompany,
		LiveCompany: persistedCompany,
		Platform: domain.ApplicationPlatform{
			Name:              platformName,
			Environment:       platformEnv,
			PlatformAccountID: paid,
			Decision:          dPlatform,
			OnboardingDetails: details,
		},
		Status:                domain.ApplicationStatus(appStatus),
		Documents:             docs,
		Submissions:           existingSubmissions,
		InformationRequests:   rfis,
		Creator:               creator,
		CreatedAt:             appCreatedAt,
		UpdatedAt:             appUpdatedAt,
		ClosedAt:              closedAt,
		Revision:              appRevision,
		OperatorReviewFlagged: flagged,
	})
}

// RecordWorkerSubmissionOutcomeWithLease atomically updates the submission and application status,
// fenced by the worker's active outbox lease token and job-to-submission linkage.
// Reconstitutes the latest application aggregate FOR UPDATE and invokes reviewed domain commands
// inside the locked transaction (no caller-supplied SQL status overrides).
func (r *Repository) RecordWorkerSubmissionOutcomeWithLease(
	ctx context.Context,
	params WorkerOutcomeParams,
) error {
	cleanOutboxID := strings.TrimSpace(params.OutboxID)
	cleanToken := strings.TrimSpace(params.LeaseToken)
	cleanAppID := strings.TrimSpace(params.ApplicationID)
	cleanSubID := strings.TrimSpace(params.SubmissionID)

	if cleanOutboxID == "" || cleanToken == "" || cleanAppID == "" || cleanSubID == "" {
		return errors.New("outboxID, leaseToken, applicationID, and submissionID must not be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin outcome transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Lease Token Fencing and Linkage verification (exact application_id AND submission_id)
	linkSQL := `
		SELECT om.payload->>'application_id', s.application_id, COALESCE(om.payload->>'submission_id', '')
		FROM onboarding.outbox_messages om
		JOIN onboarding.submissions s ON s.id = $1
		WHERE om.id = $2
		  AND om.lease_token = $3
		  AND om.status = 'CLAIMED'
		  AND om.leased_until >= now()
		FOR UPDATE OF om
	`
	var payloadAppID, subAppID, payloadSubID string
	err = tx.QueryRow(ctx, linkSQL, cleanSubID, cleanOutboxID, cleanToken).Scan(&payloadAppID, &subAppID, &payloadSubID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var outboxExists int
			chkErr := tx.QueryRow(ctx, `SELECT 1 FROM onboarding.outbox_messages WHERE id = $1 AND lease_token = $2 AND status = 'CLAIMED' AND leased_until >= now()`, cleanOutboxID, cleanToken).Scan(&outboxExists)
			if chkErr == nil {
				return ErrOutboxJobLinkageMismatch
			}
			return ErrStaleLeaseToken
		}
		return fmt.Errorf("failed to verify lease and linkage: %w", err)
	}
	if payloadAppID == "" || payloadAppID != subAppID || subAppID != cleanAppID || payloadSubID != cleanSubID {
		return ErrOutboxJobLinkageMismatch
	}

	// 2. Lock and reconstitute latest application aggregate FOR UPDATE
	app, err := r.loadAndLockApplicationTx(ctx, tx, cleanAppID)
	if err != nil {
		return err
	}

	// Terminal check
	if app.Status() == domain.ApplicationStatusRejected ||
		app.Status() == domain.ApplicationStatusCompleted ||
		app.Status() == domain.ApplicationStatusCancelled {
		return domain.ErrTerminalStatusImmutable
	}

	// CAS revision check
	if params.ExpectedAppRevision > 0 && app.Revision() != params.ExpectedAppRevision {
		return ErrOptimisticLockConflict
	}

	// 3. Invoke reviewed domain command inside the locked transaction
	switch params.SubmissionStatus {
	case domain.SubmissionStatusAccepted:
		if err := app.RecordSubmissionAccepted(cleanSubID); err != nil {
			return fmt.Errorf("domain RecordSubmissionAccepted failed: %w", err)
		}
	case domain.SubmissionStatusFailed:
		if err := app.RecordSubmissionFailed(cleanSubID, params.DefinitiveInvalidData); err != nil {
			return fmt.Errorf("domain RecordSubmissionFailed failed: %w", err)
		}
	case domain.SubmissionStatusUnresolved:
		if err := app.RecordSubmissionUnresolved(cleanSubID); err != nil {
			return fmt.Errorf("domain RecordSubmissionUnresolved failed: %w", err)
		}
	default:
		return fmt.Errorf("unsupported submission outcome status %q", params.SubmissionStatus)
	}

	// 4. Persist updated domain aggregate and target submission
	var targetSub *domain.Submission
	for _, s := range app.Submissions() {
		if s.ID == cleanSubID {
			targetSub = &s
			break
		}
	}
	if targetSub == nil {
		return errors.New("submission not found on application after domain command")
	}

	subSQL := `
		UPDATE onboarding.submissions
		SET status = $1::varchar,
			definitive_invalid_data = $2,
			accepted_at = $3
		WHERE id = $4 AND application_id = $5
	`
	if _, err := tx.Exec(ctx, subSQL,
		string(targetSub.Status),
		targetSub.DefinitiveInvalidData,
		targetSub.AcceptedAt,
		cleanSubID,
		cleanAppID,
	); err != nil {
		return fmt.Errorf("failed to update submission: %w", err)
	}

	var decStr *string
	if app.Platform().Decision != nil {
		s := string(*app.Platform().Decision)
		decStr = &s
	}

	var infoReqBytes []byte
	if app.InformationRequests() != nil {
		infoReqBytes, err = json.Marshal(app.InformationRequests())
		if err != nil {
			return fmt.Errorf("failed to marshal information requests: %w", err)
		}
	}

	appSQL := `
		UPDATE onboarding.applications
		SET status = $1::varchar,
			decision = $2::varchar,
			information_requests = CASE WHEN $3::jsonb IS NOT NULL THEN $3::jsonb ELSE information_requests END,
			revision = $4,
			updated_at = $5,
			closed_at = $6
		WHERE id = $7
	`
	if _, err := tx.Exec(ctx, appSQL,
		string(app.Status()),
		decStr,
		infoReqBytes,
		app.Revision(),
		app.UpdatedAt(),
		app.ClosedAt(),
		cleanAppID,
	); err != nil {
		return fmt.Errorf("failed to update application: %w", err)
	}

	if params.CompleteJob {
		completeSQL := `
			UPDATE onboarding.outbox_messages
			SET status = 'DELIVERED',
				lease_token = NULL,
				processed_at = now(),
				updated_at = now()
			WHERE id = $1 AND lease_token = $2
		`
		if _, err := tx.Exec(ctx, completeSQL, cleanOutboxID, cleanToken); err != nil {
			return fmt.Errorf("failed to complete outbox message: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// --- Cursor Pagination Helpers ---

// Cursor encodes the deterministic pagination state.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

// EncodeCursor produces a URL-safe base64 string from a timestamp and ID.
func EncodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(Cursor{CreatedAt: t.UTC(), ID: id})
	return base64.URLEncoding.EncodeToString(b)
}

// DecodeCursor parses and validates a cursor string.
func DecodeCursor(s string) (*Cursor, error) {
	clean := strings.TrimSpace(s)
	if clean == "" {
		return nil, nil
	}
	b, err := base64.URLEncoding.DecodeString(clean)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, ErrInvalidCursor
	}
	if c.CreatedAt.IsZero() || strings.TrimSpace(c.ID) == "" {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCompanyRow(row rowScanner) (*domain.Company, error) {
	var (
		cID         string
		legalName   *string
		tradingName *string
		entityType  *string
		regCountry  *string
		regState    *string
		status      string
		hasHistory  bool
		profileData []byte
		regIDsData  []byte
		peopleData  []byte
		auditData   []byte
		revision    int
		createdAt   time.Time
		updatedAt   time.Time
	)
	if err := row.Scan(
		&cID,
		&legalName,
		&tradingName,
		&entityType,
		&regCountry,
		&regState,
		&status,
		&hasHistory,
		&profileData,
		&regIDsData,
		&peopleData,
		&auditData,
		&revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	var profile domain.BusinessProfile
	if len(profileData) > 0 {
		if err := json.Unmarshal(profileData, &profile); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company profile: %w", err)
		}
	}
	var regIDs []domain.RegistrationIdentifier
	if len(regIDsData) > 0 {
		if err := json.Unmarshal(regIDsData, &regIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company regIDs: %w", err)
		}
	}
	var people []domain.Person
	if len(peopleData) > 0 {
		if err := json.Unmarshal(peopleData, &people); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company people: %w", err)
		}
	}
	var audit []domain.LegalIdentifierAuditRecord
	if len(auditData) > 0 {
		if err := json.Unmarshal(auditData, &audit); err != nil {
			return nil, fmt.Errorf("failed to unmarshal company audit: %w", err)
		}
	}
	lName := ""
	if legalName != nil {
		lName = *legalName
	}
	tName := ""
	if tradingName != nil {
		tName = *tradingName
	}
	eType := ""
	if entityType != nil {
		eType = *entityType
	}
	rCountry := ""
	if regCountry != nil {
		rCountry = *regCountry
	}
	rState := ""
	if regState != nil {
		rState = *regState
	}

	return domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                      cID,
		Status:                  domain.CompanyStatus(status),
		Revision:                revision,
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
		HasSubmittedHistory:     hasHistory,
		LegalName:               lName,
		TradingName:             tName,
		EntityType:              eType,
		RegistrationCountry:     rCountry,
		RegistrationState:       rState,
		RegistrationIdentifiers: regIDs,
		BusinessProfile:         profile,
		People:                  people,
		AuditTrail:              audit,
	})
}

func scanSubmissionRow(row rowScanner) (*domain.Submission, error) {
	var (
		subID            string
		subType          string
		subStatus        string
		definitiveFailed bool
		subCompData      []byte
		subDetailsData   []byte
		subDocIDsData    []byte
		rfiIDPtr         *string
		answersData      []byte
		actor            string
		subCreatedAt     time.Time
		acceptedAt       *time.Time
	)
	if err := row.Scan(
		&subID,
		&subType,
		&subStatus,
		&definitiveFailed,
		&subCompData,
		&subDetailsData,
		&subDocIDsData,
		&rfiIDPtr,
		&answersData,
		&actor,
		&subCreatedAt,
		&acceptedAt,
	); err != nil {
		return nil, err
	}

	var subComp domain.Company
	if len(subCompData) > 0 {
		if err := json.Unmarshal(subCompData, &subComp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal submission company copy: %w", err)
		}
	}
	var subDetails domain.PlatformOnboardingDetails
	if len(subDetailsData) > 0 {
		if err := json.Unmarshal(subDetailsData, &subDetails); err != nil {
			return nil, fmt.Errorf("failed to unmarshal submission onboarding details copy: %w", err)
		}
	}
	var docIDs []string
	if len(subDocIDsData) > 0 {
		if err := json.Unmarshal(subDocIDsData, &docIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal submission doc IDs: %w", err)
		}
	}
	var answers map[string]string
	if len(answersData) > 0 {
		if err := json.Unmarshal(answersData, &answers); err != nil {
			return nil, fmt.Errorf("failed to unmarshal submission answers: %w", err)
		}
	}
	rfiIDStr := ""
	if rfiIDPtr != nil {
		rfiIDStr = *rfiIDPtr
	}

	return &domain.Submission{
		ID:                    subID,
		Type:                  domain.SubmissionType(subType),
		Status:                domain.SubmissionStatus(subStatus),
		DefinitiveInvalidData: definitiveFailed,
		CompanyCopy:           &subComp,
		OnboardingDetailsCopy: subDetails,
		DocumentIDs:           docIDs,
		InformationRequestID:  rfiIDStr,
		Answers:               answers,
		SubmittingActor:       actor,
		CreatedAt:             subCreatedAt,
		AcceptedAt:            acceptedAt,
	}, nil
}

func scanPlatformAccountRow(row rowScanner) (*domain.PlatformAccount, error) {
	var (
		paID         string
		companyID    string
		platformName string
		env          string
		extID        *string
		status       string
		lastSync     *time.Time
		revision     int
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&paID,
		&companyID,
		&platformName,
		&env,
		&extID,
		&status,
		&lastSync,
		&revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	extAccountID := ""
	if extID != nil {
		extAccountID = *extID
	}

	return domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:                paID,
		CompanyID:         companyID,
		PlatformName:      platformName,
		Environment:       env,
		ExternalAccountID: extAccountID,
		Status:            domain.PlatformAccountStatus(status),
		LastSyncTime:      lastSync,
		Revision:          revision,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	})
}

type scannedAppRow struct {
	appID             string
	companyID         string
	status            string
	platformName      string
	platformEnv       string
	platformAccountID *string
	decision          *string
	detailsData       []byte
	companyData       []byte
	docsData          []byte
	rfisData          []byte
	creator           string
	flagged           bool
	revision          int
	createdAt         time.Time
	updatedAt         time.Time
	closedAt          *time.Time
}

func scanApplicationRowBase(row rowScanner) (scannedAppRow, error) {
	var ar scannedAppRow
	err := row.Scan(
		&ar.appID,
		&ar.companyID,
		&ar.status,
		&ar.platformName,
		&ar.platformEnv,
		&ar.platformAccountID,
		&ar.decision,
		&ar.detailsData,
		&ar.companyData,
		&ar.docsData,
		&ar.rfisData,
		&ar.creator,
		&ar.flagged,
		&ar.revision,
		&ar.createdAt,
		&ar.updatedAt,
		&ar.closedAt,
	)
	return ar, err
}

func reconstituteScannedApplication(ar scannedAppRow, submissions []domain.Submission, liveCompany *domain.Company) (*domain.OnboardingApplication, error) {
	var company domain.Company
	if err := json.Unmarshal(ar.companyData, &company); err != nil {
		return nil, fmt.Errorf("failed to unmarshal company snapshot: %w", err)
	}
	var details domain.PlatformOnboardingDetails
	if len(ar.detailsData) > 0 {
		if err := json.Unmarshal(ar.detailsData, &details); err != nil {
			return nil, fmt.Errorf("failed to unmarshal onboarding details: %w", err)
		}
	}
	var docs []domain.Document
	if len(ar.docsData) > 0 {
		if err := json.Unmarshal(ar.docsData, &docs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal documents: %w", err)
		}
	}
	var rfis []domain.InformationRequest
	if len(ar.rfisData) > 0 {
		if err := json.Unmarshal(ar.rfisData, &rfis); err != nil {
			return nil, fmt.Errorf("failed to unmarshal information requests: %w", err)
		}
	}
	var dPlatform *domain.PlatformDecision
	if ar.decision != nil {
		dec := domain.PlatformDecision(*ar.decision)
		dPlatform = &dec
	}
	paid := ""
	if ar.platformAccountID != nil {
		paid = *ar.platformAccountID
	}

	return domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:          ar.appID,
		Company:     &company,
		LiveCompany: liveCompany,
		Platform: domain.ApplicationPlatform{
			Name:              ar.platformName,
			Environment:       ar.platformEnv,
			PlatformAccountID: paid,
			Decision:          dPlatform,
			OnboardingDetails: details,
		},
		Status:                domain.ApplicationStatus(ar.status),
		Documents:             docs,
		Submissions:           submissions,
		InformationRequests:   rfis,
		Creator:               ar.creator,
		CreatedAt:             ar.createdAt,
		UpdatedAt:             ar.updatedAt,
		ClosedAt:              ar.closedAt,
		Revision:              ar.revision,
		OperatorReviewFlagged: ar.flagged,
	})
}

// DeleteCompany deletes a company if it is eligible for deletion.
// Eligibility rules: company must be in DRAFT status, has_submitted_history must be FALSE,
// and it must have zero applications and zero platform accounts.
func (r *Repository) DeleteCompany(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		hasHistory bool
	)
	err = tx.QueryRow(ctx, `SELECT status, has_submitted_history FROM onboarding.companies WHERE id = $1 FOR UPDATE`, id).Scan(&status, &hasHistory)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCompanyNotFound
		}
		return fmt.Errorf("failed to lock company for deletion: %w", err)
	}

	if status == string(domain.CompanyStatusArchived) {
		return fmt.Errorf("%w: status is ARCHIVED, archived companies cannot be deleted", ErrCompanyDeletionIneligible)
	}
	if hasHistory {
		return fmt.Errorf("%w: company has submitted history", ErrCompanyDeletionIneligible)
	}

	var appCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM onboarding.applications WHERE company_id = $1`, id).Scan(&appCount); err != nil {
		return fmt.Errorf("failed to check existing applications: %w", err)
	}
	if appCount > 0 {
		return fmt.Errorf("%w: company has %d existing application(s)", ErrCompanyDeletionIneligible, appCount)
	}

	var paCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM onboarding.platform_accounts WHERE company_id = $1`, id).Scan(&paCount); err != nil {
		return fmt.Errorf("failed to check existing platform accounts: %w", err)
	}
	if paCount > 0 {
		return fmt.Errorf("%w: company has %d existing platform account(s)", ErrCompanyDeletionIneligible, paCount)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM onboarding.companies WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCompanyNotFound
	}

	return tx.Commit(ctx)
}

// ListCompaniesFilter carries query parameters for listing companies.
type ListCompaniesFilter struct {
	Status *string
	Limit  int
	Cursor string
}

// CompanyListPage contains a paginated list of companies and the next page cursor.
type CompanyListPage struct {
	Companies  []*domain.Company
	NextCursor string
}

// ListCompanies returns a deterministic cursor-paginated list of companies ordered by created_at DESC, id DESC.
func (r *Repository) ListCompanies(ctx context.Context, filter ListCompaniesFilter) (*CompanyListPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	cursor, err := DecodeCursor(filter.Cursor)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			id, legal_name, trading_name, entity_type, registration_country,
			registration_state, status, has_submitted_history, business_profile,
			registration_identifiers, people, audit_trail, revision, created_at, updated_at
		FROM onboarding.companies
		WHERE 1=1
	`
	var args []any
	argIdx := 1

	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, strings.TrimSpace(*filter.Status))
		argIdx++
	}

	if cursor != nil {
		query += fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id < $%d))", argIdx, argIdx, argIdx+1)
		args = append(args, cursor.CreatedAt, cursor.ID)
		argIdx += 2
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d", limit+1)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list companies: %w", err)
	}
	defer rows.Close()

	var companies []*domain.Company
	for rows.Next() {
		c, err := scanCompanyRow(rows)
		if err != nil {
			return nil, err
		}
		companies = append(companies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing companies: %w", err)
	}

	var nextCursor string
	if len(companies) > limit {
		nextCursor = EncodeCursor(companies[limit-1].CreatedAt(), companies[limit-1].ID())
		companies = companies[:limit]
	}

	return &CompanyListPage{
		Companies:  companies,
		NextCursor: nextCursor,
	}, nil
}

// ListApplicationsFilter carries query parameters for listing applications.
type ListApplicationsFilter struct {
	CompanyID    *string
	PlatformName *string
	Status       *string
	Limit        int
	Cursor       string
}

// ApplicationListPage contains a paginated list of applications and the next page cursor.
type ApplicationListPage struct {
	Applications []*domain.OnboardingApplication
	NextCursor   string
}

// ListApplications returns a deterministic cursor-paginated list of applications.
func (r *Repository) ListApplications(ctx context.Context, filter ListApplicationsFilter) (*ApplicationListPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	cursor, err := DecodeCursor(filter.Cursor)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			id, company_id, status, platform_name, platform_environment,
			platform_account_id, decision, onboarding_details, company_snapshot,
			documents, information_requests, creator, operator_review_flagged,
			revision, created_at, updated_at, closed_at
		FROM onboarding.applications
		WHERE 1=1
	`
	var args []any
	argIdx := 1

	if filter.CompanyID != nil && strings.TrimSpace(*filter.CompanyID) != "" {
		query += fmt.Sprintf(" AND company_id = $%d", argIdx)
		args = append(args, strings.TrimSpace(*filter.CompanyID))
		argIdx++
	}
	if filter.PlatformName != nil && strings.TrimSpace(*filter.PlatformName) != "" {
		query += fmt.Sprintf(" AND platform_name = $%d", argIdx)
		args = append(args, strings.TrimSpace(*filter.PlatformName))
		argIdx++
	}
	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, strings.TrimSpace(*filter.Status))
		argIdx++
	}

	if cursor != nil {
		query += fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id < $%d))", argIdx, argIdx, argIdx+1)
		args = append(args, cursor.CreatedAt, cursor.ID)
		argIdx += 2
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d", limit+1)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}
	defer rows.Close()

	var appRows []scannedAppRow
	for rows.Next() {
		ar, err := scanApplicationRowBase(rows)
		if err != nil {
			return nil, err
		}
		appRows = append(appRows, ar)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing applications: %w", err)
	}

	var apps []*domain.OnboardingApplication
	for _, ar := range appRows {
		subs, err := r.ListSubmissions(ctx, ar.appID)
		if err != nil {
			return nil, err
		}
		var liveComp *domain.Company
		if ar.companyID != "" {
			liveComp, _ = r.GetCompany(ctx, ar.companyID)
		}
		app, err := reconstituteScannedApplication(ar, subs, liveComp)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}

	var nextCursor string
	if len(apps) > limit {
		nextCursor = EncodeCursor(apps[limit-1].CreatedAt(), apps[limit-1].ID())
		apps = apps[:limit]
	}

	return &ApplicationListPage{
		Applications: apps,
		NextCursor:   nextCursor,
	}, nil
}

// GetSubmission retrieves a single submission record by ID.
func (r *Repository) GetSubmission(ctx context.Context, id string) (*domain.Submission, error) {
	sql := `
		SELECT
			id, submission_type, status, definitive_invalid_data, company_copy,
			onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		FROM onboarding.submissions
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, sql, strings.TrimSpace(id))
	sub, err := scanSubmissionRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
		return nil, fmt.Errorf("failed to get submission: %w", err)
	}
	return sub, nil
}

// ListSubmissions retrieves all submissions for a given application ID ordered by created_at ASC.
func (r *Repository) ListSubmissions(ctx context.Context, applicationID string) ([]domain.Submission, error) {
	sql := `
		SELECT
			id, submission_type, status, definitive_invalid_data, company_copy,
			onboarding_details_copy, document_ids, information_request_id,
			answers, submitting_actor, created_at, accepted_at
		FROM onboarding.submissions
		WHERE application_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, sql, strings.TrimSpace(applicationID))
	if err != nil {
		return nil, fmt.Errorf("failed to query submissions: %w", err)
	}
	defer rows.Close()

	var subs []domain.Submission
	for rows.Next() {
		sub, err := scanSubmissionRow(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, *sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing submissions: %w", err)
	}
	return subs, nil
}

// ListPlatformAccountsByCompany retrieves all platform accounts associated with a company.
func (r *Repository) ListPlatformAccountsByCompany(ctx context.Context, companyID string) ([]domain.PlatformAccount, error) {
	sql := `
		SELECT
			id, company_id, platform_name, environment, external_account_id,
			status, last_sync_time, revision, created_at, updated_at
		FROM onboarding.platform_accounts
		WHERE company_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, sql, strings.TrimSpace(companyID))
	if err != nil {
		return nil, fmt.Errorf("failed to query platform accounts: %w", err)
	}
	defer rows.Close()

	var pas []domain.PlatformAccount
	for rows.Next() {
		pa, err := scanPlatformAccountRow(rows)
		if err != nil {
			return nil, err
		}
		pas = append(pas, *pa)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing platform accounts: %w", err)
	}
	return pas, nil
}

// WebhookInboxStatus represents processing state for a stored webhook event.
type WebhookInboxStatus string

const (
	WebhookStatusPending     WebhookInboxStatus = "PENDING"
	WebhookStatusProcessing  WebhookInboxStatus = "PROCESSING"
	WebhookStatusProcessed   WebhookInboxStatus = "PROCESSED"
	WebhookStatusIgnored     WebhookInboxStatus = "IGNORED"
	WebhookStatusQuarantined WebhookInboxStatus = "QUARANTINED"
	WebhookStatusFailed      WebhookInboxStatus = "FAILED"
)

// WebhookInboxItem represents an immutable recorded webhook delivery in Go-owned schema.
type WebhookInboxItem struct {
	ID             int64              `json:"id"`
	EventID        string             `json:"event_id"`
	AccountID      string             `json:"account_id"`
	EventType      string             `json:"event_type"`
	Environment    string             `json:"environment"`
	RawPayload     json.RawMessage    `json:"raw_payload"`
	ReceivedAt     time.Time          `json:"received_at"`
	Status         WebhookInboxStatus `json:"status"`
	ProcessedAt    *time.Time         `json:"processed_at,omitempty"`
	LastError      string             `json:"last_error,omitempty"`
	LeaseToken     string             `json:"lease_token,omitempty"`
	LeaseExpiresAt *time.Time         `json:"lease_expires_at,omitempty"`
}

// InsertWebhookOnce stores verified webhook bytes into onboarding.webhook_inbox.
// Returns true if inserted, false if idempotent duplicate.
func (r *Repository) InsertWebhookOnce(ctx context.Context, item WebhookInboxItem) (bool, error) {
	cleanEventID := strings.TrimSpace(item.EventID)
	cleanType := strings.TrimSpace(item.EventType)
	if cleanEventID == "" || cleanType == "" {
		return false, errors.New("event_id and event_type must not be empty")
	}

	rawPayload := item.RawPayload
	if len(rawPayload) == 0 {
		rawPayload = json.RawMessage("{}")
	}

	sql := `
		INSERT INTO onboarding.webhook_inbox (
			event_id, account_id, event_type, environment, raw_payload, status
		) VALUES ($1, $2, $3, $4, $5, 'PENDING')
		ON CONFLICT (event_id) DO NOTHING
		RETURNING id
	`
	var id int64
	err := r.pool.QueryRow(ctx, sql, cleanEventID, strings.TrimSpace(item.AccountID), cleanType, strings.TrimSpace(item.Environment), rawPayload).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // idempotent duplicate
		}
		return false, fmt.Errorf("failed to insert webhook inbox item: %w", err)
	}
	return true, nil
}

// ClaimPendingWebhooks leases candidate pending or failed webhook inbox rows for processing.
func (r *Repository) ClaimPendingWebhooks(ctx context.Context, limit int, leaseDuration time.Duration) ([]WebhookInboxItem, error) {
	if limit <= 0 {
		limit = 10
	}
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}

	leaseToken, err := generateSecureToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate lease token: %w", err)
	}

	sql := `
		WITH candidates AS (
			SELECT id
			FROM onboarding.webhook_inbox
			WHERE (status IN ('PENDING', 'FAILED') AND (lease_expires_at IS NULL OR lease_expires_at < now()))
			   OR (status = 'PROCESSING' AND lease_expires_at IS NOT NULL AND lease_expires_at < now())
			ORDER BY id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE onboarding.webhook_inbox w
		SET status = 'PROCESSING',
		    lease_token = $3,
		    lease_expires_at = now() + $2::interval
		FROM candidates c
		WHERE w.id = c.id
		RETURNING w.id, w.event_id, w.account_id, w.event_type, w.environment,
		          w.raw_payload, w.received_at, w.status, w.processed_at,
		          COALESCE(w.last_error, ''), COALESCE(w.lease_token, ''), w.lease_expires_at
	`
	leaseInterval := fmt.Sprintf("%d milliseconds", leaseDuration.Milliseconds())
	rows, err := r.pool.Query(ctx, sql, limit, leaseInterval, leaseToken)
	if err != nil {
		return nil, fmt.Errorf("failed to claim pending webhooks: %w", err)
	}
	defer rows.Close()

	var items []WebhookInboxItem
	for rows.Next() {
		var item WebhookInboxItem
		var statusStr string
		var scannedToken string
		err := rows.Scan(
			&item.ID,
			&item.EventID,
			&item.AccountID,
			&item.EventType,
			&item.Environment,
			&item.RawPayload,
			&item.ReceivedAt,
			&statusStr,
			&item.ProcessedAt,
			&item.LastError,
			&scannedToken,
			&item.LeaseExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan webhook inbox item: %w", err)
		}
		item.Status = WebhookInboxStatus(statusStr)
		item.LeaseToken = scannedToken
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error claiming pending webhooks: %w", err)
	}
	return items, nil
}

// MarkWebhookProcessed updates status and terminal outcome for an inbox item with strict lease token fencing.
func (r *Repository) MarkWebhookProcessed(ctx context.Context, eventID, leaseToken string, status WebhookInboxStatus, lastError string) error {
	cleanID := strings.TrimSpace(eventID)
	if cleanID == "" {
		return errors.New("eventID must not be empty")
	}

	cleanToken := strings.TrimSpace(leaseToken)
	if cleanToken == "" {
		return ErrStaleLeaseToken
	}

	sql := `
		UPDATE onboarding.webhook_inbox
		SET status = $2,
		    processed_at = now(),
		    last_error = $3,
		    lease_token = NULL,
		    lease_expires_at = NULL
		WHERE event_id = $1
		  AND status = 'PROCESSING'
		  AND lease_token = $4
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at > now()
	`
	tag, err := r.pool.Exec(ctx, sql, cleanID, string(status), lastError, cleanToken)
	if err != nil {
		return fmt.Errorf("failed to update webhook inbox status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleLeaseToken
	}
	return nil
}

// GetApplicationByPlatformAccountID retrieves the application aggregate linked to an external platform account ID or internal PA ID.
func (r *Repository) GetApplicationByPlatformAccountID(ctx context.Context, platformAccountID string) (*domain.OnboardingApplication, error) {
	cleanAccountID := strings.TrimSpace(platformAccountID)
	if cleanAccountID == "" {
		return nil, errors.New("platformAccountID must not be empty")
	}

	var appID string
	err := r.pool.QueryRow(ctx, `
		SELECT a.id
		FROM onboarding.applications a
		LEFT JOIN onboarding.platform_accounts pa ON a.platform_account_id = pa.id
		WHERE a.platform_account_id = $1
		   OR pa.external_account_id = $1
		   OR pa.id = $1
		ORDER BY a.updated_at DESC
		LIMIT 1
	`, cleanAccountID).Scan(&appID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("failed to find application by platform account id: %w", err)
	}
	return r.GetApplication(ctx, appID)
}

// GetPlatformAccountByExternalID queries a platform account by its external provider account ID.
func (r *Repository) GetPlatformAccountByExternalID(ctx context.Context, externalAccountID string) (*domain.PlatformAccount, error) {
	cleanExtID := strings.TrimSpace(externalAccountID)
	if cleanExtID == "" {
		return nil, errors.New("externalAccountID must not be empty")
	}
	var id string
	err := r.pool.QueryRow(ctx, "SELECT id FROM onboarding.platform_accounts WHERE external_account_id = $1 LIMIT 1", cleanExtID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformAccountNotFound
		}
		return nil, fmt.Errorf("failed to query platform account by external id: %w", err)
	}
	return r.GetPlatformAccount(ctx, id)
}

// GetPlatformAccountByExternalIDAndEnv queries a platform account scoped by platform name, environment, and external account ID.
func (r *Repository) GetPlatformAccountByExternalIDAndEnv(ctx context.Context, externalAccountID, platformName, environment string) (*domain.PlatformAccount, error) {
	cleanExtID := strings.TrimSpace(externalAccountID)
	if cleanExtID == "" {
		return nil, errors.New("externalAccountID must not be empty")
	}
	cleanPlat := strings.TrimSpace(platformName)
	if cleanPlat == "" {
		return nil, errors.New("platformName must not be empty")
	}
	cleanEnv := strings.ToLower(strings.TrimSpace(environment))
	if cleanEnv == "" {
		return nil, errors.New("environment must not be empty")
	}

	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT id
		FROM onboarding.platform_accounts
		WHERE platform_name = $1 AND LOWER(environment) = $2 AND external_account_id = $3
		LIMIT 1
	`, cleanPlat, cleanEnv, cleanExtID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformAccountNotFound
		}
		return nil, fmt.Errorf("failed to query platform account by external id, platform, and env: %w", err)
	}
	return r.GetPlatformAccount(ctx, id)
}

// ApplyWebhookObservationParams carries parameters for atomic webhook observation application.
type ApplyWebhookObservationParams struct {
	EventID            string
	LeaseToken         string
	AccountID          string // For inbox linkage check if EventID is provided
	ExternalAccountID  string // Scoped external provider account ID (acct_...)
	InternalAccountID  string // Dedicated internal account ID path (pa_...)
	PlatformName       string // Configured platform (e.g. "AIRWALLEX")
	Environment        string // Configured environment (e.g. "sandbox", "production")
	ExpectedPARevision int    // CAS revision check (> 0 enforces match)
	ObservedStatus     domain.PlatformAccountStatus
	SyncTime           time.Time
	InboxStatus        WebhookInboxStatus
	InboxError         string
}

// ApplyWebhookObservation applies an authoritative status observation atomically across PlatformAccount,
// Application, and WebhookInbox in a single database transaction with consistent lock order and lease fencing.
func (r *Repository) ApplyWebhookObservation(ctx context.Context, params ApplyWebhookObservationParams) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin webhook observation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Lease Token Fencing on webhook_inbox (if inbox event ID provided)
	if params.EventID != "" {
		cleanEventID := strings.TrimSpace(params.EventID)
		cleanToken := strings.TrimSpace(params.LeaseToken)
		if cleanToken == "" {
			return ErrStaleLeaseToken
		}
		expectedAccountID := strings.TrimSpace(params.AccountID)
		if expectedAccountID == "" {
			expectedAccountID = strings.TrimSpace(params.ExternalAccountID)
		}

		inboxSQL := `
			UPDATE onboarding.webhook_inbox
			SET status = $3,
			    processed_at = now(),
			    last_error = $4,
			    lease_token = NULL,
			    lease_expires_at = NULL
			WHERE event_id = $1
			  AND ($5 = '' OR account_id = $5)
			  AND status = 'PROCESSING'
			  AND lease_token = $2
			  AND lease_expires_at IS NOT NULL
			  AND lease_expires_at > now()
		`
		tag, err := tx.Exec(ctx, inboxSQL, cleanEventID, cleanToken, string(params.InboxStatus), params.InboxError, expectedAccountID)
		if err != nil {
			return fmt.Errorf("failed to update webhook inbox with lease: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrStaleLeaseToken
		}
	}

	// 2. Locate PlatformAccount
	var paID, companyID, platformName, platformEnv string
	cleanInternalID := strings.TrimSpace(params.InternalAccountID)
	cleanExtID := strings.TrimSpace(params.ExternalAccountID)
	if cleanExtID == "" && cleanInternalID == "" {
		rawAccID := strings.TrimSpace(params.AccountID)
		if strings.HasPrefix(rawAccID, "pa_") {
			cleanInternalID = rawAccID
		} else {
			cleanExtID = rawAccID
		}
	}

	cleanPlat := strings.TrimSpace(params.PlatformName)
	if cleanPlat == "" {
		cleanPlat = "AIRWALLEX"
	}
	cleanEnv := strings.ToLower(strings.TrimSpace(params.Environment))

	if cleanInternalID != "" {
		// Dedicated internal ID path for reconciliation
		paLookupSQL := `
			SELECT id, company_id, platform_name, environment
			FROM onboarding.platform_accounts
			WHERE id = $1
		`
		err = tx.QueryRow(ctx, paLookupSQL, cleanInternalID).Scan(&paID, &companyID, &platformName, &platformEnv)
	} else if cleanExtID != "" {
		if cleanEnv == "" {
			return errors.New("environment must be provided for external account lookup")
		}
		// Explicit external ID + configured platform/env from authenticated delivery scope
		paLookupSQL := `
			SELECT id, company_id, platform_name, environment
			FROM onboarding.platform_accounts
			WHERE external_account_id = $1 AND platform_name = $2 AND LOWER(environment) = $3
		`
		err = tx.QueryRow(ctx, paLookupSQL, cleanExtID, cleanPlat, cleanEnv).Scan(&paID, &companyID, &platformName, &platformEnv)
	} else {
		return errors.New("either ExternalAccountID or InternalAccountID must be provided")
	}

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// If inbox event was quarantined, commit the inbox update
			if params.EventID != "" && params.InboxStatus == WebhookStatusQuarantined {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return fmt.Errorf("failed to commit quarantined inbox: %w", commitErr)
				}
				return nil
			}
			return ErrPlatformAccountNotFound
		}
		return fmt.Errorf("failed to lookup platform account: %w", err)
	}

	// 3. Locate Eligible Application (if any)
	// ONLY explicitly linked matching account/environment eligible.
	// DRAFT applications are excluded.
	// If no eligible open/linked app exists, PA update proceeds independently.
	var appID string
	appLookupSQL := `
		SELECT id
		FROM onboarding.applications
		WHERE platform_account_id = $1
		  AND platform_name = $2
		  AND LOWER(platform_environment) = LOWER($3)
		  AND status NOT IN ('DRAFT')
		ORDER BY updated_at DESC
		LIMIT 1
	`
	err = tx.QueryRow(ctx, appLookupSQL, paID, platformName, platformEnv).Scan(&appID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to lookup application: %w", err)
	}

	// 4. Strict consistent lock order:
	//    1) Lock Company
	//    2) Lock Application (if eligible)
	//    3) Lock PlatformAccount

	// Lock Company
	var lockedCompID string
	if err := tx.QueryRow(ctx, "SELECT id FROM onboarding.companies WHERE id = $1 FOR UPDATE", companyID).Scan(&lockedCompID); err != nil {
		return fmt.Errorf("failed to lock company: %w", err)
	}

	// Lock Application (if present and eligible)
	var lockedApp *domain.OnboardingApplication
	var initialAppRevision int
	if appID != "" {
		var lockAppErr error
		lockedApp, lockAppErr = r.loadAndLockApplicationTx(ctx, tx, appID)
		if lockAppErr != nil && !errors.Is(lockAppErr, ErrApplicationNotFound) {
			return fmt.Errorf("failed to load and lock application: %w", lockAppErr)
		}
		if lockedApp != nil {
			initialAppRevision = lockedApp.Revision()
		}
	}

	// Lock PlatformAccount
	paLockSQL := `
		SELECT
			id, company_id, platform_name, environment, external_account_id,
			status, last_sync_time, revision, created_at, updated_at
		FROM onboarding.platform_accounts
		WHERE id = $1
		FOR UPDATE
	`
	var (
		paRowID    string
		paCompID   string
		paPlat     string
		paEnv      string
		paExtID    *string
		paStatus   string
		paLastSync *time.Time
		paRev      int
		paCreated  time.Time
		paUpdated  time.Time
	)
	err = tx.QueryRow(ctx, paLockSQL, paID).Scan(
		&paRowID, &paCompID, &paPlat, &paEnv, &paExtID,
		&paStatus, &paLastSync, &paRev, &paCreated, &paUpdated,
	)
	if err != nil {
		return fmt.Errorf("failed to lock platform account: %w", err)
	}

	// CAS check against expected revision if specified
	if params.ExpectedPARevision > 0 && paRev != params.ExpectedPARevision {
		return ErrOptimisticLockConflict
	}

	extIDStr := ""
	if paExtID != nil {
		extIDStr = *paExtID
	}

	pa, err := domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:                paRowID,
		CompanyID:         paCompID,
		PlatformName:      paPlat,
		Environment:       paEnv,
		ExternalAccountID: extIDStr,
		Status:            domain.PlatformAccountStatus(paStatus),
		LastSyncTime:      paLastSync,
		Revision:          paRev,
		CreatedAt:         paCreated,
		UpdatedAt:         paUpdated,
	})
	if err != nil {
		return fmt.Errorf("failed to reconstitute platform account: %w", err)
	}

	syncTime := params.SyncTime
	if syncTime.IsZero() {
		syncTime = time.Now().UTC()
	}

	// 5. Apply PlatformAccount domain commands
	targetStatus := params.ObservedStatus
	var statusErr error
	if pa.Status() == targetStatus {
		statusErr = pa.RecordSync(syncTime)
	} else {
		statusErr = pa.RecordObservedStatus(targetStatus, syncTime)
	}

	if statusErr != nil {
		if errors.Is(statusErr, domain.ErrStaleSyncTime) || errors.Is(statusErr, domain.ErrConflictingSyncStatus) {
			// On stale observation: NO aggregate change, no app transition, 0 revision churn.
			// If inbox event exists, commit the acknowledged/quarantined status in inbox.
			if params.EventID != "" {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return fmt.Errorf("failed to commit stale event ack: %w", commitErr)
				}
			}
			return nil
		}
		return fmt.Errorf("failed to record observed status: %w", statusErr)
	}

	// Persist PlatformAccount with CAS revision check only if changed
	if pa.Revision() != paRev {
		paUpdateSQL := `
			UPDATE onboarding.platform_accounts
			SET status = $1,
			    last_sync_time = $2,
			    next_check_at = now() + interval '5 minutes',
			    revision = $3,
			    updated_at = now()
			WHERE id = $4 AND revision = $5
		`
		tag, err := tx.Exec(ctx, paUpdateSQL, string(pa.Status()), pa.LastSyncTime(), pa.Revision(), pa.ID(), paRev)
		if err != nil {
			return fmt.Errorf("failed to update platform account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrOptimisticLockConflict
		}
	}

	// 6. Update Application (if present and eligible)
	if lockedApp != nil {
		var appChanged bool
		if pa.Status() == domain.PlatformAccountStatusActive {
			if lockedApp.Status() != domain.ApplicationStatusCompleted {
				err := lockedApp.RecordPlatformAccountActivation(pa)
				if err != nil && !errors.Is(err, domain.ErrTerminalStatusImmutable) {
					return fmt.Errorf("failed to record platform account activation: %w", err)
				}
				appChanged = true
			}
		} else if pa.Status() == domain.PlatformAccountStatusSuspended {
			if lockedApp.Status() != domain.ApplicationStatusCompleted {
				err := lockedApp.HandlePlatformSuspension(false)
				if err != nil && !errors.Is(err, domain.ErrTerminalStatusImmutable) {
					return fmt.Errorf("failed to handle platform suspension: %w", err)
				}
				appChanged = true
			}
		}

		if appChanged && lockedApp.Revision() != initialAppRevision {
			var decStr *string
			if lockedApp.Platform().Decision != nil {
				d := string(*lockedApp.Platform().Decision)
				decStr = &d
			}
			appUpdateSQL := `
				UPDATE onboarding.applications
				SET status = $1,
				    platform_account_id = $2,
				    decision = $3,
				    operator_review_flagged = $4,
				    revision = $5,
				    updated_at = now(),
				    closed_at = $6
				WHERE id = $7 AND revision = $8
			`
			tag, err := tx.Exec(ctx, appUpdateSQL,
				string(lockedApp.Status()),
				pa.ID(),
				decStr,
				lockedApp.OperatorReviewFlagged(),
				lockedApp.Revision(),
				lockedApp.ClosedAt(),
				lockedApp.ID(),
				initialAppRevision,
			)
			if err != nil {
				return fmt.Errorf("failed to update application aggregate: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return ErrOptimisticLockConflict
			}
		}
	}

	// 7. Commit transaction atomically
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit webhook observation: %w", err)
	}

	return nil
}

// ListTrackedPlatformAccounts retrieves non-terminal platform accounts ready for periodic reconciliation.
func (r *Repository) ListTrackedPlatformAccounts(ctx context.Context, limit int) ([]domain.PlatformAccount, error) {
	if limit <= 0 {
		limit = 10
	}
	sql := `
		SELECT
			id, company_id, platform_name, environment, external_account_id,
			status, last_sync_time, revision, created_at, updated_at
		FROM onboarding.platform_accounts
		WHERE external_account_id IS NOT NULL AND external_account_id != ''
		  AND status NOT IN ('CLOSED')
		  AND next_check_at <= now()
		ORDER BY next_check_at ASC, id ASC
		LIMIT $1
	`
	rows, err := r.pool.Query(ctx, sql, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked platform accounts: %w", err)
	}
	defer rows.Close()

	var pas []domain.PlatformAccount
	for rows.Next() {
		pa, err := scanPlatformAccountRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tracked platform account: %w", err)
		}
		pas = append(pas, *pa)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error querying tracked platform accounts: %w", err)
	}
	return pas, nil
}

// RecordReconciliationOutcome updates next_check_at for periodic reconciliation scheduling.
func (r *Repository) RecordReconciliationOutcome(ctx context.Context, paID string, nextCheckIn time.Duration) error {
	cleanID := strings.TrimSpace(paID)
	if cleanID == "" {
		return errors.New("paID must not be empty")
	}
	if nextCheckIn <= 0 {
		nextCheckIn = 5 * time.Minute
	}
	intervalStr := fmt.Sprintf("%d milliseconds", nextCheckIn.Milliseconds())
	sql := `
		UPDATE onboarding.platform_accounts
		SET next_check_at = now() + $2::interval
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, sql, cleanID, intervalStr)
	if err != nil {
		return fmt.Errorf("failed to record reconciliation outcome for pa %s: %w", cleanID, err)
	}
	return nil
}
