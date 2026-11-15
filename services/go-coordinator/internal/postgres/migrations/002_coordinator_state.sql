-- Forward state and ownership additions. Existing 001 SQL remains immutable.
ALTER TABLE coordinator.cases DROP CONSTRAINT cases_status_check;
ALTER TABLE coordinator.cases ADD CONSTRAINT cases_status_check CHECK (status IN
    ('NEW', 'INVESTIGATING', 'EVALUATING', 'MONITORING', 'ACTION_REQUIRED',
     'COMPLETED', 'CLOSED', 'RETRY_WAIT', 'HELD'));
ALTER TABLE coordinator.cases ADD COLUMN lease_stage text;
ALTER TABLE coordinator.cases ADD COLUMN originating_case_id uuid REFERENCES coordinator.cases(id);
ALTER TABLE coordinator.cases DROP CONSTRAINT cases_check;
ALTER TABLE coordinator.cases ADD CONSTRAINT cases_lease_pair_check
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)
       AND (lease_token IS NULL) = (lease_stage IS NULL));
DROP INDEX coordinator.cases_ready;
CREATE INDEX cases_ready ON coordinator.cases(retry_at, created_at, id)
    WHERE status IN ('NEW', 'RETRY_WAIT', 'EVALUATING', 'INVESTIGATING')
       OR (kind = 'COLLECTION' AND status IN ('MONITORING', 'ACTION_REQUIRED') AND refresh_needed);
DROP INDEX coordinator.cases_expired_leases;
CREATE INDEX cases_expired_leases ON coordinator.cases(lease_expires_at, id)
    WHERE lease_expires_at IS NOT NULL;
ALTER TABLE coordinator.runs ADD CONSTRAINT runs_id_case_unique UNIQUE (id, case_id);
ALTER TABLE coordinator.evidence ADD CONSTRAINT evidence_run_case_fk
    FOREIGN KEY (run_id, case_id) REFERENCES coordinator.runs(id, case_id);
ALTER TABLE coordinator.assessments ADD CONSTRAINT assessments_id_case_unique UNIQUE (id, case_id);
ALTER TABLE coordinator.servicing_changes ADD COLUMN customer_id text;
ALTER TABLE coordinator.servicing_changes ADD COLUMN account_ref text;
ALTER TABLE coordinator.servicing_changes ADD COLUMN originating_case_id uuid;
ALTER TABLE coordinator.servicing_changes ADD COLUMN evaluation_id text;
ALTER TABLE coordinator.servicing_changes ADD COLUMN offer_id text;
ALTER TABLE coordinator.steps ADD COLUMN attempt_id text;
ALTER TABLE coordinator.steps ADD CONSTRAINT steps_attempt_id_not_empty CHECK (length(attempt_id) > 0);
CREATE UNIQUE INDEX steps_attempt_id_unique ON coordinator.steps(run_id, attempt_id);
