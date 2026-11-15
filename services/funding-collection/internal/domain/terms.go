package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	errTermsInvalid = errors.New("terms_invalid")
	termsIDPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
)

// Terms is the dedicated canonical offer preimage. Keep field order aligned
// with the reviewed contract; never replace it with a map or omit-empty tags.
type Terms struct {
	TermsVersion               int    `json:"terms_version"`
	SourceMode                 string `json:"source_mode"`
	FacilityID                 string `json:"facility_id"`
	FundingAccountRef          string `json:"funding_account_ref"`
	FeeBeneficiaryRef          string `json:"fee_beneficiary_ref"`
	CustomerID                 string `json:"customer_id"`
	BorrowerAccountRef         string `json:"borrower_account_ref"`
	CardRef                    string `json:"card_ref"`
	TransactionID              string `json:"transaction_id"`
	OfferID                    string `json:"offer_id"`
	OfferRevision              int64  `json:"offer_revision"`
	PrincipalCents             int64  `json:"principal_cents"`
	ContractFeeCents           int64  `json:"contract_fee_cents"`
	TermDays                   int    `json:"term_days"`
	OfferedOn                  string `json:"offered_on"`
	DueOn                      string `json:"due_on"`
	ExpiresAt                  string `json:"expires_at"`
	PolicyID                   string `json:"policy_id"`
	PolicyHash                 string `json:"policy_hash"`
	DisclosuresVersion         string `json:"disclosures_version"`
	WaterfallVersion           int    `json:"waterfall_version"`
	LateFeeConvention          string `json:"late_fee_convention"`
	CollectionAuthorityID      string `json:"collection_authority_id"`
	CollectionAuthorityVersion int64  `json:"collection_authority_version"`
	MappingFingerprint         string `json:"mapping_fingerprint"`
}

// Validate checks canonical bindings and date/version invariants.
func (t Terms) Validate() error {
	if t.TermsVersion != 1 || t.WaterfallVersion != 1 || (t.SourceMode != "DEMO" && t.SourceMode != "SANDBOX") || t.OfferRevision < 1 || t.PrincipalCents <= 0 || t.ContractFeeCents < 0 || t.CollectionAuthorityVersion <= 0 || t.TermDays < 7 || t.TermDays > 90 {
		return errTermsInvalid
	}
	ids := []string{t.FacilityID, t.FundingAccountRef, t.FeeBeneficiaryRef, t.CustomerID, t.BorrowerAccountRef, t.CardRef, t.TransactionID, t.OfferID, t.PolicyID, t.DisclosuresVersion, t.LateFeeConvention, t.CollectionAuthorityID}
	for _, value := range ids {
		if value == "" || !termsIDPattern.MatchString(value) {
			return errTermsInvalid
		}
	}
	if !lowerSHA256(t.PolicyHash) || !lowerSHA256(t.MappingFingerprint) || t.LateFeeConvention != "OPENING_PRINCIPAL_UTC_DAY" {
		return errTermsInvalid
	}
	offered, err := time.Parse("2006-01-02", t.OfferedOn)
	if err != nil {
		return errTermsInvalid
	}
	due, err := time.Parse("2006-01-02", t.DueOn)
	if err != nil || !due.Equal(offered.AddDate(0, 0, t.TermDays)) {
		return errTermsInvalid
	}
	expiry, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil {
		return errTermsInvalid
	}
	_, offset := expiry.Zone()
	if offset != 0 || expiry.Nanosecond() != 0 || !strings.HasSuffix(t.ExpiresAt, "Z") || expiry.Format("2006-01-02T15:04:05Z") != t.ExpiresAt {
		return errTermsInvalid
	}
	return nil
}

// CanonicalJSON returns the exact dedicated-struct encoding used for terms_hash.
func (t Terms) CanonicalJSON() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Hash returns lowercase SHA-256 of the canonical ordered terms JSON.
func (t Terms) Hash() (string, error) {
	body, err := t.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func lowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}
