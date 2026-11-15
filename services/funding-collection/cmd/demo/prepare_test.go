package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func TestPreparedRunHasOneSystemApprovalActor(t *testing.T) {
	cfg, err := policy.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	const runID = "11111111-1111-4111-8111-111111111111"
	anchor := time.Now().UTC().Truncate(time.Second)
	customerID := "synthetic-customer"
	ref := domain.SourceRef{System: "synthetic-demo", RecordID: "fixture-actor", Version: runID, EffectiveAt: anchor}
	// The joined path supplies an already validated external profile. This
	// fixture tests its materialization, without creating a saved assessment.
	profile := profileBuild{
		profile: upstreamProfileRef{Name: "abundant", CustomerID: customerID,
			AccountID: "synthetic-account", CardID: "synthetic-card", AvailableCents: 200_000,
			TransactionID: "synthetic-payment", AttemptedCents: 900_000, ShortfallCents: 700_000},
		fundingCustomer: domain.FacilityCustomerImport{CustomerID: customerID,
			AccountID: "synthetic-account", CardID: "synthetic-card", HistoryComplete: true},
		actor: domain.ActorImport{ActorID: "synthetic-customer-actor", ActorType: "CUSTOMER",
			AuthenticationSubject: "synthetic-customer", CustomerID: &customerID, Active: true, SourceRef: ref},
		operator: domain.ActorImport{ActorID: "synthetic-operator-actor", ActorType: "OPERATOR",
			AuthenticationSubject: "synthetic-operator", Active: true, SourceRef: ref},
	}
	stage := t.TempDir()
	_, err = materializeFundingRun(".dev/actor-regression", stage, runID, anchor,
		"flowcard_funding_demo_actor_test", "flowcard_underwriting_demo_actor_test", cfg, &profile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, "actors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var actors domain.ActorsImport
	if err := json.Unmarshal(data, &actors); err != nil {
		t.Fatal(err)
	}
	activeSystems := 0
	for _, actor := range actors.Actors {
		if actor.ActorType != "SYSTEM" || !actor.Active {
			continue
		}
		activeSystems++
		if actor.ActorID == "" || actor.AuthenticationSubject != "system:funding-collection" ||
			actor.CustomerID != nil || actor.SourceRef.System != "synthetic-demo" ||
			actor.SourceRef.Version != runID || !actor.SourceRef.EffectiveAt.Equal(anchor) {
			t.Fatalf("system actor = %+v, want run-bound synthetic service actor", actor)
		}
	}
	if activeSystems != 1 || len(actors.Actors) != 3 {
		t.Fatalf("prepared active SYSTEM actors = %d total = %d, want one service actor plus customer/operator", activeSystems, len(actors.Actors))
	}
}
