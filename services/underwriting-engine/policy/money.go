package policy

import (
	"math"
	"math/big"
)

const basisPointDenominator int64 = 10000

func addNonnegativeCents(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

func sumNonnegativeCents(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		var ok bool
		total, ok = addNonnegativeCents(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func sumNonnegativeValid(left, right int64) bool {
	_, ok := addNonnegativeCents(left, right)
	return ok
}

func multiplyDivideFloor(value, multiplier, denominator int64) (int64, bool) {
	if value < 0 || multiplier < 0 || denominator <= 0 {
		return 0, false
	}
	numerator := new(big.Int).Mul(big.NewInt(value), big.NewInt(multiplier))
	quotient := numerator.Quo(numerator, big.NewInt(denominator))
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

func calculateFee(principal int64, termDays int, config Config) (int64, bool) {
	if principal <= 0 || termDays < config.settings.Pricing.MinimumTermDays ||
		termDays > config.settings.Pricing.MaximumTermDays {
		return 0, false
	}
	pricing := config.settings.Pricing
	numerator := new(big.Int).Mul(big.NewInt(principal), big.NewInt(pricing.MonthlyRateBasisPoints))
	numerator.Mul(numerator, big.NewInt(int64(termDays)))
	denominator := pricing.DaysPerMonth * basisPointDenominator
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(denominator), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(denominator)) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return addNonnegativeCents(pricing.BaseFeeCents, quotient.Int64())
}

func ceilBasisPoints(value, basisPoints int64) (int64, bool) {
	if value < 0 || basisPoints < 0 || basisPoints > basisPointDenominator {
		return 0, false
	}
	numerator := new(big.Int).Mul(big.NewInt(value), big.NewInt(basisPoints))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(basisPointDenominator), remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

func compareBasisPointShare(numerator, denominator, threshold int64) int {
	left := new(big.Int).Mul(big.NewInt(numerator), big.NewInt(basisPointDenominator))
	right := new(big.Int).Mul(big.NewInt(denominator), big.NewInt(threshold))
	return left.Cmp(right)
}

func subtractFacilityCommitments(total, funded, reserved, reserveFloor int64) (int64, bool) {
	if total <= 0 || funded < 0 || reserved < 0 || reserveFloor < 0 {
		return 0, false
	}
	available := new(big.Int).SetInt64(total)
	available.Sub(available, big.NewInt(funded))
	available.Sub(available, big.NewInt(reserved))
	available.Sub(available, big.NewInt(reserveFloor))
	if available.Sign() <= 0 {
		return 0, true
	}
	if !available.IsInt64() {
		return 0, false
	}
	return available.Int64(), true
}
