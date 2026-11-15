// Package fixturebundle reads the exact shortfall fixture pack in an underwriting run.
package fixturebundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
)

const maxBundleFileBytes = 2_000_000

// Manifest contains the generated underwriting fields needed to bind a shortfall pack.
type Manifest struct {
	FixturePackVersion int        `json:"fixture_pack_version"`
	RunID              string     `json:"run_id"`
	RunAnchor          time.Time  `json:"run_anchor"`
	SourceMode         string     `json:"source_mode"`
	ShortfallsFile     string     `json:"shortfalls_file"`
	Scenarios          []Scenario `json:"scenarios"`
	Provenance         string     `json:"provenance"`
}

// Scenario retains the generated IDs used to validate one source record.
type Scenario struct {
	ID               string         `json:"scenario_id"`
	CustomerID       string         `json:"customer_id"`
	AccountID        string         `json:"account_id"`
	CardID           string         `json:"card_id"`
	ShortfallID      string         `json:"shortfall_id"`
	TransactionID    string         `json:"transaction_id"`
	CanonicalEventID string         `json:"canonical_event_id"`
	LifecycleID      string         `json:"lifecycle_id"`
	CustomerFile     string         `json:"customer_file"`
	ExpectedStatus   string         `json:"expected_status"`
	PrincipalCents   int64          `json:"principal_cents"`
	TermDays         int            `json:"term_days"`
	Provenance       string         `json:"provenance"`
	ExpectedRules    []ExpectedRule `json:"expected_rules"`
}

// ExpectedRule is the generated fixture's reviewed rule/status label.
type ExpectedRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
}

// Bundle contains the manifest and validated records, ordered for source listing.
type Bundle struct {
	Manifest   Manifest
	Provenance string
	Records    []shortfall.Record
	ByID       map[string]shortfall.Record
}

type fixturePack struct {
	SourceMode string          `json:"source_mode"`
	Provenance string          `json:"provenance"`
	Records    []fixtureRecord `json:"records"`
}

type fixtureRecord struct {
	ScenarioID string           `json:"scenario_id"`
	Response   shortfall.Record `json:"response"`
}

// Load reads a retained run manifest and its exact relative shortfalls_file.
func Load(manifestPath string) (Bundle, error) {
	manifestData, bundleRoot, manifestRel, err := loadManifestFile(manifestPath)
	if err != nil {
		return Bundle{}, err
	}
	if !ValidJSON(manifestData) {
		return Bundle{}, errors.New("fixture_manifest_invalid")
	}
	var manifest Manifest
	if json.Unmarshal(manifestData, &manifest) != nil || manifest.FixturePackVersion != 1 ||
		!validUUID(manifest.RunID) || manifest.RunAnchor.IsZero() || manifest.SourceMode != "DEMO" ||
		manifest.ShortfallsFile == "" || len(manifest.Scenarios) == 0 || len(manifest.Scenarios) > 128 ||
		strings.TrimSpace(manifest.Provenance) == "" || !isUTC(manifest.RunAnchor) ||
		(manifestRel != "manifest.json" && manifestRel != filepath.Join("runs", manifest.RunID, "manifest.json")) {
		return Bundle{}, errors.New("fixture_manifest_invalid")
	}
	retainedPath := filepath.Join("runs", manifest.RunID, "manifest.json")
	retainedData, _, err := readBundleArtifact(bundleRoot, retainedPath)
	if err != nil || !bytes.Equal(retainedData, manifestData) {
		return Bundle{}, errors.New("fixture_manifest_retained_copy_mismatch")
	}
	shortfallsData, _, err := readBundleArtifact(bundleRoot, manifest.ShortfallsFile)
	if err != nil || !ValidJSON(shortfallsData) {
		return Bundle{}, errors.New("fixture_shortfalls_unavailable")
	}
	var pack fixturePack
	if decodeStrictJSON(shortfallsData, &pack) != nil || pack.SourceMode != manifest.SourceMode ||
		strings.TrimSpace(pack.Provenance) == "" || len(pack.Records) != len(manifest.Scenarios) {
		return Bundle{}, errors.New("fixture_shortfalls_invalid")
	}
	scenarios := make(map[string]Scenario, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		if !validToken(scenario.ID, 128) || !validUUID(scenario.ShortfallID) ||
			!validUUID(scenario.TransactionID) || !validUUID(scenario.CanonicalEventID) ||
			!validUUID(scenario.LifecycleID) || scenario.CustomerID == "" || scenario.AccountID == "" ||
			scenario.CardID == "" || scenario.PrincipalCents <= 0 || scenario.TermDays <= 0 ||
			scenario.Provenance == "" || scenario.ExpectedStatus == "" || !validExpectedRules(scenario.ExpectedRules) {
			return Bundle{}, errors.New("fixture_scenario_invalid")
		}
		if _, exists := scenarios[scenario.ID]; exists {
			return Bundle{}, errors.New("fixture_scenario_duplicate")
		}
		scenarios[scenario.ID] = scenario
	}
	records := make([]shortfall.Record, 0, len(pack.Records))
	byID := make(map[string]shortfall.Record, len(pack.Records))
	seenScenarios := make(map[string]bool, len(pack.Records))
	for _, item := range pack.Records {
		scenario, exists := scenarios[item.ScenarioID]
		if !exists || seenScenarios[item.ScenarioID] || !validRecord(item.Response, scenario, manifest.RunAnchor) {
			return Bundle{}, errors.New("fixture_shortfall_linkage_invalid")
		}
		seenScenarios[item.ScenarioID] = true
		if _, exists := byID[item.Response.ID]; exists {
			return Bundle{}, errors.New("fixture_shortfall_duplicate")
		}
		records = append(records, item.Response)
		byID[item.Response.ID] = item.Response
	}
	if len(seenScenarios) != len(scenarios) {
		return Bundle{}, errors.New("fixture_shortfall_scenario_missing")
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return Bundle{Manifest: manifest, Provenance: pack.Provenance, Records: records, ByID: byID}, nil
}

func validExpectedRules(rules []ExpectedRule) bool {
	if rules == nil || len(rules) == 0 || len(rules) > 32 {
		return false
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if !validToken(rule.RuleID, 128) || seen[rule.RuleID] ||
			(rule.Status != "PASS" && rule.Status != "FAIL" && rule.Status != "UNKNOWN") {
			return false
		}
		seen[rule.RuleID] = true
	}
	return true
}

func validRecord(record shortfall.Record, scenario Scenario, anchor time.Time) bool {
	if record.ID != scenario.ShortfallID || record.CustomerID == nil || *record.CustomerID != scenario.CustomerID ||
		record.AccountID != scenario.AccountID || record.CardID == nil || *record.CardID != scenario.CardID ||
		record.TransactionID == nil || *record.TransactionID != scenario.TransactionID ||
		record.CanonicalEventID != scenario.CanonicalEventID || record.LifecycleID == nil ||
		*record.LifecycleID != scenario.LifecycleID || record.Outcome != "SHORTFALL_DETECTED" ||
		record.Currency == nil || *record.Currency != "USD" || record.AttemptedAmountCents == nil ||
		*record.AttemptedAmountCents != 900_000 || record.AvailableBalanceCents == nil ||
		*record.AvailableBalanceCents != 200_000 || record.ShortfallCents == nil ||
		*record.ShortfallCents != scenario.PrincipalCents ||
		*record.ShortfallCents != *record.AttemptedAmountCents-*record.AvailableBalanceCents ||
		!record.RequiresRecheck || record.AmountSemantics != "current_need_at_balance_observation" {
		return false
	}
	return record.CreatedAt.Equal(anchor.Add(-30*time.Second)) &&
		equalTime(record.ProviderCreatedAt, anchor.Add(-60*time.Second)) &&
		equalTime(record.ProviderTransactedAt, anchor.Add(-60*time.Second)) &&
		equalTime(record.ProviderUpdatedAt, anchor.Add(-60*time.Second)) &&
		equalTime(record.BalanceRequestedAt, anchor.Add(-10*time.Second)) &&
		equalTime(record.BalanceObservedAt, anchor.Add(-5*time.Second))
}

func equalTime(value *time.Time, expected time.Time) bool {
	return value != nil && value.Equal(expected)
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func loadManifestFile(path string) ([]byte, string, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", "", errors.New("fixture_manifest_path_invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", "", errors.New("fixture_manifest_path_invalid")
	}
	data, err := readRegularFile(absolute, maxBundleFileBytes)
	if err != nil {
		return nil, "", "", errors.New("fixture_manifest_unavailable")
	}
	manifestReal, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", "", errors.New("fixture_manifest_unavailable")
	}
	manifestDir := filepath.Dir(manifestReal)
	bundleRoot := manifestDir
	if filepath.Base(filepath.Dir(manifestDir)) == "runs" {
		bundleRoot = filepath.Dir(filepath.Dir(manifestDir))
	}
	relative, err := filepath.Rel(bundleRoot, manifestReal)
	if err != nil {
		return nil, "", "", errors.New("fixture_manifest_path_invalid")
	}
	return data, bundleRoot, relative, nil
}

func readBundleArtifact(root, relative string) ([]byte, string, error) {
	local := filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(local) || !filepath.IsLocal(local) {
		return nil, "", errors.New("fixture_artifact_path_invalid")
	}
	path := filepath.Join(root, local)
	if ensureNoSymlinkPath(root, path) != nil {
		return nil, "", errors.New("fixture_artifact_path_invalid")
	}
	data, err := readRegularFile(path, maxBundleFileBytes)
	if err != nil {
		return nil, "", errors.New("fixture_artifact_unavailable")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", errors.New("fixture_artifact_unavailable")
	}
	resolved, err := filepath.Rel(root, real)
	if err != nil || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return nil, "", errors.New("fixture_artifact_outside_bundle")
	}
	return data, real, nil
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("fixture_artifact_outside_bundle")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("fixture_artifact_path_invalid")
		}
	}
	return nil
}

func readRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("fixture_file_invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("fixture_file_unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > maximum {
		return nil, errors.New("fixture_file_invalid")
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("fixture_json_invalid")
	}
	return nil
}

// ValidJSON rejects malformed JSON, duplicate object keys, and trailing values.
func ValidJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !scanJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok || keys[key] {
				return false
			}
			keys[key] = true
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func validToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}
