package apidocs

import (
	"net/http"
	"reflect"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

// ParameterDefinition models an HTTP parameter in query, path, or header.
type ParameterDefinition struct {
	Name        string
	In          string // "query", "path", "header"
	Required    bool
	Description string
	Schema      *openapi3.Schema
	Example     any
}

// ResponseDefinition models an HTTP status response variant.
type ResponseDefinition struct {
	Description   string
	Type          reflect.Type
	IsRawArray    bool
	IsBinary      bool
	ContentTypes  []string
	Headers       map[string]string
	Example       any
	SchemaBuilder func() *openapi3.SchemaRef
}

// RequestBodyDefinition models an HTTP request payload.
type RequestBodyDefinition struct {
	Description     string
	Required        bool
	ContentType     string
	Type            reflect.Type
	IsMultipart     bool
	MultipartFields map[string]MultipartFieldDefinition
	Example         any
}

// MultipartFieldDefinition defines a form field in a multipart upload.
type MultipartFieldDefinition struct {
	Type        string
	Format      string
	Description string
	Required    bool
}

// OperationDefinition captures the complete operational contract for an endpoint.
type OperationDefinition struct {
	ID          string
	Audience    string // "operator", "public", or "internal-webhook"
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	Parameters  []ParameterDefinition
	RequestBody *RequestBodyDefinition
	Responses   map[int]ResponseDefinition
	Security    []map[string][]string
}

// AllOperations returns the authoritative registry of all 25 implemented routes.
func AllOperations() []OperationDefinition {
	operatorSecurity := []map[string][]string{
		{"GatewaySessionCookie": {}},
		{"ServiceJWT": {}},
	}
	webhookSecurity := []map[string][]string{
		{"WebhookServiceJWT": {}},
	}

	return []OperationDefinition{
		// 1. Public Health Endpoints (2 routes)
		{
			ID:          "getHealthz",
			Audience:    "public",
			Method:      http.MethodGet,
			Path:        "/healthz",
			Summary:     "Liveness probe",
			Description: "Public operational health check. Returns service name and status without requiring authentication.",
			Tags:        []string{"Health"},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Service is alive and healthy",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"service": "company-onboarding", "status": "ok"},
				},
			},
			Security: nil,
		},
		{
			ID:          "getLivez",
			Audience:    "public",
			Method:      http.MethodGet,
			Path:        "/livez",
			Summary:     "Health probe alias",
			Description: "Public operational health check alias. Identical behavior to /healthz.",
			Tags:        []string{"Health"},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Service is alive and healthy",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"service": "company-onboarding", "status": "ok"},
				},
			},
			Security: nil,
		},

		// 2. Company Resource Endpoints (8 routes)
		{
			ID:          "createCompany",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/companies",
			Summary:     "Create company",
			Description: "Registers a new business entity with initial status ACTIVE and revision 1. Legal name is required (min 2, max 200 characters). Accepts optional business profile, registration identifiers, and people.",
			Tags:        []string{"Companies"},
			RequestBody: &RequestBodyDefinition{
				Description: "Company registration payload",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.CreateCompanyRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusCreated: {
					Description:  "Company registered successfully",
					Type:         reflect.TypeOf(internalHttp.CompanyResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed JSON input",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid operator session / JWT",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Field validation failed",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured or unreachable",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "listCompanies",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/companies",
			Summary:     "List companies",
			Description: "Retrieves a paginated list of companies wrapped in a pagination envelope with next_cursor. Supports status, limit, and cursor filters.",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "limit",
					In:          "query",
					Description: "Maximum records to return (default 20, positive integer)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"integer"}, Default: 20},
				},
				{
					Name:        "status",
					In:          "query",
					Description: "Filter companies by lifecycle status (ACTIVE, ARCHIVED)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}, Enum: []any{string(domain.CompanyStatusActive), string(domain.CompanyStatusArchived)}},
				},
				{
					Name:        "cursor",
					In:          "query",
					Description: "Opaque pagination cursor from previous response",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Paginated company list envelope",
					Type:         reflect.TypeOf(internalHttp.CompanyListResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Invalid pagination cursor",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "getCompany",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/companies/{id}",
			Summary:     "Get company by ID",
			Description: "Retrieves a single business entity by its opaque prefixed ID (e.g. comp_...).",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company opaque identifier (e.g. comp_...)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Company details",
					Type:         reflect.TypeOf(internalHttp.CompanyResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "editCompany",
			Audience:    "operator",
			Method:      http.MethodPatch,
			Path:        "/api/companies/{id}",
			Summary:     "Update company details (PATCH)",
			Description: "Applies partial updates to company fields. Requires expected_revision matching current revision for optimistic concurrency control (CAS). Correcting registration identifiers on companies with submitted application history requires explicit actor and reason.",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "Partial company update fields and CAS expected_revision",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.EditCompanyRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Updated company details",
					Type:         reflect.TypeOf(internalHttp.CompanyResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed JSON or domain invariant failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Optimistic concurrency conflict (revision mismatch)",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Validation failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "editCompanyPut",
			Audience:    "operator",
			Method:      http.MethodPut,
			Path:        "/api/companies/{id}",
			Summary:     "Update company details (PUT alias)",
			Description: "PUT alias for PATCH /api/companies/{id}; preserves identical partial update and CAS revision semantics.",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "Partial company update fields and CAS expected_revision",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.EditCompanyRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Updated company details",
					Type:         reflect.TypeOf(internalHttp.CompanyResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed JSON or domain invariant failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Optimistic concurrency conflict",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Validation failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "archiveCompany",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/companies/{id}/archive",
			Summary:     "Archive company",
			Description: "Transitions company status to ARCHIVED. Requires expected_revision matching current revision. Rejected with 409 Conflict if open onboarding applications exist.",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "CAS expected_revision and optional archive reason",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.ArchiveCompanyRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Company successfully archived",
					Type:         reflect.TypeOf(internalHttp.ActionResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed JSON",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Revision conflict or company has open applications",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "deleteCompany",
			Audience:    "operator",
			Method:      http.MethodDelete,
			Path:        "/api/companies/{id}",
			Summary:     "Delete draft company",
			Description: "Permanently deletes a draft company with zero platform activity or historical submissions. Returns ActionResponse with code COMPANY_DELETED (not 204 No Content).",
			Tags:        []string{"Companies"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Company permanently deleted",
					Type:         reflect.TypeOf(internalHttp.ActionResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Company has historical submissions and cannot be deleted",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "listCompanyPlatformAccounts",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/companies/{id}/platform-accounts",
			Summary:     "List company platform accounts",
			Description: "Retrieves all platform accounts connected to the company. NOTE: Returns a raw JSON array of PlatformAccountResponse items (not wrapped in a pagination envelope).",
			Tags:        []string{"Companies", "Platform Accounts"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Company ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Raw JSON array of platform accounts",
					Type:         reflect.TypeOf([]internalHttp.PlatformAccountResponse{}),
					IsRawArray:   true,
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},

		// 3. Application Resource Endpoints (8 routes)
		{
			ID:          "createApplication",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/onboarding-applications",
			Summary:     "Create onboarding application",
			Description: "Creates an onboarding application for a company and target platform. Fails with 409 Conflict if an active, non-terminal application already exists for the company and platform.",
			Tags:        []string{"Onboarding Applications"},
			RequestBody: &RequestBodyDefinition{
				Description: "Application creation parameters",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.CreateApplicationRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusCreated: {
					Description:  "Application created successfully",
					Type:         reflect.TypeOf(internalHttp.ApplicationResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed request payload",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Referenced company not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "An active application already exists for this company and platform",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Validation failed",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "listApplications",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/onboarding-applications",
			Summary:     "List onboarding applications",
			Description: "Retrieves a paginated list of onboarding applications. Supports filtering by company_id, platform, status, limit, and cursor.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "company_id",
					In:          "query",
					Description: "Filter applications by company opaque identifier",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
				{
					Name:        "platform",
					In:          "query",
					Description: "Filter by platform name (e.g. airwallex)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
				{
					Name:        "status",
					In:          "query",
					Description: "Filter by application status (DRAFT, SUBMISSION_PENDING, UNDER_REVIEW, INFORMATION_REQUIRED, COMPLETED, REJECTED, CANCELLED)",
					Schema: &openapi3.Schema{
						Type: &openapi3.Types{"string"},
						Enum: []any{
							string(domain.ApplicationStatusDraft),
							string(domain.ApplicationStatusSubmissionPending),
							string(domain.ApplicationStatusUnderReview),
							string(domain.ApplicationStatusInformationRequired),
							string(domain.ApplicationStatusCompleted),
							string(domain.ApplicationStatusRejected),
							string(domain.ApplicationStatusCancelled),
						},
					},
				},
				{
					Name:        "limit",
					In:          "query",
					Description: "Maximum records to return (default 20)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"integer"}, Default: 20},
				},
				{
					Name:        "cursor",
					In:          "query",
					Description: "Pagination cursor token",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Paginated applications list envelope",
					Type:         reflect.TypeOf(internalHttp.ApplicationListResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Invalid cursor",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "getApplication",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/onboarding-applications/{id}",
			Summary:     "Get onboarding application by ID",
			Description: "Retrieves an onboarding application aggregate including embedded company snapshot, documents, and information requests.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application opaque identifier (e.g. app_...)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Application details",
					Type:         reflect.TypeOf(internalHttp.ApplicationResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "editApplication",
			Audience:    "operator",
			Method:      http.MethodPatch,
			Path:        "/api/onboarding-applications/{id}",
			Summary:     "Update onboarding details",
			Description: "Updates platform-specific onboarding details (e.g. industry category, revenue estimates, person residential addresses). Requires expected_revision CAS.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "Updated platform details and CAS expected_revision",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.EditApplicationRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Updated application details",
					Type:         reflect.TypeOf(internalHttp.ApplicationResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed request payload",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Revision conflict or application is not editable",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Validation failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "cancelApplication",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/onboarding-applications/{id}/cancel",
			Summary:     "Cancel onboarding application",
			Description: "Transitions an application to CANCELLED status prior to platform submission. Requires expected_revision matching current revision.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "CAS expected_revision and cancellation reason",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.CancelApplicationRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Application cancelled",
					Type:         reflect.TypeOf(internalHttp.ActionResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed request payload",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Revision conflict or application already submitted/terminal",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "verifyApplication",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/onboarding-applications/{id}/verify",
			Summary:     "Verify application readiness",
			Description: "Validates saved application data for completeness and platform contract readiness. Returns HTTP 200 with code VALIDATION_PASSED when all gates pass. Returns HTTP 422 with the identical ValidationResult schema containing field errors when requirements are missing or unverified.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Application verified ready for submission",
					Type:         reflect.TypeOf(validation.ValidationResult{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Verification failed with missing or invalid fields",
					Type:         reflect.TypeOf(validation.ValidationResult{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "submitApplication",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/onboarding-applications/{id}/submit",
			Summary:     "Submit application (BLOCKED)",
			Description: "CRITICAL CONTRACT STATUS: This action is explicitly blocked fail-closed, returning HTTP 422 with code SUBMISSION_CONTRACT_BLOCKED pending resolution of deferred reviewed revisions policy. Platform submission contracts remain unexecuted.",
			Tags:        []string{"Onboarding Applications"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "CAS expected_revision",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(internalHttp.SubmitApplicationRequest{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "Contract blocked: SUBMISSION_CONTRACT_BLOCKED",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "listSubmissions",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/onboarding-applications/{id}/submissions",
			Summary:     "List application submissions",
			Description: "Retrieves immutable historical submission records attached to this application. NOTE: Returns a raw JSON array of SubmissionResponse items (not wrapped in a pagination envelope).",
			Tags:        []string{"Onboarding Applications", "Submissions"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Raw JSON array of submissions",
					Type:         reflect.TypeOf([]internalHttp.SubmissionResponse{}),
					IsRawArray:   true,
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},

		// 4. Document Endpoints (3 routes)
		{
			ID:          "uploadDocument",
			Audience:    "operator",
			Method:      http.MethodPost,
			Path:        "/api/onboarding-applications/{id}/documents",
			Summary:     "Upload application document",
			Description: "Uploads a document via multipart/form-data. Maximum size is 10 MiB. Content is sniffed for verified PDF, JPEG, or PNG magic bytes; client-declared MIME spoofing is rejected. Requires expected_revision matching current application revision.",
			Tags:        []string{"Documents"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			RequestBody: &RequestBodyDefinition{
				Description: "Multipart file and metadata form fields",
				Required:    true,
				ContentType: "multipart/form-data",
				IsMultipart: true,
				MultipartFields: map[string]MultipartFieldDefinition{
					"file":              {Type: "string", Format: "binary", Description: "Document binary stream (PDF, JPEG, or PNG, max 10 MiB)", Required: true},
					"type":              {Type: "string", Description: "Document category type", Required: true},
					"person_id":         {Type: "string", Description: "Optional person ID reference for identity documents", Required: false},
					"identification_id": {Type: "string", Description: "Optional identification ID reference", Required: false},
					"expected_revision": {Type: "integer", Description: "CAS expected application revision (positive integer)", Required: true},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusCreated: {
					Description:  "Document uploaded and persisted",
					Type:         reflect.TypeOf(internalHttp.DocumentResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Malformed multipart data or missing file",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application or referenced entity not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusConflict: {
					Description:  "Revision conflict or application is terminal",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnprocessableEntity: {
					Description:  "File exceeds 10 MiB, unsupported MIME, or spoofed content-type",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Storage or persistence failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "listDocuments",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/onboarding-applications/{id}/documents",
			Summary:     "List application documents",
			Description: "Retrieves document metadata attached to an onboarding application. NOTE: Returns a raw JSON array of DocumentResponse items (not wrapped in an envelope).",
			Tags:        []string{"Documents"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Raw JSON array of document metadata",
					Type:         reflect.TypeOf([]internalHttp.DocumentResponse{}),
					IsRawArray:   true,
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "downloadDocument",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/onboarding-applications/{id}/documents/{doc_id}",
			Summary:     "Download document binary",
			Description: "Streams private decrypted document bytes directly to the client. Responses include Content-Disposition: attachment, sniffed Content-Type (application/pdf, image/jpeg, or image/png), and X-Content-Type-Options: nosniff.",
			Tags:        []string{"Documents"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Application ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
				{
					Name:        "doc_id",
					In:          "path",
					Required:    true,
					Description: "Document ID",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Document binary stream",
					IsBinary:     true,
					ContentTypes: []string{"application/pdf", "image/jpeg", "image/png"},
					Headers: map[string]string{
						"Content-Disposition":    `attachment; filename="..."`,
						"X-Content-Type-Options": "nosniff",
					},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Document or application not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Internal storage failure",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Object storage unconfigured or unreachable",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},

		// 5. Global Reads (2 routes)
		{
			ID:          "getSubmission",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/submissions/{id}",
			Summary:     "Get submission by ID",
			Description: "Reads an immutable frozen submission aggregate by ID (e.g. sub_...).",
			Tags:        []string{"Submissions"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Submission opaque identifier (e.g. sub_...)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Submission record",
					Type:         reflect.TypeOf(internalHttp.SubmissionResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Submission not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},
		{
			ID:          "getPlatformAccount",
			Audience:    "operator",
			Method:      http.MethodGet,
			Path:        "/api/platform-accounts/{id}",
			Summary:     "Get platform account by ID",
			Description: "Reads a connected platform account aggregate by ID (e.g. pa_...).",
			Tags:        []string{"Platform Accounts"},
			Parameters: []ParameterDefinition{
				{
					Name:        "id",
					In:          "path",
					Required:    true,
					Description: "Platform account opaque identifier (e.g. pa_...)",
					Schema:      &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Platform account record",
					Type:         reflect.TypeOf(internalHttp.PlatformAccountResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid credentials",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusNotFound: {
					Description:  "Platform account not found",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusInternalServerError: {
					Description:  "Persistence error",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: operatorSecurity,
		},

		// 6. Internal Webhook Endpoints (2 routes)
		{
			ID:          "ingestAirwallexWebhook",
			Audience:    "internal-webhook",
			Method:      http.MethodPost,
			Path:        "/api/webhooks/airwallex",
			Summary:     "Ingest Airwallex platform webhook",
			Description: "Internal receiver endpoint for Airwallex webhook events forwarded by the Node gateway proxy. Requires service-to-service JWT assertion with audience flowcard-onboarding-webhooks. Events are durably persisted to PostgreSQL before acknowledging with HTTP 200. Max body size 1 MiB. Errors return map[string]string{\"error\": \"...\"}.",
			Tags:        []string{"Webhooks"},
			RequestBody: &RequestBodyDefinition{
				Description: "Inbound webhook event delivery payload",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(webhooks.InboundWebhookPayload{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Durable acknowledgement receipt",
					Type:         reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Missing event ID, name, or invalid JSON",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "invalid json payload"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid webhook forwarder JWT assertion",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusMethodNotAllowed: {
					Description:  "Method not allowed (only POST accepted)",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Headers:      map[string]string{"Allow": "POST"},
				},
				http.StatusRequestEntityTooLarge: {
					Description:  "Webhook payload exceeds 1 MiB limit",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "payload too large"},
				},
				http.StatusInternalServerError: {
					Description:  "Failed to durably commit webhook to inbox",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "failed to persist webhook"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: webhookSecurity,
		},
		{
			ID:          "ingestAirwallexWebhookV1",
			Audience:    "internal-webhook",
			Method:      http.MethodPost,
			Path:        "/api/v1/webhooks/airwallex",
			Summary:     "Ingest Airwallex webhook (v1 alias)",
			Description: "Path alias for /api/webhooks/airwallex with identical authentication, durability, and error semantics.",
			Tags:        []string{"Webhooks"},
			RequestBody: &RequestBodyDefinition{
				Description: "Inbound webhook event delivery payload",
				Required:    true,
				ContentType: "application/json",
				Type:        reflect.TypeOf(webhooks.InboundWebhookPayload{}),
			},
			Responses: map[int]ResponseDefinition{
				http.StatusOK: {
					Description:  "Durable acknowledgement receipt",
					Type:         reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusBadRequest: {
					Description:  "Missing event ID, name, or invalid JSON",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "invalid json payload"},
				},
				http.StatusUnauthorized: {
					Description:  "Missing or invalid webhook forwarder JWT assertion",
					Type:         reflect.TypeOf(map[string]any{}),
					ContentTypes: []string{"application/json"},
				},
				http.StatusMethodNotAllowed: {
					Description:  "Method not allowed (only POST accepted)",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Headers:      map[string]string{"Allow": "POST"},
				},
				http.StatusRequestEntityTooLarge: {
					Description:  "Webhook payload exceeds 1 MiB limit",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "payload too large"},
				},
				http.StatusInternalServerError: {
					Description:  "Failed to durably commit webhook to inbox",
					Type:         reflect.TypeOf(map[string]string{}),
					ContentTypes: []string{"application/json"},
					Example:      map[string]string{"error": "failed to persist webhook"},
				},
				http.StatusServiceUnavailable: {
					Description:  "Database unconfigured",
					Type:         reflect.TypeOf(internalHttp.ErrorResponse{}),
					ContentTypes: []string{"application/json"},
				},
			},
			Security: webhookSecurity,
		},
	}
}
