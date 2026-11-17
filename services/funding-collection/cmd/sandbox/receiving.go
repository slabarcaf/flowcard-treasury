package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

type globalAccount struct {
	ID        string `json:"id"`
	Nickname  string `json:"nick_name"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Country   string `json:"country_code"`
	Features  []struct {
		Currency string `json:"currency"`
		Type     string `json:"type"`
	} `json:"supported_features"`
}

func requestIDFor(runID, name string) string {
	sum := sha256.Sum256([]byte(runID + ":" + name))
	sum[6] = (sum[6] & 15) | 0x50
	sum[8] = (sum[8] & 63) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func savedIntent(path, runID, platform, name string) (setupIntent, error) {
	var intent setupIntent
	err := withManifest(path, runID, platform, false, func(m *sandboxManifest) error { intent = m.Intents[name]; return nil })
	return intent, err
}

func setupReceiving(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	for _, role := range []string{"funding", "borrower"} {
		scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, role)
		if err != nil {
			return err
		}
		name := "global-account-" + role
		requestID := requestIDFor(runID, name)
		intent, err := savedIntent(path, runID, cfg.platform, name)
		if err != nil {
			return err
		}
		var id string
		if intent.ResourceID != "" {
			id = intent.ResourceID
		} else {
			raw, _, state := scoped.get(ctx, "/api/v1/global_accounts")
			var list struct {
				Items   []globalAccount `json:"items"`
				HasMore bool            `json:"has_more"`
			}
			if state != "VERIFIED" || json.Unmarshal(raw, &list) != nil || list.Items == nil || list.HasMore {
				return errors.New("sandbox_global_account_lookup_incomplete")
			}
			count := 0
			for _, item := range list.Items {
				if item.RequestID == requestID && item.Nickname == accountName(runID, role) {
					id = item.ID
					count++
				}
			}
			if count > 1 {
				return errors.New("sandbox_global_account_lookup_ambiguous")
			}
			if id != "" && intent.RequestID == "" {
				return errors.New("sandbox_global_account_without_intent")
			}
			if id == "" {
				if intent.RequestID != "" {
					return errors.New("sandbox_global_account_unknown_reconcile_only")
				}
				body := struct {
					Country   string              `json:"country_code"`
					Nickname  string              `json:"nick_name"`
					RequestID string              `json:"request_id"`
					Features  []map[string]string `json:"required_features"`
				}{"US", accountName(runID, role), requestID, []map[string]string{{"currency": "USD", "transfer_method": "LOCAL"}}}
				payload, err := json.Marshal(body)
				if err != nil {
					return errors.New("sandbox_global_account_payload_invalid")
				}
				if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
					return err
				}
				raw, code, state := scoped.post(ctx, "/api/v1/global_accounts/create", payload)
				if state != "RECEIVED" {
					return errors.New("sandbox_global_account_create_" + stateCode(state, code))
				}
				var created globalAccount
				if json.Unmarshal(raw, &created) != nil || created.ID == "" {
					return errors.New("sandbox_global_account_create_unknown")
				}
				id = created.ID
			}
		}
		raw, _, state := scoped.get(ctx, "/api/v1/global_accounts/"+url.PathEscape(id))
		var verified globalAccount
		if state != "VERIFIED" || json.Unmarshal(raw, &verified) != nil || verified.ID != id || verified.RequestID != requestID || verified.Nickname != accountName(runID, role) || verified.Status != "ACTIVE" || verified.Country != "US" {
			return errors.New("sandbox_global_account_identity_or_status_unverified")
		}
		depositUSD := false
		for _, feature := range verified.Features {
			if feature.Currency == "USD" && feature.Type == "DEPOSIT" {
				depositUSD = true
			}
		}
		if !depositUSD {
			return errors.New("sandbox_global_account_usd_deposit_unavailable")
		}
		if err := saveAccount(path, runID, cfg.platform, name, id); err != nil {
			return err
		}
	}
	return nil
}
