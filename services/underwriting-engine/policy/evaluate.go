package policy

import (
	"sort"
	"time"
)

var ruleOrder = []RuleID{
	RuleInputFresh,
	RuleCustomerActive,
	RuleHistorySufficient,
	RuleNoOverdueOrDefault,
	RuleCustomerCapacity,
	RulePaymentNotBridged,
	RuleReceivableCoverage,
	RuleCapitalAvailable,
	RuleSpendAllowed,
	RuleRegimeTierAllowed,
}

type freshness struct {
	rule                   RuleResult
	customerFresh          bool
	facilityFresh          bool
	shortfallFresh         bool
	modeValid              bool
	customerModeCompatible bool
	facilityModeCompatible bool
	customerLinked         bool
	facilityBound          bool
	shortfallValid         bool
	shortfallStale         bool
	customerUsable         bool
	facilityUsable         bool
	shortfallUsable        bool
	customerAge            *int64
	facilityAge            *int64
	shortfallAge           *int64
}

type tierOutcome struct {
	tier  *Tier
	known bool
}

type historyOutcome struct {
	rule     RuleResult
	passed   bool
	known    bool
	balances []Balance
}

// Evaluate applies the versioned ten-rule catalogue to normalized facts and a passed instant.
func Evaluate(input Input, config Config) Result {
	result := Result{
		PolicyID:          config.policyID,
		PolicyHash:        config.policyHash,
		EvaluatedAt:       input.EvaluatedAt.UTC(),
		Status:            DecisionReviewRequired,
		FundingAuthorized: false,
		RequiresRecheck:   true,
		TermDays:          input.TermDays,
		Rules:             make([]RuleResult, 0, len(ruleOrder)),
		ReasonCodes:       make([]string, 0, len(ruleOrder)+1),
	}
	if config.policyID == "" || config.policyHash == "" {
		for _, id := range ruleOrder {
			rule := makeRule(id, RuleUnknown, nil, nil, nil)
			result.Rules = append(result.Rules, rule)
			result.ReasonCodes = append(result.ReasonCodes, rule.ReasonCode)
		}
		result.ReasonCodes = append(result.ReasonCodes, "POLICY_CONFIG_INVALID")
		return result
	}

	fresh := assessFreshness(input, config)
	result.Rules = append(result.Rules, fresh.rule)
	activeRule := assessCustomerActive(input, fresh)
	result.Rules = append(result.Rules, activeRule)

	history := assessHistory(input, fresh, config)
	result.Rules = append(result.Rules, history.rule)
	tier := determineTier(input, fresh, history, config)
	if tier.known && tier.tier != nil {
		value := *tier.tier
		result.Tier = &value
	}

	noOverdueRule := assessNoOverdueOrDefault(input, fresh, config)
	result.Rules = append(result.Rules, noOverdueRule)

	monthlyInflowsValue, inflowsKnown := monthlyInflows(
		input.Customer, input.EvaluatedAt, config.settings.History.InflowWindowDays)
	exposureValue, exposureKnown := customerExposure(input.Customer)
	if !fresh.customerUsable {
		monthlyInflowsValue, inflowsKnown = nil, false
		exposureValue, exposureKnown = nil, false
	}
	if exposureKnown {
		result.ExistingExposureCents = copyInt64(exposureValue)
	}
	if fresh.shortfallUsable {
		principal := input.Shortfall.ShortfallCents
		result.PrincipalCents = int64Pointer(principal)
		if input.TermDays >= config.settings.Pricing.MinimumTermDays &&
			input.TermDays <= config.settings.Pricing.MaximumTermDays {
			if fee, ok := calculateFee(principal, input.TermDays, config); ok {
				result.EstimatedFeeCents = int64Pointer(fee)
				if total, ok := addNonnegativeCents(principal, fee); ok {
					result.TotalDueCents = int64Pointer(total)
				}
			}
		}
	}
	if result.ExistingExposureCents != nil && result.PrincipalCents != nil &&
		result.EstimatedFeeCents != nil {
		if post, ok := sumNonnegativeCents(*result.ExistingExposureCents,
			*result.PrincipalCents, *result.EstimatedFeeCents); ok {
			result.PostFundingExposureCents = int64Pointer(post)
		}
	}

	available, capitalKnown := facilityAvailable(input.Facility)
	if !fresh.facilityUsable {
		available, capitalKnown = nil, false
	}
	if capitalKnown {
		result.AvailableCapitalCents = copyInt64(available)
		regime, ok := deriveRegime(*available, input.Facility.FacilityTotalCents, config)
		if ok {
			result.Regime = regimePointer(regime)
		}
	}

	capacityRule, limit, headroom := assessCustomerCapacity(input, fresh, tier, monthlyInflowsValue,
		inflowsKnown, exposureValue, exposureKnown, result.PrincipalCents,
		result.EstimatedFeeCents, config)
	if limit != nil {
		result.CustomerLimitCents = copyInt64(limit)
	}
	if headroom != nil {
		result.CustomerHeadroomCents = copyInt64(headroom)
	}
	result.Rules = append(result.Rules, capacityRule)

	paymentRule := assessPaymentNotBridged(input, fresh)
	result.Rules = append(result.Rules, paymentRule)

	receivableRule := assessReceivableCoverage(input, fresh, result.PrincipalCents,
		result.EstimatedFeeCents)
	result.Rules = append(result.Rules, receivableRule)

	capitalRule := assessCapitalAvailable(input, fresh, available, capitalKnown,
		result.PrincipalCents)
	result.Rules = append(result.Rules, capitalRule)

	spendRule := assessSpendAllowed(input, fresh, result.Regime, config)
	result.Rules = append(result.Rules, spendRule)

	regimeTierRule := assessRegimeTierAllowed(input, fresh, result.Regime, tier, config)
	result.Rules = append(result.Rules, regimeTierRule)

	result.ApprovalRequired = approvalRequired(result.Regime, result.PrincipalCents, config)
	for _, rule := range result.Rules {
		if rule.Status != RulePass {
			result.ReasonCodes = append(result.ReasonCodes, rule.ReasonCode)
		}
	}
	if result.ApprovalRequired {
		result.ReasonCodes = append(result.ReasonCodes, "HUMAN_APPROVAL_REQUIRED")
	}
	if fresh.shortfallStale {
		result.ReasonCodes = append(result.ReasonCodes, "SHORTFALL_STALE")
	}
	result.Status = overallStatus(result.Rules, result.ApprovalRequired)
	return result
}

func assessFreshness(input Input, config Config) freshness {
	state := freshness{}
	actual := map[string]any{}
	threshold := map[string]any{
		"customer_max_age_seconds":  config.settings.Freshness.CustomerMaxAgeSeconds,
		"facility_max_age_seconds":  config.settings.Freshness.FacilityMaxAgeSeconds,
		"shortfall_max_age_seconds": config.settings.Freshness.ShortfallMaxAgeSeconds,
		"future_tolerance_seconds":  config.settings.Freshness.FutureToleranceSeconds,
	}
	if input.EvaluatedAt.IsZero() {
		state.rule = makeRule(RuleInputFresh, RuleUnknown, actual, threshold, allSourceReferences(input))
		return state
	}
	now := input.EvaluatedAt.UTC()
	state.customerFresh, state.customerAge = freshAge(input.Customer.CapturedAt, now,
		time.Duration(config.settings.Freshness.CustomerMaxAgeSeconds)*time.Second,
		time.Duration(config.settings.Freshness.FutureToleranceSeconds)*time.Second)
	state.facilityFresh, state.facilityAge = freshAge(input.Facility.CapturedAt, now,
		time.Duration(config.settings.Freshness.FacilityMaxAgeSeconds)*time.Second,
		time.Duration(config.settings.Freshness.FutureToleranceSeconds)*time.Second)
	state.shortfallFresh, state.shortfallAge = freshAge(input.Shortfall.BalanceObservedAt, now,
		time.Duration(config.settings.Freshness.ShortfallMaxAgeSeconds)*time.Second,
		time.Duration(config.settings.Freshness.FutureToleranceSeconds)*time.Second)
	state.shortfallStale = !input.Shortfall.BalanceObservedAt.IsZero() &&
		now.Sub(input.Shortfall.BalanceObservedAt) >
			time.Duration(config.settings.Freshness.ShortfallMaxAgeSeconds)*time.Second
	state.modeValid = oneOf(input.Mode, "DEMO", "SANDBOX")
	state.customerModeCompatible = input.Customer.Mode == input.Mode
	state.facilityModeCompatible = input.Facility.Mode == input.Mode
	state.customerLinked = input.Customer.CustomerID != "" &&
		input.Customer.CustomerID == input.Shortfall.CustomerID &&
		input.Customer.AccountID != "" && input.Customer.AccountID == input.Shortfall.AccountID &&
		input.Customer.CardID != "" && input.Customer.CardID == input.Shortfall.CardID
	state.facilityBound = input.FacilityID != "" && input.Facility.FacilityID == input.FacilityID
	state.shortfallValid = validShortfall(input.Shortfall)
	state.customerUsable = state.customerFresh && state.modeValid && state.customerModeCompatible && state.customerLinked &&
		validSnapshotIdentity(input.Customer.ID, input.Customer.Version, input.Customer.SourceRefs)
	state.facilityUsable = state.facilityFresh && state.modeValid && state.facilityModeCompatible && state.facilityBound &&
		input.Facility.LedgerVersion != "" &&
		validSnapshotIdentity(input.Facility.ID, input.Facility.Version, input.Facility.SourceRefs)
	state.shortfallUsable = state.shortfallFresh && state.modeValid &&
		state.shortfallValid && input.Shortfall.TransactionID != ""
	actual["customer_age_seconds"] = state.customerAge
	actual["facility_age_seconds"] = state.facilityAge
	actual["shortfall_balance_age_seconds"] = state.shortfallAge
	actual["configured_mode_valid"] = state.modeValid
	actual["customer_mode_compatible"] = state.customerModeCompatible
	actual["facility_mode_compatible"] = state.facilityModeCompatible
	actual["customer_linkage_valid"] = state.customerLinked
	actual["facility_binding_valid"] = state.facilityBound
	if state.customerUsable && state.facilityUsable && state.shortfallUsable && state.customerLinked {
		state.rule = boolRule(RuleInputFresh, true, actual, threshold, allSourceReferences(input))
	} else {
		state.rule = makeRule(RuleInputFresh, RuleUnknown, actual, threshold, allSourceReferences(input))
	}
	return state
}

func freshAge(capturedAt, now time.Time, maximumAge, futureTolerance time.Duration) (bool, *int64) {
	if capturedAt.IsZero() {
		return false, nil
	}
	age := now.Sub(capturedAt)
	if age < -futureTolerance || age > maximumAge {
		seconds := int64(age / time.Second)
		return false, &seconds
	}
	if age < 0 {
		age = 0
	}
	seconds := int64(age / time.Second)
	return true, &seconds
}

func validSnapshotIdentity(id string, version int64, references []SourceReference) bool {
	if id == "" || version <= 0 || len(references) == 0 {
		return false
	}
	for _, reference := range references {
		_, offset := reference.EffectiveAt.Zone()
		if reference.System == "" || reference.RecordID == "" || reference.Version == "" ||
			reference.EffectiveAt.IsZero() || offset != 0 {
			return false
		}
	}
	return true
}

func validShortfall(shortfall Shortfall) bool {
	if shortfall.ID == "" || shortfall.CustomerID == "" || shortfall.AccountID == "" ||
		shortfall.CardID == "" || shortfall.CanonicalEventID == "" || shortfall.TransactionID == "" ||
		shortfall.Outcome != "SHORTFALL_DETECTED" || shortfall.Currency != "USD" ||
		shortfall.AttemptedAmountCents <= 0 || shortfall.AvailableBalanceCents < 0 || shortfall.ShortfallCents <= 0 {
		return false
	}
	expected := shortfall.AttemptedAmountCents - shortfall.AvailableBalanceCents
	if expected < 0 {
		expected = 0
	}
	return expected == shortfall.ShortfallCents
}

func assessCustomerActive(input Input, fresh freshness) RuleResult {
	actual := map[string]any{}
	threshold := map[string]any{"onboarding_complete": true, "active": true,
		"suspended": false, "provider_account_status": "ACTIVE"}
	if !fresh.customerUsable || !input.Customer.Coverage.Standing || input.Customer.Standing == nil {
		return makeRule(RuleCustomerActive, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	standing := input.Customer.Standing
	if standing.OnboardingComplete != nil {
		actual["onboarding_complete"] = *standing.OnboardingComplete
	}
	if standing.Active != nil {
		actual["active"] = *standing.Active
	}
	if standing.Suspended != nil {
		actual["suspended"] = *standing.Suspended
	}
	if standing.ProviderAccountStatus != nil {
		actual["provider_account_status"] = *standing.ProviderAccountStatus
	}
	knownFail := (standing.OnboardingComplete != nil && !*standing.OnboardingComplete) ||
		(standing.Active != nil && !*standing.Active) ||
		(standing.Suspended != nil && *standing.Suspended) ||
		(standing.ProviderAccountStatus != nil &&
			oneOf(*standing.ProviderAccountStatus, "FROZEN", "CLOSED"))
	if knownFail {
		return boolRule(RuleCustomerActive, false, actual, threshold, input.Customer.SourceRefs)
	}
	if standing.OnboardingComplete == nil || standing.Active == nil || standing.Suspended == nil ||
		standing.ProviderAccountStatus == nil || *standing.ProviderAccountStatus == "UNKNOWN" {
		return makeRule(RuleCustomerActive, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	if !oneOf(*standing.ProviderAccountStatus, "ACTIVE", "FROZEN", "CLOSED") {
		return makeRule(RuleCustomerActive, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	return boolRule(RuleCustomerActive, true, actual, threshold, input.Customer.SourceRefs)
}

func assessHistory(input Input, fresh freshness, config Config) historyOutcome {
	outcome := historyOutcome{}
	actual := map[string]any{}
	threshold := map[string]any{"minimum_tenure_months": config.settings.History.MinimumTenureMonths,
		"complete_balance_days": config.settings.History.BalanceWindowDays}
	if !fresh.customerUsable || !input.Customer.Coverage.Standing || input.Customer.Standing == nil ||
		input.Customer.Standing.RelationshipStartedOn == nil || input.EvaluatedAt.IsZero() {
		outcome.rule = makeRule(RuleHistorySufficient, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
		return outcome
	}
	tenureOK, tenureKnown := tenureAtLeast(*input.Customer.Standing.RelationshipStartedOn,
		input.EvaluatedAt, config.settings.History.MinimumTenureMonths)
	if !tenureKnown {
		outcome.rule = makeRule(RuleHistorySufficient, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
		return outcome
	}
	actual["relationship_started_on"] = string(*input.Customer.Standing.RelationshipStartedOn)
	actual["tenure_at_least_minimum"] = tenureOK
	if !tenureOK {
		outcome.known = true
		outcome.rule = boolRule(RuleHistorySufficient, false, actual, threshold, input.Customer.SourceRefs)
		return outcome
	}
	balances, complete := completeBalanceWindow(input.Customer, input.EvaluatedAt,
		config.settings.History.BalanceWindowDays)
	actual["complete_balance_days"] = len(balances)
	if !complete {
		outcome.rule = makeRule(RuleHistorySufficient, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
		return outcome
	}
	outcome.passed, outcome.known, outcome.balances = true, true, balances
	outcome.rule = boolRule(RuleHistorySufficient, true, actual, threshold, input.Customer.SourceRefs)
	return outcome
}

func determineTier(input Input, fresh freshness, history historyOutcome, config Config) tierOutcome {
	if !fresh.customerUsable || !history.known || input.Customer.Standing == nil ||
		input.Customer.Standing.RelationshipStartedOn == nil || input.EvaluatedAt.IsZero() {
		return tierOutcome{}
	}
	if !history.passed {
		return tierOutcome{known: true}
	}
	today := input.EvaluatedAt.UTC()
	recentDefaults, defaultEvidenceKnown := defaultCount(input.Customer, today,
		config.settings.Tiers.C.DefaultWindowDays)
	if !defaultEvidenceKnown {
		return tierOutcome{}
	}
	if recentDefaults > 0 {
		return tierOutcome{known: true}
	}
	tenureA, tenureKnown := tenureAtLeast(*input.Customer.Standing.RelationshipStartedOn, today,
		config.settings.Tiers.A.MinimumTenureMonths)
	tenureB, tenureBKnown := tenureAtLeast(*input.Customer.Standing.RelationshipStartedOn, today,
		config.settings.Tiers.B.MinimumTenureMonths)
	if !tenureKnown || !tenureBKnown {
		return tierOutcome{}
	}
	lateA, lateKnown := lateClosureCount(input.Customer, today, config.settings.Tiers.A.LateWindowDays)
	defaultsA, defaultsAKnown := defaultCount(input.Customer, today, config.settings.Tiers.A.DefaultWindowDays)
	nearZero, nearZeroKnown := nearZeroDayCount(history.balances, config.settings.NearZeroBasisPoints)
	aFailed := !tenureA || (lateKnown && lateA > config.settings.Tiers.A.MaximumLateClosures) ||
		(defaultsAKnown && defaultsA > config.settings.Tiers.A.MaximumDefaults) ||
		(nearZeroKnown && *nearZero > config.settings.Tiers.A.MaximumNearZeroDays)
	if !aFailed {
		if !lateKnown || !defaultsAKnown || !nearZeroKnown {
			return tierOutcome{}
		}
		return tierOutcome{tier: tierPointer(TierA), known: true}
	}

	lateB, lateBKnown := lateClosureCount(input.Customer, today, config.settings.Tiers.B.LateWindowDays)
	defaultEver, defaultEverKnown := anyDefault(input.Customer)
	bFailed := !tenureB || (lateBKnown && lateB > config.settings.Tiers.B.MaximumLateClosures) ||
		(defaultEverKnown && defaultEver)
	if !bFailed {
		if !lateBKnown || !defaultEverKnown {
			return tierOutcome{}
		}
		return tierOutcome{tier: tierPointer(TierB), known: true}
	}

	defaultsC, defaultsCKnown := defaultCount(input.Customer, today, config.settings.Tiers.C.DefaultWindowDays)
	if !defaultsCKnown {
		return tierOutcome{}
	}
	if defaultsC > 0 {
		return tierOutcome{known: true}
	}
	return tierOutcome{tier: tierPointer(TierC), known: true}
}

func assessNoOverdueOrDefault(input Input, fresh freshness, config Config) RuleResult {
	actual := map[string]any{}
	threshold := map[string]any{"default_lookback_days": config.settings.Tiers.C.DefaultWindowDays,
		"overdue_before_today": true}
	if !fresh.customerUsable || input.Customer.Bridges == nil || input.EvaluatedAt.IsZero() {
		return makeRule(RuleNoOverdueOrDefault, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	debtComplete := input.Customer.Coverage.BridgeHistory
	defaultsComplete := input.Customer.Coverage.BridgeHistory || input.Customer.Coverage.LifetimeDefaults
	if !debtComplete && !defaultsComplete {
		return makeRule(RuleNoOverdueOrDefault, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	today := input.EvaluatedAt.UTC().Truncate(24 * time.Hour)
	overdueCents := int64(0)
	recentDefaults := 0
	knownFail := false
	debtUnknown := !debtComplete
	defaultsUnknown := !defaultsComplete
	for _, bridge := range input.Customer.Bridges {
		if debtComplete {
			if bridge.PrincipalOwedCents < 0 || bridge.FeesOwedCents < 0 ||
				!oneOf(bridge.Status, "OPEN", "REPAID", "DEFAULTED") ||
				(bridge.Status == "REPAID" && (bridge.PrincipalOwedCents != 0 || bridge.FeesOwedCents != 0)) {
				debtUnknown = true
			} else {
				dueOn, ok := parseDate(bridge.DueOn)
				if !ok {
					debtUnknown = true
				} else if (bridge.PrincipalOwedCents > 0 || bridge.FeesOwedCents > 0) && dueOn.Before(today) {
					knownFail = true
					total, sumOK := sumNonnegativeCents(bridge.PrincipalOwedCents, bridge.FeesOwedCents)
					if !sumOK {
						debtUnknown = true
					} else if sum, ok := addNonnegativeCents(overdueCents, total); ok {
						overdueCents = sum
					} else {
						debtUnknown = true
					}
				}
			}
		}
		if defaultsComplete && bridge.DefaultedOn != nil {
			defaultDate, valid := parseDate(*bridge.DefaultedOn)
			if !valid || defaultDate.After(today) {
				defaultsUnknown = true
			} else if !defaultDate.Before(today.AddDate(0, 0, -config.settings.Tiers.C.DefaultWindowDays)) {
				knownFail = true
				recentDefaults++
			}
		}
	}
	if debtComplete {
		actual["overdue_debt_cents"] = overdueCents
	}
	if defaultsComplete {
		actual["recent_defaults"] = recentDefaults
	}
	if knownFail {
		return boolRule(RuleNoOverdueOrDefault, false, actual, threshold, input.Customer.SourceRefs)
	}
	if debtUnknown || defaultsUnknown {
		return makeRule(RuleNoOverdueOrDefault, RuleUnknown, actual, threshold, input.Customer.SourceRefs)
	}
	return boolRule(RuleNoOverdueOrDefault, true, actual, threshold, input.Customer.SourceRefs)
}

func assessCustomerCapacity(input Input, fresh freshness, tier tierOutcome, inflows *int64,
	inflowsKnown bool, exposure *int64, exposureKnown bool, principal, fee *int64,
	config Config) (RuleResult, *int64, *int64) {
	actual := map[string]any{}
	if !fresh.customerUsable || !tier.known || tier.tier == nil ||
		!inflowsKnown || inflows == nil || !exposureKnown || exposure == nil {
		return makeRule(RuleCustomerCapacity, RuleUnknown, actual, nil, input.Customer.SourceRefs), nil, nil
	}
	lineBPS := lineBasisPoints(*tier.tier, config)
	limit, ok := multiplyDivideFloor(*inflows, lineBPS, basisPointDenominator)
	if !ok {
		return makeRule(RuleCustomerCapacity, RuleUnknown, actual, nil, input.Customer.SourceRefs), nil, nil
	}
	headroom := int64(0)
	if *exposure < limit {
		headroom = limit - *exposure
	}
	if !fresh.shortfallUsable || principal == nil || fee == nil {
		actual["monthly_inflows_cents"] = *inflows
		actual["existing_exposure_cents"] = *exposure
		actual["customer_limit_cents"] = limit
		actual["customer_headroom_cents"] = headroom
		actual["tier"] = string(*tier.tier)
		return makeRule(RuleCustomerCapacity, RuleUnknown, actual,
			map[string]any{"line_basis_points": lineBPS, "maximum_exposure_cents": limit},
			input.Customer.SourceRefs), int64Pointer(limit), int64Pointer(headroom)
	}
	post, ok := sumNonnegativeCents(*exposure, *principal, *fee)
	if !ok {
		return makeRule(RuleCustomerCapacity, RuleUnknown, actual, nil, input.Customer.SourceRefs), nil, nil
	}
	actual["monthly_inflows_cents"] = *inflows
	actual["existing_exposure_cents"] = *exposure
	actual["requested_principal_cents"] = *principal
	actual["estimated_fee_cents"] = *fee
	actual["post_funding_exposure_cents"] = post
	actual["customer_limit_cents"] = limit
	actual["customer_headroom_cents"] = headroom
	actual["tier"] = string(*tier.tier)
	threshold := map[string]any{"line_basis_points": lineBPS, "maximum_exposure_cents": limit}
	return boolRule(RuleCustomerCapacity, post <= limit, actual, threshold, input.Customer.SourceRefs),
		int64Pointer(limit), int64Pointer(headroom)
}

func assessPaymentNotBridged(input Input, fresh freshness) RuleResult {
	actual := map[string]any{}
	if !fresh.customerUsable || !input.Customer.Coverage.BridgeHistory || !input.Customer.Coverage.Reservations ||
		input.Customer.Bridges == nil || input.Customer.Reservations == nil ||
		!fresh.shortfallUsable || input.Shortfall.TransactionID == "" {
		return makeRule(RulePaymentNotBridged, RuleUnknown, actual,
			map[string]any{"active_obligations": 0}, input.Customer.SourceRefs)
	}
	count := 0
	unknown := false
	for _, bridge := range input.Customer.Bridges {
		if bridge.TransactionID != input.Shortfall.TransactionID {
			continue
		}
		if bridge.PrincipalOwedCents < 0 || bridge.FeesOwedCents < 0 ||
			!oneOf(bridge.Status, "OPEN", "REPAID", "DEFAULTED") {
			unknown = true
			continue
		}
		if bridge.Status == "REPAID" && (bridge.PrincipalOwedCents != 0 || bridge.FeesOwedCents != 0) {
			unknown = true
			continue
		}
		if bridge.Status == "OPEN" || (bridge.Status == "DEFAULTED" &&
			(bridge.PrincipalOwedCents > 0 || bridge.FeesOwedCents > 0)) {
			count++
		}
	}
	for _, reservation := range input.Customer.Reservations {
		if reservation.TransactionID != input.Shortfall.TransactionID {
			continue
		}
		if reservation.PrincipalCents < 0 || reservation.FeeCents < 0 ||
			!oneOf(reservation.Status, "ACTIVE", "CONVERTED", "RELEASED") {
			unknown = true
			continue
		}
		switch reservation.Status {
		case "ACTIVE":
			count++
		case "CONVERTED":
			if reservation.BridgeID == nil {
				unknown = true
				continue
			}
			linked := false
			for _, bridge := range input.Customer.Bridges {
				if bridge.ID == *reservation.BridgeID && bridge.TransactionID == reservation.TransactionID {
					linked = true
					break
				}
			}
			if !linked {
				unknown = true
			}
		case "RELEASED":
			if reservation.BridgeID != nil {
				unknown = true
			}
		}
	}
	actual["active_obligations"] = count
	actual["transaction_id"] = input.Shortfall.TransactionID
	if count > 0 {
		return boolRule(RulePaymentNotBridged, false, actual,
			map[string]any{"active_obligations": 0}, input.Customer.SourceRefs)
	}
	if unknown {
		return makeRule(RulePaymentNotBridged, RuleUnknown, actual,
			map[string]any{"active_obligations": 0}, input.Customer.SourceRefs)
	}
	return boolRule(RulePaymentNotBridged, true, actual,
		map[string]any{"active_obligations": 0}, input.Customer.SourceRefs)
}

func assessReceivableCoverage(input Input, fresh freshness, principal, fee *int64) RuleResult {
	actual := map[string]any{}
	if !fresh.customerUsable || !fresh.shortfallUsable || !input.Customer.Coverage.Receivables ||
		input.Customer.Receivables == nil ||
		principal == nil || fee == nil || input.EvaluatedAt.IsZero() {
		return makeRule(RuleReceivableCoverage, RuleUnknown, actual, nil, input.Customer.SourceRefs)
	}
	required, ok := addNonnegativeCents(*principal, *fee)
	if !ok {
		return makeRule(RuleReceivableCoverage, RuleUnknown, actual, nil, input.Customer.SourceRefs)
	}
	today := input.EvaluatedAt.UTC().Truncate(24 * time.Hour)
	termEnd := today.AddDate(0, 0, input.TermDays)
	var freeTotal int64
	unknown := false
	seenInvoiceIDs := make(map[string]struct{}, len(input.Customer.Receivables))
	for _, invoice := range input.Customer.Receivables {
		if invoice.InvoiceID == "" {
			unknown = true
			continue
		}
		if _, exists := seenInvoiceIDs[invoice.InvoiceID]; exists {
			unknown = true
			continue
		}
		seenInvoiceIDs[invoice.InvoiceID] = struct{}{}
		if !invoice.DueDateVerified || invoice.DueOn == nil {
			unknown = true
			continue
		}
		dueOn, valid := parseDate(*invoice.DueOn)
		if !valid {
			unknown = true
			continue
		}
		if dueOn.Before(today) || dueOn.After(termEnd) {
			continue
		}
		if !invoice.AllocationComplete || invoice.Currency != "USD" ||
			invoice.EligibleUnpaidCents < 0 || invoice.AllocatedCents < 0 ||
			invoice.AllocatedCents > invoice.EligibleUnpaidCents ||
			!sourceReferenceMember(invoice.SourceRef, input.Customer.SourceRefs) {
			unknown = true
			continue
		}
		free := invoice.EligibleUnpaidCents - invoice.AllocatedCents
		var sumOK bool
		freeTotal, sumOK = addNonnegativeCents(freeTotal, free)
		if !sumOK {
			unknown = true
		}
	}
	actual["required_coverage_cents"] = required
	actual["verified_free_receivables_cents"] = freeTotal
	actual["term_end"] = formatDate(termEnd)
	if unknown {
		return makeRule(RuleReceivableCoverage, RuleUnknown, actual,
			map[string]any{"minimum_free_receivables_cents": required}, input.Customer.SourceRefs)
	}
	return boolRule(RuleReceivableCoverage, freeTotal >= required, actual,
		map[string]any{"minimum_free_receivables_cents": required}, input.Customer.SourceRefs)
}

func assessCapitalAvailable(input Input, fresh freshness, available *int64, capitalKnown bool, principal *int64) RuleResult {
	actual := map[string]any{}
	if !fresh.facilityUsable || !fresh.shortfallUsable || !capitalKnown || available == nil || principal == nil {
		return makeRule(RuleCapitalAvailable, RuleUnknown, actual, nil, input.Facility.SourceRefs)
	}
	actual["requested_principal_cents"] = *principal
	actual["available_capital_cents"] = *available
	return boolRule(RuleCapitalAvailable, *principal <= *available, actual,
		map[string]any{"maximum_principal_cents": *available}, input.Facility.SourceRefs)
}

func assessSpendAllowed(input Input, fresh freshness, regime *CapitalRegime, config Config) RuleResult {
	actual := map[string]any{}
	refs := allSourceReferences(input)
	if !fresh.facilityUsable || regime == nil {
		return makeRule(RuleSpendAllowed, RuleUnknown, actual, nil, refs)
	}
	actual["regime"] = string(*regime)
	if *regime == RegimeAtFloor {
		actual["merchant_category"] = nil
		return boolRule(RuleSpendAllowed, false, actual,
			map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
	}
	if *regime == RegimeAbundant || *regime == RegimeNormal {
		return boolRule(RuleSpendAllowed, true, actual,
			map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
	}
	if !fresh.customerUsable || !input.Customer.Coverage.Classification || input.Customer.Classifications == nil {
		return makeRule(RuleSpendAllowed, RuleUnknown, actual,
			map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
	}
	category, found, valid := classificationFor(input.Customer, input.Shortfall.TransactionID)
	if !found || !valid {
		return makeRule(RuleSpendAllowed, RuleUnknown, actual,
			map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
	}
	criticality, mapped := config.settings.MerchantCategories[category]
	actual["merchant_category"] = category
	if !mapped {
		return makeRule(RuleSpendAllowed, RuleUnknown, actual,
			map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
	}
	actual["criticality"] = criticality
	return boolRule(RuleSpendAllowed, containsString(config.settings.Capital.SpendAllowed[string(*regime)], criticality),
		actual, map[string]any{"allowed_criticalities": copyStrings(config.settings.Capital.SpendAllowed[string(*regime)])}, refs)
}

func assessRegimeTierAllowed(input Input, fresh freshness, regime *CapitalRegime, tier tierOutcome, config Config) RuleResult {
	refs := allSourceReferences(input)
	if !fresh.facilityUsable || regime == nil {
		return makeRule(RuleRegimeTierAllowed, RuleUnknown, nil, nil, refs)
	}
	allowed := config.settings.Capital.TiersAllowed[string(*regime)]
	actual := map[string]any{"regime": string(*regime)}
	if *regime == RegimeAtFloor {
		actual["tier"] = nil
		return boolRule(RuleRegimeTierAllowed, false, actual,
			map[string]any{"allowed_tiers": copyStrings(allowed)}, refs)
	}
	if !tier.known {
		return makeRule(RuleRegimeTierAllowed, RuleUnknown, actual,
			map[string]any{"allowed_tiers": copyStrings(allowed)}, refs)
	}
	if tier.tier == nil {
		actual["tier"] = nil
		return boolRule(RuleRegimeTierAllowed, false, actual,
			map[string]any{"allowed_tiers": copyStrings(allowed)}, refs)
	}
	actual["tier"] = string(*tier.tier)
	return boolRule(RuleRegimeTierAllowed, containsString(allowed, string(*tier.tier)), actual,
		map[string]any{"allowed_tiers": copyStrings(allowed)}, refs)
}

func approvalRequired(regime *CapitalRegime, principal *int64, config Config) bool {
	if regime == nil {
		return false
	}
	switch *regime {
	case RegimeCritical:
		return true
	case RegimeTight:
		return principal != nil && *principal > config.settings.Capital.TightHumanApprovalAboveCents
	default:
		return false
	}
}

func classificationFor(customer CustomerSnapshot, transactionID string) (string, bool, bool) {
	category := ""
	found := false
	for _, classification := range customer.Classifications {
		if classification.TransactionID != transactionID {
			continue
		}
		if classification.MerchantCategory == "" ||
			!sourceReferenceMember(classification.SourceRef, customer.SourceRefs) {
			return "", true, false
		}
		if found && category != classification.MerchantCategory {
			return "", true, false
		}
		category = classification.MerchantCategory
		found = true
	}
	return category, found, found
}

func sourceReferenceMember(reference SourceReference, available []SourceReference) bool {
	if reference.System == "" || reference.RecordID == "" || reference.Version == "" || reference.EffectiveAt.IsZero() {
		return false
	}
	_, offset := reference.EffectiveAt.Zone()
	if offset != 0 {
		return false
	}
	for _, candidate := range available {
		if candidate.System == reference.System && candidate.RecordID == reference.RecordID &&
			candidate.Version == reference.Version && candidate.EffectiveAt.Equal(reference.EffectiveAt) {
			return true
		}
	}
	return false
}

func lineBasisPoints(tier Tier, config Config) int64 {
	switch tier {
	case TierA:
		return config.settings.Tiers.A.LineBasisPoints
	case TierB:
		return config.settings.Tiers.B.LineBasisPoints
	case TierC:
		return config.settings.Tiers.C.LineBasisPoints
	default:
		return 0
	}
}

func boolRule(id RuleID, passed bool, actual, threshold map[string]any, refs []SourceReference) RuleResult {
	status := RuleFail
	if passed {
		status = RulePass
	}
	return makeRule(id, status, actual, threshold, refs)
}

func makeRule(id RuleID, status RuleStatus, actual, threshold map[string]any, refs []SourceReference) RuleResult {
	if actual == nil {
		actual = map[string]any{}
	}
	if threshold == nil {
		threshold = map[string]any{}
	}
	reasonCode := string(id) + "_UNKNOWN"
	if status == RulePass {
		reasonCode = string(id) + "_PASSED"
	} else if status == RuleFail {
		reasonCode = string(id) + "_FAILED"
	}
	sourceRefs := make([]SourceReference, len(refs))
	copy(sourceRefs, refs)
	return RuleResult{RuleID: id, Status: status, ReasonCode: reasonCode,
		Actual: actual, Threshold: threshold, SourceRefs: sourceRefs}
}

func overallStatus(rules []RuleResult, approvalRequired bool) DecisionStatus {
	unknown := false
	for _, rule := range rules {
		if rule.Status == RuleFail {
			return DecisionIneligible
		}
		if rule.Status == RuleUnknown {
			unknown = true
		}
	}
	if unknown || approvalRequired {
		return DecisionReviewRequired
	}
	return DecisionEligible
}

func allSourceReferences(input Input) []SourceReference {
	all := append([]SourceReference(nil), input.Customer.SourceRefs...)
	all = append(all, input.Facility.SourceRefs...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].System != all[j].System {
			return all[i].System < all[j].System
		}
		if all[i].RecordID != all[j].RecordID {
			return all[i].RecordID < all[j].RecordID
		}
		if all[i].Version != all[j].Version {
			return all[i].Version < all[j].Version
		}
		return all[i].EffectiveAt.Before(all[j].EffectiveAt)
	})
	unique := all[:0]
	for _, ref := range all {
		if len(unique) > 0 && unique[len(unique)-1].System == ref.System &&
			unique[len(unique)-1].RecordID == ref.RecordID && unique[len(unique)-1].Version == ref.Version &&
			unique[len(unique)-1].EffectiveAt.Equal(ref.EffectiveAt) {
			continue
		}
		unique = append(unique, ref)
	}
	return unique
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func copyStrings(values []string) []string {
	copied := make([]string, len(values))
	copy(copied, values)
	return copied
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	return int64Pointer(*value)
}

func int64Pointer(value int64) *int64                  { return &value }
func tierPointer(value Tier) *Tier                     { return &value }
func regimePointer(value CapitalRegime) *CapitalRegime { return &value }
