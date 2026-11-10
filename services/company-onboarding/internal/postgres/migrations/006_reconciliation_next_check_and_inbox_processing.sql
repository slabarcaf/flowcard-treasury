-- 006_reconciliation_next_check_and_inbox_processing.sql
-- 1. Update onboarding.webhook_inbox status check constraint to include 'PROCESSING'
-- 2. Add next_check_at to onboarding.platform_accounts for periodic reconciliation backoff and starvation prevention.

ALTER TABLE onboarding.webhook_inbox DROP CONSTRAINT IF EXISTS webhook_inbox_status_check;
ALTER TABLE onboarding.webhook_inbox ADD CONSTRAINT webhook_inbox_status_check
  CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'IGNORED', 'QUARANTINED', 'FAILED'));

ALTER TABLE onboarding.platform_accounts ADD COLUMN IF NOT EXISTS next_check_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_platform_accounts_next_check
  ON onboarding.platform_accounts (next_check_at, id)
  WHERE external_account_id IS NOT NULL AND status NOT IN ('CLOSED');
