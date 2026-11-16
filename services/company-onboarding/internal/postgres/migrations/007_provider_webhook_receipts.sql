-- Preserve original signed bytes for new receipts. Legacy rows remain nullable.
ALTER TABLE onboarding.webhook_inbox ADD COLUMN raw_body bytea;

CREATE TABLE onboarding.webhook_conflicts (
    event_id text NOT NULL REFERENCES onboarding.webhook_inbox(event_id),
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    account_id text NOT NULL,
    event_type text NOT NULL,
    environment text NOT NULL,
    raw_payload jsonb NOT NULL,
    raw_body bytea NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, payload_sha256)
);

REVOKE ALL ON onboarding.webhook_conflicts FROM PUBLIC;
REVOKE ALL ON onboarding.webhook_conflicts FROM onboarding_service;
GRANT SELECT, INSERT ON onboarding.webhook_conflicts TO onboarding_service;
GRANT SELECT, INSERT, UPDATE ON onboarding.webhook_inbox TO onboarding_service;
GRANT USAGE, SELECT ON SEQUENCE onboarding.webhook_inbox_id_seq TO onboarding_service;
