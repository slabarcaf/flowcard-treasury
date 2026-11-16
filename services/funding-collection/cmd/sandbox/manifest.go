package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const manifestOwner = "flowcard-airwallex-sandbox-v1"

var runPattern = regexp.MustCompile(`^fc-sbx-[a-z0-9]{8,32}$`)

type sandboxManifest struct {
	Owner                       string                 `json:"owner"`
	Environment                 string                 `json:"environment"`
	RunID                       string                 `json:"run_id"`
	PlatformID                  string                 `json:"platform_id"`
	SandboxAgreementsAcceptedAt string                 `json:"sandbox_agreements_accepted_at,omitempty"`
	Intents                     map[string]setupIntent `json:"intents"`
}

type setupIntent struct {
	RequestID              string `json:"request_id"`
	PayloadHash            string `json:"payload_hash"`
	State                  string `json:"state"`
	ResourceID             string `json:"resource_id,omitempty"`
	ParentTransactionID    string `json:"parent_transaction_id,omitempty"`
	SourceAccountID        string `json:"source_account_id,omitempty"`
	DepositReadKind        string `json:"deposit_read_kind,omitempty"`
	DestinationAccountID   string `json:"destination_account_id,omitempty"`
	SourceBeforeCents      *int64 `json:"source_before_cents,omitempty"`
	DestinationBeforeCents *int64 `json:"destination_before_cents,omitempty"`
}

// withManifest serializes setup processes and atomically persists private state.
// A process interrupted after dispatch retains its pre-saved UNKNOWN intent.
func withManifest(path, runID, platform string, create bool, change func(*sandboxManifest) error) error {
	if !runPattern.MatchString(runID) || platform == "" || !filepath.IsAbs(path) {
		return errors.New("sandbox_manifest_arguments_invalid")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("sandbox_manifest_directory_unavailable")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("sandbox_manifest_directory_must_be_private")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("sandbox_manifest_lock_unavailable")
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errors.New("sandbox_manifest_in_use")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	manifest := sandboxManifest{Owner: manifestOwner, Environment: "SANDBOX", RunID: runID, PlatformID: platform, Intents: map[string]setupIntent{}}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		defer f.Close()
		stat, statErr := f.Stat()
		if statErr != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
			return errors.New("sandbox_manifest_must_be_private")
		}
		decoder := json.NewDecoder(io.LimitReader(f, 1_000_001))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return errors.New("sandbox_manifest_invalid")
		}
		if create {
			return errors.New("sandbox_manifest_already_exists")
		}
	} else if !os.IsNotExist(err) || !create {
		return errors.New("sandbox_manifest_unavailable")
	}
	if manifest.Owner != manifestOwner || manifest.Environment != "SANDBOX" || manifest.RunID != runID || manifest.PlatformID != platform || manifest.Intents == nil {
		return errors.New("sandbox_manifest_ownership_mismatch")
	}
	if err := change(&manifest); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("sandbox_manifest_invalid")
	}
	tmp, err := os.CreateTemp(dir, ".sandbox-manifest-*")
	if err != nil {
		return errors.New("sandbox_manifest_write_failed")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return errors.New("sandbox_manifest_write_failed")
	}
	if tmp.Sync() != nil {
		tmp.Close()
		return errors.New("sandbox_manifest_write_failed")
	}
	if tmp.Close() != nil || os.Rename(name, path) != nil {
		return errors.New("sandbox_manifest_write_failed")
	}
	parent, err := os.Open(dir)
	if err != nil {
		return errors.New("sandbox_manifest_sync_failed")
	}
	defer parent.Close()
	if parent.Sync() != nil {
		return errors.New("sandbox_manifest_sync_failed")
	}
	return nil
}

// reserveIntent commits an immutable provider intent before a network mutation.
// Existing UNKNOWN intents cannot be sent again merely because setup restarted.
func reserveIntent(m *sandboxManifest, name, requestID string, payload []byte) error {
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	if existing, ok := m.Intents[name]; ok {
		if existing.RequestID != requestID || existing.PayloadHash != hash {
			return errors.New("sandbox_setup_payload_changed")
		}
		return errors.New("sandbox_setup_reconciliation_required")
	}
	if name == "" || requestID == "" || len(requestID) > 50 || !json.Valid(payload) {
		return errors.New("sandbox_setup_intent_invalid")
	}
	m.Intents[name] = setupIntent{RequestID: requestID, PayloadHash: hash, State: "UNKNOWN"}
	return nil
}
