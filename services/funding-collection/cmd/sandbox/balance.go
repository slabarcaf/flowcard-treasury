package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

type safeBalance struct {
	AccountType string          `json:"account_type"`
	Currency    string          `json:"currency"`
	Available   json.RawMessage `json:"available_amount"`
	Total       json.RawMessage `json:"total_amount"`
}

func inspectBalances(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, out io.Writer) error {
	report := map[string][]safeBalance{}
	for _, role := range []string{"funding", "borrower"} {
		scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, role)
		if err != nil {
			return err
		}
		raw, _, state := scoped.get(ctx, "/api/v1/balances/current?account_type=cash")
		var rows []safeBalance
		if state != "VERIFIED" || json.Unmarshal(raw, &rows) != nil || rows == nil {
			return errors.New("sandbox_balance_schema_unverified")
		}
		for _, row := range rows {
			if row.Currency == "USD" {
				report[role] = append(report[role], row)
			}
		}
	}
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}
