package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

// ImportResult reports producer-key replay without returning imported source data.
type ImportResult struct {
	Replayed bool
	Records  int
}

func lockImportScope(ctx context.Context, tx pgx.Tx, facilityID string) error {
	if facilityID == "" {
		return errors.New("import_facility_required")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('funding-import:' || $1,0))`, facilityID); err != nil {
		return errors.New("import_scope_lock_failed")
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM servicing.facilities WHERE mode='DEMO' AND id=$1 FOR UPDATE`, facilityID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("import_facility_lock_failed")
	}
	return nil
}

func canonicalHash(value any) (string, []byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", nil, errors.New("import_canonicalization_failed")
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), body, nil
}

func validateMetadata(meta domain.ImportMetadata, mode string, now time.Time) error {
	_, capturedOffset := meta.CapturedAt.Zone()
	if mode != "DEMO" || meta.SourceMode != mode || meta.SchemaVersion != 1 ||
		meta.SourceID == "" || meta.SourceVersion == "" || len(meta.SourceRefs) == 0 || len(meta.SourceRefs) > 10000 ||
		meta.CapturedAt.IsZero() || capturedOffset != 0 || meta.CapturedAt.After(now.Add(5*time.Second)) {
		return errors.New("import_metadata_invalid")
	}
	for _, ref := range meta.SourceRefs {
		if !validSourceRef(ref) {
			return errors.New("import_source_reference_invalid")
		}
	}
	return nil
}

func validSourceRef(ref domain.SourceRef) bool {
	_, offset := ref.EffectiveAt.Zone()
	return ref.System != "" && ref.RecordID != "" && ref.Version != "" && !ref.EffectiveAt.IsZero() && offset == 0
}

func recordImport(ctx context.Context, tx pgx.Tx, kind, owner string, meta domain.ImportMetadata, hash string, canonical []byte) (bool, error) {
	var oldHash string
	err := tx.QueryRow(ctx, `SELECT content_hash FROM servicing.import_journal
		WHERE mode='DEMO' AND kind=$1 AND source_id=$2 AND source_version=$3 AND owner_id=$4`,
		kind, meta.SourceID, meta.SourceVersion, owner).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			return false, errors.New("import_idempotency_conflict")
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("import_journal_read_failed")
	}
	var latest time.Time
	err = tx.QueryRow(ctx, `SELECT captured_at FROM servicing.import_journal
		WHERE mode='DEMO' AND kind=$1 AND owner_id=$2 ORDER BY captured_at DESC LIMIT 1`, kind, owner).Scan(&latest)
	if err == nil && meta.CapturedAt.Before(latest) {
		return false, errors.New("import_stale_source")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("import_journal_read_failed")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.import_journal
		(mode,kind,source_id,source_version,owner_id,content_hash,canonical_input,captured_at,source_refs)
		VALUES('DEMO',$1,$2,$3,$4,$5,$6::jsonb,$7,$8::jsonb)`, kind, meta.SourceID, meta.SourceVersion, owner, hash, string(canonical), meta.CapturedAt, metadataRefsJSON(meta)); err != nil {
		return false, errors.New("import_journal_write_failed")
	}
	return false, nil
}

func metadataRefsJSON(meta domain.ImportMetadata) string {
	data, _ := json.Marshal(meta.SourceRefs)
	return string(data)
}

// ImportFacility publishes a synthetic dedicated facility and validates each
// borrower using only existing public roster/card reads.
func (r *Repository) ImportFacility(ctx context.Context, input domain.FacilityImport, configuredFacility string, now time.Time) (ImportResult, error) {
	if err := validateMetadata(input.ImportMetadata, "DEMO", now); err != nil || input.FacilityID != configuredFacility || input.FundingAccountRef == "" || input.Currency != "USD" || input.FacilityTotalCents <= 0 || input.ReserveFloorBPS < 0 || input.ReserveFloorBPS > 10000 || input.OtherCommitmentsCents < 0 || !input.KnownEmptyBooks || len(input.Customers) == 0 || len(input.Customers) > 10000 {
		return ImportResult{}, errors.New("facility_import_invalid")
	}
	hash, canonical, err := canonicalHash(input)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockImportScope(ctx, tx, input.FacilityID); err != nil {
		return ImportResult{}, err
	}
	seenCustomers, seenAccounts, seenCards := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, customer := range input.Customers {
		if customer.CustomerID == "" || customer.AccountID == "" || customer.CardID == "" || seenCustomers[customer.CustomerID] || seenAccounts[customer.AccountID] || seenCards[customer.CardID] {
			return ImportResult{}, errors.New("facility_customer_mapping_duplicate")
		}
		seenCustomers[customer.CustomerID], seenAccounts[customer.AccountID], seenCards[customer.CardID] = true, true, true
	}
	var priorMode, priorAccount string
	err = tx.QueryRow(ctx, `SELECT mode,funding_account_ref FROM servicing.facilities WHERE mode='DEMO' AND id=$1 FOR UPDATE`, input.FacilityID).Scan(&priorMode, &priorAccount)
	facilityExists := err == nil
	if err == nil && (priorMode != "DEMO" || priorAccount != input.FundingAccountRef) {
		return ImportResult{}, errors.New("facility_binding_immutable")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ImportResult{}, errors.New("facility_read_failed")
	}
	replay, err := recordImport(ctx, tx, "facility", input.FacilityID, input.ImportMetadata, hash, canonical)
	if err != nil {
		return ImportResult{}, err
	}
	if replay {
		if err = tx.Commit(ctx); err != nil {
			return ImportResult{}, errors.New("import_commit_failed")
		}
		return ImportResult{Replayed: true, Records: len(input.Customers)}, nil
	}
	if facilityExists {
		var dirty bool
		if err = tx.QueryRow(ctx, `SELECT principal_owed_cents<>0 OR reserved_principal_cents<>0 OR other_commitments_cents<>0 OR EXISTS(SELECT 1 FROM servicing.bridges WHERE mode='DEMO' AND facility_id=$1 AND (ever_funded OR lifecycle<>'VOID_UNFUNDED')) OR EXISTS(SELECT 1 FROM servicing.customers WHERE mode='DEMO' AND defaulted_at IS NOT NULL) FROM servicing.facilities WHERE mode='DEMO' AND id=$1`, input.FacilityID).Scan(&dirty); err != nil || dirty {
			return ImportResult{}, errors.New("known_empty_facility_conflict")
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO servicing.facilities(mode,id,funding_account_ref,currency,total_cents,reserve_bps,other_commitments_cents,source_id,source_version,captured_at,source_refs)
		VALUES('DEMO',$1,$2,'USD',$3,$4,$5,$6,$7,$8,$9::jsonb)
		ON CONFLICT(mode,id) DO UPDATE SET total_cents=EXCLUDED.total_cents,reserve_bps=EXCLUDED.reserve_bps,other_commitments_cents=EXCLUDED.other_commitments_cents,configuration_version=servicing.facilities.configuration_version+1,version=servicing.facilities.version+1,source_id=EXCLUDED.source_id,source_version=EXCLUDED.source_version,captured_at=EXCLUDED.captured_at,source_refs=EXCLUDED.source_refs,updated_at=clock_timestamp()`, input.FacilityID, input.FundingAccountRef, input.FacilityTotalCents, input.ReserveFloorBPS, input.OtherCommitmentsCents, input.SourceID, input.SourceVersion, input.CapturedAt, metadataRefsJSON(input.ImportMetadata)); err != nil {
		return ImportResult{}, errors.New("facility_write_failed")
	}
	for _, customer := range input.Customers {
		if customer.CustomerID == "" || customer.AccountID == "" || customer.CardID == "" || !customer.HistoryComplete {
			return ImportResult{}, errors.New("known_empty_customer_invalid")
		}
		if _, err = upstream.ResolveMapping(ctx, tx, customer.CustomerID, customer.AccountID, customer.CardID); err != nil {
			return ImportResult{}, err
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.customers WHERE mode='DEMO' AND id=$1 AND (principal_owed_cents<>0 OR unpaid_fee_cents<>0 OR reserved_principal_cents<>0 OR reserved_fee_cents<>0 OR defaulted_at IS NOT NULL)) OR EXISTS(SELECT 1 FROM servicing.bridges WHERE mode='DEMO' AND customer_id=$1 AND (ever_funded OR lifecycle<>'VOID_UNFUNDED'))`, customer.CustomerID).Scan(&exists); err != nil || exists {
			return ImportResult{}, errors.New("known_empty_books_conflict")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO servicing.customers(mode,id,history_complete) VALUES('DEMO',$1,true) ON CONFLICT(mode,id) DO UPDATE SET history_complete=true,version=servicing.customers.version+1,updated_at=clock_timestamp()`, customer.CustomerID); err != nil {
			return ImportResult{}, errors.New("customer_write_failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ImportResult{}, errors.New("import_commit_failed")
	}
	return ImportResult{Records: len(input.Customers) + 1}, nil
}

// ImportReceivables publishes factual invoice evidence and retains local
// encumbrances. Conflicting backing is held for review rather than clamped.
func (r *Repository) ImportReceivables(ctx context.Context, input domain.ReceivablesImport, facilityID string, now time.Time) (ImportResult, error) {
	if err := validateMetadata(input.ImportMetadata, "DEMO", now); err != nil || len(input.Receivables) == 0 || len(input.Receivables) > 10000 {
		return ImportResult{}, errors.New("receivables_import_invalid")
	}
	hash, canonical, err := canonicalHash(input)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockImportScope(ctx, tx, facilityID); err != nil {
		return ImportResult{}, err
	}
	var facilityExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.facilities WHERE mode='DEMO' AND id=$1)`, facilityID).Scan(&facilityExists); err != nil || !facilityExists {
		return ImportResult{}, errors.New("receivable_facility_unavailable")
	}
	owner := input.SourceID
	replay, err := recordImport(ctx, tx, "receivables", owner, input.ImportMetadata, hash, canonical)
	if err != nil {
		return ImportResult{}, err
	}
	if replay {
		if err = tx.Commit(ctx); err != nil {
			return ImportResult{}, errors.New("import_commit_failed")
		}
		return ImportResult{Replayed: true, Records: len(input.Receivables)}, nil
	}
	imports := append([]domain.ReceivableImport(nil), input.Receivables...)
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].CustomerID != imports[j].CustomerID {
			return imports[i].CustomerID < imports[j].CustomerID
		}
		if imports[i].SourceSystem != imports[j].SourceSystem {
			return imports[i].SourceSystem < imports[j].SourceSystem
		}
		return imports[i].InvoiceID < imports[j].InvoiceID
	})
	lastLockedCustomer := ""
	for _, inv := range imports {
		if inv.CustomerID == lastLockedCustomer {
			continue
		}
		var customerID string
		if err = tx.QueryRow(ctx, `SELECT id FROM servicing.customers WHERE mode='DEMO' AND id=$1 FOR UPDATE`, inv.CustomerID).Scan(&customerID); err != nil {
			return ImportResult{}, errors.New("receivable_customer_lock_failed")
		}
		lastLockedCustomer = inv.CustomerID
	}
	bridgeIDs := map[string]bool{}
	for _, inv := range imports {
		rows, lookupErr := tx.Query(ctx, `SELECT DISTINCT b.id::text FROM servicing.bridge_invoice_allocations a JOIN servicing.bridges b ON b.mode=a.mode AND b.id=a.bridge_id WHERE a.mode='DEMO' AND a.source_system=$1 AND a.invoice_id=$2 AND b.lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD')`, inv.SourceSystem, inv.InvoiceID)
		if lookupErr != nil {
			return ImportResult{}, errors.New("receivable_bridge_lookup_failed")
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return ImportResult{}, errors.New("receivable_bridge_lookup_failed")
			}
			bridgeIDs[id] = true
		}
		if rows.Err() != nil {
			rows.Close()
			return ImportResult{}, errors.New("receivable_bridge_lookup_failed")
		}
		rows.Close()
	}
	orderedBridgeIDs := make([]string, 0, len(bridgeIDs))
	for id := range bridgeIDs {
		orderedBridgeIDs = append(orderedBridgeIDs, id)
	}
	sort.Strings(orderedBridgeIDs)
	for _, id := range orderedBridgeIDs {
		var locked string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM servicing.bridges WHERE mode='DEMO' AND id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return ImportResult{}, errors.New("receivable_bridge_lock_failed")
		}
	}
	for _, inv := range imports {
		var locked string
		err = tx.QueryRow(ctx, `SELECT invoice_id FROM servicing.receivables WHERE mode='DEMO' AND source_system=$1 AND invoice_id=$2 FOR UPDATE`, inv.SourceSystem, inv.InvoiceID).Scan(&locked)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ImportResult{}, errors.New("receivable_row_lock_failed")
		}
	}
	seen := map[string]bool{}
	for _, inv := range imports {
		key := inv.SourceSystem + "\x00" + inv.InvoiceID
		if inv.CustomerID == "" || inv.SourceSystem == "" || inv.InvoiceID == "" || inv.PayerID == "" || inv.Currency != "USD" || inv.EligibleUnpaidCents < 0 || !validSourceRef(inv.SourceRef) || seen[key] {
			return ImportResult{}, errors.New("receivable_invalid")
		}
		seen[key] = true
		if inv.DueDateVerified && (inv.DueOn == nil || *inv.DueOn == "") {
			return ImportResult{}, errors.New("receivable_due_date_invalid")
		}
		if inv.DueOn != nil {
			if _, parseErr := time.Parse("2006-01-02", *inv.DueOn); parseErr != nil {
				return ImportResult{}, errors.New("receivable_due_date_invalid")
			}
		}
		var oldCustomer string
		var oldCaptured *time.Time
		lookupErr := tx.QueryRow(ctx, `SELECT customer_id,last_captured_at FROM servicing.receivables WHERE mode='DEMO' AND source_system=$1 AND invoice_id=$2`, inv.SourceSystem, inv.InvoiceID).Scan(&oldCustomer, &oldCaptured)
		if lookupErr == nil && (oldCustomer != inv.CustomerID || (oldCaptured != nil && input.CapturedAt.Before(*oldCaptured))) {
			return ImportResult{}, errors.New("receivable_source_binding_or_version_conflict")
		}
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return ImportResult{}, errors.New("receivable_read_failed")
		}
		var due any
		if inv.DueOn != nil {
			due = *inv.DueOn
		}
		if _, err = tx.Exec(ctx, `INSERT INTO servicing.receivables(mode,customer_id,source_system,invoice_id,source_revision,currency,original_cents,unpaid_cents,due_on,due_date_verified,allocation_complete,disputed,source_complete,payer_id,source_ref,last_captured_at,backing_hold)
			VALUES('DEMO',$1,$2,$3,$4,'USD',$5,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,false)
			ON CONFLICT(mode,source_system,invoice_id) DO UPDATE SET source_revision=EXCLUDED.source_revision,unpaid_cents=EXCLUDED.unpaid_cents,due_on=EXCLUDED.due_on,due_date_verified=EXCLUDED.due_date_verified,allocation_complete=EXCLUDED.allocation_complete,disputed=EXCLUDED.disputed,source_complete=EXCLUDED.source_complete,payer_id=EXCLUDED.payer_id,source_ref=EXCLUDED.source_ref,last_captured_at=EXCLUDED.last_captured_at,backing_hold=(servicing.receivables.reserved_cents>EXCLUDED.unpaid_cents),version=servicing.receivables.version+1,imported_at=clock_timestamp() WHERE servicing.receivables.customer_id=EXCLUDED.customer_id AND (servicing.receivables.last_captured_at IS NULL OR servicing.receivables.last_captured_at<=EXCLUDED.last_captured_at)`, inv.CustomerID, inv.SourceSystem, inv.InvoiceID, input.SourceVersion, inv.EligibleUnpaidCents, due, inv.DueDateVerified, inv.AllocationComplete, inv.Disputed, inv.SourceComplete, inv.PayerID, mustJSON(inv.SourceRef), input.CapturedAt); err != nil {
			return ImportResult{}, errors.New("receivable_write_failed")
		}
	}
	rows, err := tx.Query(ctx, `WITH ranked AS (
		SELECT a.mode,a.bridge_id,a.active_cents,
			LEAST(a.active_cents,GREATEST(0,
				SUM(a.active_cents) OVER (PARTITION BY a.mode,a.source_system,a.invoice_id
					ORDER BY b.created_at,b.id,a.allocation_index ROWS UNBOUNDED PRECEDING)
				-r.unpaid_cents)) AS deficit_cents,
			(a.active_cents>0 AND (r.disputed OR NOT r.source_complete OR NOT r.allocation_complete OR NOT r.due_date_verified)) AS evidence_hold
		FROM servicing.bridge_invoice_allocations a
		JOIN servicing.bridges b ON b.mode=a.mode AND b.id=a.bridge_id
		JOIN servicing.receivables r ON r.mode=a.mode AND r.source_system=a.source_system AND r.invoice_id=a.invoice_id
		WHERE a.mode='DEMO' AND b.facility_id=$1 AND b.lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD')
	), totals AS (
		SELECT mode,bridge_id,sum(deficit_cents)::numeric assigned_deficit_cents,
			sum(active_cents)::numeric total_active_cents,bool_or(evidence_hold) AS evidence_hold
		FROM ranked GROUP BY mode,bridge_id
	), projected AS (
		SELECT b.mode,b.id AS bridge_id,
			(COALESCE(t.assigned_deficit_cents,0) + CASE WHEN b.ever_funded THEN
				GREATEST(0::numeric,b.principal_owed_cents::numeric+
				b.contract_fee_owed_cents::numeric-COALESCE(t.total_active_cents,0))
			ELSE 0::numeric END)::bigint AS deficit_cents,
			COALESCE(t.evidence_hold,false) AS evidence_hold
		FROM servicing.bridges b LEFT JOIN totals t ON t.mode=b.mode AND t.bridge_id=b.id
		WHERE b.mode='DEMO' AND b.facility_id=$1
		AND b.lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD')
	)
	UPDATE servicing.bridges b SET
		backing_deficit_cents=t.deficit_cents,
		hold_reasons=array_remove(b.hold_reasons,'RECEIVABLE_BACKING_CONFLICT') ||
			CASE WHEN t.deficit_cents>0 OR t.evidence_hold THEN ARRAY['RECEIVABLE_BACKING_CONFLICT']::text[] ELSE '{}'::text[] END,
		version=b.version+1,updated_at=clock_timestamp()
	FROM projected t WHERE b.mode=t.mode AND b.id=t.bridge_id AND b.facility_id=$1
		AND (b.backing_deficit_cents IS DISTINCT FROM t.deficit_cents OR
		(('RECEIVABLE_BACKING_CONFLICT'=ANY(b.hold_reasons)) IS DISTINCT FROM
		(t.deficit_cents>0 OR t.evidence_hold)))
	RETURNING b.id::text,b.version,b.customer_id,b.originating_case_id,b.evaluation_id,b.offer_id::text,b.terms_hash,b.backing_deficit_cents`, facilityID)
	if err != nil {
		return ImportResult{}, errors.New("receivable_backing_reconcile_failed")
	}
	type bridgeChange struct {
		id         string
		version    int64
		customer   string
		caseID     *string
		evaluation string
		offer      string
		terms      string
		deficit    int64
	}
	var changes []bridgeChange
	for rows.Next() {
		var c bridgeChange
		if err := rows.Scan(&c.id, &c.version, &c.customer, &c.caseID, &c.evaluation, &c.offer, &c.terms, &c.deficit); err != nil {
			rows.Close()
			return ImportResult{}, errors.New("receivable_backing_reconcile_failed")
		}
		changes = append(changes, c)
	}
	if rows.Err() != nil {
		rows.Close()
		return ImportResult{}, errors.New("receivable_backing_reconcile_failed")
	}
	rows.Close()
	for _, c := range changes {
		summary, _ := json.Marshal(map[string]any{"customer_id": c.customer, "account_ref": "customer:" + c.customer, "originating_case_id": c.caseID, "evaluation_id": c.evaluation, "offer_id": c.offer, "bridge_id": c.id, "bridge_version": c.version, "backing_deficit_cents": c.deficit})
		bridgeID, version := c.id, c.version
		if _, err := AppendChange(ctx, tx, domain.Change{Mode: domain.ModeDemo, ResourceType: "BRIDGE", ResourceID: c.id, ResourceVersion: c.version, ChangeType: "RECEIVABLE_BACKING_RECONCILED", Summary: summary}, &bridgeID, &version); err != nil {
			return ImportResult{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ImportResult{}, errors.New("import_commit_failed")
	}
	return ImportResult{Records: len(input.Receivables)}, nil
}

// ImportActors publishes fixture authentication bindings only. It does not
// create acceptance or approval decisions.
func (r *Repository) ImportActors(ctx context.Context, input domain.ActorsImport, facilityID string, now time.Time) (ImportResult, error) {
	if err := validateMetadata(input.ImportMetadata, "DEMO", now); err != nil || len(input.Actors) == 0 || len(input.Actors) > 10000 {
		return ImportResult{}, errors.New("actors_import_invalid")
	}
	hash, canonical, err := canonicalHash(input)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockImportScope(ctx, tx, facilityID); err != nil {
		return ImportResult{}, err
	}
	replay, err := recordImport(ctx, tx, "actors", input.SourceID, input.ImportMetadata, hash, canonical)
	if err != nil {
		return ImportResult{}, err
	}
	if replay {
		if err = tx.Commit(ctx); err != nil {
			return ImportResult{}, errors.New("import_commit_failed")
		}
		return ImportResult{Replayed: true, Records: len(input.Actors)}, nil
	}
	seen := map[string]bool{}
	for _, a := range input.Actors {
		if a.ActorID == "" || a.AuthenticationSubject == "" || !validSourceRef(a.SourceRef) || seen[a.ActorID] || seen[a.AuthenticationSubject] {
			return ImportResult{}, errors.New("actor_invalid")
		}
		seen[a.ActorID] = true
		seen[a.AuthenticationSubject] = true
		switch a.ActorType {
		case "CUSTOMER":
			if a.CustomerID == nil || *a.CustomerID == "" {
				return ImportResult{}, errors.New("actor_customer_invalid")
			}
			var mapped bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.customer_roster WHERE customer_id=$1 AND is_active)`, *a.CustomerID).Scan(&mapped); err != nil || !mapped {
				return ImportResult{}, errors.New("actor_customer_unmapped")
			}
		case "OPERATOR":
			if a.CustomerID != nil {
				return ImportResult{}, errors.New("actor_operator_scope_invalid")
			}
		case "SYSTEM":
			if a.CustomerID != nil || !strings.HasPrefix(a.AuthenticationSubject, "system:") {
				return ImportResult{}, errors.New("actor_system_subject_invalid")
			}
		default:
			return ImportResult{}, errors.New("actor_type_invalid")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO servicing.actors(mode,actor_id,customer_id,actor_type,authentication_subject,active,source_ref,captured_at,source_id,source_version)
			VALUES('DEMO',$1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)
			ON CONFLICT(mode,actor_id) DO UPDATE SET customer_id=EXCLUDED.customer_id,actor_type=EXCLUDED.actor_type,authentication_subject=EXCLUDED.authentication_subject,active=EXCLUDED.active,source_ref=EXCLUDED.source_ref,captured_at=EXCLUDED.captured_at,source_id=EXCLUDED.source_id,source_version=EXCLUDED.source_version`, a.ActorID, a.CustomerID, a.ActorType, a.AuthenticationSubject, a.Active, mustJSON(a.SourceRef), input.CapturedAt, input.SourceID, input.SourceVersion); err != nil {
			return ImportResult{}, errors.New("actor_write_failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ImportResult{}, errors.New("import_commit_failed")
	}
	return ImportResult{Records: len(input.Actors)}, nil
}

// ImportAuthorities publishes DEMO mandate fixture facts with zero initial
// usage; existing consumption is derived from persisted operations.
func (r *Repository) ImportAuthorities(ctx context.Context, input domain.AuthoritiesImport, facilityID string, now time.Time) (ImportResult, error) {
	if err := validateMetadata(input.ImportMetadata, "DEMO", now); err != nil || len(input.Authorities) == 0 || len(input.Authorities) > 10000 {
		return ImportResult{}, errors.New("authority_import_invalid")
	}
	hash, canonical, err := canonicalHash(input)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockImportScope(ctx, tx, facilityID); err != nil {
		return ImportResult{}, err
	}
	var sourceA string
	if err = tx.QueryRow(ctx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode='DEMO' AND id=$1 FOR UPDATE`, facilityID).Scan(&sourceA); err != nil {
		return ImportResult{}, errors.New("authority_facility_unavailable")
	}
	changedCustomers := map[string]bool{}
	seenCustomers := map[string]bool{}
	customerIDs := make([]string, 0, len(input.Authorities))
	for _, a := range input.Authorities {
		if a.CustomerID == "" {
			return ImportResult{}, errors.New("authority_record_invalid")
		}
		if !seenCustomers[a.CustomerID] {
			seenCustomers[a.CustomerID] = true
			customerIDs = append(customerIDs, a.CustomerID)
		}
	}
	sort.Strings(customerIDs)
	for _, customerID := range customerIDs {
		var id string
		if err = tx.QueryRow(ctx, `SELECT id FROM servicing.customers WHERE mode='DEMO' AND id=$1 FOR UPDATE`, customerID).Scan(&id); err != nil {
			return ImportResult{}, errors.New("authority_customer_lock_failed")
		}
	}
	bridgeSet := map[string]bool{}
	for _, customerID := range customerIDs {
		rows, e := tx.Query(ctx, `SELECT id::text FROM servicing.bridges WHERE mode='DEMO' AND customer_id=$1 AND lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD')`, customerID)
		if e != nil {
			return ImportResult{}, errors.New("authority_bridge_lookup_failed")
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return ImportResult{}, errors.New("authority_bridge_lookup_failed")
			}
			bridgeSet[id] = true
		}
		if rows.Err() != nil {
			rows.Close()
			return ImportResult{}, errors.New("authority_bridge_lookup_failed")
		}
		rows.Close()
	}
	bridgeIDs := make([]string, 0, len(bridgeSet))
	for id := range bridgeSet {
		bridgeIDs = append(bridgeIDs, id)
	}
	sort.Strings(bridgeIDs)
	for _, id := range bridgeIDs {
		var locked string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM servicing.bridges WHERE mode='DEMO' AND id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return ImportResult{}, errors.New("authority_bridge_lock_failed")
		}
	}
	replay, err := recordImport(ctx, tx, "authority", input.SourceID, input.ImportMetadata, hash, canonical)
	if err != nil {
		return ImportResult{}, err
	}
	if replay {
		if err = tx.Commit(ctx); err != nil {
			return ImportResult{}, errors.New("import_commit_failed")
		}
		return ImportResult{Replayed: true, Records: len(input.Authorities)}, nil
	}
	seen := map[string]bool{}
	for _, a := range input.Authorities {
		validEvidence := len(a.EvidenceRefs) > 0 && len(a.EvidenceRefs) <= 10000
		mandateEvidence := false
		for _, ref := range a.EvidenceRefs {
			if !validSourceRef(ref) {
				validEvidence = false
			}
			if ref.RecordID == a.MandateReference {
				mandateEvidence = true
			}
		}
		if a.ID == "" || a.Version <= 0 || a.CustomerID == "" || a.SourceAccountRef == a.DestinationAccountRef || a.SourceAccountRef == "" || a.DestinationAccountRef != sourceA || a.ProductCode != "CONNECTED_ACCOUNT_TRANSFERS" || a.MandateReference == "" || !mandateEvidence || a.Currency != "USD" || a.MaximumDebitCents <= 0 || a.ConsumedDebitCents != 0 || a.ReservedDebitCents != 0 || !a.ValidFrom.Before(a.ExpiresAt) || a.ValidFrom.After(input.CapturedAt.Add(24*time.Hour)) || a.DisclosuresVersion == "" || !validEvidence || len(a.PermittedKinds) == 0 || len(a.PermittedPurposes) == 0 || seen[a.ID] {
			return ImportResult{}, errors.New("authority_record_invalid")
		}
		seen[a.ID] = true
		var borrowerAccount string
		if err = tx.QueryRow(ctx, `SELECT airwallex_connected_account_id FROM public.customer_roster WHERE customer_id=$1 AND is_active`, a.CustomerID).Scan(&borrowerAccount); err != nil || borrowerAccount != a.SourceAccountRef {
			return ImportResult{}, errors.New("authority_borrower_mapping_invalid")
		}
		for _, kind := range a.PermittedKinds {
			if kind != "COLLECT_FEE" && kind != "COLLECT_PRINCIPAL" {
				return ImportResult{}, errors.New("authority_kind_invalid")
			}
		}
		validPurposes := map[string]bool{"professional_business_services": false, "loan_credit_repayment": false}
		for _, purpose := range a.PermittedPurposes {
			if _, ok := validPurposes[purpose]; !ok || validPurposes[purpose] {
				return ImportResult{}, errors.New("authority_purpose_invalid")
			}
			validPurposes[purpose] = true
		}
		for _, kind := range a.PermittedKinds {
			purpose := "loan_credit_repayment"
			if kind == "COLLECT_FEE" {
				purpose = "professional_business_services"
			}
			if !validPurposes[purpose] {
				return ImportResult{}, errors.New("authority_purpose_missing")
			}
		}
		var latest int64
		var logicalCustomer, logicalSource, logicalDestination string
		err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0),COALESCE(max(customer_id),''),COALESCE(max(source_account_ref),''),COALESCE(max(destination_account_ref),'') FROM servicing.authorities WHERE mode='DEMO' AND id=$1`, a.ID).Scan(&latest, &logicalCustomer, &logicalSource, &logicalDestination)
		if err != nil {
			return ImportResult{}, errors.New("authority_version_read_failed")
		}
		if latest > 0 && (logicalCustomer != a.CustomerID || logicalSource != a.SourceAccountRef || logicalDestination != a.DestinationAccountRef) {
			return ImportResult{}, errors.New("authority_binding_immutable")
		}
		if int64(a.Version) < latest {
			return ImportResult{}, errors.New("authority_version_stale")
		}
		var oldCustomer, oldSource, oldDestination, oldCurrency, oldStatus, oldDisclosure string
		var oldProduct, oldMandate string
		var oldKinds []string
		var oldPurposes []string
		var oldLimit int64
		var oldFrom, oldUntil time.Time
		var evidenceMatches bool
		err = tx.QueryRow(ctx, `SELECT customer_id,source_account_ref,destination_account_ref,COALESCE(product_code,''),COALESCE(mandate_reference,''),currency,permitted_kinds,permitted_purposes,maximum_debit_cents,status,disclosures_version,valid_from,expires_at,evidence_refs=$3::jsonb FROM servicing.authorities WHERE mode='DEMO' AND id=$1 AND version=$2`, a.ID, a.Version, mustJSON(a.EvidenceRefs)).Scan(&oldCustomer, &oldSource, &oldDestination, &oldProduct, &oldMandate, &oldCurrency, &oldKinds, &oldPurposes, &oldLimit, &oldStatus, &oldDisclosure, &oldFrom, &oldUntil, &evidenceMatches)
		if err == nil {
			if oldCustomer != a.CustomerID || oldSource != a.SourceAccountRef || oldDestination != a.DestinationAccountRef || oldProduct != a.ProductCode || oldMandate != a.MandateReference || oldCurrency != a.Currency || strings.Join(oldKinds, "|") != strings.Join(a.PermittedKinds, "|") || strings.Join(oldPurposes, "|") != strings.Join(a.PermittedPurposes, "|") || oldLimit != a.MaximumDebitCents || oldStatus != a.Status || oldDisclosure != a.DisclosuresVersion || !oldFrom.Equal(a.ValidFrom) || !oldUntil.Equal(a.ExpiresAt) || !evidenceMatches {
				return ImportResult{}, errors.New("authority_version_conflict")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) || int64(a.Version) != latest+1 {
			return ImportResult{}, errors.New("authority_version_conflict")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO servicing.authorities(mode,id,version,customer_id,source_account_ref,destination_account_ref,product_code,mandate_reference,permitted_purposes,currency,permitted_kinds,maximum_debit_cents,consumed_debit_cents,reserved_debit_cents,valid_from,expires_at,status,disclosures_version,evidence_refs,captured_at,source_id,source_version,source_captured_at)
			VALUES('DEMO',$1,$2,$3,$4,$5,$6,$7,$8,'USD',$9,$10,0,0,$11,$12,$13,$14,$15::jsonb,$16,$17,$18,$16)
			ON CONFLICT(mode,id,version) DO NOTHING`, a.ID, a.Version, a.CustomerID, a.SourceAccountRef, a.DestinationAccountRef, a.ProductCode, a.MandateReference, a.PermittedPurposes, a.PermittedKinds, a.MaximumDebitCents, a.ValidFrom, a.ExpiresAt, a.Status, a.DisclosuresVersion, mustJSON(a.EvidenceRefs), input.CapturedAt, input.SourceID, input.SourceVersion); err != nil {
			return ImportResult{}, errors.New("authority_write_failed")
		}
		changedCustomers[a.CustomerID] = true
	}
	for _, bridgeID := range bridgeIDs {
		var customerID string
		if err = tx.QueryRow(ctx, `SELECT customer_id FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, bridgeID).Scan(&customerID); err != nil {
			return ImportResult{}, errors.New("authority_bridge_read_failed")
		}
		if !changedCustomers[customerID] {
			continue
		}
		var caseID, evaluationID, offerID string
		var version int64
		if err = tx.QueryRow(ctx, `UPDATE servicing.bridges SET version=version+1,updated_at=clock_timestamp() WHERE mode='DEMO' AND id=$1 RETURNING version,COALESCE(originating_case_id,''),evaluation_id,offer_id::text`, bridgeID).Scan(&version, &caseID, &evaluationID, &offerID); err != nil {
			return ImportResult{}, errors.New("authority_bridge_update_failed")
		}
		var caseRef any
		if caseID != "" {
			caseRef = caseID
		}
		summary, _ := json.Marshal(map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID, "originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": offerID, "bridge_id": bridgeID, "bridge_version": version})
		parentID := bridgeID
		if _, err = AppendChange(ctx, tx, domain.Change{Mode: domain.ModeDemo, ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: version, ChangeType: "AUTHORITY_CHANGED", Summary: summary}, &parentID, &version); err != nil {
			return ImportResult{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ImportResult{}, errors.New("import_commit_failed")
	}
	return ImportResult{Records: len(input.Authorities)}, nil
}

func mustJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
