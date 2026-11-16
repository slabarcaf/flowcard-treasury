package main

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

func TestUnconfiguredReadAPIHasNoProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		err  error
		read bool
	}{
		{"empty disabled sandbox", config.Config{Mode: "SANDBOX"}, pgx.ErrNoRows, true},
		{"storage failure", config.Config{Mode: "SANDBOX"}, errors.New("unavailable"), false},
		{"dispatch enabled", config.Config{Mode: "SANDBOX", LiveDispatchEnabled: true}, pgx.ErrNoRows, false},
		{"funding enabled", config.Config{Mode: "SANDBOX", SandboxFundingEnabled: true}, pgx.ErrNoRows, false},
		{"collection enabled", config.Config{Mode: "SANDBOX", SandboxCollectionEnabled: true}, pgx.ErrNoRows, false},
		{"fixture mode", config.Config{Mode: "DEMO"}, pgx.ErrNoRows, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := apiProvider(tc.cfg, "", tc.err)
			if tc.read {
				if _, ok := p.(provider.Unsupported); err != nil || !ok {
					t.Fatal("empty read installation gained provider authority")
				}
			} else if err == nil {
				t.Fatal("missing facility or failed storage was accepted for execution")
			}
		})
	}
}
