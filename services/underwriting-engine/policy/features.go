package policy

import (
	"math/big"
	"time"
)

func parseDate(value Date) (time.Time, bool) {
	parsed, err := time.Parse("2006-01-02", string(value))
	if err != nil || parsed.Format("2006-01-02") != string(value) {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func formatDate(value time.Time) Date {
	return Date(value.UTC().Format("2006-01-02"))
}

func addMonthsClamped(date time.Time, months int) time.Time {
	firstOfTarget := time.Date(date.Year(), date.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstOfTarget.AddDate(0, 1, -1).Day()
	day := date.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), day, 0, 0, 0, 0, time.UTC)
}

func tenureAtLeast(relationshipStart Date, asOf time.Time, months int) (bool, bool) {
	start, ok := parseDate(relationshipStart)
	if !ok {
		return false, false
	}
	return !addMonthsClamped(start, months).After(asOf.UTC().Truncate(24 * time.Hour)), true
}

func dateInWindow(value Date, today time.Time, windowDays int) bool {
	date, ok := parseDate(value)
	if !ok {
		return false
	}
	current := today.UTC().Truncate(24 * time.Hour)
	start := current.AddDate(0, 0, -windowDays)
	return !date.Before(start) && !date.After(current)
}

func completeBalanceWindow(customer CustomerSnapshot, today time.Time, days int) ([]Balance, bool) {
	if !customer.Coverage.BalanceHistory || customer.BalanceHistory == nil || days < 1 {
		return nil, false
	}
	current := today.UTC().Truncate(24 * time.Hour)
	start := current.AddDate(0, 0, -days)
	end := current.AddDate(0, 0, -1)
	windowStart, startOK := parseDate(customer.HistoryWindow.WindowStart)
	windowEnd, endOK := parseDate(customer.HistoryWindow.WindowEnd)
	if !startOK || !endOK || windowStart.After(start) || windowEnd.Before(end) {
		return nil, false
	}
	byDate := make(map[Date]Balance, len(customer.BalanceHistory))
	for _, balance := range customer.BalanceHistory {
		_, ok := parseDate(balance.Date)
		if !ok {
			return nil, false
		}
		if _, exists := byDate[balance.Date]; exists {
			return nil, false
		}
		byDate[balance.Date] = balance
	}
	balances := make([]Balance, 0, days)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		balance, exists := byDate[formatDate(date)]
		if !exists {
			return nil, false
		}
		balances = append(balances, balance)
	}
	return balances, true
}

func monthlyInflows(customer CustomerSnapshot, today time.Time, days int) (*int64, bool) {
	if !customer.Coverage.InflowHistory || customer.InflowHistory == nil || days < 1 {
		return nil, false
	}
	current := today.UTC().Truncate(24 * time.Hour)
	start := current.AddDate(0, 0, -days)
	end := current.AddDate(0, 0, -1)
	windowStart, startOK := parseDate(customer.HistoryWindow.WindowStart)
	windowEnd, endOK := parseDate(customer.HistoryWindow.WindowEnd)
	if !startOK || !endOK || windowStart.After(start) || windowEnd.Before(end) {
		return nil, false
	}
	var total int64
	seenIDs := make(map[string]struct{}, len(customer.InflowHistory))
	for _, inflow := range customer.InflowHistory {
		date, ok := parseDate(inflow.Date)
		if inflow.ID == "" || inflow.PayerID == "" || !ok || inflow.AmountCents < 0 || !oneOf(inflow.Kind,
			"EXTERNAL_SETTLED", "OWN_TRANSFER", "REFUND", "BRIDGE_FUNDING", "REVERSAL", "UNKNOWN") {
			return nil, false
		}
		if _, exists := seenIDs[inflow.ID]; exists {
			return nil, false
		}
		seenIDs[inflow.ID] = struct{}{}
		if inflow.Kind == "UNKNOWN" {
			return nil, false
		}
		if inflow.Kind != "EXTERNAL_SETTLED" || date.Before(start) || date.After(end) {
			continue
		}
		var sumOK bool
		total, sumOK = addNonnegativeCents(total, inflow.AmountCents)
		if !sumOK {
			return nil, false
		}
	}
	monthly := total / 3
	return &monthly, true
}

func nearZeroDayCount(balances []Balance, thresholdBPS int64) (*int, bool) {
	if len(balances) == 0 {
		return nil, false
	}
	var total int64
	for _, balance := range balances {
		var ok bool
		total, ok = addSignedCents(total, balance.BalanceCents)
		if !ok {
			return nil, false
		}
	}
	if total == 0 {
		allZero := true
		for _, balance := range balances {
			if balance.BalanceCents != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			count := len(balances)
			return &count, true
		}
	}
	count := 0
	for _, balance := range balances {
		factor := int64(len(balances)) * basisPointDenominator
		left := new(big.Int).Mul(big.NewInt(balance.BalanceCents), big.NewInt(factor))
		right := new(big.Int).Mul(big.NewInt(total), big.NewInt(thresholdBPS))
		if left.Cmp(right) < 0 {
			count++
		}
	}
	return &count, true
}

func addSignedCents(left, right int64) (int64, bool) {
	maxInt64 := int64(^uint64(0) >> 1)
	minInt64 := -maxInt64 - 1
	if (right > 0 && left > maxInt64-right) || (right < 0 && left < minInt64-right) {
		return 0, false
	}
	return left + right, true
}

func lateClosureCount(customer CustomerSnapshot, today time.Time, windowDays int) (int, bool) {
	if !customer.Coverage.BridgeHistory || customer.Bridges == nil {
		return 0, false
	}
	count := 0
	for _, bridge := range customer.Bridges {
		if bridge.ClosedOn == nil || bridge.RepaymentOutcome == nil || *bridge.RepaymentOutcome != "LATE" {
			continue
		}
		if dateInWindow(*bridge.ClosedOn, today, windowDays) {
			count++
		}
	}
	return count, true
}

func defaultCount(customer CustomerSnapshot, today time.Time, windowDays int) (int, bool) {
	if (!customer.Coverage.BridgeHistory && !customer.Coverage.LifetimeDefaults) || customer.Bridges == nil {
		return 0, false
	}
	count := 0
	for _, bridge := range customer.Bridges {
		if bridge.DefaultedOn != nil && dateInWindow(*bridge.DefaultedOn, today, windowDays) {
			count++
		}
	}
	return count, true
}

func anyDefault(customer CustomerSnapshot) (bool, bool) {
	if !customer.Coverage.LifetimeDefaults || customer.Bridges == nil {
		return false, false
	}
	for _, bridge := range customer.Bridges {
		if bridge.DefaultedOn != nil {
			return true, true
		}
	}
	return false, true
}

func customerExposure(customer CustomerSnapshot) (*int64, bool) {
	if !customer.Coverage.BridgeHistory || !customer.Coverage.Reservations ||
		customer.Bridges == nil || customer.Reservations == nil {
		return nil, false
	}
	var exposure int64
	bridgesByID := make(map[string]Bridge, len(customer.Bridges))
	debtByTransaction := make(map[string]bool, len(customer.Bridges))
	activeReservationByTransaction := make(map[string]bool, len(customer.Reservations))
	for _, bridge := range customer.Bridges {
		if bridge.ID == "" || bridge.TransactionID == "" || bridge.PrincipalOwedCents < 0 ||
			bridge.FeesOwedCents < 0 || !oneOf(bridge.Status, "OPEN", "REPAID", "DEFAULTED") ||
			(bridge.Status == "REPAID" && (bridge.PrincipalOwedCents != 0 || bridge.FeesOwedCents != 0)) {
			return nil, false
		}
		if !sumNonnegativeValid(bridge.PrincipalOwedCents, bridge.FeesOwedCents) {
			return nil, false
		}
		if _, exists := bridgesByID[bridge.ID]; exists {
			return nil, false
		}
		bridgesByID[bridge.ID] = bridge
		if bridge.PrincipalOwedCents > 0 || bridge.FeesOwedCents > 0 {
			debtByTransaction[bridge.TransactionID] = true
		}
		var ok bool
		exposure, ok = addNonnegativeCents(exposure, bridge.PrincipalOwedCents)
		if !ok {
			return nil, false
		}
		exposure, ok = addNonnegativeCents(exposure, bridge.FeesOwedCents)
		if !ok {
			return nil, false
		}
	}
	for _, reservation := range customer.Reservations {
		if reservation.ID == "" || reservation.TransactionID == "" ||
			reservation.PrincipalCents < 0 || reservation.FeeCents < 0 ||
			!sumNonnegativeValid(reservation.PrincipalCents, reservation.FeeCents) ||
			!oneOf(reservation.Status, "ACTIVE", "CONVERTED", "RELEASED") {
			return nil, false
		}
		switch reservation.Status {
		case "CONVERTED":
			if reservation.BridgeID == nil {
				return nil, false
			}
			bridge, exists := bridgesByID[*reservation.BridgeID]
			if !exists || bridge.TransactionID != reservation.TransactionID {
				return nil, false
			}
			continue
		case "RELEASED":
			if reservation.BridgeID != nil {
				return nil, false
			}
			continue
		}
		if activeReservationByTransaction[reservation.TransactionID] || reservation.BridgeID != nil ||
			debtByTransaction[reservation.TransactionID] {
			return nil, false
		}
		activeReservationByTransaction[reservation.TransactionID] = true
		var ok bool
		exposure, ok = addNonnegativeCents(exposure, reservation.PrincipalCents)
		if !ok {
			return nil, false
		}
		exposure, ok = addNonnegativeCents(exposure, reservation.FeeCents)
		if !ok {
			return nil, false
		}
	}
	return &exposure, true
}

func facilityAvailable(facility FacilitySnapshot) (*int64, bool) {
	if facility.Currency != "USD" || !facility.LedgerComplete ||
		facility.FacilityTotalCents <= 0 || facility.ReserveFloorBPS < 0 || facility.ReserveFloorBPS > basisPointDenominator ||
		facility.FundedPrincipalCents < 0 || facility.ReservedPrincipalCents < 0 {
		return nil, false
	}
	reserve, ok := ceilBasisPoints(facility.FacilityTotalCents, facility.ReserveFloorBPS)
	if !ok {
		return nil, false
	}
	available, ok := subtractFacilityCommitments(facility.FacilityTotalCents,
		facility.FundedPrincipalCents, facility.ReservedPrincipalCents, reserve)
	if !ok {
		return nil, false
	}
	return &available, true
}

func deriveRegime(available, total int64, config Config) (CapitalRegime, bool) {
	if total <= 0 || available < 0 {
		return "", false
	}
	if available == 0 {
		return RegimeAtFloor, true
	}
	capital := config.settings.Capital
	if compareBasisPointShare(available, total, capital.AbundantAboveBasisPoints) > 0 {
		return RegimeAbundant, true
	}
	if compareBasisPointShare(available, total, capital.NormalAboveBasisPoints) > 0 {
		return RegimeNormal, true
	}
	if compareBasisPointShare(available, total, capital.TightAboveBasisPoints) > 0 {
		return RegimeTight, true
	}
	return RegimeCritical, true
}
