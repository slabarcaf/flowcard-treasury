CREATE TABLE IF NOT EXISTS kit5_repayment_deposits (
  id uuid PRIMARY KEY,
  scenario_id text NOT NULL,
  customer_id text NOT NULL,
  action_id text NOT NULL,
  bridge_id uuid NOT NULL REFERENCES bridge_cases(id),
  account_id text NOT NULL CHECK (length(btrim(account_id)) > 0),
  global_account_id text NOT NULL CHECK (length(btrim(global_account_id)) > 0),
  amount_cents bigint NOT NULL DEFAULT 800000 CHECK (amount_cents = 800000),
  currency text NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
  statement_ref text NOT NULL UNIQUE CHECK (length(btrim(statement_ref)) > 0),
  status text NOT NULL CHECK (status IN ('creating', 'ambiguous', 'created', 'settled', 'blocked')),
  provider_deposit_id text UNIQUE,
  provider_result jsonb,
  error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  UNIQUE (bridge_id),
  FOREIGN KEY (scenario_id, action_id)
    REFERENCES kit5_card_attempts(scenario_id, action_id),
  CHECK (status <> 'settled' OR (provider_deposit_id IS NOT NULL AND settled_at IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS kit5_repayment_deposit_events (
  provider_event_id text PRIMARY KEY REFERENCES webhook_inbox(provider_event_id),
  deposit_operation_id uuid NOT NULL REFERENCES kit5_repayment_deposits(id),
  associated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS kit5_repayment_deposit_events_operation
  ON kit5_repayment_deposit_events(deposit_operation_id, associated_at);

CREATE INDEX IF NOT EXISTS kit5_repayment_deposits_customer_status
  ON kit5_repayment_deposits(customer_id, status);

CREATE OR REPLACE FUNCTION reject_kit5_repayment_deposit_identity_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.scenario_id IS DISTINCT FROM NEW.scenario_id
     OR OLD.customer_id IS DISTINCT FROM NEW.customer_id
     OR OLD.action_id IS DISTINCT FROM NEW.action_id
     OR OLD.bridge_id IS DISTINCT FROM NEW.bridge_id
     OR OLD.account_id IS DISTINCT FROM NEW.account_id
     OR OLD.global_account_id IS DISTINCT FROM NEW.global_account_id
     OR OLD.amount_cents IS DISTINCT FROM NEW.amount_cents
     OR OLD.currency IS DISTINCT FROM NEW.currency
     OR OLD.statement_ref IS DISTINCT FROM NEW.statement_ref THEN
    RAISE EXCEPTION 'Kit 5 repayment deposit identity is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS kit5_repayment_deposits_identity_immutable ON kit5_repayment_deposits;
CREATE TRIGGER kit5_repayment_deposits_identity_immutable
  BEFORE UPDATE ON kit5_repayment_deposits
  FOR EACH ROW EXECUTE FUNCTION reject_kit5_repayment_deposit_identity_mutation();
