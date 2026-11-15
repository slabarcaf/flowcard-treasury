import { loadConfig } from "./config.js";
import { createHttpServer } from "./http/server.js";
import { createDatabasePool } from "./db/pool.js";
import { HttpAirwallexUnderwritingGateway } from "./airwallex/client.js";
import { createDemoSnapshotSource, SnapshotSyncWorker } from "./snapshots/sync-worker.js";
import { startDemoSnapshotSync } from "./snapshots/runtime.js";
import { snapshotDailyRunTime } from "./snapshots/scheduler.js";
import { parseTextLengthConfig } from "./salesforce/validate.js";
import { runEvidenceReviewCli } from "./ai-evidence/review-cli.js";
import { DailyEvidenceRunner } from "./ai-evidence/runner.js";
import { FixtureEvidenceProvider } from "./ai-evidence/fixture-provider.js";
import { publishDemoUnderwritingProfiles } from "./underwriting/demo-fixtures.js";
import { runDailyUnderwritingProfileBuild } from "./underwriting/runner.js";
import { recordFailedProfileAttempt } from "./underwriting/publisher.js";
import { randomUUID } from "node:crypto";
import { OnboardingWebhookForwarder } from "./webhooks/onboarding-forwarder.js";
import { createGracefulShutdown } from "./lifecycle.js";

if (process.argv[2] && process.argv[2] !== "evidence-review") {
  process.stderr.write("Unsupported command: legacy funding and policy approval commands are retired.\n");
  process.exit(1);
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
let activeSchedule: { stop: () => void } | undefined;
if (process.env.SALESFORCE_SNAPSHOT_MODE?.trim() === "demo") {
  const evidenceRunner = new DailyEvidenceRunner({
    pool,
    provider: new FixtureEvidenceProvider(),
    packet: { maxPacketBytes: 32_000, maxNarrativeChars: 4_000 },
    reviewerId: process.env.AI_EVIDENCE_REVIEWER_ID?.trim(),
  });
  const snapshotWorker = new SnapshotSyncWorker({
    pool,
    source: createDemoSnapshotSource(),
    textLimits: parseTextLengthConfig(),
    onPublished: async (customer) => { await evidenceRunner.runCustomer(customer); },
  });
  const dailySnapshotWorker = {
    async run() {
      const snapshotResult = await snapshotWorker.run();
      await publishDemoUnderwritingProfiles(pool);
      return snapshotResult;
    },
  };
  activeSchedule = startDemoSnapshotSync(dailySnapshotWorker, {
    runAtUtc: snapshotDailyRunTime(),
    onError: (error) => console.error("Demo Salesforce snapshot sync failed", error),
  });
} else if (process.env.SALESFORCE_SNAPSHOT_MODE?.trim() === "production_mcp") {
  console.error("Production Salesforce snapshots are disabled: this server has no trusted host MCP runtime binding");
  const underwritingGateway = new HttpAirwallexUnderwritingGateway({
    baseUrl: config.airwallexBaseUrl,
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
  activeSchedule = startDemoSnapshotSync(dailyUnderwritingWorker, {
    runAtUtc: snapshotDailyRunTime(),
    onError: (error) => console.error("Production Airwallex underwriting sync failed", error),
  });
}
let onboardingForwarder: OnboardingWebhookForwarder | undefined;
if (config.onboardingServiceUrl && config.onboardingServiceSecret) {
  onboardingForwarder = new OnboardingWebhookForwarder({
    pool,
    onboardingServiceUrl: config.onboardingServiceUrl,
    serviceSecret: config.onboardingServiceSecret,
  });
  onboardingForwarder.start();
}

const server = createHttpServer(config, pool, onboardingForwarder);
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Compatibility API listening on port ${config.port}`);
});

const shutdown = createGracefulShutdown({
  server,
  pool,
  onboardingForwarder,
  snapshotSchedule: activeSchedule,
});

process.on("SIGTERM", () => { void shutdown(); });
process.on("SIGINT", () => { void shutdown(); });
