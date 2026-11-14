package apidocs_test

import (
	"context"
	"encoding/json"
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
)

// Helper to validate JSON payload against a Schema using OpenAPI 3.1 / JSON Schema 2020-12 rules.
func validateJSONAgainstSchema(t *testing.T, s *openapi3.Schema, rawJSON string) error {
	t.Helper()
	var val any
	if err := json.Unmarshal([]byte(rawJSON), &val); err != nil {
		t.Fatalf("test helper failed to unmarshal test JSON: %v", err)
	}
	return s.VisitJSON(val, openapi3.EnableJSONSchema2020())
}

// TestTask2_DeterministicGeneration verifies that generation produces byte-for-byte identical output across runs.
func TestTask2_DeterministicGeneration(t *testing.T) {
	opDoc1, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec 1: %v", err)
	}
	opBytes1, err := json.MarshalIndent(opDoc1, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal 1: %v", err)
	}

	opDoc2, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec 2: %v", err)
	}
	opBytes2, err := json.MarshalIndent(opDoc2, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal 2: %v", err)
	}

	if !reflect.DeepEqual(opBytes1, opBytes2) {
		t.Errorf("operator specification generation is non-deterministic")
	}

	whDoc1, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec 1: %v", err)
	}
	whBytes1, err := json.MarshalIndent(whDoc1, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal 1: %v", err)
	}

	whDoc2, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec 2: %v", err)
	}
	whBytes2, err := json.MarshalIndent(whDoc2, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal 2: %v", err)
	}

	if !reflect.DeepEqual(whBytes1, whBytes2) {
		t.Errorf("webhook specification generation is non-deterministic")
	}
}

// TestTask2_OpenAPI31Conformance verifies that generated specs conform to the official OpenAPI 3.1.0 specification.
func TestTask2_OpenAPI31Conformance(t *testing.T) {
	ctx := context.Background()

	// 1. Operator spec validation
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}
	if !opDoc.IsOpenAPI31OrLater() {
		t.Errorf("expected operator spec to be OpenAPI 3.1+, got %s", opDoc.OpenAPI)
	}
	if err := opDoc.Validate(ctx); err != nil {
		t.Errorf("operator spec failed kin-openapi 3.1 validation: %v", err)
	}

	// 2. Webhook spec validation
	whDoc, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec: %v", err)
	}
	if !whDoc.IsOpenAPI31OrLater() {
		t.Errorf("expected webhook spec to be OpenAPI 3.1+, got %s", whDoc.OpenAPI)
	}
	if err := whDoc.Validate(ctx); err != nil {
		t.Errorf("webhook spec failed kin-openapi 3.1 validation: %v", err)
	}
}

// TestTask2_AudienceSeparation verifies strict boundary separation between operator and internal webhook specs.
func TestTask2_AudienceSeparation(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}
	whDoc, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec: %v", err)
	}

	// 1. Operator spec checks:
	// - Must contain exactly 16 unique path items (2 public health + 14 operator path resources)
	// - Must contain exactly 23 operations (21 operator operations + 2 public health)
	// - Must NOT contain webhook receiver endpoints
	// - Must contain GatewaySessionCookie and ServiceJWT
	// - Must NOT contain WebhookServiceJWT
	if opDoc.Paths.Len() != 16 {
		t.Errorf("expected 16 unique path items in operator spec, got %d", opDoc.Paths.Len())
	}

	opCount := 0
	for _, pathItem := range opDoc.Paths.Map() {
		opCount += len(pathItem.Operations())
	}
	if opCount != 23 {
		t.Errorf("expected 23 operations in operator spec (21 operator + 2 public), got %d", opCount)
	}

	if opDoc.Paths.Find("/api/webhooks/airwallex") != nil {
		t.Errorf("operator spec must NOT contain /api/webhooks/airwallex")
	}
	if opDoc.Paths.Find("/api/v1/webhooks/airwallex") != nil {
		t.Errorf("operator spec must NOT contain /api/v1/webhooks/airwallex")
	}

	if opDoc.Components.SecuritySchemes["GatewaySessionCookie"] == nil {
		t.Errorf("operator spec missing GatewaySessionCookie scheme")
	}
	if opDoc.Components.SecuritySchemes["ServiceJWT"] == nil {
		t.Errorf("operator spec missing ServiceJWT scheme")
	}
	if opDoc.Components.SecuritySchemes["WebhookServiceJWT"] != nil {
		t.Errorf("operator spec must NOT contain WebhookServiceJWT scheme")
	}

	// 2. Webhook spec checks:
	// - Must contain exactly 2 webhook receiver paths
	// - Must NOT contain operator paths or public health paths
	// - Must contain WebhookServiceJWT
	// - Must NOT contain operator session cookie or operator JWT
	if whDoc.Paths.Len() != 2 {
		t.Errorf("expected exactly 2 paths in webhook spec, got %d", whDoc.Paths.Len())
	}
	if whDoc.Paths.Find("/api/webhooks/airwallex") == nil {
		t.Errorf("webhook spec missing /api/webhooks/airwallex")
	}
	if whDoc.Paths.Find("/api/v1/webhooks/airwallex") == nil {
		t.Errorf("webhook spec missing /api/v1/webhooks/airwallex")
	}
	if whDoc.Paths.Find("/api/companies") != nil {
		t.Errorf("webhook spec must NOT contain /api/companies")
	}
	if whDoc.Paths.Find("/healthz") != nil {
		t.Errorf("webhook spec must NOT contain /healthz")
	}

	if whDoc.Components.SecuritySchemes["WebhookServiceJWT"] == nil {
		t.Errorf("webhook spec missing WebhookServiceJWT scheme")
	}
	if whDoc.Components.SecuritySchemes["GatewaySessionCookie"] != nil {
		t.Errorf("webhook spec must NOT contain GatewaySessionCookie scheme")
	}
	if whDoc.Components.SecuritySchemes["ServiceJWT"] != nil {
		t.Errorf("webhook spec must NOT contain ServiceJWT scheme")
	}
}

// TestTask2_CheckModeCleanAndDriftRejection tests WriteSpecs in write mode and verify mode.
func TestTask2_CheckModeCleanAndDriftRejection(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	moduleRoot := filepath.Join(tempDir, "module")

	// 1. Check mode on ungenerated directories must fail
	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, true); err == nil {
		t.Errorf("expected check mode to fail when spec files are missing")
	}

	// 2. Generate files
	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, false); err != nil {
		t.Fatalf("failed to write specs: %v", err)
	}

	// 3. Check mode on freshly generated files must succeed
	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, true); err != nil {
		t.Errorf("expected check mode to succeed on freshly generated files: %v", err)
	}

	// 4. Drift detection: Modify one file by appending drift comment
	targetPath := filepath.Join(repoRoot, "docs", "api", "openapi.json")
	data, _ := os.ReadFile(targetPath)
	_ = os.WriteFile(targetPath, append(data, []byte("// drift\n")...), 0644)

	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, true); err == nil {
		t.Errorf("expected check mode to fail on drifted specification file")
	}
}

// TestTask2_ContractAccuraciesInGeneratedSpecs audits required contract subtleties in generated output.
func TestTask2_ContractAccuraciesInGeneratedSpecs(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	// 1. Submit application documents idempotency, the 202 receipt, and readiness errors.
	submitPath := opDoc.Paths.Find("/api/onboarding-applications/{id}/submit")
	if submitPath == nil || submitPath.Post == nil {
		t.Fatalf("missing POST /api/onboarding-applications/{id}/submit in operator spec")
	}
	idempotencyKeyRequired := false
	for _, parameter := range submitPath.Post.Parameters {
		if parameter.Value != nil && parameter.Value.Name == "Idempotency-Key" &&
			parameter.Value.In == "header" && parameter.Value.Required {
			idempotencyKeyRequired = true
		}
	}
	if !idempotencyKeyRequired {
		t.Errorf("submit operation must require the Idempotency-Key header")
	}
	resp202 := submitPath.Post.Responses.Value("202")
	if resp202 == nil || resp202.Value == nil || resp202.Value.Headers["Location"] == nil {
		t.Fatalf("submit operation must document a 202 response with Location")
	}
	resp422 := submitPath.Post.Responses.Value("422")
	if resp422 == nil || resp422.Value == nil {
		t.Fatalf("missing 422 response in submit operation")
	}
	if !strings.Contains(*resp422.Value.Description, "readiness") {
		t.Errorf("expected 422 description to mention readiness, got %s", *resp422.Value.Description)
	}

	// 2. Raw array responses: list platform accounts, submissions, documents
	listPA := opDoc.Paths.Find("/api/companies/{id}/platform-accounts").Get
	pa200 := listPA.Responses.Value("200").Value.Content["application/json"].Schema.Value
	if pa200.Type.Slice()[0] != "array" {
		t.Errorf("expected platform-accounts response schema type 'array', got %v", pa200.Type.Slice()[0])
	}

	listSub := opDoc.Paths.Find("/api/onboarding-applications/{id}/submissions").Get
	sub200 := listSub.Responses.Value("200").Value.Content["application/json"].Schema.Value
	if sub200.Type.Slice()[0] != "array" {
		t.Errorf("expected submissions response schema type 'array', got %v", sub200.Type.Slice()[0])
	}

	listDoc := opDoc.Paths.Find("/api/onboarding-applications/{id}/documents").Get
	doc200 := listDoc.Responses.Value("200").Value.Content["application/json"].Schema.Value
	if doc200.Type.Slice()[0] != "array" {
		t.Errorf("expected documents response schema type 'array', got %v", doc200.Type.Slice()[0])
	}

	// 3. Document download binary streaming
	downloadDoc := opDoc.Paths.Find("/api/onboarding-applications/{id}/documents/{doc_id}").Get
	for _, mime := range []string{"application/pdf", "image/jpeg", "image/png"} {
		if downloadDoc.Responses.Value("200").Value.Content[mime] == nil {
			t.Errorf("missing content-type %s in document download response", mime)
		}
	}

	// 4. Document upload multipart
	uploadDoc := opDoc.Paths.Find("/api/onboarding-applications/{id}/documents").Post
	mpContent := uploadDoc.RequestBody.Value.Content["multipart/form-data"]
	if mpContent == nil {
		t.Fatalf("missing multipart/form-data request body in document upload")
	}
	fileProp := mpContent.Schema.Value.Properties["file"]
	if fileProp == nil || fileProp.Value.Format != "binary" {
		t.Errorf("expected binary format on file property")
	}

	// 5. Server URL is relative root
	if len(opDoc.Servers) != 1 || opDoc.Servers[0].URL != "/" {
		t.Errorf("expected single relative server root '/', got %v", opDoc.Servers)
	}
}

// TestTask2_LeadPoint1_GatewayCookieAndAuthSecurity verifies exact gateway cookie and security scheme properties.
func TestTask2_LeadPoint1_GatewayCookieAndAuthSecurity(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	sec := opDoc.Components.SecuritySchemes["GatewaySessionCookie"]
	if sec == nil || sec.Value == nil {
		t.Fatalf("missing GatewaySessionCookie security scheme")
	}

	// Exact cookie name matching server/src/http/kit5-operator-auth.ts COOKIE_NAME
	if sec.Value.Name != "kit5_operator_session" {
		t.Errorf("expected cookie name 'kit5_operator_session', got %q", sec.Value.Name)
	}
	if sec.Value.In != "cookie" {
		t.Errorf("expected in 'cookie', got %q", sec.Value.In)
	}
	if sec.Value.Type != "apiKey" {
		t.Errorf("expected type 'apiKey', got %q", sec.Value.Type)
	}
	if !strings.Contains(sec.Value.Description, "POST /api/kit5/session") {
		t.Errorf("expected description to document session creation via POST /api/kit5/session, got %q", sec.Value.Description)
	}
	if !strings.Contains(sec.Value.Description, "SameSite=None") {
		t.Errorf("expected description to document SameSite=None, got %q", sec.Value.Description)
	}
	if !strings.Contains(sec.Value.Description, "Path=/api") {
		t.Errorf("expected description to document Path=/api, got %q", sec.Value.Description)
	}
}

// TestTask2_LeadPoint2_ScopedStatusEnums verifies that status enums are strictly scoped per concrete DTO.
func TestTask2_LeadPoint2_ScopedStatusEnums(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	compSchema := opDoc.Components.Schemas["CompanyResponse"].Value
	statusProp := compSchema.Properties["status"].Value

	// CompanyResponse: only ACTIVE and ARCHIVED
	if len(statusProp.Enum) != 2 {
		t.Fatalf("expected 2 enums for CompanyResponse status, got %d", len(statusProp.Enum))
	}
	if statusProp.Enum[0] != "ACTIVE" || statusProp.Enum[1] != "ARCHIVED" {
		t.Errorf("expected ACTIVE and ARCHIVED, got %v", statusProp.Enum)
	}

	// CompanyResponse with status ACTIVE validates
	if err := validateJSONAgainstSchema(t, statusProp, `"ACTIVE"`); err != nil {
		t.Errorf("status ACTIVE failed validation: %v", err)
	}
	// CompanyResponse with status ARCHIVED validates
	if err := validateJSONAgainstSchema(t, statusProp, `"ARCHIVED"`); err != nil {
		t.Errorf("status ARCHIVED failed validation: %v", err)
	}
	// CompanyResponse with status DRAFT MUST FAIL validation
	if err := validateJSONAgainstSchema(t, statusProp, `"DRAFT"`); err == nil {
		t.Errorf("expected DRAFT to fail validation on CompanyResponse.status, but it succeeded")
	}

	// ApplicationResponse: all 7 statuses
	appSchema := opDoc.Components.Schemas["ApplicationResponse"].Value
	appStatusProp := appSchema.Properties["status"].Value
	expectedAppStatuses := []string{
		"DRAFT", "SUBMISSION_PENDING", "UNDER_REVIEW", "INFORMATION_REQUIRED",
		"COMPLETED", "REJECTED", "CANCELLED",
	}
	if len(appStatusProp.Enum) != len(expectedAppStatuses) {
		t.Fatalf("expected %d enums for ApplicationResponse status, got %d", len(expectedAppStatuses), len(appStatusProp.Enum))
	}
	for _, expectedStatus := range expectedAppStatuses {
		if err := validateJSONAgainstSchema(t, appStatusProp, `"`+expectedStatus+`"`); err != nil {
			t.Errorf("expected status %s to be valid on ApplicationResponse, failed: %v", expectedStatus, err)
		}
	}
	if err := validateJSONAgainstSchema(t, appStatusProp, `"UNKNOWN_APP_STATUS"`); err == nil {
		t.Errorf("expected UNKNOWN_APP_STATUS to fail validation on ApplicationResponse.status")
	}

	// SubmissionResponse: all 4 statuses
	subSchema := opDoc.Components.Schemas["SubmissionResponse"].Value
	subStatusProp := subSchema.Properties["status"].Value
	expectedSubStatuses := []string{"PENDING", "ACCEPTED", "FAILED", "UNRESOLVED"}
	if len(subStatusProp.Enum) != len(expectedSubStatuses) {
		t.Fatalf("expected %d enums for SubmissionResponse status, got %d", len(expectedSubStatuses), len(subStatusProp.Enum))
	}
	for _, expectedStatus := range expectedSubStatuses {
		if err := validateJSONAgainstSchema(t, subStatusProp, `"`+expectedStatus+`"`); err != nil {
			t.Errorf("expected status %s to be valid on SubmissionResponse, failed: %v", expectedStatus, err)
		}
	}
	if err := validateJSONAgainstSchema(t, subStatusProp, `"INVALID_SUBMISSION"`); err == nil {
		t.Errorf("expected INVALID_SUBMISSION to fail validation on SubmissionResponse.status")
	}

	// PlatformAccountResponse: open string (Enum is nil, accepting raw unmapped platform statuses)
	paSchema := opDoc.Components.Schemas["PlatformAccountResponse"].Value
	paStatusProp := paSchema.Properties["status"].Value
	if paStatusProp.Enum != nil {
		t.Errorf("expected PlatformAccountResponse.status to have nil Enum (open string), got %v", paStatusProp.Enum)
	}
	rawStatuses := []string{"UNOBSERVED", "CREATED", "SUBMITTED", "PENDING", "ACTIVE", "CUSTOM_RAW_STATUS", "TOTALLY_UNKNOWN"}
	for _, rawStatus := range rawStatuses {
		if err := validateJSONAgainstSchema(t, paStatusProp, `"`+rawStatus+`"`); err != nil {
			t.Errorf("raw status %s should validate on PlatformAccountResponse, failed: %v", rawStatus, err)
		}
	}
}

// TestTask2_LeadPoint3_AuthErrorOneOfVariants tests unauthenticated error schemas and payload variants.
func TestTask2_LeadPoint3_AuthErrorOneOfVariants(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	// GatewayAuthError schema requires only "error"
	gwAuthSchema := opDoc.Components.Schemas["GatewayAuthError"].Value
	if err := validateJSONAgainstSchema(t, gwAuthSchema, `{"error": "operator_auth_required"}`); err != nil {
		t.Errorf("gateway auth error payload failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, gwAuthSchema, `{}`); err == nil {
		t.Errorf("empty object should fail GatewayAuthError validation")
	}

	// ServiceAuthError schema requires "code" and "message", and permits optional "error"
	svcAuthSchema := opDoc.Components.Schemas["ServiceAuthError"].Value
	if err := validateJSONAgainstSchema(t, svcAuthSchema, `{"code": "UNAUTHENTICATED", "message": "invalid token"}`); err != nil {
		t.Errorf("service auth error without 'error' field failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, svcAuthSchema, `{"code": "UNAUTHENTICATED", "error": "unauthorized", "message": "invalid token"}`); err != nil {
		t.Errorf("service auth error with 'error' field failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, svcAuthSchema, `{"message": "only message"}`); err == nil {
		t.Errorf("missing code should fail ServiceAuthError validation")
	}

	// UnauthenticatedResponse has oneOf variants
	unauthSchema := opDoc.Components.Schemas["UnauthenticatedResponse"].Value
	if len(unauthSchema.OneOf) != 2 {
		t.Fatalf("expected UnauthenticatedResponse to have 2 oneOf variants, got %d", len(unauthSchema.OneOf))
	}

	// Webhook unauthenticated error
	whDoc, err := apidocs.GenerateWebhookSpec()
	if err != nil {
		t.Fatalf("failed to generate webhook spec: %v", err)
	}
	whUnauthSchema := whDoc.Components.Schemas["UnauthenticatedResponse"].Value
	if err := validateJSONAgainstSchema(t, whUnauthSchema, `{"code": "UNAUTHENTICATED", "error": "unauthorized", "message": "token missing"}`); err != nil {
		t.Errorf("webhook auth error failed validation: %v", err)
	}
}

// TestTask2_LeadPoint4_NativeJSONSchemaNullUnionTypes audits that no OpenAPI 3.0 nullable: true exists and union types work.
func TestTask2_LeadPoint4_NativeJSONSchemaNullUnionTypes(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	moduleRoot := filepath.Join(tempDir, "module")

	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, false); err != nil {
		t.Fatalf("failed to write specs: %v", err)
	}

	for _, relPath := range []string{
		filepath.Join("docs", "api", "openapi.json"),
		filepath.Join("docs", "api", "webhooks.json"),
		filepath.Join("services", "company-onboarding", "internal", "apidocs", "specs", "openapi.json"),
		filepath.Join("services", "company-onboarding", "internal", "apidocs", "specs", "webhooks.json"),
	} {
		var fullPath string
		if strings.HasPrefix(relPath, "docs") {
			fullPath = filepath.Join(repoRoot, relPath)
		} else {
			fullPath = filepath.Join(moduleRoot, strings.TrimPrefix(relPath, "services/company-onboarding/"))
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", fullPath, err)
		}
		if strings.Contains(string(data), `"nullable": true`) {
			t.Errorf("file %s contains legacy OpenAPI 3.0 'nullable: true'", relPath)
		}
	}

	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	// ApplicationResponse.closed_at has type: ["string", "null"]
	closedAtProp := opDoc.Components.Schemas["ApplicationResponse"].Value.Properties["closed_at"].Value
	types := closedAtProp.Type.Slice()
	if len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("expected closed_at to have type ['string', 'null'], got %v", types)
	}
	if err := validateJSONAgainstSchema(t, closedAtProp, `null`); err != nil {
		t.Errorf("closed_at: null failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, closedAtProp, `"2026-11-10T12:00:00Z"`); err != nil {
		t.Errorf("closed_at timestamp failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, closedAtProp, `12345`); err == nil {
		t.Errorf("closed_at: 12345 should fail validation")
	}

	// PlatformAccountResponse.last_sync_time has type: ["string", "null"]
	lastSyncProp := opDoc.Components.Schemas["PlatformAccountResponse"].Value.Properties["last_sync_time"].Value
	paTypes := lastSyncProp.Type.Slice()
	if len(paTypes) != 2 || paTypes[0] != "string" || paTypes[1] != "null" {
		t.Errorf("expected last_sync_time to have type ['string', 'null'], got %v", paTypes)
	}
	if err := validateJSONAgainstSchema(t, lastSyncProp, `null`); err != nil {
		t.Errorf("last_sync_time: null failed validation: %v", err)
	}
	if err := validateJSONAgainstSchema(t, lastSyncProp, `"2026-11-10T12:00:00Z"`); err != nil {
		t.Errorf("last_sync_time timestamp failed validation: %v", err)
	}
}

// TestTask2_LeadPoint5_ExcludedFieldsAndDomainReflection tests json:"-" exclusion and custom domain.Company MarshalJSON.
func TestTask2_LeadPoint5_ExcludedFieldsAndDomainReflection(t *testing.T) {
	// 1. json:"-" fields must be excluded from properties even when UseAllExportedFields is set
	type sampleDTOWithExcluded struct {
		VisibleField  string `json:"visible_field"`
		ExcludedField string `json:"-"`
		PrivateSecret string `json:"-"`
	}

	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(apidocs.SchemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	sRef, err := gen.NewSchemaRefForValue(sampleDTOWithExcluded{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for sample DTO: %v", err)
	}

	if sRef.Value.Properties["visible_field"] == nil {
		t.Errorf("missing visible_field in schema properties")
	}
	if sRef.Value.Properties["ExcludedField"] != nil || sRef.Value.Properties["-"] != nil {
		t.Errorf("json:\"-\" field ExcludedField was exposed in schema properties")
	}
	if sRef.Value.Properties["PrivateSecret"] != nil {
		t.Errorf("json:\"-\" field PrivateSecret was exposed in schema properties")
	}

	// 2. domain.Company custom MarshalJSON reflection produces correct schema
	compRef, err := gen.NewSchemaRefForValue(domain.Company{}, nil)
	if err != nil {
		t.Fatalf("failed to generate schema for domain.Company: %v", err)
	}

	// Reconstitute a real domain.Company and marshal to JSON
	realComp, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_1234567890",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
		HasSubmittedHistory: false,
		LegalName:           "Acme Technologies Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		BusinessProfile: domain.BusinessProfile{
			Website:     "https://example.com",
			Description: "Financial infrastructure provider",
			RegisteredAddress: domain.Address{
				Line1:      "100 Market St",
				City:       "San Francisco",
				State:      "CA",
				PostalCode: "94105",
				Country:    "US",
			},
		},
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		People: []domain.Person{
			{
				ID:          "per_123",
				FirstName:   "Alice",
				LastName:    "Smith",
				Role:        "CEO",
				DateOfBirth: domain.Date("1985-04-12"),
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to reconstitute real company: %v", err)
	}

	compJSONBytes, err := json.Marshal(realComp)
	if err != nil {
		t.Fatalf("failed to marshal real company: %v", err)
	}

	// Validate the emitted JSON from domain.Company against its schema
	if err := validateJSONAgainstSchema(t, compRef.Value, string(compJSONBytes)); err != nil {
		t.Errorf("real domain.Company JSON failed schema validation: %v", err)
	}
}

// TestTask2_LeadPoint6_SchemaValidationAllDocumentedTypes validates every documented DTO response and request.
func TestTask2_LeadPoint6_SchemaValidationAllDocumentedTypes(t *testing.T) {
	opDoc, err := apidocs.GenerateOperatorSpec()
	if err != nil {
		t.Fatalf("failed to generate operator spec: %v", err)
	}

	schemas := opDoc.Components.Schemas

	// 1. CompanyResponse
	validCompanyJSON := `{
		"id": "comp_123",
		"legal_name": "Stripe Payments Inc",
		"status": "ACTIVE",
		"has_submitted_history": false,
		"business_profile": {
			"website": "https://stripe.com",
			"description": "Payment platform",
			"registered_address": {
				"line_1": "510 Townsend St",
				"city": "San Francisco",
				"postal_code": "94103",
				"country": "US"
			}
		},
		"registration_identifiers": [
			{"type": "EIN", "value": "12-3456789", "issuing_country": "US"}
		],
		"people": [
			{"id": "per_1", "person_id": "per_1", "first_name": "Patrick", "last_name": "Collison", "role": "CEO", "date_of_birth": "1988-09-09"}
		],
		"revision": 1,
		"created_at": "2026-11-10T10:00:00Z",
		"updated_at": "2026-11-10T10:00:00Z"
	}`
	if err := validateJSONAgainstSchema(t, schemas["CompanyResponse"].Value, validCompanyJSON); err != nil {
		t.Errorf("CompanyResponse schema validation failed: %v", err)
	}

	// 2. ApplicationResponse with null closed_at
	validAppJSON := `{
		"id": "app_123",
		"company_id": "comp_123",
		"status": "DRAFT",
		"platform": {
			"name": "AIRWALLEX",
			"onboarding_details": {
				"estimated_monthly_revenue": {
					"amount": "100000",
					"currency": "USD"
				},
				"customer_agreements": {
					"agreed_to_terms_and_conditions": true,
					"agreed_to_data_usage": true
				}
			}
		},
		"company": null,
		"documents": [],
		"information_requests": [],
		"creator": "usr_operator_1",
		"operator_review_flagged": false,
		"revision": 1,
		"created_at": "2026-11-10T10:00:00Z",
		"updated_at": "2026-11-10T10:00:00Z",
		"closed_at": null
	}`
	if err := validateJSONAgainstSchema(t, schemas["ApplicationResponse"].Value, validAppJSON); err != nil {
		t.Errorf("ApplicationResponse schema validation failed: %v", err)
	}

	// 3. SubmissionResponse
	validSubJSON := `{
		"submission_id": "sub_123",
		"type": "INITIAL",
		"status": "PENDING",
		"company": null,
		"onboarding_details": {
			"estimated_monthly_revenue": {
				"amount": "100000",
				"currency": "USD"
			},
			"customer_agreements": {
				"agreed_to_terms_and_conditions": true,
				"agreed_to_data_usage": true
			}
		},
		"submitting_actor": "usr_operator_1",
		"created_at": "2026-11-10T10:00:00Z"
	}`
	if err := validateJSONAgainstSchema(t, schemas["SubmissionResponse"].Value, validSubJSON); err != nil {
		t.Errorf("SubmissionResponse schema validation failed: %v", err)
	}

	// 4. PlatformAccountResponse with raw unknown status and null last_sync_time
	validPAJSON := `{
		"id": "pa_123",
		"company_id": "comp_123",
		"platform_name": "AIRWALLEX",
		"environment": "SANDBOX",
		"status": "UNOBSERVED_RAW_STATE",
		"last_sync_time": null,
		"revision": 1,
		"created_at": "2026-11-10T10:00:00Z",
		"updated_at": "2026-11-10T10:00:00Z"
	}`
	if err := validateJSONAgainstSchema(t, schemas["PlatformAccountResponse"].Value, validPAJSON); err != nil {
		t.Errorf("PlatformAccountResponse schema validation failed: %v", err)
	}

	// 5. DocumentResponse
	validDocJSON := `{
		"document_id": "doc_123",
		"type": "ARTICLES_OF_INCORPORATION",
		"file_name": "articles.pdf",
		"content_type": "application/pdf",
		"size_bytes": 1048576,
		"uploaded_by": "usr_operator_1",
		"uploaded_at": "2026-11-10T10:00:00Z"
	}`
	if err := validateJSONAgainstSchema(t, schemas["DocumentResponse"].Value, validDocJSON); err != nil {
		t.Errorf("DocumentResponse schema validation failed: %v", err)
	}

	// 6. ActionResponse
	validActionJSON := `{"code": "APPLICATION_CANCELLED", "message": "application cancelled by operator"}`
	if err := validateJSONAgainstSchema(t, schemas["ActionResponse"].Value, validActionJSON); err != nil {
		t.Errorf("ActionResponse schema validation failed: %v", err)
	}

	// 7. ErrorResponse
	validErrJSON := `{
		"code": "VALIDATION_FAILED",
		"message": "request payload validation failed",
		"errors": [{"field": "legal_name", "code": "REQUIRED", "message": "legal_name is required"}]
	}`
	if err := validateJSONAgainstSchema(t, schemas["ErrorResponse"].Value, validErrJSON); err != nil {
		t.Errorf("ErrorResponse schema validation failed: %v", err)
	}

	// 8. FieldError
	validFieldErrJSON := `{"field": "country", "code": "ISO3166_ALPHA2", "message": "country must be a valid ISO 3166-1 alpha-2 code"}`
	if err := validateJSONAgainstSchema(t, schemas["FieldError"].Value, validFieldErrJSON); err != nil {
		t.Errorf("FieldError schema validation failed: %v", err)
	}

	// 9. ValidationResult - Complete (Passed)
	passValResultJSON := `{
		"code": "VALIDATION_PASSED",
		"message": "application is ready for platform submission",
		"application_revision": 2,
		"company_revision": 1
	}`
	if err := validateJSONAgainstSchema(t, schemas["ValidationResult"].Value, passValResultJSON); err != nil {
		t.Errorf("ValidationResult (passed) schema validation failed: %v", err)
	}

	// 10. ValidationResult - Incomplete (Failed with FieldErrors)
	failValResultJSON := `{
		"code": "VALIDATION_FAILED",
		"message": "application failed platform readiness checks",
		"application_revision": 1,
		"company_revision": 1,
		"errors": [
			{"field": "business_profile.registered_address", "code": "REQUIRED", "message": "registered address is required"}
		]
	}`
	if err := validateJSONAgainstSchema(t, schemas["ValidationResult"].Value, failValResultJSON); err != nil {
		t.Errorf("ValidationResult (failed) schema validation failed: %v", err)
	}

	// 11. Lists: CompanyListResponse & ApplicationListResponse
	compListJSON := `{
		"companies": [` + validCompanyJSON + `],
		"next_cursor": "cursor_999"
	}`
	if err := validateJSONAgainstSchema(t, schemas["CompanyListResponse"].Value, compListJSON); err != nil {
		t.Errorf("CompanyListResponse schema validation failed: %v", err)
	}

	appListJSON := `{
		"applications": [` + validAppJSON + `]
	}`
	if err := validateJSONAgainstSchema(t, schemas["ApplicationListResponse"].Value, appListJSON); err != nil {
		t.Errorf("ApplicationListResponse schema validation failed: %v", err)
	}

	// 12. /submit request, accepted receipt, and validation error shapes
	submitReqJSON := `{"expected_revision": 1}`
	if err := validateJSONAgainstSchema(t, schemas["SubmitApplicationRequest"].Value, submitReqJSON); err != nil {
		t.Errorf("SubmitApplicationRequest schema validation failed: %v", err)
	}
	invalidSubmitReqJSON := `{"expected_revision": 0}`
	if err := validateJSONAgainstSchema(t, schemas["SubmitApplicationRequest"].Value, invalidSubmitReqJSON); err == nil {
		t.Errorf("expected expected_revision 0 to fail min=1 on SubmitApplicationRequest")
	}

	submitRespJSON := `{
		"code": "OK",
		"application_id": "app_123",
		"submission_id": "sub_123"
	}`
	if err := validateJSONAgainstSchema(t, schemas["SubmitApplicationResponse"].Value, submitRespJSON); err != nil {
		t.Errorf("SubmitApplicationResponse schema validation failed: %v", err)
	}

	// 13. CreateCompanyRequest with min/max string limits and ISO country regex
	validCreateCompJSON := `{
		"legal_name": "Valid Company Name",
		"registration_country": "US"
	}`
	if err := validateJSONAgainstSchema(t, schemas["CreateCompanyRequest"].Value, validCreateCompJSON); err != nil {
		t.Errorf("CreateCompanyRequest valid schema failed: %v", err)
	}

	// legal_name min length violation (1 char < min 2)
	shortNameJSON := `{"legal_name": "A"}`
	if err := validateJSONAgainstSchema(t, schemas["CreateCompanyRequest"].Value, shortNameJSON); err == nil {
		t.Errorf("expected 1-char legal_name to fail minLength=2")
	}

	// registration_country regex violation ("USA" != 2 uppercase chars)
	invalidCountryJSON := `{"legal_name": "Valid Name", "registration_country": "USA"}`
	if err := validateJSONAgainstSchema(t, schemas["CreateCompanyRequest"].Value, invalidCountryJSON); err == nil {
		t.Errorf("expected 3-char registration_country to fail pattern ^[A-Z]{2}$")
	}

	// 14. Raw array endpoint schemas
	listDocsPath := opDoc.Paths.Find("/api/onboarding-applications/{id}/documents").Get
	docs200Schema := listDocsPath.Responses.Value("200").Value.Content["application/json"].Schema.Value
	rawDocsArrayJSON := `[` + validDocJSON + `]`
	if err := validateJSONAgainstSchema(t, docs200Schema, rawDocsArrayJSON); err != nil {
		t.Errorf("raw documents array response schema validation failed: %v", err)
	}

	listSubsPath := opDoc.Paths.Find("/api/onboarding-applications/{id}/submissions").Get
	subs200Schema := listSubsPath.Responses.Value("200").Value.Content["application/json"].Schema.Value
	rawSubsArrayJSON := `[` + validSubJSON + `]`
	if err := validateJSONAgainstSchema(t, subs200Schema, rawSubsArrayJSON); err != nil {
		t.Errorf("raw submissions array response schema validation failed: %v", err)
	}
}
