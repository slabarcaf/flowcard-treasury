ALTER TABLE policy_request_evaluations
  ADD COLUMN IF NOT EXISTS ledger_sequence bigint NOT NULL DEFAULT 0
    CHECK (ledger_sequence >= 0);

ALTER TABLE policy_request_evaluations
  ALTER COLUMN ledger_sequence DROP DEFAULT;
