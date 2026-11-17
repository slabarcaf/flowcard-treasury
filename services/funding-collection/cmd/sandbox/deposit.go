package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"
)

type globalDeposit struct {
	ID          string          `json:"id"`
	Amount      json.RawMessage `json:"amount"`
	Currency    string          `json:"currency"`
	Status      string          `json:"status"`
	Type        string          `json:"type"`
	FeeAmount   json.RawMessage `json:"fee_amount"`
	FeeCurrency string          `json:"fee_currency"`
}

func seedOpeningBalances(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	for _, role := range []string{"funding", "borrower"} {
		amount := int64(100000)
		if role == "borrower" {
			amount = 2000
		}
		scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, role)
		if err != nil {
			return err
		}
		receiving, err := savedIntent(path, runID, cfg.platform, "global-account-"+role)
		if err != nil {
			return err
		}
		if receiving.State != "VERIFIED" || receiving.ResourceID == "" {
			return errors.New("sandbox_usd_receiving_account_required")
		}
		name := "opening-deposit-" + role
		intent, err := savedIntent(path, runID, cfg.platform, name)
		if err != nil {
			return err
		}
		id := intent.ResourceID
		if id == "" {
			if intent.RequestID != "" {
				return errors.New("sandbox_opening_deposit_unknown_reconcile_only")
			}
			requestID := requestIDFor(runID, name)
			payload, _ := json.Marshal(struct {
				Amount        int64  `json:"amount"`
				GlobalAccount string `json:"global_account_id"`
				PayerCountry  string `json:"payer_country"`
				PayerName     string `json:"payer_name"`
				Reference     string `json:"reference"`
				StatementRef  string `json:"statement_ref"`
				Status        string `json:"status"`
			}{amount, receiving.ResourceID, "US", "FlowCard Sandbox", runID + "-opening-" + role, requestID, "SETTLED"})
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
				return err
			}
			raw, code, state := scoped.post(ctx, "/api/v1/simulation/deposit/create", payload)
			if state != "RECEIVED" {
				return errors.New("sandbox_opening_deposit_" + stateCode(state, code))
			}
			var created globalDeposit
			if json.Unmarshal(raw, &created) != nil || created.ID == "" {
				return errors.New("sandbox_opening_deposit_response_unknown")
			}
			id = created.ID
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
				intent := m.Intents[name]
				intent.ResourceID = id
				intent.State = "RECEIVED"
				m.Intents[name] = intent
				return nil
			}); err != nil {
				return err
			}
		}
		deposit, err := scoped.globalDeposit(ctx, receiving.ResourceID, id)
		if err != nil {
			return err
		}
		value, ok := majorAmount(deposit.Amount)
		expected, _ := majorAmount(json.RawMessage(strconv.FormatInt(amount, 10)))
		if !ok || value.Cmp(expected) != 0 || deposit.Currency != "USD" || deposit.Type != "CREDIT" || deposit.Status != "SETTLED" {
			return errors.New("sandbox_opening_deposit_identity_or_settlement_unverified")
		}
		if err := saveAccount(path, runID, cfg.platform, name, id); err != nil {
			return err
		}
	}
	return nil
}

func (a *sandboxAPI) globalDeposit(ctx context.Context, globalID, depositID string) (globalDeposit, error) {
	var found globalDeposit
	count := 0
	now := time.Now().UTC()
	query := url.Values{"from_created_at": {now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)}, "to_created_at": {now.Format(time.RFC3339)}, "page_size": {"100"}}
	for page := 0; page < 100; page++ {
		query.Set("page_num", strconv.Itoa(page))
		raw, _, state := a.get(ctx, "/api/v1/global_accounts/"+url.PathEscape(globalID)+"/transactions?"+query.Encode())
		var result struct {
			Items   []globalDeposit `json:"items"`
			HasMore *bool           `json:"has_more"`
		}
		if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil || result.HasMore == nil {
			return found, errors.New("sandbox_deposit_retrieval_unavailable")
		}
		for _, item := range result.Items {
			if item.ID == depositID {
				found = item
				count++
			}
		}
		if count > 1 {
			return found, errors.New("sandbox_deposit_retrieval_ambiguous")
		}
		if !*result.HasMore {
			if count != 1 {
				return found, errors.New("sandbox_deposit_not_yet_observed")
			}
			return found, nil
		}
	}
	return found, errors.New("sandbox_deposit_retrieval_incomplete")
}
