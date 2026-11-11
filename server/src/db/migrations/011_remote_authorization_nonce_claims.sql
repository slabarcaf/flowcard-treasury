CREATE TABLE IF NOT EXISTS remote_authorization_nonce_claims (
  nonce_fingerprint text PRIMARY KEY CHECK (nonce_fingerprint ~ '^[a-f0-9]{64}$'),
  request_fingerprint text NOT NULL CHECK (request_fingerprint ~ '^[a-f0-9]{64}$'),
  card_transaction_event_id text NOT NULL CHECK (length(btrim(card_transaction_event_id)) > 0),
  claimed_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS remote_authorization_nonce_claim_event
  ON remote_authorization_nonce_claims (card_transaction_event_id);
