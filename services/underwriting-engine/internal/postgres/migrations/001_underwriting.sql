-- Run only through cmd/migrate with a privileged connection.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'underwriting_runtime') THEN
        CREATE ROLE underwriting_runtime NOLOGIN NOSUPERUSER NOCREATEDB
            NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_roles
        WHERE rolname = 'underwriting_runtime'
          AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole
               OR rolreplication OR rolbypassrls)
    ) OR EXISTS (
        SELECT 1 FROM pg_auth_members
        WHERE member = (SELECT oid FROM pg_roles WHERE rolname = 'underwriting_runtime')
    ) THEN
        RAISE EXCEPTION 'underwriting_runtime must be a restricted NOLOGIN role';
    END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS underwriting;
REVOKE ALL ON SCHEMA underwriting FROM PUBLIC;

CREATE TABLE IF NOT EXISTS underwriting.schema_migrations (
    version integer PRIMARY KEY CHECK (version > 0),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE underwriting.customer_snapshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_mode text NOT NULL CHECK (source_mode IN ('DEMO', 'SANDBOX')),
    customer_id text NOT NULL CHECK (length(btrim(customer_id)) > 0),
    account_id text NOT NULL CHECK (length(btrim(account_id)) > 0),
    card_id text NOT NULL CHECK (length(btrim(card_id)) > 0),
    version bigint NOT NULL CHECK (version > 0),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    source_id text NOT NULL CHECK (length(btrim(source_id)) > 0),
    source_version text NOT NULL CHECK (length(btrim(source_version)) > 0),
    captured_at timestamptz NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs) = 'array'),
    coverage jsonb NOT NULL CHECK (jsonb_typeof(coverage) = 'object'),
    snapshot_json jsonb NOT NULL CHECK (jsonb_typeof(snapshot_json) = 'object'),
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_mode, customer_id, version),
    UNIQUE (source_mode, customer_id, id, version),
    UNIQUE (source_mode, customer_id, source_id, source_version)
);
CREATE INDEX customer_snapshots_current
    ON underwriting.customer_snapshots (source_mode, customer_id, captured_at DESC, version DESC);

CREATE TABLE underwriting.facility_snapshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_mode text NOT NULL CHECK (source_mode IN ('DEMO', 'SANDBOX')),
    facility_id text NOT NULL CHECK (length(btrim(facility_id)) > 0),
    version bigint NOT NULL CHECK (version > 0),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    source_id text NOT NULL CHECK (length(btrim(source_id)) > 0),
    source_version text NOT NULL CHECK (length(btrim(source_version)) > 0),
    captured_at timestamptz NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs) = 'array'),
    ledger_version text NOT NULL CHECK (length(btrim(ledger_version)) > 0),
    ledger_complete boolean NOT NULL,
    facility_total_cents bigint NOT NULL CHECK (facility_total_cents > 0),
    reserve_floor_bps integer NOT NULL CHECK (reserve_floor_bps BETWEEN 0 AND 10000),
    funded_principal_cents bigint NOT NULL CHECK (funded_principal_cents >= 0),
    reserved_principal_cents bigint NOT NULL CHECK (reserved_principal_cents >= 0),
    snapshot_json jsonb NOT NULL CHECK (jsonb_typeof(snapshot_json) = 'object'),
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_mode, facility_id, version),
    UNIQUE (source_mode, id, version),
    UNIQUE (source_mode, facility_id, source_id, source_version)
);
CREATE INDEX facility_snapshots_current
    ON underwriting.facility_snapshots (source_mode, facility_id, captured_at DESC, version DESC);

CREATE TABLE underwriting.evaluations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_mode text NOT NULL CHECK (source_mode IN ('DEMO', 'SANDBOX')),
    caller text NOT NULL CHECK (length(btrim(caller)) > 0),
    customer_id text NOT NULL CHECK (length(btrim(customer_id)) > 0),
    shortfall_id uuid NOT NULL,
    idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    request_json jsonb NOT NULL CHECK (jsonb_typeof(request_json) = 'object'),
    shortfall_json jsonb NOT NULL CHECK (jsonb_typeof(shortfall_json) = 'object'),
    customer_snapshot_id uuid,
    customer_snapshot_version bigint CHECK (customer_snapshot_version > 0),
    facility_snapshot_id uuid,
    facility_snapshot_version bigint CHECK (facility_snapshot_version > 0),
    input_facts jsonb NOT NULL CHECK (jsonb_typeof(input_facts) = 'object'),
    evaluated_at timestamptz NOT NULL,
    policy_id text NOT NULL CHECK (length(btrim(policy_id)) > 0),
    policy_hash text NOT NULL CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    result_json jsonb NOT NULL CHECK (jsonb_typeof(result_json) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_mode, caller, idempotency_key),
    FOREIGN KEY (source_mode, customer_id, customer_snapshot_id, customer_snapshot_version)
        REFERENCES underwriting.customer_snapshots(source_mode, customer_id, id, version)
        ON DELETE RESTRICT,
    FOREIGN KEY (source_mode, facility_snapshot_id, facility_snapshot_version)
        REFERENCES underwriting.facility_snapshots(source_mode, id, version)
        ON DELETE RESTRICT,
    CHECK ((customer_snapshot_id IS NULL) = (customer_snapshot_version IS NULL)),
    CHECK ((facility_snapshot_id IS NULL) = (facility_snapshot_version IS NULL))
);
CREATE INDEX evaluations_customer_cursor
    ON underwriting.evaluations (source_mode, customer_id, created_at DESC, id DESC);

CREATE FUNCTION underwriting.reject_immutable_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'underwriting rows are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER customer_snapshots_immutable
    BEFORE UPDATE OR DELETE ON underwriting.customer_snapshots
    FOR EACH ROW EXECUTE FUNCTION underwriting.reject_immutable_mutation();
CREATE TRIGGER facility_snapshots_immutable
    BEFORE UPDATE OR DELETE ON underwriting.facility_snapshots
    FOR EACH ROW EXECUTE FUNCTION underwriting.reject_immutable_mutation();
CREATE TRIGGER evaluations_immutable
    BEFORE UPDATE OR DELETE ON underwriting.evaluations
    FOR EACH ROW EXECUTE FUNCTION underwriting.reject_immutable_mutation();

REVOKE ALL ON ALL TABLES IN SCHEMA underwriting FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA underwriting FROM underwriting_runtime;
GRANT USAGE ON SCHEMA underwriting, public TO underwriting_runtime;
GRANT SELECT ON underwriting.schema_migrations TO underwriting_runtime;
GRANT SELECT, INSERT ON underwriting.customer_snapshots,
    underwriting.facility_snapshots, underwriting.evaluations TO underwriting_runtime;
GRANT SELECT ON public.customer_roster, public.issuing_card_bindings TO underwriting_runtime;

DO $$
BEGIN
    IF has_schema_privilege('underwriting_runtime', 'underwriting', 'CREATE')
       OR has_schema_privilege('underwriting_runtime', 'public', 'CREATE')
       OR has_database_privilege('underwriting_runtime', current_database(), 'CREATE')
       OR has_table_privilege('underwriting_runtime', 'underwriting.schema_migrations',
                             'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('underwriting_runtime', 'public.customer_roster',
                             'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('underwriting_runtime', 'public.issuing_card_bindings',
                             'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('underwriting_runtime', 'underwriting.customer_snapshots',
                             'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('underwriting_runtime', 'underwriting.facility_snapshots',
                             'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('underwriting_runtime', 'underwriting.evaluations',
                             'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') THEN
        RAISE EXCEPTION 'underwriting runtime inherits unsafe grants';
    END IF;
END
$$;
