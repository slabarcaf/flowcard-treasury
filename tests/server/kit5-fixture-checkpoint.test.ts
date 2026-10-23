import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import type { AirwallexGateway, IssuingSimulationGateway } from "../../server/src/airwallex/client.js";
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
const actionId = `${scenarioId}:txn_bloom_supplier_001`;
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
  readonly query = vi.fn(async <Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> => {
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
    const createIssuingSimulation = vi.fn(async () => matchingCheckpointSimulation("life_bloom_checkpoint"));
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
        attemptId: actionId,
        lifecycleId: "life_bloom_checkpoint",
        attemptedAmount: { amountCents: 900_000, currency: "USD" },
        result: {
          processResult: "DECLINED",
          failureReason: "INSUFFICIENT_FUNDS",
          cardTransactionEventId: "evt_bloom_checkpoint",
          cardTransactionId: "txn_bloom_checkpoint",
          cardTransactionLifecycleId: "life_bloom_checkpoint",
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
    expect(attempt).toMatchObject({ customerId, status: "completed", lifecycleId: "life_bloom_checkpoint", policyStatus: "not_started" });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(createIssuingSimulation).toHaveBeenCalledWith({
      accountId: `verified-account-${customerId}`,
      cardId: `verified-card-${customerId}`,
      amountCents: 900_000,
      currency: "USD",
      merchant: "Supplier",
      merchantCategoryCode: "5411",
      scenarioId,
    });
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
      body: { status: "ambiguous", attemptId: actionId, lifecycleId: null, attemptedAmount: { amountCents: 900_000, currency: "USD" } },
    });
    expect(replay.statusCode).toBe(202);
    expect(replay.body.status).toBe("ambiguous");
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId: null });
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
    const createIssuingSimulation = vi.fn(async () => matchingCheckpointSimulation(providerLifecycleId, mismatch));
    const getIssuingLifecycle = vi.fn(async () => { throw new Error("checkpoint must use event-level lookup"); });
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle });

    const result = await send(handler, { scenarioId, customerId, actionId });

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: providerLifecycleId, result: null,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
  });

  it("reconciles a mismatched immediate result from the saved lifecycle without another create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () =>
      matchingCheckpointSimulation(providerLifecycleId, { card_id: "other-card" }));
    const getIssuingLifecycleEvents = vi.fn(async () => [matchingCheckpointLifecycle(providerLifecycleId)]);
    const handler = route(pool, {
      createIssuingSimulation,
      getIssuingLifecycle: vi.fn(),
      getIssuingLifecycleEvents,
    });

    const first = await send(handler, { scenarioId, customerId, actionId });
    const replay = await send(handler, { scenarioId, customerId, actionId });

    expect(first.statusCode).toBe(202);
    expect(first.body).toMatchObject({ status: "ambiguous", lifecycleId: providerLifecycleId });
    expect(replay.statusCode).toBe(200);
    expect(replay.body).toMatchObject({ status: "completed", lifecycleId: providerLifecycleId });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("invalidates a completed replay that lacks provider identity and cannot match a lifecycle event", async () => {
    const pool = new MemoryPool();
    pool.attempts.set(`${scenarioId}/${actionId}`, {
      scenarioId, customerId, actionId, sequenceIndex: 0, lifecycleId: providerLifecycleId,
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
      scenarioId, customerId, actionId, sequenceIndex: 0, lifecycleId: providerLifecycleId,
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
      scenarioId, customerId, actionId, sequenceIndex: 0, lifecycleId: providerLifecycleId,
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
      expect(id).toBe(providerLifecycleId);
      expect(accountId).toBe(`verified-account-${customerId}`);
      return [matchingCheckpointLifecycle(providerLifecycleId)];
    });
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(200);
    expect(reconciled.body).toMatchObject({
      status: "completed",
      attemptId: actionId,
      lifecycleId: providerLifecycleId,
      result: {
        processResult: "DECLINED",
        failureReason: "INSUFFICIENT_FUNDS",
        cardTransactionLifecycleId: providerLifecycleId,
      },
    });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "completed", lifecycleId: providerLifecycleId,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
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
    const getIssuingLifecycleEvents = vi.fn(async () => [matchingCheckpointLifecycle(providerLifecycleId, mismatch)]);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(202);
    expect(reconciled.body).toMatchObject({ status: "ambiguous", lifecycleId: null });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: null,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledOnce();
  });

  it("keeps duplicate matching events unresolved instead of choosing one", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const matching = matchingCheckpointLifecycle(providerLifecycleId);
    const getIssuingLifecycleEvents = vi.fn(async () => [matching, { ...matching, id: "evt_bloom_duplicate" }]);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });

    const reconciled = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(reconciled.statusCode).toBe(202);
    expect(reconciled.body).toMatchObject({ status: "ambiguous", lifecycleId: null });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({
      status: "ambiguous", lifecycleId: null,
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("keeps an unknown operator lifecycle unresolved and does not create another attempt", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => ({ process_result: "PROCESSING" }));
    const getIssuingLifecycleEvents = vi.fn(async () => []);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle: vi.fn(), getIssuingLifecycleEvents });
    await send(handler, { scenarioId, customerId, actionId });

    const result = await send(handler, {
      scenarioId, customerId, actionId, lifecycleId: providerLifecycleId,
    }, true, "/api/kit5/fixture-checkpoint/reconcile");

    expect(result.statusCode).toBe(202);
    expect(result.body).toMatchObject({ status: "ambiguous", lifecycleId: null });
    expect(pool.attempts.get(`${scenarioId}/${actionId}`)).toMatchObject({ status: "ambiguous", lifecycleId: null });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith(providerLifecycleId, `verified-account-${customerId}`);
  });

  it("returns a saved in-progress lifecycle for reconciliation without resubmitting the create", async () => {
    const pool = new MemoryPool();
    const createIssuingSimulation = vi.fn(async () => matchingCheckpointSimulation("life_bloom_checkpoint"));
    const getIssuingLifecycle = vi.fn(async () => null);
    const getIssuingLifecycleEvents = vi.fn(async () => null);
    const handler = route(pool, { createIssuingSimulation, getIssuingLifecycle, getIssuingLifecycleEvents });
    const input = { scenarioId, customerId, actionId };
    await send(handler, input);
    const attempt = pool.attempts.get(`${scenarioId}/${actionId}`)!;
    attempt.status = "in_progress";

    const result = await send(handler, input);

    expect(result).toEqual({
      statusCode: 202,
      body: {
        status: "in_progress",
        attemptId: actionId,
        lifecycleId: "life_bloom_checkpoint",
        attemptedAmount: { amountCents: 900_000, currency: "USD" },
      },
    });
    expect(createIssuingSimulation).toHaveBeenCalledOnce();
    expect(getIssuingLifecycle).not.toHaveBeenCalled();
    expect(getIssuingLifecycleEvents).toHaveBeenCalledWith("life_bloom_checkpoint", `verified-account-${customerId}`);
  });
});
