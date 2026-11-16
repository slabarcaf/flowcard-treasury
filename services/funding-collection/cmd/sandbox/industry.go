package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type industryItem struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Value  string         `json:"value"`
	Values []industryItem `json:"values"`
}

func listSoftwareIndustries(ctx context.Context, api *sandboxAPI, out io.Writer) error {
	raw, _, state := api.get(ctx, "/api/v1/reference/industry_categories")
	if state != "VERIFIED" {
		return errors.New("sandbox_industry_reference_unavailable")
	}
	var tree []industryItem
	if json.Unmarshal(raw, &tree) != nil {
		return errors.New("sandbox_industry_reference_invalid")
	}
	matches := []struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}{}
	var visit func([]industryItem, string)
	visit = func(items []industryItem, parent string) {
		for _, item := range items {
			path := item.Name
			if parent != "" {
				path = parent + " / " + path
			}
			if strings.EqualFold(item.Type, "CODE") && strings.Contains(strings.ToLower(path), "software") {
				matches = append(matches, struct {
					Name string `json:"name"`
					Code string `json:"code"`
				}{path, item.Value})
			}
			visit(item.Values, path)
		}
	}
	visit(tree, "")
	if json.NewEncoder(out).Encode(matches) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}
