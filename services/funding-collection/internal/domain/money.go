package domain

import (
	"errors"
	"math"
	"math/big"
)

// Money arithmetic is checked and always expressed in USD cents.
var (
	ErrMoneyOverflow = errors.New("money_overflow")
	ErrMoneyNegative = errors.New("money_negative")
	ErrMoneyInvalid  = errors.New("money_invalid")
)

// AddMoney adds two nonnegative amounts without overflowing int64.
func AddMoney(left, right Money) (Money, error) {
	if left < 0 || right < 0 {
		return 0, ErrMoneyNegative
	}
	if int64(left) > math.MaxInt64-int64(right) {
		return 0, ErrMoneyOverflow
	}
	return left + right, nil
}

// SubtractMoney subtracts nonnegative amounts and rejects a negative result.
func SubtractMoney(left, right Money) (Money, error) {
	if left < 0 || right < 0 {
		return 0, ErrMoneyNegative
	}
	if right > left {
		return 0, ErrMoneyNegative
	}
	return left - right, nil
}

// MultiplyDivideHalfUp computes round-half-up(value*multiplier/divisor) exactly.
func MultiplyDivideHalfUp(value Money, multiplier, divisor int64) (Money, error) {
	if value < 0 || multiplier < 0 || divisor <= 0 {
		return 0, ErrMoneyInvalid
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(value)), big.NewInt(multiplier))
	quotient, remainder := new(big.Int).QuoRem(numerator, big.NewInt(divisor), new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(divisor)) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrMoneyOverflow
	}
	return Money(quotient.Int64()), nil
}

// Pricing pins the reviewed fixed fee parameters for one calculation.
type Pricing struct {
	ServiceFeeCents    Money
	MonthlyBasisPoints int64
	DaysPerMonth       int64
	MinimumTermDays    int
	MaximumTermDays    int
}

// DefaultPricing returns the current approved demo terms parameters.
func DefaultPricing() Pricing {
	return Pricing{ServiceFeeCents: 5000, MonthlyBasisPoints: 200, DaysPerMonth: 30, MinimumTermDays: 7, MaximumTermDays: 90}
}

// Validate checks all explicit pricing parameters without supplying defaults.
func (p Pricing) Validate() error {
	if p.ServiceFeeCents < 0 || p.MonthlyBasisPoints < 0 || p.DaysPerMonth <= 0 || p.MinimumTermDays < 7 || p.MaximumTermDays > 90 || p.MinimumTermDays > p.MaximumTermDays {
		return ErrMoneyInvalid
	}
	return nil
}

// Fee calculates a one-time rounded percentage fee plus the fixed service fee.
func (p Pricing) Fee(principal Money, termDays int) (Money, error) {
	if principal <= 0 || p.ServiceFeeCents < 0 || p.MonthlyBasisPoints < 0 || p.DaysPerMonth <= 0 || p.MinimumTermDays < 7 || p.MaximumTermDays > 90 || p.MinimumTermDays > p.MaximumTermDays || termDays < p.MinimumTermDays || termDays > p.MaximumTermDays {
		return 0, ErrMoneyInvalid
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(principal)), big.NewInt(p.MonthlyBasisPoints))
	numerator.Mul(numerator, big.NewInt(int64(termDays)))
	divisor := new(big.Int).Mul(big.NewInt(10000), big.NewInt(p.DaysPerMonth))
	quotient, remainder := new(big.Int).QuoRem(numerator, divisor, new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrMoneyOverflow
	}
	return AddMoney(p.ServiceFeeCents, Money(quotient.Int64()))
}

// TotalDue adds principal and the selected-term fee with overflow checking.
func (p Pricing) TotalDue(principal Money, termDays int) (Money, Money, error) {
	fee, err := p.Fee(principal, termDays)
	if err != nil {
		return 0, 0, err
	}
	total, err := AddMoney(principal, fee)
	return fee, total, err
}
