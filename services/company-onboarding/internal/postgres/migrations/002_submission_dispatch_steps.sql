-- 002_submission_dispatch_steps.sql
-- Durable per-step dispatch intent states and uploaded document references.
-- Strictly isolated to onboarding schema.

-- 1. Submission dispatch steps table for crash-safe state machine
CREATE TABLE IF NOT EXISTS onboarding.submission_dispatch_steps (
    submission_id VARCHAR(64) NOT NULL REFERENCES onboarding.submissions(id) ON DELETE CASCADE,
    step_name VARCHAR(64) NOT NULL,
    state VARCHAR(32) NOT NULL,
    external_id TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (submission_id, step_name)
);

CREATE INDEX IF NOT EXISTS idx_dispatch_steps_submission
ON onboarding.submission_dispatch_steps (submission_id);

-- 2. Durable document uploads mapping application documents to platform file IDs
CREATE TABLE IF NOT EXISTS onboarding.submission_document_uploads (
    submission_id VARCHAR(64) NOT NULL REFERENCES onboarding.submissions(id) ON DELETE CASCADE,
    document_id VARCHAR(64) NOT NULL,
    platform_file_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (submission_id, document_id)
);

CREATE INDEX IF NOT EXISTS idx_doc_uploads_submission
ON onboarding.submission_document_uploads (submission_id);

-- Grant DML on new tables to onboarding_service
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    onboarding.submission_dispatch_steps,
    onboarding.submission_document_uploads
TO onboarding_service;
