import { readFile } from "node:fs/promises";
import { describe, expect, it, vi } from "vitest";
import type { DatabasePool } from "../../server/src/db/pool.js";
import {
  initializeFacilityProjection,
  insertWebhookOnce,
} from "../../server/src/db/repositories.js";

const migrationUrl = new URL(
  "../../server/src/db/migrations/001_bridge_ledger.sql",
  import.meta.url,
);
const rosterMigrationUrl = new URL(
  "../../server/src/db/migrations/002_customer_roster.sql",
  import.meta.url,
);
const remoteAuthorizationMigrationUrl = new URL(
  "../../server/src/db/migrations/008_remote_authorization.sql",
  import.meta.url,
);
const remoteAuthorizationFingerprintMigrationUrl = new URL(
  "../../server/src/db/migrations/010_remote_authorization_nonce_fingerprint.sql",
  import.meta.url,
);
const remoteAuthorizationNonceClaimsMigrationUrl = new URL(
  "../../server/src/db/migrations/011_remote_authorization_nonce_claims.sql",
  import.meta.url,
);
const remoteAuthorizationDecisionIdentityMigrationUrl = new URL(
  "../../server/src/db/migrations/012_remote_authorization_decision_identity.sql",
  import.meta.url,
);
const remoteAuthorizationDispatchGateMigrationUrl = new URL(
  "../../server/src/db/migrations/013_remote_authorization_dispatch_gate.sql",
  import.meta.url,
);
const aiEvidenceDecisionGateMigrationUrl = new URL(
  "../../server/src/db/migrations/014_ai_evidence_decision_gate.sql",
  import.meta.url,
);

describe("bridge ledger schema", () => {
  it("defines the required tables and enforces core constraints", async () => {
    const sql = await readFile(migrationUrl, "utf8");

    for (const table of [
      "facility_configs",
      "bridge_cases",
      "ledger_events",
      "provider_operations",
      "outbox_commands",
      "webhook_inbox",
      "bridge_projections",
      "collection_attempts",
      "facility_projections",
    ]) {
      expect(sql).toMatch(new RegExp(`CREATE TABLE IF NOT EXISTS ${table}\\b`));
    }
    const operations = sql.match(/CREATE TABLE IF NOT EXISTS provider_operations \(([\s\S]*?)\n\);/)?.[1];
    const inbox = sql.match(/CREATE TABLE IF NOT EXISTS webhook_inbox \(([\s\S]*?)\n\);/)?.[1];
    expect(operations).toMatch(/request_id text NOT NULL UNIQUE/);
    expect(inbox).toMatch(/provider_event_id text NOT NULL UNIQUE/);
    expect(sql).toContain("bridge_cases_one_open_per_customer");
    expect(sql).toContain("repayment_due_at timestamptz");
    expect(sql).toContain("repayment_term = 'fixed_date' AND repayment_due_at IS NOT NULL");
    expect(sql).toContain("UNIQUE (bridge_id, deposit_id)");
    expect(sql).toContain("ledger_events_immutable");
    expect(sql).toContain("CHECK (currency = 'USD')");
  });

  it("seeds synthetic opening exposure and the initial $15,000 projection", async () => {
    const sql = await readFile(migrationUrl, "utf8");

    expect(sql).toContain("'demo_seed'");
    expect(sql).toContain("'demo-seed-opening-exposure-v1'");
    expect(sql).toContain("'opening_exposure', 2500000");
    expect(sql).toContain('"synthetic":true');
    expect(sql).toContain("('USD', 5000000, 1000000, 2500000, 0, 0, 1500000)");
  });

  it("initializes projections from facility configuration and ledger events", async () => {
    const query = vi.fn().mockResolvedValue({ rows: [], rowCount: 1 });
    await initializeFacilityProjection({ query } as unknown as DatabasePool);

    expect(query).toHaveBeenCalledOnce();
    expect(query.mock.calls[0][0]).toContain("FROM facility_configs AS config");
    expect(query.mock.calls[0][0]).toContain("event.event_type = 'opening_exposure'");
    expect(query.mock.calls[0][0]).toContain("ON CONFLICT (currency) DO NOTHING");
  });

  it("inserts webhook events once by provider event ID", async () => {
    const query = vi.fn().mockResolvedValue({ rows: [], rowCount: 0 });
    const inserted = await insertWebhookOnce(
      { query } as unknown as DatabasePool,
      { id: "evt-1", type: "transfer.settled", rawBody: new Uint8Array([1, 2]) },
    );

    expect(inserted).toBe(false);
    expect(query.mock.calls[0][0]).toContain("ON CONFLICT (provider_event_id) DO NOTHING");
  });

  it("defines unique, audited enrollment mappings and synthetic demo roster entries", async () => {
    const sql = await readFile(rosterMigrationUrl, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS customer_roster");
    expect(sql).toMatch(/salesforce_account_id text NOT NULL UNIQUE/);
    expect(sql).toMatch(/airwallex_connected_account_id text NOT NULL UNIQUE/);
    expect(sql).toContain("customer_roster_audit");
    expect(sql).toContain("customer_roster_audit_immutable");
    expect(sql).toContain("demo-seed");
    expect(sql).toContain("Synthetic Bloom demo customer");
    expect(sql).toContain("ON CONFLICT (customer_id) DO NOTHING");
  });

  it("stores only allowlisted remote authorization facts and audited card bindings", async () => {
    const sql = await readFile(remoteAuthorizationMigrationUrl, "utf8");
    const fingerprintSql = await readFile(remoteAuthorizationFingerprintMigrationUrl, "utf8");
    const nonceClaimsSql = await readFile(remoteAuthorizationNonceClaimsMigrationUrl, "utf8");
    const decisionIdentitySql = await readFile(remoteAuthorizationDecisionIdentityMigrationUrl, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS issuing_card_bindings");
    expect(sql).toContain("issuing_card_binding_audit_immutable");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS remote_authorization_requests");
    expect(fingerprintSql).toContain("nonce_fingerprint text");
    expect(fingerprintSql).toContain("request_fingerprint text");
    expect(fingerprintSql).toContain("digest(nonce, 'sha256')");
    expect(fingerprintSql).toContain("DROP COLUMN IF EXISTS nonce");
    expect(fingerprintSql).toContain("CREATE UNIQUE INDEX IF NOT EXISTS remote_authorization_nonce_fingerprint_unique");
    expect(nonceClaimsSql).toContain("CREATE TABLE IF NOT EXISTS remote_authorization_nonce_claims");
    expect(nonceClaimsSql).toContain("nonce_fingerprint text PRIMARY KEY");
    expect(nonceClaimsSql).toContain("request_fingerprint text NOT NULL");
    expect(nonceClaimsSql).not.toContain("nonce text");
    expect(decisionIdentitySql).toContain("ALTER COLUMN decision_id SET DEFAULT gen_random_uuid()");
    expect(decisionIdentitySql).toContain("ALTER COLUMN decision_id SET NOT NULL");
    expect(sql).toContain("card_transaction_event_id text PRIMARY KEY");
    expect(sql).toContain("merchant_category_code text NOT NULL");
    expect(sql).not.toContain("raw_body");
    expect(sql).not.toContain("merchant_name");
  });

  it("gates policy transfer dispatch until remote authorization is confirmed", async () => {
    const sql = await readFile(remoteAuthorizationDispatchGateMigrationUrl, "utf8");
    expect(sql).toContain("authorization_request_id text");
    expect(sql).toContain("dispatch_ready boolean NOT NULL DEFAULT true");
  });

  it("adds fail-closed decision columns to existing evidence schemas", async () => {
    const sql = await readFile(aiEvidenceDecisionGateMigrationUrl, "utf8");
    expect(sql).toContain("ADD COLUMN IF NOT EXISTS decision_allowed boolean NOT NULL DEFAULT false");
    expect(sql).toContain("ADD COLUMN IF NOT EXISTS decision_block_reason text");
  });

});
