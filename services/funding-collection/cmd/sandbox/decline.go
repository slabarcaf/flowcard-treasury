package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
)

func captureContract(path, name string, raw []byte) error {
	dir := filepath.Join(filepath.Dir(path), "contracts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("sandbox_contract_directory_unavailable")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("sandbox_contract_directory_not_private")
	}
	target := filepath.Join(dir, name+".json")
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if os.IsExist(err) {
			old, e := os.ReadFile(target)
			if e == nil && bytes.Equal(old, raw) {
				return nil
			}
			return errors.New("sandbox_contract_evidence_conflict")
		}
		return errors.New("sandbox_contract_write_failed")
	}
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		return errors.New("sandbox_contract_write_failed")
	}
	if file.Sync() != nil {
		return errors.New("sandbox_contract_sync_failed")
	}
	return nil
}

func simulateDecline(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, out io.Writer) error {
	return simulateDeclineScenario(ctx, api, cfg, path, runID, "contract-decline", out)
}

func simulateDeclineScenario(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID, name string, out io.Writer) error {
	if name != "contract-decline" && name != "callback-decline" && name != "journey-decline" && name != "journey-recovery-decline" {
		return errors.New("sandbox_decline_scenario_invalid")
	}
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	card, err := savedIntent(path, runID, cfg.platform, "card-borrower")
	if err != nil {
		return err
	}
	if card.State != "VERIFIED" || card.ResourceID == "" {
		return errors.New("sandbox_verified_wallet_card_required")
	}
	intent, err := savedIntent(path, runID, cfg.platform, name)
	if err != nil {
		return err
	}
	if intent.RequestID != "" {
		return errors.New("sandbox_decline_already_attempted_read_evidence_only")
	}
	raw, _, state := scoped.get(ctx, "/api/v1/balances/current?account_type=cash")
	var balances []safeBalance
	if state != "VERIFIED" || json.Unmarshal(raw, &balances) != nil {
		return errors.New("sandbox_borrower_balance_unverified")
	}
	usd := 0
	for _, balance := range balances {
		if balance.Currency == "USD" && balance.AccountType == "cash" {
			usd++
			amount, ok := majorAmount(balance.Available)
			expected, _ := majorAmount(json.RawMessage(`2000`))
			if !ok || amount.Cmp(expected) != 0 {
				return errors.New("sandbox_opening_balance_changed")
			}
		}
	}
	if usd != 1 {
		return errors.New("sandbox_opening_balance_unverified")
	}
	payload, _ := json.Marshal(struct {
		CardID   string `json:"card_id"`
		Amount   int64  `json:"transaction_amount"`
		Currency string `json:"transaction_currency"`
		MCC      string `json:"merchant_category_code"`
		Merchant string `json:"merchant_info"`
	}{card.ResourceID, 9000, "USD", "5734", runID + " FlowCard Sandbox"})
	if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestIDFor(runID, name), payload) }); err != nil {
		return err
	}
	raw, code, state := scoped.post(ctx, "/api/v1/simulation/issuing/create", payload)
	if state != "RECEIVED" {
		return errors.New("sandbox_decline_simulation_" + stateCode(state, code))
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("sandbox_decline_response_unknown")
	}
	// Retain only non-sensitive transaction facts. Never persist card-number,
	// expiry, security-code or other unneeded issuing response fields.
	allowed := []string{"id", "card_transaction_id", "card_id", "lifecycle_id", "type", "subtype", "transaction_category", "process_result", "failure_reason", "transaction_amount", "transaction_currency", "billing_amount", "billing_currency", "created_at", "transacted_at", "updated_at", "status", "transaction_id"}
	evidence := map[string]json.RawMessage{}
	keys := []string{}
	for _, key := range allowed {
		if value, ok := fields[key]; ok {
			evidence[key] = value
			keys = append(keys, key)
		}
	}
	safe, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return errors.New("sandbox_decline_response_unknown")
	}
	if err := captureContract(path, map[string]string{"contract-decline": "issuing-decline-create", "callback-decline": "issuing-callback-decline-create", "journey-decline": "issuing-journey-decline-create", "journey-recovery-decline": "issuing-journey-recovery-decline-create"}[name], safe); err != nil {
		return err
	}
	var parent string
	_ = json.Unmarshal(fields["card_transaction_id"], &parent)
	var id string
	_ = json.Unmarshal(fields["id"], &id)
	if id != "" || parent != "" {
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			intent := m.Intents[name]
			intent.ResourceID = id
			intent.ParentTransactionID = parent
			intent.State = "RECEIVED"
			m.Intents[name] = intent
			return nil
		}); err != nil {
			return err
		}
	}
	sort.Strings(keys)
	report := struct {
		Status  int      `json:"http_status"`
		Fields  []string `json:"schema_fields"`
		Outcome string   `json:"outcome"`
	}{code, keys, "CANONICAL_GET_UNVERIFIED"}
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}

type canonicalEvent struct {
	ID              string          `json:"id"`
	CardID          string          `json:"card_id"`
	Parent          string          `json:"card_transaction_id"`
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	Category        string          `json:"transaction_category"`
	Result          string          `json:"process_result"`
	Reason          string          `json:"failure_reason"`
	BillingAmount   json.RawMessage `json:"billing_amount"`
	BillingCurrency string          `json:"billing_currency"`
}

func captureEvent(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, out io.Writer) error {
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	intent, err := savedIntent(path, runID, cfg.platform, "contract-decline")
	if err != nil {
		return err
	}
	card, err := savedIntent(path, runID, cfg.platform, "card-borrower")
	if err != nil {
		return err
	}
	if intent.ResourceID == "" && intent.ParentTransactionID == "" {
		// Recover a known response captured before canonical-reference discovery.
		file, err := os.OpenFile(filepath.Join(filepath.Dir(path), "contracts", "issuing-decline-create.json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errors.New("sandbox_decline_response_unknown")
		}
		info, err := file.Stat()
		if err != nil || info.Mode().Perm()&0077 != 0 {
			file.Close()
			return errors.New("sandbox_decline_evidence_not_private")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 1_000_001))
		file.Close()
		var retained canonicalEvent
		if err != nil || len(raw) > 1_000_000 || json.Unmarshal(raw, &retained) != nil || retained.Parent == "" || retained.CardID != card.ResourceID {
			return errors.New("sandbox_decline_response_unknown")
		}
		intent.ParentTransactionID = retained.Parent
	}
	id := intent.ResourceID
	if id == "" {
		query := url.Values{"card_transaction_id": {intent.ParentTransactionID}, "page_size": {"100"}}
		seen := map[string]bool{}
		candidates := []canonicalEvent{}
		complete := false
		for page := 0; page < 100; page++ {
			raw, code, state := scoped.get(ctx, "/api/v1/issuing/card_transaction_events?"+query.Encode())
			var result struct {
				Items []canonicalEvent `json:"items"`
				After string           `json:"page_after"`
			}
			if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil {
				return errors.New("sandbox_event_list_unavailable_http_" + strconv.Itoa(code))
			}
			if err := captureContract(path, "issuing-event-list-page-"+strconv.Itoa(page), raw); err != nil {
				return err
			}
			for _, event := range result.Items {
				if event.Parent != intent.ParentTransactionID || event.CardID != card.ResourceID {
					return errors.New("sandbox_event_list_ownership_mismatch")
				}
				if event.Type == "AUTHORIZATION" && event.Subtype == "AUTHORIZATION" {
					candidates = append(candidates, event)
				}
			}
			if result.After == "" {
				complete = true
				break
			}
			if seen[result.After] {
				return errors.New("sandbox_event_pagination_invalid")
			}
			seen[result.After] = true
			query.Set("page", result.After)
		}
		if !complete || len(candidates) != 1 || candidates[0].ID == "" {
			return errors.New("sandbox_canonical_event_ambiguous_or_unavailable")
		}
		id = candidates[0].ID
	}
	raw, status, state := scoped.get(ctx, "/api/v1/issuing/card_transaction_events/"+url.PathEscape(id))
	if state != "VERIFIED" {
		return errors.New("sandbox_canonical_event_get_http_" + strconv.Itoa(status))
	}
	var event canonicalEvent
	if json.Unmarshal(raw, &event) != nil || event.ID != id || event.Parent == "" {
		return errors.New("sandbox_canonical_event_schema_unverified")
	}
	if err := captureContract(path, "issuing-decline-event", raw); err != nil {
		return err
	}
	if event.CardID != card.ResourceID || event.Type != "AUTHORIZATION" || event.Subtype != "AUTHORIZATION" || event.Category != "PURCHASE" || event.Result != "DECLINED" || event.Reason != "INSUFFICIENT_FUNDS" || event.BillingCurrency != "USD" {
		return errors.New("sandbox_canonical_insufficient_funds_unverified")
	}
	amount, ok := majorAmount(event.BillingAmount)
	expected, _ := majorAmount(json.RawMessage(`9000`))
	if !ok || amount.Cmp(expected) != 0 {
		return errors.New("sandbox_canonical_amount_unverified")
	}
	if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
		intent := m.Intents["contract-decline"]
		intent.ResourceID = event.ID
		intent.ParentTransactionID = event.Parent
		intent.State = "VERIFIED"
		m.Intents["contract-decline"] = intent
		return nil
	}); err != nil {
		return err
	}
	if _, err := io.WriteString(out, "canonical wallet-funded USD purchase authorization declined for insufficient funds, verified by scoped GET\n"); err != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}
