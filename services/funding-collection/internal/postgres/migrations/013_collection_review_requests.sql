CREATE TABLE servicing.collection_review_requests (
    mode text NOT NULL CHECK (mode IN ('DEMO','SANDBOX')),
    id uuid NOT NULL,
    bridge_id uuid NOT NULL,
    requested_bridge_version bigint NOT NULL CHECK (requested_bridge_version > 0),
    requested_by text NOT NULL CHECK (length(btrim(requested_by)) > 0),
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z0-9_:-]{1,96}$'),
    state text NOT NULL CHECK (state IN ('QUEUED','COMPLETED')),
    result_code text CHECK (result_code IS NULL OR result_code ~ '^[A-Z0-9_:-]{1,96}$'),
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    processed_at timestamptz,
    PRIMARY KEY (mode,id),
    FOREIGN KEY (mode,bridge_id) REFERENCES servicing.bridges(mode,id),
    CHECK ((state='QUEUED')=(processed_at IS NULL)),
    CHECK ((state='QUEUED')=(result_code IS NULL))
);
CREATE UNIQUE INDEX collection_review_one_queued_per_bridge
    ON servicing.collection_review_requests(mode,bridge_id) WHERE state='QUEUED';
CREATE INDEX collection_review_queue
    ON servicing.collection_review_requests(mode,requested_at,id) WHERE state='QUEUED';
REVOKE ALL ON servicing.collection_review_requests FROM PUBLIC;
GRANT SELECT,INSERT ON servicing.collection_review_requests TO servicing_runtime;
GRANT UPDATE (state,result_code,processed_at)
    ON servicing.collection_review_requests TO servicing_runtime;
