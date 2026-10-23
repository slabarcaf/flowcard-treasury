import { createHmac } from "node:crypto";
import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { createRequestHandler } from "../../server/src/http/server.js";
import { handleAirwallexWebhook } from "../../server/src/http/routes/airwallex-webhook.js";
import { processWebhookEvent } from "../../server/src/webhooks/process.js";
import { verifyAirwallexSignature } from "../../server/src/webhooks/verify.js";
import { WebhookInboxWorker } from "../../server/src/webhooks/worker.js";

const secret = "notification-url-secret";
const body = Buffer.from('{ "id":"evt-1", "name":"connected_account_transfer.settled", "data":{"object":{"id":"tr_1","request_id":"bridge-123"}} }');
const timestamp = "1792600000000";
const signature = createHmac("sha256", secret).update(timestamp).update(body).digest("hex");

describe("Airwallex webhook signatures", () => {
  it("verifies the exact raw body and rejects changed bytes or missing headers", () => {
    expect(verifyAirwallexSignature({ rawBody: body, timestamp, signature, secret })).toBe(true);
    expect(verifyAirwallexSignature({ rawBody: Buffer.from(body.toString().replace(" ", "")), timestamp, signature, secret })).toBe(false);
    expect(verifyAirwallexSignature({ rawBody: body, timestamp: undefined, signature, secret })).toBe(false);
  });
});

describe("webhook inbox route", () => {
  it("rejects invalid signatures before database access", async () => {
    const pool = { query: vi.fn() } as unknown as DatabasePool;
    const mock = mockHttpRequest(body, timestamp, "bad-signature");
    await handleAirwallexWebhook(mock.request, mock.response, { pool, secret });
    expect(mock.response.writeHead).toHaveBeenCalledWith(401, expect.any(Object));
    expect(pool.query).not.toHaveBeenCalled();
    mock.finish();
  });

  it("persists a verified event once and acknowledges duplicate delivery", async () => {
    let inserted = true;
    const query = vi.fn(async () => ({ rows: inserted ? [{ id: "evt-1" }] : [], rowCount: inserted ? 1 : 0 }));
    const pool = { query } as unknown as DatabasePool;
    const first = mockHttpRequest(body, timestamp, signature);
    await handleAirwallexWebhook(first.request, first.response, { pool, secret });
    expect(first.response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(query).toHaveBeenCalledTimes(1);
    expect(query.mock.calls[0]?.[1]).toEqual(["evt-1", "connected_account_transfer.settled", body]);
    first.finish();
    inserted = false;
    const duplicate = mockHttpRequest(body, timestamp, signature);
    await handleAirwallexWebhook(duplicate.request, duplicate.response, { pool, secret });
    expect(duplicate.response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(query).toHaveBeenCalledTimes(2);
    duplicate.finish();
  });

  it("mounts POST /webhooks/airwallex in the application handler without a TCP listener", async () => {
    const query = vi.fn(async () => ({ rows: [{ id: "evt-1" }], rowCount: 1 }));
    const pool = { query } as unknown as DatabasePool;
    const handler = createRequestHandler({
      port: 3000, databaseUrl: "postgres://localhost/test", airwallexClientId: "client",
      airwallexApiKey: "api-key", airwallexWebhookSecret: secret,
    }, pool);
    const mock = mockHttpRequest(body, timestamp, signature);
    handler(mock.request, mock.response);
    await mock.done;
    expect(mock.response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(query).toHaveBeenCalledTimes(1);
    expect(query.mock.calls[0]?.[1]).toEqual(["evt-1", "connected_account_transfer.settled", body]);
  });

  it("processes a persisted pending row after the route has acknowledged it", async () => {
    const pendingBody = Buffer.from(JSON.stringify({
      id: "evt-deposit-1", name: "deposit.settled", accountId: "acct-provider",
      data: { id: "dep_1", status: "SETTLED", type: "BANK_TRANSFER", amount: 8_000,
        currency: "USD", global_account_id: "global-usd", reference: "kit5-demo-ref",
        settled_at: "2026-10-22T12:00:00Z" },
    }));
    const ts = "1792600000001";
    const sig = createHmac("sha256", secret).update(ts).update(pendingBody).digest("hex");
    const durable = durableInboxPool(pendingBody);
    const getDeposit = vi.fn(async (depositId: string, accountId: string) => {
      durable.state.depositLookup = [depositId, accountId];
      return { id: depositId, amountCents: 800_000, currency: "USD" as const, status: "pending" as const, settledAt: null };
    });
    const worker = new WebhookInboxWorker(durable.pool, {
      getTransferByRequestId: vi.fn(() => { throw new Error("not expected for deposit event"); }),
      getChargeByRequestId: vi.fn(() => { throw new Error("not expected for deposit event"); }),
      getDeposit,
    } as never);
    const nudge = vi.fn();
    const handler = createRequestHandler({
      port: 3000, databaseUrl: "postgres://localhost/test", airwallexClientId: "client",
      airwallexApiKey: "api-key", airwallexWebhookSecret: secret,
    }, durable.pool, { nudge } as never);
    const mock = mockHttpRequest(pendingBody, ts, sig);
    handler(mock.request, mock.response);
    await mock.done;
    expect(mock.response.writeHead).toHaveBeenCalledWith(200, expect.any(Object));
    expect(durable.state.processed).toBe(false);
    expect(nudge).toHaveBeenCalledTimes(1);

    expect(await worker.processPendingOnce()).toBe(true);
    expect(durable.state.processed).toBe(true);
    expect(durable.state.depositLookup).toEqual(["dep_1", "acct-provider"]);
    expect(await worker.processPendingOnce()).toBe(false);
  });
});

describe("webhook status processing", () => {
  it("binds the documented Global Account deposit payload to its saved Kit 5 operation", async () => {
    const eventBody = Buffer.from(JSON.stringify({
      id: "evt-nova-settled", name: "deposit.settled", accountId: "acct-provider",
      data: { id: "dep-nova-1", type: "BANK_TRANSFER", status: "SETTLED", amount: 8_000,
        currency: "USD", global_account_id: "global-usd", reference: "kit5-demo-ref",
        settled_at: "2026-10-22T12:00:00Z" },
    }));
    const db = kit5DepositWebhookPool(eventBody);
    const gateway = {
      getDeposit: vi.fn(async () => ({ id: "dep-nova-1", amountCents: 800_000, currency: "USD" as const,
        status: "settled" as const, settledAt: new Date("2026-10-22T12:00:00Z") })),
      getBalance: vi.fn(async () => ({ availableCents: 800_000, currency: "USD" as const })),
    };

    await processWebhookEvent(db.pool, gateway as never, "evt-nova-settled");

    expect(gateway.getDeposit).toHaveBeenCalledWith("dep-nova-1", "acct-provider");
    expect(db.deposit.status).toBe("settled");
    expect(db.eventBindings.get("evt-nova-settled")).toBe("repayment-operation");
    expect(db.statements.some(([sql, values]) => sql.includes("INSERT INTO kit5_repayment_deposit_events") &&
      values?.[0] === "evt-nova-settled")).toBe(true);
  });

  it("uses the provider's current status for an out-of-order notification", async () => {
    const eventBody = Buffer.from(JSON.stringify({
      id: "evt-old-settled", name: "connected_account_transfer.settled",
      created_at: "2026-10-21T00:00:00Z", data: { object: { id: "tr_1", request_id: "bridge-123", status: "SETTLED" } },
    }));
    const db = fakePool(eventBody);
    const gateway = {
      getTransferByRequestId: vi.fn(async () => ({ id: "tr_1", requestId: "bridge-123", status: "pending" as const })),
    };
    await processWebhookEvent(db.pool, gateway as never, "evt-old-settled");
    expect(gateway.getTransferByRequestId).toHaveBeenCalledWith("bridge-123");
    expect(db.statements.some(([sql, values]) => sql.includes("UPDATE provider_operations") && values?.[2] === "pending")).toBe(true);
    expect(db.statements.some(([sql, values]) => sql.includes("INSERT INTO ledger_events") && values?.[0] === "transfer_pending")).toBe(true);
    expect(db.statements.some(([sql, values]) => sql.includes("INSERT INTO ledger_events") && values?.[0] === "transfer_settled")).toBe(false);
  });

  it.each(["pending", "failed"] as const)("does not reduce ledger balances for a %s collection charge", async (status) => {
    const requestId = "fee-00000000-0000-4000-8000-000000000001";
    const eventBody = Buffer.from(JSON.stringify({
      id: `evt-charge-${status}`, name: `charge.${status}`,
      data: { object: { id: "ch_1", request_id: requestId, status: status.toUpperCase() } },
    }));
    const db = fakePool(eventBody, "fee");
    const gateway = {
      getChargeByRequestId: vi.fn(async () => ({ id: "ch_1", requestId, status })),
    };
    await processWebhookEvent(db.pool, gateway as never, `evt-charge-${status}`);
    expect(db.statements.some(([sql]) => sql.includes("INSERT INTO ledger_events"))).toBe(false);
    expect(db.statements.some(([sql, values]) => sql.includes("UPDATE provider_operations") && values?.[2] === status)).toBe(true);
  });

  it("applies a settled fee charge and fetches available balance before opening its database transaction", async () => {
    const requestId = "fee-00000000-0000-4000-8000-000000000001";
    const eventBody = Buffer.from(JSON.stringify({
      id: "evt-charge-settled", name: "charge.settled", created_at: "2026-10-10T00:00:00Z",
      data: { object: { id: "ch_1", request_id: requestId, status: "SETTLED" } },
    }));
    const db = fakePool(eventBody, "fee", { id: "attempt-1", deposit_id: "dep-1", account_id: "acct-1", deposit_amount_cents: 800_000, available_balance_cents: 800_000, fee_operation_id: "op-1" });
    const gateway = {
      getChargeByRequestId: vi.fn(async () => ({ id: "ch_1", requestId, status: "settled" as const })),
      getBalance: vi.fn(async () => {
        db.timeline.push("balance");
        return { availableCents: 800_000, currency: "USD" as const };
      }),
    };
    await processWebhookEvent(db.pool, gateway as never, "evt-charge-settled");

    expect(db.timeline.indexOf("balance")).toBeLessThan(db.timeline.indexOf("BEGIN"));
    expect(db.statements.some(([sql, values]) => sql.includes("INSERT INTO ledger_events") && values?.[0] === "fee_repaid")).toBe(true);
  });
});

function mockHttpRequest(raw: Buffer, ts: string, sig: string) {
  const request = new EventEmitter() as EventEmitter & { headers: Record<string, string>; method: string; url: string };
  Object.assign(request, { headers: { "x-timestamp": ts, "x-signature": sig }, method: "POST", url: "/webhooks/airwallex" });
  const writeHead = vi.fn();
  let complete!: () => void;
  const done = new Promise<void>((resolve) => { complete = resolve; });
  const end = vi.fn(() => complete());
  const response = { headersSent: false, writeHead, end };
  const finish = () => { request.emit("data", raw); request.emit("end"); };
  setImmediate(finish);
  return { request: request as never, response: response as never, done, finish: () => undefined };
}

function fakePool(rawBody: Buffer, operationType: "bridge" | "fee" = "bridge", attempt?: Record<string, unknown>) {
  const statements: Array<[string, readonly unknown[] | undefined]> = [];
  const timeline: string[] = [];
  const eventPayload = JSON.parse(rawBody.toString("utf8")) as { id: string; name: string };
  const operation = { id: "op-1", bridge_id: "bridge-uuid", operation_type: operationType, request_id: operationType === "bridge" ? "bridge-123" : "fee-00000000-0000-4000-8000-000000000001", provider_object_id: operationType === "bridge" ? "tr_1" : "ch_1", amount_cents: 500000 };
  const clientQuery = vi.fn(async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
    statements.push([sql, values]);
    if (sql === "BEGIN") timeline.push("BEGIN");
    if (sql.includes("FROM provider_operations")) return result([operation] as Row[]);
    if (sql.includes("FROM collection_attempts")) return result(attempt ? [attempt] as Row[] : []);
    if (sql.includes("FROM facility_configs")) return result([{ facility_amount_cents: 5000000, reserve_floor_bps: 2000 }] as Row[]);
    if (sql.includes("FROM bridge_cases")) return result([{ id: "bridge-uuid", customer_id: "customer-1" }] as Row[]);
    if (sql.includes("FROM ledger_events")) return result([
      { id: 1, event_type: "bridge_reserved", bridge_id: "bridge-uuid", amount_cents: 500000, currency: "USD", provenance: "approved_command", source_id: "reserved", payload: {}, occurred_at: new Date() },
      { id: 2, event_type: "transfer_pending", bridge_id: "bridge-uuid", amount_cents: 500000, currency: "USD", provenance: "provider_observation", source_id: "event", payload: {}, occurred_at: new Date() },
    ] as Row[]);
    if (sql.includes("INSERT INTO ledger_events")) return { rows: [{ id: 2 }] as Row[], rowCount: 1 };
    return { rows: [], rowCount: 1 };
  });
  const client: DatabaseClient = { query: clientQuery, release: vi.fn() };
  const pool: DatabasePool = {
    query: vi.fn(async <Row>(sql: string, values?: readonly unknown[]) => {
      statements.push([sql, values]);
      if (sql.includes("FROM webhook_inbox")) return result([{ provider_event_id: eventPayload.id, event_type: eventPayload.name, raw_body: rawBody, processed_at: null }] as Row[]);
      if (sql.includes("FROM provider_operations")) return result([operation] as Row[]);
      if (sql.includes("FROM collection_attempts")) return result(attempt ? [attempt] as Row[] : []);
      return { rows: [], rowCount: 1 };
    }),
    connect: vi.fn(async () => client), end: vi.fn(async () => undefined),
  };
  return { pool, statements, timeline };
}

function durableInboxPool(rawBody: Buffer) {
  const state: { rawBody: Buffer; inserted: boolean; processing: boolean; processed: boolean; observed: boolean;
    depositLookup?: [string, string] } = { rawBody, inserted: false, processing: false, processed: false, observed: false };
  const inboxRow = () => [{
    provider_event_id: "evt-deposit-1", event_type: "deposit.settled", raw_body: state.rawBody,
    processed_at: state.processed ? new Date() : null,
  }];
  const client: DatabaseClient = {
    query: vi.fn(async <Row>(sql: string): Promise<QueryResult<Row>> => {
      if (sql.includes("WITH candidate AS")) {
        if (!state.inserted || state.processed || state.processing) return result([]);
        state.processing = true;
        return result([{ provider_event_id: "evt-deposit-1" }] as Row[]);
      }
      if (sql.includes("INSERT INTO provider_observations")) {
        state.observed = true;
        return { rows: [], rowCount: 1 };
      }
      if (sql.includes("SET processed_at = now()")) {
        state.processed = true;
        state.processing = false;
        return { rows: [], rowCount: 1 };
      }
      return { rows: [], rowCount: 1 };
    }),
    release: vi.fn(),
  };
  const pool: DatabasePool = {
    query: vi.fn(async <Row>(sql: string): Promise<QueryResult<Row>> => {
      if (sql.includes("INSERT INTO webhook_inbox")) {
        if (state.inserted) return result([]);
        state.inserted = true;
        return result([{ id: "evt-deposit-1" }] as Row[]);
      }
      if (sql.includes("FROM customer_roster")) return result([{ customer_id: "acct-nova" }] as Row[]);
      if (sql.includes("FROM webhook_inbox")) return result(state.inserted ? inboxRow() as Row[] : []);
      return { rows: [], rowCount: 1 };
    }),
    connect: vi.fn(async () => client),
    end: vi.fn(async () => undefined),
  };
  return { pool, state };
}

function kit5DepositWebhookPool(rawBody: Buffer) {
  const event = JSON.parse(rawBody.toString("utf8")) as { id: string; name: string };
  const deposit = {
    id: "repayment-operation", scenario_id: "kit5-target", customer_id: "acct-nova",
    action_id: "nova-action", bridge_id: "00000000-0000-4000-8000-000000000001",
    account_id: "acct-provider", global_account_id: "global-usd", amount_cents: 800_000,
    currency: "USD", statement_ref: "kit5-demo-ref", status: "created",
    provider_deposit_id: "dep-nova-1", provider_result: { status: "pending" }, error: null,
    settled_at: null as Date | null,
  };
  const eventBindings = new Map<string, string>();
  const statements: Array<[string, readonly unknown[] | undefined]> = [];
  const client: DatabaseClient = {
    query: vi.fn(async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
      statements.push([sql, values]);
      if (["BEGIN", "COMMIT", "ROLLBACK"].includes(sql)) return result<Row>([]);
      if (sql.includes("INSERT INTO kit5_repayment_deposit_events")) {
        eventBindings.set(String(values?.[0]), String(values?.[1]));
        return result<Row>([]);
      }
      if (sql.includes("FROM kit5_repayment_deposit_events")) {
        const operationId = eventBindings.get(String(values?.[0]));
        return result(operationId ? [{ deposit_operation_id: operationId } as Row] : []);
      }
      if (sql.includes("UPDATE kit5_repayment_deposits SET provider_deposit_id = COALESCE")) {
        deposit.status = "settled";
        deposit.settled_at = values?.[2] as Date;
        return result([deposit as Row]);
      }
      if (sql.includes("FROM bridge_cases")) return result<Row>([]);
      return result<Row>([]);
    }),
    release: vi.fn(),
  };
  const pool: DatabasePool = {
    query: vi.fn(async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
      statements.push([sql, values]);
      if (sql.includes("FROM webhook_inbox")) return result([{
        provider_event_id: event.id, event_type: event.name, raw_body: rawBody, processed_at: null,
      } as Row]);
      if (sql.includes("FROM customer_roster")) return result([{ customer_id: "acct-nova" } as Row]);
      if (sql.includes("WHERE (provider_deposit_id = $1")) return result([deposit as Row]);
      return result<Row>([]);
    }),
    connect: vi.fn(async () => client),
    end: vi.fn(async () => undefined),
  };
  return { pool, deposit, eventBindings, statements };
}

function result<Row>(rows: Row[]): QueryResult<Row> { return { rows, rowCount: rows.length }; }
