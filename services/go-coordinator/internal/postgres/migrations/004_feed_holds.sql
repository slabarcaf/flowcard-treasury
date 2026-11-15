-- Persist source feed holds separately from transient retry state.
ALTER TABLE coordinator.discovery_progress
    ADD COLUMN shortfall_hold_reason text,
    ADD COLUMN servicing_hold_reason text;
