-- Apply only through cmd/migrate with a privileged connection.
CREATE SCHEMA IF NOT EXISTS coordinator;
REVOKE ALL ON SCHEMA coordinator FROM PUBLIC;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'coordinator_runtime') THEN
        CREATE ROLE coordinator_runtime NOLOGIN NOSUPERUSER NOCREATEDB
            NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'coordinator_runtime'
          AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole
               OR rolreplication OR rolbypassrls)
    ) OR EXISTS (
        SELECT 1 FROM pg_auth_members
        WHERE member = (SELECT oid FROM pg_roles WHERE rolname = 'coordinator_runtime')
    ) THEN
        RAISE EXCEPTION 'coordinator_runtime must be a restricted NOLOGIN role';
    END IF;
END
$$;

CREATE TABLE coordinator.schema_migrations (
    version integer PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE coordinator.source_bindings (
    id text PRIMARY KEY CHECK (length(btrim(id)) BETWEEN 1 AND 128),
    mode text NOT NULL CHECK (mode IN ('DEMO', 'SANDBOX')),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    endpoint_identity text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE coordinator.discovery_progress (
    binding_id text PRIMARY KEY REFERENCES coordinator.source_bindings(id),
    shortfall_cursor text,
    scan_started_at timestamptz,
    scan_completed_at timestamptz,
    last_page_succeeded_at timestamptz,
    greatest_created_at timestamptz,
    greatest_detection_id text,
    servicing_sequence bigint NOT NULL DEFAULT 0 CHECK (servicing_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE coordinator.cases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    binding_id text NOT NULL REFERENCES coordinator.source_bindings(id),
    kind text NOT NULL CHECK (kind IN ('SHORTFALL', 'COLLECTION')),
    detection_id text,
    bridge_id text,
    customer_id text,
    account_ref text,
    status text NOT NULL DEFAULT 'NEW' CHECK (status IN
        ('NEW', 'INVESTIGATING', 'EVALUATING', 'COMPLETED', 'RETRY_WAIT', 'HELD')),
    next_stage text NOT NULL DEFAULT 'LOAD_DETAIL',
    retry_at timestamptz,
    lease_token uuid,
    lease_expires_at timestamptz,
    bridge_version bigint CHECK (bridge_version IS NULL OR bridge_version >= 0),
    refresh_needed boolean NOT NULL DEFAULT false,
    target_bridge_version bigint CHECK (target_bridge_version IS NULL OR target_bridge_version >= 0),
    last_change_sequence bigint CHECK (last_change_sequence IS NULL OR last_change_sequence > 0),
    held_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'INVESTIGATING' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'INVESTIGATING' AND lease_token IS NULL AND lease_expires_at IS NULL)),
    CHECK ((kind = 'SHORTFALL' AND detection_id IS NOT NULL)
        OR (kind = 'COLLECTION' AND bridge_id IS NOT NULL)),
    CHECK ((kind = 'SHORTFALL' AND bridge_id IS NULL)
        OR (kind = 'COLLECTION' AND detection_id IS NULL))
);
CREATE UNIQUE INDEX cases_detection_unique ON coordinator.cases(binding_id, detection_id)
    WHERE detection_id IS NOT NULL;
CREATE UNIQUE INDEX cases_bridge_unique ON coordinator.cases(binding_id, bridge_id)
    WHERE bridge_id IS NOT NULL;
CREATE INDEX cases_ready ON coordinator.cases(retry_at, created_at, id)
    WHERE status IN ('NEW', 'RETRY_WAIT');
CREATE INDEX cases_expired_leases ON coordinator.cases(lease_expires_at, id)
    WHERE status = 'INVESTIGATING';

CREATE TABLE coordinator.servicing_changes (
    binding_id text NOT NULL REFERENCES coordinator.source_bindings(id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    mode text NOT NULL CHECK (mode IN ('DEMO', 'SANDBOX')),
    resource_type text NOT NULL CHECK (resource_type IN ('OFFER', 'BRIDGE', 'OPERATION')),
    resource_id text NOT NULL,
    resource_version bigint NOT NULL CHECK (resource_version >= 0),
    bridge_id text,
    bridge_version bigint CHECK (bridge_version IS NULL OR bridge_version >= 0),
    change_type text NOT NULL,
    recorded_at timestamptz NOT NULL,
    summary jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
    PRIMARY KEY (binding_id, sequence),
    CHECK ((bridge_id IS NULL) = (bridge_version IS NULL))
);

CREATE TABLE coordinator.collection_observations (
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    bridge_version bigint NOT NULL CHECK (bridge_version >= 0),
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    resource_version bigint NOT NULL CHECK (resource_version >= 0),
    observed_at timestamptz NOT NULL,
    state jsonb NOT NULL CHECK (jsonb_typeof(state) = 'object'),
    PRIMARY KEY (case_id, bridge_version, resource_type, resource_id, resource_version)
);

CREATE TABLE coordinator.investigation_triggers (
    binding_id text NOT NULL REFERENCES coordinator.source_bindings(id),
    bridge_id text NOT NULL,
    bridge_version bigint NOT NULL CHECK (bridge_version >= 0),
    trigger_code text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (binding_id, bridge_id, bridge_version, trigger_code)
);

CREATE TABLE coordinator.runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    revision integer NOT NULL CHECK (revision > 0),
    stage text NOT NULL,
    prompt_version text NOT NULL,
    model_version text NOT NULL,
    context_refs jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(context_refs) = 'object'),
    disposition text,
    model_call_count integer NOT NULL DEFAULT 0 CHECK (model_call_count >= 0),
    tool_call_count integer NOT NULL DEFAULT 0 CHECK (tool_call_count >= 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (case_id, revision)
);

CREATE TABLE coordinator.steps (
    run_id uuid NOT NULL REFERENCES coordinator.runs(id),
    sequence integer NOT NULL CHECK (sequence > 0),
    kind text NOT NULL CHECK (kind IN ('MODEL', 'TOOL')),
    call_id text,
    request_hash bytea CHECK (request_hash IS NULL OR octet_length(request_hash) = 32),
    request_body bytea,
    result_body bytea,
    outcome text NOT NULL CHECK (outcome IN ('ATTEMPTED', 'DISPATCHED', 'COMPLETED', 'FAILED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (run_id, sequence),
    UNIQUE (run_id, call_id),
    CHECK ((outcome IN ('COMPLETED', 'FAILED')) = (completed_at IS NOT NULL))
);

CREATE TABLE coordinator.evidence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    run_id uuid NOT NULL REFERENCES coordinator.runs(id),
    source_handle text NOT NULL,
    source_version text NOT NULL,
    source_hash bytea NOT NULL CHECK (octet_length(source_hash) = 32),
    locator text NOT NULL,
    excerpt text NOT NULL,
    claim_field text NOT NULL,
    claim_value text NOT NULL,
    claim_unit text,
    claim_date date,
    uncertainty text,
    state text NOT NULL DEFAULT 'PENDING_REVIEW'
        CHECK (state IN ('PENDING_REVIEW', 'REJECTED', 'LINKED_TO_SNAPSHOT')),
    canonical_hash bytea NOT NULL CHECK (octet_length(canonical_hash) = 32),
    tool_call_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (case_id, run_id, canonical_hash),
    UNIQUE (run_id, tool_call_id)
);

CREATE TABLE coordinator.evidence_link_audits (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    evidence_id uuid NOT NULL REFERENCES coordinator.evidence(id),
    snapshot_id text NOT NULL,
    snapshot_version text NOT NULL,
    actor_id text NOT NULL,
    linked_at timestamptz NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs) = 'array'),
    UNIQUE (evidence_id, snapshot_id, snapshot_version)
);

CREATE TABLE coordinator.assessments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    revision integer NOT NULL CHECK (revision > 0),
    idempotency_key text NOT NULL,
    request_body bytea NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    saved_assessment_id text,
    response_body bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    saved_at timestamptz,
    UNIQUE (case_id, revision),
    UNIQUE (idempotency_key),
    CHECK (saved_at IS NULL OR saved_assessment_id IS NOT NULL)
);

REVOKE ALL ON ALL TABLES IN SCHEMA coordinator FROM PUBLIC;
GRANT USAGE ON SCHEMA coordinator TO coordinator_runtime;
GRANT SELECT ON coordinator.schema_migrations TO coordinator_runtime;
GRANT SELECT, INSERT, UPDATE ON coordinator.source_bindings, coordinator.discovery_progress,
    coordinator.cases, coordinator.servicing_changes, coordinator.collection_observations,
    coordinator.investigation_triggers, coordinator.runs, coordinator.steps,
    coordinator.evidence, coordinator.evidence_link_audits, coordinator.assessments
    TO coordinator_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA coordinator TO coordinator_runtime;
