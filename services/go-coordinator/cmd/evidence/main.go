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

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/config"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

type reviewExport struct {
	SchemaVersion int                       `json:"schema_version"`
	CaseID        string                    `json:"case_id"`
	Mode          string                    `json:"mode"`
	CustomerID    string                    `json:"customer_id"`
	AccountRef    string                    `json:"account_ref"`
	BindingID     string                    `json:"source_binding_id"`
	ExportedAt    time.Time                 `json:"exported_at"`
	ExportedBy    string                    `json:"exported_by"`
	Evidence      []postgres.EvidenceRecord `json:"evidence"`
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: evidence export|link [flags]"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch os.Args[1] {
	case "export":
		if err := export(ctx, os.Args[2:]); err != nil {
			fatal(err)
		}
	case "link":
		if err := link(ctx, os.Args[2:]); err != nil {
			fatal(err)
		}
	default:
		fatal(errors.New("usage: evidence export|link [flags]"))
	}
}

func export(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	caseID := flags.String("case", "", "coordinator case ID")
	output := flags.String("output", "", "new output file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *caseID == "" || *output == "" {
		return errors.New("case_and_output_required")
	}
	user, err := user.Current()
	if err != nil || user.Username == "" {
		return errors.New("operator_identity_unavailable")
	}
	cfg := configFromEnvironment()
	repo, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	scope, err := repo.ReadCaseEvidenceScope(ctx, *caseID)
	if err != nil {
		return err
	}
	if scope.Mode != cfg.Mode || scope.SourceBindingID != cfg.SourceBindingID {
		return errors.New("case_evidence_scope_invalid")
	}
	evidence, err := repo.ListEvidence(ctx, *caseID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(reviewExport{SchemaVersion: 1, CaseID: *caseID, Mode: scope.Mode, CustomerID: scope.CustomerID, AccountRef: scope.AccountRef, BindingID: scope.SourceBindingID, ExportedAt: time.Now().UTC(), ExportedBy: user.Username, Evidence: evidence}, "", "  ")
	if err != nil {
		return errors.New("evidence_export_encode_failed")
	}
	data = append(data, '\n')
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("evidence_export_create_failed")
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return errors.New("evidence_export_write_failed")
	}
	return nil
}

// link records source-reference attribution only; it does not semantically validate the claim.
func link(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("link", flag.ContinueOnError)
	evidenceID := flags.String("evidence", "", "pending evidence ID")
	expectedSnapshotID := flags.String("snapshot", "", "expected current accepted snapshot ID")
	expectedSnapshotVersion := flags.Int64("snapshot-version", 0, "expected current accepted snapshot version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *evidenceID == "" || *expectedSnapshotID == "" || *expectedSnapshotVersion <= 0 {
		return errors.New("evidence_and_expected_current_snapshot_required")
	}
	operator, err := user.Current()
	if err != nil || operator.Username == "" {
		return errors.New("operator_identity_unavailable")
	}
	cfg := configFromEnvironment()
	if cfg.UnderwritingURL == "" || cfg.UnderwritingSecret == "" {
		return errors.New("underwriting_context_unavailable")
	}
	repo, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	evidence, scope, err := repo.ReadEvidence(ctx, *evidenceID)
	if err != nil {
		return err
	}
	if evidence.State != "PENDING_REVIEW" || scope.Mode != cfg.Mode || scope.SourceBindingID != cfg.SourceBindingID {
		return errors.New("evidence_link_scope_invalid")
	}
	client, err := customercontext.New(cfg.UnderwritingURL, cfg.UnderwritingSecret, cfg.Mode, nil)
	if err != nil {
		return err
	}
	snapshot, _, err := client.ReadScoped(ctx, scope.CustomerID, scope.RawAccountID, scope.RawCardID)
	if err != nil {
		return errors.New("current_accepted_snapshot_unavailable")
	}
	if snapshot.SnapshotID != *expectedSnapshotID || snapshot.SnapshotVersion != *expectedSnapshotVersion {
		return errors.New("current_accepted_snapshot_changed")
	}
	matched := false
	for _, ref := range snapshot.SourceRefs {
		if evidence.SourceType != nil && evidence.SourceRecordID != nil && ref.System == *evidence.SourceType && ref.RecordID == *evidence.SourceRecordID && ref.Version == evidence.SourceVersion {
			matched = true
			break
		}
	}
	if !matched {
		return errors.New("evidence_source_not_in_current_snapshot")
	}
	refs, err := json.Marshal(snapshot.SourceRefs)
	if err != nil {
		return errors.New("evidence_link_refs_encode_failed")
	}
	if err := repo.AuditEvidenceLinkScoped(ctx, evidence.ID, snapshot.SnapshotID, fmt.Sprint(snapshot.SnapshotVersion), operator.Username, scope.Mode, scope.CustomerID, scope.AccountRef, scope.SourceBindingID, refs); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Linked by matching source references only; this does not semantically validate the claim. Human verification and snapshot import remain separate.")
	return nil
}

func configFromEnvironment() config.Config {
	return config.Config{DatabaseURL: os.Getenv("DATABASE_URL"), Mode: os.Getenv("COORDINATOR_MODE"), SourceBindingID: os.Getenv("COORDINATOR_SOURCE_BINDING_ID"), UnderwritingURL: os.Getenv("UNDERWRITING_BASE_URL"), UnderwritingSecret: os.Getenv("UNDERWRITING_SERVICE_SECRET")}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
