package domain

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var decimalToken = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

// USDCents converts a numeric JSON decimal token to exact int64 cents.
// Strings, excess fractional precision, malformed tokens, and overflow are invalid.
func USDCents(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if len(text) > 128 {
		return 0, errors.New("usd_amount_invalid")
	}
	parts := decimalToken.FindStringSubmatch(text)
	if parts == nil {
		return 0, errors.New("usd_amount_invalid")
	}
	var exponent int64
	if parts[4] != "" {
		var err error
		exponent, err = strconv.ParseInt(parts[4], 10, 32)
		if err != nil {
			return 0, errors.New("usd_amount_invalid")
		}
	}
	scale := int64(len(parts[3])) - exponent
	if scale > 2 {
		return 0, errors.New("usd_cent_precision_exceeded")
	}
	digits := strings.TrimLeft(parts[2]+parts[3], "0")
	if digits == "" {
		return 0, nil
	}
	zeroes := 2 - scale
	if zeroes > 19 || int64(len(digits))+zeroes > 19 {
		return 0, errors.New("usd_amount_overflow")
	}
	cents, err := strconv.ParseInt(parts[1]+digits+strings.Repeat("0", int(zeroes)), 10, 64)
	if err != nil {
		return 0, errors.New("usd_amount_overflow")
	}
	return cents, nil
}

func purchaseCents(raw json.RawMessage, convention string) (int64, error) {
	cents, err := USDCents(raw)
	if err != nil {
		return 0, err
	}
	if convention == "negative_outgoing_currency_units" {
		if cents >= 0 || cents == math.MinInt64 {
			return 0, errors.New("purchase_amount_sign_invalid")
		}
		cents = -cents
	}
	if cents <= 0 {
		return 0, errors.New("purchase_amount_nonpositive")
	}
	return cents, nil
}
