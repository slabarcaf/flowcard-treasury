import { readFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { describe, expect, it, beforeAll, afterAll } from "vitest";
import type { AirwallexGateway } from "../../../server/src/airwallex/client.js";
import { createDatabasePool, type DatabasePool } from "../../../server/src/db/pool.js";
import { persistExtraction } from "../../../server/src/ai-evidence/repository.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import { evaluateQualification } from "../../../server/src/ai-evidence/qualification.js";
import { enrollCustomer, bindIssuingCard } from "../../../server/src/roster/repository.js";
import { publishCustomerSnapshot } from "../../../server/src/snapshots/publisher.js";
import { publishCustomerProfile } from "../../../server/src/underwriting/publisher.js";
import { UNDERWRITING_FEATURE_VERSION, UNDERWRITING_PROFILE_SCHEMA_VERSION, type UnderwritingProfileCandidate } from "../../../server/src/underwriting/types.js";
import { runDailyPolicyPrechecks } from "../../../server/src/policy/runner.js";
import { createRemoteAuthorizationResolver } from "../../../server/src/policy/remote-authorization.js";
import type { NoGoMappingReadiness } from "../../../server/src/policy/gates.js";
import type { SalesforceCustomerSnapshot } from "../../../server/src/salesforce/types.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import type { AirwallexRemoteAuthorizationV2 } from "../../../server/src/http/routes/remote-authorization.js";

const databaseUrl = process.env.POLICY_ACCEPTANCE_DATABASE_URL;
if (!databaseUrl && process.env.POLICY_ACCEPTANCE_REQUIRED === "1") {
  throw new Error("POLICY_ACCEPTANCE_DATABASE_URL is required for PostgreSQL policy acceptance tests");
}
const migrationFiles = [
  "001_bridge_ledger.sql", "002_customer_roster.sql", "003_salesforce_snapshots.sql",
  "004_ai_evidence.sql", "005_underwriting_profiles.sql", "006_policy_evaluations.sql",
  "007_policy_request_evaluations.sql", "008_remote_authorization.sql", "009_policy_request_ledger_sequence.sql",
];
const now = new Date();
const nowIso = now.toISOString();
const originalSnapshotMode = process.env.SALESFORCE_SNAPSHOT_MODE;
const verifiedMappings: NoGoMappingReadiness = {
  industry: { verified: true, version: "fixture-industry-v1" }, industryExcluded: false,
  fraud: { verified: true, version: "fixture-fraud-v1" }, fraudFlag: false,
  priorDefault: { verified: true, version: "fixture-default-v1" },
  concentration: { verified: true, version: "fixture-concentration-v1" },
};

const acceptance = databaseUrl ? describe : describe.skip;

acceptance("policy end-to-end acceptance (isolated PostgreSQL)", () => {
  let pool: DatabasePool;
  let customerId: string;
  let snapshotId: string;
  let extractionId: string;
  let cardId: string;

  beforeAll(async () => {
    if (!databaseUrl) return;
    const parsed = new URL(databaseUrl);
    if (!parsed.pathname.toLowerCase().includes("test") || !["localhost", "127.0.0.1", "::1"].includes(parsed.hostname)) {
      throw new Error("POLICY_ACCEPTANCE_DATABASE_URL must target a localhost database whose name includes 'test'");
    }
    pool = await createDatabasePool({ databaseUrl });
    const schema = await pool.query<{ exists: string | null }>(
      "SELECT to_regclass('public.customer_roster') AS exists",
    );
    if (!schema.rows[0]?.exists) {
      for (const file of migrationFiles.slice(0, -1)) {
        await pool.query(await readFile(new URL(`../../../server/src/db/migrations/${file}`, import.meta.url), "utf8"));
      }
    }
    await pool.query(await readFile(new URL(`../../../server/src/db/migrations/${migrationFiles.at(-1)}`, import.meta.url), "utf8"));
    await pool.query(`TRUNCATE TABLE customer_roster, bridge_cases, ledger_events, provider_operations,
      outbox_commands, webhook_inbox, provider_observations, facility_projections, facility_configs CASCADE`);
    await pool.query(`INSERT INTO facility_configs (id, version, currency, facility_amount_cents, reserve_floor_bps)
      VALUES ('acceptance-usd', 1, 'USD', 5000000, 2000)`);
    await pool.query(`INSERT INTO ledger_events (event_type, amount_cents, currency, provenance, source_id, payload, occurred_at)
      VALUES ('opening_exposure', 2500000, 'USD', 'test_fixture', 'acceptance-opening', '{}'::jsonb, $1)`, [now]);
    await pool.query(`INSERT INTO facility_projections (currency, facility_amount_cents, reserve_floor_cents,
      opening_exposure_cents, settled_principal_cents, active_reservations_cents, deployable_cents)
      VALUES ('USD', 5000000, 1000000, 2500000, 0, 0, 1500000)`);

    customerId = `acceptance-${randomUUID()}`;
    cardId = `card-${randomUUID()}`;
    const rosterClient = await pool.connect();
    let enrolled;
    try {
      enrolled = await enrollCustomer(rosterClient, {
        customerId, salesforceAccountId: `sf-${customerId}`,
        airwallexConnectedAccountId: `awx-${customerId}`, actor: "acceptance-test", reason: "synthetic acceptance fixture",
      });
    } finally {
      rosterClient.release();
    }
    await bindIssuingCard(pool, {
      customerId, cardId, accountId: enrolled.airwallexConnectedAccountId,
      actor: "acceptance-test", reason: "synthetic acceptance fixture",
    });
    const snapshot = completeSnapshot(enrolled.salesforceAccountId);
    const publishedSnapshot = await publishCustomerSnapshot(pool, snapshotCandidate(customerId, snapshot));
    snapshotId = publishedSnapshot.snapshotId;
    const publishedProfile = await publishCustomerProfile(pool, profileCandidate(customerId));
    expect(publishedProfile.status).toBe("published");
    if (publishedProfile.status !== "published") throw new Error("Fixture profile was not published");
    const evidence: ExtractionResult = {
      schemaVersion: 1, customerId, snapshotId, status: "no_signal", signals: [], unmapped: [],
      model: { modelId: "synthetic-fixture-v1", promptVersion: "fixture-v1", catalogVersion: "proposed-v1",
        outputSchemaVersion: 1, packetBuilderVersion: "fixture-v1", calibrationVersion: null },
    };
    extractionId = await persistExtraction(pool, evidence, "a".repeat(64), "fixture-config-v1");
  }, 30_000);

  afterAll(async () => {
    if (pool) await pool.end();
    if (originalSnapshotMode === undefined) delete process.env.SALESFORCE_SNAPSHOT_MODE;
    else process.env.SALESFORCE_SNAPSHOT_MODE = originalSnapshotMode;
  });

  it("evaluates a roster customer from versioned sources through request audit and atomic ledger reservation", async () => {
    viDemoMode();
    const results = await runDailyPolicyPrechecks({
      pool, facility: { facilitySize: 50_000, reserveFloorPct: 20, maxPerCustomerPct: 0.2, feeRatePer30Days: 0.015, minHistoryDays: 90 },
      mappings: verifiedMappings, qualifications: {}, now,
    });
    expect(results).toEqual([expect.objectContaining({ customerId, status: "evaluated", outcome: "ready" })]);

    const daily = await pool.query<{ evaluation_id: string; profile_id: string; snapshot_id: string;
      evidence_run_id: string; policy_version: string; input_versions: Record<string, unknown> }>(
      `SELECT evaluation_id, profile_id, snapshot_id, evidence_run_id, policy_version, input_versions
         FROM policy_evaluations WHERE customer_id = $1`, [customerId]);
    expect(daily.rows).toHaveLength(1);
    expect(daily.rows[0]).toMatchObject({ profile_id: expect.any(String), snapshot_id: snapshotId,
      evidence_run_id: extractionId, policy_version: expect.any(String) });
    expect(daily.rows[0].input_versions).toMatchObject({
      rosterUpdatedAt: expect.any(String), profileId: daily.rows[0].profile_id,
      profileVersion: 1, profileFeatureVersion: UNDERWRITING_FEATURE_VERSION,
      profileAccountSourceVersion: "fixture-account-v1", profileBalancesSourceVersion: "fixture-balances-v1",
      profileTransactionsSourceVersion: "fixture-transactions-v1",
      profileExpectedInflowsSourceVersion: "fixture-expected-dates-v1", profileRepaymentsSourceVersion: "fixture-ledger-v1",
      salesforceSnapshotId: snapshotId, salesforceSnapshotVersion: 1,
      salesforceMappingVersion: "acceptance-salesforce-v1", evidenceRunId: extractionId,
      evidenceConfigurationVersion: "fixture-config-v1", evidenceCatalogVersion: "proposed-v1",
      evidenceSchemaVersion: 1, ledgerSequence: expect.any(String),
    });

    const request: AirwallexRemoteAuthorizationV2 = {
      version: 2, accountId: `awx-${customerId}`, cardId,
      cardTransactionEventId: randomUUID(), cardTransactionId: randomUUID(), cardTransactionLifecycleId: randomUUID(),
      transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", transactionAmount: 100,
      transactionCurrency: "USD", merchantCategoryCode: "5411",
    };
    const fakeGateway = { getBalance: async () => ({ availableCents: 0, currency: "USD" as const }) } as unknown as AirwallexGateway;
    const resolve = createRemoteAuthorizationResolver({ pool, gateway: fakeGateway, repaymentTerm: "asap", now: () => now });
    const outcome = await resolve(request, `${now.getTime()}.synthetic-acceptance-nonce`, new AbortController().signal);
    expect(outcome).toEqual({ responseStatus: "AUTHORIZED", statusReason: "auto_approved" });

    const persisted = await pool.query<{ outcome: string; evaluation_id: string; request_id: string; ledger_sequence: string }>(
      `SELECT outcome, evaluation_id, request_id, ledger_sequence::text AS ledger_sequence
         FROM policy_request_evaluations WHERE request_id = $1`,
      [request.cardTransactionEventId]);
    expect(persisted.rows).toEqual([expect.objectContaining({ outcome: "auto_approved", evaluation_id: daily.rows[0].evaluation_id,
      ledger_sequence: daily.rows[0].input_versions.ledgerSequence })]);
    const reservation = await pool.query<{ amount_cents: number; customer_id: string }>(
      `SELECT amount_cents::int AS amount_cents, customer_id FROM bridge_cases WHERE customer_id = $1`, [customerId]);
    expect(reservation.rows).toEqual([{ amount_cents: 10000, customer_id: customerId }]);
    const outbox = await pool.query<{ command_type: string; payload: { amountCents: number } }>(
      `SELECT command_type, payload FROM outbox_commands`);
    expect(outbox.rows).toEqual([{ command_type: "create_bridge_transfer", payload: expect.objectContaining({ amountCents: 10000 }) }]);
  });

  it("accepts only evidence types whose qualification is backed by enough exact audit examples", () => {
    const examples = Array.from({ length: 1000 }, (_, index) => ({
      signalType: "expected_inflow_delay" as const, correct: true, labelId: `label-${index}`,
      labeledAt: nowIso, calibrationVersion: "cal-v1",
    }));
    const qualification = evaluateQualification("expected_inflow_delay", "cal-v1", examples, nowIso);
    expect(qualification).toMatchObject({ qualified: true, mode: "auto_apply_with_audit", sampleCount: 1000 });
  });

  it("blocks each customer's missing, stale, unqualified, review, and no-go inputs independently", async () => {
    viDemoMode();
    const cases = [
      await seedCustomer(pool, { profile: false, snapshot: false, evidence: false }),
      await seedCustomer(pool, { profile: true, snapshot: false, evidence: false }),
      await seedCustomer(pool, { profile: true, snapshot: true, evidence: false }),
      await seedCustomer(pool, { profileAgeMs: 25 * 60 * 60 * 1000, snapshot: true, evidence: "no_signal" }),
      await seedCustomer(pool, { profile: true, snapshotAgeMs: 25 * 60 * 60 * 1000, evidence: "no_signal" }),
      await seedCustomer(pool, { profile: true, snapshot: true, evidence: "unqualified" }),
      await seedCustomer(pool, { profile: true, snapshot: true, evidence: "review_required" }),
      await seedCustomer(pool, { profile: true, snapshot: true, evidence: "no_signal", priorDefault: "yes" }),
    ];
    const results = await runDailyPolicyPrechecks({
      pool, facility: { facilitySize: 50_000, reserveFloorPct: 20, maxPerCustomerPct: 0.2,
        feeRatePer30Days: 0.015, minHistoryDays: 90 },
      mappings: verifiedMappings, qualifications: {}, now,
    });
    const byCustomer = new Map(results.map((result) => [result.customerId, result]));
    for (const fixture of cases) expect(byCustomer.get(fixture.customerId)).toMatchObject(fixture.expected);
    expect(byCustomer.get(customerId)).toMatchObject({ status: "evaluated", outcome: "ready" });
  });

  it("blocks each mapped no-go fact in an isolated customer evaluation", async () => {
    viDemoMode();
    const scenarios: Array<{ label: string; outcome: "blocked" | "ineligible"; profileData?: Partial<UnderwritingProfileCandidate["data"]>; mappings: NoGoMappingReadiness }> = [
      { label: "frozen account", outcome: "blocked", profileData: { connectedAccountStatus: "frozen" }, mappings: verifiedMappings },
      { label: "closed account", outcome: "blocked", profileData: { connectedAccountStatus: "closed" }, mappings: verifiedMappings },
      { label: "open RFI", outcome: "blocked", profileData: { openRfi: true }, mappings: verifiedMappings },
      { label: "payer concentration", outcome: "ineligible", profileData: { topPayerConcentration: 0.71 }, mappings: verifiedMappings },
      { label: "excluded industry", outcome: "ineligible", mappings: { ...verifiedMappings, industryExcluded: true } },
      { label: "fraud flag", outcome: "ineligible", mappings: { ...verifiedMappings, fraudFlag: true } },
    ];
    for (const scenario of scenarios) {
      const fixture = await seedCustomer(pool, { profile: true, snapshot: true, evidence: "no_signal", profileData: scenario.profileData });
      const results = await runDailyPolicyPrechecks({
        pool, facility: { facilitySize: 50_000, reserveFloorPct: 20, maxPerCustomerPct: 0.2,
          feeRatePer30Days: 0.015, minHistoryDays: 90 },
        mappings: scenario.mappings, qualifications: {}, now, customerIds: [fixture.customerId],
      });
      expect(results, scenario.label).toEqual([expect.objectContaining({
        customerId: fixture.customerId, status: "evaluated", outcome: scenario.outcome,
      })]);
    }
  });

  it("keeps browser input restricted to a trusted request identifier", async () => {
    const { handleBridgeDecision } = await import("../../../server/src/http/routes/bridge-decision.js");
    const { Readable } = await import("node:stream");
    const request = Readable.from([JSON.stringify({ requestId: "request-1", amountCents: 1,
      ratings: { inflowReliability: "green" }, tier: "A", creditLineCents: 999999,
      transferCommand: { amountCents: 1 } })]) as never;
    const response = {
      status: 0, headersSent: false, body: "",
      writeHead(status: number) { this.status = status; this.headersSent = true; },
      end(body: string) { this.body = body; },
    };
    let resolverCalled = false;
    await handleBridgeDecision(request, response as never, async () => {
      resolverCalled = true;
      throw new Error("must not resolve client policy fields");
    });
    expect(response.status).toBe(400);
    expect(resolverCalled).toBe(false);
  });
});

function viDemoMode(): void { process.env.SALESFORCE_SNAPSHOT_MODE = "demo"; }

function completeSnapshot(accountId: string): SalesforceCustomerSnapshot {
  const fixture = getDemoSalesforceSnapshot("acct_atlas001");
  if (!fixture) throw new Error("Atlas Salesforce fixture is unavailable");
  return {
    ...fixture, mappingVersion: "acceptance-salesforce-v1", mappingVerified: true,
    account: { ...fixture.account, recordId: accountId,
      customerStatus: { ...fixture.account.customerStatus, value: "active" },
      kybStatus: { ...fixture.account.kybStatus, value: "verified" },
      creditReviewStatus: { ...fixture.account.creditReviewStatus, value: "approved" },
      approvedBridgeCap: { ...fixture.account.approvedBridgeCap, value: 8000 },
      priorDefaultStatus: { ...fixture.account.priorDefaultStatus, value: "no" } },
    contract: { ...fixture.contract, endDate: { ...fixture.contract.endDate, value: "2027-10-20" } },
  };
}

function snapshotCandidate(customerId: string, payload: SalesforceCustomerSnapshot, capturedAt = now) {
  const complete = { complete: true, pages: 1 };
  return {
    customerId, runId: `acceptance-snapshot-${customerId}`, sourceMode: "demo" as const,
    capturedAt, startedAt: capturedAt, queryCompletion: { account: complete, contract: complete, case: complete, activity: complete },
    recordCounts: { account: 1, contract: 1, case: 0, activity: 0 }, payload,
  };
}

function profileCandidate(customerId: string, capturedAt = now, dataOverrides: Partial<UnderwritingProfileCandidate["data"]> = {}): UnderwritingProfileCandidate {
  const complete = { complete: true, pages: 1 };
  const windowEnd = new Date(now.getTime());
  return {
    customerId, runId: `acceptance-profile-${customerId}`, sourceMode: "demo", capturedAt, startedAt: capturedAt,
    sourceVersions: { account: "fixture-account-v1", balances: "fixture-balances-v1", transactions: "fixture-transactions-v1",
      expected_inflows: "fixture-expected-dates-v1", repayments: "fixture-ledger-v1" },
    queryCompletion: { account: complete, balances: complete, transactions: complete,
      expected_inflows: complete, repayments: complete }, schemaVersion: UNDERWRITING_PROFILE_SCHEMA_VERSION,
    featureVersion: UNDERWRITING_FEATURE_VERSION, windowStart: new Date(windowEnd.getTime() - 90 * 86_400_000), windowEnd,
    data: { averageMonthlySettledInflowsCents: 10_000_000, expectedDateReliability: 0.95,
      topPayerConcentration: 0.3, lowBalanceDays: 3, historyDays: 90, eligibleSpend: "supported",
      connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed", ...dataOverrides },
  };
}

async function seedCustomer(pool: DatabasePool, input: {
  profile?: boolean;
  snapshot?: boolean;
  evidence?: false | "no_signal" | "unqualified" | "review_required";
  profileAgeMs?: number;
  snapshotAgeMs?: number;
  priorDefault?: "yes" | "no";
  profileData?: Partial<UnderwritingProfileCandidate["data"]>;
}): Promise<{ customerId: string; expected: Record<string, unknown> }> {
  const id = `acceptance-${randomUUID()}`;
  const client = await pool.connect();
  let accountId: string;
  try {
    const roster = await enrollCustomer(client, {
      customerId: id, salesforceAccountId: `sf-${id}`, airwallexConnectedAccountId: `awx-${id}`,
      actor: "acceptance-test", reason: "synthetic gate fixture",
    });
    accountId = roster.salesforceAccountId;
  } finally {
    client.release();
  }

  let expected: Record<string, unknown>;
  if (input.profile === false) {
    expected = { status: "waiting", reason: "profile_missing" };
    return { customerId: id, expected };
  }
  const profileCapturedAt = new Date(now.getTime() - (input.profileAgeMs ?? 0));
  await publishCustomerProfile(pool, profileCandidate(id, profileCapturedAt, input.profileData));
  if (input.snapshot === false) {
    expected = { status: "waiting", reason: "snapshot_missing" };
    return { customerId: id, expected };
  }
  const snapshotCapturedAt = new Date(now.getTime() - (input.snapshotAgeMs ?? 0));
  const payload = completeSnapshot(accountId);
  if (input.priorDefault) {
    payload.account.priorDefaultStatus = { ...payload.account.priorDefaultStatus, value: input.priorDefault };
  }
  const snapshot = await publishCustomerSnapshot(pool, snapshotCandidate(id, payload, snapshotCapturedAt));
  if (input.evidence === false) {
    expected = { status: "waiting", reason: "evidence_missing" };
    return { customerId: id, expected };
  }
  const status: ExtractionResult["status"] = input.evidence === "unqualified" ? "success" : input.evidence ?? "no_signal";
  const result: ExtractionResult = {
    schemaVersion: 1, customerId: id, snapshotId: snapshot.snapshotId, status,
    signals: status === "unqualified" ? [{ type: "expected_inflow_delay", value: { delayed: true }, counterparty: null,
      evidence: [], quotes: [], rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: "fixture" }] : [],
    unmapped: [],
    model: { modelId: "synthetic-fixture-v1", promptVersion: "fixture-v1", catalogVersion: "proposed-v1",
      outputSchemaVersion: 1, packetBuilderVersion: "fixture-v1", calibrationVersion: status === "unqualified" ? "cal-v1" : null },
  };
  await persistExtraction(pool, result, randomUUID().replaceAll("-", "").padEnd(64, "a").slice(0, 64), "fixture-config-v1");
  if (input.profileAgeMs || input.snapshotAgeMs) {
    expected = { status: "evaluated", outcome: "blocked" };
  } else if (status === "unqualified" || status === "review_required") {
    expected = { status: "evaluated", outcome: "blocked" };
  } else if (input.priorDefault === "yes") {
    expected = { status: "evaluated", outcome: "ineligible" };
  } else {
    expected = { status: "evaluated", outcome: "ready" };
  }
  return { customerId: id, expected };
}
