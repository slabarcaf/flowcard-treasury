package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func seedLocalBooks(ctx context.Context, api *sandboxAPI, cfg credentials, manifest, runID, adminFile, databaseName string) error {
	var profile localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &profile, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(profile); err != nil {
		return err
	}
	if profile.RunID != runID || profile.DatabaseName != databaseName {
		return errors.New("sandbox_book_target_mismatch")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	adminURL, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	adminURL.Path = "/" + databaseName
	admin, err := pgxpool.New(ctx, adminURL.String())
	if err != nil {
		return errors.New("sandbox_book_admin_unavailable")
	}
	defer admin.Close()
	var actual, marker, owner string
	if admin.QueryRow(ctx, `SELECT current_database(),COALESCE(shobj_description(d.oid,'pg_database'),''),r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=current_database()`).Scan(&actual, &marker, &owner) != nil || actual != databaseName || marker != profile.Marker || owner != profile.AdminOwner {
		return errors.New("sandbox_book_database_not_owned")
	}
	borrower, err := ownedScopedAPI(ctx, api, cfg, manifest, runID, "borrower")
	if err != nil {
		return err
	}
	source, err := ownedScopedAPI(ctx, api, cfg, manifest, runID, "funding")
	if err != nil {
		return err
	}
	card, err := savedIntent(manifest, runID, cfg.platform, "card-borrower")
	if err != nil || card.State != "VERIFIED" {
		return errors.New("sandbox_book_card_unverified")
	}
	attempt, err := savedIntent(manifest, runID, cfg.platform, "callback-decline")
	if err != nil || attempt.ParentTransactionID == "" {
		return errors.New("sandbox_book_payment_reference_unverified")
	}
	now := profile.CapturedAt.UTC()
	invoiceID := "sandbox-invoice-" + runID
	invoiceSystem := "sandbox-synthetic-invoicing"
	invoiceRef := domain.SourceRef{System: invoiceSystem, RecordID: invoiceID, Version: "1", EffectiveAt: now}
	sourceRefs, _ := json.Marshal([]domain.SourceRef{invoiceRef})
	actorRef, _ := json.Marshal(domain.SourceRef{System: "sandbox-invited-users", RecordID: runID, Version: "1", EffectiveAt: now})
	authorityRefs, _ := json.Marshal([]domain.SourceRef{{System: "sandbox-test-authorization", RecordID: runID + "-collection", Version: "1", EffectiveAt: now}})
	seedProof, _ := json.Marshal(struct {
		RunID           string    `json:"run_id"`
		CustomerID      string    `json:"customer_id"`
		FacilityID      string    `json:"facility_id"`
		FundingAccount  string    `json:"funding_account"`
		BorrowerAccount string    `json:"borrower_account"`
		Card            string    `json:"card_id"`
		CapturedAt      time.Time `json:"captured_at"`
		KnownEmpty      bool      `json:"known_empty_opening_books"`
		History         string    `json:"financial_history"`
	}{runID, profile.CustomerID, profile.FacilityID, source.scope, borrower.scope, card.ResourceID, now, true, "explicit synthetic sandbox underwriting fixture; actual provider status and account mapping verified separately"})
	digest := sha256.Sum256(seedProof)
	hash := hex.EncodeToString(digest[:])
	tx, err := admin.Begin(ctx)
	if err != nil {
		return errors.New("sandbox_book_transaction_failed")
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, runID+":opening-books"); err != nil {
		return errors.New("sandbox_book_lock_failed")
	}
	var journalCount int
	if tx.QueryRow(ctx, `SELECT count(*) FROM servicing.import_journal WHERE mode='SANDBOX' AND source_id=$1 AND source_version='1' AND content_hash=$2`, runID, hash).Scan(&journalCount) != nil {
		return errors.New("sandbox_book_journal_unavailable")
	}
	if journalCount != 4 {
		var dirty bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.facilities) OR EXISTS(SELECT 1 FROM servicing.customers) OR EXISTS(SELECT 1 FROM servicing.actors) OR EXISTS(SELECT 1 FROM servicing.authorities) OR EXISTS(SELECT 1 FROM servicing.offers) OR EXISTS(SELECT 1 FROM servicing.operations)`).Scan(&dirty) != nil || dirty {
			return errors.New("sandbox_book_requires_empty_owned_state")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.facilities(mode,id,funding_account_ref,currency,total_cents,reserve_bps,source_id,source_version,captured_at,source_refs) VALUES('SANDBOX',$1,$2,'USD',10000000,2000,$3,'1',$4,$5::jsonb)`, profile.FacilityID, source.scope, runID, now, string(sourceRefs)); err != nil {
			return errors.New("sandbox_facility_seed_failed")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.customers(mode,id,history_complete) VALUES('SANDBOX',$1,true)`, profile.CustomerID); err != nil {
			return errors.New("sandbox_customer_seed_failed")
		}
		due := now.AddDate(0, 0, 20).Format("2006-01-02")
		invoiceRefJSON, _ := json.Marshal(invoiceRef)
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.receivables(mode,customer_id,source_system,invoice_id,source_revision,currency,original_cents,unpaid_cents,due_on,due_date_verified,allocation_complete,disputed,source_complete,payer_id,source_ref,last_captured_at) VALUES('SANDBOX',$1,$2,$3,'1','USD',800000,800000,$4,true,true,false,true,'sandbox-payer',$5::jsonb,$6)`, profile.CustomerID, invoiceSystem, invoiceID, due, string(invoiceRefJSON), now); err != nil {
			return errors.New("sandbox_receivable_seed_failed")
		}
		for _, role := range []string{"operator", "customer"} {
			user := profile.Users[role]
			if user.ID == "" {
				return errors.New("sandbox_invited_user_missing")
			}
			var customer any
			if role == "customer" {
				customer = profile.CustomerID
			}
			if _, err := tx.Exec(ctx, `INSERT INTO servicing.actors(mode,actor_id,customer_id,actor_type,authentication_subject,active,source_ref,captured_at,source_id,source_version) VALUES('SANDBOX',$1,$2,$3,$4,true,$5,$6,$7,'1')`, requestIDFor(runID, "actor-"+role), customer, map[string]string{"operator": "OPERATOR", "customer": "CUSTOMER"}[role], user.ID, string(actorRef), now, runID); err != nil {
				return errors.New("sandbox_actor_seed_failed")
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.authorities(mode,id,version,customer_id,source_account_ref,destination_account_ref,currency,permitted_kinds,maximum_debit_cents,valid_from,expires_at,status,disclosures_version,evidence_refs,captured_at,source_id,source_version,source_captured_at,product_code,mandate_reference,permitted_purposes) VALUES('SANDBOX',$1,1,$2,$3,$4,'USD',ARRAY['COLLECT_FEE','COLLECT_PRINCIPAL'],1000000,$5,$6,'ACTIVE','disclosure-v1',$7::jsonb,$8,$9,'1',$8,'CONNECTED_ACCOUNT_TRANSFERS',$10,ARRAY['professional_business_services','loan_credit_repayment'])`, "sandbox-authority-"+runID, profile.CustomerID, borrower.scope, source.scope, now.Add(-time.Minute), time.Now().UTC().Add(8*time.Hour), string(authorityRefs), now, runID, "sandbox-mandate-"+runID); err != nil {
			return errors.New("sandbox_authority_seed_failed")
		}
		for _, kind := range []string{"facility", "receivables", "actors", "authority"} {
			if _, err := tx.Exec(ctx, `INSERT INTO servicing.import_journal(mode,kind,source_id,source_version,owner_id,content_hash,canonical_input,captured_at,source_refs) VALUES('SANDBOX',$1,$2,'1',$3,$4,$5::jsonb,$6,$7::jsonb)`, kind, runID, profile.FacilityID, hash, string(seedProof), now, string(sourceRefs)); err != nil {
				return errors.New("sandbox_book_journal_write_failed")
			}
		}
	}
	// The rule engine records a system audit decision separately from the two
	// real browser users. This actor has no browser/login credential.
	var systemCount int
	if tx.QueryRow(ctx, `SELECT count(*) FROM servicing.actors WHERE mode='SANDBOX' AND actor_type='SYSTEM' AND active`).Scan(&systemCount) != nil {
		return errors.New("sandbox_system_actor_lookup_failed")
	}
	if systemCount == 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.actors(mode,actor_id,customer_id,actor_type,authentication_subject,active,source_ref,captured_at,source_id,source_version) VALUES('SANDBOX',$1,NULL,'SYSTEM','system:funding-collection',true,$2,$3,$4,'1')`, requestIDFor(runID, "actor-system"), string(actorRef), now, runID); err != nil {
			return errors.New("sandbox_system_actor_seed_failed")
		}
	} else {
		var matches bool
		if tx.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(actor_id=$1 AND authentication_subject='system:funding-collection') FROM servicing.actors WHERE mode='SANDBOX' AND actor_type='SYSTEM' AND active`, requestIDFor(runID, "actor-system")).Scan(&matches) != nil || !matches {
			return errors.New("sandbox_system_actor_mismatch")
		}
	}
	if tx.Commit(ctx) != nil {
		return errors.New("sandbox_book_commit_failed")
	}
	synthetic, err := upstreamfixture.New(now, "SANDBOX")
	if err != nil {
		return errors.New("sandbox_policy_fixture_unavailable")
	}
	customer := synthetic.Input.Customer
	customer.CustomerID = profile.CustomerID
	customer.AccountID = borrower.scope
	customer.CardID = card.ResourceID
	customer.SourceRefs = []policy.SourceReference{{System: "sandbox-synthetic-history", RecordID: runID, Version: "1", EffectiveAt: now}, {System: invoiceSystem, RecordID: invoiceID, Version: "1", EffectiveAt: now}, {System: "sandbox-synthetic-classification", RecordID: runID + "-classification", Version: "1", EffectiveAt: now}}
	customer.Receivables[0].InvoiceID = invoiceID
	customer.Receivables[0].SourceRef = customer.SourceRefs[1]
	customer.Classifications[0].TransactionID = attempt.ParentTransactionID
	customer.Classifications[0].SourceRef = customer.SourceRefs[2]
	body, err := json.Marshal(customer)
	if err != nil {
		return errors.New("sandbox_customer_context_invalid")
	}
	var customerFile map[string]json.RawMessage
	if json.Unmarshal(body, &customerFile) != nil {
		return errors.New("sandbox_customer_context_invalid")
	}
	delete(customerFile, "snapshot_id")
	delete(customerFile, "snapshot_version")
	customerFile["schema_version"] = json.RawMessage(`1`)
	customerFile["source_id"], _ = json.Marshal("sandbox-synthetic-customer-" + runID)
	customerFile["source_version"] = json.RawMessage(`"1"`)
	customerJSON, _ := json.MarshalIndent(customerFile, "", "  ")
	customerPath := filepath.Join(filepath.Dir(manifest), "underwriting-customer.json")
	if err := writePrivateFile(customerPath, customerJSON); err != nil {
		return err
	}
	facilityJSON, _ := json.MarshalIndent(map[string]any{"schema_version": 1, "source_mode": "SANDBOX", "source_id": "sandbox-opening-facility-" + runID, "source_version": "1", "captured_at": now, "source_refs": customer.SourceRefs[:1], "facility_id": profile.FacilityID, "currency": "USD", "facility_total_cents": 10000000, "reserve_floor_bps": 2000, "funded_principal_cents": 0, "reserved_principal_cents": 0, "ledger_version": "sandbox-opening-" + runID, "ledger_complete": true}, "", "  ")
	facilityPath := filepath.Join(filepath.Dir(manifest), "underwriting-facility.json")
	if err := writePrivateFile(facilityPath, facilityJSON); err != nil {
		return err
	}
	environment := map[string]string{"DATABASE_URL": profile.URLs["underwriting"], "UNDERWRITING_MODE": "SANDBOX", "UNDERWRITING_FACILITY_ID": profile.FacilityID}
	for _, snapshot := range []struct{ kind, path string }{{"customer", customerPath}, {"facility", facilityPath}} {
		if err := runLocalCommand(ctx, root, "underwriting-engine", []string{"./cmd/import-snapshot", "--kind", snapshot.kind, "--file", snapshot.path}, environment, ""); err != nil {
			return err
		}
	}
	return nil
}
