CREATE TABLE servicing.import_journal (
    mode text NOT NULL CHECK (mode='DEMO'),
    kind text NOT NULL CHECK (kind IN ('facility','receivables','authority','actors')),
    source_id text NOT NULL,
    source_version text NOT NULL,
    owner_id text NOT NULL,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    canonical_input jsonb NOT NULL CHECK (jsonb_typeof(canonical_input)='object'),
    captured_at timestamptz NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs)='array'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(mode,kind,source_id,source_version,owner_id)
);
CREATE INDEX import_journal_latest ON servicing.import_journal
    (mode,kind,owner_id,captured_at DESC,source_version DESC);
REVOKE ALL ON servicing.import_journal FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.import_journal TO servicing_runtime;
CREATE FUNCTION servicing.reject_import_journal_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN RAISE EXCEPTION 'import journal is append only'; END
$$;
CREATE TRIGGER import_journal_append_only BEFORE UPDATE OR DELETE ON servicing.import_journal
FOR EACH ROW EXECUTE FUNCTION servicing.reject_import_journal_mutation();

ALTER TABLE servicing.facilities ADD COLUMN source_id text;
ALTER TABLE servicing.facilities ADD COLUMN source_version text;
ALTER TABLE servicing.facilities ADD COLUMN captured_at timestamptz;
ALTER TABLE servicing.facilities ADD COLUMN source_refs jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE servicing.receivables ADD COLUMN payer_id text NOT NULL DEFAULT '';
ALTER TABLE servicing.receivables ADD COLUMN source_ref jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE servicing.receivables ADD COLUMN last_captured_at timestamptz;
ALTER TABLE servicing.receivables ADD COLUMN backing_hold boolean NOT NULL DEFAULT false;
DO $$
DECLARE constraint_row record;
BEGIN
    FOR constraint_row IN
        SELECT conname,pg_get_constraintdef(oid) AS definition
          FROM pg_constraint WHERE conrelid='servicing.receivables'::regclass AND contype='c'
    LOOP
        IF position('original_cents > 0' IN constraint_row.definition)>0
           OR position('unpaid_cents <= original_cents' IN constraint_row.definition)>0
           OR position('reserved_cents <= unpaid_cents' IN constraint_row.definition)>0 THEN
            EXECUTE format('ALTER TABLE servicing.receivables DROP CONSTRAINT %I',constraint_row.conname);
        END IF;
    END LOOP;
END
$$;
ALTER TABLE servicing.receivables ADD COLUMN backing_deficit_cents bigint
    GENERATED ALWAYS AS (GREATEST(reserved_cents-unpaid_cents,0)) STORED;
ALTER TABLE servicing.receivables ADD CONSTRAINT receivables_backing_hold_check
    CHECK (backing_deficit_cents=0 OR backing_hold);
ALTER TABLE servicing.receivables ADD CONSTRAINT receivables_original_nonnegative CHECK (original_cents>=0);
ALTER TABLE servicing.actors ADD COLUMN captured_at timestamptz;
ALTER TABLE servicing.actors ADD COLUMN source_id text;
ALTER TABLE servicing.actors ADD COLUMN source_version text;
ALTER TABLE servicing.authorities ADD COLUMN source_id text;
ALTER TABLE servicing.authorities ADD COLUMN source_version text;
ALTER TABLE servicing.authorities ADD COLUMN source_captured_at timestamptz;

GRANT UPDATE (source_id,source_version,captured_at,source_refs,total_cents,reserve_bps)
    ON servicing.facilities TO servicing_runtime;
GRANT UPDATE (history_complete) ON servicing.customers TO servicing_runtime;
GRANT UPDATE (original_cents,unpaid_cents,due_on,due_date_verified,
    allocation_complete,disputed,source_complete,source_revision,version,
    imported_at,payer_id,source_ref,last_captured_at,backing_hold)
    ON servicing.receivables TO servicing_runtime;
GRANT UPDATE (customer_id,actor_type,authentication_subject,active,source_ref,
    captured_at,source_id,source_version) ON servicing.actors TO servicing_runtime;
GRANT UPDATE (consumed_debit_cents,reserved_debit_cents,status)
    ON servicing.authorities TO servicing_runtime;

CREATE FUNCTION servicing.guard_authority_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.version,NEW.customer_id,NEW.source_account_ref,
        NEW.destination_account_ref,NEW.currency,NEW.permitted_kinds,
        NEW.maximum_debit_cents,NEW.valid_from,NEW.expires_at,
        NEW.disclosures_version,NEW.evidence_refs,NEW.captured_at,NEW.source_id,
        NEW.source_version,NEW.source_captured_at)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.version,OLD.customer_id,OLD.source_account_ref,
        OLD.destination_account_ref,OLD.currency,OLD.permitted_kinds,
        OLD.maximum_debit_cents,OLD.valid_from,OLD.expires_at,
        OLD.disclosures_version,OLD.evidence_refs,OLD.captured_at,OLD.source_id,
        OLD.source_version,OLD.source_captured_at)
       OR NEW.consumed_debit_cents < OLD.consumed_debit_cents
       OR (OLD.status='REVOKED' AND NEW.status<>'REVOKED') THEN
        RAISE EXCEPTION 'authority facts or usage are immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER authorities_immutable BEFORE UPDATE ON servicing.authorities
FOR EACH ROW EXECUTE FUNCTION servicing.guard_authority_update();
