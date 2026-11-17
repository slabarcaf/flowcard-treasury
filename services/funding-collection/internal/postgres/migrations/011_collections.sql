ALTER TABLE servicing.authorities
    ADD COLUMN product_code text,
    ADD COLUMN mandate_reference text,
    ADD COLUMN permitted_purposes text[] NOT NULL DEFAULT '{}';

CREATE OR REPLACE FUNCTION servicing.guard_authority_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.version,NEW.customer_id,NEW.source_account_ref,
        NEW.destination_account_ref,NEW.currency,NEW.permitted_kinds,
        NEW.maximum_debit_cents,NEW.valid_from,NEW.expires_at,
        NEW.disclosures_version,NEW.evidence_refs,NEW.captured_at,NEW.source_id,
        NEW.source_version,NEW.source_captured_at,NEW.product_code,
        NEW.mandate_reference,NEW.permitted_purposes)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.version,OLD.customer_id,OLD.source_account_ref,
        OLD.destination_account_ref,OLD.currency,OLD.permitted_kinds,
        OLD.maximum_debit_cents,OLD.valid_from,OLD.expires_at,
        OLD.disclosures_version,OLD.evidence_refs,OLD.captured_at,OLD.source_id,
        OLD.source_version,OLD.source_captured_at,OLD.product_code,
        OLD.mandate_reference,OLD.permitted_purposes)
       OR NEW.consumed_debit_cents < OLD.consumed_debit_cents
       OR (OLD.status='REVOKED' AND NEW.status<>'REVOKED') THEN
        RAISE EXCEPTION 'authority facts or usage are immutable';
    END IF;
    RETURN NEW;
END
$$;

ALTER TABLE servicing.authorities
    ADD CONSTRAINT authority_collection_evidence_check
    CHECK ((product_code IS NULL AND mandate_reference IS NULL) OR
        (product_code='CONNECTED_ACCOUNT_TRANSFERS' AND
         length(btrim(mandate_reference))>0));

ALTER TABLE servicing.bridges
    ADD COLUMN collection_debt_version bigint NOT NULL DEFAULT 1;
UPDATE servicing.bridges SET collection_debt_version=version;
ALTER TABLE servicing.bridges
    ADD CONSTRAINT bridges_collection_debt_version_check
    CHECK (collection_debt_version>0);

CREATE FUNCTION servicing.guard_collection_debt_version() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW.collection_debt_version < OLD.collection_debt_version THEN
        RAISE EXCEPTION 'collection debt version cannot regress';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER bridges_collection_debt_version_monotonic BEFORE UPDATE
ON servicing.bridges FOR EACH ROW
EXECUTE FUNCTION servicing.guard_collection_debt_version();
GRANT UPDATE (collection_debt_version) ON servicing.bridges TO servicing_runtime;

DO $$
DECLARE capacity_checks text[];
BEGIN
    SELECT array_agg(conname ORDER BY conname) INTO capacity_checks
      FROM pg_constraint
     WHERE conrelid='servicing.authorities'::regclass AND contype='c'
       AND regexp_replace(lower(pg_get_constraintdef(oid)),'[[:space:]()]','','g')
           LIKE '%consumed_debit_cents+reserved_debit_cents<=maximum_debit_cents%';
    IF capacity_checks IS NULL OR cardinality(capacity_checks) <> 1 THEN
        RAISE EXCEPTION 'expected one authority capacity constraint, found %',
            COALESCE(cardinality(capacity_checks),0);
    END IF;
    EXECUTE format('ALTER TABLE servicing.authorities DROP CONSTRAINT %I',capacity_checks[1]);
END
$$;
ALTER TABLE servicing.authorities ADD CONSTRAINT authority_reserved_capacity_check
    CHECK (reserved_debit_cents <= maximum_debit_cents);

DO $$
DECLARE account_checks text[];
BEGIN
    SELECT array_agg(conname ORDER BY conname) INTO account_checks
      FROM pg_constraint
     WHERE conrelid='servicing.ledger_lines'::regclass
       AND contype='c'
       AND conname='ledger_lines_account_check'
       AND conkey=ARRAY[(SELECT attnum FROM pg_attribute
           WHERE attrelid='servicing.ledger_lines'::regclass
             AND attname='account' AND NOT attisdropped)]::smallint[];
    IF account_checks IS NULL OR cardinality(account_checks) <> 1 THEN
        RAISE EXCEPTION 'expected one ledger account constraint, found %',
            COALESCE(cardinality(account_checks),0);
    END IF;
    EXECUTE format('ALTER TABLE servicing.ledger_lines DROP CONSTRAINT %I',account_checks[1]);
END
$$;
ALTER TABLE servicing.ledger_lines ADD CONSTRAINT ledger_lines_account_check_v2
    CHECK (account IN ('FACILITY_CASH','PRINCIPAL_RECEIVABLE','CONTRACT_FEE_RECEIVABLE',
        'LATE_FEE_RECEIVABLE','FEE_INCOME','COLLECTED_CASH','COMMITMENT_MEMO',
        'CUSTOMER_REFUND_PAYABLE','PROVIDER_FEE_EXPENSE','PROVIDER_FEE_CLEARING',
        'COLLECTION_REVERSAL_RECEIVABLE'));

ALTER TABLE servicing.collection_attempts
    ADD COLUMN deposit_observation_id uuid,
    ADD COLUMN balance_observation_id uuid,
    ADD COLUMN evidence_key text CHECK (evidence_key IS NULL OR evidence_key ~ '^[0-9a-f]{64}$'),
    ADD COLUMN planned_on date,
    ADD COLUMN fee_contract_id text,
    ADD CONSTRAINT collection_attempt_deposit_observation_fk
        FOREIGN KEY (mode,deposit_observation_id)
        REFERENCES servicing.provider_observations(mode,id),
    ADD CONSTRAINT collection_attempt_balance_observation_fk
        FOREIGN KEY (mode,balance_observation_id)
        REFERENCES servicing.provider_observations(mode,id),
    ADD CONSTRAINT collection_attempt_plan_evidence_pair
        CHECK ((deposit_observation_id IS NULL) = (balance_observation_id IS NULL)),
    ADD CONSTRAINT collection_attempt_plan_fields_pair
        CHECK ((evidence_key IS NULL) = (planned_on IS NULL));

CREATE UNIQUE INDEX collection_attempt_plan_evidence
    ON servicing.collection_attempts(mode,deposit_id,planned_on,evidence_key)
    WHERE evidence_key IS NOT NULL;

CREATE INDEX collection_deposit_budget
    ON servicing.collection_attempts(mode,deposit_id,state);

CREATE TABLE servicing.collection_plan_checks (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    deposit_id uuid NOT NULL,
    bridge_id uuid NOT NULL,
    planned_on date NOT NULL,
    evidence_key text NOT NULL CHECK (evidence_key ~ '^[0-9a-f]{64}$'),
    deposit_observation_id uuid NOT NULL,
    balance_observation_id uuid NOT NULL,
    authority_id text NOT NULL,
    authority_version bigint NOT NULL,
    bridge_version bigint NOT NULL CHECK (bridge_version > 0),
    bridge_debt_version bigint NOT NULL CHECK (bridge_debt_version > 0),
    result_code text NOT NULL CHECK (result_code ~ '^[A-Z0-9_:-]{1,96}$'),
    operation_id uuid,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,deposit_id,planned_on,evidence_key),
    FOREIGN KEY (mode,deposit_id) REFERENCES servicing.deposits(mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,deposit_observation_id) REFERENCES servicing.provider_observations(mode,id),
    FOREIGN KEY (mode,balance_observation_id) REFERENCES servicing.provider_observations(mode,id),
    FOREIGN KEY (mode,authority_id,authority_version) REFERENCES servicing.authorities(mode,id,version),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id)
);
CREATE INDEX collection_plan_day_limit
    ON servicing.collection_plan_checks(mode,deposit_id,planned_on);

CREATE TABLE servicing.collection_refund_payables (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    bridge_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    remaining_cents bigint NOT NULL CHECK (remaining_cents BETWEEN 0 AND amount_cents),
    currency text NOT NULL CHECK (currency='USD'),
    state text NOT NULL CHECK (state IN ('OPEN','RESOLVED')),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    resolved_at timestamptz,
    PRIMARY KEY (mode,id),
    UNIQUE (mode,operation_id),
    FOREIGN KEY (mode,customer_id) REFERENCES servicing.customers(mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,operation_id) REFERENCES servicing.operations(mode,id),
    CHECK ((state='RESOLVED')=(remaining_cents=0)),
    CHECK ((state='RESOLVED')=(resolved_at IS NOT NULL))
);
CREATE INDEX collection_refund_open_customer
    ON servicing.collection_refund_payables(mode,customer_id,state);
REVOKE ALL ON servicing.collection_refund_payables FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.collection_refund_payables TO servicing_runtime;
GRANT UPDATE (remaining_cents,state,resolved_at)
    ON servicing.collection_refund_payables TO servicing_runtime;

CREATE TABLE servicing.collection_reversals (
    mode text NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    original_operation_id uuid NOT NULL,
    provider_reversal_id text NOT NULL,
    account_ref text NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL CHECK (currency='USD'),
    reversed_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    provider_observation_id uuid NOT NULL,
    evidence_hash text NOT NULL CHECK (evidence_hash ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,account_ref,provider_reversal_id),
    FOREIGN KEY (mode,original_operation_id) REFERENCES servicing.operations(mode,id),
    FOREIGN KEY (mode,provider_observation_id) REFERENCES servicing.provider_observations(mode,id)
);
CREATE INDEX collection_reversals_operation
    ON servicing.collection_reversals(mode,original_operation_id,reversed_at,id);

CREATE TABLE servicing.collection_reversal_allocations (
    mode text NOT NULL,
    reversal_id uuid NOT NULL,
    allocation_index integer NOT NULL CHECK (allocation_index>=0),
    bucket text NOT NULL CHECK (bucket IN ('LATE_FEE','CONTRACT_FEE','PRINCIPAL',
        'REFUND_PAYABLE','REVERSAL_RECEIVABLE')),
    amount_cents bigint NOT NULL CHECK (amount_cents>0),
    PRIMARY KEY (mode,reversal_id,allocation_index),
    FOREIGN KEY (mode,reversal_id) REFERENCES servicing.collection_reversals(mode,id)
);
REVOKE ALL ON servicing.collection_reversals,servicing.collection_reversal_allocations FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.collection_reversals,
    servicing.collection_reversal_allocations TO servicing_runtime;

CREATE FUNCTION servicing.reject_collection_reversal_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'collection reversal evidence is append only';
END
$$;
CREATE TRIGGER collection_reversals_immutable BEFORE UPDATE OR DELETE
ON servicing.collection_reversals FOR EACH ROW
EXECUTE FUNCTION servicing.reject_collection_reversal_mutation();
CREATE TRIGGER collection_reversal_allocations_immutable BEFORE UPDATE OR DELETE
ON servicing.collection_reversal_allocations FOR EACH ROW
EXECUTE FUNCTION servicing.reject_collection_reversal_mutation();

CREATE FUNCTION servicing.reject_collection_plan_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'collection planning evidence is append only';
END
$$;
CREATE TRIGGER collection_plan_checks_immutable BEFORE UPDATE OR DELETE
ON servicing.collection_plan_checks FOR EACH ROW
EXECUTE FUNCTION servicing.reject_collection_plan_mutation();

REVOKE ALL ON servicing.collection_plan_checks FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.collection_plan_checks TO servicing_runtime;
