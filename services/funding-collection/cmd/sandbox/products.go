package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

func ownedScopedAPI(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID, role string) (*sandboxAPI, error) {
	var id string
	if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
		id = m.Intents["account-"+role].ResourceID
		if id == "" {
			return errors.New("sandbox_account_pair_not_created")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	account, err := api.verifyAccount(ctx, id, runID, role)
	if err != nil {
		return nil, err
	}
	if account.Status != "ACTIVE" {
		return nil, errors.New("sandbox_product_requires_active_account")
	}
	scoped := *api
	scoped.scope = id
	return &scoped, nil
}

func probeProducts(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, out io.Writer) error {
	report := map[string][]check{}
	all := true
	for _, role := range []string{"funding", "borrower"} {
		scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, role)
		if err != nil {
			return err
		}
		probes := []struct{ name, path string }{{"balances", "/api/v1/balances/current?account_type=cash"}, {"global_accounts", "/api/v1/global_accounts"}, {"ca_transfers", "/api/v1/connected_account_transfers"}}
		if role == "borrower" {
			probes = append(probes, struct{ name, path string }{"issuing_config", "/api/v1/issuing/config"}, struct{ name, path string }{"cardholders", "/api/v1/issuing/cardholders"}, struct{ name, path string }{"cards", "/api/v1/issuing/cards"})
		}
		for _, probe := range probes {
			raw, code, state := scoped.get(ctx, probe.path)
			if state == "VERIFIED" && !json.Valid(raw) {
				state = "INVALID_RESPONSE"
			}
			if state != "VERIFIED" {
				all = false
			}
			report[role] = append(report[role], check{probe.name, state, code})
		}
	}
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	if !all {
		return errors.New("sandbox_product_access_incomplete")
	}
	return nil
}
