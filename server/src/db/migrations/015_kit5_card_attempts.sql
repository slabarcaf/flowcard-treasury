CREATE TABLE IF NOT EXISTS kit5_demo_scenario_state (
  scenario_id text PRIMARY KEY CHECK (length(btrim(scenario_id)) > 0),
  next_customer_index integer NOT NULL DEFAULT 0 CHECK (next_customer_index >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kit5_card_attempts (
  scenario_id text NOT NULL REFERENCES kit5_demo_scenario_state(scenario_id),
  customer_id text NOT NULL CHECK (length(btrim(customer_id)) > 0),
  action_id text NOT NULL CHECK (length(btrim(action_id)) > 0 AND length(action_id) <= 120),
  sequence_index integer NOT NULL CHECK (sequence_index >= 0),
  lifecycle_id uuid UNIQUE,
  status text NOT NULL,
  provider_result jsonb,
  error text,
  funding_claimed boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (scenario_id, action_id),
  UNIQUE (scenario_id, sequence_index),
  CONSTRAINT kit5_attempt_provider_result_status_check
    CHECK ((status IN ('accepted', 'submitting', 'in_progress') AND provider_result IS NULL)
      OR status IN ('completed', 'ambiguous', 'failed')),
  CONSTRAINT kit5_attempt_lifecycle_status_check
    CHECK (status NOT IN ('in_progress', 'completed', 'ambiguous', 'failed') OR lifecycle_id IS NOT NULL)
);

ALTER TABLE kit5_card_attempts
  ADD COLUMN IF NOT EXISTS funding_claimed boolean NOT NULL DEFAULT false;

-- Replace constraints from earlier local copies of migration 005 as well as
-- the named constraints in this definition.
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_card_attempts_status_check;
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_card_attempts_check;
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_card_attempts_check1;
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_attempt_status_check;
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_attempt_provider_result_status_check;
ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_attempt_lifecycle_status_check;
ALTER TABLE kit5_card_attempts
  ADD CONSTRAINT kit5_attempt_status_check
    CHECK (status IN ('accepted', 'submitting', 'in_progress', 'completed', 'ambiguous', 'failed')),
  ADD CONSTRAINT kit5_attempt_provider_result_status_check
    CHECK ((status IN ('accepted', 'submitting', 'in_progress') AND provider_result IS NULL)
      OR status IN ('completed', 'ambiguous', 'failed')),
  ADD CONSTRAINT kit5_attempt_lifecycle_status_check
    CHECK (status NOT IN ('in_progress', 'completed', 'ambiguous', 'failed') OR lifecycle_id IS NOT NULL);

CREATE INDEX IF NOT EXISTS kit5_card_attempts_scenario_status
  ON kit5_card_attempts (scenario_id, status, sequence_index);

CREATE OR REPLACE FUNCTION reject_kit5_attempt_mutation_delete() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'kit5_card_attempts cannot be deleted';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS kit5_card_attempts_no_delete ON kit5_card_attempts;
CREATE TRIGGER kit5_card_attempts_no_delete
  BEFORE DELETE ON kit5_card_attempts
  FOR EACH ROW EXECUTE FUNCTION reject_kit5_attempt_mutation_delete();
