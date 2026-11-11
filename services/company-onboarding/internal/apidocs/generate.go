package apidocs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

// SchemaCustomizer inspects Go types, JSON tags, and validator tags to produce accurate OpenAPI 3.1 schemas.
func SchemaCustomizer(name string, t reflect.Type, tag reflect.StructTag, schema *openapi3.Schema) error {
	// 0. Exclude json:"-" fields (including when UseAllExportedFields is active)
	jsonTag := tag.Get("json")
	if jsonTag == "-" || strings.HasPrefix(jsonTag, "-,") {
		return &openapi3gen.ExcludeSchemaSentinel{}
	}

	// 1. Reflect domain.Company root via its serialization envelope (c.MarshalJSON -> companyJSON)
	if t == reflect.TypeOf(domain.Company{}) || t == reflect.TypeOf(&domain.Company{}) {
		schema.Type = &openapi3.Types{"object"}
		schema.Description = "Domain Company aggregate root serialized via custom MarshalJSON."
		minRev := float64(1)
		schema.Properties = map[string]*openapi3.SchemaRef{
			"company_id": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"status": {Value: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Enum:        []any{string(domain.CompanyStatusActive), string(domain.CompanyStatusArchived)},
				Description: "Company lifecycle status: ACTIVE or ARCHIVED",
			}},
			"revision":              {Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}, Min: &minRev}},
			"created_at":            {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "date-time"}},
			"updated_at":            {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "date-time"}},
			"has_submitted_history": {Value: &openapi3.Schema{Type: &openapi3.Types{"boolean"}}},
			"legal_name":            {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"trading_name":          {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"entity_type":           {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"registration_country":  {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"registration_state":    {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"registration_identifiers": {Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
			}},
			"business_profile": {Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
			"people": {Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
			}},
			"legal_identifier_audit_trail": {Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
			}},
		}
		schema.Required = []string{
			"business_profile", "company_id", "created_at", "has_submitted_history",
			"people", "registration_identifiers", "revision", "status", "updated_at",
		}
		return nil
	}

	// 2. Date-only fields (domain.Date)
	if t == reflect.TypeOf(domain.Date("")) {
		schema.Type = &openapi3.Types{"string"}
		schema.Format = "date"
		schema.Pattern = `^\d{4}-\d{2}-\d{2}$`
		schema.Description = "Calendar date in ISO 8601 YYYY-MM-DD format"
		return nil
	}

	// 3. Standard timestamp (time.Time)
	if t == reflect.TypeOf(time.Time{}) {
		schema.Type = &openapi3.Types{"string"}
		schema.Format = "date-time"
		return nil
	}

	// 4. Nullable pointer primitive fields in OpenAPI 3.1
	if t.Kind() == reflect.Pointer {
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}

		if elem == reflect.TypeOf(time.Time{}) {
			schema.Type = &openapi3.Types{"string", "null"}
			schema.Format = "date-time"
			schema.Nullable = false
			return nil
		}
		if elem == reflect.TypeOf(domain.Date("")) {
			schema.Type = &openapi3.Types{"string", "null"}
			schema.Format = "date"
			schema.Pattern = `^\d{4}-\d{2}-\d{2}$`
			schema.Nullable = false
			return nil
		}
		if elem.Kind() == reflect.String {
			schema.Type = &openapi3.Types{"string", "null"}
			schema.Nullable = false
		} else if elem.Kind() == reflect.Int || elem.Kind() == reflect.Int64 {
			schema.Type = &openapi3.Types{"integer", "null"}
			schema.Nullable = false
		} else if elem.Kind() == reflect.Bool {
			schema.Type = &openapi3.Types{"boolean", "null"}
			schema.Nullable = false
		}
	}

	// 5. Clear uuid format assumption from opaque prefixed IDs
	if name == "id" || strings.HasSuffix(name, "_id") {
		if schema.Format == "uuid" {
			schema.Format = ""
		}
		schema.Type = &openapi3.Types{"string"}
	}

	// 6. Revision fields
	if name == "revision" || name == "expected_revision" {
		min := float64(1)
		schema.Min = &min
		schema.Type = &openapi3.Types{"integer"}
		return nil
	}

	// 7. Parse validation tags from go-playground validator
	vTag := tag.Get("validate")
	if vTag != "" {
		elemType := t
		for elemType.Kind() == reflect.Pointer {
			elemType = elemType.Elem()
		}

		// Handle dive: rules before dive apply to container, rules after dive apply to items
		containerTag := vTag
		elemTag := ""
		if diveIdx := strings.Index(vTag, "dive"); diveIdx != -1 {
			containerTag = strings.Trim(vTag[:diveIdx], ",")
			elemTag = strings.Trim(vTag[diveIdx+len("dive"):], ",")
		}

		if containerTag != "" {
			parts := strings.Split(containerTag, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				switch {
				case strings.HasPrefix(part, "min="):
					if val, err := strconv.ParseFloat(strings.TrimPrefix(part, "min="), 64); err == nil {
						switch elemType.Kind() {
						case reflect.String:
							schema.MinLength = uint64(val)
						case reflect.Slice, reflect.Array:
							schema.MinItems = uint64(val)
						default:
							schema.Min = &val
						}
					}
				case strings.HasPrefix(part, "max="):
					if val, err := strconv.ParseFloat(strings.TrimPrefix(part, "max="), 64); err == nil {
						u := uint64(val)
						switch elemType.Kind() {
						case reflect.String:
							schema.MaxLength = &u
						case reflect.Slice, reflect.Array:
							schema.MaxItems = &u
						default:
							schema.Max = &val
						}
					}
				case part == "iso3166_alpha2":
					u := uint64(2)
					schema.MinLength = 2
					schema.MaxLength = &u
					schema.Pattern = `^[A-Z]{2}$`
					schema.Description = "ISO 3166-1 alpha-2 uppercase country code (format validation via regex ^[A-Z]{2}$; domain membership validated at runtime)"
				case part == "url":
					schema.Format = "uri"
				case part == "past_date":
					schema.Format = "date"
					schema.Pattern = `^\d{4}-\d{2}-\d{2}$`
					schema.Description = "Calendar date in YYYY-MM-DD format. Subject to dynamic runtime validation ensuring date is strictly in the past relative to execution time."
				}
			}
		}

		if elemTag != "" && schema.Items != nil && schema.Items.Value != nil {
			elemParts := strings.Split(elemTag, ",")
			for _, part := range elemParts {
				part = strings.TrimSpace(part)
				switch {
				case part == "iso3166_alpha2":
					u := uint64(2)
					schema.Items.Value.MinLength = 2
					schema.Items.Value.MaxLength = &u
					schema.Items.Value.Pattern = `^[A-Z]{2}$`
					schema.Items.Value.Description = "ISO 3166-1 alpha-2 uppercase country code (format validation via regex ^[A-Z]{2}$; domain membership validated at runtime)"
				case part == "url":
					schema.Items.Value.Format = "uri"
				case part == "past_date":
					schema.Items.Value.Format = "date"
					schema.Items.Value.Pattern = `^\d{4}-\d{2}-\d{2}$`
					schema.Items.Value.Description = "Calendar date in YYYY-MM-DD format. Subject to dynamic runtime validation ensuring date is strictly in the past relative to execution time."
				}
			}
		}
	}

	return nil
}

// populateRequiredFields scans struct types and sets schema.Required accurately.
// For request DTOs: fields with validate:"required" are required.
// For response DTOs: non-omitempty fields are required.
func populateRequiredFields(t reflect.Type, schema *openapi3.Schema) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || schema == nil || schema.Properties == nil {
		return
	}

	isRequest := strings.HasSuffix(t.Name(), "Request") || strings.HasSuffix(t.Name(), "DTO")

	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		parts := strings.Split(jsonTag, ",")
		fieldName := parts[0]
		if fieldName == "" {
			fieldName = f.Name
		}
		hasOmitempty := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				hasOmitempty = true
				break
			}
		}

		vTag := f.Tag.Get("validate")
		vParts := strings.Split(vTag, ",")
		hasValidateRequired := false
		for _, vp := range vParts {
			if strings.TrimSpace(vp) == "required" {
				hasValidateRequired = true
				break
			}
		}

		if isRequest {
			if hasValidateRequired {
				required = append(required, fieldName)
			}
		} else {
			if !hasOmitempty {
				required = append(required, fieldName)
			}
		}

		// Recurse into nested struct fields
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
			ft = ft.Elem()
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
		}
		if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Time{}) && ft != reflect.TypeOf(domain.Date("")) {
			prop := schema.Properties[fieldName]
			if prop != nil && prop.Value != nil {
				populateRequiredFields(ft, prop.Value)
			}
		}
	}

	if len(required) > 0 {
		sort.Strings(required)
		schema.Required = required
	}
}

// normalizeOpenAPI31 recursively converts OpenAPI 3.0 nullable:true into OpenAPI 3.1 JSON Schema types.
func normalizeOpenAPI31(schema *openapi3.Schema) {
	if schema == nil {
		return
	}

	if schema.Nullable {
		schema.Nullable = false
		if schema.Type != nil && len(*schema.Type) > 0 {
			hasNull := false
			for _, t := range *schema.Type {
				if t == "null" {
					hasNull = true
					break
				}
			}
			if !hasNull {
				*schema.Type = append(*schema.Type, "null")
			}
		} else if len(schema.Properties) > 0 {
			schema.Type = &openapi3.Types{"object", "null"}
		} else if schema.Items != nil {
			schema.Type = &openapi3.Types{"array", "null"}
		}
	}

	for _, propRef := range schema.Properties {
		if propRef != nil && propRef.Value != nil {
			normalizeOpenAPI31(propRef.Value)
		}
	}

	if schema.Items != nil && schema.Items.Value != nil {
		normalizeOpenAPI31(schema.Items.Value)
	}

	if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
		normalizeOpenAPI31(schema.AdditionalProperties.Schema.Value)
	}

	for _, sub := range schema.AllOf {
		if sub != nil && sub.Value != nil {
			normalizeOpenAPI31(sub.Value)
		}
	}
	for _, sub := range schema.AnyOf {
		if sub != nil && sub.Value != nil {
			normalizeOpenAPI31(sub.Value)
		}
	}
	for _, sub := range schema.OneOf {
		if sub != nil && sub.Value != nil {
			normalizeOpenAPI31(sub.Value)
		}
	}
}

// normalizeDocOpenAPI31 walks every schema in the openapi3.T document and normalizes to OpenAPI 3.1.
func normalizeDocOpenAPI31(doc *openapi3.T) {
	if doc == nil {
		return
	}
	if doc.Components != nil {
		for _, sRef := range doc.Components.Schemas {
			if sRef != nil && sRef.Value != nil {
				normalizeOpenAPI31(sRef.Value)
			}
		}
	}
	if doc.Paths != nil {
		for _, pathItem := range doc.Paths.Map() {
			if pathItem == nil {
				continue
			}
			for _, op := range pathItem.Operations() {
				if op == nil {
					continue
				}
				for _, param := range op.Parameters {
					if param != nil && param.Value != nil && param.Value.Schema != nil && param.Value.Schema.Value != nil {
						normalizeOpenAPI31(param.Value.Schema.Value)
					}
				}
				if op.RequestBody != nil && op.RequestBody.Value != nil {
					for _, mt := range op.RequestBody.Value.Content {
						if mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
							normalizeOpenAPI31(mt.Schema.Value)
						}
					}
				}
				if op.Responses != nil {
					for _, resp := range op.Responses.Map() {
						if resp != nil && resp.Value != nil {
							for _, mt := range resp.Value.Content {
								if mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
									normalizeOpenAPI31(mt.Schema.Value)
								}
							}
						}
					}
				}
			}
		}
	}
}

// GenerateOperatorSpec constructs the OpenAPI 3.1.0 document for the operator and public API.
func GenerateOperatorSpec() (*openapi3.T, error) {
	ctx := context.Background()

	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(SchemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	// Build reusable component schemas
	schemas := make(openapi3.Schemas)

	dtoMap := map[string]any{
		"CompanyResponse":          internalHttp.CompanyResponse{},
		"CreateCompanyRequest":     internalHttp.CreateCompanyRequest{},
		"EditCompanyRequest":       internalHttp.EditCompanyRequest{},
		"ArchiveCompanyRequest":    internalHttp.ArchiveCompanyRequest{},
		"CompanyListResponse":      internalHttp.CompanyListResponse{},
		"ApplicationResponse":      internalHttp.ApplicationResponse{},
		"CreateApplicationRequest": internalHttp.CreateApplicationRequest{},
		"EditApplicationRequest":   internalHttp.EditApplicationRequest{},
		"CancelApplicationRequest": internalHttp.CancelApplicationRequest{},
		"SubmitApplicationRequest": internalHttp.SubmitApplicationRequest{},
		"ApplicationListResponse":  internalHttp.ApplicationListResponse{},
		"PlatformAccountResponse":  internalHttp.PlatformAccountResponse{},
		"SubmissionResponse":       internalHttp.SubmissionResponse{},
		"DocumentResponse":         internalHttp.DocumentResponse{},
		"ActionResponse":           internalHttp.ActionResponse{},
		"ErrorResponse":            internalHttp.ErrorResponse{},
		"FieldError":               validation.FieldError{},
		"ValidationResult":         validation.ValidationResult{},
	}

	schemaNames := make([]string, 0, len(dtoMap))
	for name := range dtoMap {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)

	for _, name := range schemaNames {
		val := dtoMap[name]
		sRef, err := gen.NewSchemaRefForValue(val, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to generate schema for %s: %w", name, err)
		}
		populateRequiredFields(reflect.TypeOf(val), sRef.Value)
		schemas[name] = sRef
	}

	// Scope exact status enums by DTO aggregate per domain boundaries
	if compResp := schemas["CompanyResponse"]; compResp != nil && compResp.Value != nil && compResp.Value.Properties["status"] != nil {
		compResp.Value.Properties["status"].Value.Type = &openapi3.Types{"string"}
		compResp.Value.Properties["status"].Value.Enum = []any{
			string(domain.CompanyStatusActive),
			string(domain.CompanyStatusArchived),
		}
		compResp.Value.Properties["status"].Value.Description = "Company lifecycle status: ACTIVE or ARCHIVED"
	}

	if appResp := schemas["ApplicationResponse"]; appResp != nil && appResp.Value != nil && appResp.Value.Properties["status"] != nil {
		appResp.Value.Properties["status"].Value.Type = &openapi3.Types{"string"}
		appResp.Value.Properties["status"].Value.Enum = []any{
			string(domain.ApplicationStatusDraft),
			string(domain.ApplicationStatusSubmissionPending),
			string(domain.ApplicationStatusUnderReview),
			string(domain.ApplicationStatusInformationRequired),
			string(domain.ApplicationStatusCompleted),
			string(domain.ApplicationStatusRejected),
			string(domain.ApplicationStatusCancelled),
		}
		appResp.Value.Properties["status"].Value.Description = "Application underwriting lifecycle status"
		if closedAt := appResp.Value.Properties["closed_at"]; closedAt != nil && closedAt.Value != nil {
			closedAt.Value.Type = &openapi3.Types{"string", "null"}
			closedAt.Value.Format = "date-time"
			closedAt.Value.Nullable = false
			closedAt.Value.Description = "Timestamp when application reached terminal status (null/omitted while active)"
		}
	}

	if subResp := schemas["SubmissionResponse"]; subResp != nil && subResp.Value != nil && subResp.Value.Properties["status"] != nil {
		subResp.Value.Properties["status"].Value.Type = &openapi3.Types{"string"}
		subResp.Value.Properties["status"].Value.Enum = []any{
			string(domain.SubmissionStatusPending),
			string(domain.SubmissionStatusAccepted),
			string(domain.SubmissionStatusFailed),
			string(domain.SubmissionStatusUnresolved),
		}
		subResp.Value.Properties["status"].Value.Description = "Asynchronous platform submission delivery status"
	}

	if paResp := schemas["PlatformAccountResponse"]; paResp != nil && paResp.Value != nil && paResp.Value.Properties["status"] != nil {
		paResp.Value.Properties["status"].Value.Type = &openapi3.Types{"string"}
		paResp.Value.Properties["status"].Value.Enum = nil // Open string: raw platform statuses accepted
		paResp.Value.Properties["status"].Value.Description = "Observed platform account status (e.g. UNOBSERVED, CREATED, SUBMITTED, PENDING, ACTIVE, ACTION_REQUIRED, SUSPENDED, CLOSED). Unknown/unmapped statuses are accepted."
		if syncTime := paResp.Value.Properties["last_sync_time"]; syncTime != nil && syncTime.Value != nil {
			syncTime.Value.Type = &openapi3.Types{"string", "null"}
			syncTime.Value.Format = "date-time"
			syncTime.Value.Nullable = false
		}
	}

	// Add exact unauthenticated error schema variants for Node gateway and Go service JWT
	schemas["GatewayAuthError"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:        &openapi3.Types{"object"},
			Description: "Node gateway session authentication failure response (HTTP 401)",
			Required:    []string{"error"},
			Properties: map[string]*openapi3.SchemaRef{
				"error": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "operator_auth_required"}},
			},
		},
	}

	schemas["ServiceAuthError"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:        &openapi3.Types{"object"},
			Description: "Direct Go service JWT assertion authentication failure response (HTTP 401)",
			Required:    []string{"code", "message"},
			Properties: map[string]*openapi3.SchemaRef{
				"code":    {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "UNAUTHENTICATED"}},
				"message": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "invalid or missing authentication token"}},
				"error":   {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "unauthorized"}},
			},
		},
	}

	schemas["UnauthenticatedResponse"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Description: "Unauthenticated error response (HTTP 401). Returned as GatewayAuthError from Node gateway (kit5_operator_session missing/invalid), or ServiceAuthError from direct Go service JWT assertion failure.",
			OneOf: []*openapi3.SchemaRef{
				{Ref: "#/components/schemas/GatewayAuthError"},
				{Ref: "#/components/schemas/ServiceAuthError"},
			},
		},
	}

	// Assemble root OpenAPI 3.1 document
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info: &openapi3.Info{
			Title:       "FlowCard Company Onboarding Operator API",
			Version:     "1.0.0",
			Description: "Authoritative underwriting platform company onboarding and document management API. Business endpoints are auth-gated via kit5_operator_session cookie at the Node gateway proxy (or internal ServiceJWT assertion) and require database persistence. Application submission (/submit) is fail-closed blocked.",
		},
		Servers: openapi3.Servers{
			{URL: "/", Description: "Current environment (relative gateway proxy or direct service host)"},
		},
		Paths: openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: schemas,
			SecuritySchemes: openapi3.SecuritySchemes{
				"GatewaySessionCookie": &openapi3.SecuritySchemeRef{
					Value: &openapi3.SecurityScheme{
						Type:        "apiKey",
						In:          "cookie",
						Name:        "kit5_operator_session",
						Description: "Kit 5 operator session cookie (kit5_operator_session). Established via POST /api/kit5/session with operator token, origin authorization, and 8-hour TTL (Set-Cookie: kit5_operator_session=...; Path=/api; HttpOnly; Secure; SameSite=None; Max-Age=28800). Authenticated at Node gateway proxy before forwarding to Go service with actor context.",
					},
				},
				"ServiceJWT": &openapi3.SecuritySchemeRef{
					Value: &openapi3.SecurityScheme{
						Type:         "http",
						Scheme:       "bearer",
						BearerFormat: "JWT",
						Description:  "Internal service-to-service JWT assertion for direct Go service access (Issuer: flowcard-node-proxy, Audience: flowcard-onboarding-service).",
					},
				},
			},
		},
	}

	// Populate paths from operations registry
	for _, op := range AllOperations() {
		if op.Audience != "operator" && op.Audience != "public" {
			continue
		}
		attachOperation(doc, op)
	}

	// Normalize all schemas in the document to native OpenAPI 3.1 JSON Schema null union types
	normalizeDocOpenAPI31(doc)

	// Serialize and load via loader to verify references and spec validity
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal operator spec: %w", err)
	}

	loader := openapi3.NewLoader()
	loadedDoc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load operator spec with loader: %w", err)
	}

	if err := loadedDoc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("operator spec failed OpenAPI 3.1 validation: %w", err)
	}

	return loadedDoc, nil
}

// GenerateWebhookSpec constructs the OpenAPI 3.1.0 document for the internal webhook receiver API.
func GenerateWebhookSpec() (*openapi3.T, error) {
	ctx := context.Background()

	gen := openapi3gen.NewGenerator(
		openapi3gen.SchemaCustomizer(SchemaCustomizer),
		openapi3gen.UseAllExportedFields(),
	)

	schemas := make(openapi3.Schemas)

	payloadRef, err := gen.NewSchemaRefForValue(webhooks.InboundWebhookPayload{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate schema for InboundWebhookPayload: %w", err)
	}
	populateRequiredFields(reflect.TypeOf(webhooks.InboundWebhookPayload{}), payloadRef.Value)
	schemas["InboundWebhookPayload"] = payloadRef

	ackRef, err := gen.NewSchemaRefForValue(webhooks.WebhookAcknowledgeResponse{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate schema for WebhookAcknowledgeResponse: %w", err)
	}
	populateRequiredFields(reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}), ackRef.Value)
	schemas["WebhookAcknowledgeResponse"] = ackRef

	errRespRef, err := gen.NewSchemaRefForValue(internalHttp.ErrorResponse{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate schema for ErrorResponse: %w", err)
	}
	populateRequiredFields(reflect.TypeOf(internalHttp.ErrorResponse{}), errRespRef.Value)
	schemas["ErrorResponse"] = errRespRef

	schemas["WebhookErrorResponse"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:     &openapi3.Types{"object"},
			Required: []string{"error"},
			Properties: map[string]*openapi3.SchemaRef{
				"error": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "invalid json payload"}},
			},
		},
	}

	schemas["UnauthenticatedResponse"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:        &openapi3.Types{"object"},
			Description: "Internal webhook service JWT assertion failure response (HTTP 401)",
			Required:    []string{"code", "message"},
			Properties: map[string]*openapi3.SchemaRef{
				"code":    {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "UNAUTHENTICATED"}},
				"message": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "invalid or missing authentication token"}},
				"error":   {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Example: "unauthorized"}},
			},
		},
	}

	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info: &openapi3.Info{
			Title:       "FlowCard Onboarding Webhook Receiver API",
			Version:     "1.0.0",
			Description: "Internal receiver endpoint for platform webhook events forwarded by the Node gateway proxy. Requires service-to-service JWT assertion with audience flowcard-onboarding-webhooks. Events are durably persisted to inbox before acknowledgement.",
		},
		Servers: openapi3.Servers{
			{URL: "/", Description: "Current environment (relative gateway or direct service)"},
		},
		Paths: openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: schemas,
			SecuritySchemes: openapi3.SecuritySchemes{
				"WebhookServiceJWT": &openapi3.SecuritySchemeRef{
					Value: &openapi3.SecurityScheme{
						Type:         "http",
						Scheme:       "bearer",
						BearerFormat: "JWT",
						Description:  "Internal service-to-service JWT assertion for webhook forwarding (Issuer: flowcard-node-proxy, Audience: flowcard-onboarding-webhooks, Subject: node-webhook-forwarder).",
					},
				},
			},
		},
	}

	// Populate webhook receiver routes as regular paths in this dedicated specification
	for _, op := range AllOperations() {
		if op.Audience == "internal-webhook" {
			attachOperation(doc, op)
		}
	}

	// Normalize all schemas to native OpenAPI 3.1 JSON Schema null union types
	normalizeDocOpenAPI31(doc)

	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook spec: %w", err)
	}

	loader := openapi3.NewLoader()
	loadedDoc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load webhook spec with loader: %w", err)
	}

	if err := loadedDoc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("webhook spec failed OpenAPI 3.1 validation: %w", err)
	}

	return loadedDoc, nil
}

// attachOperation wires an OperationDefinition to the openapi3.T document.
func attachOperation(doc *openapi3.T, op OperationDefinition) {
	pathItem := doc.Paths.Find(op.Path)
	if pathItem == nil {
		pathItem = &openapi3.PathItem{}
		doc.Paths.Set(op.Path, pathItem)
	}

	kinOp := &openapi3.Operation{
		OperationID: op.ID,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        op.Tags,
		Responses:   &openapi3.Responses{},
	}

	// Security
	if len(op.Security) > 0 {
		reqs := make(openapi3.SecurityRequirements, 0, len(op.Security))
		for _, sec := range op.Security {
			reqs = append(reqs, openapi3.SecurityRequirement(sec))
		}
		kinOp.Security = &reqs
	} else if op.Audience == "public" {
		emptyReqs := make(openapi3.SecurityRequirements, 0)
		kinOp.Security = &emptyReqs
	}

	// Parameters
	for _, p := range op.Parameters {
		kinParam := &openapi3.Parameter{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Description: p.Description,
			Schema:      &openapi3.SchemaRef{Value: p.Schema},
		}
		if p.In == "path" {
			kinParam.Required = true
		}
		kinOp.Parameters = append(kinOp.Parameters, &openapi3.ParameterRef{Value: kinParam})
	}

	// Request Body
	if op.RequestBody != nil {
		rb := &openapi3.RequestBody{
			Description: op.RequestBody.Description,
			Required:    op.RequestBody.Required,
			Content:     make(openapi3.Content),
		}

		if op.RequestBody.IsMultipart {
			props := make(map[string]*openapi3.SchemaRef)
			fNames := make([]string, 0, len(op.RequestBody.MultipartFields))
			for fName := range op.RequestBody.MultipartFields {
				fNames = append(fNames, fName)
			}
			sort.Strings(fNames)

			requiredFields := make([]string, 0)
			for _, fName := range fNames {
				fDef := op.RequestBody.MultipartFields[fName]
				s := &openapi3.Schema{
					Type:        &openapi3.Types{fDef.Type},
					Description: fDef.Description,
				}
				if fDef.Format != "" {
					s.Format = fDef.Format
				}
				props[fName] = &openapi3.SchemaRef{Value: s}
				if fDef.Required {
					requiredFields = append(requiredFields, fName)
				}
			}
			rb.Content[op.RequestBody.ContentType] = &openapi3.MediaType{
				Schema: &openapi3.SchemaRef{
					Value: &openapi3.Schema{
						Type:       &openapi3.Types{"object"},
						Properties: props,
						Required:   requiredFields,
					},
				},
			}
		} else if op.RequestBody.Type != nil {
			refName := op.RequestBody.Type.Name()
			rb.Content[op.RequestBody.ContentType] = &openapi3.MediaType{
				Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + refName},
			}
		}

		kinOp.RequestBody = &openapi3.RequestBodyRef{Value: rb}
	}

	// Responses in sorted HTTP status code order
	statusCodes := make([]int, 0, len(op.Responses))
	for status := range op.Responses {
		statusCodes = append(statusCodes, status)
	}
	sort.Ints(statusCodes)

	for _, status := range statusCodes {
		respDef := op.Responses[status]
		statusStr := strconv.Itoa(status)
		kinResp := &openapi3.Response{
			Description: &respDef.Description,
			Content:     make(openapi3.Content),
		}

		// Content Types and Schemas
		for _, ct := range respDef.ContentTypes {
			var sRef *openapi3.SchemaRef
			if respDef.IsBinary {
				sRef = &openapi3.SchemaRef{
					Value: &openapi3.Schema{
						Type:   &openapi3.Types{"string"},
						Format: "binary",
					},
				}
			} else if respDef.IsRawArray && respDef.Type != nil {
				elemName := respDef.Type.Elem().Name()
				sRef = &openapi3.SchemaRef{
					Value: &openapi3.Schema{
						Type: &openapi3.Types{"array"},
						Items: &openapi3.SchemaRef{
							Ref: "#/components/schemas/" + elemName,
						},
					},
				}
			} else if respDef.Type != nil {
				typeName := respDef.Type.Name()
				if typeName == "" {
					// Fallback for map[string]string or map[string]any
					if status == http.StatusUnauthorized {
						sRef = &openapi3.SchemaRef{Ref: "#/components/schemas/UnauthenticatedResponse"}
					} else if op.Audience == "internal-webhook" && status >= 400 {
						sRef = &openapi3.SchemaRef{Ref: "#/components/schemas/WebhookErrorResponse"}
					} else {
						sRef = &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"object"}},
						}
					}
				} else {
					sRef = &openapi3.SchemaRef{Ref: "#/components/schemas/" + typeName}
				}
			}

			kinResp.Content[ct] = &openapi3.MediaType{Schema: sRef}
		}

		// Response Headers in sorted order
		if len(respDef.Headers) > 0 {
			kinResp.Headers = make(openapi3.Headers)
			hNames := make([]string, 0, len(respDef.Headers))
			for hName := range respDef.Headers {
				hNames = append(hNames, hName)
			}
			sort.Strings(hNames)

			for _, hName := range hNames {
				desc := respDef.Headers[hName]
				kinResp.Headers[hName] = &openapi3.HeaderRef{
					Value: &openapi3.Header{
						Parameter: openapi3.Parameter{
							Description: desc,
							Schema:      &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
						},
					},
				}
			}
		}

		kinOp.Responses.Set(statusStr, &openapi3.ResponseRef{Value: kinResp})
	}

	// Attach by HTTP method
	switch strings.ToUpper(op.Method) {
	case http.MethodGet:
		pathItem.Get = kinOp
	case http.MethodPost:
		pathItem.Post = kinOp
	case http.MethodPatch:
		pathItem.Patch = kinOp
	case http.MethodPut:
		pathItem.Put = kinOp
	case http.MethodDelete:
		pathItem.Delete = kinOp
	}
}

// WriteSpecs generates both OpenAPI specs and either verifies or writes them to disk deterministically.
func WriteSpecs(repoRoot string, moduleRoot string, checkOnly bool) error {
	opDoc, err := GenerateOperatorSpec()
	if err != nil {
		return fmt.Errorf("failed to generate operator spec: %w", err)
	}

	whDoc, err := GenerateWebhookSpec()
	if err != nil {
		return fmt.Errorf("failed to generate webhook spec: %w", err)
	}

	// Deterministic serialization with 2 spaces indent + trailing newline
	opBytes, err := json.MarshalIndent(opDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal operator spec: %w", err)
	}
	opBytes = append(opBytes, '\n')

	whBytes, err := json.MarshalIndent(whDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal webhook spec: %w", err)
	}
	whBytes = append(whBytes, '\n')

	// Verify absence of OpenAPI 3.0 "nullable": true
	if bytes.Contains(opBytes, []byte(`"nullable": true`)) {
		return fmt.Errorf("operator spec contains legacy OpenAPI 3.0 nullable: true")
	}
	if bytes.Contains(whBytes, []byte(`"nullable": true`)) {
		return fmt.Errorf("webhook spec contains legacy OpenAPI 3.0 nullable: true")
	}

	targets := map[string][]byte{
		filepath.Join(repoRoot, "docs", "api", "openapi.json"):                     opBytes,
		filepath.Join(repoRoot, "docs", "api", "webhooks.json"):                    whBytes,
		filepath.Join(moduleRoot, "internal", "apidocs", "specs", "openapi.json"):  opBytes,
		filepath.Join(moduleRoot, "internal", "apidocs", "specs", "webhooks.json"): whBytes,
	}

	if checkOnly {
		for path, expectedBytes := range targets {
			actualBytes, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("spec check failed: target file missing %s: %w", path, err)
			}
			if !bytes.Equal(actualBytes, expectedBytes) {
				return fmt.Errorf("spec check failed: stale generated specification at %s (run go run ./cmd/openapi to update)", path)
			}
		}
		return nil
	}

	for path, content := range targets {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return fmt.Errorf("failed to write spec to %s: %w", path, err)
		}
	}

	return nil
}
