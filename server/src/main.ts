import { loadConfig } from "./config.js";
import { createHttpServer } from "./http/server.js";
import { createDatabasePool } from "./db/pool.js";
import { HttpAirwallexGateway } from "./airwallex/client.js";
import { WebhookInboxWorker } from "./webhooks/worker.js";
import { ReconciliationWorker } from "./reconciliation/worker.js";
import { OutboxWorker } from "./outbox/worker.js";

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
const server = createHttpServer(config, pool, webhookWorker);
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Bridge ledger server listening on port ${config.port}`);
});
