// Package sources reads only explicitly mapped local source sections.
package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxHandles             = 8
	MaxSectionBytes        = 8 << 10
	MaxTotalBytes          = 32 << 10
	maxManifestBytes       = 64 << 10
	maxOriginalSectionSize = 2 << 20
	maxSectionsPerManifest = MaxHandles * 16
)

// Scope binds a source manifest to one immutable coordinator source binding.
type Scope struct{ Mode, CustomerID, AccountRef, BindingID string }
type Manifest struct {
	Version    int      `json:"version"`
	Mode       string   `json:"mode"`
	CustomerID string   `json:"customer_id"`
	AccountRef string   `json:"account_ref"`
	BindingID  string   `json:"source_binding_id"`
	Sources    []Source `json:"sources"`
}
type Source struct {
	Handle        string    `json:"handle"`
	Type          string    `json:"type"`
	RecordID      string    `json:"record_id"`
	Version       string    `json:"version"`
	ExportVersion string    `json:"export_version"`
	Redacted      bool      `json:"redacted"`
	Sections      []Section `json:"sections"`
}
type Section struct {
	ID      string `json:"id"`
	Locator string `json:"locator"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}
type RetainedSection struct {
	Handle         string
	SectionID      string
	SourceType     string
	RecordID       string
	SourceVersion  string
	ExportVersion  string
	Locator        string
	OriginalSHA256 string
	VisibleSHA256  string
	Text           string
	Truncated      bool
}
type Reader struct {
	root       string
	scope      Scope
	sections   map[string]map[string]RetainedSection
	totalBytes int
}

// Load validates source ownership, hashes, path containment and byte limits.
func Load(root string, manifestBytes []byte, expected Scope) (*Reader, error) {
	if len(manifestBytes) > maxManifestBytes {
		return nil, errors.New("source_manifest_too_large")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.New("source_root_invalid")
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, errors.New("source_root_unavailable")
	}
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestBytes))
	dec.DisallowUnknownFields()
	if dec.Decode(&manifest) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("source_manifest_invalid")
	}
	if manifest.Version != 1 || manifest.Mode != expected.Mode || manifest.CustomerID != expected.CustomerID || manifest.AccountRef != expected.AccountRef || manifest.BindingID != expected.BindingID || expected.BindingID == "" || len(manifest.Sources) > MaxHandles {
		return nil, errors.New("source_manifest_scope_invalid")
	}
	reader := &Reader{root: rootReal, scope: expected, sections: make(map[string]map[string]RetainedSection)}
	totalSections := 0
	for _, source := range manifest.Sources {
		if !validToken(source.Handle, 128) || !validToken(source.Type, 64) || !validToken(source.RecordID, 256) || !validToken(source.Version, 128) || !validToken(source.ExportVersion, 128) || !source.Redacted {
			return nil, errors.New("source_manifest_entry_invalid")
		}
		if _, exists := reader.sections[source.Handle]; exists {
			return nil, errors.New("source_handle_duplicate")
		}
		reader.sections[source.Handle] = make(map[string]RetainedSection)
		sourceBytes := 0
		for _, section := range source.Sections {
			totalSections++
			if totalSections > maxSectionsPerManifest || !validToken(section.ID, 128) || !validText(section.Locator, 512) || !validToken(section.SHA256, 64) || len(section.SHA256) != 64 || !validPath(section.Path) || filepath.IsAbs(section.Path) {
				return nil, errors.New("source_section_invalid")
			}
			clean := filepath.Clean(section.Path)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return nil, errors.New("source_path_invalid")
			}
			candidate := filepath.Join(rootReal, clean)
			real, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return nil, errors.New("source_section_unavailable")
			}
			rel, err := filepath.Rel(rootReal, real)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, errors.New("source_path_outside_root")
			}
			file, err := os.Open(real)
			if err != nil {
				return nil, errors.New("source_section_unavailable")
			}
			raw, err := io.ReadAll(io.LimitReader(file, maxOriginalSectionSize+1))
			closeErr := file.Close()
			if err != nil || closeErr != nil || len(raw) > maxOriginalSectionSize {
				return nil, errors.New("source_section_invalid")
			}
			sum := sha256.Sum256(raw)
			hash := hex.EncodeToString(sum[:])
			if !strings.EqualFold(hash, section.SHA256) {
				return nil, errors.New("source_hash_mismatch")
			}
			text, err := sanitize(raw)
			if err != nil {
				return nil, err
			}
			remaining := MaxSectionBytes - sourceBytes
			if remaining < 0 {
				remaining = 0
			}
			truncated := len(text) > remaining
			if truncated {
				text = text[:remaining]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
			}
			visibleSum := sha256.Sum256([]byte(text))
			sourceBytes += len(text)
			if reader.totalBytes+len(text) > MaxTotalBytes {
				return nil, errors.New("source_total_text_limit")
			}
			reader.totalBytes += len(text)
			if _, exists := reader.sections[source.Handle][section.ID]; exists {
				return nil, errors.New("source_section_duplicate")
			}
			reader.sections[source.Handle][section.ID] = RetainedSection{Handle: source.Handle, SectionID: section.ID, SourceType: source.Type, RecordID: source.RecordID, SourceVersion: source.Version, ExportVersion: source.ExportVersion, Locator: section.Locator, OriginalSHA256: hash, VisibleSHA256: hex.EncodeToString(visibleSum[:]), Text: text, Truncated: truncated}
		}
	}
	return reader, nil
}

// LoadForCase reads only the server-owned manifest named for one coordinator case.
func LoadForCase(root, caseID string, expected Scope) (*Reader, error) {
	if !validUUID(caseID) {
		return nil, errors.New("source_case_id_invalid")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.New("source_root_invalid")
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, errors.New("source_root_unavailable")
	}
	manifestCandidate := filepath.Join(rootReal, caseID+".json")
	manifestReal, err := filepath.EvalSymlinks(manifestCandidate)
	if err != nil {
		return nil, errors.New("source_manifest_unavailable")
	}
	relative, err := filepath.Rel(rootReal, manifestReal)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("source_manifest_outside_root")
	}
	file, err := os.Open(manifestReal)
	if err != nil {
		return nil, errors.New("source_manifest_unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > maxManifestBytes {
		return nil, errors.New("source_manifest_invalid")
	}
	return Load(rootReal, contents, expected)
}

// ValidateManifestRoot checks a bounded set of prepared case manifests for the configured binding and mode.
func ValidateManifestRoot(root, mode, bindingID string) (int, error) {
	if strings.TrimSpace(root) == "" || (mode != "DEMO" && mode != "SANDBOX") || bindingID == "" {
		return 0, errors.New("source_manifest_root_configuration_invalid")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return 0, errors.New("source_manifest_root_invalid")
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return 0, errors.New("source_manifest_root_unavailable")
	}
	info, err := os.Stat(rootReal)
	if err != nil || !info.IsDir() {
		return 0, errors.New("source_manifest_root_unavailable")
	}
	entries, err := os.ReadDir(rootReal)
	if err != nil || len(entries) > 128 {
		return 0, errors.New("source_manifest_root_unavailable")
	}
	validated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		caseID := strings.TrimSuffix(entry.Name(), ".json")
		if !validUUID(caseID) {
			continue
		}
		candidate := filepath.Join(rootReal, entry.Name())
		manifestReal, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return 0, errors.New("source_manifest_unavailable")
		}
		relative, err := filepath.Rel(rootReal, manifestReal)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return 0, errors.New("source_manifest_outside_root")
		}
		file, err := os.Open(manifestReal)
		if err != nil {
			return 0, errors.New("source_manifest_unavailable")
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(contents) > maxManifestBytes {
			return 0, errors.New("source_manifest_invalid")
		}
		var manifest Manifest
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Mode != mode || manifest.BindingID != bindingID || manifest.CustomerID == "" || manifest.AccountRef == "" {
			return 0, errors.New("source_manifest_scope_invalid")
		}
		if _, err := Load(rootReal, contents, Scope{Mode: mode, CustomerID: manifest.CustomerID, AccountRef: manifest.AccountRef, BindingID: bindingID}); err != nil {
			return 0, err
		}
		validated++
	}
	if validated == 0 {
		return 0, errors.New("source_manifest_root_empty")
	}
	return validated, nil
}

// Scope returns the customer and source binding validated by Load.
func (r *Reader) Scope() Scope {
	return r.scope
}

func (r *Reader) Read(handle, section string) (RetainedSection, error) {
	sections, ok := r.sections[handle]
	if !ok {
		return RetainedSection{}, errors.New("source_handle_unknown")
	}
	value, ok := sections[section]
	if !ok {
		return RetainedSection{}, errors.New("source_section_unknown")
	}
	return value, nil
}

func (r *Reader) SectionRefs() []RetainedSection {
	var out []RetainedSection
	for _, sections := range r.sections {
		for _, section := range sections {
			section.Text = ""
			out = append(out, section)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Handle == out[j].Handle {
			return out[i].SectionID < out[j].SectionID
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

func (r *Reader) Handles() []string {
	out := make([]string, 0, len(r.sections))
	for handle := range r.sections {
		out = append(out, handle)
	}
	sort.Strings(out)
	return out
}

func sanitize(raw []byte) (string, error) {
	if !utf8.Valid(raw) {
		return "", errors.New("source_text_invalid_utf8")
	}
	var b strings.Builder
	inTag := false
	for _, r := range string(raw) {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' && inTag {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	if inTag {
		return "", errors.New("source_text_markup_invalid")
	}
	return b.String(), nil
}

func validToken(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func validText(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	if value == "" || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
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
