CREATE TABLE IF NOT EXISTS customer_snapshot_versions (
  snapshot_id uuid PRIMARY KEY,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  version integer NOT NULL CHECK (version > 0),
  source_mode text NOT NULL CHECK (source_mode IN ('demo', 'production_mcp')),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  mapping_version text NOT NULL CHECK (length(btrim(mapping_version)) > 0),
  run_id text NOT NULL CHECK (length(btrim(run_id)) > 0),
  sync_started_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL,
  published_at timestamptz NOT NULL DEFAULT now(),
  query_completion jsonb NOT NULL,
  record_counts jsonb NOT NULL,
  canonical_payload jsonb NOT NULL,
  UNIQUE (customer_id, version),
  UNIQUE (customer_id, snapshot_id),
  UNIQUE (customer_id, run_id)
);

CREATE TABLE IF NOT EXISTS customer_snapshot_current (
  customer_id text PRIMARY KEY REFERENCES customer_roster(customer_id),
  snapshot_id uuid NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (customer_id, snapshot_id)
    REFERENCES customer_snapshot_versions(customer_id, snapshot_id)
);

CREATE TABLE IF NOT EXISTS customer_snapshot_attempts (
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  run_id text NOT NULL CHECK (length(btrim(run_id)) > 0),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed')),
  source_mode text NOT NULL CHECK (source_mode IN ('demo', 'production_mcp')),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  mapping_version text NOT NULL CHECK (length(btrim(mapping_version)) > 0),
  started_at timestamptz NOT NULL,
  finished_at timestamptz NOT NULL,
  query_completion jsonb NOT NULL DEFAULT '{}'::jsonb,
  record_counts jsonb NOT NULL DEFAULT '{}'::jsonb,
  error_message text,
  PRIMARY KEY (customer_id, run_id),
  CHECK ((status = 'succeeded' AND error_message IS NULL)
      OR (status = 'failed' AND error_message IS NOT NULL))
);

CREATE OR REPLACE FUNCTION reject_customer_snapshot_version_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'customer_snapshot_versions are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS customer_snapshot_versions_immutable ON customer_snapshot_versions;
CREATE TRIGGER customer_snapshot_versions_immutable
  BEFORE UPDATE OR DELETE ON customer_snapshot_versions
  FOR EACH ROW EXECUTE FUNCTION reject_customer_snapshot_version_mutation();
