CREATE TABLE IF NOT EXISTS ai_evidence_extractions (
  extraction_id uuid PRIMARY KEY,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  snapshot_id uuid NOT NULL,
  evidence_fingerprint text NOT NULL CHECK (evidence_fingerprint ~ '^[a-f0-9]{64}$'),
  configuration_version text NOT NULL CHECK (length(btrim(configuration_version)) > 0),
  status text NOT NULL CHECK (status IN ('success', 'no_signal', 'review_required', 'unmapped', 'failed')),
  decision_allowed boolean NOT NULL DEFAULT false,
  decision_block_reason text,
  output jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (customer_id, snapshot_id, configuration_version),
  UNIQUE (customer_id, evidence_fingerprint, extraction_id),
  FOREIGN KEY (customer_id, snapshot_id)
    REFERENCES customer_snapshot_versions(customer_id, snapshot_id)
);

CREATE TABLE IF NOT EXISTS ai_evidence_claims (
  extraction_id uuid NOT NULL REFERENCES ai_evidence_extractions(extraction_id),
  claim_index integer NOT NULL CHECK (claim_index >= 0),
  signal jsonb NOT NULL,
  evidence_references jsonb NOT NULL,
  is_selected boolean NOT NULL,
  PRIMARY KEY (extraction_id, claim_index)
);

CREATE TABLE IF NOT EXISTS ai_evidence_reviews (
  review_id uuid PRIMARY KEY,
  extraction_id uuid NOT NULL REFERENCES ai_evidence_extractions(extraction_id),
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  evidence_fingerprint text NOT NULL CHECK (evidence_fingerprint ~ '^[a-f0-9]{64}$'),
  reviewer_id text NOT NULL CHECK (length(btrim(reviewer_id)) > 0),
  action text NOT NULL CHECK (action IN ('confirmed', 'corrected', 'cleared')),
  corrected_signal jsonb,
  clear_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (customer_id, evidence_fingerprint, extraction_id)
    REFERENCES ai_evidence_extractions(customer_id, evidence_fingerprint, extraction_id),
  CHECK ((action = 'corrected' AND corrected_signal IS NOT NULL AND clear_reason IS NULL)
      OR (action = 'cleared' AND corrected_signal IS NULL AND length(btrim(clear_reason)) > 0)
      OR (action = 'confirmed' AND corrected_signal IS NULL AND clear_reason IS NULL))
);

CREATE INDEX IF NOT EXISTS ai_evidence_reviews_customer_fingerprint
  ON ai_evidence_reviews (customer_id, evidence_fingerprint, created_at DESC);

CREATE OR REPLACE FUNCTION reject_ai_evidence_history_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'AI evidence extraction and review history are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ai_evidence_extractions_immutable ON ai_evidence_extractions;
CREATE TRIGGER ai_evidence_extractions_immutable
  BEFORE UPDATE OR DELETE ON ai_evidence_extractions
  FOR EACH ROW EXECUTE FUNCTION reject_ai_evidence_history_mutation();

DROP TRIGGER IF EXISTS ai_evidence_reviews_immutable ON ai_evidence_reviews;
CREATE TRIGGER ai_evidence_reviews_immutable
  BEFORE UPDATE OR DELETE ON ai_evidence_reviews
  FOR EACH ROW EXECUTE FUNCTION reject_ai_evidence_history_mutation();

DROP TRIGGER IF EXISTS ai_evidence_claims_immutable ON ai_evidence_claims;
CREATE TRIGGER ai_evidence_claims_immutable
  BEFORE UPDATE OR DELETE ON ai_evidence_claims
  FOR EACH ROW EXECUTE FUNCTION reject_ai_evidence_history_mutation();
