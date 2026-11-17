import { loadConfig } from "./config.js";
import { createHttpServer } from "./http/server.js";
import { createDatabasePool } from "./db/pool.js";
import { createGracefulShutdown } from "./lifecycle.js";

if (process.argv[2]) {
  process.stderr.write("Unsupported command: legacy funding, snapshots and evidence review are retired.\n");
  process.exit(1);
}

const config = loadConfig();
const pool = await createDatabasePool(config);
const server = createHttpServer(config, pool);
server.listen(config.port, "0.0.0.0", () => {
  console.log(`Compatibility API listening on port ${config.port}`);
});

const shutdown = createGracefulShutdown({
  server,
  pool,
});

process.on("SIGTERM", () => { void shutdown(); });
process.on("SIGINT", () => { void shutdown(); });
