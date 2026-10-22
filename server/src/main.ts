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
  const snapshotWorker = new SnapshotSyncWorker({
    pool,
    source: createDemoSnapshotSource(),
    textLimits: parseTextLengthConfig(),
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
const server = createHttpServer(config, pool, webhookWorker);
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Bridge ledger server listening on port ${config.port}`);
});
