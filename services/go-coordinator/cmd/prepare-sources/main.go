// Command prepare-sources creates scoped source manifests for one retained DEMO run.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/fixturebundle"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
)

const profileSourceBinding = "flowcard-integration-20261114"

type preparedCase struct {
	CaseID        string `json:"case_id"`
	ShortfallID   string `json:"shortfall_id"`
	InvoiceSource bool   `json:"invoice_source"`
}

type preparedSummary struct {
	SourceMode string         `json:"source_mode"`
	RunID      string         `json:"run_id"`
	CaseFiles  []preparedCase `json:"case_files"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("prepare-sources", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "retained underwriting run manifest")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*manifestPath) == "" {
		return errors.New("usage: prepare-sources --manifest PATH")
	}
	if strings.TrimSpace(os.Getenv("COORDINATOR_MODE")) != "DEMO" || strings.TrimSpace(os.Getenv("COORDINATOR_SOURCE_BINDING_ID")) != profileSourceBinding {
		return errors.New("source preparation requires the approved DEMO binding")
	}
	sourceRootValue := strings.TrimSpace(os.Getenv("COORDINATOR_SOURCE_ROOT"))
	if sourceRootValue == "" {
		return errors.New("coordinator source root required")
	}
	bundle, err := fixturebundle.Load(*manifestPath)
	if err != nil {
		return err
	}
	repositoryRoot, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	sourceRoot := sourceRootValue
	if !filepath.IsAbs(sourceRoot) {
		sourceRoot = filepath.Join(repositoryRoot, sourceRoot)
	}
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil || !within(filepath.Join(repositoryRoot, ".dev"), sourceRoot) {
		return errors.New("coordinator source root must be under repository .dev")
	}
	if err := ensureNoSymlinkPath(repositoryRoot, sourceRoot); err != nil {
		return errors.New("coordinator source root path invalid")
	}
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		return errors.New("coordinator source root unavailable")
	}
	if err := ensureNoSymlinkPath(repositoryRoot, sourceRoot); err != nil {
		return errors.New("coordinator source root path invalid")
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil || len(entries) != 0 {
		return errors.New("coordinator source root must be new or empty")
	}
	summary := preparedSummary{SourceMode: "DEMO", RunID: bundle.Manifest.RunID, CaseFiles: make([]preparedCase, 0, len(bundle.Manifest.Scenarios))}
	for _, scenario := range bundle.Manifest.Scenarios {
		caseID, err := domain.StableCaseID(profileSourceBinding, domain.CaseShortfall, scenario.ShortfallID)
		if err != nil {
			return err
		}
		manifest, sourceFiles, hasInvoice, err := sourceManifest(bundle.Manifest, scenario)
		if err != nil {
			return err
		}
		for name, body := range sourceFiles {
			if err := writePrivateExclusive(filepath.Join(sourceRoot, name), body); err != nil {
				return err
			}
		}
		manifestBody, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return errors.New("source manifest encode failed")
		}
		manifestBody = append(manifestBody, '\n')
		if err := writePrivateExclusive(filepath.Join(sourceRoot, caseID+".json"), manifestBody); err != nil {
			return err
		}
		summary.CaseFiles = append(summary.CaseFiles, preparedCase{CaseID: caseID, ShortfallID: scenario.ShortfallID, InvoiceSource: hasInvoice})
	}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return errors.New("source preparation summary write failed")
	}
	return nil
}

func sourceManifest(bundle fixturebundle.Manifest, scenario fixturebundle.Scenario) (sources.Manifest, map[string][]byte, bool, error) {
	manifest := sources.Manifest{
		Version: 1, Mode: "DEMO", CustomerID: scenario.CustomerID,
		AccountRef: domain.InternalAccountRef(scenario.CustomerID), BindingID: profileSourceBinding,
		Sources: []sources.Source{},
	}
	files := make(map[string][]byte)
	if !needsReceivableEvidence(scenario.ExpectedRules) {
		return manifest, files, false, nil
	}
	date := bundle.RunAnchor.AddDate(0, 0, 30).Format("2006-01-02")
	recordID := "coordinator-fixture-invoice-" + scenario.ID
	fileName := "invoice-" + scenario.ShortfallID + ".txt"
	text := "Coordinator-owned synthetic invoice source.\n" +
		"Invoice reference: " + recordID + "\n" +
		"Due date: " + date + "\n" +
		"This DEMO source does not amend or re-evaluate the saved underwriting result.\n"
	body := []byte(text)
	hash := sha256.Sum256(body)
	manifest.Sources = []sources.Source{{
		Handle: "invoice", Type: "INVOICE", RecordID: recordID,
		Version: bundle.RunID, ExportVersion: bundle.RunID, Redacted: true,
		Sections: []sources.Section{{
			ID: "invoice-summary", Locator: "synthetic invoice header", Path: fileName,
			SHA256: hex.EncodeToString(hash[:]),
		}},
	}}
	files[fileName] = body
	return manifest, files, true, nil
}

func needsReceivableEvidence(rules []fixturebundle.ExpectedRule) bool {
	for _, rule := range rules {
		if rule.RuleID == "RECEIVABLE_COVERAGE" && rule.Status == "UNKNOWN" {
			return true
		}
	}
	return false
}

func writePrivateExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("coordinator source artifact already exists or cannot be created")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return errors.New("coordinator source artifact write failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("coordinator source artifact close failed")
	}
	return nil
}

func findRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", errors.New("repository root unavailable")
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "docs", "designs", "go-coordinator.md")); err == nil {
			return filepath.EvalSymlinks(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root unavailable")
		}
		current = parent
	}
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("source root outside repository")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source root contains invalid path")
		}
	}
	return nil
}
