package policy

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

//go:embed policy.v1.json
var embeddedPolicy embed.FS

var (
	configOnce   sync.Once
	loadedConfig Config
	configError  error
)

type historyConfig struct {
	MinimumTenureMonths int `json:"minimum_tenure_months"`
	BalanceWindowDays   int `json:"balance_window_days"`
	InflowWindowDays    int `json:"inflow_window_days"`
}

type freshnessConfig struct {
	CustomerMaxAgeSeconds  int64 `json:"customer_max_age_seconds"`
	FacilityMaxAgeSeconds  int64 `json:"facility_max_age_seconds"`
	ShortfallMaxAgeSeconds int64 `json:"shortfall_max_age_seconds"`
	FutureToleranceSeconds int64 `json:"future_tolerance_seconds"`
}

type tierAConfig struct {
	MinimumTenureMonths int   `json:"minimum_tenure_months"`
	LateWindowDays      int   `json:"late_window_days"`
	MaximumLateClosures int   `json:"maximum_late_closures"`
	DefaultWindowDays   int   `json:"default_window_days"`
	MaximumDefaults     int   `json:"maximum_defaults"`
	MaximumNearZeroDays int   `json:"maximum_near_zero_days"`
	LineBasisPoints     int64 `json:"line_basis_points"`
}

type tierBConfig struct {
	MinimumTenureMonths int   `json:"minimum_tenure_months"`
	LateWindowDays      int   `json:"late_window_days"`
	MaximumLateClosures int   `json:"maximum_late_closures"`
	NoDefaultEver       bool  `json:"no_default_ever"`
	LineBasisPoints     int64 `json:"line_basis_points"`
}

type tierCConfig struct {
	DefaultWindowDays int   `json:"default_window_days"`
	LineBasisPoints   int64 `json:"line_basis_points"`
}

type tiersConfig struct {
	A tierAConfig `json:"A"`
	B tierBConfig `json:"B"`
	C tierCConfig `json:"C"`
}

type pricingConfig struct {
	BaseFeeCents           int64 `json:"base_fee_cents"`
	MonthlyRateBasisPoints int64 `json:"monthly_rate_basis_points"`
	DaysPerMonth           int64 `json:"days_per_month"`
	MinimumTermDays        int   `json:"minimum_term_days"`
	MaximumTermDays        int   `json:"maximum_term_days"`
}

type capitalConfig struct {
	AbundantAboveBasisPoints     int64               `json:"abundant_above_basis_points"`
	NormalAboveBasisPoints       int64               `json:"normal_above_basis_points"`
	TightAboveBasisPoints        int64               `json:"tight_above_basis_points"`
	TightHumanApprovalAboveCents int64               `json:"tight_human_approval_above_cents"`
	SpendAllowed                 map[string][]string `json:"spend_allowed"`
	TiersAllowed                 map[string][]string `json:"tiers_allowed"`
}

type policySettings struct {
	PolicyID            string            `json:"policy_id"`
	History             historyConfig     `json:"history"`
	Freshness           freshnessConfig   `json:"freshness"`
	Tiers               tiersConfig       `json:"tiers"`
	Pricing             pricingConfig     `json:"pricing"`
	Capital             capitalConfig     `json:"capital"`
	NearZeroBasisPoints int64             `json:"near_zero_basis_points"`
	MerchantCategories  map[string]string `json:"merchant_categories"`
}

// LoadConfig reads and validates the checked-in policy once, with no environment access.
func LoadConfig() (Config, error) {
	configOnce.Do(loadEmbeddedConfig)
	if configError != nil {
		return Config{}, configError
	}
	return loadedConfig, nil
}

func loadEmbeddedConfig() {
	data, err := embeddedPolicy.ReadFile("policy.v1.json")
	if err != nil {
		configError = errors.New("policy_config_unavailable")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var settings policySettings
	if decoder.Decode(&settings) != nil || decoder.Decode(new(any)) != io.EOF || !validSettings(settings) {
		configError = errors.New("policy_config_invalid")
		return
	}
	digest := sha256.Sum256(data)
	loadedConfig = Config{
		policyID:   settings.PolicyID,
		policyHash: hex.EncodeToString(digest[:]),
		settings:   settings,
	}
}

func validSettings(settings policySettings) bool {
	if settings.PolicyID != "bridge-capital-go-v1" ||
		settings.History.MinimumTenureMonths < 1 || settings.History.BalanceWindowDays < 1 ||
		settings.History.InflowWindowDays < 1 || settings.Freshness.CustomerMaxAgeSeconds < 1 ||
		settings.Freshness.FacilityMaxAgeSeconds < 1 || settings.Freshness.ShortfallMaxAgeSeconds < 1 ||
		settings.Freshness.FutureToleranceSeconds < 0 || settings.Tiers.A.MinimumTenureMonths < 1 ||
		settings.Tiers.A.LateWindowDays < 1 || settings.Tiers.A.MaximumLateClosures < 0 ||
		settings.Tiers.A.DefaultWindowDays < 1 || settings.Tiers.A.MaximumDefaults < 0 ||
		settings.Tiers.A.MaximumNearZeroDays < 0 || !validBasisPoints(settings.Tiers.A.LineBasisPoints) ||
		settings.Tiers.B.MinimumTenureMonths < 1 || settings.Tiers.B.LateWindowDays < 1 ||
		settings.Tiers.B.MaximumLateClosures < 0 || !settings.Tiers.B.NoDefaultEver ||
		!validBasisPoints(settings.Tiers.B.LineBasisPoints) || settings.Tiers.C.DefaultWindowDays < 1 ||
		!validBasisPoints(settings.Tiers.C.LineBasisPoints) || settings.Pricing.BaseFeeCents < 0 ||
		settings.Pricing.MonthlyRateBasisPoints < 0 || settings.Pricing.MonthlyRateBasisPoints > 10000 ||
		settings.Pricing.DaysPerMonth < 1 || settings.Pricing.DaysPerMonth > 365 ||
		settings.Pricing.MinimumTermDays < 1 || settings.Pricing.MaximumTermDays < settings.Pricing.MinimumTermDays ||
		settings.Capital.AbundantAboveBasisPoints > 10000 ||
		settings.Capital.NormalAboveBasisPoints >= settings.Capital.AbundantAboveBasisPoints ||
		settings.Capital.TightAboveBasisPoints >= settings.Capital.NormalAboveBasisPoints ||
		settings.Capital.TightAboveBasisPoints < 0 || settings.Capital.TightHumanApprovalAboveCents < 0 ||
		!validBasisPoints(settings.NearZeroBasisPoints) || len(settings.MerchantCategories) == 0 {
		return false
	}
	for category, criticality := range settings.MerchantCategories {
		if category == "" || !oneOf(criticality, "CRITICAL", "STANDARD", "DISCRETIONARY") {
			return false
		}
	}
	for _, regime := range []string{"ABUNDANT", "NORMAL", "TIGHT", "CRITICAL", "AT_FLOOR"} {
		spend, hasSpend := settings.Capital.SpendAllowed[regime]
		tiers, hasTiers := settings.Capital.TiersAllowed[regime]
		if !hasSpend || !hasTiers {
			return false
		}
		for _, criticality := range spend {
			if !oneOf(criticality, "CRITICAL", "STANDARD", "DISCRETIONARY") {
				return false
			}
		}
		for _, tier := range tiers {
			if !oneOf(tier, "A", "B", "C") {
				return false
			}
		}
	}
	return true
}

func validBasisPoints(value int64) bool {
	return value >= 0 && value <= 10000
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
