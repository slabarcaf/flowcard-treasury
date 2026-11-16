package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

const verifiedSandboxVersion = "2026-08-21"

// SandboxContract binds observed provider contracts to one installed card and
// subscription. It grants no credit or transfer authority.
type SandboxContract struct {
	SchemaVersion         int       `json:"schema_version"`
	Environment           string    `json:"environment"`
	RunID                 string    `json:"run_id"`
	Source                string    `json:"source"`
	Version               string    `json:"version"`
	CustomerID            string    `json:"customer_id"`
	AccountID             string    `json:"account_id"`
	CardID                string    `json:"card_id"`
	FundingModel          string    `json:"funding_model"`
	AmountConvention      string    `json:"amount_convention"`
	CanonicalEvidenceHash string    `json:"canonical_evidence_hash"`
	WebhookEvidenceHashes []string  `json:"webhook_evidence_hashes"`
	ExpiresAt             time.Time `json:"expires_at"`
	client                *Client
	now                   func() time.Time
}

// LoadSandboxContract is side-effect free apart from reading a private evidence
// file. Every actual observation still verifies current mapping and card funding.
func LoadSandboxContract(path, environment, source string, client *Client) (*SandboxContract, error) {
	if environment != "SANDBOX" || path == "" || source == "" || client == nil {
		return nil, errors.New("sandbox_contract_unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("sandbox_contract_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("sandbox_contract_not_private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("sandbox_contract_invalid")
	}
	var contract SandboxContract
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if !uniqueJSON(raw) || decoder.Decode(&contract) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("sandbox_contract_invalid")
	}
	contract.client = client
	contract.now = time.Now
	if contract.SchemaVersion != 1 || contract.Environment != environment || contract.Source != source || contract.Version != verifiedSandboxVersion || !strings.HasPrefix(contract.RunID, "fc-sbx-") || contract.CustomerID == "" || contract.AccountID == "" || contract.CardID == "" || contract.FundingModel != "USD_WALLET" || contract.AmountConvention != "positive_currency_units" || !contract.ExpiresAt.After(time.Now()) || len(contract.WebhookEvidenceHashes) != 2 {
		return nil, errors.New("sandbox_contract_invalid")
	}
	for _, hash := range append([]string{contract.CanonicalEvidenceHash}, contract.WebhookEvidenceHashes...) {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("sandbox_contract_invalid")
		}
	}
	if client.version != contract.Version || client.baseURL != "https://api.sandbox.airwallex.com" {
		return nil, errors.New("sandbox_contract_client_mismatch")
	}
	return &contract, nil
}

// Extract uses only the two observed version-specific reference paths. Webhook
// IDs never become canonical event IDs; webhook amounts never become facts.
func (c *SandboxContract) Extract(_ context.Context, delivery domain.Delivery) (Reference, error) {
	if c == nil || c.now == nil || !c.ExpiresAt.After(c.now()) || delivery.Environment != "SANDBOX" || delivery.Source != c.Source {
		return Reference{}, &domain.Failure{Reason: "SANDBOX_CONTRACT_UNAVAILABLE"}
	}
	if delivery.EventName != "issuing.card_transaction.declined" && delivery.EventName != "issuing.card_transaction_event.failed" {
		return Reference{}, &domain.Failure{Reason: "UNRELATED_EVENT", Ignored: true}
	}
	var envelope struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		AccountID string `json:"account_id"`
		Version   string `json:"version"`
		Data      struct {
			ID     string `json:"id"`
			CardID string `json:"card_id"`
			Parent string `json:"card_transaction_id"`
		} `json:"data"`
	}
	if !uniqueJSON(delivery.RawBody) || json.Unmarshal(delivery.RawBody, &envelope) != nil || envelope.ID == "" || envelope.ID != delivery.WebhookID || envelope.Name != delivery.EventName || envelope.Data.ID == "" || envelope.AccountID == "" || envelope.Data.CardID == "" {
		return Reference{}, &domain.Failure{Reason: "WEBHOOK_REFERENCE_INVALID"}
	}
	if envelope.Version != c.Version {
		return Reference{}, &domain.Failure{Reason: "UNSUPPORTED_WEBHOOK_VERSION"}
	}
	if envelope.AccountID != c.AccountID || envelope.Data.CardID != c.CardID {
		return Reference{}, &domain.Failure{Reason: "MAPPING_CONFLICT"}
	}
	ref := Reference{AccountID: envelope.AccountID, CardID: envelope.Data.CardID, Version: envelope.Version, Provenance: "observed signed sandbox delivery 2026-08-21; envelope account_id and data.card_id"}
	if delivery.EventName == "issuing.card_transaction_event.failed" {
		if envelope.Data.Parent == "" {
			return Reference{}, &domain.Failure{Reason: "TRANSACTION_REFERENCE_MISSING"}
		}
		ref.EventID = envelope.Data.ID
		ref.TransactionID = envelope.Data.Parent
		ref.Provenance += "; data.id is canonical event; data.card_transaction_id is parent"
	} else {
		ref.TransactionID = envelope.Data.ID
		ref.Provenance += "; data.id is parent transaction"
	}
	return ref, nil
}

// Evidence confirms the wallet-funded US program from a fresh provider read.
func (c *SandboxContract) Evidence(ctx context.Context, mapping domain.Mapping, version string) (domain.ContractEvidence, error) {
	if c == nil || c.client == nil || c.now == nil || !c.ExpiresAt.After(c.now()) || version != c.Version || mapping.AccountID != c.AccountID || mapping.CardID != c.CardID || mapping.CustomerID != c.CustomerID {
		return domain.ContractEvidence{}, &domain.Failure{Reason: "SANDBOX_CONTRACT_UNAVAILABLE"}
	}
	raw, err := c.client.BeginAttempt().get(ctx, "/api/v1/issuing/cards/"+url.PathEscape(mapping.CardID), mapping.AccountID, false)
	if err != nil {
		return domain.ContractEvidence{}, err
	}
	var card struct {
		ID              string `json:"card_id"`
		Status          string `json:"card_status"`
		FundingSourceID string `json:"funding_source_id"`
		Program         struct {
			Purpose string `json:"purpose"`
			Type    string `json:"type"`
			Subtype string `json:"sub_type"`
		} `json:"program"`
	}
	if json.Unmarshal(raw, &card) != nil || card.ID != mapping.CardID || card.Status != "ACTIVE" || card.FundingSourceID != "" || card.Program.Purpose != "COMMERCIAL" || card.Program.Type != "CREDIT" || card.Program.Subtype != "GOOD_FUNDS_CREDIT" {
		return domain.ContractEvidence{}, &domain.Failure{Reason: "FUNDING_MODEL_UNVERIFIED"}
	}
	return domain.ContractEvidence{FundingModel: "USD_WALLET", AmountConvention: "positive_currency_units", Provenance: "sandbox 2026-08-21 scoped current card GET; US COMMERCIAL CREDIT GOOD_FUNDS_CREDIT; canonical numeric USD event evidence " + c.CanonicalEvidenceHash}, nil
}

func uniqueJSON(raw []byte) bool {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("json_depth")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				text, ok := key.(string)
				if !ok || seen[text] {
					return errors.New("json_duplicate")
				}
				seen[text] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("json_delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if value(0) != nil {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
