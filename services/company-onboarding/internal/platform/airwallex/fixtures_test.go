package airwallex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSanitizedFixtures verifies that recorded Airwallex contract fixtures
// parse as valid JSON, conform to expected structural shapes documented in official
// API references, and pass a limited structural scan against common live credential keys.
//
// NOTE: A key-name scan is a limited heuristic check, NOT cryptographic or absolute
// proof of zero secrets or PII.
func TestSanitizedFixtures(t *testing.T) {
	t.Parallel()

	fixtureFiles := []string{
		"account_create_request.json",
		"account_create_response.json",
		"account_submit_response.json",
		"file_upload_response.json",
		"webhook_account_submitted.json",
		"webhook_account_active.json",
		"webhook_account_action_required.json",
		"webhook_account_suspended.json",
		"error_invalid_field_400.json",
		"error_rate_limit_429.json",
		"error_server_error_500.json",
	}

	// Limited structural key scan (heuristic check only)
	disallowedKeys := []string{"api_key", "secret_key", "password", "private_key", "bearer_token"}

	for _, fileName := range fixtureFiles {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			filePath := filepath.Join("testdata", fileName)
			bytes, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("ReadFile(%q) failed: %v", filePath, err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(bytes, &parsed); err != nil {
				t.Fatalf("json.Unmarshal(%q) failed: %v", filePath, err)
			}

			// Limited structural scan for suspicious credential keys
			rawLower := strings.ToLower(string(bytes))
			for _, key := range disallowedKeys {
				if strings.Contains(rawLower, `"`+key+`"`) {
					t.Errorf("fixture %q contains forbidden credential key %q", fileName, key)
				}
			}

			// Structural shape and type assertions based on official documentation
			switch fileName {
			case "account_create_request.json":
				details, ok := parsed["account_details"].(map[string]any)
				if !ok {
					t.Fatalf("%s missing object field 'account_details'", fileName)
				}
				if let, ok := details["legal_entity_type"].(string); !ok || let != "BUSINESS" {
					t.Errorf("%s legal_entity_type = %v, want BUSINESS", fileName, let)
				}
				biz, ok := details["business_details"].(map[string]any)
				if !ok {
					t.Fatalf("%s missing object field 'business_details'", fileName)
				}
				if name, ok := biz["business_name"].(string); !ok || name == "" {
					t.Errorf("%s missing non-empty 'business_name'", fileName)
				}
				if bs, ok := biz["business_structure"].(string); !ok || bs == "" {
					t.Errorf("%s missing non-empty 'business_structure'", fileName)
				}
				agreements, ok := parsed["customer_agreements"].(map[string]any)
				if !ok {
					t.Fatalf("%s missing object field 'customer_agreements'", fileName)
				}
				if _, hasPerson := agreements["accepting_person_id"]; hasPerson {
					t.Errorf("%s must not contain 'accepting_person_id'", fileName)
				}
				tc, ok := agreements["terms_and_conditions"].(map[string]any)
				if !ok {
					t.Fatalf("%s missing 'terms_and_conditions' object", fileName)
				}
				if agreementType, ok := tc["service_agreement_type"].(string); !ok || agreementType != "FULL" {
					t.Errorf("%s terms_and_conditions service_agreement_type = %v, want FULL", fileName, agreementType)
				}

			case "account_create_response.json":
				if id, ok := parsed["id"].(string); !ok || id == "" {
					t.Errorf("%s missing non-empty string 'id'", fileName)
				}
				if status, ok := parsed["status"].(string); !ok || status != "CREATED" {
					t.Errorf("%s status = %v, want CREATED", fileName, status)
				}

			case "account_submit_response.json":
				if id, ok := parsed["id"].(string); !ok || id == "" {
					t.Errorf("%s missing non-empty string 'id'", fileName)
				}
				if status, ok := parsed["status"].(string); !ok || status != "SUBMITTED" {
					t.Errorf("%s status = %v, want SUBMITTED", fileName, status)
				}

			case "file_upload_response.json":
				if fileID, ok := parsed["file_id"].(string); !ok || fileID == "" {
					t.Errorf("%s missing non-empty string 'file_id'", fileName)
				}
				if size, ok := parsed["size"].(float64); !ok || size <= 0 {
					t.Errorf("%s missing positive number 'size'", fileName)
				}

			case "webhook_account_submitted.json":
				assertWebhookEnvelope(t, fileName, parsed, "account.submitted")

			case "webhook_account_active.json":
				assertWebhookEnvelope(t, fileName, parsed, "account.active")

			case "webhook_account_action_required.json":
				assertWebhookEnvelope(t, fileName, parsed, "account.action_required")

			case "webhook_account_suspended.json":
				assertWebhookEnvelope(t, fileName, parsed, "account.suspended")

			case "error_invalid_field_400.json":
				code, ok := parsed["code"].(string)
				if !ok || code == "" {
					t.Errorf("%s missing non-empty string 'code'", fileName)
				}
				msg, ok := parsed["message"].(string)
				if !ok || msg == "" {
					t.Errorf("%s missing non-empty string 'message'", fileName)
				}
				if source, ok := parsed["source"].(string); !ok || source == "" {
					t.Errorf("%s missing non-empty string 'source'", fileName)
				}

			case "error_rate_limit_429.json":
				code, ok := parsed["code"].(string)
				if !ok || code == "" {
					t.Errorf("%s missing non-empty string 'code'", fileName)
				}
				msg, ok := parsed["message"].(string)
				if !ok || msg == "" {
					t.Errorf("%s missing non-empty string 'message'", fileName)
				}

			case "error_server_error_500.json":
				code, ok := parsed["code"].(string)
				if !ok || code == "" {
					t.Errorf("%s missing non-empty string 'code'", fileName)
				}
				msg, ok := parsed["message"].(string)
				if !ok || msg == "" {
					t.Errorf("%s missing non-empty string 'message'", fileName)
				}
			}
		})
	}
}

func assertWebhookEnvelope(t *testing.T, fileName string, parsed map[string]any, wantName string) {
	t.Helper()
	if id, ok := parsed["id"].(string); !ok || id == "" {
		t.Errorf("%s missing non-empty string 'id'", fileName)
	}
	if name, ok := parsed["name"].(string); !ok || name != wantName {
		t.Errorf("%s name = %v, want %v", fileName, name, wantName)
	}
	if accountID, ok := parsed["accountId"].(string); !ok || accountID == "" {
		t.Errorf("%s missing non-empty string 'accountId'", fileName)
	}
	if _, ok := parsed["data"].(map[string]any); !ok {
		t.Errorf("%s missing object 'data'", fileName)
	}
}
