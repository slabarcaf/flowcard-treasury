ALTER TABLE kit5_card_attempts DROP CONSTRAINT IF EXISTS kit5_attempt_lifecycle_status_check;
ALTER TABLE kit5_card_attempts
  ADD CONSTRAINT kit5_attempt_lifecycle_status_check
    CHECK (status NOT IN ('in_progress', 'completed') OR lifecycle_id IS NOT NULL);
