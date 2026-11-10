CREATE TABLE IF NOT EXISTS onboarding.webhook_inbox (
    id bigserial PRIMARY KEY,
    event_id text NOT NULL UNIQUE,
    account_id text NOT NULL DEFAULT '',
    event_type text NOT NULL,
    environment text NOT NULL DEFAULT '',
    raw_payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSED', 'IGNORED', 'QUARANTINED', 'FAILED')),
    processed_at timestamptz,
    last_error text,
    lease_expires_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_webhook_inbox_pending ON onboarding.webhook_inbox(id) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_webhook_inbox_account_id ON onboarding.webhook_inbox(account_id);
