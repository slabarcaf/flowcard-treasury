package provider

import (
	"encoding/json"
	"errors"
	"strings"
)

// WebhookWake contains only references; amount and settlement facts are always
// retrieved independently before financial accounting.
type WebhookWake struct{ TargetKind, RequestID, CustomerID, CardID, DepositID, State, BlockedCode string }
type SandboxWebhookReader interface {
	NormalizeSandboxWebhook([]byte) (WebhookWake, error)
}

func (a *AirwallexAdapter) NormalizeSandboxWebhook(raw []byte) (WebhookWake, error) {
	held := WebhookWake{TargetKind: "UNSUPPORTED", State: "HELD", BlockedCode: "SANDBOX_WEBHOOK_CONTRACT_UNAVAILABLE"}
	c := a.sandboxExecutionContract
	if a.mode != "SANDBOX" || !c.valid(a.now()) {
		return held, nil
	}
	var envelope struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		AccountID string `json:"account_id"`
		Version   string `json:"version"`
		CreatedAt string `json:"created_at"`
		Data      struct {
			ID              string `json:"id"`
			RequestID       string `json:"request_id"`
			GlobalAccountID string `json:"global_account_id"`
			Destination     string `json:"destination"`
			Currency        string `json:"currency"`
			Type            string `json:"type"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ID == "" || envelope.Name == "" {
		return held, errors.New("sandbox_webhook_envelope_invalid")
	}
	if envelope.Version != c.Version {
		held.BlockedCode = "UNSUPPORTED_WEBHOOK_VERSION"
		return held, nil
	}
	if envelope.AccountID != c.FundingAccountID && envelope.AccountID != c.BorrowerAccountID {
		held.BlockedCode = "SANDBOX_WEBHOOK_ACCOUNT_MISMATCH"
		return held, nil
	}
	created, err := parseProviderExpiry(envelope.CreatedAt)
	if err != nil {
		held.BlockedCode = "SANDBOX_WEBHOOK_TIME_UNVERIFIED"
		return held, nil
	}
	if created.Before(c.CutoverAt) {
		held.BlockedCode = "SANDBOX_WEBHOOK_BEFORE_CUTOVER"
		return held, nil
	}
	switch envelope.Name {
	case "connected_account_transfer.new", "connected_account_transfer.pending", "connected_account_transfer.settled", "connected_account_transfer.suspended", "connected_account_transfer.failed":
		if !validProviderReference(envelope.Data.ID) || !validProviderReference(envelope.Data.RequestID) || len(envelope.Data.RequestID) > 50 || envelope.Data.Currency != "USD" || (envelope.Data.Destination != c.FundingAccountID && envelope.Data.Destination != c.BorrowerAccountID) {
			return held, errors.New("sandbox_webhook_transfer_reference_invalid")
		}
		return WebhookWake{TargetKind: "FUND_REQUEST", RequestID: envelope.Data.RequestID, State: "PENDING"}, nil
	case "deposit.settled", "deposit.pending", "deposit.rejected", "deposit.reversed":
		if envelope.AccountID != c.BorrowerAccountID || envelope.Data.GlobalAccountID != c.BorrowerGlobalAccountID || envelope.Data.Type != "BANK_TRANSFER" || envelope.Data.Currency != "USD" || !validProviderReference(envelope.Data.ID) {
			held.BlockedCode = "SANDBOX_DEPOSIT_REFERENCE_UNSUPPORTED"
			return held, nil
		}
		wake := WebhookWake{TargetKind: "DEPOSIT", CustomerID: c.CustomerID, CardID: c.CardID, DepositID: envelope.Data.ID, State: "PENDING"}
		if strings.HasSuffix(envelope.Name, ".reversed") || strings.HasSuffix(envelope.Name, ".rejected") {
			wake.TargetKind = "UNSUPPORTED"
			wake.BlockedCode = "SANDBOX_DEPOSIT_REVIEW_REQUIRED"
		}
		return wake, nil
	default:
		held.BlockedCode = "SANDBOX_WEBHOOK_EVENT_UNSUPPORTED"
		return held, nil
	}
}
