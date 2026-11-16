package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
)

type cardholder struct {
	ID     string `json:"cardholder_id"`
	Email  string `json:"email"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func setupCardholder(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	name := "cardholder-borrower"
	email := accountEmail(runID, "borrower-card")
	intent, err := savedIntent(path, runID, cfg.platform, name)
	if err != nil {
		return err
	}
	id := intent.ResourceID
	if id == "" {
		count := 0
		complete := false
		for page := 0; page < 100; page++ {
			raw, _, state := scoped.get(ctx, "/api/v1/issuing/cardholders?email="+url.QueryEscape(email)+"&page_size=100&page_num="+strconv.Itoa(page))
			var result struct {
				Items   []cardholder `json:"items"`
				HasMore *bool        `json:"has_more"`
			}
			if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil || result.HasMore == nil {
				return errors.New("sandbox_cardholder_lookup_unavailable")
			}
			for _, item := range result.Items {
				if item.Email == email {
					count++
					id = item.ID
				}
			}
			if count > 1 {
				return errors.New("sandbox_cardholder_lookup_ambiguous")
			}
			if !*result.HasMore {
				complete = true
				break
			}
		}
		if !complete {
			return errors.New("sandbox_cardholder_lookup_incomplete")
		}
		if id != "" && intent.RequestID == "" {
			return errors.New("sandbox_cardholder_without_intent")
		}
		if id == "" {
			if intent.RequestID != "" {
				return errors.New("sandbox_cardholder_unknown_reconcile_only")
			}
			requestID := requestIDFor(runID, name)
			payload, _ := json.Marshal(struct {
				RequestID string `json:"request_id"`
				Email     string `json:"email"`
				Type      string `json:"type"`
			}{requestID, email, "DELEGATE"})
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
				return err
			}
			raw, code, state := scoped.post(ctx, "/api/v1/issuing/cardholders/create", payload)
			if state != "RECEIVED" {
				return errors.New("sandbox_cardholder_create_" + stateCode(state, code))
			}
			var created cardholder
			if json.Unmarshal(raw, &created) != nil || created.ID == "" {
				return errors.New("sandbox_cardholder_create_unknown")
			}
			id = created.ID
		}
	}
	raw, _, state := scoped.get(ctx, "/api/v1/issuing/cardholders/"+url.PathEscape(id))
	var verified cardholder
	if state != "VERIFIED" || json.Unmarshal(raw, &verified) != nil || verified.ID != id || verified.Email != email || verified.Type != "DELEGATE" || verified.Status != "READY" {
		return errors.New("sandbox_cardholder_identity_or_readiness_unverified")
	}
	return saveAccount(path, runID, cfg.platform, name, id)
}
