-- Durable extraction provenance and case-local nonexecuting recommendations.
ALTER TABLE coordinator.evidence
    ADD COLUMN source_type text,
    ADD COLUMN source_record_id text,
    ADD COLUMN source_binding_id text REFERENCES coordinator.source_bindings(id),
    ADD COLUMN source_export_version text,
    ADD COLUMN section_id text,
    ADD COLUMN extracted_at timestamptz,
    ADD COLUMN model_version text,
    ADD COLUMN prompt_version text,
    ADD COLUMN conflicts jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(conflicts) = 'array');
ALTER TABLE coordinator.evidence_link_audits
    ADD COLUMN source_binding_id text REFERENCES coordinator.source_bindings(id);
CREATE TABLE coordinator.collection_recommendations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    run_id uuid NOT NULL,
    tool_call_id text NOT NULL,
    bridge_version bigint NOT NULL CHECK (bridge_version >= 0),
    action_code text NOT NULL CHECK (action_code IN (
        'WAIT_FOR_SETTLEMENT', 'REQUEST_OPERATOR_REVIEW',
        'VERIFY_COLLECTION_AUTHORITY', 'RECONCILE_OPERATION',
        'WAIT_FOR_ELIGIBLE_DEPOSIT', 'MONITOR_REPAYMENT', 'WAIT_FOR_ACCRUAL')),
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 2000),
    citations jsonb NOT NULL CHECK (jsonb_typeof(citations) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, tool_call_id),
    FOREIGN KEY (run_id, case_id) REFERENCES coordinator.runs(id, case_id)
);
GRANT SELECT, INSERT, UPDATE ON coordinator.evidence TO coordinator_runtime;
GRANT SELECT, INSERT, UPDATE ON coordinator.evidence_link_audits TO coordinator_runtime;
REVOKE ALL ON coordinator.collection_recommendations FROM PUBLIC;
GRANT SELECT, INSERT ON coordinator.collection_recommendations TO coordinator_runtime;
