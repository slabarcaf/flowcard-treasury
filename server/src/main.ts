import { loadConfig } from "./config.js";
import { createHttpServer } from "./http/server.js";
import { createDatabasePool } from "./db/pool.js";
import { HttpAirwallexGateway } from "./airwallex/client.js";
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
  const snapshotSchedule = startDemoSnapshotSync(snapshotWorker, {
    runAtUtc: snapshotDailyRunTime(),
    onError: (error) => console.error("Demo Salesforce snapshot sync failed", error),
  });
  process.on("SIGTERM", () => snapshotSchedule.stop());
  process.on("SIGINT", () => snapshotSchedule.stop());
} else if (process.env.SALESFORCE_SNAPSHOT_MODE?.trim() === "production_mcp") {
  console.error("Production Salesforce snapshots are disabled: this server has no trusted host MCP runtime binding");
}
const server = createHttpServer(config, pool, webhookWorker, {
  pool,
  gateway,
  // Provider-issued customer/card mappings remain a sandbox acceptance gate.
  // Keep the endpoint mounted, but fail closed until those mappings are configured.
  resolveScenario: () => null,
  operatorAuth: {
    operatorToken: config.kit5OperatorToken,
    allowedOrigins: config.kit5AllowedOrigins,
  },
});
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Bridge ledger server listening on port ${config.port}`);
});
