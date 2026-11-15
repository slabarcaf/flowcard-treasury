CREATE TABLE servicing.webhook_jobs (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 64),
    event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256),
    target_kind text NOT NULL CHECK (target_kind IN ('FUND_REQUEST','DEPOSIT','UNSUPPORTED')),
    request_id text CHECK (request_id IS NULL OR length(request_id) BETWEEN 1 AND 50),
    customer_id text CHECK (customer_id IS NULL OR length(customer_id) BETWEEN 1 AND 256),
    card_id text CHECK (card_id IS NULL OR length(card_id) BETWEEN 1 AND 256),
    deposit_id text CHECK (deposit_id IS NULL OR length(deposit_id) BETWEEN 1 AND 256),
    state text NOT NULL CHECK (state IN ('PENDING','PROCESSING','RETRY','HELD','DONE')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    lease_token uuid,
    lease_expires_at timestamptz,
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_check_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    sanitized_error_code text CHECK (sanitized_error_code IS NULL OR sanitized_error_code ~ '^[A-Z0-9_:-]{1,96}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    processed_at timestamptz,
    PRIMARY KEY (mode,source,event_id),
    FOREIGN KEY (mode,source,event_id) REFERENCES servicing.webhook_inbox(mode,source,event_id),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    CHECK (state <> 'PROCESSING' OR lease_token IS NOT NULL),
    CHECK (state = 'PROCESSING' OR lease_token IS NULL),
    CHECK ((target_kind = 'FUND_REQUEST') = (request_id IS NOT NULL)),
    CHECK ((target_kind = 'DEPOSIT') = (customer_id IS NOT NULL AND card_id IS NOT NULL AND deposit_id IS NOT NULL))
);

CREATE INDEX webhook_jobs_due ON servicing.webhook_jobs(mode,next_check_at,created_at,event_id)
    WHERE state IN ('PENDING','RETRY','PROCESSING');

REVOKE ALL ON servicing.webhook_jobs FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.webhook_jobs TO servicing_runtime;
GRANT UPDATE (state,version,lease_token,lease_expires_at,retry_count,next_check_at,
    sanitized_error_code,updated_at,processed_at) ON servicing.webhook_jobs TO servicing_runtime;
