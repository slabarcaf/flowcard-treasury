package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/legacywebhooks"
)

func TestRefusesUnreviewedApplyAndWrongDatabaseBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "legacy-url.txt")
	secretURL := "postgres://fixture:private-fixture-password@127.0.0.1:1/actual_database"
	if os.WriteFile(file, []byte(secretURL), 0600) != nil {
		t.Fatal("private fixture unavailable")
	}
	t.Setenv("DATABASE_URL", "postgres://fixture:private-target@127.0.0.1:1/target_database")
	args := []string{"--source-database-url-file", file, "--source-database-name", "wrong_database", "--database-name", "target_database", "--environment", "sandbox"}
	var out bytes.Buffer
	if err := run(context.Background(), args, &out); !errors.Is(err, legacywebhooks.ErrTarget) {
		t.Fatal("expected source name was not enforced before connecting")
	}
	args = append(args, "--apply")
	if err := run(context.Background(), args, &out); !errors.Is(err, legacywebhooks.ErrChanged) {
		t.Fatal("apply accepted an absent reviewed hash")
	}
	if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "postgres://") {
		t.Fatal("connection values leaked")
	}
}

func TestRefusesPublicAndSymlinkCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "legacy-url.txt")
	if os.WriteFile(file, []byte("postgres://fixture:private@127.0.0.1:1/source"), 0644) != nil {
		t.Fatal("fixture unavailable")
	}
	args := []string{"--source-database-url-file", file, "--source-database-name", "source", "--database-name", "target", "--environment", "sandbox"}
	var out bytes.Buffer
	if err := run(context.Background(), args, &out); err == nil {
		t.Fatal("public connection file accepted")
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(file, link) != nil {
		t.Fatal("symlink fixture unavailable")
	}
	args[1] = link
	if err := run(context.Background(), args, &out); err == nil {
		t.Fatal("symlink connection file accepted")
	}
}
