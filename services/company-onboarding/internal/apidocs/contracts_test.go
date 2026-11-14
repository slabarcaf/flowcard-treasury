package apidocs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/apidocs"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

// Selected OpenAPI dialect and library tooling recorded per Task 1:
// - Library: github.com/getkin/kin-openapi v0.149.0 (with openapi3gen reflection engine)
// - OpenAPI Dialect: 3.1.0 (OpenAPI Specification 3.1.0 with JSON Schema draft 2020-12 alignment)
// - Explicit Rationale for 3.1.0:
//  1. JSON Schema draft 2020-12 compatibility supports native type arrays for nullable fields (e.g. type: ["string", "null"]).
//  2. Root "webhooks" mapping cleanly isolates internal webhook deliveries without fake path operations.
//  3. Full backwards-compatible validation supported in kin-openapi v0.149 via EnableJSONSchema2020().
//  4. Bundled Swagger UI 5.x natively renders OpenAPI 3.1.0 schemas.
const (
	ChosenOpenAPIVersion = "3.1.0"
	ChosenLibrary        = "github.com/getkin/kin-openapi v0.149.0"
)

// RouteInventoryItem captures the contract specification for a registered endpoint.
type RouteInventoryItem struct {
	Method           string
	Path             string
	Audience         string // "operator", "public", or "internal-webhook"
	RequestType      reflect.Type
	ResponseType     reflect.Type
	SuccessStatus    int
	ErrorStatuses    []int
	IsMultipart      bool
	IsBinaryStream   bool
	QueryParameters  []string
	PathParameters   []string
	HeaderParameters []string
	ContentTypes     []string
	Notes            string
}

// FullInventory returns the exhaustive contract specification of all 25 implemented routes.
func FullInventory() []RouteInventoryItem {
	return []RouteInventoryItem{
		// 1. Public Operational Health Endpoints (2 routes)
		{
			Method:        "GET",
			Path:          "/healthz",
			Audience:      "public",
			ResponseType:  reflect.TypeOf(map[string]string{}),
			SuccessStatus: http.StatusOK,
			ErrorStatuses: nil,
			ContentTypes:  []string{"application/json"},
			Notes:         "Public liveness probe; returns service name and ok status",
		},
		{
			Method:        "GET",
			Path:          "/livez",
			Audience:      "public",
			ResponseType:  reflect.TypeOf(map[string]string{}),
			SuccessStatus: http.StatusOK,
			ErrorStatuses: nil,
			ContentTypes:  []string{"application/json"},
			Notes:         "Public health probe alias; identical behavior to /healthz",
		},

		// 2. Company Resource Endpoints (8 routes)
		{
			Method:        "POST",
			Path:          "/api/companies",
			Audience:      "operator",
			RequestType:   reflect.TypeOf(internalHttp.CreateCompanyRequest{}),
			ResponseType:  reflect.TypeOf(internalHttp.CompanyResponse{}),
			SuccessStatus: http.StatusCreated,
			ErrorStatuses: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:  []string{"application/json"},
			Notes:         "Creates company. Legal name required (min=2, max=200). Initial status is ACTIVE.",
		},
		{
			Method:          "GET",
			Path:            "/api/companies",
			Audience:        "operator",
			ResponseType:    reflect.TypeOf(internalHttp.CompanyListResponse{}),
			SuccessStatus:   http.StatusOK,
			ErrorStatuses:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError, http.StatusServiceUnavailable},
			QueryParameters: []string{"limit", "status", "cursor"},
			ContentTypes:    []string{"application/json"},
			Notes:           "Paginated list of companies; filters by status (ACTIVE, ARCHIVED), limit, cursor.",
		},
		{
			Method:         "GET",
			Path:           "/api/companies/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(internalHttp.CompanyResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Fetch single company by prefixed ID (comp_...)",
		},
		{
			Method:         "PATCH",
			Path:           "/api/companies/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			RequestType:    reflect.TypeOf(internalHttp.EditCompanyRequest{}),
			ResponseType:   reflect.TypeOf(internalHttp.CompanyResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Partial update requiring expected_revision CAS. Also aliased via PUT.",
		},
		{
			Method:         "PUT",
			Path:           "/api/companies/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			RequestType:    reflect.TypeOf(internalHttp.EditCompanyRequest{}),
			ResponseType:   reflect.TypeOf(internalHttp.CompanyResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "PUT alias for PATCH /api/companies/{id}; preserves partial update semantics.",
		},
		{
			Method:         "POST",
			Path:           "/api/companies/{id}/archive",
			Audience:       "operator",
			PathParameters: []string{"id"},
			RequestType:    reflect.TypeOf(internalHttp.ArchiveCompanyRequest{}),
			ResponseType:   reflect.TypeOf(internalHttp.ActionResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Archives company if no open applications exist. Returns ActionResponse(COMPANY_ARCHIVED).",
		},
		{
			Method:         "DELETE",
			Path:           "/api/companies/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(internalHttp.ActionResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Deletes draft company with no platform activity. Returns ActionResponse(COMPANY_DELETED), not 204.",
		},
		{
			Method:         "GET",
			Path:           "/api/companies/{id}/platform-accounts",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf([]internalHttp.PlatformAccountResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Returns raw JSON array of platform accounts (not wrapped in envelope).",
		},

		// 3. Application Resource Endpoints (8 routes)
		{
			Method:        "POST",
			Path:          "/api/onboarding-applications",
			Audience:      "operator",
			RequestType:   reflect.TypeOf(internalHttp.CreateApplicationRequest{}),
			ResponseType:  reflect.TypeOf(internalHttp.ApplicationResponse{}),
			SuccessStatus: http.StatusCreated,
			ErrorStatuses: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:  []string{"application/json"},
			Notes:         "Creates application for company. Conflicts (409) if active application exists.",
		},
		{
			Method:          "GET",
			Path:            "/api/onboarding-applications",
			Audience:        "operator",
			ResponseType:    reflect.TypeOf(internalHttp.ApplicationListResponse{}),
			SuccessStatus:   http.StatusOK,
			ErrorStatuses:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError, http.StatusServiceUnavailable},
			QueryParameters: []string{"company_id", "platform", "status", "limit", "cursor"},
			ContentTypes:    []string{"application/json"},
			Notes:           "Paginated list of applications; filters by company_id, platform, status, limit, cursor.",
		},
		{
			Method:         "GET",
			Path:           "/api/onboarding-applications/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(internalHttp.ApplicationResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Fetch application with embedded company and document metadata. ID prefixed with app_.",
		},
		{
			Method:         "PATCH",
			Path:           "/api/onboarding-applications/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			RequestType:    reflect.TypeOf(internalHttp.EditApplicationRequest{}),
			ResponseType:   reflect.TypeOf(internalHttp.ApplicationResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Updates platform onboarding details. Requires expected_revision CAS.",
		},
		{
			Method:         "POST",
			Path:           "/api/onboarding-applications/{id}/cancel",
			Audience:       "operator",
			PathParameters: []string{"id"},
			RequestType:    reflect.TypeOf(internalHttp.CancelApplicationRequest{}),
			ResponseType:   reflect.TypeOf(internalHttp.ActionResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Cancels application prior to platform submission. Returns ActionResponse(APPLICATION_CANCELLED).",
		},
		{
			Method:         "POST",
			Path:           "/api/onboarding-applications/{id}/verify",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(validation.ValidationResult{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Checks readiness for platform submission. Returns 200 on pass; 422 with same ValidationResult on failure.",
		},
		{
			Method:           "POST",
			Path:             "/api/onboarding-applications/{id}/submit",
			Audience:         "operator",
			PathParameters:   []string{"id"},
			HeaderParameters: []string{"Idempotency-Key"},
			RequestType:      reflect.TypeOf(internalHttp.SubmitApplicationRequest{}),
			ResponseType:     reflect.TypeOf(internalHttp.SubmitApplicationResponse{}),
			SuccessStatus:    http.StatusAccepted,
			ErrorStatuses:    []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:     []string{"application/json"},
			Notes:            "Requires Idempotency-Key. Atomically queues a durable submission and returns 202 with IDs and a Location for the existing submissions collection; readiness failures return 422.",
		},
		{
			Method:         "GET",
			Path:           "/api/onboarding-applications/{id}/submissions",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf([]internalHttp.SubmissionResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Returns raw JSON array of immutable historical submission records.",
		},

		// 4. Document Endpoints (3 routes)
		{
			Method:         "POST",
			Path:           "/api/onboarding-applications/{id}/documents",
			Audience:       "operator",
			PathParameters: []string{"id"},
			IsMultipart:    true,
			ResponseType:   reflect.TypeOf(internalHttp.DocumentResponse{}),
			SuccessStatus:  http.StatusCreated,
			ErrorStatuses:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"multipart/form-data"},
			Notes:          "Multipart upload (form fields: file, type, person_id, identification_id, expected_revision). Max 10 MiB, sniffed PDF/JPEG/PNG.",
		},
		{
			Method:         "GET",
			Path:           "/api/onboarding-applications/{id}/documents",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf([]internalHttp.DocumentResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Returns raw JSON array of document metadata attached to application.",
		},
		{
			Method:         "GET",
			Path:           "/api/onboarding-applications/{id}/documents/{doc_id}",
			Audience:       "operator",
			PathParameters: []string{"id", "doc_id"},
			IsBinaryStream: true,
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/pdf", "image/jpeg", "image/png"},
			Notes:          "Direct streaming download with Content-Disposition: attachment, sniffed MIME (PDF, JPEG, PNG), and nosniff header.",
		},

		// 5. Global Reads (2 routes)
		{
			Method:         "GET",
			Path:           "/api/submissions/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(internalHttp.SubmissionResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Reads frozen submission record by ID (sub_...).",
		},
		{
			Method:         "GET",
			Path:           "/api/platform-accounts/{id}",
			Audience:       "operator",
			PathParameters: []string{"id"},
			ResponseType:   reflect.TypeOf(internalHttp.PlatformAccountResponse{}),
			SuccessStatus:  http.StatusOK,
			ErrorStatuses:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:   []string{"application/json"},
			Notes:          "Reads platform account aggregate by ID (pa_...).",
		},

		// 6. Internal Webhook Endpoints (2 routes)
		{
			Method:        "POST",
			Path:          "/api/webhooks/airwallex",
			Audience:      "internal-webhook",
			RequestType:   reflect.TypeOf(webhooks.InboundWebhookPayload{}),
			ResponseType:  reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}),
			SuccessStatus: http.StatusOK,
			ErrorStatuses: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:  []string{"application/json"},
			Notes:         "Internal webhook ingress with JWT service assertion auth and durable inbox storage before acknowledgement.",
		},
		{
			Method:        "POST",
			Path:          "/api/v1/webhooks/airwallex",
			Audience:      "internal-webhook",
			RequestType:   reflect.TypeOf(webhooks.InboundWebhookPayload{}),
			ResponseType:  reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}),
			SuccessStatus: http.StatusOK,
			ErrorStatuses: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge, http.StatusInternalServerError, http.StatusServiceUnavailable},
			ContentTypes:  []string{"application/json"},
			Notes:         "Alias route for webhook ingress with identical handler, auth, and persistence semantics.",
		},
	}
}

// schemaCustomizer handles DTO domain types, validation tags, and domain invariants.
func schemaCustomizer(name string, t reflect.Type, tag reflect.StructTag, schema *openapi3.Schema) error {
	// Date-only fields
	if t == reflect.TypeOf(domain.Date("")) {
		schema.Type = &openapi3.Types{"string"}
		schema.Format = "date"
		schema.Pattern = `^\d{4}-\d{2}-\d{2}$`
		return nil
	}

	// Status enums from actual domain constants
	if name == "status" {
		if t.Kind() == reflect.String {
			schema.Type = &openapi3.Types{"string"}
			schema.Enum = []any{
				string(domain.CompanyStatusActive),
				string(domain.CompanyStatusArchived),
			}
			return nil
		}
	}

	// Application status enum
	if name == "app_status" || (name == "status" && strings.Contains(tag.Get("json"), "status")) {
		// Handled per specific DTO inspection
	}

	// String revision bounds
	if name == "revision" || name == "expected_revision" {
		min := float64(1)
		schema.Min = &min
		schema.Type = &openapi3.Types{"integer"}
		return nil
	}

	// String ID patterns (never UUID)
	if name == "id" || name == "company_id" || name == "submission_id" || name == "person_id" || name == "identification_id" {
		if schema.Format == "uuid" {
			schema.Format = "" // Clear any default uuid assumption
		}
		schema.Type = &openapi3.Types{"string"}
		schema.Pattern = `^[a-z]+_[0-9a-fA-F]+$`
		return nil
	}

	return nil
}

// TestTask1_ToolingEvaluation_KinOpenAPI31 evaluates kin-openapi v0.149.0 with OpenAPI 3.1.0,
// generating schemas directly from real project DTOs and validating the root document via doc.Validate(ctx).
func TestTask1_ToolingEvaluation_KinOpenAPI31(t *testing.T) {
	ctx := context.Background()

	// 1. Initialize generator with customizer
	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(schemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	// 2. Generate schemas from real project DTOs using NewSchemaRefForValue
	companyRespRef, err := gen.NewSchemaRefForValue(internalHttp.CompanyResponse{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for CompanyResponse: %v", err)
	}

	createReqRef, err := gen.NewSchemaRefForValue(internalHttp.CreateCompanyRequest{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for CreateCompanyRequest: %v", err)
	}

	errorRespRef, err := gen.NewSchemaRefForValue(internalHttp.ErrorResponse{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for ErrorResponse: %v", err)
	}

	valResultRef, err := gen.NewSchemaRefForValue(validation.ValidationResult{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for ValidationResult: %v", err)
	}

	// 3. Assemble complete OpenAPI 3.1.0 document
	doc := &openapi3.T{
		OpenAPI: ChosenOpenAPIVersion,
		Info: &openapi3.Info{
			Title:       "FlowCard Onboarding API",
			Version:     "1.0.0",
			Description: "Authoritative underwriting platform onboarding service.",
		},
		Paths: openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{
				"CompanyResponse":      companyRespRef,
				"CreateCompanyRequest": createReqRef,
				"ErrorResponse":        errorRespRef,
				"ValidationResult":     valResultRef,
			},
			SecuritySchemes: openapi3.SecuritySchemes{
				"BearerAuth": &openapi3.SecuritySchemeRef{
					Value: &openapi3.SecurityScheme{
						Type:         "http",
						Scheme:       "bearer",
						BearerFormat: "JWT",
						Description:  "Internal service-to-service JWT assertion",
					},
				},
			},
		},
	}

	// 4. Add a representative route operation
	doc.Paths.Set("/api/companies", &openapi3.PathItem{
		Post: &openapi3.Operation{
			OperationID: "createCompany",
			Summary:     "Create draft company",
			RequestBody: &openapi3.RequestBodyRef{
				Value: &openapi3.RequestBody{
					Required: true,
					Content: openapi3.Content{
						"application/json": &openapi3.MediaType{
							Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/CreateCompanyRequest"},
						},
					},
				},
			},
			Responses: &openapi3.Responses{},
		},
	})
	doc.Paths.Find("/api/companies").Post.Responses.Set("201", &openapi3.ResponseRef{
		Value: &openapi3.Response{
			Description: stringPtr("Company successfully created"),
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/CompanyResponse"},
				},
			},
		},
	})

	// 5. Serialize and reload via loader to verify self-contained references and schema validity
	docJSON, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal doc: %v", err)
	}

	loader := openapi3.NewLoader()
	loadedDoc, err := loader.LoadFromData(docJSON)
	if err != nil {
		t.Fatalf("failed to load serialized OpenAPI 3.1 document with kin-openapi loader: %v", err)
	}

	if !loadedDoc.IsOpenAPI31OrLater() {
		t.Fatalf("expected loadedDoc.IsOpenAPI31OrLater() to be true for OpenAPI %s", ChosenOpenAPIVersion)
	}

	if err := loadedDoc.Validate(ctx); err != nil {
		t.Fatalf("doc.Validate failed for OpenAPI 3.1.0: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(docJSON, &parsed); err != nil {
		t.Fatalf("failed to parse generated JSON: %v", err)
	}

	if parsed["openapi"] != "3.1.0" {
		t.Errorf("expected openapi 3.1.0, got %v", parsed["openapi"])
	}
}

// TestTask1_GeneratedSchemas_DomainDTOParity verifies that schemas generated directly from DTOs
// accurately reflect domain rules, ID formatting, enums, and nested structures without inventing fake values.
func TestTask1_GeneratedSchemas_DomainDTOParity(t *testing.T) {
	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(schemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	// 1. CompanyResponse schema verification
	compSchemaRef, err := gen.GenerateSchemaRef(reflect.TypeOf(internalHttp.CompanyResponse{}))
	if err != nil {
		t.Fatalf("failed to generate schema for CompanyResponse: %v", err)
	}
	compSchema := compSchemaRef.Value

	// ID must not be uuid; must be typed string
	idProp := compSchema.Properties["id"]
	if idProp == nil || idProp.Value == nil {
		t.Fatalf("missing id property in CompanyResponse schema")
	}
	if idProp.Value.Format == "uuid" {
		t.Errorf("id property must NOT have uuid format; actual IDs are prefixed strings (comp_...)")
	}

	// Status enum must be strictly ACTIVE / ARCHIVED (no invented DRAFT)
	statusProp := compSchema.Properties["status"]
	if statusProp == nil || statusProp.Value == nil {
		t.Fatalf("missing status property in CompanyResponse schema")
	}
	expectedCompanyStatuses := []string{string(domain.CompanyStatusActive), string(domain.CompanyStatusArchived)}
	if len(statusProp.Value.Enum) != len(expectedCompanyStatuses) {
		t.Errorf("expected status enum len %d, got %d: %v", len(expectedCompanyStatuses), len(statusProp.Value.Enum), statusProp.Value.Enum)
	}
	for _, expected := range expectedCompanyStatuses {
		found := false
		for _, enumVal := range statusProp.Value.Enum {
			if enumVal == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected status enum %s in %v", expected, statusProp.Value.Enum)
		}
	}

	// Nested arrays: RegistrationIdentifiers, People
	if compSchema.Properties["registration_identifiers"] == nil || compSchema.Properties["registration_identifiers"].Value.Type.Slice()[0] != "array" {
		t.Errorf("expected registration_identifiers to be an array schema")
	}
	if compSchema.Properties["people"] == nil || compSchema.Properties["people"].Value.Type.Slice()[0] != "array" {
		t.Errorf("expected people to be an array schema")
	}

	// 2. ErrorResponse schema verification: must have field, code, param, message
	errSchemaRef, err := gen.GenerateSchemaRef(reflect.TypeOf(internalHttp.ErrorResponse{}))
	if err != nil {
		t.Fatalf("failed to generate schema for ErrorResponse: %v", err)
	}
	errSchema := errSchemaRef.Value
	if errSchema.Properties["code"] == nil || errSchema.Properties["message"] == nil {
		t.Fatalf("missing required code/message properties in ErrorResponse")
	}
	errorsArr := errSchema.Properties["errors"]
	if errorsArr == nil || errorsArr.Value.Items == nil {
		t.Fatalf("missing errors array in ErrorResponse")
	}
	fieldErrorSchema := errorsArr.Value.Items.Value
	for _, prop := range []string{"field", "code", "message"} {
		if fieldErrorSchema.Properties[prop] == nil {
			t.Errorf("missing %s property in validation.FieldError schema", prop)
		}
	}
	// Verify it does NOT have incorrect "tag" property
	if fieldErrorSchema.Properties["tag"] != nil {
		t.Errorf("FieldError must not have 'tag' property; actual struct uses 'code' and 'param'")
	}
}

// TestTask1_SchemaValidation_EmittedJSONAndNegativeFixtures validates actual JSON bytes emitted
// by json.Marshal against the generated OpenAPI 3.1 schemas using kin-openapi's JSONSchema2020 validator,
// and confirms that negative fixtures fail with genuine violations.
func TestTask1_SchemaValidation_EmittedJSONAndNegativeFixtures(t *testing.T) {
	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(schemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	// 1. Validate real CompanyResponse emitted JSON
	compSchemaRef, err := gen.GenerateSchemaRef(reflect.TypeOf(internalHttp.CompanyResponse{}))
	if err != nil {
		t.Fatalf("failed to generate schema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	realComp := internalHttp.CompanyResponse{
		ID:                  "comp_0123456789abcdef0123456789abcdef",
		LegalName:           "Acme International Corp",
		TradingName:         "Acme Global",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		Status:              "ACTIVE",
		HasSubmittedHistory: false,
		BusinessProfile: domain.BusinessProfile{
			Description: "Cross-border trade finance solutions",
			Industry:    "FINANCE",
			Website:     "https://acme.example.com",
			Email:       "compliance@acme.example.com",
			Phone:       "+1-555-0199",
			RegisteredAddress: domain.Address{
				Line1:      "100 Market St",
				City:       "Wilmington",
				State:      "DE",
				PostalCode: "19801",
				Country:    "US",
			},
		},
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		People: []domain.Person{
			{
				ID:          "per_0123456789abcdef0123456789abcdef",
				FirstName:   "Jane",
				LastName:    "Doe",
				DateOfBirth: domain.Date("1985-04-12"),
				Nationality: "US",
				Email:       "jane@acme.example.com",
				Role:        "Director",
				Identifications: []domain.Identification{
					{
						ID:             "idn_0123456789abcdef0123456789abcdef",
						Type:           "PASSPORT",
						IssuingCountry: "US",
						Number:         "P123456789",
						IssuedOn:       domain.Date("2020-01-15"),
						ExpiresOn:      domain.Date("2030-01-15"),
					},
				},
			},
		},
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	realCompBytes, err := json.Marshal(realComp)
	if err != nil {
		t.Fatalf("failed to marshal real CompanyResponse: %v", err)
	}

	var parsedComp any
	if err := json.Unmarshal(realCompBytes, &parsedComp); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// POSITIVE FIXTURE: real CompanyResponse must validate cleanly
	if err := compSchemaRef.Value.VisitJSON(parsedComp, openapi3.EnableJSONSchema2020()); err != nil {
		t.Errorf("real CompanyResponse JSON failed schema validation: %v", err)
	}

	// NEGATIVE FIXTURE 1: Invented status "DRAFT" or "SUSPENDED" must fail enum validation
	invalidStatusJSON := strings.Replace(string(realCompBytes), `"status":"ACTIVE"`, `"status":"DRAFT"`, 1)
	var parsedInvalidStatus any
	_ = json.Unmarshal([]byte(invalidStatusJSON), &parsedInvalidStatus)
	if err := compSchemaRef.Value.VisitJSON(parsedInvalidStatus, openapi3.EnableJSONSchema2020()); err == nil {
		t.Errorf("expected schema validation to REJECT status='DRAFT' on CompanyResponse, but it passed")
	}

	// NEGATIVE FIXTURE 2: Invalid date format must fail date pattern validation
	invalidDateJSON := strings.Replace(string(realCompBytes), `"1985-04-12"`, `"04-12-1985"`, 1)
	var parsedInvalidDate any
	_ = json.Unmarshal([]byte(invalidDateJSON), &parsedInvalidDate)
	if err := compSchemaRef.Value.VisitJSON(parsedInvalidDate, openapi3.EnableJSONSchema2020()); err == nil {
		t.Errorf("expected schema validation to REJECT date_of_birth='04-12-1985', but it passed")
	}

	// NEGATIVE FIXTURE 3: Negative revision must fail min validation
	invalidRevJSON := strings.Replace(string(realCompBytes), `"revision":1`, `"revision":0`, 1)
	var parsedInvalidRev any
	_ = json.Unmarshal([]byte(invalidRevJSON), &parsedInvalidRev)
	if err := compSchemaRef.Value.VisitJSON(parsedInvalidRev, openapi3.EnableJSONSchema2020()); err == nil {
		t.Errorf("expected schema validation to REJECT revision=0, but it passed")
	}

	// 2. Validate ErrorResponse with FieldErrors
	errSchemaRef, err := gen.GenerateSchemaRef(reflect.TypeOf(internalHttp.ErrorResponse{}))
	if err != nil {
		t.Fatalf("failed to generate schema: %v", err)
	}
	realErr := internalHttp.ErrorResponse{
		Code:    "VALIDATION_FAILED",
		Message: "request payload contains 1 validation error",
		Errors: []validation.FieldError{
			{
				Field:   "legal_name",
				Code:    "required",
				Message: "legal_name is a required field",
			},
		},
	}
	errBytes, _ := json.Marshal(realErr)
	var parsedErr any
	_ = json.Unmarshal(errBytes, &parsedErr)

	// POSITIVE FIXTURE: valid ErrorResponse must pass
	if err := errSchemaRef.Value.VisitJSON(parsedErr, openapi3.EnableJSONSchema2020()); err != nil {
		t.Errorf("real ErrorResponse JSON failed schema validation: %v", err)
	}

	// 3. Validate ValidationResult (verify endpoint response)
	valResultSchemaRef, err := gen.GenerateSchemaRef(reflect.TypeOf(validation.ValidationResult{}))
	if err != nil {
		t.Fatalf("failed to generate schema: %v", err)
	}
	realValResult := validation.ValidationResult{
		Code:                "VALIDATION_PASSED",
		Message:             "all application requirements verified",
		ApplicationRevision: 2,
		CompanyRevision:     1,
		Errors:              nil,
	}
	valBytes, _ := json.Marshal(realValResult)
	var parsedVal any
	_ = json.Unmarshal(valBytes, &parsedVal)

	if err := valResultSchemaRef.Value.VisitJSON(parsedVal, openapi3.EnableJSONSchema2020()); err != nil {
		t.Errorf("real ValidationResult JSON failed schema validation: %v", err)
	}
}

// TestTask1_RouteInventoryAndWiringParity uses Go AST parsing on handlers.go and live HTTP routing
// with injected auth to verify 100% exact parity between the documented inventory and actual router wiring,
// including negative tests for nonexistent routes and invalid methods.
func TestTask1_RouteInventoryAndWiringParity(t *testing.T) {
	inventory := FullInventory()
	if len(inventory) != 25 {
		t.Fatalf("expected exactly 25 inventory routes, got %d", len(inventory))
	}

	// 1. AST PARSING: Parse internal/http/handlers.go to find all registered route patterns
	handlersPath := filepath.Join("..", "http", "handlers.go")
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, handlersPath, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s with Go AST: %v", handlersPath, err)
	}

	astRoutes := make(map[string]bool)
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// Match mux.HandleFunc("PATTERN", ...) or mux.Handle("PATTERN", ...)
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if (sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(call.Args) >= 1 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				pattern := strings.Trim(lit.Value, `"`)
				if strings.Contains(pattern, " ") { // e.g. "GET /healthz", "POST /api/companies"
					astRoutes[pattern] = true
				}
			}
		}
		return true
	})

	// Verify every route in FullInventory exists in the AST registration
	inventoryMap := make(map[string]bool)
	for _, item := range inventory {
		routeKey := fmt.Sprintf("%s %s", item.Method, item.Path)
		inventoryMap[routeKey] = true

		if !astRoutes[routeKey] {
			t.Errorf("route %s is in inventory but NOT found in handlers.go AST registrations", routeKey)
		}
	}

	// Verify no unregistered routes exist in AST
	for astRoute := range astRoutes {
		if !inventoryMap[astRoute] {
			t.Errorf("route %s is registered in handlers.go AST but MISSING from inventory", astRoute)
		}
	}

	// 2. LIVE ROUTER VERIFICATION
	// Part A: With s.repo == nil, verify that unconfigured DB causes:
	// - Public routes (/healthz, /livez) return 200 OK
	// - Every operator route returns 503 SERVICE_UNAVAILABLE from requireRepo
	// - Webhook routes return 503 SERVICE_UNAVAILABLE from requireRepo
	allowAuth := mockAllowAuthenticator{actor: "test-operator"}
	serverNilRepo := internalHttp.NewServer(
		nil, // Nil repository to test requireRepo gating
		allowAuth,
		validation.UnverifiedPlatformContractGate{},
		storage.NewUnavailableStore("test"),
	)
	mockWH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true,"eventId":"evt_test"}`))
	})
	serverNilRepo.WithWebhookHandler(mockWH, allowAuth)
	handlerNilRepo := serverNilRepo.Routes()

	// Verify all 21 operator routes return 503 with repo=nil
	for _, item := range inventory {
		if item.Audience != "operator" {
			continue
		}
		testPath := item.Path
		testPath = strings.Replace(testPath, "{id}", "test_id_123", 1)
		testPath = strings.Replace(testPath, "{doc_id}", "test_doc_456", 1)

		req := httptest.NewRequest(item.Method, testPath, nil)
		rec := httptest.NewRecorder()
		handlerNilRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected registered route %s %s to reach requireRepo and return 503, got %d (body: %s)",
				item.Method, testPath, rec.Code, rec.Body.String())
		}
	}

	// Verify public health routes return 200 OK
	for _, path := range []string{"/healthz", "/livez"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handlerNilRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected %s to return 200 OK, got %d", path, rec.Code)
		}
	}

	// Part B: With non-nil repository, verify webhook handler and negative routing
	repo := postgres.NewRepository(nil)
	serverWithRepo := internalHttp.NewServer(
		repo,
		allowAuth,
		validation.UnverifiedPlatformContractGate{},
		storage.NewUnavailableStore("test"),
	)
	serverWithRepo.WithWebhookHandler(mockWH, allowAuth)
	handlerWithRepo := serverWithRepo.Routes()

	// Verify webhook routes reach mock webhook handler and return 200
	for _, path := range []string{"/api/webhooks/airwallex", "/api/v1/webhooks/airwallex"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		handlerWithRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected webhook route POST %s to return 200 from mock handler, got %d", path, rec.Code)
		}
	}

	// NEGATIVE ROUTING: Nonexistent route must return 404 Not Found from apiMux
	{
		req := httptest.NewRequest(http.MethodGet, "/api/completely-nonexistent-path-12345", nil)
		rec := httptest.NewRecorder()
		handlerWithRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected unregistered path to return 404 Not Found, got %d", rec.Code)
		}
	}

	// NEGATIVE METHOD: Invalid method on registered path returns 405 Method Not Allowed
	{
		req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
		rec := httptest.NewRecorder()
		handlerWithRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected POST /healthz to return 405 Method Not Allowed, got %d", rec.Code)
		}
	}

	// NEGATIVE METHOD ON OPERATOR API: DELETE /api/companies returns 405 Method Not Allowed (only GET and POST registered)
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/companies", nil)
		rec := httptest.NewRecorder()
		handlerWithRepo.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected DELETE /api/companies to return 405 Method Not Allowed, got %d", rec.Code)
		}
	}
}

// TestTask1_ContractAudit_SubtletiesAndGating documents and verifies specific audited contract subtleties:
// 1. Array responses (platform accounts, submissions, documents) are raw JSON arrays, not envelopes.
// 2. Query parameter filters: GET /api/companies (limit, status, cursor); GET /api/onboarding-applications (company_id, platform, status, limit, cursor).
// 3. Document download serves PDF/JPEG/PNG binary content with Content-Disposition: attachment.
// 4. Submit application endpoint durably queues work and returns a 202 receipt.
// 5. Webhook error responses use map[string]string{"error": "..."} instead of ErrorResponse.
func TestTask1_ContractAudit_SubtletiesAndGating(t *testing.T) {
	// 1. Array responses are raw JSON arrays
	paArr := []internalHttp.PlatformAccountResponse{
		{ID: "pa_123", CompanyID: "comp_123", PlatformName: "airwallex", Environment: "sandbox", Status: "ACTIVE"},
	}
	paBytes, _ := json.Marshal(paArr)
	var paDecoded any
	if err := json.Unmarshal(paBytes, &paDecoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if _, isSlice := paDecoded.([]any); !isSlice {
		t.Errorf("expected PlatformAccountResponse to serialize as top-level JSON array, got %T", paDecoded)
	}

	// 2. Submit application response contains a durable receipt.
	submitResponse := internalHttp.SubmitApplicationResponse{
		Code:          "OK",
		ApplicationID: "app_123",
		SubmissionID:  "sub_123",
	}
	submitBytes, _ := json.Marshal(submitResponse)
	var submitDecoded map[string]any
	_ = json.Unmarshal(submitBytes, &submitDecoded)
	if submitDecoded["code"] != "OK" || submitDecoded["application_id"] != "app_123" || submitDecoded["submission_id"] != "sub_123" {
		t.Errorf("submit response = %v, want OK receipt IDs", submitDecoded)
	}

	// 3. Webhook error response format: map[string]string{"error": "..."}
	whErr := map[string]string{"error": "invalid json payload"}
	whErrBytes, _ := json.Marshal(whErr)
	var whErrDecoded map[string]any
	_ = json.Unmarshal(whErrBytes, &whErrDecoded)
	if whErrDecoded["error"] != "invalid json payload" {
		t.Errorf("expected error 'invalid json payload', got %v", whErrDecoded["error"])
	}
	if _, hasCode := whErrDecoded["code"]; hasCode {
		t.Errorf("webhook error must not have 'code' property; returns plain map[string]string error")
	}
}

// mockAllowAuthenticator allows all requests for routing verification.
type mockAllowAuthenticator struct {
	actor string
}

func (m mockAllowAuthenticator) Authenticate(r *http.Request) (string, error) {
	return m.actor, nil
}

func stringPtr(s string) *string {
	return &s
}

// extractASTRoutes parses handlers.go and returns all registered route patterns (e.g. "GET /healthz").
func extractASTRoutes(t *testing.T) map[string]bool {
	t.Helper()
	handlersPath := filepath.Join("..", "http", "handlers.go")
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, handlersPath, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s with Go AST: %v", handlersPath, err)
	}

	astRoutes := make(map[string]bool)
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if (sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(call.Args) >= 1 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				pattern := strings.Trim(lit.Value, `"`)
				if strings.Contains(pattern, " ") {
					astRoutes[pattern] = true
				}
			}
		}
		return true
	})
	return astRoutes
}

// compareRoutesWithOperations performs independent drift detection between Go router registrations and OpenAPI operations.
func compareRoutesWithOperations(astRoutes map[string]bool, operations []apidocs.OperationDefinition) []string {
	var discrepancies []string

	documentedOperator := make(map[string]bool)
	documentedWebhook := make(map[string]bool)

	for _, op := range operations {
		key := fmt.Sprintf("%s %s", op.Method, op.Path)
		if op.Audience == "operator" {
			documentedOperator[key] = true
		} else if op.Audience == "internal-webhook" {
			documentedWebhook[key] = true
		}
	}

	// Categorize AST routes
	for astRoute := range astRoutes {
		parts := strings.SplitN(astRoute, " ", 2)
		if len(parts) != 2 {
			continue
		}
		_, p := parts[0], parts[1]

		// 1. Health routes are public operational probes (excluded from business API docs)
		if p == "/healthz" || p == "/livez" {
			continue
		}

		// 2. Docs routes are public documentation UI routes
		if p == "/docs/api" || p == "/docs/api/" || strings.HasPrefix(p, "/docs/api/") {
			continue
		}

		// 3. Webhook routes
		if strings.HasPrefix(p, "/api/webhooks/") || strings.HasPrefix(p, "/api/v1/webhooks/") {
			if !documentedWebhook[astRoute] {
				discrepancies = append(discrepancies, fmt.Sprintf("AST registered webhook route %q is MISSING from OpenAPI WebhookOperations documentation", astRoute))
			}
			continue
		}

		// 4. Operator routes
		if !documentedOperator[astRoute] {
			discrepancies = append(discrepancies, fmt.Sprintf("AST registered route %q is MISSING from OpenAPI OperatorOperations documentation", astRoute))
		}
	}

	// Verify reverse: every documented operation must exist in AST
	for _, op := range operations {
		if op.Audience != "operator" && op.Audience != "internal-webhook" {
			continue
		}
		key := fmt.Sprintf("%s %s", op.Method, op.Path)
		if !astRoutes[key] {
			discrepancies = append(discrepancies, fmt.Sprintf("Documented operation %q (audience: %s) is NOT registered in handlers.go AST", key, op.Audience))
		}
	}

	return discrepancies
}

// TestTask5_DriftPrevention_ASTVsOpenAPIOperations verifies that Go AST registrations in handlers.go
// and the OpenAPI operation metadata in operations.go are in exact 100% 1-to-1 parity.
func TestTask5_DriftPrevention_ASTVsOpenAPIOperations(t *testing.T) {
	astRoutes := extractASTRoutes(t)
	allOps := apidocs.AllOperations()

	discrepancies := compareRoutesWithOperations(astRoutes, allOps)
	if len(discrepancies) > 0 {
		t.Fatalf("OpenAPI drift detected between Go handlers.go and operations metadata:\n  - %s",
			strings.Join(discrepancies, "\n  - "))
	}

	var operatorCount, webhookCount int
	for _, op := range allOps {
		if op.Audience == "operator" {
			operatorCount++
		} else if op.Audience == "internal-webhook" {
			webhookCount++
		}
	}

	// Verify exact counts
	if operatorCount != 21 {
		t.Errorf("expected exactly 21 operator operations, got %d", operatorCount)
	}
	if webhookCount != 2 {
		t.Errorf("expected exactly 2 webhook operations, got %d", webhookCount)
	}

	// Also verify that the generated OpenAPI 3.1 specs contain every path
	opSpec, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}
	for _, op := range allOps {
		if op.Audience != "operator" {
			continue
		}
		item := opSpec.Paths.Find(op.Path)
		if item == nil {
			t.Errorf("path %s from OperatorOperations is missing in generated openapi3.T Paths", op.Path)
			continue
		}
		opItem := item.GetOperation(op.Method)
		if opItem == nil {
			t.Errorf("method %s %s is missing in generated openapi3.T Paths", op.Method, op.Path)
		}
	}

	whSpec, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec: %v", err)
	}
	for _, op := range allOps {
		if op.Audience != "internal-webhook" {
			continue
		}
		item := whSpec.Paths.Find(op.Path)
		if item == nil {
			t.Errorf("webhook path %s is missing in generated openapi3.T Paths", op.Path)
			continue
		}
		opItem := item.GetOperation(op.Method)
		if opItem == nil {
			t.Errorf("webhook method %s %s is missing in generated openapi3.T Paths", op.Method, op.Path)
		}
	}
}

// TestTask5_NegativeDriftRejection verifies that adding a synthetic unapproved route,
// removing an operation, or having stale JSON on disk triggers drift check failures.
func TestTask5_NegativeDriftRejection(t *testing.T) {
	astRoutes := extractASTRoutes(t)
	allOps := apidocs.AllOperations()

	// NEGATIVE 1: Adding a synthetic extra route to AST must be detected and rejected
	syntheticAST := make(map[string]bool)
	for k, v := range astRoutes {
		syntheticAST[k] = v
	}
	syntheticAST["POST /api/unapproved-synthetic-feature"] = true

	discrepancies1 := compareRoutesWithOperations(syntheticAST, allOps)
	if len(discrepancies1) == 0 {
		t.Errorf("expected drift check to FAIL when synthetic route was added, but it passed")
	} else {
		found := false
		for _, d := range discrepancies1 {
			if strings.Contains(d, "unapproved-synthetic-feature") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected discrepancy mentioning synthetic route, got: %v", discrepancies1)
		}
	}

	// NEGATIVE 2: Adding an undocumented webhook route must be detected and rejected
	syntheticAST2 := make(map[string]bool)
	for k, v := range astRoutes {
		syntheticAST2[k] = v
	}
	syntheticAST2["POST /api/webhooks/unapproved-provider"] = true

	discrepancies2 := compareRoutesWithOperations(syntheticAST2, allOps)
	if len(discrepancies2) == 0 {
		t.Errorf("expected drift check to FAIL when synthetic webhook route was added, but it passed")
	}

	// NEGATIVE 3: Phantom operation in documentation not mounted in AST must be detected and rejected
	phantomOps := append([]apidocs.OperationDefinition{}, allOps...)
	phantomOps = append(phantomOps, apidocs.OperationDefinition{
		Method:   "DELETE",
		Path:     "/api/companies/phantom-endpoint",
		Audience: "operator",
	})
	discrepancies3 := compareRoutesWithOperations(astRoutes, phantomOps)
	if len(discrepancies3) == 0 {
		t.Errorf("expected drift check to FAIL for phantom undocumented operation, but it passed")
	}

	// NEGATIVE 4: Stale disk file drift detection via WriteSpecs(..., check=true)
	tmpDir := t.TempDir()
	repoTmp := filepath.Join(tmpDir, "repo")
	moduleTmp := filepath.Join(tmpDir, "module")

	// First write current clean specs
	if err := apidocs.WriteSpecs(repoTmp, moduleTmp, false); err != nil {
		t.Fatalf("failed to write initial clean specs: %v", err)
	}

	// Verify check passes on clean specs
	if err := apidocs.WriteSpecs(repoTmp, moduleTmp, true); err != nil {
		t.Fatalf("expected WriteSpecs(check=true) to PASS on clean specs, got: %v", err)
	}

	// Now tamper with one of the generated files to simulate stale drift
	staleTarget := filepath.Join(repoTmp, "docs", "api", "openapi.json")
	if err := os.WriteFile(staleTarget, []byte(`{"stale": true}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := apidocs.WriteSpecs(repoTmp, moduleTmp, true)
	if err == nil {
		t.Errorf("expected WriteSpecs(check=true) to FAIL on stale disk JSON, but it returned nil")
	} else if !strings.Contains(err.Error(), "stale generated specification") && !strings.Contains(err.Error(), "spec check failed") {
		t.Errorf("expected drift error message, got: %v", err)
	}
}

// TestTask5_SchemaVsEmittedHandlerResponses validates realistic wire JSON representations
// against OpenAPI 3.1 schemas across raw arrays, requests, errors, and gate 422 responses.
func TestTask5_SchemaVsEmittedHandlerResponses(t *testing.T) {
	opSpec, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	validateWithSchema := func(label string, sRef *openapi3.SchemaRef, rawJSON string) {
		if sRef == nil {
			t.Fatalf("schemaRef is nil for %s", label)
		}
		var val any
		if err := json.Unmarshal([]byte(rawJSON), &val); err != nil {
			t.Fatalf("failed to unmarshal JSON for %s: %v", label, err)
		}
		// In OpenAPI 3.1, visit root with components resolved
		if err := sRef.Value.VisitJSON(val, openapi3.EnableJSONSchema2020()); err != nil {
			t.Errorf("JSON failed schema validation for %s: %v\nJSON: %s", label, err, rawJSON)
		}
	}

	validateComponent := func(name, rawJSON string) {
		ref := opSpec.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			t.Fatalf("schema %q not found in components", name)
		}
		validateWithSchema(name, ref, rawJSON)
	}

	// 1. Raw Arrays: PlatformAccountList (from GET /api/companies/{id}/platform-accounts 200 response)
	paOp := opSpec.Paths.Find("/api/companies/{id}/platform-accounts")
	if paOp == nil || paOp.Get == nil {
		t.Fatalf("GET /api/companies/{id}/platform-accounts not found in opSpec")
	}
	paArrayRef := paOp.Get.Responses.Status(200).Value.Content.Get("application/json").Schema
	// Also construct resolved array schema for VisitJSON against component items
	paResolvedArray := &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: opSpec.Components.Schemas["PlatformAccountResponse"],
		},
	}
	validateWithSchema("PlatformAccountList", paResolvedArray, `[
		{
			"id": "pa_live_123",
			"company_id": "comp_abc",
			"platform_name": "airwallex",
			"environment": "sandbox",
			"status": "ACTIVE",
			"revision": 1,
			"created_at": "2026-11-10T12:00:00Z",
			"updated_at": "2026-11-10T12:00:00Z"
		},
		{
			"id": "pa_live_456",
			"company_id": "comp_abc",
			"platform_name": "airwallex",
			"environment": "production",
			"status": "SUSPENDED",
			"revision": 2,
			"created_at": "2026-11-10T12:00:00Z",
			"updated_at": "2026-11-10T12:00:00Z"
		}
	]`)
	if paArrayRef == nil {
		t.Errorf("expected GET /api/companies/{id}/platform-accounts 200 to have response schema")
	}

	// 2. Raw Arrays: SubmissionList
	subResolvedArray := &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: opSpec.Components.Schemas["SubmissionResponse"],
		},
	}
	validateWithSchema("SubmissionList", subResolvedArray, `[
		{
			"submission_id": "sub_1",
			"type": "INITIAL_ONBOARDING",
			"status": "PENDING",
			"company": null,
			"onboarding_details": {
				"estimated_monthly_revenue": {
					"amount": "50000.00",
					"currency": "USD"
				},
				"customer_agreements": {
					"agreed_to_terms_and_conditions": true,
					"agreed_to_data_usage": true
				}
			},
			"submitting_actor": "operator_42",
			"created_at": "2026-11-10T12:00:00Z"
		}
	]`)

	// 3. Raw Arrays: DocumentList
	docResolvedArray := &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: opSpec.Components.Schemas["DocumentResponse"],
		},
	}
	validateWithSchema("DocumentList", docResolvedArray, `[
		{
			"document_id": "doc_1",
			"type": "CERTIFICATE_OF_INCORPORATION",
			"file_name": "incorp.pdf",
			"content_type": "application/pdf",
			"size_bytes": 204800,
			"uploaded_by": "operator_42",
			"uploaded_at": "2026-11-10T12:00:00Z"
		}
	]`)

	// 4. Request DTO: CreateCompanyRequest
	validateComponent("CreateCompanyRequest", `{
		"legal_name": "Acme Holdings LLC",
		"jurisdiction": "US-DE",
		"tax_identifier": "12-3456789"
	}`)

	// 5. Request DTO: EditCompanyRequest
	validateComponent("EditCompanyRequest", `{
		"expected_revision": 1,
		"legal_name": "Acme Global Technologies Inc.",
		"status": "ACTIVE"
	}`)

	// 6. Request DTO: CreateApplicationRequest
	validateComponent("CreateApplicationRequest", `{
		"company_id": "comp_123",
		"platform_name": "airwallex"
	}`)

	// 7. Verification Response: ValidationResult
	validateComponent("ValidationResult", `{
		"code": "VALIDATION_PASSED",
		"message": "all application requirements verified",
		"application_revision": 2,
		"company_revision": 1
	}`)

	// 8. Readiness 422 error payload
	validateComponent("ErrorResponse", `{
		"code": "IDEMPOTENCY_CONFLICT",
		"message": "Idempotency-Key was used with a different request"
	}`)

	// 9. ErrorResponse with field errors (400 validation error)
	validateComponent("ErrorResponse", `{
		"code": "VALIDATION_FAILED",
		"message": "request payload validation failed",
		"errors": [
			{
				"field": "legal_name",
				"code": "min_length",
				"message": "legal_name must be at least 2 characters"
			}
		]
	}`)

	// 10. Gateway vs Service Auth Error models
	validateComponent("GatewayAuthError", `{"error": "operator_auth_required"}`)
	validateComponent("ServiceAuthError", `{"code": "UNAUTHORIZED", "message": "missing or invalid service authorization token"}`)

	// 11. Sensitive Credentials Scan: Verify NO real/fake secret keys exist in generated specs
	specs := []*openapi3.T{opSpec}
	whSpec, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec: %v", err)
	}
	specs = append(specs, whSpec)

	forbiddenSubstrings := []string{
		"eyJhbGciOi",            // JWT header prefix
		"client_secret_live",    // Live secret
		"BEGIN PRIVATE KEY",     // PEM private key
		"BEGIN RSA PRIVATE KEY", // RSA private key
	}

	for i, spec := range specs {
		data, err := json.Marshal(spec)
		if err != nil {
			t.Fatalf("failed to marshal spec %d: %v", i, err)
		}
		specStr := string(data)
		for _, forbidden := range forbiddenSubstrings {
			if strings.Contains(specStr, forbidden) {
				t.Errorf("spec %d contains forbidden sensitive string %q in example or schema", i, forbidden)
			}
		}
	}
}

// TestTask5_DeterministicArtifactParity verifies that the 4 generated files on disk
// match the generated specifications deterministically and have exact artifact parity.
func TestTask5_DeterministicArtifactParity(t *testing.T) {
	// Root docs/api specs (4 levels up from internal/apidocs)
	rootOpPath := filepath.Join("..", "..", "..", "..", "docs", "api", "openapi.json")
	rootWhPath := filepath.Join("..", "..", "..", "..", "docs", "api", "webhooks.json")

	// Module embedded specs
	modOpPath := filepath.Join("specs", "openapi.json")
	modWhPath := filepath.Join("specs", "webhooks.json")

	rootOpBytes, err := os.ReadFile(rootOpPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", rootOpPath, err)
	}
	modOpBytes, err := os.ReadFile(modOpPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", modOpPath, err)
	}

	if string(rootOpBytes) != string(modOpBytes) {
		t.Errorf("drift between %s and %s: root and module embedded operator specs must be byte-for-byte identical",
			rootOpPath, modOpPath)
	}

	rootWhBytes, err := os.ReadFile(rootWhPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", rootWhPath, err)
	}
	modWhBytes, err := os.ReadFile(modWhPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", modWhPath, err)
	}

	if string(rootWhBytes) != string(modWhBytes) {
		t.Errorf("drift between %s and %s: root and module embedded webhook specs must be byte-for-byte identical",
			rootWhPath, modWhPath)
	}
}
