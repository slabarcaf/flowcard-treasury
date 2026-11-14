-- Run only through the privileged migration command. No shared mapping DML.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shortfall_runtime') THEN
        CREATE ROLE shortfall_runtime NOLOGIN NOSUPERUSER NOCREATEDB
            NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_roles
        WHERE rolname = 'shortfall_runtime'
          AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole
               OR rolreplication OR rolbypassrls)
    ) OR EXISTS (
        SELECT 1 FROM pg_auth_members
        WHERE member = (SELECT oid FROM pg_roles WHERE rolname = 'shortfall_runtime')
    ) THEN
        RAISE EXCEPTION 'shortfall_runtime must be a restricted NOLOGIN role';
    END IF;
END
$$;

CREATE TABLE shortfall.webhook_inbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment text NOT NULL CHECK (length(btrim(environment)) > 0),
    source text NOT NULL CHECK (length(btrim(source)) > 0),
    webhook_id text NOT NULL CHECK (length(btrim(webhook_id)) > 0),
    event_name text NOT NULL CHECK (length(btrim(event_name)) > 0),
    raw_body bytea NOT NULL CHECK (octet_length(raw_body) <= 10000000),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    state text NOT NULL DEFAULT 'RECEIVED'
        CHECK (state IN ('RECEIVED', 'PROCESSING', 'RETRY_WAIT', 'PROCESSED', 'HELD')),
    failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 8),
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_expires_at timestamptz,
    last_reason text,
    conflicted boolean NOT NULL DEFAULT false,
    replay_audit jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(replay_audit) = 'array'),
    received_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    UNIQUE (environment, source, webhook_id),
    CHECK (
        (state = 'PROCESSING' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state <> 'PROCESSING' AND lease_token IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK ((state = 'PROCESSED') = (processed_at IS NOT NULL)),
    CHECK (state <> 'RETRY_WAIT' OR next_attempt_at IS NOT NULL),
    CHECK (NOT conflicted OR state IN ('HELD', 'PROCESSED'))
);

CREATE INDEX webhook_inbox_queue ON shortfall.webhook_inbox
    (next_attempt_at, received_at, id)
    WHERE state IN ('RECEIVED', 'RETRY_WAIT') AND NOT conflicted;
CREATE INDEX webhook_inbox_expired_leases ON shortfall.webhook_inbox (lease_expires_at, id)
    WHERE state = 'PROCESSING';

CREATE TABLE shortfall.inbox_conflicts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inbox_id uuid NOT NULL REFERENCES shortfall.webhook_inbox(id),
    raw_body bytea NOT NULL CHECK (octet_length(raw_body) <= 10000000),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (inbox_id, payload_hash)
);

CREATE TABLE shortfall.detections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment text NOT NULL CHECK (length(btrim(environment)) > 0),
    account_id text NOT NULL CHECK (length(btrim(account_id)) > 0),
    canonical_event_id text NOT NULL CHECK (length(btrim(canonical_event_id)) > 0),
    card_id text CHECK (length(btrim(card_id)) > 0),
    customer_id text CHECK (length(btrim(customer_id)) > 0),
    transaction_id text CHECK (length(btrim(transaction_id)) > 0),
    lifecycle_id text CHECK (length(btrim(lifecycle_id)) > 0),
    provider_facts jsonb NOT NULL CHECK (jsonb_typeof(provider_facts) = 'object'),
    field_provenance jsonb NOT NULL CHECK (jsonb_typeof(field_provenance) = 'object'),
    provider_created_at timestamptz,
    provider_transacted_at timestamptz,
    provider_updated_at timestamptz,
    attempted_amount_cents bigint CHECK (attempted_amount_cents > 0),
    available_balance_cents bigint CHECK (available_balance_cents >= 0),
    shortfall_cents bigint CHECK (shortfall_cents >= 0),
    currency text CHECK (currency ~ '^[A-Z]{3}$'),
    balance_requested_at timestamptz,
    balance_observed_at timestamptz,
    outcome text NOT NULL CHECK (
        outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL', 'IGNORED', 'UNRESOLVED')
    ),
    reason text,
    result_version integer NOT NULL DEFAULT 1 CHECK (result_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment, account_id, canonical_event_id),
    CHECK (
        outcome NOT IN ('IGNORED', 'UNRESOLVED')
        OR (reason IS NOT NULL AND length(btrim(reason)) > 0)
    ),
    CHECK (
        (outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL')
         AND card_id IS NOT NULL AND customer_id IS NOT NULL
         AND transaction_id IS NOT NULL
         AND currency IS NOT NULL AND currency = 'USD'
         AND attempted_amount_cents IS NOT NULL
         AND available_balance_cents IS NOT NULL AND shortfall_cents IS NOT NULL
         AND balance_requested_at IS NOT NULL AND balance_observed_at IS NOT NULL
         AND balance_observed_at >= balance_requested_at
         AND shortfall_cents = GREATEST(attempted_amount_cents - available_balance_cents, 0)
         AND ((outcome = 'SHORTFALL_DETECTED' AND shortfall_cents > 0)
              OR (outcome = 'NO_CURRENT_SHORTFALL' AND shortfall_cents = 0)))
        OR (outcome IN ('IGNORED', 'UNRESOLVED')
            AND available_balance_cents IS NULL AND shortfall_cents IS NULL
            AND balance_requested_at IS NULL AND balance_observed_at IS NULL)
    )
);

CREATE INDEX detections_cursor ON shortfall.detections (created_at DESC, id DESC);
CREATE INDEX detections_customer_cursor ON shortfall.detections
    (customer_id, created_at DESC, id DESC);
CREATE INDEX detections_outcome_cursor ON shortfall.detections
    (outcome, created_at DESC, id DESC);

CREATE TABLE shortfall.inbox_detections (
    inbox_id uuid PRIMARY KEY REFERENCES shortfall.webhook_inbox(id),
    detection_id uuid NOT NULL REFERENCES shortfall.detections(id),
    attached_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inbox_detections_detection ON shortfall.inbox_detections (detection_id);

-- Schema/table ownership stays with the privileged migration identity.
-- Runtime role membership and credentials are provisioned separately, never here.
REVOKE ALL ON SCHEMA shortfall FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA shortfall FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA shortfall FROM shortfall_runtime;
GRANT USAGE ON SCHEMA shortfall, public TO shortfall_runtime;
GRANT SELECT ON public.customer_roster, public.issuing_card_bindings TO shortfall_runtime;
GRANT SELECT ON shortfall.schema_migrations TO shortfall_runtime;
GRANT SELECT, INSERT ON shortfall.webhook_inbox TO shortfall_runtime;
GRANT UPDATE (state, failed_attempts, next_attempt_at, lease_token, lease_expires_at,
    last_reason, conflicted, replay_audit, updated_at, processed_at)
    ON shortfall.webhook_inbox TO shortfall_runtime;
GRANT SELECT, INSERT ON shortfall.inbox_conflicts, shortfall.detections,
    shortfall.inbox_detections TO shortfall_runtime;

-- Existing shared grants must already be safe. Fail instead of rewriting them.
DO $$
BEGIN
    IF has_schema_privilege('shortfall_runtime', 'shortfall', 'CREATE')
       OR has_schema_privilege('shortfall_runtime', 'public', 'CREATE')
       OR has_database_privilege('shortfall_runtime', current_database(), 'CREATE')
       OR has_table_privilege('shortfall_runtime', 'public.customer_roster',
                             'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('shortfall_runtime', 'public.issuing_card_bindings',
                             'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') THEN
        RAISE EXCEPTION 'shortfall runtime inherits unsafe shared grants';
    END IF;
END
$$;
