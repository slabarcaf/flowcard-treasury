package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
)

func inspectAccounts(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID string, out io.Writer) error {
	report := map[string]any{}
	for _, role := range []string{"funding", "borrower"} {
		var id string
		if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
			id = m.Intents["account-"+role].ResourceID
			if id == "" {
				return errors.New("sandbox_account_pair_not_created")
			}
			return nil
		}); err != nil {
			return err
		}
		account, err := api.verifyAccount(ctx, id, runID, role)
		if err != nil {
			return err
		}
		raw, _, state := api.get(ctx, "/api/v1/accounts/"+url.PathEscape(id))
		if state != "VERIFIED" {
			return errors.New("sandbox_account_read_unavailable")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil {
			return errors.New("sandbox_account_response_invalid")
		}
		var requirements any
		if r, ok := body["requirements"]; ok {
			if json.Unmarshal(r, &requirements) != nil {
				return errors.New("sandbox_account_requirements_invalid")
			}
		}
		fields := []string{}
		var visit func(any)
		visit = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, value := range x {
					if strings.HasPrefix(k, "account_details.") || strings.HasPrefix(k, "primary_contact.") {
						fields = append(fields, k)
					}
					visit(value)
				}
			case []any:
				for _, item := range x {
					visit(item)
				}
			case string:
				if strings.HasPrefix(x, "account_details.") || strings.HasPrefix(x, "primary_contact.") {
					safe := true
					for _, c := range x {
						if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._[]", c)) {
							safe = false
						}
					}
					if safe {
						fields = append(fields, x)
					}
				}
			}
		}
		visit(requirements)
		sort.Strings(fields)
		report[role] = struct {
			Status            string   `json:"status"`
			RequirementFields []string `json:"requirement_fields"`
		}{account.Status, fields}
	}
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}
