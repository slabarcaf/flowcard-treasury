-- Privileged, append-only maintenance evidence. Runtime roles cannot insert
-- rechecks or use an old recheck to release a later hold.
CREATE TABLE servicing.deposit_timestamp_rechecks (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    deposit_id uuid NOT NULL,
    bridge_id uuid NOT NULL,
    original_observation_id uuid NOT NULL,
    verified_observation_id uuid NOT NULL,
    original_settled_at timestamptz NOT NULL,
    observed_settled_at timestamptz NOT NULL,
    maintenance_actor text NOT NULL DEFAULT session_user,
    transaction_id bigint NOT NULL DEFAULT txid_current(),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (mode,id),
    UNIQUE (mode,deposit_id),
    FOREIGN KEY (mode,deposit_id) REFERENCES servicing.deposits(mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    FOREIGN KEY (mode,original_observation_id) REFERENCES servicing.provider_observations(mode,id),
    FOREIGN KEY (mode,verified_observation_id) REFERENCES servicing.provider_observations(mode,id)
);

REVOKE ALL ON servicing.deposit_timestamp_rechecks FROM PUBLIC, servicing_runtime;
CREATE TRIGGER deposit_timestamp_rechecks_append_only BEFORE UPDATE OR DELETE
ON servicing.deposit_timestamp_rechecks FOR EACH ROW
EXECUTE FUNCTION servicing.reject_ledger_mutation();

CREATE OR REPLACE FUNCTION servicing.guard_deposit_conflict_hold() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.customer_id,NEW.account_ref,NEW.provider_deposit_id,
        NEW.currency,NEW.gross_cents,NEW.net_cents,NEW.provider_fee_cents,NEW.kind,
        NEW.settled_at,NEW.source_observation_id,NEW.created_at)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.customer_id,OLD.account_ref,OLD.provider_deposit_id,
        OLD.currency,OLD.gross_cents,OLD.net_cents,OLD.provider_fee_cents,OLD.kind,
        OLD.settled_at,OLD.source_observation_id,OLD.created_at) THEN
        RAISE EXCEPTION 'deposit source facts are immutable';
    END IF;
    IF OLD.status = 'SETTLED' AND NEW.status = 'HELD' THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'HELD' AND NEW.status = 'SETTLED' THEN
        IF EXISTS (
            SELECT 1 FROM servicing.deposit_timestamp_rechecks r
            JOIN servicing.provider_observations o
              ON o.mode=r.mode AND o.id=r.verified_observation_id
            WHERE r.mode=OLD.mode AND r.deposit_id=OLD.id
              AND r.transaction_id=txid_current()
              AND r.original_observation_id=OLD.source_observation_id
              AND r.original_settled_at=OLD.settled_at
              AND o.observation_kind='DEPOSIT'
              AND o.provider_object_id=OLD.provider_deposit_id
              AND o.source_account_ref=OLD.account_ref
              AND o.status='SETTLED' AND o.currency=OLD.currency
              AND o.amount_cents=OLD.gross_cents
              AND o.provider_fee_cents=OLD.provider_fee_cents
              AND o.normalized_evidence->>'customer_id'=OLD.customer_id
              AND o.normalized_evidence->>'kind'=OLD.kind
              AND (o.normalized_evidence->>'net_cents')::bigint=OLD.net_cents
        ) THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'deposit release requires a fresh privileged recheck';
END
$$;
