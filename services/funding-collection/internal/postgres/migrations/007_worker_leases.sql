-- Replace only the known original operation lease CHECK. If the installed
-- schema does not contain exactly one matching definition, fail closed.
DO $$
DECLARE lease_check_names text[];
BEGIN
    SELECT array_agg(conname ORDER BY conname)
      INTO lease_check_names
      FROM (
        SELECT conname,
            regexp_replace(
                regexp_replace(lower(pg_get_constraintdef(oid)), '[[:space:]()]', '', 'g'),
                '::text', '', 'g') AS normalized
          FROM pg_constraint
         WHERE conrelid = 'servicing.operations'::regclass
           AND contype = 'c'
      ) operation_checks
     WHERE normalized = 'checkstate=''dispatching''=lease_tokenisnotnullandlease_expires_atisnotnull';

    IF lease_check_names IS NULL OR cardinality(lease_check_names) <> 1 THEN
        RAISE EXCEPTION 'expected one original operations lease CHECK, found %',
            COALESCE(cardinality(lease_check_names), 0);
    END IF;

    EXECUTE format('ALTER TABLE servicing.operations DROP CONSTRAINT %I', lease_check_names[1]);
END
$$;

ALTER TABLE servicing.operations
    ADD CONSTRAINT operations_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    ADD CONSTRAINT operations_dispatch_requires_lease_check
        CHECK (state <> 'DISPATCHING' OR lease_token IS NOT NULL),
    ADD CONSTRAINT operations_lease_claimable_state_check
        CHECK (lease_token IS NULL OR state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD'));

CREATE UNIQUE INDEX operations_one_fund_per_bridge
    ON servicing.operations(mode,bridge_id) WHERE kind='FUND';

ALTER TABLE servicing.operations
    ADD COLUMN lookup_requested_at timestamptz,
    ADD COLUMN lookup_requested_by text,
    ADD CONSTRAINT operations_lookup_request_pair_check
        CHECK ((lookup_requested_at IS NULL) = (lookup_requested_by IS NULL)),
    ADD CONSTRAINT operations_lookup_request_held_check
        CHECK (lookup_requested_at IS NULL OR state='HELD');
GRANT UPDATE (lookup_requested_at,lookup_requested_by) ON servicing.operations TO servicing_runtime;

CREATE TABLE servicing.operation_journal (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    operation_id uuid NOT NULL,
    sequence integer NOT NULL CHECK (sequence > 0),
    entry_id uuid NOT NULL DEFAULT gen_random_uuid(),
    event_type text NOT NULL CHECK (event_type IN (
        'INTENT_CREATED','CLAIMED','LEASE_EXPIRED_UNKNOWN','ATTEMPT_STARTED',
        'OBSERVATION_RECORDED','STATE_TRANSITION','LOOKUP_REQUESTED')),
    from_state text CHECK (from_state IS NULL OR from_state IN (
        'QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD','SETTLED','FAILED_CONFIRMED','CANCELLED_UNSENT')),
    to_state text CHECK (to_state IS NULL OR to_state IN (
        'QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD','SETTLED','FAILED_CONFIRMED','CANCELLED_UNSENT')),
    lease_token uuid,
    lease_expires_at timestamptz,
    sanitized_code text CHECK (sanitized_code IS NULL OR sanitized_code ~ '^[A-Z0-9_:-]{1,96}$'),
    provider_observation_id uuid,
    evidence_ref jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence_ref)='object'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,operation_id,sequence),
    UNIQUE (mode,entry_id),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id),
    FOREIGN KEY (mode,provider_observation_id) REFERENCES servicing.provider_observations(mode,id),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
);
CREATE INDEX operation_journal_order ON servicing.operation_journal(mode,operation_id,sequence);

CREATE FUNCTION servicing.reject_operation_journal_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'operation journal is append only';
END
$$;
CREATE TRIGGER operation_journal_immutable BEFORE UPDATE OR DELETE
ON servicing.operation_journal FOR EACH ROW
EXECUTE FUNCTION servicing.reject_operation_journal_mutation();

REVOKE ALL ON servicing.operation_journal FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.operation_journal TO servicing_runtime;
REVOKE UPDATE,DELETE,TRUNCATE ON servicing.operation_journal FROM servicing_runtime;

INSERT INTO servicing.operation_journal(mode,operation_id,sequence,event_type,to_state,evidence_ref)
SELECT mode,id,1,'INTENT_CREATED',state,
    jsonb_build_object('kind',kind,'request_id',request_id,'payload_hash',payload_hash,
        'bridge_id',bridge_id,'reservation_id',reservation_id)
  FROM servicing.operations
ON CONFLICT (mode,operation_id,sequence) DO NOTHING;
