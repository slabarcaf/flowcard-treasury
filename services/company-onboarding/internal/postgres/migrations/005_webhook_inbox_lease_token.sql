-- 005_webhook_inbox_lease_token.sql
-- Adds lease token to onboarding.webhook_inbox for worker fencing and crash recovery.

ALTER TABLE onboarding.webhook_inbox ADD COLUMN IF NOT EXISTS lease_token text;
CREATE INDEX IF NOT EXISTS idx_webhook_inbox_lease ON onboarding.webhook_inbox(lease_token, lease_expires_at);
