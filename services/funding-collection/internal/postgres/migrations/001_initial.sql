-- Applied only by the privileged funding migration command.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'servicing_runtime') THEN
        CREATE ROLE servicing_runtime NOLOGIN NOSUPERUSER NOCREATEDB
            NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_roles
        WHERE rolname = 'servicing_runtime'
          AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole
               OR rolreplication OR rolbypassrls)
    ) OR EXISTS (
        SELECT 1 FROM pg_auth_members
        WHERE member = (SELECT oid FROM pg_roles WHERE rolname = 'servicing_runtime')
    ) THEN
        RAISE EXCEPTION 'servicing_runtime must be restricted NOLOGIN';
    END IF;
    IF to_regclass('public.customer_roster') IS NULL
       OR to_regclass('public.issuing_card_bindings') IS NULL THEN
        RAISE EXCEPTION 'required public mapping tables are missing';
    END IF;
END
$$;

CREATE TABLE servicing.facilities (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id text NOT NULL CHECK (length(btrim(id)) > 0),
    funding_account_ref text NOT NULL CHECK (length(btrim(funding_account_ref)) > 0),
    currency text NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    total_cents bigint NOT NULL CHECK (total_cents > 0),
    reserve_bps integer NOT NULL CHECK (reserve_bps BETWEEN 0 AND 10000),
    principal_owed_cents bigint NOT NULL DEFAULT 0 CHECK (principal_owed_cents >= 0),
    reserved_principal_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_principal_cents >= 0),
    other_commitments_cents bigint NOT NULL DEFAULT 0 CHECK (other_commitments_cents >= 0),
    configuration_version bigint NOT NULL DEFAULT 1 CHECK (configuration_version > 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,funding_account_ref)
);

CREATE TABLE servicing.customers (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id text NOT NULL CHECK (length(btrim(id)) > 0),
    principal_owed_cents bigint NOT NULL DEFAULT 0 CHECK (principal_owed_cents >= 0),
    unpaid_fee_cents bigint NOT NULL DEFAULT 0 CHECK (unpaid_fee_cents >= 0),
    reserved_principal_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_principal_cents >= 0),
    reserved_fee_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_fee_cents >= 0),
    history_complete boolean NOT NULL DEFAULT false,
    defaulted_at timestamptz,
    active_hold boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id)
);

CREATE TABLE servicing.receivables (
    mode text NOT NULL,
    customer_id text NOT NULL,
    source_system text NOT NULL CHECK (length(btrim(source_system)) > 0),
    invoice_id text NOT NULL CHECK (length(btrim(invoice_id)) > 0),
    source_revision text NOT NULL CHECK (length(btrim(source_revision)) > 0),
    currency text NOT NULL CHECK (currency = 'USD'),
    original_cents bigint NOT NULL CHECK (original_cents > 0),
    unpaid_cents bigint NOT NULL CHECK (unpaid_cents >= 0),
    reserved_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_cents >= 0),
    due_on date,
    due_date_verified boolean NOT NULL,
    allocation_complete boolean NOT NULL,
    disputed boolean NOT NULL DEFAULT false,
    source_complete boolean NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,source_system,invoice_id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    CHECK (unpaid_cents <= original_cents),
    CHECK (reserved_cents <= unpaid_cents)
);
CREATE INDEX receivables_customer_due ON servicing.receivables
    (mode,customer_id,due_on,source_system,invoice_id);

CREATE TABLE servicing.actors (
    mode text NOT NULL CHECK (mode = 'DEMO'),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    customer_id text,
    actor_type text NOT NULL CHECK (actor_type IN ('CUSTOMER','OPERATOR','SYSTEM')),
    authentication_subject text NOT NULL CHECK (length(btrim(authentication_subject)) > 0),
    active boolean NOT NULL,
    source_ref text NOT NULL,
    PRIMARY KEY (mode,actor_id),
    UNIQUE (mode,authentication_subject),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    CHECK ((actor_type = 'CUSTOMER') = (customer_id IS NOT NULL)),
    CHECK (actor_type <> 'SYSTEM' OR authentication_subject LIKE 'system:%')
);

CREATE TABLE servicing.authorities (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    customer_id text NOT NULL,
    source_account_ref text NOT NULL,
    destination_account_ref text NOT NULL,
    currency text NOT NULL CHECK (currency = 'USD'),
    permitted_kinds text[] NOT NULL CHECK (cardinality(permitted_kinds) > 0),
    maximum_debit_cents bigint NOT NULL CHECK (maximum_debit_cents > 0),
    consumed_debit_cents bigint NOT NULL DEFAULT 0 CHECK (consumed_debit_cents >= 0),
    reserved_debit_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_debit_cents >= 0),
    valid_from timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('ACTIVE','REVOKED')),
    disclosures_version text NOT NULL,
    evidence_refs jsonb NOT NULL CHECK (jsonb_typeof(evidence_refs) = 'array'),
    captured_at timestamptz NOT NULL,
    PRIMARY KEY (mode,id,version),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    CHECK (source_account_ref <> destination_account_ref),
    CHECK (valid_from < expires_at),
    CHECK (consumed_debit_cents + reserved_debit_cents <= maximum_debit_cents),
    CHECK (permitted_kinds <@ ARRAY['COLLECT_FEE','COLLECT_PRINCIPAL']::text[])
);

CREATE TABLE servicing.offers (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    revision bigint NOT NULL CHECK (revision > 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    facility_id text NOT NULL,
    customer_id text NOT NULL,
    canonical_account_id text NOT NULL,
    transaction_id text NOT NULL,
    authorization_event_id text NOT NULL,
    policy_id text NOT NULL,
    policy_hash text NOT NULL,
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    terms_json jsonb NOT NULL CHECK (jsonb_typeof(terms_json) = 'object'),
    principal_cents bigint NOT NULL CHECK (principal_cents > 0),
    contract_fee_cents bigint NOT NULL CHECK (contract_fee_cents >= 0),
    term_days integer NOT NULL CHECK (term_days BETWEEN 7 AND 90),
    offered_on date NOT NULL,
    due_on date NOT NULL,
    expires_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('OFFERED','AWAITING_ACCEPTANCE','AWAITING_APPROVAL','READY','CONSUMED','SUPERSEDED','EXPIRED','TERMINATED')),
    evaluation_id text NOT NULL,
    source_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(source_refs) = 'array'),
    originating_case_id text,
    supersedes_offer_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,canonical_account_id,transaction_id,id),
    FOREIGN KEY (mode,facility_id) REFERENCES servicing.facilities(mode,id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    FOREIGN KEY (mode,supersedes_offer_id) REFERENCES servicing.offers(mode,id),
    CHECK (due_on = offered_on + term_days)
);
CREATE INDEX offers_customer_state ON servicing.offers (mode,customer_id,state,created_at DESC);

CREATE TABLE servicing.actor_decisions (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    offer_id uuid NOT NULL,
    offer_revision bigint NOT NULL,
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    actor_id text NOT NULL,
    actor_type text NOT NULL CHECK (actor_type IN ('CUSTOMER','OPERATOR','SYSTEM')),
    decision text NOT NULL CHECK (decision IN ('ACCEPT','APPROVE','AUTO_APPROVE')),
    reason_code text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    authentication_subject text NOT NULL,
    disclosures_version text NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs) = 'array'),
    PRIMARY KEY (mode,id),
    FOREIGN KEY (mode,offer_id) REFERENCES servicing.offers(mode,id),
    FOREIGN KEY (mode,actor_id) REFERENCES servicing.actors(mode,actor_id),
    CHECK ((decision = 'ACCEPT' AND actor_type = 'CUSTOMER') OR
           (decision = 'APPROVE' AND actor_type = 'OPERATOR') OR
           (decision = 'AUTO_APPROVE' AND actor_type = 'SYSTEM'))
);
CREATE INDEX actor_decisions_offer ON servicing.actor_decisions (mode,offer_id,recorded_at,id);

CREATE TABLE servicing.bridges (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    facility_id text NOT NULL,
    customer_id text NOT NULL,
    offer_id uuid NOT NULL,
    offer_revision bigint NOT NULL,
    canonical_account_id text NOT NULL,
    transaction_id text NOT NULL,
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    lifecycle text NOT NULL CHECK (lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REPAID','VOID_UNFUNDED','REOPENED_HELD')),
    ever_funded boolean NOT NULL DEFAULT false,
    principal_original_cents bigint NOT NULL CHECK (principal_original_cents > 0),
    contract_fee_original_cents bigint NOT NULL CHECK (contract_fee_original_cents >= 0),
    principal_owed_cents bigint NOT NULL DEFAULT 0 CHECK (principal_owed_cents >= 0),
    contract_fee_owed_cents bigint NOT NULL DEFAULT 0 CHECK (contract_fee_owed_cents >= 0),
    late_fee_owed_cents bigint NOT NULL DEFAULT 0 CHECK (late_fee_owed_cents >= 0),
    principal_paid_cents bigint NOT NULL DEFAULT 0 CHECK (principal_paid_cents >= 0),
    contract_fee_paid_cents bigint NOT NULL DEFAULT 0 CHECK (contract_fee_paid_cents >= 0),
    late_fee_paid_cents bigint NOT NULL DEFAULT 0 CHECK (late_fee_paid_cents >= 0),
    due_on date NOT NULL,
    funded_at timestamptz,
    closed_at timestamptz,
    first_defaulted_on date,
    delinquency_stage text NOT NULL DEFAULT 'CURRENT' CHECK (delinquency_stage IN ('CURRENT','WATCH','DELINQUENT','SERIOUS','DEFAULT')),
    last_accrued_on date,
    cumulative_late_fee_assessed_cents bigint NOT NULL DEFAULT 0 CHECK (cumulative_late_fee_assessed_cents >= 0),
    cumulative_late_fee_paid_cents bigint NOT NULL DEFAULT 0 CHECK (cumulative_late_fee_paid_cents >= 0),
    backing_deficit_cents bigint NOT NULL DEFAULT 0 CHECK (backing_deficit_cents >= 0),
    hold_reasons text[] NOT NULL DEFAULT '{}',
    originating_case_id text,
    evaluation_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    FOREIGN KEY (mode,facility_id) REFERENCES servicing.facilities(mode,id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    FOREIGN KEY (mode,offer_id) REFERENCES servicing.offers(mode,id),
    UNIQUE (mode,canonical_account_id,transaction_id,id),
    CHECK (principal_paid_cents + principal_owed_cents <= principal_original_cents),
    CHECK (contract_fee_paid_cents + contract_fee_owed_cents <= contract_fee_original_cents),
    CHECK ((lifecycle = 'VOID_UNFUNDED') = (NOT ever_funded AND principal_owed_cents = 0 AND contract_fee_owed_cents = 0 AND late_fee_owed_cents = 0) OR lifecycle <> 'VOID_UNFUNDED'),
    CHECK (lifecycle <> 'REPAID' OR (ever_funded AND principal_owed_cents = 0 AND contract_fee_owed_cents = 0 AND late_fee_owed_cents = 0 AND closed_at IS NOT NULL)),
    CHECK (ever_funded OR funded_at IS NULL)
);
ALTER TABLE servicing.bridges ADD CONSTRAINT bridges_offer_payment_fk
    FOREIGN KEY (mode,canonical_account_id,transaction_id,offer_id)
    REFERENCES servicing.offers(mode,canonical_account_id,transaction_id,id);
CREATE UNIQUE INDEX bridges_one_normal_open_customer ON servicing.bridges (mode,customer_id)
    WHERE lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING');
CREATE UNIQUE INDEX bridges_payment_identity ON servicing.bridges (mode,canonical_account_id,transaction_id)
    WHERE ever_funded OR lifecycle <> 'VOID_UNFUNDED';
CREATE INDEX bridges_customer_history ON servicing.bridges (mode,customer_id,created_at DESC,id DESC);
CREATE INDEX bridges_due ON servicing.bridges (mode,due_on,id)
    WHERE lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD');

CREATE TABLE servicing.reservations (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    facility_id text NOT NULL,
    customer_id text NOT NULL,
    bridge_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('NEVER_SENT','DISPATCHED','CONVERTED','RELEASED')),
    facility_principal_cents bigint NOT NULL CHECK (facility_principal_cents > 0),
    customer_principal_cents bigint NOT NULL CHECK (customer_principal_cents > 0),
    customer_fee_cents bigint NOT NULL CHECK (customer_fee_cents >= 0),
    invoice_coverage_cents bigint NOT NULL CHECK (invoice_coverage_cents > 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,bridge_id),
    FOREIGN KEY (mode,facility_id) REFERENCES servicing.facilities(mode,id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    CHECK (invoice_coverage_cents = customer_principal_cents + customer_fee_cents)
);
CREATE INDEX reservations_active_customer ON servicing.reservations (mode,customer_id,state)
    WHERE state IN ('NEVER_SENT','DISPATCHED');

CREATE TABLE servicing.bridge_invoice_allocations (
    mode text NOT NULL,
    bridge_id uuid NOT NULL,
    source_system text NOT NULL,
    invoice_id text NOT NULL,
    allocation_index integer NOT NULL CHECK (allocation_index >= 0),
    original_coverage_cents bigint NOT NULL CHECK (original_coverage_cents > 0),
    active_cents bigint NOT NULL CHECK (active_cents >= 0),
    PRIMARY KEY (mode,bridge_id,source_system,invoice_id),
    UNIQUE (mode,bridge_id,allocation_index),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,source_system,invoice_id) REFERENCES servicing.receivables(mode,source_system,invoice_id),
    CHECK (active_cents <= original_coverage_cents)
);

CREATE TABLE servicing.operations (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    bridge_id uuid NOT NULL,
    reservation_id uuid,
    kind text NOT NULL CHECK (kind IN ('FUND','COLLECT_FEE','COLLECT_PRINCIPAL')),
    state text NOT NULL CHECK (state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD','SETTLED','FAILED_CONFIRMED','CANCELLED_UNSENT')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 50),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    source_account_ref text NOT NULL,
    destination_account_ref text NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL CHECK (currency = 'USD'),
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    authority_id text,
    authority_version bigint,
    late_fee_cents bigint NOT NULL DEFAULT 0 CHECK (late_fee_cents >= 0),
    contract_fee_cents bigint NOT NULL DEFAULT 0 CHECK (contract_fee_cents >= 0),
    principal_cents bigint NOT NULL DEFAULT 0 CHECK (principal_cents >= 0),
    provider_object_id text,
    provider_status text,
    provider_fee_cents bigint CHECK (provider_fee_cents >= 0),
    lease_token uuid,
    lease_expires_at timestamptz,
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_check_at timestamptz,
    sanitized_error_code text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,request_id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,reservation_id) REFERENCES servicing.reservations(mode,id),
    FOREIGN KEY (mode,authority_id,authority_version) REFERENCES servicing.authorities(mode,id,version),
    CHECK ((state = 'DISPATCHING') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (late_fee_cents + contract_fee_cents <= amount_cents),
    CHECK ((kind <> 'COLLECT_FEE') OR amount_cents = late_fee_cents + contract_fee_cents),
    CHECK ((kind <> 'COLLECT_PRINCIPAL') OR amount_cents = principal_cents),
    CHECK ((authority_id IS NULL) = (authority_version IS NULL))
);
CREATE INDEX operations_queue ON servicing.operations (mode,next_check_at,created_at,id)
    WHERE state IN ('QUEUED','PENDING','UNKNOWN');
CREATE INDEX operations_expired_lease ON servicing.operations (mode,lease_expires_at,id)
    WHERE state = 'DISPATCHING';

CREATE TABLE servicing.deposits (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    account_ref text NOT NULL,
    provider_deposit_id text NOT NULL,
    currency text NOT NULL CHECK (currency = 'USD'),
    gross_cents bigint NOT NULL CHECK (gross_cents > 0),
    net_cents bigint NOT NULL CHECK (net_cents > 0),
    provider_fee_cents bigint NOT NULL CHECK (provider_fee_cents >= 0),
    status text NOT NULL CHECK (status IN ('SETTLED','REVERSED','HELD')),
    kind text NOT NULL CHECK (kind IN ('EXTERNAL_SETTLED','OWN_TRANSFER','REFUND','BRIDGE_FUNDING','REVERSAL','UNKNOWN')),
    settled_at timestamptz NOT NULL,
    source_observation_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,account_ref,provider_deposit_id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    CHECK (net_cents + provider_fee_cents = gross_cents)
);

CREATE TABLE servicing.collection_attempts (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    bridge_id uuid NOT NULL,
    deposit_id uuid NOT NULL,
    customer_id text NOT NULL,
    account_ref text NOT NULL,
    authority_id text NOT NULL,
    authority_version bigint NOT NULL,
    reserved_debit_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_debit_cents >= 0),
    settled_debit_cents bigint NOT NULL DEFAULT 0 CHECK (settled_debit_cents >= 0),
    provider_fee_cents bigint NOT NULL DEFAULT 0 CHECK (provider_fee_cents >= 0),
    state text NOT NULL CHECK (state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','SETTLED','FAILED_CONFIRMED','HELD')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,deposit_id) REFERENCES servicing.deposits(mode,id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    FOREIGN KEY (mode,authority_id,authority_version) REFERENCES servicing.authorities(mode,id,version)
);
CREATE UNIQUE INDEX one_unresolved_collection_per_customer ON servicing.collection_attempts (mode,customer_id,account_ref)
    WHERE state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD');

CREATE TABLE servicing.collection_allocations (
    mode text NOT NULL,
    attempt_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    allocation_index integer NOT NULL CHECK (allocation_index >= 0),
    bucket text NOT NULL CHECK (bucket IN ('LATE_FEE','CONTRACT_FEE','PRINCIPAL')),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    PRIMARY KEY (mode,attempt_id,allocation_index),
    UNIQUE (mode,operation_id,bucket),
    FOREIGN KEY (mode,attempt_id) REFERENCES servicing.collection_attempts(mode,id),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id)
);

CREATE TABLE servicing.accrual_days (
    mode text NOT NULL,
    bridge_id uuid NOT NULL,
    accrual_on date NOT NULL,
    opening_principal_cents bigint NOT NULL CHECK (opening_principal_cents >= 0),
    settlement_refs jsonb NOT NULL CHECK (jsonb_typeof(settlement_refs) = 'array'),
    cumulative_numerator numeric(38,0) NOT NULL CHECK (cumulative_numerator >= 0),
    assessed_delta_cents bigint NOT NULL CHECK (assessed_delta_cents >= 0),
    ledger_effect_key text NOT NULL UNIQUE,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,bridge_id,accrual_on),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id)
);

CREATE TABLE servicing.provider_observations (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    operation_id uuid,
    observation_kind text NOT NULL CHECK (observation_kind IN ('TRANSFER','BALANCE','DEPOSIT','PAYMENT')),
    provider_object_id text,
    source_account_ref text,
    destination_account_ref text,
    request_id text,
    status text,
    amount_cents bigint CHECK (amount_cents >= 0),
    currency text CHECK (currency = 'USD'),
    provider_fee_cents bigint CHECK (provider_fee_cents >= 0),
    effective_at timestamptz,
    observed_at timestamptz NOT NULL,
    evidence_hash text NOT NULL CHECK (evidence_hash ~ '^[0-9a-f]{64}$'),
    normalized_evidence jsonb NOT NULL CHECK (jsonb_typeof(normalized_evidence) = 'object'),
    PRIMARY KEY (mode,id),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id)
);

CREATE TABLE servicing.webhook_inbox (
    mode text NOT NULL,
    source text NOT NULL,
    event_id text NOT NULL,
    raw_body bytea NOT NULL CHECK (octet_length(raw_body) <= 10000000),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,source,event_id)
);

CREATE TABLE servicing.commands (
    mode text NOT NULL,
    principal text NOT NULL,
    http_method text NOT NULL,
    resource_path text NOT NULL,
    idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 599),
    response_body jsonb NOT NULL CHECK (jsonb_typeof(response_body) = 'object'),
    resource_id text,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,principal,http_method,resource_path,idempotency_key)
);

CREATE TABLE servicing.change_counters (
    mode text PRIMARY KEY CHECK (mode IN ('DEMO','SANDBOX')),
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0)
);
INSERT INTO servicing.change_counters(mode,last_sequence) VALUES ('DEMO',0),('SANDBOX',0);

CREATE TABLE servicing.changes (
    mode text NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    resource_type text NOT NULL CHECK (resource_type IN ('OFFER','BRIDGE','OPERATION')),
    resource_id text NOT NULL,
    resource_version bigint NOT NULL CHECK (resource_version > 0),
    parent_bridge_id uuid,
    parent_bridge_version bigint,
    change_type text NOT NULL,
    summary jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,sequence),
    CHECK ((parent_bridge_id IS NULL) = (parent_bridge_version IS NULL)),
    CHECK (parent_bridge_version IS NULL OR parent_bridge_version > 0)
);
CREATE INDEX changes_resource ON servicing.changes (mode,resource_type,resource_id,sequence);

CREATE TABLE servicing.ledger_transactions (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    creation_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
    effect_key text NOT NULL,
    transaction_kind text NOT NULL,
    bridge_id uuid,
    operation_id uuid,
    effective_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    provenance jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'object'),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,effect_key),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id)
);

CREATE TABLE servicing.ledger_lines (
    mode text NOT NULL,
    transaction_id uuid NOT NULL,
    line_number smallint NOT NULL CHECK (line_number > 0),
    account text NOT NULL CHECK (account IN ('FACILITY_CASH','PRINCIPAL_RECEIVABLE','CONTRACT_FEE_RECEIVABLE','LATE_FEE_RECEIVABLE','FEE_INCOME','COLLECTED_CASH','COMMITMENT_MEMO','CUSTOMER_REFUND_PAYABLE','PROVIDER_FEE_EXPENSE')),
    debit_cents bigint NOT NULL DEFAULT 0 CHECK (debit_cents >= 0),
    credit_cents bigint NOT NULL DEFAULT 0 CHECK (credit_cents >= 0),
    PRIMARY KEY (mode,transaction_id,line_number),
    FOREIGN KEY (mode,transaction_id) REFERENCES servicing.ledger_transactions(mode,id),
    CHECK ((debit_cents > 0) <> (credit_cents > 0))
);

CREATE FUNCTION servicing.pin_ledger_creation_xid() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.creation_xid IS DISTINCT FROM pg_current_xact_id() THEN
        RAISE EXCEPTION 'ledger creation transaction mismatch';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER ledger_transaction_creation_xid BEFORE INSERT
ON servicing.ledger_transactions FOR EACH ROW
EXECUTE FUNCTION servicing.pin_ledger_creation_xid();

CREATE FUNCTION servicing.require_ledger_line_creation_xid() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE header_xid xid8;
BEGIN
    SELECT creation_xid INTO header_xid FROM servicing.ledger_transactions
     WHERE mode = NEW.mode AND id = NEW.transaction_id;
    IF header_xid IS NULL OR header_xid <> pg_current_xact_id() THEN
        RAISE EXCEPTION 'ledger lines must be added with their transaction header';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER ledger_line_creation_xid BEFORE INSERT ON servicing.ledger_lines
FOR EACH ROW EXECUTE FUNCTION servicing.require_ledger_line_creation_xid();

-- The feed counter is updated in the caller transaction; this helper makes
-- sequence allocation and change insertion atomic and commit ordered.
CREATE FUNCTION servicing.append_change(
    p_mode text, p_resource_type text, p_resource_id text,
    p_resource_version bigint, p_parent_bridge_id uuid,
    p_parent_bridge_version bigint, p_change_type text, p_summary jsonb
) RETURNS bigint LANGUAGE plpgsql SECURITY INVOKER
SET search_path = pg_catalog AS $$
DECLARE next_seq bigint;
BEGIN
    UPDATE servicing.change_counters
       SET last_sequence = last_sequence + 1
     WHERE mode = p_mode
     RETURNING last_sequence INTO next_seq;
    IF next_seq IS NULL THEN RAISE EXCEPTION 'change counter missing'; END IF;
    INSERT INTO servicing.changes(mode,sequence,resource_type,resource_id,
        resource_version,parent_bridge_id,parent_bridge_version,change_type,summary)
    VALUES(p_mode,next_seq,p_resource_type,p_resource_id,p_resource_version,
        p_parent_bridge_id,p_parent_bridge_version,p_change_type,p_summary);
    RETURN next_seq;
END
$$;

-- Called only inside the caller's financial transaction after facility,
-- customer, bridge and sorted invoice locks. Fixed SQL locks roster, then card.
CREATE FUNCTION servicing.lock_verified_mapping(
    p_customer_id text, p_account_id text, p_card_id text
) RETURNS TABLE(customer_id text, roster_account_id text, roster_active boolean,
    card_id text, card_account_id text, card_active boolean)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog AS $$
DECLARE roster_count integer; card_count integer;
BEGIN
    SELECT count(*) INTO roster_count FROM public.customer_roster r
     WHERE r.customer_id = p_customer_id;
    IF roster_count <> 1 THEN RAISE EXCEPTION 'mapping roster unavailable'; END IF;
    SELECT r.customer_id, r.airwallex_connected_account_id, r.is_active
      INTO customer_id, roster_account_id, roster_active
      FROM public.customer_roster r
     WHERE r.customer_id = p_customer_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'mapping roster unavailable'; END IF;
    SELECT count(*) INTO card_count FROM public.issuing_card_bindings b
     WHERE b.card_id = p_card_id AND b.customer_id = p_customer_id;
    IF card_count <> 1 THEN RAISE EXCEPTION 'mapping binding unavailable'; END IF;
    SELECT b.card_id, b.airwallex_account_id, b.is_active
      INTO lock_verified_mapping.card_id, card_account_id, card_active
      FROM public.issuing_card_bindings b
     WHERE b.card_id = p_card_id AND b.customer_id = p_customer_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'mapping binding unavailable'; END IF;
    IF NOT roster_active OR NOT card_active
       OR roster_account_id <> p_account_id OR card_account_id <> p_account_id THEN
        RAISE EXCEPTION 'mapping inactive or conflicting';
    END IF;
    RETURN NEXT;
END
$$;

CREATE FUNCTION servicing.reject_delete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'servicing records are retained';
END
$$;
CREATE TRIGGER ledger_transactions_no_delete BEFORE DELETE ON servicing.ledger_transactions
FOR EACH ROW EXECUTE FUNCTION servicing.reject_delete();
CREATE TRIGGER ledger_lines_no_delete BEFORE DELETE ON servicing.ledger_lines
FOR EACH ROW EXECUTE FUNCTION servicing.reject_delete();
CREATE TRIGGER operations_no_delete BEFORE DELETE ON servicing.operations
FOR EACH ROW EXECUTE FUNCTION servicing.reject_delete();
CREATE TRIGGER bridges_no_delete BEFORE DELETE ON servicing.bridges
FOR EACH ROW EXECUTE FUNCTION servicing.reject_delete();

CREATE FUNCTION servicing.reject_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'servicing ledger rows are append only';
END
$$;
CREATE TRIGGER ledger_transactions_append_only BEFORE UPDATE OR DELETE
ON servicing.ledger_transactions FOR EACH ROW EXECUTE FUNCTION servicing.reject_ledger_mutation();
CREATE TRIGGER ledger_lines_append_only BEFORE UPDATE OR DELETE
ON servicing.ledger_lines FOR EACH ROW EXECUTE FUNCTION servicing.reject_ledger_mutation();
CREATE TRIGGER provider_observations_append_only BEFORE UPDATE OR DELETE
ON servicing.provider_observations FOR EACH ROW EXECUTE FUNCTION servicing.reject_ledger_mutation();
CREATE TRIGGER accrual_days_append_only BEFORE UPDATE OR DELETE
ON servicing.accrual_days FOR EACH ROW EXECUTE FUNCTION servicing.reject_ledger_mutation();

-- Immutable offer and bridge identities/terms; mutable counters/state remain
-- transactionally updated by the owning service code.
CREATE FUNCTION servicing.guard_offer_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.revision,NEW.facility_id,NEW.customer_id,
        NEW.canonical_account_id,NEW.transaction_id,NEW.authorization_event_id,
        NEW.policy_id,NEW.policy_hash,NEW.terms_hash,NEW.terms_json,
        NEW.principal_cents,NEW.contract_fee_cents,NEW.term_days,NEW.offered_on,
        NEW.due_on,NEW.expires_at,NEW.evaluation_id,NEW.originating_case_id)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.revision,OLD.facility_id,OLD.customer_id,
        OLD.canonical_account_id,OLD.transaction_id,OLD.authorization_event_id,
        OLD.policy_id,OLD.policy_hash,OLD.terms_hash,OLD.terms_json,
        OLD.principal_cents,OLD.contract_fee_cents,OLD.term_days,OLD.offered_on,
        OLD.due_on,OLD.expires_at,OLD.evaluation_id,OLD.originating_case_id)
       OR NEW.version < OLD.version THEN
        RAISE EXCEPTION 'offer identity or terms are immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER offers_immutable BEFORE UPDATE ON servicing.offers
FOR EACH ROW EXECUTE FUNCTION servicing.guard_offer_update();

CREATE FUNCTION servicing.guard_bridge_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.facility_id,NEW.customer_id,NEW.offer_id,
        NEW.offer_revision,NEW.canonical_account_id,NEW.transaction_id,
        NEW.terms_hash,NEW.principal_original_cents,NEW.contract_fee_original_cents,
        NEW.due_on,NEW.evaluation_id)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.facility_id,OLD.customer_id,OLD.offer_id,
        OLD.offer_revision,OLD.canonical_account_id,OLD.transaction_id,
        OLD.terms_hash,OLD.principal_original_cents,OLD.contract_fee_original_cents,
        OLD.due_on,OLD.evaluation_id)
       OR (OLD.ever_funded AND NOT NEW.ever_funded) OR NEW.version < OLD.version THEN
        RAISE EXCEPTION 'bridge identity is immutable';
    END IF;
    IF NEW.lifecycle = 'VOID_UNFUNDED' AND
       (NEW.ever_funded OR NEW.principal_owed_cents <> 0 OR
        NEW.contract_fee_owed_cents <> 0 OR NEW.late_fee_owed_cents <> 0 OR
        EXISTS (SELECT 1 FROM servicing.reservations r
                WHERE r.mode = NEW.mode AND r.bridge_id = NEW.id
                  AND r.state IN ('NEVER_SENT','DISPATCHED'))) THEN
        RAISE EXCEPTION 'void bridge must be never funded with no debt or reservation';
    END IF;
    IF NEW.ever_funded AND NEW.lifecycle = 'VOID_UNFUNDED' THEN
        RAISE EXCEPTION 'ever funded bridge cannot be voided';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER bridges_immutable BEFORE UPDATE ON servicing.bridges
FOR EACH ROW EXECUTE FUNCTION servicing.guard_bridge_update();

CREATE FUNCTION servicing.check_ledger_balance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE total_debit numeric; total_credit numeric; target_id uuid;
BEGIN
    IF TG_TABLE_NAME = 'ledger_transactions' THEN
        target_id := NEW.id;
    ELSE
        target_id := NEW.transaction_id;
    END IF;
    SELECT COALESCE(sum(debit_cents),0), COALESCE(sum(credit_cents),0)
      INTO total_debit,total_credit FROM servicing.ledger_lines
     WHERE mode = NEW.mode AND transaction_id = target_id;
    IF total_debit <= 0 OR total_debit <> total_credit THEN
        RAISE EXCEPTION 'ledger transaction must balance';
    END IF;
    RETURN NULL;
END
$$;
CREATE CONSTRAINT TRIGGER ledger_transaction_balanced
AFTER INSERT ON servicing.ledger_transactions DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION servicing.check_ledger_balance();
CREATE CONSTRAINT TRIGGER ledger_line_balanced
AFTER INSERT ON servicing.ledger_lines DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION servicing.check_ledger_balance();

REVOKE ALL ON ALL TABLES IN SCHEMA servicing FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA servicing FROM PUBLIC;
GRANT USAGE ON SCHEMA servicing, public TO servicing_runtime;
GRANT SELECT ON public.customer_roster, public.issuing_card_bindings TO servicing_runtime;
GRANT EXECUTE ON FUNCTION servicing.lock_verified_mapping(text,text,text) TO servicing_runtime;
GRANT EXECUTE ON FUNCTION servicing.append_change(text,text,text,bigint,uuid,bigint,text,jsonb)
    TO servicing_runtime;
GRANT SELECT ON servicing.schema_migrations, servicing.facilities,
    servicing.customers, servicing.receivables, servicing.actors,
    servicing.authorities, servicing.offers, servicing.actor_decisions,
    servicing.bridges, servicing.reservations, servicing.bridge_invoice_allocations,
    servicing.operations, servicing.deposits, servicing.collection_attempts,
    servicing.collection_allocations, servicing.accrual_days,
    servicing.provider_observations, servicing.webhook_inbox, servicing.commands,
    servicing.change_counters, servicing.changes, servicing.ledger_transactions,
    servicing.ledger_lines TO servicing_runtime;
GRANT INSERT ON servicing.receivables, servicing.actors, servicing.authorities,
    servicing.provider_observations, servicing.webhook_inbox,
    servicing.facilities, servicing.customers, servicing.offers,
    servicing.actor_decisions, servicing.bridges,
    servicing.reservations, servicing.bridge_invoice_allocations,
    servicing.operations, servicing.deposits, servicing.collection_attempts,
    servicing.collection_allocations, servicing.accrual_days, servicing.commands,
    servicing.changes, servicing.ledger_transactions, servicing.ledger_lines
    TO servicing_runtime;
GRANT UPDATE (principal_owed_cents,reserved_principal_cents,other_commitments_cents,
    configuration_version,version,updated_at) ON servicing.facilities TO servicing_runtime;
GRANT UPDATE (principal_owed_cents,unpaid_fee_cents,reserved_principal_cents,
    reserved_fee_cents,defaulted_at,active_hold,version,updated_at)
    ON servicing.customers TO servicing_runtime;
GRANT UPDATE (version,state) ON servicing.offers TO servicing_runtime;
GRANT UPDATE (lifecycle,ever_funded,principal_owed_cents,contract_fee_owed_cents,
    late_fee_owed_cents,principal_paid_cents,contract_fee_paid_cents,
    late_fee_paid_cents,funded_at,closed_at,first_defaulted_on,delinquency_stage,
    last_accrued_on,cumulative_late_fee_assessed_cents,
    cumulative_late_fee_paid_cents,backing_deficit_cents,hold_reasons,version,
    updated_at) ON servicing.bridges TO servicing_runtime;
GRANT UPDATE (state,updated_at) ON servicing.reservations TO servicing_runtime;
GRANT UPDATE (state,version,provider_object_id,provider_status,provider_fee_cents,
    lease_token,lease_expires_at,retry_count,next_check_at,sanitized_error_code,
    updated_at) ON servicing.operations TO servicing_runtime;
GRANT UPDATE (reserved_debit_cents,settled_debit_cents,provider_fee_cents,state)
    ON servicing.collection_attempts TO servicing_runtime;
GRANT UPDATE (reserved_cents,version) ON servicing.receivables TO servicing_runtime;
GRANT UPDATE (consumed_debit_cents,reserved_debit_cents,status)
    ON servicing.authorities TO servicing_runtime;
GRANT UPDATE (last_sequence) ON servicing.change_counters TO servicing_runtime;
GRANT UPDATE (response_status,response_body,resource_id)
    ON servicing.commands TO servicing_runtime;
REVOKE UPDATE,DELETE,TRUNCATE ON servicing.ledger_transactions,
    servicing.ledger_lines,servicing.provider_observations,servicing.accrual_days,
    servicing.actor_decisions,servicing.webhook_inbox,servicing.changes
    FROM servicing_runtime;

CREATE FUNCTION servicing.guard_operation_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.bridge_id,NEW.reservation_id,NEW.kind,NEW.request_id,
        NEW.payload_hash,NEW.source_account_ref,NEW.destination_account_ref,
        NEW.amount_cents,NEW.currency,NEW.terms_hash,NEW.authority_id,
        NEW.authority_version,NEW.late_fee_cents,NEW.contract_fee_cents,
        NEW.principal_cents,NEW.created_at)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.bridge_id,OLD.reservation_id,OLD.kind,OLD.request_id,
        OLD.payload_hash,OLD.source_account_ref,OLD.destination_account_ref,
        OLD.amount_cents,OLD.currency,OLD.terms_hash,OLD.authority_id,
        OLD.authority_version,OLD.late_fee_cents,OLD.contract_fee_cents,
        OLD.principal_cents,OLD.created_at)
       OR NEW.version < OLD.version
       OR (OLD.provider_object_id IS NOT NULL AND
           NEW.provider_object_id IS DISTINCT FROM OLD.provider_object_id) THEN
        RAISE EXCEPTION 'operation intent is immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER operations_immutable BEFORE UPDATE ON servicing.operations
FOR EACH ROW EXECUTE FUNCTION servicing.guard_operation_update();
DO $$
BEGIN
    IF pg_has_role('servicing_runtime','servicing_runtime','MEMBER') IS NOT TRUE
       OR has_schema_privilege('servicing_runtime','servicing','CREATE')
       OR has_schema_privilege('servicing_runtime','public','CREATE')
       OR has_database_privilege('servicing_runtime',current_database(),'CREATE')
       OR has_table_privilege('servicing_runtime','public.customer_roster',
            'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('servicing_runtime','public.issuing_card_bindings',
            'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
       OR has_table_privilege('servicing_runtime','servicing.ledger_transactions','UPDATE,DELETE,TRUNCATE')
       OR has_table_privilege('servicing_runtime','servicing.ledger_lines','UPDATE,DELETE,TRUNCATE')
       OR has_table_privilege('servicing_runtime','servicing.provider_observations','UPDATE,DELETE,TRUNCATE')
       OR has_table_privilege('servicing_runtime','servicing.accrual_days','UPDATE,DELETE,TRUNCATE') THEN
        RAISE EXCEPTION 'servicing runtime inherits unsafe privileges';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM pg_proc p
          JOIN pg_namespace n ON n.oid = p.pronamespace
          CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) acl
         WHERE n.nspname = 'servicing'
           AND p.oid = 'servicing.lock_verified_mapping(text,text,text)'::regprocedure
           AND acl.grantee = 0
           AND acl.privilege_type = 'EXECUTE'
    ) THEN
        RAISE EXCEPTION 'public mapping helper execution must be revoked';
    END IF;
END
$$;
