CREATE TABLE servicing.funding_checks (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    offer_id uuid NOT NULL,
    offer_revision bigint NOT NULL CHECK (offer_revision > 0),
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    policy_id text NOT NULL,
    policy_hash text NOT NULL CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    customer_context_id text NOT NULL,
    customer_context_version bigint NOT NULL CHECK (customer_context_version > 0),
    facility_source_id text NOT NULL,
    facility_source_version text NOT NULL,
    shortfall_id text NOT NULL,
    payment_observed_at timestamptz NOT NULL,
    borrower_balance_observed_at timestamptz NOT NULL,
    facility_balance_observed_at timestamptz NOT NULL,
    source_refs jsonb NOT NULL CHECK (jsonb_typeof(source_refs) = 'array'),
    invoice_allocations jsonb NOT NULL CHECK (jsonb_typeof(invoice_allocations) = 'array'),
    provider_observations jsonb NOT NULL CHECK (jsonb_typeof(provider_observations) = 'object'),
    policy_input jsonb NOT NULL CHECK (jsonb_typeof(policy_input) = 'object'),
    policy_result jsonb NOT NULL CHECK (jsonb_typeof(policy_result) = 'object'),
    input_hash text NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    result_hash text NOT NULL CHECK (result_hash ~ '^[0-9a-f]{64}$'),
    passed boolean NOT NULL,
    blocked_code text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,id,offer_id),
    FOREIGN KEY (mode,offer_id) REFERENCES servicing.offers(mode,id),
    CHECK ((passed AND blocked_code IS NULL) OR (NOT passed AND blocked_code IS NOT NULL))
);
CREATE INDEX funding_checks_offer ON servicing.funding_checks(mode,offer_id,created_at DESC,id);

ALTER TABLE servicing.operations
    ADD COLUMN reserved_provider_fee_cents bigint CHECK (reserved_provider_fee_cents >= 0);
CREATE FUNCTION servicing.guard_reserved_provider_fee() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.reserved_provider_fee_cents IS DISTINCT FROM OLD.reserved_provider_fee_cents THEN
        RAISE EXCEPTION 'reserved provider fee quote is immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER operations_reserved_provider_fee_immutable BEFORE UPDATE
ON servicing.operations FOR EACH ROW
EXECUTE FUNCTION servicing.guard_reserved_provider_fee();
GRANT UPDATE (active_cents) ON servicing.bridge_invoice_allocations TO servicing_runtime;

ALTER TABLE servicing.actor_decisions
    ADD COLUMN funding_check_id uuid;
ALTER TABLE servicing.actor_decisions
    ADD CONSTRAINT actor_decisions_funding_check_fk
    FOREIGN KEY (mode,funding_check_id,offer_id)
    REFERENCES servicing.funding_checks(mode,id,offer_id);
ALTER TABLE servicing.actor_decisions
    ADD CONSTRAINT actor_decisions_auto_approval_check
    CHECK ((decision = 'AUTO_APPROVE') = (funding_check_id IS NOT NULL));

CREATE FUNCTION servicing.reject_funding_check_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'funding checks are immutable';
END
$$;
CREATE TRIGGER funding_checks_immutable BEFORE UPDATE OR DELETE
ON servicing.funding_checks FOR EACH ROW
EXECUTE FUNCTION servicing.reject_funding_check_mutation();

GRANT SELECT,INSERT ON servicing.funding_checks TO servicing_runtime;
REVOKE UPDATE,DELETE,TRUNCATE ON servicing.funding_checks FROM servicing_runtime;
