package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type sandboxAPI struct {
	client *http.Client
	token  string
	scope  string
}

func openSandboxAPI(ctx context.Context, cfg credentials, transport http.RoundTripper) (*sandboxAPI, error) {
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw, _, state := readProvider(ctx, client, http.MethodPost, "/api/v1/authentication/login", http.Header{"X-Client-Id": {cfg.clientID}, "X-Api-Key": {cfg.apiKey}, "X-Login-As": {cfg.platform}})
	if state != "VERIFIED" {
		return nil, errors.New("sandbox_authentication_unavailable")
	}
	var login struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(raw, &login) != nil || login.Token == "" || !validExpiry(login.ExpiresAt, time.Now()) {
		return nil, errors.New("sandbox_authentication_response_invalid")
	}
	api := &sandboxAPI{client: client, token: login.Token}
	raw, _, state = api.get(ctx, "/api/v1/account")
	var identity struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if state != "VERIFIED" || json.Unmarshal(raw, &identity) != nil || identity.ID != cfg.platform || identity.Status != "ACTIVE" {
		return nil, errors.New("sandbox_platform_identity_mismatch")
	}
	return api, nil
}

func (a *sandboxAPI) get(ctx context.Context, path string) ([]byte, int, string) {
	headers := http.Header{"Authorization": {"Bearer " + a.token}}
	if a.scope != "" {
		headers.Set("X-On-Behalf-Of", a.scope)
	}
	if strings.HasPrefix(path, "/api/v1/issuing/") || strings.HasPrefix(path, "/api/v1/simulation/issuing/") || strings.HasPrefix(path, "/api/v1/deposits") || strings.HasPrefix(path, "/api/v1/connected_account_transfers") {
		headers.Set("X-Api-Version", "2026-08-21")
	}
	return readProvider(ctx, a.client, http.MethodGet, path, headers)
}

func (a *sandboxAPI) post(ctx context.Context, path string, body []byte) ([]byte, int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sandboxHost+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, "REQUEST_INVALID"
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	if a.scope != "" {
		req.Header.Set("X-On-Behalf-Of", a.scope)
	}
	if strings.HasPrefix(path, "/api/v1/issuing/") || strings.HasPrefix(path, "/api/v1/simulation/issuing/") || strings.HasPrefix(path, "/api/v1/deposits") || strings.HasPrefix(path, "/api/v1/connected_account_transfers") {
		req.Header.Set("X-Api-Version", "2026-08-21")
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, 0, "UNKNOWN"
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64_000))
		return nil, response.StatusCode, refusalCode(raw)

	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1_000_001))
	if err != nil || len(raw) > 1_000_000 || !json.Valid(raw) {
		return nil, response.StatusCode, "UNKNOWN"
	}
	return raw, response.StatusCode, "RECEIVED"
}

type ownedAccount struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	AccountDetails struct {
		BusinessDetails struct {
			BusinessName string `json:"business_name"`
		} `json:"business_details"`
	} `json:"account_details"`
	PrimaryContact struct {
		Email string `json:"email"`
	} `json:"primary_contact"`
}

func accountName(runID, role string) string  { return "FlowCard Sandbox " + runID + " " + role }
func accountEmail(runID, role string) string { return runID + "-" + role + "@flowcard.example.com" }

// accountPayload deliberately omits uncollected customer agreements, matching
// the existing explicit sandbox-only onboarding contract.
func accountPayload(runID, role string, acceptAgreements bool) []byte {
	body := struct {
		CustomerAgreements *struct {
			DataUsage bool `json:"agreed_to_data_usage"`
			Terms     bool `json:"agreed_to_terms_and_conditions"`
		} `json:"customer_agreements,omitempty"`
		AccountDetails struct {
			LegalEntityType string `json:"legal_entity_type"`
			BusinessDetails struct {
				BusinessName        string `json:"business_name"`
				RegistrationAddress struct {
					CountryCode string `json:"country_code"`
				} `json:"registration_address"`
			} `json:"business_details"`
		} `json:"account_details"`
		PrimaryContact struct {
			Email string `json:"email"`
		} `json:"primary_contact"`
	}{}
	body.AccountDetails.LegalEntityType = "BUSINESS"
	body.AccountDetails.BusinessDetails.BusinessName = accountName(runID, role)
	body.AccountDetails.BusinessDetails.RegistrationAddress.CountryCode = "US"
	body.PrimaryContact.Email = accountEmail(runID, role)
	if acceptAgreements {
		body.CustomerAgreements = &struct {
			DataUsage bool `json:"agreed_to_data_usage"`
			Terms     bool `json:"agreed_to_terms_and_conditions"`
		}{true, true}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// findAccount reconciles only exact synthetic name and email matches on P's
// connected-account list. Ambiguity or an incomplete list never permits create.
func (a *sandboxAPI) findAccount(ctx context.Context, runID, role string) (ownedAccount, bool, error) {
	var match ownedAccount
	count := 0
	for page := 0; page < 100; page++ {
		raw, _, state := a.get(ctx, "/api/v1/accounts?page_size=100&page_num="+strconv.Itoa(page))
		var result struct {
			Items   []ownedAccount `json:"items"`
			HasMore *bool          `json:"has_more"`
		}
		if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil || result.HasMore == nil {
			return match, false, errors.New("sandbox_account_lookup_unavailable")
		}
		for _, account := range result.Items {
			if account.AccountDetails.BusinessDetails.BusinessName == accountName(runID, role) && account.PrimaryContact.Email == accountEmail(runID, role) {
				count++
				match = account
			}
		}
		if count > 1 {
			return match, false, errors.New("sandbox_account_lookup_ambiguous")
		}
		if !*result.HasMore {
			return match, count == 1, nil
		}
	}
	return match, false, errors.New("sandbox_account_lookup_incomplete")
}

func (a *sandboxAPI) verifyAccount(ctx context.Context, id, runID, role string) (ownedAccount, error) {
	raw, _, state := a.get(ctx, "/api/v1/accounts/"+url.PathEscape(id))
	var account ownedAccount
	if state != "VERIFIED" || json.Unmarshal(raw, &account) != nil || account.ID != id || account.AccountDetails.BusinessDetails.BusinessName != accountName(runID, role) || account.PrimaryContact.Email != accountEmail(runID, role) {
		return account, errors.New("sandbox_account_ownership_mismatch")
	}
	return account, nil
}

func setupAccounts(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, acceptAgreements bool) error {
	// Initialize once. A valid existing manifest is resumed, never overwritten.
	err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
		if acceptAgreements {
			m.SandboxAgreementsAcceptedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return nil
	})
	if err != nil && err.Error() != "sandbox_manifest_already_exists" {
		return err
	}
	for _, role := range []string{"funding", "borrower"} {
		name := "account-" + role
		var existing setupIntent
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			if (m.SandboxAgreementsAcceptedAt != "") != acceptAgreements {
				return errors.New("sandbox_agreement_profile_changed")
			}
			existing = m.Intents[name]
			return nil
		}); err != nil {
			return err
		}
		if existing.ResourceID != "" {
			if _, err := api.verifyAccount(ctx, existing.ResourceID, runID, role); err != nil {
				return err
			}
			continue
		}
		account, found, err := api.findAccount(ctx, runID, role)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestID == "" {
				return errors.New("sandbox_account_exists_without_intent")
			}
			if err := saveAccount(path, runID, cfg.platform, name, account.ID); err != nil {
				return err
			}
			continue
		}
		if existing.RequestID != "" {
			return errors.New("sandbox_account_create_unknown_reconcile_only")
		}
		payload := accountPayload(runID, role, acceptAgreements)
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, runID+"-"+role, payload) }); err != nil {
			return err
		}
		raw, code, state := api.post(ctx, "/api/v1/accounts/create", payload)
		if state != "RECEIVED" {
			if code >= 400 && code < 500 && code != 408 && code != 429 {
				if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
					intent := m.Intents[name]
					intent.State = "REFUSED_HTTP_" + strconv.Itoa(code) + "_" + state
					m.Intents[name] = intent
					return nil
				}); err != nil {
					return err
				}
			}
			return errors.New("sandbox_account_create_" + stateCode(state, code))

		}
		var created ownedAccount
		if json.Unmarshal(raw, &created) != nil || created.ID == "" {
			return errors.New("sandbox_account_create_response_unknown")
		}
		if _, err := api.verifyAccount(ctx, created.ID, runID, role); err != nil {
			return err
		}
		if err := saveAccount(path, runID, cfg.platform, name, created.ID); err != nil {
			return err
		}
	}
	return nil
}

func saveAccount(path, runID, platform, name, id string) error {
	return withManifest(path, runID, platform, false, func(m *sandboxManifest) error {
		intent, ok := m.Intents[name]
		if !ok || intent.ResourceID != "" && intent.ResourceID != id {
			return errors.New("sandbox_account_intent_mismatch")
		}
		intent.ResourceID = id
		intent.State = "VERIFIED"
		m.Intents[name] = intent
		return nil
	})
}
func stateCode(state string, code int) string {
	if state != "UNKNOWN" && code >= 300 {
		return strings.ToLower(state) + "_http_" + strconv.Itoa(code)
	}
	return "unknown_reconcile_only"
}

// Refusals are classified into local codes. Provider messages remain private.
func refusalCode(raw []byte) string {
	var problem struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Source  string `json:"source"`
	}
	if json.Unmarshal(raw, &problem) != nil {
		return "REFUSED"
	}
	text := strings.ToLower(string(raw))
	switch {
	case strings.Contains(text, "agreed_to_data_usage"), strings.Contains(text, "data usage"), strings.Contains(text, "customer_agreements"), strings.Contains(text, "terms_and_conditions"):
		return "CUSTOMER_AGREEMENT_REQUIRED"
	case strings.Contains(text, "permission"), strings.Contains(text, "not enabled"), strings.Contains(text, "not a platform"):
		return "PLATFORM_PERMISSION_REQUIRED"
	case strings.Contains(text, "legal_entity_type"):
		return "LEGAL_ENTITY_TYPE_REJECTED"
	case strings.Contains(text, "attachments"), strings.Contains(text, "file_id"):
		return "VERIFICATION_DOCUMENT_REQUIRED"
	case strings.Contains(text, "business_person_details"), strings.Contains(text, "beneficial_owners"):
		return "BUSINESS_PERSON_DETAILS_REJECTED"
	case strings.Contains(text, "business_details"), strings.Contains(text, "business_name"):
		return "BUSINESS_DETAILS_REJECTED"
	case strings.Contains(text, "primary_contact"), strings.Contains(text, "email"):
		return "CONTACT_REJECTED"
	}
	return "REFUSED"
}
