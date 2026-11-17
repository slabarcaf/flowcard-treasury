package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRestartAndOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run.json")
	runID := "fc-sbx-12345678"
	create := func(m *sandboxManifest) error {
		return reserveIntent(m, "account-a", "stable-request", []byte(`{"name":"synthetic"}`))
	}
	if err := withManifest(path, runID, "test-platform", true, create); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("manifest permissions unsafe")
	}
	err = withManifest(path, runID, "test-platform", false, create)
	if err == nil || err.Error() != "sandbox_setup_reconciliation_required" {
		t.Fatal("unknown intent could be resent")
	}
	err = withManifest(path, runID, "test-platform", false, func(m *sandboxManifest) error {
		return reserveIntent(m, "account-a", "stable-request", []byte(`{"name":"changed"}`))
	})
	if err == nil || err.Error() != "sandbox_setup_payload_changed" {
		t.Fatal("immutable intent changed")
	}
	for _, tc := range []struct{ run, platform string }{{"fc-sbx-87654321", "test-platform"}, {runID, "wrong-platform"}} {
		called := false
		err := withManifest(path, tc.run, tc.platform, false, func(*sandboxManifest) error { called = true; return nil })
		if err == nil || called {
			t.Fatal("foreign manifest accepted")
		}
	}
	if err := withManifest(path, runID, "test-platform", false, func(m *sandboxManifest) error {
		intent := m.Intents["account-a"]
		intent.State = "VERIFIED"
		intent.ResourceID = "test-account"
		m.Intents["account-a"] = intent
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := withManifest(path, runID, "test-platform", false, func(m *sandboxManifest) error {
		if m.Intents["account-a"].ResourceID != "test-account" {
			t.Fatal("receipt lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsUnsafeFilesAndPreservesOnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run.json")
	runID := "fc-sbx-12345678"
	if err := withManifest(path, runID, "test-platform", true, func(*sandboxManifest) error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = withManifest(path, runID, "test-platform", false, func(m *sandboxManifest) error { m.RunID = "changed"; return errors.New("test interruption") })
	if err == nil {
		t.Fatal("expected callback failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed update changed durable state")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := withManifest(path, runID, "test-platform", false, func(*sandboxManifest) error { return nil }); err == nil {
		t.Fatal("public manifest accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "not-owned"), path); err != nil {
		t.Fatal(err)
	}
	if err := withManifest(path, runID, "test-platform", true, func(*sandboxManifest) error { return nil }); err == nil {
		t.Fatal("symlink followed")
	}
}
