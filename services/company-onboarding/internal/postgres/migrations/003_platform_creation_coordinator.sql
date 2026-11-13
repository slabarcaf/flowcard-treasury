-- 003_platform_creation_coordinator.sql
-- Durable platform account creation coordination across applications and outbox jobs for a Company.
-- Enforces that at most one remote platform account creation is dispatched per (company_id, platform_name, environment).

CREATE TABLE IF NOT EXISTS onboarding.platform_creation_coordinators (
    company_id VARCHAR(64) NOT NULL REFERENCES onboarding.companies(id) ON DELETE CASCADE,
    platform_name VARCHAR(64) NOT NULL,
    environment VARCHAR(32) NOT NULL,
    state VARCHAR(32) NOT NULL, -- 'DISPATCHING', 'CONFIRMED', 'UNRESOLVED'
    external_account_id TEXT,
    owning_submission_id VARCHAR(64) NOT NULL,
    owning_outbox_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, platform_name, environment)
);

CREATE INDEX IF NOT EXISTS idx_platform_creation_coordinators_company
ON onboarding.platform_creation_coordinators (company_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    onboarding.platform_creation_coordinators
TO onboarding_service;
