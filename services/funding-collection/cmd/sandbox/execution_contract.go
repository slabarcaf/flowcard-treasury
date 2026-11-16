package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

func parseSandboxTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("sandbox_provider_time_invalid")
}

func exportExecutionContract(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(path), &local, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(local); err != nil {
		return err
	}
	if local.RunID != runID {
		return errors.New("sandbox_execution_run_mismatch")
	}
	funding, err := ownedScopedAPI(ctx, api, cfg, path, runID, "funding")
	if err != nil {
		return err
	}
	borrower, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	card, err := savedIntent(path, runID, cfg.platform, "card-borrower")
	if err != nil {
		return err
	}
	receiving, err := savedIntent(path, runID, cfg.platform, "global-account-borrower")
	if err != nil {
		return err
	}
	contract := provider.SandboxExecutionContract{SchemaVersion: 1, Environment: "SANDBOX", RunID: runID, FacilityID: local.FacilityID, SourceBindingID: local.SourceBindingID, CustomerID: local.CustomerID, FundingAccountID: funding.scope, BorrowerAccountID: borrower.scope, CardID: card.ResourceID, BorrowerGlobalAccountID: receiving.ResourceID, Version: "2026-08-21", FundingPurpose: "investment_capital", TransferFeeField: "fee", SettlementTimeField: "updated_at", DepositFeeModel: "EXPLICIT_ZERO_ONLY", CutoverAt: time.Now().UTC().Truncate(time.Second), ExpiresAt: time.Now().UTC().Add(8 * time.Hour), EvidenceHashes: map[string]string{}}
	expectedEvents := map[string]string{}
	for _, step := range []struct {
		name           string
		kind           provider.OperationKind
		source, target *sandboxAPI
		amount         int64
		reason         string
	}{
		{"fund", provider.OperationFund, funding, borrower, 700000, "investment_capital"}, {"principal", provider.OperationCollectPrincipal, borrower, funding, 700000, "loan_credit_repayment"}, {"fee", provider.OperationCollectFee, borrower, funding, 19000, "professional_business_services"},
	} {
		intent, err := savedIntent(path, runID, cfg.platform, "transfer-contract-"+step.name)
		if err != nil || intent.State != "VERIFIED" || intent.SourceAccountID != step.source.scope || intent.DestinationAccountID != step.target.scope {
			return errors.New("sandbox_exact_route_probe_unverified")
		}
		raw, _, state := step.source.get(ctx, "/api/v1/connected_account_transfers/"+url.PathEscape(intent.ResourceID))
		var transfer struct {
			ID          string          `json:"id"`
			RequestID   string          `json:"request_id"`
			Destination string          `json:"destination"`
			Currency    string          `json:"currency"`
			Amount      json.RawMessage `json:"amount"`
			Fee         json.RawMessage `json:"fee"`
			Status      string          `json:"status"`
			Reason      string          `json:"reason"`
			UpdatedAt   string          `json:"updated_at"`
		}
		if state != "VERIFIED" || json.Unmarshal(raw, &transfer) != nil || transfer.ID != intent.ResourceID || transfer.RequestID != intent.RequestID || transfer.Destination != step.target.scope || transfer.Currency != "USD" || transfer.Status != "SETTLED" || transfer.Reason != step.reason {
			return errors.New("sandbox_current_transfer_probe_mismatch")
		}
		amount, ok := majorAmount(transfer.Amount)
		if !ok {
			return errors.New("sandbox_exact_route_amount_unverified")
		}
		amount.Mul(amount, big.NewRat(100, 1))
		if !amount.IsInt() || !amount.Num().IsInt64() || amount.Num().Int64() != step.amount {
			return errors.New("sandbox_exact_route_amount_unverified")
		}
		fee, ok := majorAmount(transfer.Fee)
		if !ok || fee.Sign() != 0 {
			return errors.New("sandbox_exact_route_fee_unverified")
		}
		updated, err := parseSandboxTimestamp(transfer.UpdatedAt)
		if err != nil || updated.After(time.Now().UTC().Add(5*time.Second)) {
			return errors.New("sandbox_transfer_terminal_time_unverified")
		}
		proofPath := filepath.Join(filepath.Dir(path), "contracts", "ca-"+step.name+"-balance-proof.json")
		var proof struct {
			Kind         provider.OperationKind `json:"kind"`
			AmountCents  int64                  `json:"amount_cents"`
			FeeCents     int64                  `json:"fee_cents"`
			SourceBefore int64                  `json:"source_before_cents"`
			SourceAfter  int64                  `json:"source_after_cents"`
			TargetBefore int64                  `json:"target_before_cents"`
			TargetAfter  int64                  `json:"target_after_cents"`
		}
		if readPrivateJSON(proofPath, &proof, 65536) != nil || proof.Kind != step.kind || proof.AmountCents != step.amount || proof.FeeCents != 0 || proof.SourceBefore-proof.SourceAfter != step.amount || proof.TargetAfter-proof.TargetBefore != step.amount {
			return errors.New("sandbox_transfer_balance_proof_invalid")
		}
		proofRaw, err := os.ReadFile(proofPath)
		if err != nil {
			return errors.New("sandbox_transfer_balance_proof_unavailable")
		}
		hash := sha256.Sum256(append(append([]byte(nil), raw...), proofRaw...))
		encoded := hex.EncodeToString(hash[:])
		contract.Quotes = append(contract.Quotes, provider.SandboxQuote{Kind: step.kind, AmountCents: step.amount, FeeCents: 0, EvidenceHash: encoded})
		contract.EvidenceHashes["transfer-get-"+step.name] = encoded
		expectedEvents[intent.ResourceID] = "transfer-signed-" + step.name
	}
	deposit, err := savedIntent(path, runID, cfg.platform, "deposit-contract")
	if err != nil || deposit.ResourceID == "" {
		return errors.New("sandbox_deposit_probe_unavailable")
	}
	raw, _, state := borrower.get(ctx, "/api/v1/deposits/"+url.PathEscape(deposit.ResourceID))
	var bank struct {
		ID       string          `json:"id"`
		GlobalID string          `json:"global_account_id"`
		Status   string          `json:"status"`
		Type     string          `json:"type"`
		Currency string          `json:"currency"`
		Amount   json.RawMessage `json:"amount"`
		Fee      *struct {
			Amount   json.RawMessage `json:"amount"`
			Currency string          `json:"currency"`
		} `json:"fee"`
		SettledAt string `json:"settled_at"`
	}
	if state != "VERIFIED" || json.Unmarshal(raw, &bank) != nil || bank.ID != deposit.ResourceID || bank.GlobalID != receiving.ResourceID || bank.Status != "SETTLED" || bank.Type != "BANK_TRANSFER" || bank.Currency != "USD" || bank.Fee == nil || bank.Fee.Currency != "USD" {
		return errors.New("sandbox_deposit_get_contract_unverified")
	}
	bankFee, ok := majorAmount(bank.Fee.Amount)
	if !ok || bankFee.Sign() != 0 {
		return errors.New("sandbox_deposit_fee_contract_unverified")
	}
	if _, err := parseSandboxTimestamp(bank.SettledAt); err != nil {
		return err
	}
	bankHash := sha256.Sum256(raw)
	contract.EvidenceHashes["deposit-get"] = hex.EncodeToString(bankHash[:])
	expectedEvents[deposit.ResourceID] = "deposit-signed"
	var capture captureSubscriptions
	if err := readPrivateJSON(filepath.Join(filepath.Dir(path), "capture-subscriptions.json"), &capture, 65536); err != nil {
		return err
	}
	if capture.Owner != manifestOwner || capture.RunID != runID || capture.Environment != "SANDBOX" {
		return errors.New("sandbox_subscription_owner_mismatch")
	}
	subscription := capture.Subscriptions["SERVICING"]
	if subscription.ID == "" || subscription.Secret == "" || subscription.Version != contract.Version {
		return errors.New("sandbox_servicing_subscription_unverified")
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "webhook-evidence"))
	if err != nil {
		return errors.New("sandbox_servicing_evidence_unavailable")
	}
	observed := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record struct {
			RunID     string `json:"run_id"`
			Source    string `json:"source"`
			Timestamp string `json:"x_timestamp"`
			Signature string `json:"x_signature"`
			Raw       []byte `json:"raw_body_base64"`
		}
		if err := readPrivateJSON(filepath.Join(filepath.Dir(path), "webhook-evidence", entry.Name()), &record, 16*1024*1024); err != nil {
			return err
		}
		if record.RunID != runID || record.Source != "SERVICING" {
			continue
		}
		signature, err := hex.DecodeString(record.Signature)
		if err != nil {
			return errors.New("sandbox_servicing_signature_invalid")
		}
		mac := hmac.New(sha256.New, []byte(subscription.Secret))
		mac.Write([]byte(record.Timestamp))
		mac.Write(record.Raw)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return errors.New("sandbox_servicing_signature_invalid")
		}
		var envelope struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			AccountID string `json:"account_id"`
			Data      struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if json.Unmarshal(record.Raw, &envelope) != nil || envelope.Version != contract.Version || (envelope.AccountID != funding.scope && envelope.AccountID != borrower.scope) {
			return errors.New("sandbox_servicing_evidence_scope_mismatch")
		}
		key, found := expectedEvents[envelope.Data.ID]
		if !found || envelope.Data.Status != "SETTLED" {
			continue
		}
		expectedName := "connected_account_transfer.settled"
		if key == "deposit-signed" {
			expectedName = "deposit.settled"
		}
		if envelope.Name != expectedName {
			continue
		}
		hash := sha256.Sum256(record.Raw)
		contract.EvidenceHashes[key] = hex.EncodeToString(hash[:])
		observed[key] = true
	}
	if len(observed) != 4 {
		return errors.New("sandbox_signed_movement_contracts_incomplete")
	}
	contract.FundingVerified = true
	contract.CollectionVerified = true
	encoded, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return errors.New("sandbox_execution_profile_invalid")
	}
	return writePrivateFile(filepath.Join(filepath.Dir(path), "execution-contract.json"), encoded)
}
