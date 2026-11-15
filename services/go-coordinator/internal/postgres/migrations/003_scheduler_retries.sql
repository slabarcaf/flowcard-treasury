-- Persist bounded source and case retry state across process restarts.
ALTER TABLE coordinator.discovery_progress
    ADD COLUMN shortfall_failed_attempts integer NOT NULL DEFAULT 0 CHECK (shortfall_failed_attempts >= 0),
    ADD COLUMN shortfall_retry_at timestamptz,
    ADD COLUMN servicing_failed_attempts integer NOT NULL DEFAULT 0 CHECK (servicing_failed_attempts >= 0),
    ADD COLUMN servicing_retry_at timestamptz,
    ADD COLUMN servicing_last_page_at timestamptz;
ALTER TABLE coordinator.cases
    ADD COLUMN transient_failures integer NOT NULL DEFAULT 0 CHECK (transient_failures >= 0);
CREATE TABLE coordinator.collection_refresh_progress (
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    bridge_version bigint NOT NULL CHECK (bridge_version >= 0),
    page_number integer NOT NULL CHECK (page_number BETWEEN 1 AND 2),
    cursor_in text,
    cursor_out text,
    items jsonb NOT NULL CHECK (jsonb_typeof(items) = 'array'),
    observed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (case_id, bridge_version, page_number)
);
REVOKE ALL ON coordinator.collection_refresh_progress FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON coordinator.collection_refresh_progress TO coordinator_runtime;
