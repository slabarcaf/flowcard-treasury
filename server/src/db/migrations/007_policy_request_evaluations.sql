CREATE TABLE IF NOT EXISTS policy_request_evaluations (
  decision_id uuid PRIMARY KEY,
  evaluation_id uuid NOT NULL REFERENCES policy_evaluations(evaluation_id),
  request_id text NOT NULL CHECK (length(btrim(request_id)) > 0),
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  outcome text NOT NULL CHECK (outcome IN ('auto_approved', 'human_review', 'declined', 'blocked')),
  amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
  credit_line_cents bigint NOT NULL CHECK (credit_line_cents >= 0),
  tier text NOT NULL CHECK (tier IN ('A', 'B', 'C', 'none')),
  capital_band text CHECK (capital_band IN ('a_lot', 'normal', 'less', 'critical', 'at_floor')),
  door text CHECK (door IN ('auto', 'human', 'closed')),
  reason_codes jsonb NOT NULL,
  rationale text NOT NULL,
  actor_id text,
  approval_reason text,
  evaluated_at timestamptz NOT NULL,
  CHECK ((actor_id IS NULL) = (approval_reason IS NULL)),
  CHECK (actor_id IS NULL OR length(btrim(actor_id)) > 0),
  CHECK (approval_reason IS NULL OR length(btrim(approval_reason)) > 0)
);

CREATE INDEX IF NOT EXISTS policy_request_evaluations_request_time
  ON policy_request_evaluations (request_id, evaluated_at DESC);

CREATE OR REPLACE FUNCTION reject_policy_request_evaluation_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'policy_request_evaluations are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS policy_request_evaluations_immutable ON policy_request_evaluations;
CREATE TRIGGER policy_request_evaluations_immutable
  BEFORE UPDATE OR DELETE ON policy_request_evaluations
  FOR EACH ROW EXECUTE FUNCTION reject_policy_request_evaluation_mutation();
