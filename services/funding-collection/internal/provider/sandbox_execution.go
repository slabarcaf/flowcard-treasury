package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// SandboxQuote is evidence for one exact kind/amount and account pair. It is
// never a general fee schedule or permission to move another customer's funds.
type SandboxQuote struct {
	Kind         OperationKind `json:"kind"`
	AmountCents  int64         `json:"amount_cents"`
	FeeCents     int64         `json:"fee_cents"`
	EvidenceHash string        `json:"evidence_hash"`
}

// SandboxExecutionContract is a private, expiring installation profile built
// from actual sandbox reads, signed events and exact-amount route probes.
type SandboxExecutionContract struct {
	SchemaVersion           int               `json:"schema_version"`
	Environment             string            `json:"environment"`
	RunID                   string            `json:"run_id"`
	FacilityID              string            `json:"facility_id"`
	SourceBindingID         string            `json:"source_binding_id"`
	CustomerID              string            `json:"customer_id"`
	FundingAccountID        string            `json:"funding_account_id"`
	BorrowerAccountID       string            `json:"borrower_account_id"`
	CardID                  string            `json:"card_id"`
	BorrowerGlobalAccountID string            `json:"borrower_global_account_id"`
	Version                 string            `json:"version"`
	FundingPurpose          string            `json:"funding_purpose"`
	TransferFeeField        string            `json:"transfer_fee_field"`
	SettlementTimeField     string            `json:"settlement_time_field"`
	DepositFeeModel         string            `json:"deposit_fee_model"`
	CutoverAt               time.Time         `json:"cutover_at"`
	ExpiresAt               time.Time         `json:"expires_at"`
	FundingVerified         bool              `json:"funding_verified"`
	CollectionVerified      bool              `json:"collection_verified"`
	Quotes                  []SandboxQuote    `json:"quotes"`
	EvidenceHashes          map[string]string `json:"evidence_hashes"`
}

type ExecutionCapabilities struct {
	FundingEnabled    bool
	CollectionEnabled bool
	ProviderVerified  bool
}
type ExecutionGate interface{ ExecutionCapabilities() ExecutionCapabilities }

func (c *SandboxExecutionContract) valid(now time.Time) bool {
	if c == nil || c.SchemaVersion != 1 || c.Environment != "SANDBOX" || !strings.HasPrefix(c.RunID, "fc-sbx-") || c.FacilityID == "" || c.SourceBindingID == "" || c.CustomerID == "" || c.FundingAccountID == "" || c.BorrowerAccountID == "" || c.FundingAccountID == c.BorrowerAccountID || c.CardID == "" || c.BorrowerGlobalAccountID == "" || c.Version != "2026-08-21" || c.FundingPurpose != "investment_capital" || c.TransferFeeField != "fee" || c.SettlementTimeField != "updated_at" || c.DepositFeeModel != "EXPLICIT_ZERO_ONLY" || c.CutoverAt.IsZero() || c.CutoverAt.After(now.Add(5*time.Second)) || !c.ExpiresAt.After(now) || len(c.Quotes) == 0 || len(c.EvidenceHashes) < 4 {
		return false
	}
	seen := map[string]bool{}
	for _, quote := range c.Quotes {
		if (quote.Kind != OperationFund && quote.Kind != OperationCollectFee && quote.Kind != OperationCollectPrincipal) || quote.AmountCents <= 0 || quote.FeeCents < 0 {
			return false
		}
		hash, err := hex.DecodeString(quote.EvidenceHash)
		if err != nil || len(hash) != 32 {
			return false
		}
		key := string(quote.Kind) + ":" + exactCents(quote.AmountCents)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	for _, value := range c.EvidenceHashes {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return false
		}
	}
	return true
}

func loadSandboxExecutionContract(path string) (*SandboxExecutionContract, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("sandbox_execution_contract_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("sandbox_execution_contract_not_private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, errors.New("sandbox_execution_contract_invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var contract SandboxExecutionContract
	if decoder.Decode(&contract) != nil || decoder.Decode(&struct{}{}) != io.EOF || !contract.valid(time.Now()) {
		return nil, errors.New("sandbox_execution_contract_invalid")
	}
	return &contract, nil
}

// ExecutionCapabilities is deliberately independent from actor delegation.
func (a *AirwallexAdapter) ExecutionCapabilities() ExecutionCapabilities {
	if a.mode == "DEMO" && a.providerMode == "MOCK_HTTP" {
		return ExecutionCapabilities{FundingEnabled: true, CollectionEnabled: true}
	}
	c := a.sandboxExecutionContract
	if a.mode != "SANDBOX" || a.providerMode != "AIRWALLEX_SANDBOX" || !c.valid(a.now()) || c.FundingAccountID != a.fundingAccountID {
		return ExecutionCapabilities{}
	}
	payment := a.sandboxPaymentContract
	sameMapping := payment.valid(a.now()) && payment.CustomerID == c.CustomerID && payment.AccountID == c.BorrowerAccountID && payment.CardID == c.CardID && payment.RunID == c.RunID
	return ExecutionCapabilities{FundingEnabled: a.sandboxFundingEnabled && c.FundingVerified && sameMapping, CollectionEnabled: a.sandboxCollectionEnabled && c.CollectionVerified && sameMapping, ProviderVerified: c.FundingVerified && c.CollectionVerified && sameMapping}
}

func (a *AirwallexAdapter) sandboxQuote(kind OperationKind, source, target string, amount int64) (FundingFeeQuote, error) {
	caps := a.ExecutionCapabilities()
	c := a.sandboxExecutionContract
	if kind == OperationFund {
		if !caps.FundingEnabled || source != c.FundingAccountID || target != c.BorrowerAccountID {
			return FundingFeeQuote{}, ErrProviderUnavailable
		}
	} else if !caps.CollectionEnabled || source != c.BorrowerAccountID || target != c.FundingAccountID {
		return FundingFeeQuote{}, ErrProviderUnavailable
	}
	for _, quote := range c.Quotes {
		if quote.Kind == kind && quote.AmountCents == amount {
			digest := sha256.Sum256([]byte(c.RunID + ":" + string(kind) + ":" + exactCents(amount) + ":" + quote.EvidenceHash))
			return FundingFeeQuote{Cents: quote.FeeCents, ContractID: "AIRWALLEX_SANDBOX_EXACT_" + hex.EncodeToString(digest[:])}, nil
		}
	}
	return FundingFeeQuote{}, ErrProviderUnavailable
}

func exactCents(value int64) string { return strconv.FormatInt(value, 10) }

// ValidCollectionFeeQuote accepts only the selected mode's verified zero-fee
// contract. Sandbox quotes are bound by the adapter to an exact kind, amount
// and installation, then checked again before first send.
func ValidCollectionFeeQuote(mode string, quote FundingFeeQuote) bool {
	if quote.Cents != 0 {
		return false
	}
	if mode == "DEMO" {
		return quote.ContractID == "MOCK_HTTP_ZERO_COLLECTION_FEE_V1"
	}
	if mode != "SANDBOX" || !strings.HasPrefix(quote.ContractID, "AIRWALLEX_SANDBOX_EXACT_") {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(quote.ContractID, "AIRWALLEX_SANDBOX_EXACT_"))
	return err == nil && len(raw) == 32
}
