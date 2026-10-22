import type { ServerResponse } from "node:http";
import { timingSafeEqual } from "node:crypto";
import type { IncomingMessage } from "node:http";
import type { DatabasePool } from "../../db/pool.js";
import { getCustomerSnapshotReadModel } from "../../snapshots/freshness.js";

export async function handleCustomerSnapshot(
  response: ServerResponse,
  pool: DatabasePool,
  customerId: string,
  authorization: string | undefined,
  productionReadToken: string | undefined,
): Promise<void> {
  try {
    const model = await getCustomerSnapshotReadModel(pool, customerId);
    if (model.snapshot?.sourceMode === "production_mcp" && !hasValidBearerToken(authorization, productionReadToken)) {
      response.writeHead(401, {
        "content-type": "application/json",
        "cache-control": "no-store",
        "www-authenticate": "Bearer",
      });
      response.end(JSON.stringify({ error: "snapshot_read_unauthorized" }));
      return;
    }
    response.writeHead(200, {
      "content-type": "application/json",
      "cache-control": "no-store",
    });
    response.end(JSON.stringify(model));
  } catch {
    response.writeHead(503, {
      "content-type": "application/json",
      "cache-control": "no-store",
    });
    response.end(JSON.stringify({ error: "customer_snapshot_unavailable" }));
  }
}

export function requestAuthorization(request: IncomingMessage): string | undefined {
  const authorization = request.headers.authorization;
  return Array.isArray(authorization) ? authorization[0] : authorization;
}

function hasValidBearerToken(authorization: string | undefined, expectedToken: string | undefined): boolean {
  if (!expectedToken || !authorization?.startsWith("Bearer ")) return false;
  const provided = Buffer.from(authorization.slice("Bearer ".length));
  const expected = Buffer.from(expectedToken);
  return provided.length === expected.length && timingSafeEqual(provided, expected);
}
