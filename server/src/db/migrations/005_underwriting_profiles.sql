CREATE TABLE IF NOT EXISTS underwriting_profile_versions (
  profile_id uuid PRIMARY KEY,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  version integer NOT NULL CHECK (version > 0),
  run_id text NOT NULL CHECK (length(btrim(run_id)) > 0),
  source_mode text NOT NULL CHECK (source_mode IN ('demo', 'production')),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  feature_version text NOT NULL CHECK (length(btrim(feature_version)) > 0),
  source_versions jsonb NOT NULL,
  window_start timestamptz NOT NULL,
  window_end timestamptz NOT NULL,
  started_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL,
  published_at timestamptz NOT NULL DEFAULT now(),
  query_completion jsonb NOT NULL,
  canonical_payload jsonb NOT NULL,
  UNIQUE (customer_id, version),
  UNIQUE (customer_id, profile_id),
  UNIQUE (customer_id, run_id),
  CHECK (window_end > window_start)
);

CREATE TABLE IF NOT EXISTS underwriting_profile_current (
  customer_id text PRIMARY KEY REFERENCES customer_roster(customer_id),
  profile_id uuid NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (customer_id, profile_id)
    REFERENCES underwriting_profile_versions(customer_id, profile_id)
);

CREATE TABLE IF NOT EXISTS underwriting_profile_attempts (
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  run_id text NOT NULL CHECK (length(btrim(run_id)) > 0),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed')),
  source_mode text NOT NULL CHECK (source_mode IN ('demo', 'production')),
  started_at timestamptz NOT NULL,
  finished_at timestamptz NOT NULL,
  source_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
  query_completion jsonb NOT NULL DEFAULT '{}'::jsonb,
  error_message text,
  PRIMARY KEY (customer_id, run_id),
  CHECK ((status = 'succeeded' AND error_message IS NULL)
      OR (status = 'failed' AND error_message IS NOT NULL))
);

CREATE OR REPLACE FUNCTION reject_underwriting_profile_version_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'underwriting_profile_versions are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS underwriting_profile_versions_immutable ON underwriting_profile_versions;
CREATE TRIGGER underwriting_profile_versions_immutable
  BEFORE UPDATE OR DELETE ON underwriting_profile_versions
  FOR EACH ROW EXECUTE FUNCTION reject_underwriting_profile_version_mutation();
