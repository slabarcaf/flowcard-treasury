package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func readPrivateJSON(path string, destination any, limit int64) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("sandbox_private_evidence_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("sandbox_private_evidence_not_private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit || json.Unmarshal(raw, destination) != nil {
		return errors.New("sandbox_private_evidence_invalid")
	}
	return nil
}

func exportShortfallContract(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string) error {
	if err := setupCard(ctx, api, cfg, path, runID); err != nil {
		return err
	}
	scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, "borrower")
	if err != nil {
		return err
	}
	card, err := savedIntent(path, runID, cfg.platform, "card-borrower")
	if err != nil {
		return err
	}
	attempt, err := savedIntent(path, runID, cfg.platform, "callback-decline")
	if err != nil {
		return err
	}
	if attempt.ParentTransactionID == "" {
		return errors.New("sandbox_callback_parent_unverified")
	}
	var capture captureSubscriptions
	if err := readPrivateJSON(filepath.Join(filepath.Dir(path), "capture-subscriptions.json"), &capture, 65536); err != nil {
		return err
	}
	if capture.Owner != manifestOwner || capture.Environment != "SANDBOX" || capture.RunID != runID {
		return errors.New("sandbox_capture_ownership_mismatch")
	}
	subscription := capture.Subscriptions["SHORTFALL"]
	if subscription.ID == "" || subscription.Secret == "" || subscription.Version != "2026-08-21" {
		return errors.New("sandbox_subscription_unverified")
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "webhook-evidence"))
	if err != nil {
		return errors.New("sandbox_signed_evidence_unavailable")
	}
	hashes := map[string]string{}
	eventID := attempt.ResourceID
	var canonical []byte
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record struct {
			RunID     string `json:"run_id"`
			Source    string `json:"source"`
			EventID   string `json:"event_id"`
			EventName string `json:"event_name"`
			Timestamp string `json:"x_timestamp"`
			Signature string `json:"x_signature"`
			RawBody   []byte `json:"raw_body_base64"`
		}
		if err := readPrivateJSON(filepath.Join(filepath.Dir(path), "webhook-evidence", entry.Name()), &record, 16*1024*1024); err != nil {
			return err
		}
		if record.Source != "SHORTFALL" || record.RunID != runID {
			continue
		}
		signature, err := hex.DecodeString(record.Signature)
		if err != nil {
			return errors.New("sandbox_signed_evidence_invalid")
		}
		mac := hmac.New(sha256.New, []byte(subscription.Secret))
		mac.Write([]byte(record.Timestamp))
		mac.Write(record.RawBody)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return errors.New("sandbox_signed_evidence_invalid")
		}
		var envelope struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Account string `json:"account_id"`
			Version string `json:"version"`
			Data    struct {
				ID     string `json:"id"`
				Card   string `json:"card_id"`
				Parent string `json:"card_transaction_id"`
			} `json:"data"`
		}
		if json.Unmarshal(record.RawBody, &envelope) != nil || envelope.ID != record.EventID || envelope.Name != record.EventName || envelope.Account != scoped.scope || envelope.Data.Card != card.ResourceID || envelope.Version != "2026-08-21" {
			return errors.New("sandbox_signed_evidence_identity_mismatch")
		}
		matched := false
		switch envelope.Name {
		case "issuing.card_transaction_event.failed":
			if envelope.Data.ID == "" || envelope.Data.Parent == "" {
				return errors.New("sandbox_signed_event_reference_missing")
			}
			sample, _, sampleState := scoped.get(ctx, "/api/v1/issuing/card_transaction_events/"+url.PathEscape(envelope.Data.ID))
			if sampleState != "VERIFIED" || !verifiedCanonicalDecline(sample, envelope.Data.ID, envelope.Data.Parent, card.ResourceID) {
				return errors.New("sandbox_canonical_signed_event_unverified")
			}
			// The two notification shapes need not be delivered for the same
			// transaction. Verify each observed shape's own provider reference.
			matched = true
			if envelope.Data.Parent == attempt.ParentTransactionID {
				eventID = envelope.Data.ID
				canonical = sample
			}
		case "issuing.card_transaction.declined":
			matched = envelope.Data.ID == attempt.ParentTransactionID
		}
		if matched {
			hash := sha256.Sum256(record.RawBody)
			hashes[envelope.Name] = hex.EncodeToString(hash[:])
		}
	}
	if len(hashes) != 2 {
		return errors.New("sandbox_signed_event_pair_unverified")
	}
	if eventID == "" {
		eventID, err = scoped.findInitialEvent(ctx, path, "issuing-callback-event-list-page-", attempt.ParentTransactionID, card.ResourceID)
		if err != nil {
			return err
		}
	}
	if canonical == nil {
		var state string
		canonical, _, state = scoped.get(ctx, "/api/v1/issuing/card_transaction_events/"+url.PathEscape(eventID))
		if state != "VERIFIED" {
			return errors.New("sandbox_canonical_callback_event_unverified")
		}
	}
	if !verifiedCanonicalDecline(canonical, eventID, attempt.ParentTransactionID, card.ResourceID) {
		return errors.New("sandbox_canonical_callback_event_unverified")
	}
	if err := captureContract(path, "issuing-callback-event", canonical); err != nil {
		return err
	}
	hash := sha256.Sum256(canonical)
	contract := struct {
		SchemaVersion    int       `json:"schema_version"`
		Environment      string    `json:"environment"`
		RunID            string    `json:"run_id"`
		Source           string    `json:"source"`
		Version          string    `json:"version"`
		CustomerID       string    `json:"customer_id"`
		AccountID        string    `json:"account_id"`
		CardID           string    `json:"card_id"`
		FundingModel     string    `json:"funding_model"`
		AmountConvention string    `json:"amount_convention"`
		CanonicalHash    string    `json:"canonical_evidence_hash"`
		WebhookHashes    []string  `json:"webhook_evidence_hashes"`
		ExpiresAt        time.Time `json:"expires_at"`
	}{1, "SANDBOX", runID, subscription.ID, "2026-08-21", "sandbox-customer-" + runID, scoped.scope, card.ResourceID, "USD_WALLET", "positive_currency_units", hex.EncodeToString(hash[:]), []string{hashes["issuing.card_transaction.declined"], hashes["issuing.card_transaction_event.failed"]}, time.Now().UTC().Add(8 * time.Hour)}
	encoded, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return errors.New("sandbox_shortfall_contract_invalid")
	}
	return writePrivateFile(filepath.Join(filepath.Dir(path), "shortfall-contract.json"), encoded)
}

func verifiedCanonicalDecline(raw []byte, eventID, parentID, cardID string) bool {
	var event canonicalEvent
	if json.Unmarshal(raw, &event) != nil || event.ID != eventID || event.Parent != parentID || event.CardID != cardID || event.Type != "AUTHORIZATION" || event.Subtype != "AUTHORIZATION" || event.Category != "PURCHASE" || event.Result != "DECLINED" || event.Reason != "INSUFFICIENT_FUNDS" || event.BillingCurrency != "USD" {
		return false
	}
	amount, ok := majorAmount(event.BillingAmount)
	expected, _ := majorAmount(json.RawMessage(`9000`))
	return ok && amount.Cmp(expected) == 0
}
