package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
)

// Server implements HTTP routing, DTO decoding, and controller actions for onboarding.
type Server struct {
	repo           *postgres.Repository
	validator      *validation.Validator
	authenticator  Authenticator
	webhookAuth    Authenticator
	webhookHandler http.Handler
	platformGate   validation.PlatformContractValidator
	storage        storage.ObjectStore
}

// WithWebhookHandler configures a dedicated webhook handler and distinct webhook authenticator.
func (s *Server) WithWebhookHandler(handler http.Handler, auth Authenticator) *Server {
	s.webhookHandler = handler
	s.webhookAuth = auth
	return s
}

// NewServer initializes a new Server with required dependencies.
// If auth is nil, defaults to DenyAllAuthenticator.
// If gate is nil, defaults to UnverifiedPlatformContractGate.
// If store is nil, defaults to storage.NewUnavailableStore.
func NewServer(repo *postgres.Repository, auth Authenticator, gate validation.PlatformContractValidator, store storage.ObjectStore) *Server {
	if auth == nil {
		auth = DenyAllAuthenticator{}
	}
	if gate == nil {
		gate = validation.UnverifiedPlatformContractGate{}
	}
	if store == nil {
		store = storage.NewUnavailableStore("storage not configured")
	}
	return &Server{
		repo:          repo,
		validator:     validation.NewValidator(),
		authenticator: auth,
		platformGate:  gate,
		storage:       store,
	}
}

// Routes constructs the HTTP handler tree, separating public health checks from auth-gated API endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// 1. Public operational health endpoints
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /livez", s.handleHealth)

	// 2. Private/auth-gated internal API endpoints
	apiMux := http.NewServeMux()

	// Company resource endpoints
	apiMux.HandleFunc("POST /api/companies", s.handleCreateCompany)
	apiMux.HandleFunc("GET /api/companies", s.handleListCompanies)
	apiMux.HandleFunc("GET /api/companies/{id}", s.handleGetCompany)
	apiMux.HandleFunc("PATCH /api/companies/{id}", s.handleEditCompany)
	apiMux.HandleFunc("PUT /api/companies/{id}", s.handleEditCompany)
	apiMux.HandleFunc("POST /api/companies/{id}/archive", s.handleArchiveCompany)
	apiMux.HandleFunc("DELETE /api/companies/{id}", s.handleDeleteCompany)
	apiMux.HandleFunc("GET /api/companies/{id}/platform-accounts", s.handleListCompanyPlatformAccounts)

	// Application resource endpoints
	apiMux.HandleFunc("POST /api/onboarding-applications", s.handleCreateApplication)
	apiMux.HandleFunc("GET /api/onboarding-applications", s.handleListApplications)
	apiMux.HandleFunc("GET /api/onboarding-applications/{id}", s.handleGetApplication)
	apiMux.HandleFunc("PATCH /api/onboarding-applications/{id}", s.handleEditApplication)
	apiMux.HandleFunc("POST /api/onboarding-applications/{id}/cancel", s.handleCancelApplication)
	apiMux.HandleFunc("POST /api/onboarding-applications/{id}/verify", s.handleVerifyApplication)
	apiMux.HandleFunc("POST /api/onboarding-applications/{id}/submit", s.handleSubmitApplication)
	apiMux.HandleFunc("GET /api/onboarding-applications/{id}/submissions", s.handleListSubmissions)

	// Document resource endpoints
	apiMux.HandleFunc("POST /api/onboarding-applications/{id}/documents", s.handleUploadDocument)
	apiMux.HandleFunc("GET /api/onboarding-applications/{id}/documents/{doc_id}", s.handleDownloadDocument)
	apiMux.HandleFunc("GET /api/onboarding-applications/{id}/documents", s.handleListDocuments)

	// Submissions and Platform Account reads
	apiMux.HandleFunc("GET /api/submissions/{id}", s.handleGetSubmission)
	apiMux.HandleFunc("GET /api/platform-accounts/{id}", s.handleGetPlatformAccount)

	// Wrap private endpoints in RequireAuth and requireRepo
	mux.Handle("/api/", RequireAuth(s.authenticator)(s.requireRepo(apiMux)))

	// Dedicated webhook endpoints with separate webhook authenticator
	if s.webhookHandler != nil {
		whAuth := s.webhookAuth
		if whAuth == nil {
			whAuth = DenyAllAuthenticator{}
		}
		mux.Handle("POST /api/webhooks/airwallex", RequireAuth(whAuth)(s.requireRepo(s.webhookHandler)))
		mux.Handle("POST /api/v1/webhooks/airwallex", RequireAuth(whAuth)(s.requireRepo(s.webhookHandler)))
	}

	return mux
}

// requireRepo ensures that endpoints requiring database persistence return an explicit
// HTTP 503 Service Unavailable when the repository is unconfigured or unavailable,
// avoiding nil-pointer dereferences.
func (s *Server) requireRepo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.repo == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error":   "DATABASE_UNAVAILABLE",
				"message": "database repository is unconfigured or unavailable",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Helper Functions ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "company-onboarding",
		"status":  "ok",
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	// Enforce 1 MiB body limit to protect against oversized payload attacks
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // Reject unexpected or unmapped fields

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errors.New("request body exceeds maximum allowed size limit of 1 MiB")
		}
		return fmt.Errorf("malformed JSON or unknown fields: %w", err)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain only a single JSON object")
	}

	return nil
}

func safeMalformedInputMessage(err error) string {
	if err == nil {
		return "malformed request payload"
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return "request body exceeds maximum allowed size limit of 1 MiB"
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown field") {
		return "request body contains unmapped or forbidden fields"
	}
	if strings.Contains(msg, "single JSON object") {
		return "request body must contain only a single JSON object"
	}
	return "request body contains malformed JSON or invalid syntax"
}

func generateID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b)), nil
}

// --- Company Handlers ---

func (s *Server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	var req CreateCompanyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	if fieldErrs := s.validator.ValidateStruct(req); len(fieldErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "VALIDATION_FAILED",
			Message: "request validation failed",
			Errors:  fieldErrs,
		})
		return
	}

	compID, err := generateID("comp")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to generate ID"})
		return
	}

	comp, err := domain.NewDraftCompany(compID, req.LegalName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
		return
	}

	if req.TradingName != "" {
		if err := comp.UpdateTradingName(req.TradingName); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if req.BusinessProfile != nil {
		var addr domain.Address
		if req.BusinessProfile.RegisteredAddress != nil {
			addr = domain.Address{
				Line1:      req.BusinessProfile.RegisteredAddress.Line1,
				Line2:      req.BusinessProfile.RegisteredAddress.Line2,
				City:       req.BusinessProfile.RegisteredAddress.City,
				State:      req.BusinessProfile.RegisteredAddress.State,
				PostalCode: req.BusinessProfile.RegisteredAddress.PostalCode,
				Country:    req.BusinessProfile.RegisteredAddress.Country,
			}
		}
		if err := comp.UpdateProfile(domain.BusinessProfile{
			Website:           req.BusinessProfile.Website,
			Description:       req.BusinessProfile.Description,
			RegisteredAddress: addr,
		}); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if req.EntityType != "" || req.RegistrationCountry != "" || req.RegistrationState != "" || len(req.RegistrationIdentifiers) > 0 {
		var regIDs []domain.RegistrationIdentifier
		for _, rid := range req.RegistrationIdentifiers {
			regIDs = append(regIDs, domain.RegistrationIdentifier{
				Type:           rid.Type,
				Value:          rid.Value,
				IssuingCountry: rid.IssuingCountry,
			})
		}
		actor, err := GetActor(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
			return
		}
		if err := comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
			LegalName:               req.LegalName,
			EntityType:              req.EntityType,
			RegistrationCountry:     req.RegistrationCountry,
			RegistrationState:       req.RegistrationState,
			RegistrationIdentifiers: regIDs,
			Actor:                   actor,
			Reason:                  "initial creation",
		}); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	for _, p := range req.People {
		pID := strings.TrimSpace(p.ID)
		if pID == "" {
			pID, _ = generateID("person")
		}
		var idents []domain.Identification
		for _, idn := range p.Identifications {
			idnID := strings.TrimSpace(idn.ID)
			if idnID == "" {
				idnID, _ = generateID("ident")
			}
			idents = append(idents, domain.Identification{
				ID:             idnID,
				Type:           idn.Type,
				Number:         idn.Number,
				IssuingCountry: idn.IssuingCountry,
				IssuedOn:       domain.Date(idn.IssuedOn),
				ExpiresOn:      domain.Date(idn.ExpiresOn),
			})
		}
		if err := comp.AddPerson(domain.Person{
			ID:              pID,
			FirstName:       p.FirstName,
			LastName:        p.LastName,
			Role:            p.Role,
			DateOfBirth:     domain.Date(p.DateOfBirth),
			Identifications: idents,
		}); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if err := s.repo.CreateCompany(r.Context(), comp); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusCreated, toCompanyResponse(comp))
}

func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 20
	if l := q.Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	var statusPtr *string
	if st := strings.TrimSpace(q.Get("status")); st != "" {
		statusPtr = &st
	}

	cursor := q.Get("cursor")

	page, err := s.repo.ListCompanies(r.Context(), postgres.ListCompaniesFilter{
		Status: statusPtr,
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrInvalidCursor) {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_CURSOR", Message: "invalid pagination cursor"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	resp := CompanyListResponse{
		Companies:  make([]CompanyResponse, len(page.Companies)),
		NextCursor: page.NextCursor,
	}
	for i, c := range page.Companies {
		resp.Companies[i] = toCompanyResponse(c)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetCompany(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	comp, err := s.repo.GetCompany(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, toCompanyResponse(comp))
}

func (s *Server) handleEditCompany(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req EditCompanyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	if fieldErrs := s.validator.ValidateStruct(req); len(fieldErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "VALIDATION_FAILED",
			Message: "request validation failed",
			Errors:  fieldErrs,
		})
		return
	}

	comp, err := s.repo.GetCompany(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	if comp.Status() == domain.CompanyStatusArchived {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "COMPANY_ARCHIVED", Message: "archived company cannot be edited"})
		return
	}

	// Partial updates: modify only explicitly provided fields, leaving omitted fields intact
	if req.TradingName != nil {
		if err := comp.UpdateTradingName(*req.TradingName); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if req.BusinessProfile != nil {
		profile := comp.BusinessProfile()
		if req.BusinessProfile.Website != nil {
			profile.Website = *req.BusinessProfile.Website
		}
		if req.BusinessProfile.Description != nil {
			profile.Description = *req.BusinessProfile.Description
		}
		if req.BusinessProfile.RegisteredAddress != nil {
			profile.RegisteredAddress = domain.Address{
				Line1:      req.BusinessProfile.RegisteredAddress.Line1,
				Line2:      req.BusinessProfile.RegisteredAddress.Line2,
				City:       req.BusinessProfile.RegisteredAddress.City,
				State:      req.BusinessProfile.RegisteredAddress.State,
				PostalCode: req.BusinessProfile.RegisteredAddress.PostalCode,
				Country:    req.BusinessProfile.RegisteredAddress.Country,
			}
		}
		if err := comp.UpdateProfile(profile); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if req.LegalName != nil || req.EntityType != nil || req.RegistrationCountry != nil || req.RegistrationState != nil || req.RegistrationIdentifiers != nil {
		lName := comp.LegalName()
		if req.LegalName != nil {
			lName = *req.LegalName
		}
		eType := comp.EntityType()
		if req.EntityType != nil {
			eType = *req.EntityType
		}
		rCountry := comp.RegistrationCountry()
		if req.RegistrationCountry != nil {
			rCountry = *req.RegistrationCountry
		}
		rState := comp.RegistrationState()
		if req.RegistrationState != nil {
			rState = *req.RegistrationState
		}
		regIDs := comp.RegistrationIdentifiers()
		if req.RegistrationIdentifiers != nil {
			regIDs = make([]domain.RegistrationIdentifier, len(*req.RegistrationIdentifiers))
			for i, rid := range *req.RegistrationIdentifiers {
				regIDs[i] = domain.RegistrationIdentifier{
					Type:           rid.Type,
					Value:          rid.Value,
					IssuingCountry: rid.IssuingCountry,
				}
			}
		}

		actor, err := GetActor(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
			return
		}
		reason := req.Reason
		if reason == "" {
			reason = "operator update"
		}

		if err := comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
			LegalName:               lName,
			EntityType:              eType,
			RegistrationCountry:     rCountry,
			RegistrationState:       rState,
			RegistrationIdentifiers: regIDs,
			Actor:                   actor,
			Reason:                  reason,
		}); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if req.People != nil {
		for _, p := range *req.People {
			pID := strings.TrimSpace(p.ID)
			if pID == "" {
				pID, _ = generateID("person")
			}
			var idents []domain.Identification
			for _, idn := range p.Identifications {
				idnID := strings.TrimSpace(idn.ID)
				if idnID == "" {
					idnID, _ = generateID("ident")
				}
				idents = append(idents, domain.Identification{
					ID:             idnID,
					Type:           idn.Type,
					Number:         idn.Number,
					IssuingCountry: idn.IssuingCountry,
					IssuedOn:       domain.Date(idn.IssuedOn),
					ExpiresOn:      domain.Date(idn.ExpiresOn),
				})
			}
			personObj := domain.Person{
				ID:              pID,
				FirstName:       p.FirstName,
				LastName:        p.LastName,
				Role:            p.Role,
				DateOfBirth:     domain.Date(p.DateOfBirth),
				Identifications: idents,
			}
			if _, exists := comp.Person(pID); exists {
				_ = comp.UpdatePerson(personObj)
			} else {
				_ = comp.AddPerson(personObj)
			}
		}
	}

	if err := s.repo.UpdateCompany(r.Context(), comp, req.ExpectedRevision); err != nil {
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OPTIMISTIC_LOCK_CONFLICT", Message: "company revision mismatch; record updated concurrently"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, toCompanyResponse(comp))
}

func (s *Server) handleArchiveCompany(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ArchiveCompanyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	if err := s.repo.ArchiveCompany(r.Context(), id); err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		if errors.Is(err, postgres.ErrCannotArchiveWithOpenApplications) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "CANNOT_ARCHIVE_WITH_OPEN_APPLICATIONS", Message: "company has open onboarding applications and cannot be archived"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, ActionResponse{Code: "COMPANY_ARCHIVED", Message: "company archived successfully"})
}

func (s *Server) handleDeleteCompany(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.repo.DeleteCompany(r.Context(), id); err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		if errors.Is(err, postgres.ErrCompanyDeletionIneligible) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "DELETION_INELIGIBLE", Message: "company has entered platform onboarding and is ineligible for deletion"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListCompanyPlatformAccounts(w http.ResponseWriter, r *http.Request) {
	compID := r.PathValue("id")
	if _, err := s.repo.GetCompany(r.Context(), compID); err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	pas, err := s.repo.ListPlatformAccountsByCompany(r.Context(), compID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	resp := make([]PlatformAccountResponse, len(pas))
	for i, pa := range pas {
		resp[i] = toPlatformAccountResponse(&pa)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Application Handlers ---

func (s *Server) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	var req CreateApplicationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	if fieldErrs := s.validator.ValidateStruct(req); len(fieldErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "VALIDATION_FAILED",
			Message: "request validation failed",
			Errors:  fieldErrs,
		})
		return
	}

	comp, err := s.repo.GetCompany(r.Context(), req.CompanyID)
	if err != nil {
		if errors.Is(err, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	if comp.Status() == domain.CompanyStatusArchived {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "COMPANY_ARCHIVED", Message: "cannot create application on archived company"})
		return
	}

	appID, err := generateID("app")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to generate ID"})
		return
	}

	actor, err := GetActor(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
		return
	}

	app, err := domain.NewDraftApplication(appID, comp, actor)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
		return
	}

	if req.OnboardingDetails != nil {
		details := convertDetailsDTO(*req.OnboardingDetails)
		if err := app.UpdateOnboardingDetails(details); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
			return
		}
	}

	if err := s.repo.CreateApplication(r.Context(), app); err != nil {
		if errors.Is(err, postgres.ErrOpenApplicationExists) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OPEN_APPLICATION_EXISTS", Message: "an open application already exists for this company and platform"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusCreated, toApplicationResponse(app, comp))
}

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 20
	if l := q.Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	var compIDPtr, platPtr, statusPtr *string
	if c := strings.TrimSpace(q.Get("company_id")); c != "" {
		compIDPtr = &c
	}
	if p := strings.TrimSpace(q.Get("platform")); p != "" {
		platPtr = &p
	}
	if st := strings.TrimSpace(q.Get("status")); st != "" {
		statusPtr = &st
	}

	cursor := q.Get("cursor")

	page, err := s.repo.ListApplications(r.Context(), postgres.ListApplicationsFilter{
		CompanyID:    compIDPtr,
		PlatformName: platPtr,
		Status:       statusPtr,
		Limit:        limit,
		Cursor:       cursor,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrInvalidCursor) {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_CURSOR", Message: "invalid pagination cursor"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	resp := ApplicationListResponse{
		Applications: make([]ApplicationResponse, len(page.Applications)),
		NextCursor:   page.NextCursor,
	}
	for i, app := range page.Applications {
		var companyToReturn *domain.Company
		if app.Status() == domain.ApplicationStatusDraft {
			if liveComp, err := s.repo.GetCompany(r.Context(), app.Company().ID()); err == nil {
				companyToReturn = liveComp
			} else {
				companyToReturn = app.Company()
			}
		} else {
			companyToReturn = app.Company()
		}
		resp.Applications[i] = toApplicationResponse(app, companyToReturn)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	app, err := s.repo.GetApplication(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	var companyToReturn *domain.Company
	if app.Status() == domain.ApplicationStatusDraft {
		// DRAFT application GET resolves live current Company
		if liveComp, err := s.repo.GetCompany(r.Context(), app.Company().ID()); err == nil {
			companyToReturn = liveComp
		} else {
			companyToReturn = app.Company()
		}
	} else {
		// Submitted application returns frozen copy
		companyToReturn = app.Company()
	}

	writeJSON(w, http.StatusOK, toApplicationResponse(app, companyToReturn))
}

func (s *Server) handleEditApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req EditApplicationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	if fieldErrs := s.validator.ValidateStruct(req); len(fieldErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "VALIDATION_FAILED",
			Message: "request validation failed",
			Errors:  fieldErrs,
		})
		return
	}

	app, err := s.repo.GetApplication(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	details := convertDetailsDTO(req.OnboardingDetails)
	if err := app.UpdateOnboardingDetails(details); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
		return
	}

	if err := s.repo.UpdateApplication(r.Context(), app, req.ExpectedRevision); err != nil {
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OPTIMISTIC_LOCK_CONFLICT", Message: "application revision mismatch; record updated concurrently"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, toApplicationResponse(app, app.Company()))
}

func (s *Server) handleCancelApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req CancelApplicationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: safeMalformedInputMessage(err)})
		return
	}

	app, err := s.repo.GetApplication(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	actor, err := GetActor(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
		return
	}
	if err := app.Cancel(actor); err != nil {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "CANCELLATION_NOT_PERMITTED", Message: "cancellation is permitted only before submission"})
		return
	}

	if err := s.repo.UpdateApplication(r.Context(), app, req.ExpectedRevision); err != nil {
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OPTIMISTIC_LOCK_CONFLICT", Message: "application revision mismatch; record updated concurrently"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, ActionResponse{Code: "APPLICATION_CANCELLED", Message: "application cancelled successfully"})
}

func (s *Server) handleVerifyApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	app, err := s.repo.GetApplication(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	var comp *domain.Company
	if app.Status() == domain.ApplicationStatusDraft {
		var compErr error
		comp, compErr = s.repo.GetCompany(r.Context(), app.Company().ID())
		if compErr != nil {
			if errors.Is(compErr, postgres.ErrCompanyNotFound) {
				writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
			return
		}
	} else {
		comp = app.Company()
	}

	result := validation.VerifyOnboardingReadiness(comp, app, s.platformGate)
	if result.Code != "VALIDATION_PASSED" {
		writeJSON(w, http.StatusUnprocessableEntity, result)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSubmitApplication(w http.ResponseWriter, r *http.Request) {
	// Task 6 explicit constraint: submit API action contract remains blocked pending
	// explicit resolution of deferred reviewed revisions policy. No silent "latest" semantics.
	writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
		Code:    "SUBMISSION_CONTRACT_BLOCKED",
		Message: "submit API action is blocked pending explicit resolution of deferred reviewed revisions policy; fail-closed on unapproved submission contract",
	})
}

func (s *Server) handleListSubmissions(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, err := s.repo.GetApplication(r.Context(), appID); err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	subs, err := s.repo.ListSubmissions(r.Context(), appID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	resp := make([]SubmissionResponse, len(subs))
	for i, sub := range subs {
		resp[i] = toSubmissionResponse(sub)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetSubmission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sub, err := s.repo.GetSubmission(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrSubmissionNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "submission not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, toSubmissionResponse(*sub))
}

func (s *Server) handleGetPlatformAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pa, err := s.repo.GetPlatformAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrPlatformAccountNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "platform account not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	writeJSON(w, http.StatusOK, toPlatformAccountResponse(pa))
}

func convertDetailsDTO(dto PlatformOnboardingDetailsDTO) domain.PlatformOnboardingDetails {
	var people []domain.PersonPlatformDetails
	for _, pd := range dto.People {
		var resAddr domain.Address
		if pd.ResidentialAddress != nil {
			resAddr = domain.Address{
				Line1:      pd.ResidentialAddress.Line1,
				Line2:      pd.ResidentialAddress.Line2,
				City:       pd.ResidentialAddress.City,
				State:      pd.ResidentialAddress.State,
				PostalCode: pd.ResidentialAddress.PostalCode,
				Country:    pd.ResidentialAddress.Country,
			}
		}
		people = append(people, domain.PersonPlatformDetails{
			PersonID:           pd.PersonID,
			PlatformRoles:      pd.PlatformRoles,
			ResidentialAddress: resAddr,
		})
	}

	var terms *domain.TermsAndConditionsAgreement
	if dto.CustomerAgreements.TermsAndConditions != nil {
		var devData *domain.DeviceData
		if dto.CustomerAgreements.TermsAndConditions.DeviceData != nil {
			devData = &domain.DeviceData{
				IPAddress: dto.CustomerAgreements.TermsAndConditions.DeviceData.IPAddress,
				UserAgent: dto.CustomerAgreements.TermsAndConditions.DeviceData.UserAgent,
			}
		}
		terms = &domain.TermsAndConditionsAgreement{
			AgreedAt:             dto.CustomerAgreements.TermsAndConditions.AgreedAt,
			ServiceAgreementType: dto.CustomerAgreements.TermsAndConditions.ServiceAgreementType,
			DeviceData:           devData,
		}
	}

	return domain.PlatformOnboardingDetails{
		IndustryCategoryCode:   dto.IndustryCategoryCode,
		IntendedProducts:       dto.IntendedProducts,
		EstimatedRevenue:       domain.RevenueEstimate(dto.EstimatedRevenue),
		PrimaryContactPersonID: dto.PrimaryContactPersonID,
		People:                 people,
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToTermsAndConditions: dto.CustomerAgreements.AgreedToTermsAndConditions,
			AgreedToDataUsage:          dto.CustomerAgreements.AgreedToDataUsage,
			TermsAndConditions:         terms,
		},
	}
}
