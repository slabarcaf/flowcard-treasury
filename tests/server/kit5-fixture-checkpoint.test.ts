import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { ProviderHttpError, type AirwallexGateway, type IssuingSimulationGateway } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import type { Kit5CardAttempt } from "../../server/src/db/repositories.js";
import type { Kit5BridgeFundingBoundary } from "../../server/src/demo/next-customer.js";
import type { Kit5DemoRouteDependencies } from "../../server/src/http/routes/kit5-demo.js";
import { createKit5FixtureCheckpointResolver } from "../../server/src/demo/kit5-scenario-resolver.js";
import type { Kit5CustomerFixture } from "../../server/src/demo/next-customer.js";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie } from "../../server/src/http/kit5-operator-auth.js";

const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
const origin = "https://demo.example";
const runId = "00000000-0000-4000-8000-000000000001";
const providerLifecycleId = "33333333-3333-4333-8333-333333333333";
const scenarioId = `kit5-target:${runId}`;
const customerId = "acct_bloom001";
const actionId = `${scenarioId}:fixture-checkpoint:txn_bloom_supplier_001`;
const customerIds = [customerId, "acct_atlas001", "acct_nova001", "acct_pixel001", "acct_juniper001"];

function binding(customer: string, overrides: Record<string, unknown> = {}) {
  return {
    customer_id: customer,
    roster_account_id: `verified-account-${customer}`,
    roster_active: true,
    card_id: `verified-card-${customer}`,
    binding_account_id: `verified-account-${customer}`,
    binding_active: true,
    ...overrides,
  };
}

function matchingCheckpointLifecycle(lifecycleId: string, overrides: Record<string, unknown> = {}) {
  return {
    process_result: "DECLINED",
    failure_reason: "INSUFFICIENT_FUNDS",
    id: "evt_bloom_reconciled",
    card_transaction_id: "txn_bloom_reconciled",
    lifecycle_id: lifecycleId,
    account_id: `verified-account-${customerId}`,
    card_id: `verified-card-${customerId}`,
    transaction_amount: 9_000,
    transaction_currency: "USD",
    merchant: { name: "Supplier", category_code: "5411" },
    type: "AUTHORIZATION",
    transaction_category: "PURCHASE",
    ...overrides,
  };
}

function matchingCheckpointSimulation(lifecycleId: string, overrides: Record<string, unknown> = {}) {
  return {
    process_result: "DECLINED",
    failure_reason: "INSUFFICIENT_FUNDS",
    card_transaction_event_id: "evt_bloom_checkpoint",
    card_transaction_id: "txn_bloom_checkpoint",
    card_transaction_lifecycle_id: lifecycleId,
    card_id: `verified-card-${customerId}`,
    transaction_amount: 9_000,
    transaction_currency: "USD",
    merchant: { name: "Supplier", category_code: "5411" },
    transaction_type: "AUTHORIZATION",
    transaction_category: "PURCHASE",
    ...overrides,
  };
}

class MemoryPool implements DatabasePool {
  readonly attempts = new Map<string, Kit5CardAttempt>();
  readonly rosterRows: Record<string, unknown>[];
  failLifecyclePersistence = false;
  readonly query = vi.fn(async <Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> => {
    if (sql.includes("FROM kit5_card_attempts") && sql.includes("ORDER BY created_at DESC")) {
      const attempts = [...this.attempts.values()].filter((attempt) =>
        attempt.customerId === customerId && attempt.scenarioId.startsWith("kit5-target:") &&
        (sql.includes("attempt_type = 'fixture_checkpoint'")
          ? attempt.attemptType === "fixture_checkpoint" && attempt.actionId === `${attempt.scenarioId}:fixture-checkpoint:txn_bloom_supplier_001`
          : attempt.attemptType !== "fixture_checkpoint" && [
            `${attempt.scenarioId}:live:txn_bloom_supplier_001`,
            `${attempt.scenarioId}:txn_bloom_supplier_001`,
          ].includes(attempt.actionId)));
      const latest = attempts.at(-1);
      return { rows: latest ? [this.row(latest) as Row] : [], rowCount: latest ? 1 : 0 };
    }
    if (sql.includes("FROM customer_roster AS roster")) {
      const ids = values[0] as string[];
      const rows = this.rosterRows.filter((row) => ids.includes(String(row.customer_id)));
      return { rows: rows as Row[], rowCount: rows.length };
    }
    if (sql.includes("UPDATE kit5_card_attempts SET status = 'submitting'")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "accepted") return { rows: [], rowCount: 0 };
      attempt.status = "submitting";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET funding_claimed = true")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "completed" || attempt.fundingClaimed) return { rows: [], rowCount: 0 };
      attempt.fundingClaimed = true;
      attempt.policyStatus = "evaluating";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET status = 'ambiguous', provider_result = NULL")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "completed" || attempt.fundingClaimed || attempt.policyStatus !== "not_started") {
        return { rows: [], rowCount: 0 };
      }
      attempt.status = "ambiguous";
      attempt.result = null;
      attempt.error = "checkpoint_identity_unverified";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET lifecycle_id = $3, status = 'in_progress'")) {
      if (this.failLifecyclePersistence) throw new Error("simulated database write failure");
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || !["submitting", "ambiguous"].includes(attempt.status) || attempt.lifecycleId !== null) {
        return { rows: [], rowCount: 0 };
      }
      attempt.lifecycleId = String(values[2]);
      attempt.status = "in_progress";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET lifecycle_id =")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "submitting") return { rows: [], rowCount: 0 };
      attempt.lifecycleId = String(values[2]);
      attempt.status = "in_progress";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET status = $3, provider_result")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || !["submitting", "in_progress", "ambiguous"].includes(attempt.status)) return { rows: [], rowCount: 0 };
      attempt.status = values[2] as Kit5CardAttempt["status"];
      attempt.result = values[3] ? JSON.parse(String(values[3])) as Record<string, unknown> : null;
      attempt.error = values[4] ? String(values[4]) : null;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
    }
    throw new Error(`Unexpected pool query: ${sql}`);
  });

  private readonly nextIndex = new Map<string, number>();

  constructor(rows: Record<string, unknown>[] = customerIds.map((id) => binding(id))) {
    this.rosterRows = rows;
  }

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        if (["BEGIN", "COMMIT", "ROLLBACK"].includes(sql)) return { rows: [], rowCount: 0 };
        if (sql.includes("pg_advisory_xact_lock")) return { rows: [], rowCount: 1 };
        if (sql.includes("FROM kit5_card_attempts") && sql.includes("ORDER BY created_at DESC")) {
          const attempts = [...this.attempts.values()].filter((attempt) =>
            attempt.customerId === customerId && attempt.scenarioId.startsWith("kit5-target:") &&
            (sql.includes("attempt_type = 'fixture_checkpoint'")
              ? attempt.attemptType === "fixture_checkpoint" && attempt.actionId === `${attempt.scenarioId}:fixture-checkpoint:txn_bloom_supplier_001`
              : attempt.attemptType !== "fixture_checkpoint" && [
                `${attempt.scenarioId}:live:txn_bloom_supplier_001`,
                `${attempt.scenarioId}:txn_bloom_supplier_001`,
              ].includes(attempt.actionId)));
          const latest = attempts.at(-1);
          return { rows: latest ? [this.row(latest) as Row] : [], rowCount: latest ? 1 : 0 };
        }
        if (sql.includes("INSERT INTO kit5_demo_scenario_state")) {
          if (!this.nextIndex.has(String(values[0]))) this.nextIndex.set(String(values[0]), 0);
          return { rows: [], rowCount: 1 };
        }
        if (sql.includes("SELECT next_customer_index")) {
          return { rows: [{ next_customer_index: this.nextIndex.get(String(values[0])) ?? 0 } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
          const attempt = this.attempt(String(values[0]), String(values[1]));
          return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
        }
        if (sql.includes("WHERE scenario_id = $1 AND sequence_index = $2")) {
          const attempt = [...this.attempts.values()].find((candidate) => candidate.scenarioId === values[0] && candidate.sequenceIndex === values[1]);
          return { rows: attempt ? [{ status: attempt.status, policy_status: attempt.policyStatus, provider_result: attempt.result } as Row] : [], rowCount: attempt ? 1 : 0 };
        }
        if (sql.includes("INSERT INTO kit5_card_attempts")) {
          const attempt: Kit5CardAttempt = {
            scenarioId: String(values[0]),
            customerId: String(values[1]),
            actionId: String(values[2]),
            attemptType: values[5] === "fixture_checkpoint" ? "fixture_checkpoint" : "sandbox_live",
            sequenceIndex: Number(values[3]),
            lifecycleId: null,
            status: "accepted",
            result: null,
            error: null,
            fundingClaimed: false,
            requestFacts: JSON.parse(String(values[4])) as Kit5CardAttempt["requestFacts"],
            policyStatus: "not_started",
            policyError: null,
            policyDecisionId: null,
            pendingPolicyDecisionId: null,
            policyActorId: null,
            policyApprovalReason: null,
            bridgeId: null,
            retryLifecycleId: null,
            retryRequestId: null,
            retryResult: null,
            retryError: null,
          };
          this.attempts.set(this.key(attempt.scenarioId, attempt.actionId), attempt);
          return { rows: [this.row(attempt) as Row], rowCount: 1 };
        }
        if (sql.includes("UPDATE kit5_demo_scenario_state SET next_customer_index = $2")) {
          this.nextIndex.set(String(values[0]), Number(values[1]));
          return { rows: [], rowCount: 1 };
        }
        throw new Error(`Unexpected client query: ${sql}`);
      },
      release: () => {},
    };
  }

  async end(): Promise<void> {}

  private attempt(scenario: string, action: string): Kit5CardAttempt | undefined {
    return this.attempts.get(this.key(scenario, action));
  }

  private key(scenario: string, action: string): string {
    return `${scenario}/${action}`;
  }

  private row(attempt: Kit5CardAttempt): Record<string, unknown> {
    return {
      scenario_id: attempt.scenarioId,
      customer_id: attempt.customerId,
      action_id: attempt.actionId,
      attempt_type: attempt.attemptType ?? "sandbox_live",
      sequence_index: attempt.sequenceIndex,
      lifecycle_id: attempt.lifecycleId,
      status: attempt.status,
      provider_result: attempt.result,
      error: attempt.error,
      funding_claimed: attempt.fundingClaimed,
      request_facts: attempt.requestFacts,
      policy_status: attempt.policyStatus,
      policy_error: attempt.policyError,
      policy_decision_id: attempt.policyDecisionId,
      pending_policy_decision_id: attempt.pendingPolicyDecisionId,
      policy_actor_id: attempt.policyActorId,
      policy_approval_reason: attempt.policyApprovalReason,
      bridge_id: attempt.bridgeId,
      retry_lifecycle_id: attempt.retryLifecycleId,
      retry_request_id: attempt.retryRequestId,
      retry_provider_result: attempt.retryResult,
      retry_error: attempt.retryError,
    };
  }
}

function request(body: unknown, authenticated = true, url = "/api/kit5/fixture-checkpoint"): IncomingMessage {
  return Object.assign(Readable.from([JSON.stringify(body)]), {
    method: "POST",
    url,
    headers: {
      origin,
      ...(authenticated ? { cookie: `kit5_operator_session=${createOperatorSessionCookie(operatorToken)}` } : {}),
    },
  }) as IncomingMessage;
}

function responseRecorder() {
  let statusCode = 0;
  let body = "";
  const response = {
    headersSent: false,
    setHeader() { return this; },
    writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
    end(value = "") { body = value; return this; },
  } as unknown as ServerResponse;
  return { response, status: () => statusCode, body: () => JSON.parse(body) as Record<string, unknown> };
}

function fixture(): Kit5CustomerFixture {
  return {
    orderedCustomerIds: customerIds,
    policyFacts: (id) => id === customerId
      ? { transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", merchantCategoryCode: "5411" }
      : null,
    resolveCustomer: (id) => id === customerId ? {
      accountId: `verified-account-${customerId}`,
      cardId: `verified-card-${customerId}`,
      amountCents: 900_000,
      currency: "USD",
      merchant: "Supplier",
      merchantCategoryCode: "5411",
    } : null,
  };
}

function route(
  pool: MemoryPool,
  gateway: IssuingSimulationGateway,
  resolveFixtureCheckpoint = createKit5FixtureCheckpointResolver(pool),
  bridgeFunding?: Kit5BridgeFundingBoundary,
  extra: Partial<Kit5DemoRouteDependencies> = {},
) {
  const dependencies = {
    pool,
    gateway,
    resolveScenario: () => fixture(),
    resolveFixtureCheckpoint,
    operatorAuth: { operatorToken, allowedOrigins: [origin] },
    bridgeFunding,
    ...extra,
  };
  return createRequestHandler({
    port: 8000,
    databaseUrl: "fixture",
    airwallexClientId: "fixture",
    airwallexApiKey: "fixture",
    airwallexWebhookSecret: "fixture",
    kit5OperatorToken: operatorToken,
    kit5AllowedOrigins: [origin],
  }, pool, undefined, dependencies);
}

async function send(
  handler: ReturnType<typeof createRequestHandler>,
  body: unknown,
  authenticated = true,
  url = "/api/kit5/fixture-checkpoint",
) {
  const recorder = responseRecorder();
  handler(request(body, authenticated, url), recorder.response);
  await new Promise((resolve) => setImmediate(resolve));
  return { statusCode: recorder.status(), body: recorder.body() };
}

describe("Kit 5 Airwallex fixture checkpoint route", () => {
  it("requires operator authentication before resolving mappings or using the provider", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
    });

    const result = await send(handler, { scenarioId, customerId, actionId }, false);

    expect(result).toEqual({ statusCode: 401, body: { error: "operator_auth_required" } });
    expect(pool.query).not.toHaveBeenCalled();
    expect(createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("requires operator authentication for manual lifecycle reconciliation", async () => {
    const pool = new MemoryPool();
    const getIssuingLifecycle = vi.fn();
    const handler = route(pool, { createIssuingSimulation: vi.fn(), getIssuingLifecycle });

    const result = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, false, "/api/kit5/fixture-checkpoint/reconcile");

    expect(result).toEqual({ statusCode: 401, body: { error: "operator_auth_required" } });
    expect(pool.query).not.toHaveBeenCalled();
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
  });

  it.each([
    ["other scenario", { scenarioId: "original:00000000-0000-4000-8000-000000000001", customerId, actionId: "original:00000000-0000-4000-8000-000000000001:txn_bloom_supplier_001" }],
    ["other customer", { scenarioId, customerId: "acct_atlas001", actionId }],
    ["other request ID", { scenarioId, customerId, actionId: `${scenarioId}:txn_atlas_supplier_001` }],
    ["extra request ID", { scenarioId, customerId, actionId, requestId: "txn_bloom_supplier_001" }],
  ])("rejects %s", async (_name, body) => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });

    const result = await send(handler, body);

    expect(result.statusCode).toBe(Object.hasOwn(body, "requestId") ? 400 : 409);
    expect(createIssuingSimulation).not.toHaveBeenCalled();
    expect(pool.query).not.toHaveBeenCalled();
  });

  it("rejects the hybrid checkpoint identity on the generic route before provider or bridge work", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn();
    const funding = {
      evaluate: vi.fn(),
      reserveAndWaitForSettlement: vi.fn(),
      retryCardAfterSettlement: vi.fn(),
    } as unknown as Kit5BridgeFundingBoundary;
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() }, undefined, funding);

    const result = await send(handler, { scenarioId, customerId, actionId }, true, "/api/kit5/next-customer");

    expect(result).toEqual({ statusCode: 409, body: { status: "rejected", reason: "use_fixture_checkpoint_route" } });
    expect(pool.query).not.toHaveBeenCalled();
    expect(createIssuingSimulation).not.toHaveBeenCalled();
    expect(funding.evaluate).not.toHaveBeenCalled();
    expect(funding.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(funding.retryCardAfterSettlement).not.toHaveBeenCalled();
  });

  it("preserves the full Sandbox-live funding path for the explicitly marked live action", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => matchingCheckpointSimulation(providerLifecycleId));
    const funding = {
      evaluate: vi.fn(async () => ({
        status: "approved" as const,
        command: { bridgeId: "00000000-0000-4000-8000-000000000008", customerId, amountCents: 700_000, repaymentTerm: "asap" as const },
      })),
      reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: "bridge-live", status: "settled" as const })),
      retryCardAfterSettlement: vi.fn(async () => {}),
    } as unknown as Kit5BridgeFundingBoundary;
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() }, undefined, funding);
    const liveActionId = `${scenarioId}:live:txn_bloom_supplier_001`;

    const result = await send(handler, { scenarioId, customerId, actionId: liveActionId }, true, "/api/kit5/next-customer");

    expect(result.statusCode).toBe(202);
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(funding.evaluate).toHaveBeenCalledOnce();
    expect(funding.reserveAndWaitForSettlement).toHaveBeenCalledOnce();
    expect(funding.retryCardAfterSettlement).toHaveBeenCalledOnce();
  });

  it("keeps a legacy Sandbox-live Bloom attempt separate from a new fixture checkpoint", async () => {
    const pool = new MemoryPool();
    const legacyScenarioId = "kit5-target:00000000-0000-4000-8000-000000000002";
    const legacyActionId = `${legacyScenarioId}:txn_bloom_supplier_001`;
    const legacyCreate = vi.fn(async () => matchingCheckpointSimulation(providerLifecycleId));
    const legacyHandler = route(pool, { createIssuingSimulation: legacyCreate, getIssuingLifecycle: vi.fn() });

    const legacy = await send(legacyHandler, {
      scenarioId: legacyScenarioId,
      customerId,
      actionId: legacyActionId,
    }, true, "/api/kit5/next-customer");
    const legacyAttempt = pool.attempts.get(`${legacyScenarioId}/${legacyActionId}`);
    if (legacyAttempt) {
      legacyAttempt.fundingClaimed = true;
      legacyAttempt.policyStatus = "settled";
      legacyAttempt.bridgeId = "legacy-bridge";
      legacyAttempt.retryLifecycleId = "legacy-retry";
    }

    const checkpointCreate = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle"));
    const checkpointHandler = route(pool, {
      createIssuingSimulation: checkpointCreate,
      getIssuingLifecycle: vi.fn(),
    });
    const checkpoint = await send(checkpointHandler, { scenarioId, customerId, actionId });
    const checkpointAttempt = pool.attempts.get(`${scenarioId}/${actionId}`);
    const recovery = await send(checkpointHandler, {}, true, "/api/kit5/fixture-checkpoint/recover");

    expect(legacy.statusCode).toBe(200);
    expect(legacyAttempt).toMatchObject({ attemptType: "sandbox_live", status: "completed", fundingClaimed: true });
    expect(checkpoint.statusCode).toBe(200);
    expect(checkpoint.body).toMatchObject({ status: "completed", scenarioId, actionId });
    expect(checkpointAttempt).toMatchObject({ attemptType: "fixture_checkpoint", status: "completed" });
    expect(checkpointCreate).toHaveBeenCalledOnce();
    expect(recovery.body).toMatchObject({ status: "completed", scenarioId, actionId });
  });

  it("marks a definitive Airwallex 4xx failed after lifecycle correlation was saved", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => { throw new ProviderHttpError(400, "invalid issuing request"); });
    const getIssuingLifecycleEvents = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });

    const first = await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId, customerId, actionId });

    expect(first).toMatchObject({ statusCode: 502, body: { status: "failed", lifecycleId: expect.any(String) } });
    expect(replay).toMatchObject({ statusCode: 502, body: { status: "failed", lifecycleId: expect.any(String) } });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "failed", lifecycleId: expect.any(String) });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).not.toHaveBeenCalled();
  });

  it("marks a failed lifecycle-ID persistence terminal before calling Airwallex", async () => {
    const pool = new MemoryPool();
    pool.failLifecyclePersistence = true;
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });

    const first = await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId, customerId, actionId });

    expect(first).toMatchObject({ statusCode: 502, body: { status: "failed", lifecycleId: null } });
    expect(replay).toMatchObject({ statusCode: 502, body: { status: "failed", lifecycleId: null } });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "failed",
      lifecycleId: null,
      error: "checkpoint_create_not_started",
    });
    expect(createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("adopts the unresolved server attempt across fresh run UUIDs without another create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });
    const anotherRunId = "00000000-0000-4000-8000-000000000002";
    const anotherScenarioId = `kit5-target:${anotherRunId}`;
    const anotherActionId = `${anotherScenarioId}:fixture-checkpoint:txn_bloom_supplier_001`;

    const first = await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId: anotherScenarioId, customerId, actionId: anotherActionId, startNew: true });

    expect(first.statusCode).toBe(202);
    expect(replay.statusCode).toBe(202);
    expect(replay.body).toMatchObject({
      status: "ambiguous",
      scenarioId,
      customerId,
      actionId,
      attemptId: actionId,
      lifecycleId: pool.attempts.get(`${scenarioId}/${actionId}`)?.lifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(pool.attempts.size).toBe(1);
  });

  it("allows a new provider run only after resolution and explicit presenter confirmation", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle"));
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });
    const nextRunId = "00000000-0000-4000-8000-000000000002";
    const nextScenarioId = `kit5-target:${nextRunId}`;
    const nextActionId = `${nextScenarioId}:fixture-checkpoint:txn_bloom_supplier_001`;

    await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId: nextScenarioId, customerId, actionId: nextActionId });
    const started = await send(handler, {
      scenarioId: nextScenarioId,
      customerId,
      actionId: nextActionId,
      startNew: true,
    });

    expect(replay.statusCode).toBe(200);
    expect(replay.body).toMatchObject({ status: "completed", scenarioId, actionId });
    expect(started.statusCode).toBe(200);
    expect(started.body).toMatchObject({ status: "completed", scenarioId: nextScenarioId, actionId: nextActionId });
    expect(createIssuingSimulation).toHaveBeenCalledTimes(2);
    expect(pool.attempts.size).toBe(2);
  });

  it("returns the saved attempt identity through authenticated reload recovery", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });
    await send(handler, { scenarioId, customerId, actionId });

    const recovered = await send(handler, {}, true, "/api/kit5/fixture-checkpoint/recover");

    expect(recovered.statusCode).toBe(202);
    expect(recovered.body).toMatchObject({
      status: "ambiguous",
      scenarioId,
      customerId,
      actionId,
      attemptId: actionId,
      lifecycleId: pool.attempts.get(`${scenarioId}/${actionId}`)?.lifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("revalidates completed checkpoint results before cross-run adoption or reload recovery", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle"));
    const getIssuingLifecycleEvents = vi.fn(async () => []);
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
      getIssuingLifecycleEvents,
    });

    const first = await send(handler, { scenarioId, customerId, actionId });
    const saved = pool.attempts.get(`${scenarioId}/${actionId}`);
    if (!saved?.result) throw new Error("checkpoint result was not saved");
    saved.result = { ...saved.result, cardId: "card-from-another-transaction" };

    const nextScenarioId = "kit5-target:00000000-0000-4000-8000-000000000002";
    const nextActionId = `${nextScenarioId}:fixture-checkpoint:txn_bloom_supplier_001`;
    const adopted = await send(handler, {
      scenarioId: nextScenarioId,
      customerId,
      actionId: nextActionId,
    });
    const recovered = await send(handler, {}, true, "/api/kit5/fixture-checkpoint/recover");

    expect(first.statusCode).toBe(200);
    expect(adopted).toMatchObject({ statusCode: 202, body: { status: "ambiguous", scenarioId, actionId } });
    expect(adopted.body).not.toHaveProperty("result");
    expect(recovered).toMatchObject({ statusCode: 202, body: { status: "ambiguous", scenarioId, actionId } });
    expect(recovered.body).not.toHaveProperty("result");
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous",
      error: "checkpoint_identity_unverified",
      result: null,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["missing mapping", customerIds.slice(1).map((id) => binding(id))],
    ["inactive roster mapping", customerIds.map((id) => binding(id, id === customerId ? { roster_active: false } : {}))],
    ["stale card binding", customerIds.map((id) => binding(id, id === customerId ? { binding_account_id: "stale-account" } : {}))],
  ])("fails closed for a %s", async (_name, rows) => {
    const pool = new MemoryPool(rows);
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });

    const result = await send(handler, { scenarioId, customerId, actionId });

    expect(result).toEqual({ statusCode: 409, body: { status: "rejected", reason: "fixture_checkpoint_mapping_unavailable" } });
    expect(createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("persists the normalized Bloom attempt once and never invokes bridge funding or card retry", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle"));
    const boundary = {
      evaluate: vi.fn(),
      reserveAndWaitForSettlement: vi.fn(),
      retryCardAfterSettlement: vi.fn(),
    };
    const chargeFee = vi.fn();
    const createDeposit = vi.fn();
    const resolveRepaymentDepositTarget = vi.fn();
    const repaymentDepositGateway = {
      createCharge: chargeFee,
      createGlobalAccountDepositSimulation: createDeposit,
    } as unknown as AirwallexGateway;
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() }, undefined, boundary, {
      repaymentDepositGateway,
      resolveRepaymentDepositTarget,
    });
    const input = { scenarioId, customerId, actionId };

    const first = await send(handler, input);
    const replay = await send(handler, input);
    const attempt = pool.attempts.get(`${scenarioId}/${actionId}`);

    expect(first).toEqual({
      statusCode: 200,
      body: {
        status: "completed",
        scenarioId,
        customerId,
        actionId,
        attemptId: actionId,
        lifecycleId: expect.any(String),
        attemptedAmount: { amountCents: 900_000, currency: "USD" },
        result: {
          processResult: "DECLINED",
          failureReason: "INSUFFICIENT_FUNDS",
          cardTransactionEventId: "evt_bloom_checkpoint",
          cardTransactionId: "txn_bloom_checkpoint",
          cardTransactionLifecycleId: expect.any(String),
          transactionType: "AUTHORIZATION",
          transactionCategory: "PURCHASE",
          merchantName: "Supplier",
          merchantCategoryCode: "5411",
          cardId: `verified-card-${customerId}`,
          transactionAmountCents: 900_000,
          transactionCurrency: "USD",
          scenarioId,
        },
      },
    });
    expect(replay.statusCode).toBe(200);
    expect(replay.body.attemptId).toBe(actionId);
    expect(attempt).toMatchObject({ customerId, status: "completed", lifecycleId: expect.any(String), policyStatus: "not_started" });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(createIssuingSimulation).toHaveBeenCalledWith(expect.objectContaining({
      accountId: `verified-account-${customerId}`,
      cardId: `verified-card-${customerId}`,
      amountCents: 900_000,
      currency: "USD",
      merchant: "Supplier",
      merchantCategoryCode: "5411",
      scenarioId,
      lifecycleId: expect.any(String),
    }));
    expect(createIssuingSimulation.mock.calls[0]?.[0].lifecycleId).toBe(attempt?.lifecycleId);
    expect(boundary.evaluate).not.toHaveBeenCalled();
    expect(boundary.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(boundary.retryCardAfterSettlement).not.toHaveBeenCalled();
    expect(chargeFee).not.toHaveBeenCalled();
    expect(createDeposit).not.toHaveBeenCalled();
    expect(resolveRepaymentDepositTarget).not.toHaveBeenCalled();
    expect(pool.attempts.size).toBe(1);
  });

  it("retains an ambiguous attempt and never creates a second provider attempt on replay", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn() });
    const input = { scenarioId, customerId, actionId };

    const first = await send(handler, input);
    const replay = await send(handler, input);

    expect(first).toEqual({
      statusCode: 202,
      body: expect.objectContaining({
        status: "ambiguous", scenarioId, customerId, actionId, attemptId: actionId,
        lifecycleId: expect.any(String), attemptedAmount: { amountCents: 900_000, currency: "USD" },
      }),
    });
    expect(replay.statusCode).toBe(202);
    expect(replay.body.status).toBe("ambiguous");
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId: expect.any(String) });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it.each([
    ["different card", { card_id: "verified-card-acct_atlas001" }],
    ["different amount", { transaction_amount: 8_999 }],
    ["different currency", { transaction_currency: "EUR" }],
    ["different merchant", { merchant: { name: "Other supplier", category_code: "5411" } }],
    ["different returned account", { account_id: "unrelated-account" }],
    ["missing card", { card_id: undefined }],
    ["missing amount", { transaction_amount: undefined }],
    ["missing currency", { transaction_currency: undefined }],
    ["missing merchant", { merchant: undefined }],
  ])("keeps an immediate create result ambiguous when it has a %s", async (_name, mismatch) => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle", mismatch));
    const getIssuingLifecycle = vi.fn(async () => { throw new Error("checkpoint must use event-level lookup"); });
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle });

    const result = await send(handler, { scenarioId, customerId, actionId });

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: expect.any(String) });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: expect.any(String), result: null,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
  });

  it("reconciles a mismatched immediate result from the saved lifecycle without another create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle", { card_id: "other-card" }));
    const getIssuingLifecycleEvents = vi.fn(async (id: string) => [matchingCheckpointLifecycle(id)]);
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
      getIssuingLifecycleEvents,
    });

    const first = await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId, customerId, actionId });

    expect(first.statusCode).toBe(202);
    expect(first.body).toMatchObject({ status: "ambiguous", lifecycleId: expect.any(String) });
    expect(replay.statusCode).toBe(200);
    expect(replay.body).toMatchObject({ status: "completed", lifecycleId: first.body.lifecycleId });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("invalidates a completed replay that lacks provider identity and cannot match a lifecycle event", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, attemptType: "fixture_checkpoint", sequenceIndex: 0, lifecycleId: providerLifecycleId,
      status: "completed",
      result: {
        processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS",
        cardTransactionEventId: "evt_old", cardTransactionId: "txn_old",
        cardTransactionLifecycleId: providerLifecycleId,
      },
      error: null, fundingClaimed: false,
      requestFacts: {
        accountId: `verified-account-${customerId}`, cardId: `verified-card-${customerId}`,
        amountCents: 900_000, currency: "USD", merchant: "Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411",
      },
      policyStatus: "not_started", policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
      policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const createIssuingSimulation = vi.fn();
    const getIssuingLifecycleEvents = vi.fn(async () => []);
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
      getIssuingLifecycleEvents,
    });

    const result = await send(handler, { scenarioId, customerId, actionId });

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(result.body).not.toHaveProperty("result");
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: providerLifecycleId, result: null,
    });
    expect(createIssuingSimulation).not.toHaveBeenCalled();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("does not let manual reconciliation return an unverified completed result", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, attemptType: "fixture_checkpoint", sequenceIndex: 0, lifecycleId: providerLifecycleId,
      status: "completed",
      result: {
        processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS",
        cardTransactionEventId: "evt_old", cardTransactionId: "txn_old",
        cardTransactionLifecycleId: providerLifecycleId,
      },
      error: null, fundingClaimed: false,
      requestFacts: {
        accountId: `verified-account-${customerId}`, cardId: `verified-card-${customerId}`,
        amountCents: 900_000, currency: "USD", merchant: "Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411",
      },
      policyStatus: "not_started", policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
      policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const createIssuingSimulation = vi.fn();
    const getIssuingLifecycleEvents = vi.fn(async () => [
      matchingCheckpointLifecycle(providerLifecycleId, { transaction_amount: 8_999 }),
    ]);
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
      getIssuingLifecycleEvents,
    });

    const result = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(result.body).not.toHaveProperty("result");
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: providerLifecycleId, result: null,
    });
    expect(createIssuingSimulation).not.toHaveBeenCalled();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("keeps checkpoint attempt-status on event verification and skips bridge recovery", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, attemptType: "fixture_checkpoint", sequenceIndex: 0, lifecycleId: providerLifecycleId,
      status: "ambiguous", result: null, error: "checkpoint_identity_unverified", fundingClaimed: false,
      requestFacts: {
        accountId: `verified-account-${customerId}`, cardId: `verified-card-${customerId}`,
        amountCents: 900_000, currency: "USD", merchant: "Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411",
      },
      policyStatus: "not_started", policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
      policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const getIssuingLifecycle = vi.fn(async () => matchingCheckpointLifecycle(providerLifecycleId));
    const getIssuingLifecycleEvents = vi.fn(async () => []);
    const createIssuingSimulation = vi.fn();
    const recover = vi.fn(async () => {});
    const boundary: Kit5BridgeFundingBoundary = {
      evaluate: vi.fn(async () => ({ status: "ineligible" })),
      reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: "unused", status: "failed" })),
      retryCardAfterSettlement: vi.fn(async () => {}),
      recover,
    };
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle, getIssuingLifecycleEvents }, undefined, boundary);

    const result = await send(handler, { scenarioId, customerId, actionId }, true, "/api/kit5/attempt-status");

    expect(result.statusCode).toBe(200);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(result.body).not.toHaveProperty("result");
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
    expect(recover).not.toHaveBeenCalled();
    expect(createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("reconciles a lifecycle found by the operator without repeating an ambiguous create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const getIssuingLifecycleEvents = vi.fn(async (id: string, accountId: string) => {
      expect(accountId).toBe(`verified-account-${customerId}`);
      return [matchingCheckpointLifecycle(id)];
    });
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });
    const attempt = pool.attempts.get(`${scenarioId}/${actionId}`)!;
    const lifecycleId = attempt.lifecycleId!;

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(200);
    expect(reconciled.body).toMatchObject({
      status: "completed",
      attemptId: actionId,
      lifecycleId,
      result: {
        processResult: "DECLINED",
        failureReason: "INSUFFICIENT_FUNDS",
        cardTransactionLifecycleId: lifecycleId,
      },
    });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "completed", lifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith(lifecycleId, `verified-account-${customerId}`);
  });

  it.each([
    ["different card", { card_id: "verified-card-acct_atlas001" }],
    ["different amount", { transaction_amount: 8_999 }],
    ["different currency", { transaction_currency: "EUR" }],
    ["different account", { account_id: "unrelated-account" }],
    ["unrelated merchant transaction", { merchant: { name: "Other supplier", category_code: "5411" } }],
    ["missing card identity", { card_id: undefined }],
    ["missing transaction amount", { transaction_amount: undefined }],
    ["missing transaction currency", { transaction_currency: undefined }],
    ["missing merchant identity", { merchant: undefined }],
  ])("leaves an operator lifecycle unresolved when it has a %s", async (_name, mismatch) => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const getIssuingLifecycleEvents = vi.fn(async (id: string) => [matchingCheckpointLifecycle(id, mismatch)]);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });
    const lifecycleId = pool.attempts.get(`${scenarioId}/${actionId}`)!.lifecycleId!;

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(202);
    expect(reconciled.body).toMatchObject({ status: "ambiguous", lifecycleId });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("keeps duplicate matching events unresolved instead of choosing one", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const getIssuingLifecycleEvents = vi.fn(async (id: string) => {
      const matching = matchingCheckpointLifecycle(id);
      return [matching, { ...matching, id: "evt_bloom_duplicate" }];
    });
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });
    const lifecycleId = pool.attempts.get(`${scenarioId}/${actionId}`)!.lifecycleId!;

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(202);
    expect(reconciled.body).toMatchObject({ status: "ambiguous", lifecycleId });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("keeps an unknown operator lifecycle unresolved and does not create another attempt", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const getIssuingLifecycleEvents = vi.fn(async () => []);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });
    const lifecycleId = pool.attempts.get(`${scenarioId}/${actionId}`)!.lifecycleId!;

    const result = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith(lifecycleId, `verified-account-${customerId}`);
  });

  it("returns a saved in-progress lifecycle for reconciliation without resubmitting the create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async (request: { lifecycleId?: string }) =>
      matchingCheckpointSimulation(request.lifecycleId ?? "missing-lifecycle"));
    const getIssuingLifecycle = vi.fn(async () => null);
    const getIssuingLifecycleEvents = vi.fn(async () => null);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle, getIssuingLifecycleEvents });
    const input = { scenarioId, customerId, actionId };
    await send(handler, input);
    const attempt = pool.attempts.get(`${scenarioId}/${actionId}`)!;
    attempt.status = "in_progress";

    const result = await send(handler, input);

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({
        status: "in_progress",
        scenarioId,
        customerId,
        actionId,
        attemptId: actionId,
        lifecycleId: attempt.lifecycleId,
        attemptedAmount: { amountCents: 900_000, currency: "USD" },
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith(attempt.lifecycleId, `verified-account-${customerId}`);
  });

  it("does not accept a prior identical Bloom transaction when the current attempt has no correlation ID", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, attemptType: "fixture_checkpoint", sequenceIndex: 0, lifecycleId: null,
      status: "ambiguous", result: null, error: "legacy_attempt", fundingClaimed: false,
      requestFacts: {
        accountId: `verified-account-${customerId}`, cardId: `verified-card-${customerId}`,
        amountCents: 900_000, currency: "USD", merchant: "Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411",
      },
      policyStatus: "not_started", policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
      policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const getIssuingLifecycleEvents = vi.fn(async () => [matchingCheckpointLifecycle(providerLifecycleId)]);
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });

    const result = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(result).toEqual({
      statusCode: 409,
      body: { status: "rejected", reason: "checkpoint_lifecycle_correlation_unavailable" },
    });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId: null });
    expect(getIssuingLifecycleEvents).not.toHaveBeenCalled();
    expect(createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("does not match a same-terms event from an older lifecycle", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, attemptType: "fixture_checkpoint", sequenceIndex: 0, lifecycleId: providerLifecycleId,
      status: "ambiguous", result: null, error: "provider_result_unresolved", fundingClaimed: false,
      requestFacts: {
        accountId: `verified-account-${customerId}`, cardId: `verified-card-${customerId}`,
        amountCents: 900_000, currency: "USD", merchant: "Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411",
      },
      policyStatus: "not_started", policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
      policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const olderLifecycleId = "44444444-4444-4444-8444-444444444444";
    const getIssuingLifecycleEvents = vi.fn(async (_id: string) => [matchingCheckpointLifecycle(olderLifecycleId)]);
    const createIssuingSimulation = vi.fn();
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });

    const result = await send(handler, { scenarioId, customerId, actionId });

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(result.body).not.toHaveProperty("result");
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith(providerLifecycleId, `verified-account-${customerId}`);
    expect(createIssuingSimulation).not.toHaveBeenCalled();
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId, result: null });
  });
});
