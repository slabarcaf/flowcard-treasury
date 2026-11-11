CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE remote_authorization_requests
  ADD COLUMN IF NOT EXISTS nonce_fingerprint text;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_schema = current_schema()
       AND table_name = 'remote_authorization_requests'
       AND column_name = 'nonce'
  ) THEN
    UPDATE remote_authorization_requests
       SET nonce_fingerprint = encode(digest(nonce, 'sha256'), 'hex')
     WHERE nonce_fingerprint IS NULL;
  END IF;
END;
$$;

ALTER TABLE remote_authorization_requests
  ALTER COLUMN nonce_fingerprint SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS remote_authorization_nonce_fingerprint_unique
  ON remote_authorization_requests (nonce_fingerprint);

ALTER TABLE remote_authorization_requests
  ADD COLUMN IF NOT EXISTS request_fingerprint text;

ALTER TABLE remote_authorization_requests
  DROP COLUMN IF EXISTS nonce;
