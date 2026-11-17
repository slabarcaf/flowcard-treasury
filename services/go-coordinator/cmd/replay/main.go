package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

func main() {
	caseID := flag.String("case", "", "coordinator case ID")
	reason := flag.String("reason", "", "operator reason for resuming the same workflow revision")
	refresh := flag.Bool("refresh-collection", false, "Queue a source reread for an idle collection monitoring case")
	flag.Parse()
	if *caseID == "" || *reason == "" {
		fatal(errors.New("case_and_reason_required"))
	}
	operator, err := user.Current()
	if err != nil || operator.Username == "" {
		fatal(errors.New("operator_identity_unavailable"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bindingID := os.Getenv("COORDINATOR_SOURCE_BINDING_ID")
	mode := os.Getenv("COORDINATOR_MODE")
	if bindingID == "" || (mode != "DEMO" && mode != "SANDBOX") {
		fatal(errors.New("coordinator_scope_configuration_invalid"))
	}
	repo, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fatal(err)
	}
	defer repo.Close()
	var receipt postgres.ReplayReceipt
	if *refresh {
		receipt, err = repo.RefreshCollectionCase(ctx, *caseID, bindingID, mode, operator.Username, *reason)
	} else {
		receipt, err = repo.ReplayCase(ctx, *caseID, bindingID, mode, operator.Username, *reason)
	}
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(receipt); err != nil {
		fatal(errors.New("replay_receipt_write_failed"))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
