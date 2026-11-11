package apidocs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/apidocs"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
)

// newTestServer returns a Server with nil repository and DenyAllAuthenticator to prove documentation
// operates independently of database persistence, authentication keys, and external clients.
func newTestServer() http.Handler {
	s := internalHttp.NewServer(
		nil,
		internalHttp.DenyAllAuthenticator{},
		validation.UnverifiedPlatformContractGate{},
		storage.NewUnavailableStore("storage unconfigured"),
	)
	s.WithDocsHandler(apidocs.Handler())
	return s.Routes()
}

// TestTask3_PublicDocsAccessWithoutDBAndAuth tests that /docs/api endpoints are fully public
// and accessible even when database and auth are unconfigured.
func TestTask3_PublicDocsAccessWithoutDBAndAuth(t *testing.T) {
	handler := newTestServer()

	// 1. Redirect /docs/api to /docs/api/
	reqRedirect := httptest.NewRequest(http.MethodGet, "/docs/api", nil)
	recRedirect := httptest.NewRecorder()
	handler.ServeHTTP(recRedirect, reqRedirect)

	if recRedirect.Code != http.StatusMovedPermanently {
		t.Errorf("GET /docs/api status = %d, want %d", recRedirect.Code, http.StatusMovedPermanently)
	}
	if loc := recRedirect.Header().Get("Location"); loc != "/docs/api/" {
		t.Errorf("GET /docs/api Location = %q, want %q", loc, "/docs/api/")
	}

	// 2. Index HTML under /docs/api/
	reqIndex := httptest.NewRequest(http.MethodGet, "/docs/api/", nil)
	recIndex := httptest.NewRecorder()
	handler.ServeHTTP(recIndex, reqIndex)

	if recIndex.Code != http.StatusOK {
		t.Errorf("GET /docs/api/ status = %d, want %d", recIndex.Code, http.StatusOK)
	}
	if ct := recIndex.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /docs/api/ Content-Type = %q, want text/html", ct)
	}

	// 3. Operator spec under /docs/api/openapi.json
	reqOp := httptest.NewRequest(http.MethodGet, "/docs/api/openapi.json", nil)
	recOp := httptest.NewRecorder()
	handler.ServeHTTP(recOp, reqOp)

	if recOp.Code != http.StatusOK {
		t.Errorf("GET /docs/api/openapi.json status = %d, want %d", recOp.Code, http.StatusOK)
	}
	if ct := recOp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET /docs/api/openapi.json Content-Type = %q, want application/json", ct)
	}

	// 4. Webhook spec under /docs/api/webhooks.json
	reqWh := httptest.NewRequest(http.MethodGet, "/docs/api/webhooks.json", nil)
	recWh := httptest.NewRecorder()
	handler.ServeHTTP(recWh, reqWh)

	if recWh.Code != http.StatusOK {
		t.Errorf("GET /docs/api/webhooks.json status = %d, want %d", recWh.Code, http.StatusOK)
	}
	if ct := recWh.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET /docs/api/webhooks.json Content-Type = %q, want application/json", ct)
	}
}

// TestTask3_BundledAssetsAndProvenance tests that Swagger UI static assets are bundled locally and served correctly.
func TestTask3_BundledAssetsAndProvenance(t *testing.T) {
	handler := newTestServer()

	assets := []struct {
		path        string
		contentType string
	}{
		{"/docs/api/swagger-ui.css", "text/css"},
		{"/docs/api/swagger-ui-bundle.js", "application/javascript"},
		{"/docs/api/swagger-ui-standalone-preset.js", "application/javascript"},
		{"/docs/api/favicon-32x32.png", "image/png"},
		{"/docs/api/favicon-16x16.png", "image/png"},
		{"/docs/api/VERSION", "text/plain"},
		{"/docs/api/LICENSE", "text/plain"},
		{"/docs/api/NOTICE", "text/plain"},
		{"/docs/api/manifest.json", "application/json"},
	}

	for _, a := range assets {
		t.Run(a.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, a.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("%s status = %d, want %d", a.path, rec.Code, http.StatusOK)
			}
			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, a.contentType) {
				t.Errorf("%s Content-Type = %q, want prefix %q", a.path, ct, a.contentType)
			}
			if len(rec.Body.Bytes()) == 0 {
				t.Errorf("%s returned empty body", a.path)
			}

			// Sub-path alias with /assets/ prefix should also resolve identically
			aliasPath := strings.Replace(a.path, "/docs/api/", "/docs/api/assets/", 1)
			reqAlias := httptest.NewRequest(http.MethodGet, aliasPath, nil)
			recAlias := httptest.NewRecorder()
			handler.ServeHTTP(recAlias, reqAlias)
			if recAlias.Code != http.StatusOK {
				t.Errorf("alias %s status = %d, want %d", aliasPath, recAlias.Code, http.StatusOK)
			}
		})
	}
}

// TestTask3_ReadOnlyUIConfigAndSecurityControls verifies that the Swagger UI HTML has all execution disabled,
// relative URLs configured, and zero external CDN/font references.
func TestTask3_ReadOnlyUIConfigAndSecurityControls(t *testing.T) {
	handler := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/docs/api/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	htmlBody := rec.Body.String()

	// 1. Mandatory read-only controls
	if !strings.Contains(htmlBody, "supportedSubmitMethods: []") {
		t.Errorf("UI HTML missing 'supportedSubmitMethods: []' to disable API execution")
	}
	if !strings.Contains(htmlBody, "tryItOutEnabled: false") {
		t.Errorf("UI HTML missing 'tryItOutEnabled: false'")
	}
	if !strings.Contains(htmlBody, "persistAuthorization: false") {
		t.Errorf("UI HTML missing 'persistAuthorization: false'")
	}
	if !strings.Contains(htmlBody, "validatorUrl: null") {
		t.Errorf("UI HTML missing 'validatorUrl: null' to disable external schema validator request")
	}
	if !strings.Contains(htmlBody, "queryConfigEnabled: false") {
		t.Errorf("UI HTML missing 'queryConfigEnabled: false' to prevent external query parameter injection")
	}

	// 2. Relative URLs for dual spec selectors (works at direct service host and via gateway proxy)
	if !strings.Contains(htmlBody, `url: "./openapi.json"`) {
		t.Errorf("UI HTML missing relative operator spec selector url: ./openapi.json")
	}
	if !strings.Contains(htmlBody, `url: "./webhooks.json"`) {
		t.Errorf("UI HTML missing relative webhook spec selector url: ./webhooks.json")
	}

	// 3. Absolute offline hygiene: zero external CDN or remote font references
	forbiddenStrings := []string{
		"fonts.googleapis.com",
		"fonts.gstatic.com",
		"cdnjs.cloudflare.com",
		"unpkg.com",
		"cdn.jsdelivr.net",
		"petstore.swagger.io",
	}
	for _, forbidden := range forbiddenStrings {
		if strings.Contains(htmlBody, forbidden) {
			t.Errorf("UI HTML contains forbidden external reference %q", forbidden)
		}
	}

	// 4. Required security headers on HTML response
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing X-Content-Type-Options: nosniff header")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing X-Frame-Options: DENY header")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy header invalid or missing: %q", csp)
	}
}

// TestTask3_EmbeddedSpecJSONValidityAndAudienceContent tests that served specs parse as valid OpenAPI 3.1
// and preserve appropriate audience separation.
func TestTask3_EmbeddedSpecJSONValidityAndAudienceContent(t *testing.T) {
	handler := newTestServer()

	// 1. Operator specification
	reqOp := httptest.NewRequest(http.MethodGet, "/docs/api/openapi.json", nil)
	recOp := httptest.NewRecorder()
	handler.ServeHTTP(recOp, reqOp)

	var opJSON map[string]any
	if err := json.Unmarshal(recOp.Body.Bytes(), &opJSON); err != nil {
		t.Fatalf("served operator spec is not valid JSON: %v", err)
	}
	if opJSON["openapi"] != "3.1.0" {
		t.Errorf("operator spec openapi version = %v, want 3.1.0", opJSON["openapi"])
	}

	loader := openapi3.NewLoader()
	opDoc, err := loader.LoadFromData(recOp.Body.Bytes())
	if err != nil {
		t.Fatalf("failed to load served operator spec with kin-openapi: %v", err)
	}
	if err := opDoc.Validate(reqOp.Context()); err != nil {
		t.Errorf("served operator spec failed OpenAPI 3.1 validation: %v", err)
	}

	// Operator spec audience content
	if opDoc.Paths.Find("/api/companies") == nil {
		t.Errorf("served operator spec missing /api/companies")
	}
	if opDoc.Paths.Find("/api/webhooks/airwallex") != nil {
		t.Errorf("served operator spec must NOT contain /api/webhooks/airwallex")
	}

	// 2. Webhook specification
	reqWh := httptest.NewRequest(http.MethodGet, "/docs/api/webhooks.json", nil)
	recWh := httptest.NewRecorder()
	handler.ServeHTTP(recWh, reqWh)

	var whJSON map[string]any
	if err := json.Unmarshal(recWh.Body.Bytes(), &whJSON); err != nil {
		t.Fatalf("served webhook spec is not valid JSON: %v", err)
	}
	if whJSON["openapi"] != "3.1.0" {
		t.Errorf("webhook spec openapi version = %v, want 3.1.0", whJSON["openapi"])
	}

	whDoc, err := loader.LoadFromData(recWh.Body.Bytes())
	if err != nil {
		t.Fatalf("failed to load served webhook spec with kin-openapi: %v", err)
	}
	if err := whDoc.Validate(reqWh.Context()); err != nil {
		t.Errorf("served webhook spec failed OpenAPI 3.1 validation: %v", err)
	}

	// Webhook spec audience content
	if whDoc.Paths.Find("/api/webhooks/airwallex") == nil {
		t.Errorf("served webhook spec missing /api/webhooks/airwallex")
	}
	if whDoc.Paths.Find("/api/companies") != nil {
		t.Errorf("served webhook spec must NOT contain /api/companies")
	}
}

// TestTask3_MethodRestrictionsAndHEAD tests that only GET and HEAD methods are permitted on docs routes.
func TestTask3_MethodRestrictionsAndHEAD(t *testing.T) {
	handler := newTestServer()

	mutatingMethods := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}

	endpoints := []string{
		"/docs/api/",
		"/docs/api/openapi.json",
		"/docs/api/webhooks.json",
		"/docs/api/swagger-ui.css",
	}

	for _, ep := range endpoints {
		for _, method := range mutatingMethods {
			t.Run(method+" "+ep, func(t *testing.T) {
				req := httptest.NewRequest(method, ep, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("%s %s status = %d, want %d", method, ep, rec.Code, http.StatusMethodNotAllowed)
				}
				if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
					t.Errorf("%s %s Allow header = %q, want 'GET, HEAD'", method, ep, allow)
				}
			})
		}

		// HEAD method must succeed with 200 and empty body
		t.Run("HEAD "+ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodHead, ep, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("HEAD %s status = %d, want %d", ep, rec.Code, http.StatusOK)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("HEAD %s returned non-empty body: %d bytes", ep, rec.Body.Len())
			}
		})
	}
}

// TestTask3_PathTraversalAndDirectoryListingRejection verifies that traversal attacks and directory listings fail.
func TestTask3_PathTraversalAndDirectoryListingRejection(t *testing.T) {
	handler := newTestServer()

	traversalPaths := []string{
		"/docs/api/..",
		"/docs/api/../",
		"/docs/api/../../docs/plans",
		"/docs/api/%2e%2e",
		"/docs/api/%2e%2e/",
		"/docs/api/%2e%2e/%2e%2e/plans",
		"/docs/api/assets/../../specs",
		"/docs/api/nonexistent-file.js",
	}

	for _, p := range traversalPaths {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound &&
				rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect &&
				rec.Code != http.StatusPermanentRedirect {
				t.Errorf("GET %s status = %d, want 400, 404, or redirect", p, rec.Code)
			}
			// Must never reveal sensitive files or listing
			body := rec.Body.String()
			if strings.Contains(body, "20261110") || strings.Contains(body, "Directory listing") || strings.Contains(body, "Index of") {
				t.Errorf("GET %s exposed filesystem information: %s", p, body)
			}
		})
	}
}

// TestTask3_UnchangedBusinessAuthAndDBGuards verifies that serving public docs does not degrade or bypass
// business API authorization or database dependency gates.
func TestTask3_UnchangedBusinessAuthAndDBGuards(t *testing.T) {
	handler := newTestServer()

	// 1. Private API endpoints return 401 UNAUTHENTICATED without credentials
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	recAPI := httptest.NewRecorder()
	handler.ServeHTTP(recAPI, reqAPI)

	if recAPI.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/companies status = %d, want %d", recAPI.Code, http.StatusUnauthorized)
	}

	// 2. Health checks remain public
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	handler.ServeHTTP(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", recHealth.Code, http.StatusOK)
	}

	// 3. Unrouted API paths return 401 unauthenticated through auth-gated prefix
	reqUnknown := httptest.NewRequest(http.MethodGet, "/api/unknown-endpoint", nil)
	recUnknown := httptest.NewRecorder()
	handler.ServeHTTP(recUnknown, reqUnknown)

	if recUnknown.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/unknown-endpoint status = %d, want %d", recUnknown.Code, http.StatusUnauthorized)
	}
}
