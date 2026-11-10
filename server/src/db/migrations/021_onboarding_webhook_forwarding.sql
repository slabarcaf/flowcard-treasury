CREATE TABLE IF NOT EXISTS onboarding_webhook_forwarding (
  id bigserial PRIMARY KEY,
  provider_event_id text NOT NULL UNIQUE REFERENCES webhook_inbox(provider_event_id),
  event_type text NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'forwarding', 'forwarded', 'failed', 'quarantined')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  lease_token text,
  leased_until timestamptz,
  last_error text,
  next_retry_at timestamptz NOT NULL DEFAULT now(),
  forwarded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_onboarding_webhook_forwarding_claim
  ON onboarding_webhook_forwarding (status, next_retry_at, leased_until, id);
