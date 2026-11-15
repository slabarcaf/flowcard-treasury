-- Record bounded collection run admission and operator-requested replay.
ALTER TABLE coordinator.investigation_triggers
    ADD COLUMN case_id uuid REFERENCES coordinator.cases(id),
    ADD COLUMN run_id uuid,
    ADD COLUMN revision integer,
    ADD COLUMN admitted_at timestamptz,
    ADD CONSTRAINT investigation_trigger_run_pair CHECK (
        (run_id IS NULL) = (case_id IS NULL)
        AND (run_id IS NULL) = (revision IS NULL)
        AND (run_id IS NULL) = (admitted_at IS NULL)
        AND (revision IS NULL OR revision > 0)
    ),
    ADD CONSTRAINT investigation_trigger_run_fk
        FOREIGN KEY (run_id, case_id) REFERENCES coordinator.runs(id, case_id);
ALTER TABLE coordinator.runs
    ADD COLUMN superseded_by_bridge_version bigint
        CHECK (superseded_by_bridge_version IS NULL OR superseded_by_bridge_version >= 0);
ALTER TABLE coordinator.cases
    ADD COLUMN pending_investigation_trigger text,
    ADD COLUMN pending_trigger_bridge_version bigint
        CHECK (pending_trigger_bridge_version IS NULL OR pending_trigger_bridge_version >= 0),
    ADD CONSTRAINT cases_pending_trigger_pair CHECK (
        (pending_investigation_trigger IS NULL) = (pending_trigger_bridge_version IS NULL)
    );
CREATE UNIQUE INDEX investigation_triggers_run_unique
    ON coordinator.investigation_triggers(run_id) WHERE run_id IS NOT NULL;

CREATE TABLE coordinator.case_replays (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id uuid NOT NULL REFERENCES coordinator.cases(id),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 128),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
    prior_status text NOT NULL,
    prior_stage text NOT NULL,
    resumed_revision integer CHECK (resumed_revision IS NULL OR resumed_revision > 0),
    replayed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON coordinator.case_replays FROM PUBLIC;
GRANT SELECT, INSERT ON coordinator.case_replays TO coordinator_runtime;
