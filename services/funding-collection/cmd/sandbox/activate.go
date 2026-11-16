package main

import (
	"context"
	"errors"
	"net/url"
)

func activateAccounts(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
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
		if account.Status == "ACTIVE" {
			continue
		}
		if account.Status == "CREATED" {
			if err := accountTransition(ctx, api, cfg, path, runID, role, "submit", accountID, []byte(`{}`), "/api/v1/accounts/"+url.PathEscape(accountID)+"/submit"); err != nil {
				return err
			}
			account, err = api.verifyAccount(ctx, accountID, runID, role)
			if err != nil {
				return err
			}
		}
		if account.Status == "ACTIVE" {
			continue
		}
		if account.Status != "SUBMITTED" {
			return errors.New("sandbox_account_not_submitted")
		}
		if err := accountTransition(ctx, api, cfg, path, runID, role, "activate", accountID, []byte(`{"next_status":"ACTIVE","force":true}`), "/api/v1/simulation/accounts/"+url.PathEscape(accountID)+"/update_status"); err != nil {
			return err
		}
		account, err = api.verifyAccount(ctx, accountID, runID, role)
		if err != nil {
			return err
		}
		if account.Status != "ACTIVE" {
			return errors.New("sandbox_account_activation_unverified")
		}
	}
	return nil
}

func accountTransition(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID, role, step, accountID string, payload []byte, endpoint string) error {
	name := step + "-" + role
	if step == "submit" {
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			if m.Intents["profile-v1-"+role].State == "VERIFIED" {
				name = "submit-profile-v1-" + role
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
		requestID := requestIDFor(runID, name)
		if existing := m.Intents[name]; existing.RequestID != "" {
			requestID = existing.RequestID
		}
		return reserveIntent(m, name, requestID, payload)
	}); err != nil {
		return err
	}
	_, code, state := api.post(ctx, endpoint, payload)
	if state != "RECEIVED" {
		if code >= 400 && code < 500 && code != 408 && code != 429 {
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
				intent := m.Intents[name]
				intent.State = "REFUSED_HTTP_" + stateCode(state, code)
				m.Intents[name] = intent
				return nil
			}); err != nil {
				return err
			}
		}
		return errors.New("sandbox_account_" + step + "_" + stateCode(state, code))
	}
	account, err := api.verifyAccount(ctx, accountID, runID, role)
	if err != nil {
		return err
	}
	expected := "SUBMITTED"
	if step == "activate" {
		expected = "ACTIVE"
	}
	if account.Status != expected && !(step == "submit" && account.Status == "ACTIVE") {
		return errors.New("sandbox_account_transition_unverified")
	}
	return saveAccount(path, runID, cfg.platform, name, accountID)
}
