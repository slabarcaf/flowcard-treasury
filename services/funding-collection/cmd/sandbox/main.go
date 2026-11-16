// Command sandbox provides bounded, opt-in Airwallex sandbox acceptance tools.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		// Only locally defined codes reach stderr. Never print credentials or driver errors.
		_, _ = io.WriteString(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("sandbox_command_required")
	}
	command := args[0]
	switch command {
	case "cleanup-webhooks", "inventory-local", "cleanup-local", "verify-journey", "preflight", "reference-industries", "inspect-webhook", "setup-accounts", "activate-accounts", "inspect-accounts", "complete-profiles", "probe-products", "setup-receiving", "setup-cardholder", "setup-card", "seed-opening-balances", "inspect-balances", "simulate-decline", "capture-event", "setup-webhooks", "simulate-callback-decline", "export-shortfall-contract", "prepare-local", "probe-deposit", "probe-transfers", "seed-local-books", "export-execution-contract", "serve-local", "bind-local-users", "relay-shortfall", "refresh-local-snapshots", "simulate-journey-decline", "simulate-recovery-decline", "simulate-income", "simulate-recovery-income":
	default:
		return errors.New("sandbox_command_unknown")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	testAdminFile := flags.String("test-admin-url-file", "", "Existing localhost:54329 maintenance URL file for owned setup")
	databaseName := flags.String("database-name", "", "Mandatory expected owned database name")
	callbackOriginFile := flags.String("callback-origin-file", "", "Private file with approved HTTPS callback origin")
	manifest := flags.String("manifest", "", "Private absolute manifest path for owned setup")
	runID := flags.String("run-id", "", "Unique fc-sbx- run marker")
	acceptAgreements := flags.Bool("accept-sandbox-agreements", false, "Explicit acceptance for synthetic sandbox businesses only")
	envFile := flags.String("env-file", "", "Existing root credential file; never copied")
	environment := flags.String("environment", "", "Must explicitly be SANDBOX")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *envFile == "" || *environment != "SANDBOX" {
		return errors.New("sandbox_arguments_invalid")
	}
	if *acceptAgreements && command != "setup-accounts" {
		return errors.New("sandbox_agreements_only_for_setup")
	}
	needsManifest := command != "preflight" && command != "reference-industries" && command != "inspect-webhook"
	if needsManifest && (*manifest == "" || !runPattern.MatchString(*runID)) {
		return errors.New("sandbox_manifest_arguments_invalid")
	}
	cfg, err := loadCredentials(*envFile)
	if err != nil {
		return err
	}
	duration := 60 * time.Second
	if command == "serve-local" {
		duration = 45 * time.Minute
	}
	if command == "prepare-local" || command == "seed-local-books" || command == "bind-local-users" {
		duration = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if command == "preflight" {
		report := preflight(ctx, cfg, nil)
		if json.NewEncoder(out).Encode(report) != nil {
			return errors.New("sandbox_report_write_failed")
		}
		if !report.ReadAccessVerified {
			return errors.New("sandbox_preflight_blocked")
		}
		return nil
	}
	if command == "inventory-local" {
		_, err := inspectLocal(ctx, *manifest, *runID, *databaseName, out)
		return err
	}
	if command == "cleanup-local" {
		return cleanupAcceptanceLocal(ctx, *manifest, *runID, *testAdminFile, *databaseName, out)
	}
	if command == "verify-journey" {
		return verifyJourney(ctx, cfg, *manifest, *runID, *databaseName, out)
	}
	api, err := openSandboxAPI(ctx, cfg, nil)
	if err != nil {
		return err
	}
	switch command {
	case "reference-industries":
		return listSoftwareIndustries(ctx, api, out)
	case "inspect-webhook":
		return inspectWebhook(ctx, api, cfg, out)
	case "inspect-accounts":
		return inspectAccounts(ctx, api, cfg, *manifest, *runID, out)
	case "complete-profiles":
		return completeProfiles(ctx, api, cfg, *manifest, *runID)
	case "probe-products":
		return probeProducts(ctx, api, cfg, *manifest, *runID, out)
	case "setup-receiving":
		return setupReceiving(ctx, api, cfg, *manifest, *runID)
	case "setup-cardholder":
		return setupCardholder(ctx, api, cfg, *manifest, *runID)
	case "setup-card":
		return setupCard(ctx, api, cfg, *manifest, *runID)
	case "seed-opening-balances":
		return seedOpeningBalances(ctx, api, cfg, *manifest, *runID)
	case "inspect-balances":
		return inspectBalances(ctx, api, cfg, *manifest, *runID, out)
	case "simulate-decline":
		return simulateDecline(ctx, api, cfg, *manifest, *runID, out)
	case "capture-event":
		return captureEvent(ctx, api, cfg, *manifest, *runID, out)
	case "cleanup-webhooks":
		return cleanupWebhooks(ctx, api, cfg, *manifest, *runID, *callbackOriginFile)
	case "setup-webhooks":
		return setupWebhooks(ctx, api, cfg, *manifest, *runID, *callbackOriginFile)
	case "simulate-callback-decline":
		return simulateDeclineScenario(ctx, api, cfg, *manifest, *runID, "callback-decline", out)
	case "export-shortfall-contract":
		return exportShortfallContract(ctx, api, cfg, *manifest, *runID)
	case "prepare-local":
		return prepareLocalDatabase(ctx, api, cfg, *manifest, *runID, *testAdminFile, *databaseName)
	case "simulate-income", "simulate-recovery-income":
		name := "journey-income"
		if command == "simulate-recovery-income" {
			name = "journey-recovery-income"
		}
		return simulateIncome(ctx, api, cfg, *manifest, *runID, name)
	case "probe-deposit":
		return probeDeposit(ctx, api, cfg, *manifest, *runID)
	case "probe-transfers":
		return probeTransfers(ctx, api, cfg, *manifest, *runID)
	case "seed-local-books":
		return seedLocalBooks(ctx, api, cfg, *manifest, *runID, *testAdminFile, *databaseName)
	case "export-execution-contract":
		return exportExecutionContract(ctx, api, cfg, *manifest, *runID)
	case "serve-local":
		return serveLocalStack(ctx, cfg, *manifest, *runID)
	case "bind-local-users":
		return bindLocalUsers(ctx, *manifest, *runID, *testAdminFile, *databaseName)
	case "relay-shortfall":
		return relayShortfallEvidence(ctx, *manifest, *runID)
	case "refresh-local-snapshots":
		return refreshLocalSnapshots(ctx, *manifest, *runID, true)
	case "simulate-journey-decline", "simulate-recovery-decline":
		scenario := "journey-decline"
		if command == "simulate-recovery-decline" {
			scenario = "journey-recovery-decline"
		}
		if err := refreshLocalSnapshots(ctx, *manifest, *runID, true); err != nil {
			return err
		}
		if err := simulateDeclineScenario(ctx, api, cfg, *manifest, *runID, scenario, out); err != nil {
			return err
		}
		return refreshLocalSnapshots(ctx, *manifest, *runID, true)
	case "activate-accounts":
		if err := activateAccounts(ctx, api, cfg, *manifest, *runID); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "sandbox account pair ACTIVE verified; financial capabilities unverified\n"); err != nil {
			return errors.New("sandbox_report_write_failed")
		}
	case "setup-accounts":
		if err := setupAccounts(ctx, api, cfg, *manifest, *runID, *acceptAgreements); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "sandbox account pair created and ownership verified; activation and financial capabilities unverified\n"); err != nil {
			return errors.New("sandbox_report_write_failed")
		}
	}
	return nil
}
