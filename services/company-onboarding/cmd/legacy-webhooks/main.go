// Command legacy-webhooks audits or transfers trusted legacy Node receipts.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/legacywebhooks"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("legacy-webhooks", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceFile := flags.String("source-database-url-file", "", "Private approved legacy database connection file")
	sourceName := flags.String("source-database-name", "", "Mandatory expected legacy database name")
	targetName := flags.String("database-name", "", "Mandatory expected Go database name")
	environment := flags.String("environment", "", "Explicit sandbox or production inbox scope")
	apply := flags.Bool("apply", false, "Transfer audited receipts, keeping all legacy rows unchanged")
	sourceHash := flags.String("expected-source-hash", "", "Exact hash returned by the prior audit")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *sourceFile == "" || *sourceName == "" || *targetName == "" || (*environment != "sandbox" && *environment != "production") {
		return errors.New("legacy_webhook_arguments_invalid")
	}
	if *apply {
		digest, err := hex.DecodeString(*sourceHash)
		if err != nil || len(digest) != 32 {
			return legacywebhooks.ErrChanged
		}
	}
	file, err := os.OpenFile(*sourceFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("legacy_webhook_source_file_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("legacy_webhook_source_file_not_private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(raw) > 16384 {
		return errors.New("legacy_webhook_source_file_invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	pools := make([]*pgxpool.Pool, 0, 2)
	for _, binding := range []struct{ url, name string }{{strings.TrimSpace(string(raw)), *sourceName}, {os.Getenv("DATABASE_URL"), *targetName}} {
		cfg, err := pgxpool.ParseConfig(binding.url)
		if err != nil || cfg.ConnConfig.Database != binding.name || binding.url == "" {
			return legacywebhooks.ErrTarget
		}
		cfg.ConnConfig.ConnectTimeout = 5 * time.Second
		cfg.MaxConns = 2
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return errors.New("legacy_webhook_connection_unavailable")
		}
		defer pool.Close()
		pools = append(pools, pool)
	}
	report, err := legacywebhooks.Transfer(ctx, pools[0], pools[1], legacywebhooks.Options{SourceDatabase: *sourceName, TargetDatabase: *targetName, Environment: *environment, Apply: *apply, ExpectedSourceHash: *sourceHash})
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("legacy_webhook_report_write_failed")
	}
	return err
}
