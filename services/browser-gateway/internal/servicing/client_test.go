package servicing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestActorReadRequiresExplicitMatchingDelegationProfile(t *testing.T) {
	for _, tc := range []struct {
		name, mode              string
		fixture, sandbox, valid bool
	}{
		{"sandbox verified", "SANDBOX", false, true, true},
		{"sandbox label alone", "SANDBOX", false, false, false},
		{"mixed profiles", "SANDBOX", true, true, false},
		{"demo verified", "DEMO", true, false, true},
		{"production refused", "PRODUCTION", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/servicing/actor" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					t.Fatal("actor lookup contract changed")
				}
				_ = json.NewEncoder(w).Encode(Actor{Restriction: Restriction{ActorID: "fixture-actor", ActorType: "CUSTOMER", CustomerID: "fixture-customer", Mode: tc.mode, FacilityID: "fixture-facility", SourceBindingID: "fixture-installation"}, FixtureOnly: tc.fixture, SandboxDelegation: tc.sandbox})
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ReadActor(context.Background(), server.Client(), base, strings.Repeat("k", 32), "fixture-subject", nil)
			if (err == nil) != tc.valid {
				t.Fatalf("delegation result %v, want valid %v", err, tc.valid)
			}
		})
	}
}
