CREATE TABLE IF NOT EXISTS facility_configs (
  id text PRIMARY KEY,
  version integer NOT NULL CHECK (version > 0),
  currency text NOT NULL CHECK (currency = 'USD'),
  facility_amount_cents bigint NOT NULL CHECK (facility_amount_cents > 0),
  reserve_floor_bps integer NOT NULL CHECK (reserve_floor_bps BETWEEN 0 AND 10000),
  operating_buffer_cents bigint NOT NULL DEFAULT 0 CHECK (operating_buffer_cents = 0),
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS facility_configs_one_active
  ON facility_configs (is_active) WHERE is_active;

CREATE TABLE IF NOT EXISTS bridge_cases (
  id uuid PRIMARY KEY,
  customer_id text NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
  repayment_term text NOT NULL CHECK (repayment_term IN ('15_days', '30_days', 'fixed_date', 'asap')),
  repayment_due_at timestamptz,
  status text NOT NULL CHECK (status IN ('reserved', 'pending', 'suspended', 'funded', 'collecting', 'repaid', 'failed', 'cancelled')),
  request_id text NOT NULL UNIQUE CHECK (request_id ~ '^bridge-[0-9a-fA-F-]{36}$' AND length(request_id) <= 50),
  CHECK ((repayment_term = 'fixed_date' AND repayment_due_at IS NOT NULL)
      OR (repayment_term <> 'fixed_date' AND repayment_due_at IS NULL)),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS bridge_cases_one_open_per_customer
  ON bridge_cases (customer_id)
  WHERE status IN ('reserved', 'pending', 'suspended', 'funded', 'collecting');

CREATE TABLE IF NOT EXISTS ledger_events (
  id bigserial PRIMARY KEY,
  event_type text NOT NULL,
  bridge_id uuid REFERENCES bridge_cases(id),
  amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
  provenance text NOT NULL,
  source_id text UNIQUE,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION reject_ledger_event_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'ledger_events are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_events_immutable ON ledger_events;
CREATE TRIGGER ledger_events_immutable
  BEFORE UPDATE OR DELETE ON ledger_events
  FOR EACH ROW EXECUTE FUNCTION reject_ledger_event_mutation();

CREATE TABLE IF NOT EXISTS provider_operations (
  id uuid PRIMARY KEY,
  bridge_id uuid REFERENCES bridge_cases(id),
  operation_type text NOT NULL CHECK (operation_type IN ('bridge', 'fee', 'principal')),
  request_id text NOT NULL UNIQUE CHECK (request_id ~ '^(bridge|fee|principal)-[a-zA-Z0-9-]+$' AND length(request_id) <= 50),
  provider_object_id text UNIQUE,
  status text NOT NULL DEFAULT 'created',
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS outbox_commands (
  id bigserial PRIMARY KEY,
  provider_operation_id uuid NOT NULL UNIQUE REFERENCES provider_operations(id),
  command_type text NOT NULL,
  payload jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'processing', 'done', 'failed')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  available_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE outbox_commands ADD COLUMN IF NOT EXISTS last_error text;

CREATE TABLE IF NOT EXISTS webhook_inbox (
  id bigserial PRIMARY KEY,
  provider_event_id text NOT NULL UNIQUE,
  event_type text NOT NULL,
  raw_body bytea NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  processing_at timestamptz,
  processed_at timestamptz
);

ALTER TABLE webhook_inbox ADD COLUMN IF NOT EXISTS processing_at timestamptz;

CREATE TABLE IF NOT EXISTS provider_observations (
  id bigserial PRIMARY KEY,
  provider_event_id text NOT NULL REFERENCES webhook_inbox(provider_event_id),
  provider_operation_id uuid REFERENCES provider_operations(id),
  event_type text NOT NULL,
  provider_status text,
  provider_object_id text,
  request_id text,
  payload jsonb NOT NULL,
  observed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider_event_id)
);

CREATE TABLE IF NOT EXISTS bridge_projections (
  bridge_id uuid PRIMARY KEY REFERENCES bridge_cases(id),
  customer_id text NOT NULL,
  status text NOT NULL,
  principal_outstanding_cents bigint NOT NULL DEFAULT 0 CHECK (principal_outstanding_cents >= 0),
  fee_outstanding_cents bigint NOT NULL DEFAULT 0 CHECK (fee_outstanding_cents >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS collection_attempts (
  id uuid PRIMARY KEY,
  bridge_id uuid NOT NULL REFERENCES bridge_cases(id),
  deposit_id text NOT NULL,
  account_id text NOT NULL,
  deposit_amount_cents bigint NOT NULL CHECK (deposit_amount_cents > 0),
  available_balance_cents bigint NOT NULL CHECK (available_balance_cents >= 0),
  assessed_fee_cents bigint NOT NULL CHECK (assessed_fee_cents >= 0),
  fee_operation_id uuid REFERENCES provider_operations(id),
  principal_operation_id uuid REFERENCES provider_operations(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (bridge_id, deposit_id)
);

CREATE INDEX IF NOT EXISTS collection_attempts_bridge_created
  ON collection_attempts (bridge_id, created_at);

CREATE TABLE IF NOT EXISTS facility_projections (
  currency text PRIMARY KEY CHECK (currency = 'USD'),
  facility_amount_cents bigint NOT NULL CHECK (facility_amount_cents > 0),
  reserve_floor_cents bigint NOT NULL CHECK (reserve_floor_cents >= 0),
  opening_exposure_cents bigint NOT NULL DEFAULT 0 CHECK (opening_exposure_cents >= 0),
  settled_principal_cents bigint NOT NULL DEFAULT 0 CHECK (settled_principal_cents >= 0),
  active_reservations_cents bigint NOT NULL DEFAULT 0 CHECK (active_reservations_cents >= 0),
  deployable_cents bigint NOT NULL DEFAULT 0 CHECK (deployable_cents >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS facility_holds (
  id bigserial PRIMARY KEY,
  reason text NOT NULL,
  provider_operation_id uuid REFERENCES provider_operations(id),
  request_id text,
  mismatch jsonb NOT NULL,
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS facility_holds_one_active_mismatch
  ON facility_holds (provider_operation_id, reason) WHERE is_active;

CREATE TABLE IF NOT EXISTS reconciliation_observations (
  id bigserial PRIMARY KEY,
  provider_operation_id uuid NOT NULL REFERENCES provider_operations(id),
  request_id text NOT NULL,
  provider_object_id text,
  provider_status text NOT NULL,
  payload jsonb NOT NULL,
  observed_at timestamptz NOT NULL DEFAULT now(),
  source_id text NOT NULL UNIQUE
);

CREATE OR REPLACE FUNCTION reject_reconciliation_observation_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'reconciliation_observations are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS reconciliation_observations_immutable ON reconciliation_observations;
CREATE TRIGGER reconciliation_observations_immutable
  BEFORE UPDATE OR DELETE ON reconciliation_observations
  FOR EACH ROW EXECUTE FUNCTION reject_reconciliation_observation_mutation();

INSERT INTO facility_configs (
  id, version, currency, facility_amount_cents, reserve_floor_bps, operating_buffer_cents
) VALUES ('usd-primary', 1, 'USD', 5000000, 2000, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO ledger_events (
  event_type, amount_cents, currency, provenance, source_id, payload, occurred_at
) VALUES (
  'opening_exposure', 2500000, 'USD', 'demo_seed', 'demo-seed-opening-exposure-v1',
  '{"synthetic":true,"description":"Synthetic opening exposure for demo; not an Airwallex transfer"}'::jsonb,
  now()
) ON CONFLICT DO NOTHING;

INSERT INTO facility_projections (
  currency, facility_amount_cents, reserve_floor_cents, opening_exposure_cents,
  settled_principal_cents, active_reservations_cents, deployable_cents
) VALUES ('USD', 5000000, 1000000, 2500000, 0, 0, 1500000)
ON CONFLICT (currency) DO NOTHING;
