package config

import "testing"

func TestPrivateListenerRequiresExplicitSandboxOption(t *testing.T) {
	for _, tc := range []struct {
		address, mode, allow string
		ok                   bool
	}{
		{"127.0.0.1:8085", ModeSandbox, "", true},
		{"0.0.0.0:8085", ModeSandbox, "", false},
		{"0.0.0.0:8085", ModeSandbox, "false", false},
		{"0.0.0.0:8085", ModeSandbox, "true", true},
		{"0.0.0.0:8085", ModeDemo, "true", false},
		{"public.example:8085", ModeSandbox, "true", false},
		{"0.0.0.0:8085", ModeSandbox, "yes", false},
	} {
		if got := validateRuntimeAddress(tc.address, tc.mode, tc.allow) == nil; got != tc.ok {
			t.Fatalf("listener %s/%s/%s accepted=%v", tc.address, tc.mode, tc.allow, got)
		}
	}
}
