CREATE FUNCTION servicing.guard_deposit_conflict_hold() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.mode,NEW.id,NEW.customer_id,NEW.account_ref,NEW.provider_deposit_id,
        NEW.currency,NEW.gross_cents,NEW.net_cents,NEW.provider_fee_cents,NEW.kind,
        NEW.settled_at,NEW.source_observation_id,NEW.created_at)
       IS DISTINCT FROM
       (OLD.mode,OLD.id,OLD.customer_id,OLD.account_ref,OLD.provider_deposit_id,
        OLD.currency,OLD.gross_cents,OLD.net_cents,OLD.provider_fee_cents,OLD.kind,
        OLD.settled_at,OLD.source_observation_id,OLD.created_at)
       OR OLD.status <> 'SETTLED' OR NEW.status <> 'HELD' THEN
        RAISE EXCEPTION 'deposit conflicts may only place settled facts on hold';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER deposits_conflict_hold_only BEFORE UPDATE ON servicing.deposits
FOR EACH ROW EXECUTE FUNCTION servicing.guard_deposit_conflict_hold();

GRANT UPDATE (status) ON servicing.deposits TO servicing_runtime;
