import { loadConfig } from "./config.js";
import { createHttpServer } from "./http/server.js";
import { createDatabasePool } from "./db/pool.js";
import { HttpAirwallexGateway, HttpAirwallexUnderwritingGateway } from "./airwallex/client.js";
import { WebhookInboxWorker } from "./webhooks/worker.js";
import { ReconciliationWorker } from "./reconciliation/worker.js";
import { OutboxWorker } from "./outbox/worker.js";
import { createDemoSnapshotSource, SnapshotSyncWorker } from "./snapshots/sync-worker.js";
import { startDemoSnapshotSync } from "./snapshots/runtime.js";
import { snapshotDailyRunTime } from "./snapshots/scheduler.js";
import { parseTextLengthConfig } from "./salesforce/validate.js";
import { runEvidenceReviewCli } from "./ai-evidence/review-cli.js";
import { DailyEvidenceRunner } from "./ai-evidence/runner.js";
import { FixtureEvidenceProvider } from "./ai-evidence/fixture-provider.js";
import { runPolicyApprovalCli } from "./policy/approval-cli.js";
import {
  createRemoteAuthorizationApprovalRevalidator,
  createRemoteAuthorizationResolver,
  createRemoteAuthorizationNonceClaimer,
  confirmRemoteAuthorizationDispatch,
  resolveStoredRemoteAuthorizationDecision,
} from "./policy/remote-authorization.js";
import { runDailyPolicyPrechecks } from "./policy/runner.js";
import { publishDemoUnderwritingProfiles } from "./underwriting/demo-fixtures.js";
import { runDailyUnderwritingProfileBuild } from "./underwriting/runner.js";
import { recordFailedProfileAttempt } from "./underwriting/publisher.js";
import { randomUUID } from "node:crypto";

if (process.argv[2] === "policy-approval") {
  const databaseUrl = process.env.DATABASE_URL?.trim();
  if (!databaseUrl) {
    process.stderr.write("DATABASE_URL is required\n");
    process.exit(1);
  }
  const approvalPool = await createDatabasePool({ databaseUrl });
  try {
    const approvalConfig = loadConfig();
    const approvalGateway = new HttpAirwallexGateway({
      baseUrl: process.env.AIRWALLEX_BASE_URL?.trim() || "https://api.sandbox.airwallex.com",
      clientId: approvalConfig.airwallexClientId,
      apiKey: approvalConfig.airwallexApiKey,
    });
    const approvalRevalidator = createRemoteAuthorizationApprovalRevalidator({
      pool: approvalPool,
      gateway: approvalGateway,
      repaymentTerm: approvalConfig.remoteAuthorizationRepaymentTerm,
    });
    await runPolicyApprovalCli(approvalPool, process.argv.slice(3), process.env, undefined, approvalRevalidator);
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  } finally {
    await approvalPool.end();
  }
  process.exit();
}

if (process.argv[2] === "evidence-review") {
  const databaseUrl = process.env.DATABASE_URL?.trim();
  if (!databaseUrl) {
    process.stderr.write("DATABASE_URL is required\n");
    process.exit(1);
  }
  const reviewPool = await createDatabasePool({ databaseUrl });
  try {
    await runEvidenceReviewCli(reviewPool, process.argv.slice(3), process.env);
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  } finally {
    await reviewPool.end();
  }
  process.exit();
}
const config = loadConfig();
const pool = await createDatabasePool(config);
const gateway = new HttpAirwallexGateway({
  baseUrl: process.env.AIRWALLEX_BASE_URL?.trim() || "https://api.sandbox.airwallex.com",
  clientId: config.airwallexClientId,
  apiKey: config.airwallexApiKey,
});
const webhookWorker = new WebhookInboxWorker(pool, gateway);
webhookWorker.start();
const outboxWorker = new OutboxWorker(pool, gateway);
outboxWorker.start();
const reconciliationWorker = new ReconciliationWorker(pool, gateway);
reconciliationWorker.start();
if (process.env.SALESFORCE_SNAPSHOT_MODE?.trim() === "demo") {
  const evidenceRunner = new DailyEvidenceRunner({
    pool,
    provider: new FixtureEvidenceProvider(),
    packet: { maxPacketBytes: 32_000, maxNarrativeChars: 4_000 },
  });
  const snapshotWorker = new SnapshotSyncWorker({
    pool,
    source: createDemoSnapshotSource(),
    textLimits: parseTextLengthConfig(),
    onPublished: async (customer) => { await evidenceRunner.runCustomer(customer); },
  });
  const dailyPolicyWorker = {
    async run() {
      const snapshotResult = await snapshotWorker.run();
      await publishDemoUnderwritingProfiles(pool);
      const results = await runDailyPolicyPrechecks({
        pool,
        facility: { facilitySize: 50_000, reserveFloorPct: 20, maxPerCustomerPct: 0.2,
          feeRatePer30Days: 0.015, minHistoryDays: 90 },
        mappings: {
          industry: { verified: true, version: "demo-industry-v1" }, industryExcluded: false,
          fraud: { verified: true, version: "demo-fraud-v1" }, fraudFlag: false,
          priorDefault: { verified: true, version: "demo-prior-default-v1" },
          concentration: { verified: true, version: "demo-concentration-v1" },
        },
        qualifications: {},
      });
      for (const result of results) {
        if (result.status === "failed") console.error("Demo policy precheck failed", result.customerId, result.reason);
      }
      return snapshotResult;
    },
  };
  const snapshotSchedule = startDemoSnapshotSync(dailyPolicyWorker, {
    runAtUtc: snapshotDailyRunTime(),
    onError: (error) => console.error("Demo Salesforce snapshot sync failed", error),
  });
  process.on("SIGTERM", () => snapshotSchedule.stop());
  process.on("SIGINT", () => snapshotSchedule.stop());
} else if (process.env.SALESFORCE_SNAPSHOT_MODE?.trim() === "production_mcp") {
  console.error("Production Salesforce snapshots are disabled: this server has no trusted host MCP runtime binding");
  const underwritingGateway = new HttpAirwallexUnderwritingGateway({
    baseUrl: process.env.AIRWALLEX_BASE_URL?.trim() || "https://api.sandbox.airwallex.com",
    clientId: config.airwallexClientId,
    apiKey: config.airwallexApiKey,
  });
  const dailyUnderwritingWorker = {
    async run() {
      const results = await runDailyUnderwritingProfileBuild({ pool, gateway: underwritingGateway, runId: randomUUID() });
      for (const result of results) {
        await recordFailedProfileAttempt(pool, {
          customerId: result.customerId, runId: result.candidate.runId, sourceMode: "production",
          startedAt: result.candidate.startedAt, finishedAt: new Date(),
          sourceVersions: result.candidate.sourceVersions, queryCompletion: result.candidate.queryCompletion,
          errorMessage: result.failure ?? "Trusted expected deposit dates are unavailable",
        });
      }
    },
  };
  const underwritingSchedule = startDemoSnapshotSync(dailyUnderwritingWorker, {
    runAtUtc: snapshotDailyRunTime(),
    onError: (error) => console.error("Production Airwallex underwriting sync failed", error),
  });
  process.on("SIGTERM", () => underwritingSchedule.stop());
  process.on("SIGINT", () => underwritingSchedule.stop());
}
const remoteAuthorizationResolver = createRemoteAuthorizationResolver({
  pool,
  gateway,
  repaymentTerm: config.remoteAuthorizationRepaymentTerm,
});
const remoteAuthorizationNonceClaimer = createRemoteAuthorizationNonceClaimer(pool);
const server = createHttpServer(
  config,
  pool,
  webhookWorker,
  (requestId) => resolveStoredRemoteAuthorizationDecision(pool, requestId),
  remoteAuthorizationResolver,
  remoteAuthorizationNonceClaimer,
  (request, signal) => confirmRemoteAuthorizationDispatch(pool, request, signal),
);
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Bridge ledger server listening on port ${config.port}`);
});
