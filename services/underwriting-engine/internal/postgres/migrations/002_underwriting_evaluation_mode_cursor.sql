-- Support mode-wide list pages when no customer filter is supplied.
CREATE INDEX evaluations_mode_cursor
    ON underwriting.evaluations (source_mode, created_at DESC, id DESC);
