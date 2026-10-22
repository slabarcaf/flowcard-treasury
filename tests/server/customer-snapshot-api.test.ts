import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { createRequestHandler } from "../../server/src/http/server.js";
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import type { SalesforceCustomerSnapshot } from "../../server/src/salesforce/types.js";

const now = new Date();
const customerId = "acct_bloom001";
const payload = getDemoSalesforceSnapshot(customerId)!;

describe("customer snapshot read API", () => {
  it("returns a fresh version, age, source, and only canonical allowlisted data", async () => {
    const row = snapshotRow();
    row.canonical_payload = { ...payload, unexpectedPrivateField: "must not leak" } as SalesforceCustomerSnapshot;
    const { response, query } = await request(row, `/api/customers/${customerId}/snapshot`);
    const result = body(response);

    expect(response.writeHead).toHaveBeenCalledWith(200, expect.objectContaining({ "cache-control": "no-store" }));
    expect(result).toMatchObject({
      customerId,
      active: true,
      freshness: "fresh",
      ageSeconds: 600,
      snapshot: { snapshotId: "snapshot-1", version: 3, sourceMode: "demo", data: payload },
      latestAttempt: { status: "succeeded" },
    });
    expect(result.snapshot?.data).not.toHaveProperty("unexpectedPrivateField");
    expect(query).toHaveBeenCalledTimes(1);
    expect(query.mock.calls[0]?.[0]).toContain("FROM customer_snapshot_attempts");
  });

  it("reports a missing current version without fabricating canonical data", async () => {
    const row = snapshotRow({ snapshot_id: null, version: null, source_mode: null, schema_version: null,
      mapping_version: null, run_id: null, captured_at: null, published_at: null, canonical_payload: null,
      attempt_run_id: null, attempt_status: null, attempt_finished_at: null });
    const { response } = await request(row, `/api/customers/${customerId}/snapshot`);
    expect(body(response)).toMatchObject({ freshness: "missing", ageSeconds: null, snapshot: null, latestAttempt: null });
  });

  it("requires production authorization before querying even when no snapshot exists", async () => {
    const row = snapshotRow({ snapshot_id: null, version: null, source_mode: null, schema_version: null,
      mapping_version: null, run_id: null, captured_at: null, published_at: null, canonical_payload: null,
      attempt_run_id: null, attempt_status: null, attempt_finished_at: null });
    const { response, query } = await request(row, `/api/customers/${customerId}/snapshot`, undefined,
      "read-secret", "production_mcp");
    expect(response.writeHead).toHaveBeenCalledWith(401, expect.any(Object));
    expect(body(response)).toEqual({ error: "snapshot_read_unauthorized" });
    expect(query).not.toHaveBeenCalled();
  });

  it("allows demo snapshot reads without a bearer token", async () => {
    const { response } = await request(snapshotRow(), `/api/customers/${customerId}/snapshot`, undefined,
      "read-secret", "demo");
    expect(response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(body(response).active).toBe(true);
  });

  it("marks the prior version stale immediately after the latest exhausted attempt fails", async () => {
    const { response } = await request(snapshotRow({ attempt_run_id: "run-failed", attempt_status: "failed" }),
      `/api/customers/${customerId}/snapshot`);
    expect(body(response)).toMatchObject({ freshness: "stale", latestAttempt: { status: "failed" } });
  });

  it("marks a version stale once its capture age reaches 24 hours", async () => {
    const { response } = await request(snapshotRow({ captured_at: new Date(now.getTime() - 24 * 60 * 60 * 1000 - 1) }),
      `/api/customers/${customerId}/snapshot`);
    expect(body(response)).toMatchObject({ freshness: "stale", ageSeconds: 86_400 });
  });

  it("marks canonical data invalid when the approved cap is null", async () => {
    const invalid = structuredClone(payload);
    invalid.account.approvedBridgeCap.value = null;
    const { response } = await request(snapshotRow({ canonical_payload: invalid }), `/api/customers/${customerId}/snapshot`);
    expect(body(response)).toMatchObject({ freshness: "invalid" });
    expect(body(response).validationErrors).toContain("Account approved bridge cap is required");
  });

  it("reports deactivated customers without making their snapshot eligible", async () => {
    const { response } = await request(snapshotRow({ is_active: false }), `/api/customers/${customerId}/snapshot`);
    expect(body(response)).toMatchObject({ active: false, freshness: "deactivated" });
  });

  it("requires the production bearer token and accepts only the matching token", async () => {
    const productionPayload = { ...payload, mappingVerified: true };
    const row = snapshotRow({ source_mode: "production_mcp", canonical_payload: productionPayload });
    const denied = await request(row, `/api/customers/${customerId}/snapshot`, undefined, "read-secret", "production_mcp");
    expect(denied.response.writeHead).toHaveBeenCalledWith(401, expect.any(Object));
    expect(body(denied.response)).toEqual({ error: "snapshot_read_unauthorized" });

    const wrong = await request(row, `/api/customers/${customerId}/snapshot`, "Bearer wrong-secret", "read-secret", "production_mcp");
    expect(wrong.response.writeHead).toHaveBeenCalledWith(401, expect.any(Object));
    expect(body(wrong.response)).toEqual({ error: "snapshot_read_unauthorized" });

    const allowed = await request(row, `/api/customers/${customerId}/snapshot`, "Bearer read-secret", "read-secret", "production_mcp");
    expect(allowed.response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(body(allowed.response).snapshot?.data).toMatchObject({ mappingVerified: true });
  });

  it("keeps an unverified production mapping invalid even with a valid read token", async () => {
    const row = snapshotRow({ source_mode: "production_mcp", canonical_payload: { ...payload, mappingVerified: false } });
    const { response } = await request(row, `/api/customers/${customerId}/snapshot`, "Bearer read-secret", "read-secret", "production_mcp");
    expect(body(response)).toMatchObject({ freshness: "invalid" });
    expect(body(response).validationErrors).toContain("Production Salesforce mapping is not verified");
  });
});

async function request(
  row: ReturnType<typeof snapshotRow>,
  url: string,
  authorization?: string,
  productionReadToken?: string,
  snapshotMode: "demo" | "production_mcp" = "demo",
) {
  const query = vi.fn(async <Row>(): Promise<QueryResult<Row>> => ({ rows: [row as Row], rowCount: 1 }));
  const pool = { query } as unknown as DatabasePool;
  const handler = createRequestHandler({
    port: 3000,
    databaseUrl: "postgres://localhost/test",
    airwallexClientId: "client",
    airwallexApiKey: "key",
    airwallexWebhookSecret: "secret",
    salesforceSnapshotReadToken: productionReadToken,
    salesforceSnapshotMode: snapshotMode,
  }, pool);
  const response = { writeHead: vi.fn(), end: vi.fn() };
  handler({ method: "GET", url, headers: { authorization } } as never, response as never);
  await vi.waitFor(() => expect(response.end).toHaveBeenCalled());
  return { response, query };
}

function body(response: { end: ReturnType<typeof vi.fn> }) {
  return JSON.parse(response.end.mock.calls[0]?.[0] as string) as {
    freshness: string;
    ageSeconds: number | null;
    validationErrors: string[];
    snapshot: { data: SalesforceCustomerSnapshot | null } | null;
    [key: string]: unknown;
  };
}

interface SnapshotRow {
  is_active: boolean;
  snapshot_id: string | null;
  version: number | null;
  source_mode: "demo" | "production_mcp" | null;
  schema_version: number | null;
  mapping_version: string | null;
  run_id: string | null;
  captured_at: Date | null;
  published_at: Date | null;
  canonical_payload: SalesforceCustomerSnapshot | null;
  attempt_run_id: string | null;
  attempt_status: "succeeded" | "failed" | null;
  attempt_finished_at: Date | null;
}

function snapshotRow(overrides: Partial<SnapshotRow> = {}): SnapshotRow {
  return {
    is_active: true,
    snapshot_id: "snapshot-1",
    version: 3,
    source_mode: "demo",
    schema_version: 1,
    mapping_version: "salesforce-proposed-v1",
    run_id: "run-3",
    captured_at: new Date(now.getTime() - 10 * 60 * 1000),
    published_at: new Date(now.getTime() - 9 * 60 * 1000),
    canonical_payload: payload,
    attempt_run_id: "run-3",
    attempt_status: "succeeded",
    attempt_finished_at: new Date(now.getTime() - 9 * 60 * 1000),
    ...overrides,
  };
}
