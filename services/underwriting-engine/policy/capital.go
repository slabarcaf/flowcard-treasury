package policy

// CapitalDescription is a read-only description of known facility accounting.
// It is never an eligibility, approval or funding decision.
type CapitalDescription struct {
	Known                 bool
	ReserveFloorCents     *int64
	AvailableCapitalCents *int64
	Regime                *CapitalRegime
}

// DescribeCapital uses the same reserve and regime rules as Evaluate.
func DescribeCapital(input FacilitySnapshot, config Config) CapitalDescription {
	if config.PolicyID() == "" || config.PolicyHash() == "" {
		return CapitalDescription{}
	}
	available, ok := facilityAvailable(input)
	if !ok {
		return CapitalDescription{}
	}
	reserve, ok := ceilBasisPoints(input.FacilityTotalCents, input.ReserveFloorBPS)
	if !ok {
		return CapitalDescription{}
	}
	regime, ok := deriveRegime(*available, input.FacilityTotalCents, config)
	if !ok {
		return CapitalDescription{}
	}
	return CapitalDescription{Known: true, ReserveFloorCents: &reserve, AvailableCapitalCents: available, Regime: &regime}
}
