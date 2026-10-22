ALTER TABLE kit5_card_attempts
  ADD COLUMN IF NOT EXISTS request_facts jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS policy_status text NOT NULL DEFAULT 'not_started',
  ADD COLUMN IF NOT EXISTS policy_error text,
  ADD COLUMN IF NOT EXISTS policy_decision_id uuid,
  ADD COLUMN IF NOT EXISTS pending_policy_decision_id uuid,
  ADD COLUMN IF NOT EXISTS policy_actor_id text,
  ADD COLUMN IF NOT EXISTS policy_approval_reason text,
  ADD COLUMN IF NOT EXISTS bridge_id uuid REFERENCES bridge_cases(id),
  ADD COLUMN IF NOT EXISTS retry_request_id text,
  ADD COLUMN IF NOT EXISTS retry_lifecycle_id uuid UNIQUE,
  ADD COLUMN IF NOT EXISTS retry_provider_result jsonb,
  ADD COLUMN IF NOT EXISTS retry_error text;

ALTER TABLE kit5_card_attempts
  ADD CONSTRAINT kit5_policy_status_check CHECK (policy_status IN (
    'not_started', 'evaluating', 'pending_review', 'blocked', 'rejected', 'reserving', 'reserved', 'settled',
    'retry_submitting', 'retry_ambiguous', 'retry_completed', 'retry_failed'
  )),
  ADD CONSTRAINT kit5_retry_claim_check CHECK (
    policy_status NOT IN ('retry_submitting', 'retry_ambiguous', 'retry_completed', 'retry_failed')
    OR (retry_lifecycle_id IS NOT NULL AND retry_request_id IS NOT NULL)
  ),
  ADD CONSTRAINT kit5_retry_result_check CHECK (
    policy_status <> 'retry_completed' OR retry_provider_result IS NOT NULL
  ),
  ADD CONSTRAINT kit5_policy_approval_audit_check CHECK (
    (policy_actor_id IS NULL) = (policy_approval_reason IS NULL)
  );

CREATE UNIQUE INDEX IF NOT EXISTS kit5_card_attempts_policy_decision_id
  ON kit5_card_attempts(policy_decision_id) WHERE policy_decision_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS kit5_card_attempts_retry_request_id
  ON kit5_card_attempts(retry_request_id) WHERE retry_request_id IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_kit5_request_facts_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.request_facts IS DISTINCT FROM NEW.request_facts THEN
    RAISE EXCEPTION 'kit5_card_attempts request_facts are immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS kit5_card_attempts_request_facts_immutable ON kit5_card_attempts;
CREATE TRIGGER kit5_card_attempts_request_facts_immutable
  BEFORE UPDATE ON kit5_card_attempts
  FOR EACH ROW EXECUTE FUNCTION reject_kit5_request_facts_mutation();
