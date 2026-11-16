package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

// These are explicitly synthetic US test businesses and people. No real
// customer's identity or verification documents are reused.
func profilePayload(runID, role string) []byte {
	var body struct {
		AccountDetails struct {
			BusinessDetails       map[string]any   `json:"business_details"`
			BusinessPersonDetails []map[string]any `json:"business_person_details"`
		} `json:"account_details"`
	}
	address := map[string]any{"address_line1": "1 Sandbox Way", "suburb": "San Francisco", "state": "CA", "postcode": "94105", "country_code": "US"}
	body.AccountDetails.BusinessDetails = map[string]any{
		"business_name": accountName(runID, role), "business_name_english": accountName(runID, role),
		"business_structure": "COMPANY", "description_of_goods_or_services": "Synthetic sandbox software development business for FlowCard integration testing",
		"industry_category_code": "ICCV3_0006XX", "registration_address": address, "business_address": address,
		"business_identifiers": []map[string]string{{"country_code": "US", "type": "EIN", "number": "123456789"}},
		"operating_country":    []string{"US"}, "state_of_incorporation": "CA", "business_start_date": "2020-01-01",
		"contact_number": "+14155550199", "url": "https://flowcard.example.com",
		"account_usage": map[string]any{"estimated_monthly_revenue": map[string]string{"amount": "50000", "currency": "USD"}, "product_reference": []string{"RECEIVE_TRANSFERS", "MAKE_TRANSFERS", "CREATE_CARDS", "USE_AWX_API"}},
	}
	body.AccountDetails.BusinessPersonDetails = []map[string]any{{
		"first_name": "FlowCard", "last_name": "Sandbox", "date_of_birth": "1980-01-01", "nationality": "US",
		"email": accountEmail(runID, role), "phone_number": "+14155550199", "roles": []string{"AUTHORISED_PERSON", "BENEFICIAL_OWNER", "CONTROLLING_PERSON", "DIRECTOR"}, "residential_address": address,
		"identifications": map[string]any{"primary": map[string]any{"identification_type": "TAX_ID", "issuing_country_code": "US", "tax_id": map[string]string{"type": "SSN", "number": "123456789"}}},
	}}
	raw, _ := json.Marshal(body)
	return raw
}

func completeProfiles(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	// Validate the concrete category against the live reference catalog.
	raw, _, state := api.get(ctx, "/api/v1/reference/industry_categories")
	var tree []industryItem
	if state != "VERIFIED" || json.Unmarshal(raw, &tree) != nil {
		return errors.New("sandbox_industry_reference_unavailable")
	}
	found := false
	var visit func([]industryItem)
	visit = func(items []industryItem) {
		for _, item := range items {
			if item.Value == "ICCV3_0006XX" && item.Name == "Software development" {
				found = true
			}
			visit(item.Values)
		}
	}
	visit(tree)
	if !found {
		return errors.New("sandbox_software_industry_contract_changed")
	}
	for _, role := range []string{"funding", "borrower"} {
		var accountID string
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			if m.SandboxAgreementsAcceptedAt == "" {
				return errors.New("sandbox_agreement_acceptance_required")
			}
			accountID = m.Intents["account-"+role].ResourceID
			if accountID == "" {
				return errors.New("sandbox_account_pair_not_created")
			}
			return nil
		}); err != nil {
			return err
		}
		account, err := api.verifyAccount(ctx, accountID, runID, role)
		if err != nil {
			return err
		}
		if account.Status != "CREATED" {
			return errors.New("sandbox_profile_update_requires_created_account")
		}
		name := "profile-v1-" + role
		payload := profilePayload(runID, role)
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			if intent, ok := m.Intents[name]; ok && intent.State == "VERIFIED" {
				return nil
			}
			return reserveIntent(m, name, runID+"-"+name, payload)
		}); err != nil {
			return err
		}
		var complete bool
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { complete = m.Intents[name].State == "VERIFIED"; return nil }); err != nil {
			return err
		}
		if complete {
			continue
		}
		_, code, state := api.post(ctx, "/api/v1/accounts/"+url.PathEscape(accountID)+"/update", payload)
		if state != "RECEIVED" {
			return errors.New("sandbox_profile_update_" + stateCode(state, code))
		}
		if _, err := api.verifyAccount(ctx, accountID, runID, role); err != nil {
			return err
		}
		if err := saveAccount(path, runID, cfg.platform, name, accountID); err != nil {
			return err
		}
	}
	return nil
}
