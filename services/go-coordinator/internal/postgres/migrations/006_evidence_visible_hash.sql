-- Hash the exact sanitized and truncated section visible to the agent.
ALTER TABLE coordinator.evidence
    ADD COLUMN model_visible_source_hash bytea
        CHECK (model_visible_source_hash IS NULL OR octet_length(model_visible_source_hash) = 32);
