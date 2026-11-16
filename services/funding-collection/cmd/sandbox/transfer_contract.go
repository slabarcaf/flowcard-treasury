package main

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strconv"
)

type contractTransfer struct {
	ID          string          `json:"id"`
	RequestID   string          `json:"request_id"`
	Destination string          `json:"destination"`
	Amount      json.RawMessage `json:"amount"`
	Currency    string          `json:"currency"`
	Status      string          `json:"status"`
}

func (a *sandboxAPI) usdBalance(ctx context.Context) (int64, error) {
	raw, _, state := a.get(ctx, "/api/v1/balances/current?account_type=cash")
	var rows []safeBalance
	if state != "VERIFIED" || json.Unmarshal(raw, &rows) != nil {
		return 0, errors.New("sandbox_balance_unverified")
	}
	count := 0
	var cents int64
	for _, row := range rows {
		if row.Currency == "USD" && row.AccountType == "cash" {
			count++
			amount, ok := majorAmount(row.Available)
			if !ok {
				return 0, errors.New("sandbox_balance_amount_invalid")
			}
			amount.Mul(amount, big.NewRat(100, 1))
			if !amount.IsInt() || !amount.Num().IsInt64() {
				return 0, errors.New("sandbox_balance_amount_invalid")
			}
			cents = amount.Num().Int64()
		}
	}
	if count != 1 || cents < 0 {
		return 0, errors.New("sandbox_balance_unverified")
	}
	return cents, nil
}

func probeTransfers(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	for _, step := range []struct {
		name, sourceRole, targetRole, reason, kind string
		amount                                     int64
	}{
		{"fund", "funding", "borrower", "investment_capital", "FUND", 700000},
		{"principal", "borrower", "funding", "loan_credit_repayment", "COLLECT_PRINCIPAL", 700000},
		{"fee", "borrower", "funding", "professional_business_services", "COLLECT_FEE", 19000},
	} {
		source, err := ownedScopedAPI(ctx, api, cfg, path, runID, step.sourceRole)
		if err != nil {
			return err
		}
		target, err := ownedScopedAPI(ctx, api, cfg, path, runID, step.targetRole)
		if err != nil {
			return err
		}
		name := "transfer-contract-" + step.name
		requestID := requestIDFor(runID, name)
		intent, err := savedIntent(path, runID, cfg.platform, name)
		if err != nil {
			return err
		}
		id := intent.ResourceID
		if intent.RequestID != "" && (intent.SourceAccountID != source.scope || intent.DestinationAccountID != target.scope || intent.SourceBeforeCents == nil || intent.DestinationBeforeCents == nil) {
			return errors.New("sandbox_transfer_intent_binding_changed")
		}
		if id == "" {
			if intent.RequestID != "" {
				return errors.New("sandbox_transfer_unknown_reconcile_only")
			}
			beforeSource, err := source.usdBalance(ctx)
			if err != nil {
				return err
			}
			beforeTarget, err := target.usdBalance(ctx)
			if err != nil {
				return err
			}
			if beforeSource < step.amount {
				return errors.New("sandbox_transfer_source_liquidity_unavailable")
			}
			body := struct {
				RequestID   string `json:"request_id"`
				Destination string `json:"destination"`
				Amount      string `json:"amount"`
				Currency    string `json:"currency"`
				Reason      string `json:"reason"`
				Reference   string `json:"reference"`
			}{requestID, target.scope, strconv.FormatInt(step.amount/100, 10) + "." + twoDigitCents(step.amount%100), "USD", step.reason, runID + "-contract-" + step.name}
			payload, _ := json.Marshal(body)
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
				if err := reserveIntent(m, name, requestID, payload); err != nil {
					return err
				}
				intent := m.Intents[name]
				intent.SourceAccountID = source.scope
				intent.DestinationAccountID = target.scope
				intent.SourceBeforeCents = &beforeSource
				intent.DestinationBeforeCents = &beforeTarget
				m.Intents[name] = intent
				return nil
			}); err != nil {
				return err
			}
			raw, code, state := source.post(ctx, "/api/v1/connected_account_transfers/create", payload)
			if state != "RECEIVED" {
				return errors.New("sandbox_transfer_create_" + stateCode(state, code))
			}
			var created contractTransfer
			if json.Unmarshal(raw, &created) != nil || created.ID == "" {
				return errors.New("sandbox_transfer_create_unknown")
			}
			id = created.ID
			if err := captureContract(path, "ca-"+step.name+"-create", raw); err != nil {
				return err
			}
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
				intent := m.Intents[name]
				intent.ResourceID = id
				intent.State = "RECEIVED"
				m.Intents[name] = intent
				return nil
			}); err != nil {
				return err
			}
			intent, err = savedIntent(path, runID, cfg.platform, name)
			if err != nil {
				return err
			}
		}
		if intent.State == "VERIFIED" {
			continue
		}
		raw, code, state := source.get(ctx, "/api/v1/connected_account_transfers/"+url.PathEscape(id))
		if state != "VERIFIED" {
			return errors.New("sandbox_transfer_get_http_" + strconv.Itoa(code))
		}
		var verified contractTransfer
		if json.Unmarshal(raw, &verified) != nil || verified.ID != id || verified.RequestID != requestID || verified.Destination != target.scope || verified.Currency != "USD" {
			return errors.New("sandbox_transfer_receipt_identity_mismatch")
		}
		if verified.Status != "SETTLED" {
			return errors.New("sandbox_transfer_not_yet_settled")
		}
		amount, ok := majorAmount(verified.Amount)
		if !ok {
			return errors.New("sandbox_transfer_amount_invalid")
		}
		amount.Mul(amount, big.NewRat(100, 1))
		if !amount.IsInt() || !amount.Num().IsInt64() || amount.Num().Int64() != step.amount {
			return errors.New("sandbox_transfer_amount_mismatch")
		}
		afterSource, err := source.usdBalance(ctx)
		if err != nil {
			return err
		}
		afterTarget, err := target.usdBalance(ctx)
		if err != nil {
			return err
		}
		if intent.SourceBeforeCents == nil || intent.DestinationBeforeCents == nil || *intent.SourceBeforeCents-afterSource != step.amount || afterTarget-*intent.DestinationBeforeCents != step.amount {
			return errors.New("sandbox_transfer_fee_or_balance_delta_unverified")
		}
		if err := captureContract(path, "ca-"+step.name+"-settled", raw); err != nil {
			return err
		}
		proof, _ := json.Marshal(struct {
			Kind         string `json:"kind"`
			AmountCents  int64  `json:"amount_cents"`
			FeeCents     int64  `json:"fee_cents"`
			SourceBefore int64  `json:"source_before_cents"`
			SourceAfter  int64  `json:"source_after_cents"`
			TargetBefore int64  `json:"target_before_cents"`
			TargetAfter  int64  `json:"target_after_cents"`
		}{step.kind, step.amount, 0, *intent.SourceBeforeCents, afterSource, *intent.DestinationBeforeCents, afterTarget})
		if err := captureContract(path, "ca-"+step.name+"-balance-proof", proof); err != nil {
			return err
		}
		if err := saveAccount(path, runID, cfg.platform, name, id); err != nil {
			return err
		}
	}
	return nil
}

func twoDigitCents(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}

func probeDeposit(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	return simulateOwnedDeposit(ctx, api, cfg, path, runID, "deposit-contract", 190)
}

func simulateOwnedDeposit(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID, name string, amount int) error {
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	receiving, err := savedIntent(path, runID, cfg.platform, "global-account-borrower")
	if err != nil {
		return err
	}
	if receiving.State != "VERIFIED" {
		return errors.New("sandbox_receiving_account_unverified")
	}
	intent, err := savedIntent(path, runID, cfg.platform, name)
	if err != nil {
		return err
	}
	id := intent.ResourceID
	if id == "" {
		if intent.RequestID != "" {
			return errors.New("sandbox_deposit_contract_unknown_reconcile_only")
		}
		requestID := requestIDFor(runID, name)
		payload, _ := json.Marshal(struct {
			Amount        int    `json:"amount"`
			GlobalAccount string `json:"global_account_id"`
			PayerCountry  string `json:"payer_country"`
			PayerName     string `json:"payer_name"`
			Reference     string `json:"reference"`
			Statement     string `json:"statement_ref"`
			Status        string `json:"status"`
		}{amount, receiving.ResourceID, "US", "FlowCard Sandbox", runID + "-" + name, requestID, "SETTLED"})
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
			return err
		}
		raw, code, state := scoped.post(ctx, "/api/v1/simulation/deposit/create", payload)
		if state != "RECEIVED" {
			return errors.New("sandbox_deposit_contract_create_" + stateCode(state, code))
		}
		var created globalDeposit
		if json.Unmarshal(raw, &created) != nil || created.ID == "" {
			return errors.New("sandbox_deposit_contract_response_unknown")
		}
		id = created.ID
		if err := captureContract(path, name+"-create", raw); err != nil {
			return err
		}
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
	raw, code, state := scoped.get(ctx, "/api/v1/deposits/"+url.PathEscape(id))
	readerKind := "OBJECT_BY_ID"
	if state != "VERIFIED" {
		return errors.New("sandbox_core_deposit_get_http_" + strconv.Itoa(code))
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("sandbox_core_deposit_schema_invalid")
	}
	if err := captureContract(path, name+"-get", raw); err != nil {
		return err
	}
	return withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
		intent := m.Intents[name]
		intent.ResourceID = id
		intent.State = "CAPTURED"
		intent.DepositReadKind = readerKind
		m.Intents[name] = intent
		return nil
	})
}
