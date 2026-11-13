CREATE UNIQUE INDEX IF NOT EXISTS ai_evidence_extractions_customer_snapshot_run
  ON ai_evidence_extractions (customer_id, snapshot_id, extraction_id);

CREATE TABLE IF NOT EXISTS policy_evaluations (
  evaluation_id uuid PRIMARY KEY,
  idempotency_key text NOT NULL UNIQUE CHECK (idempotency_key ~ '^[a-f0-9]{64}$'),
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  profile_id uuid,
  snapshot_id uuid,
  evidence_run_id uuid,
  policy_version text NOT NULL CHECK (length(btrim(policy_version)) > 0),
  roster_updated_at timestamptz NOT NULL,
  evaluated_at timestamptz NOT NULL,
  ratings jsonb,
  tier text NOT NULL CHECK (tier IN ('A', 'B', 'C', 'none')),
  credit_line_cents bigint NOT NULL CHECK (credit_line_cents >= 0),
  outcome text NOT NULL CHECK (outcome IN ('ready', 'ineligible', 'blocked')),
  human_approval_required boolean NOT NULL DEFAULT false,
  reason_codes jsonb NOT NULL,
  input_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
  mapping_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
  FOREIGN KEY (customer_id, profile_id)
    REFERENCES underwriting_profile_versions(customer_id, profile_id),
  FOREIGN KEY (customer_id, snapshot_id)
    REFERENCES customer_snapshot_versions(customer_id, snapshot_id),
  FOREIGN KEY (customer_id, snapshot_id, evidence_run_id)
    REFERENCES ai_evidence_extractions(customer_id, snapshot_id, extraction_id)
);

CREATE INDEX IF NOT EXISTS policy_evaluations_customer_time
  ON policy_evaluations (customer_id, evaluated_at DESC);

CREATE OR REPLACE FUNCTION reject_policy_evaluation_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'policy_evaluations are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS policy_evaluations_immutable ON policy_evaluations;
CREATE TRIGGER policy_evaluations_immutable
  BEFORE UPDATE OR DELETE ON policy_evaluations
  FOR EACH ROW EXECUTE FUNCTION reject_policy_evaluation_mutation();
