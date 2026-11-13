-- 001_initial_onboarding_schema.sql
-- Dedicated onboarding schema and tables for FlowCard onboarding service.
-- Strictly isolated: no writes to public or ledger schemas.

CREATE SCHEMA IF NOT EXISTS onboarding;

-- Revoke default public access on onboarding schema
REVOKE ALL ON SCHEMA onboarding FROM PUBLIC;

-- 1. Companies aggregate table
CREATE TABLE IF NOT EXISTS onboarding.companies (
    id VARCHAR(64) PRIMARY KEY,
    legal_name TEXT,
    trading_name TEXT,
    entity_type TEXT,
    registration_country VARCHAR(2),
    registration_state TEXT,
    status VARCHAR(32) NOT NULL,
    has_submitted_history BOOLEAN NOT NULL DEFAULT FALSE,
    business_profile JSONB,
    registration_identifiers JSONB NOT NULL DEFAULT '[]'::jsonb,
    people JSONB NOT NULL DEFAULT '[]'::jsonb,
    audit_trail JSONB NOT NULL DEFAULT '[]'::jsonb,
    revision INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- Note: No unique constraint on registration_identifiers: duplicate legal identities across companies are allowed.
CREATE INDEX IF NOT EXISTS idx_companies_status ON onboarding.companies (status);

-- 2. Applications aggregate table
CREATE TABLE IF NOT EXISTS onboarding.applications (
    id VARCHAR(64) PRIMARY KEY,
    company_id VARCHAR(64) NOT NULL REFERENCES onboarding.companies(id),
    status VARCHAR(32) NOT NULL,
    platform_name VARCHAR(64) NOT NULL,
    platform_environment VARCHAR(32) NOT NULL,
    platform_account_id VARCHAR(64),
    decision VARCHAR(32),
    onboarding_details JSONB NOT NULL,
    company_snapshot JSONB NOT NULL,
    documents JSONB NOT NULL DEFAULT '[]'::jsonb,
    information_requests JSONB NOT NULL DEFAULT '[]'::jsonb,
    creator TEXT NOT NULL,
    operator_review_flagged BOOLEAN NOT NULL DEFAULT FALSE,
    revision INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ
);

-- Enforce exactly at most ONE open application per Company and platform
CREATE UNIQUE INDEX IF NOT EXISTS idx_applications_one_open_per_company_platform
ON onboarding.applications (company_id, platform_name)
WHERE status IN ('DRAFT', 'SUBMISSION_PENDING', 'UNDER_REVIEW', 'INFORMATION_REQUIRED');

CREATE INDEX IF NOT EXISTS idx_applications_company_id ON onboarding.applications (company_id);
CREATE INDEX IF NOT EXISTS idx_applications_status ON onboarding.applications (status);

-- 3. Submissions table
CREATE TABLE IF NOT EXISTS onboarding.submissions (
    id VARCHAR(64) PRIMARY KEY,
    application_id VARCHAR(64) NOT NULL REFERENCES onboarding.applications(id) ON DELETE CASCADE,
    submission_type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    definitive_invalid_data BOOLEAN NOT NULL DEFAULT FALSE,
    company_copy JSONB NOT NULL,
    onboarding_details_copy JSONB NOT NULL,
    document_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    information_request_id VARCHAR(64),
    answers JSONB NOT NULL DEFAULT '{}'::jsonb,
    submitting_actor TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_submissions_application_id ON onboarding.submissions (application_id);

-- 4. Platform Accounts aggregate table
CREATE TABLE IF NOT EXISTS onboarding.platform_accounts (
    id VARCHAR(64) PRIMARY KEY,
    company_id VARCHAR(64) NOT NULL REFERENCES onboarding.companies(id),
    platform_name VARCHAR(64) NOT NULL,
    environment VARCHAR(32) NOT NULL,
    external_account_id TEXT,
    status VARCHAR(64) NOT NULL,
    last_sync_time TIMESTAMPTZ,
    revision INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- Enforce at most ONE platform account per Company, platform, and environment
CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_accounts_company_platform_env
ON onboarding.platform_accounts (company_id, platform_name, environment);

-- Enforce external account ID uniqueness per platform and environment when populated
CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_accounts_ext_id_platform_env
ON onboarding.platform_accounts (platform_name, environment, external_account_id)
WHERE external_account_id IS NOT NULL AND external_account_id != '';

CREATE INDEX IF NOT EXISTS idx_platform_accounts_company_id ON onboarding.platform_accounts (company_id);

-- 5. Outbox Messages table for reliable asynchronous platform delivery
CREATE TABLE IF NOT EXISTS onboarding.outbox_messages (
    id VARCHAR(64) PRIMARY KEY,
    topic VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    lease_token VARCHAR(64),
    leased_until TIMESTAMPTZ,
    retry_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending_claim
ON onboarding.outbox_messages (status, leased_until)
WHERE status IN ('PENDING', 'CLAIMED');

-- 6. Idempotency Keys table for request fingerprinting, leased reservations, and replay protection
CREATE TABLE IF NOT EXISTS onboarding.idempotency_keys (
    key_scope VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    reservation_token VARCHAR(64),
    response_status_code INT,
    response_body JSONB,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (key_scope, idempotency_key)
);

-- 7. Dedicated least-privilege runtime role without hardcoded password (NOLOGIN grant role)
-- SECURITY NOTE on PostgreSQL PUBLIC role and inherited privileges in shared databases:
-- Revoking permissions from role onboarding_service does NOT strip privileges granted to PUBLIC
-- at the database or cluster level (e.g. default CONNECT or objects in the public schema).
-- In shared PostgreSQL deployments, onboarding_service inherits default PUBLIC privileges unless
-- REVOKE is executed on those specific external objects or the database itself.
-- This service enforces schema isolation by strictly constraining its runtime queries to the
-- onboarding schema and explicitly revoking all default access on schema onboarding from PUBLIC.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'onboarding_service') THEN
        CREATE ROLE onboarding_service NOLOGIN;
    END IF;
END
$$;

-- Grant USAGE on onboarding schema only
GRANT USAGE ON SCHEMA onboarding TO onboarding_service;

-- Grant DML on domain tables to onboarding_service
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    onboarding.companies,
    onboarding.applications,
    onboarding.submissions,
    onboarding.platform_accounts,
    onboarding.outbox_messages,
    onboarding.idempotency_keys
TO onboarding_service;
