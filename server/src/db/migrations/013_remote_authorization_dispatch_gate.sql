ALTER TABLE provider_operations
  ADD COLUMN IF NOT EXISTS authorization_request_id text;

ALTER TABLE outbox_commands
  ADD COLUMN IF NOT EXISTS dispatch_ready boolean NOT NULL DEFAULT true;
