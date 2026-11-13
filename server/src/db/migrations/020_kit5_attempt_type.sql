ALTER TABLE kit5_card_attempts
  ADD COLUMN IF NOT EXISTS attempt_type text NOT NULL DEFAULT 'sandbox_live';

ALTER TABLE kit5_card_attempts
  DROP CONSTRAINT IF EXISTS kit5_card_attempts_attempt_type_check;

ALTER TABLE kit5_card_attempts
  ADD CONSTRAINT kit5_card_attempts_attempt_type_check
    CHECK (attempt_type IN ('sandbox_live', 'fixture_checkpoint'));

CREATE INDEX IF NOT EXISTS kit5_card_attempts_bloom_type_created
  ON kit5_card_attempts (attempt_type, created_at DESC)
  WHERE customer_id = 'acct_bloom001';
