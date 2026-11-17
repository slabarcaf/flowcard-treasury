package main

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const cardLimitUSD int64 = 10000

var majorAmountPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,2})?$`)

type cardProgram struct {
	Purpose string `json:"purpose"`
	Type    string `json:"type"`
	Subtype string `json:"sub_type"`
}
type cardLimit struct {
	Amount   json.RawMessage `json:"amount"`
	Interval string          `json:"interval"`
}
type cardControls struct {
	Count      string   `json:"allowed_transaction_count"`
	Currencies []string `json:"allowed_currencies"`
	Limits     struct {
		Currency string      `json:"currency"`
		Limits   []cardLimit `json:"limits"`
	} `json:"transaction_limits"`
}
type issuedCard struct {
	ID              string       `json:"card_id"`
	HolderID        string       `json:"cardholder_id"`
	RequestID       string       `json:"request_id"`
	Nickname        string       `json:"nick_name"`
	Status          string       `json:"card_status"`
	FormFactor      string       `json:"form_factor"`
	Personalized    bool         `json:"is_personalized"`
	FundingSourceID string       `json:"funding_source_id"`
	Program         cardProgram  `json:"program"`
	Controls        cardControls `json:"authorization_controls"`
}

func majorAmount(raw json.RawMessage) (*big.Rat, bool) {
	value := string(raw)
	if strings.HasPrefix(value, `"`) {
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
	}
	if !majorAmountPattern.MatchString(value) {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(value)
	return number, ok
}

func cardPayload(runID, holderID, requestID string) []byte {
	body := struct {
		RequestID    string       `json:"request_id"`
		HolderID     string       `json:"cardholder_id"`
		CreatedBy    string       `json:"created_by"`
		FormFactor   string       `json:"form_factor"`
		Personalized bool         `json:"is_personalized"`
		Nickname     string       `json:"nick_name"`
		Program      cardProgram  `json:"program"`
		Controls     cardControls `json:"authorization_controls"`
	}{RequestID: requestID, HolderID: holderID, CreatedBy: "FlowCard Sandbox", FormFactor: "VIRTUAL", Nickname: accountName(runID, "funding-walkthrough"), Program: cardProgram{"COMMERCIAL", "CREDIT", "GOOD_FUNDS_CREDIT"}}
	body.Controls.Count = "MULTIPLE"
	body.Controls.Currencies = []string{"USD"}
	body.Controls.Limits.Currency = "USD"
	body.Controls.Limits.Limits = []cardLimit{{json.RawMessage(strconv.FormatInt(cardLimitUSD, 10)), "PER_TRANSACTION"}, {json.RawMessage(strconv.FormatInt(cardLimitUSD, 10)), "DAILY"}}
	raw, _ := json.Marshal(body)
	return raw
}

func setupCard(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	holder, err := savedIntent(path, runID, cfg.platform, "cardholder-borrower")
	if err != nil {
		return err
	}
	if holder.State != "VERIFIED" || holder.ResourceID == "" {
		return errors.New("sandbox_ready_cardholder_required")
	}
	raw, _, state := scoped.get(ctx, "/api/v1/issuing/cardholders/"+url.PathEscape(holder.ResourceID))
	var currentHolder cardholder
	if state != "VERIFIED" || json.Unmarshal(raw, &currentHolder) != nil || currentHolder.ID != holder.ResourceID || currentHolder.Type != "DELEGATE" || currentHolder.Email != accountEmail(runID, "borrower-card") || currentHolder.Status != "READY" {
		return errors.New("sandbox_ready_cardholder_required")
	}
	raw, _, state = scoped.get(ctx, "/api/v1/issuing/config")
	var config struct {
		Spending struct {
			PerTransaction []struct {
				Currency string          `json:"currency"`
				Maximum  json.RawMessage `json:"maximum"`
			} `json:"per_transaction_limits"`
		} `json:"spending_limit_settings"`
		RemoteAuth struct {
			Enabled bool `json:"enabled"`
		} `json:"remote_auth_settings"`
	}
	if state != "VERIFIED" || json.Unmarshal(raw, &config) != nil {
		return errors.New("sandbox_issuing_config_unavailable")
	}
	if config.RemoteAuth.Enabled {
		return errors.New("sandbox_remote_authorization_requires_review")
	}
	supported := false
	for _, limit := range config.Spending.PerTransaction {
		if limit.Currency == "USD" {
			maximum, ok := majorAmount(limit.Maximum)
			supported = ok && maximum.Cmp(new(big.Rat).SetInt64(cardLimitUSD)) >= 0
		}
	}
	if !supported {
		return errors.New("sandbox_card_usd_limit_not_supported")
	}
	name := "card-borrower"
	requestID := requestIDFor(runID, name)
	intent, err := savedIntent(path, runID, cfg.platform, name)
	if err != nil {
		return err
	}
	id := intent.ResourceID
	if id == "" {
		count := 0
		complete := false
		for page := 0; page < 100; page++ {
			raw, _, state := scoped.get(ctx, "/api/v1/issuing/cards?page_size=100&page_num="+strconv.Itoa(page))
			var result struct {
				Items   []issuedCard `json:"items"`
				HasMore *bool        `json:"has_more"`
			}
			if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil || result.HasMore == nil {
				return errors.New("sandbox_card_lookup_unavailable")
			}
			for _, item := range result.Items {
				if item.RequestID == requestID && item.HolderID == holder.ResourceID && item.Nickname == accountName(runID, "funding-walkthrough") {
					id = item.ID
					count++
				}
			}
			if count > 1 {
				return errors.New("sandbox_card_lookup_ambiguous")
			}
			if !*result.HasMore {
				complete = true
				break
			}
		}
		if !complete {
			return errors.New("sandbox_card_lookup_incomplete")
		}
		if id != "" && intent.RequestID == "" {
			return errors.New("sandbox_card_without_intent")
		}
		if id == "" {
			if intent.RequestID != "" {
				return errors.New("sandbox_card_unknown_reconcile_only")
			}
			payload := cardPayload(runID, holder.ResourceID, requestID)
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
				return err
			}
			raw, code, state := scoped.post(ctx, "/api/v1/issuing/cards/create", payload)
			if state != "RECEIVED" {
				return errors.New("sandbox_card_create_" + stateCode(state, code))
			}
			var created issuedCard
			if json.Unmarshal(raw, &created) != nil || created.ID == "" {
				return errors.New("sandbox_card_create_unknown")
			}
			id = created.ID
		}
	}
	raw, _, state = scoped.get(ctx, "/api/v1/issuing/cards/"+url.PathEscape(id))
	var verified issuedCard
	if state != "VERIFIED" || json.Unmarshal(raw, &verified) != nil || verified.ID != id || verified.HolderID != holder.ResourceID || verified.RequestID != requestID || verified.Nickname != accountName(runID, "funding-walkthrough") || verified.Status != "ACTIVE" || verified.FormFactor != "VIRTUAL" || verified.Personalized || verified.FundingSourceID != "" || verified.Program != (cardProgram{"COMMERCIAL", "CREDIT", "GOOD_FUNDS_CREDIT"}) {
		return errors.New("sandbox_card_identity_or_wallet_program_unverified")
	}
	perTransaction, daily := false, false
	if verified.Controls.Count != "MULTIPLE" || verified.Controls.Limits.Currency != "USD" {
		return errors.New("sandbox_card_controls_unverified")
	}
	for _, limit := range verified.Controls.Limits.Limits {
		amount, ok := majorAmount(limit.Amount)
		if !ok || amount.Cmp(new(big.Rat).SetInt64(cardLimitUSD)) != 0 {
			return errors.New("sandbox_card_limit_unverified")
		}
		if limit.Interval == "PER_TRANSACTION" {
			perTransaction = true
		}
		if limit.Interval == "DAILY" {
			daily = true
		}
	}
	if !perTransaction || !daily {
		return errors.New("sandbox_card_limit_unverified")
	}
	return saveAccount(path, runID, cfg.platform, name, id)
}
