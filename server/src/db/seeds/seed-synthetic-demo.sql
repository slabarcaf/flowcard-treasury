-- ==============================================================================
-- FlowCard AWS Demonstration Deployment: Synthetic Fixture Seed
-- ==============================================================================
--
-- STRICT BOUNDARY NOTICE:
-- 1. This file contains STRICTLY FICTIONAL, SYNTHETIC demonstration aliases.
-- 2. Contains ZERO real customer PII, zero real business identifiers, and zero
--    real bank account numbers.
-- 3. Identifiers use explicit synthetic prefixes (e.g. 'SYNTHETIC-EIN-BLOOM').
-- 4. Derived directly from reviewed Node migrations and Go domain aggregates.
-- 5. Idempotent: safe to execute repeatedly without duplicating data or errors.
-- ==============================================================================

-- 1. Facility Configuration (Node Ledger Subsystem)
-- Table columns: id, version, currency, facility_amount_cents, reserve_floor_bps,
-- operating_buffer_cents, is_active, created_at.
-- Enforces a partial unique index on active configurations.
-- Preserves any existing active/operator configuration; insert only when no
-- conflicting row exists, including a different active facility ID.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'facility_configs') THEN
    INSERT INTO facility_configs (
      id, version, currency, facility_amount_cents, reserve_floor_bps, operating_buffer_cents, is_active
    ) VALUES (
      'usd-primary', 1, 'USD', 5000000, 2000, 0, true
    ) ON CONFLICT DO NOTHING;
  END IF;
END $$;

-- 2. Customer Roster (Node Subsystem - Synthetic Aliases)
-- Non-destructive: preserves existing roster records and audit history.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'customer_roster') THEN
    INSERT INTO customer_roster (
      customer_id, salesforce_account_id, airwallex_connected_account_id,
      enrolled_by, enrollment_reason, is_active
    ) VALUES
      ('acct_bloom001', 'demo-sf-bloom001', 'acct_bloom001', 'synthetic-seed', 'Synthetic Bloom demonstration customer', true),
      ('acct_atlas001', 'demo-sf-atlas001', 'acct_atlas001', 'synthetic-seed', 'Synthetic Atlas demonstration customer', true),
      ('acct_nova001', 'demo-sf-nova001', 'acct_nova001', 'synthetic-seed', 'Synthetic Nova demonstration customer', true),
      ('acct_pixel001', 'demo-sf-pixel001', 'acct_pixel001', 'synthetic-seed', 'Synthetic Pixel demonstration customer', true)
    ON CONFLICT (customer_id) DO NOTHING;

    -- Audit trail for newly enrolled roster customers
    INSERT INTO customer_roster_audit (customer_id, action, actor, reason)
    SELECT customer_id, 'enrolled', 'synthetic-seed', enrollment_reason
      FROM customer_roster
     WHERE customer_id IN ('acct_bloom001', 'acct_atlas001', 'acct_nova001', 'acct_pixel001')
       AND NOT EXISTS (
         SELECT 1 FROM customer_roster_audit AS audit
          WHERE audit.customer_id = customer_roster.customer_id AND audit.action = 'enrolled'
       );
  END IF;
END $$;

-- 3. Issuing Card Bindings (Node Subsystem - Synthetic Card Placeholders)
-- Non-destructive: preserves existing card bindings and audit history.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'issuing_card_bindings') THEN
    INSERT INTO issuing_card_bindings (
      card_id, customer_id, airwallex_account_id, bound_by, binding_reason, is_active
    ) VALUES
      ('card_synthetic_bloom001', 'acct_bloom001', 'acct_bloom001', 'synthetic-seed', 'Synthetic Bloom card binding for demo simulation', true),
      ('card_synthetic_atlas001', 'acct_atlas001', 'acct_atlas001', 'synthetic-seed', 'Synthetic Atlas card binding for demo simulation', true),
      ('card_synthetic_nova001', 'acct_nova001', 'acct_nova001', 'synthetic-seed', 'Synthetic Nova card binding for demo simulation', true)
    ON CONFLICT (card_id) DO NOTHING;

    -- Audit trail for newly bound cards
    INSERT INTO issuing_card_binding_audit (card_id, customer_id, airwallex_account_id, action, actor, reason)
    SELECT card_id, customer_id, airwallex_account_id, 'bound', 'synthetic-seed', binding_reason
      FROM issuing_card_bindings
     WHERE card_id IN ('card_synthetic_bloom001', 'card_synthetic_atlas001', 'card_synthetic_nova001')
       AND NOT EXISTS (
         SELECT 1 FROM issuing_card_binding_audit AS audit
          WHERE audit.card_id = issuing_card_bindings.card_id AND audit.action = 'bound'
       );
  END IF;
END $$;

-- 4. Company Onboarding Subsystem (Seeded strictly if onboarding schema exists)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'onboarding') THEN

    -- Synthetic demo companies derived strictly from Go domain Company aggregate
    INSERT INTO onboarding.companies (
      id, legal_name, trading_name, entity_type, registration_country, registration_state,
      status, has_submitted_history, business_profile, registration_identifiers, people,
      audit_trail, revision, created_at, updated_at
    ) VALUES
      (
        'comp_synthetic_bloom',
        'Bloom Synthetic Holdings Inc.',
        'Bloom & Co. Demo',
        'corporation',
        'US',
        'CA',
        'ACTIVE',
        false,
        '{
          "description": "Synthetic e-commerce merchant for demonstration",
          "industry": "Retail",
          "website": "https://bloom.demo.internal",
          "email": "demo@bloom.demo.internal",
          "registered_address": {
            "line_1": "100 Synthetic Way",
            "city": "San Francisco",
            "state": "CA",
            "postal_code": "94105",
            "country": "US"
          },
          "products": "E-commerce demonstration products"
        }'::jsonb,
        '[{"type": "EIN", "value": "SYNTHETIC-EIN-BLOOM", "issuing_country": "US"}]'::jsonb,
        '[
          {
            "person_id": "person_synthetic_bloom001",
            "first_name": "Jane",
            "last_name": "Doe",
            "date_of_birth": "1985-05-12",
            "nationality": "US",
            "email": "jane.doe@bloom.demo.internal",
            "role": "CEO",
            "identifications": [
              {
                "identification_id": "ident_synthetic_bloom001",
                "type": "PASSPORT",
                "issuing_country": "US",
                "number": "SYNTHETIC-PASS-001",
                "issued_on": "2020-01-01",
                "expires_on": "2030-01-01"
              }
            ]
          }
        ]'::jsonb,
        '[]'::jsonb,
        1,
        '2026-11-11 00:00:00+00',
        '2026-11-11 00:00:00+00'
      ),
      (
        'comp_synthetic_atlas',
        'Atlas Synthetic Software Corp.',
        'Atlas SaaS Demo',
        'corporation',
        'US',
        'DE',
        'ACTIVE',
        false,
        '{
          "description": "Synthetic software provider for demonstration",
          "industry": "Software",
          "website": "https://atlas.demo.internal",
          "email": "demo@atlas.demo.internal",
          "registered_address": {
            "line_1": "200 Synthetic Boulevard",
            "city": "Wilmington",
            "state": "DE",
            "postal_code": "19801",
            "country": "US"
          },
          "products": "Software demonstration subscription"
        }'::jsonb,
        '[{"type": "EIN", "value": "SYNTHETIC-EIN-ATLAS", "issuing_country": "US"}]'::jsonb,
        '[
          {
            "person_id": "person_synthetic_atlas001",
            "first_name": "John",
            "last_name": "Smith",
            "date_of_birth": "1982-10-20",
            "nationality": "US",
            "email": "john.smith@atlas.demo.internal",
            "role": "Founder",
            "identifications": [
              {
                "identification_id": "ident_synthetic_atlas001",
                "type": "DRIVERS_LICENSE",
                "issuing_country": "US",
                "number": "SYNTHETIC-DL-001",
                "issued_on": "2021-06-01",
                "expires_on": "2031-06-01"
              }
            ]
          }
        ]'::jsonb,
        '[]'::jsonb,
        1,
        '2026-11-11 00:00:00+00',
        '2026-11-11 00:00:00+00'
      )
    ON CONFLICT (id) DO NOTHING;

    -- Synthetic demo applications linked to synthetic companies
    -- Non-destructive: preserves existing applications, revisions, decisions, and frozen snapshots.
    -- Fictional account reconciliation boundary: platform_account_id is kept NULL so background
    -- worker reconcilers do not poll fictional external IDs against live platform APIs.
    INSERT INTO onboarding.applications (
      id, company_id, status, platform_name, platform_environment,
      platform_account_id, decision, onboarding_details, company_snapshot,
      documents, information_requests, creator, operator_review_flagged,
      revision, created_at, updated_at, closed_at
    ) VALUES
      (
        'app_synthetic_bloom',
        'comp_synthetic_bloom',
        'UNDER_REVIEW',
        'AIRWALLEX',
        'sandbox',
        NULL,
        NULL,
        '{
          "industry_category_code": "5399",
          "intended_products": ["SCALE"],
          "estimated_monthly_revenue": {
            "amount": "50000",
            "currency": "USD"
          },
          "primary_contact_person_id": "person_synthetic_bloom001",
          "people": [
            {
              "person_id": "person_synthetic_bloom001",
              "platform_roles": ["DIRECTOR", "BENEFICIAL_OWNER"],
              "residential_address": {
                "line_1": "100 Synthetic Way",
                "city": "San Francisco",
                "state": "CA",
                "postal_code": "94105",
                "country": "US"
              }
            }
          ],
          "customer_agreements": {
            "agreed_to_terms_and_conditions": true,
            "agreed_to_data_usage": true,
            "terms_and_conditions": {
              "agreed_at": "2026-11-11T00:00:00Z",
              "service_agreement_type": "FULL",
              "device_data": {
                "ip_address": "127.0.0.1",
                "user_agent": "FlowCard-Synthetic-Seed/1.0"
              }
            }
          }
        }'::jsonb,
        '{
          "company_id": "comp_synthetic_bloom",
          "status": "ACTIVE",
          "revision": 1,
          "created_at": "2026-11-11T00:00:00Z",
          "updated_at": "2026-11-11T00:00:00Z",
          "has_submitted_history": false,
          "legal_name": "Bloom Synthetic Holdings Inc.",
          "trading_name": "Bloom & Co. Demo",
          "entity_type": "corporation",
          "registration_country": "US",
          "registration_state": "CA",
          "registration_identifiers": [
            {"type": "EIN", "value": "SYNTHETIC-EIN-BLOOM", "issuing_country": "US"}
          ],
          "business_profile": {
            "description": "Synthetic e-commerce merchant for demonstration",
            "industry": "Retail",
            "website": "https://bloom.demo.internal",
            "email": "demo@bloom.demo.internal",
            "registered_address": {
              "line_1": "100 Synthetic Way",
              "city": "San Francisco",
              "state": "CA",
              "postal_code": "94105",
              "country": "US"
            },
            "products": "E-commerce demonstration products"
          },
          "people": [
            {
              "person_id": "person_synthetic_bloom001",
              "first_name": "Jane",
              "last_name": "Doe",
              "date_of_birth": "1985-05-12",
              "nationality": "US",
              "email": "jane.doe@bloom.demo.internal",
              "role": "CEO",
              "identifications": [
                {
                  "identification_id": "ident_synthetic_bloom001",
                  "type": "PASSPORT",
                  "issuing_country": "US",
                  "number": "SYNTHETIC-PASS-001",
                  "issued_on": "2020-01-01",
                  "expires_on": "2030-01-01"
                }
              ]
            }
          ],
          "legal_identifier_audit_trail": []
        }'::jsonb,
        '[]'::jsonb,
        '[]'::jsonb,
        'synthetic-seed',
        false,
        1,
        '2026-11-11 00:00:00+00',
        '2026-11-11 00:00:00+00',
        NULL
      ),
      (
        'app_synthetic_atlas',
        'comp_synthetic_atlas',
        'DRAFT',
        'AIRWALLEX',
        'sandbox',
        NULL,
        NULL,
        '{
          "industry_category_code": "7372",
          "intended_products": ["SCALE"],
          "estimated_monthly_revenue": {
            "amount": "25000",
            "currency": "USD"
          },
          "primary_contact_person_id": "person_synthetic_atlas001",
          "people": [
            {
              "person_id": "person_synthetic_atlas001",
              "platform_roles": ["DIRECTOR"],
              "residential_address": {
                "line_1": "200 Synthetic Boulevard",
                "city": "Wilmington",
                "state": "DE",
                "postal_code": "19801",
                "country": "US"
              }
            }
          ],
          "customer_agreements": {
            "agreed_to_terms_and_conditions": false,
            "agreed_to_data_usage": false
          }
        }'::jsonb,
        '{
          "company_id": "comp_synthetic_atlas",
          "status": "ACTIVE",
          "revision": 1,
          "created_at": "2026-11-11T00:00:00Z",
          "updated_at": "2026-11-11T00:00:00Z",
          "has_submitted_history": false,
          "legal_name": "Atlas Synthetic Software Corp.",
          "trading_name": "Atlas SaaS Demo",
          "entity_type": "corporation",
          "registration_country": "US",
          "registration_state": "DE",
          "registration_identifiers": [
            {"type": "EIN", "value": "SYNTHETIC-EIN-ATLAS", "issuing_country": "US"}
          ],
          "business_profile": {
            "description": "Synthetic software provider for demonstration",
            "industry": "Software",
            "website": "https://atlas.demo.internal",
            "email": "demo@atlas.demo.internal",
            "registered_address": {
              "line_1": "200 Synthetic Boulevard",
              "city": "Wilmington",
              "state": "DE",
              "postal_code": "19801",
              "country": "US"
            },
            "products": "Software demonstration subscription"
          },
          "people": [
            {
              "person_id": "person_synthetic_atlas001",
              "first_name": "John",
              "last_name": "Smith",
              "date_of_birth": "1982-10-20",
              "nationality": "US",
              "email": "john.smith@atlas.demo.internal",
              "role": "Founder",
              "identifications": [
                {
                  "identification_id": "ident_synthetic_atlas001",
                  "type": "DRIVERS_LICENSE",
                  "issuing_country": "US",
                  "number": "SYNTHETIC-DL-001",
                  "issued_on": "2021-06-01",
                  "expires_on": "2031-06-01"
                }
              ]
            }
          ],
          "legal_identifier_audit_trail": []
        }'::jsonb,
        '[]'::jsonb,
        '[]'::jsonb,
        'synthetic-seed',
        false,
        1,
        '2026-11-11 00:00:00+00',
        '2026-11-11 00:00:00+00',
        NULL
      )
    ON CONFLICT (id) DO NOTHING;

  END IF;
END $$;
