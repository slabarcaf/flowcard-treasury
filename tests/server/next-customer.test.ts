import { readFile } from "node:fs/promises";
import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { ProviderHttpError, type IssuingSimulationGateway, type IssuingSimulationRequest } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import type { Kit5CardAttempt, Kit5AttemptStatus } from "../../server/src/db/repositories.js";
import { processNextCustomerAction, reconcileKit5CustomerAction, type Kit5CustomerFixture } from "../../server/src/demo/next-customer.js";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie } from "../../server/src/http/kit5-operator-auth.js";

const scenarioId = "kit5-target";
const customerIds = ["bloom-fixture", "atlas-fixture"];
const providerLifecycleId = "life-provider-generated";
function providerResponse(id: string) {
  return {
  process_result: "DECLINED",
  failure_reason: "INSUFFICIENT_FUNDS",
  card_transaction_event_id: "evt_fixture_1",
  card_transaction_id: "txn_fixture_1",
  card_transaction_lifecycle_id: id,
  };
}

class MemoryPool implements DatabasePool {
  readonly attempts = new Map<string, Kit5CardAttempt>();
  policyDecisionSummary: Record<string, unknown> | null = null;
  readonly states = new Map<string, number>();

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => this.transactionQuery<Row>(sql, values),
      release: () => {},
    };
  }

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes('FROM policy_request_evaluations WHERE decision_id')) {
      return { rows: this.policyDecisionSummary ? [this.policyDecisionSummary as Row] : [], rowCount: this.policyDecisionSummary ? 1 : 0 };
    }
    if (sql.includes("SET status = 'submitting'")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "accepted") return { rows: [], rowCount: 0 };
      attempt.status = "submitting";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET funding_claimed = true")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "completed" || attempt.fundingClaimed) return { rows: [], rowCount: 0 };
      attempt.fundingClaimed = true;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET lifecycle_id =")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "submitting") return { rows: [], rowCount: 0 };
      attempt.lifecycleId = String(values[2]);
      attempt.status = "in_progress";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET status = $3, provider_result")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      if (!attempt || !["submitting", "in_progress", "ambiguous"].includes(attempt.status)) return { rows: [], rowCount: 0 };
      attempt.status = values[2] as Kit5AttemptStatus;
      attempt.result = values[3] ? JSON.parse(String(values[3])) as Record<string, unknown> : null;
      attempt.error = values[4] ? String(values[4]) : null;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
    }
    throw new Error(`Unexpected pool query: ${sql}`);
  }

  async end(): Promise<void> {}

  private async transactionQuery<Row>(sql: string, values: readonly unknown[]): Promise<QueryResult<Row>> {
    if (sql === "BEGIN" || sql === "COMMIT" || sql === "ROLLBACK") return { rows: [], rowCount: 0 };
    if (sql.includes("INSERT INTO kit5_demo_scenario_state")) {
      const id = String(values[0]);
      if (!this.states.has(id)) this.states.set(id, 0);
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SELECT next_customer_index FROM kit5_demo_scenario_state")) {
      const index = this.states.get(String(values[0]));
      return { rows: index === undefined ? [] : [{ next_customer_index: index } as Row], rowCount: index === undefined ? 0 : 1 };
    }
    if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
      const attempt = this.get(String(values[0]), String(values[1]));
      return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
    }
    if (sql.includes("WHERE scenario_id = $1 AND sequence_index = $2")) {
      const attempt = [...this.attempts.values()].find((item) => item.scenarioId === values[0] && item.sequenceIndex === values[1]);
      return { rows: attempt ? [{ status: attempt.status, policy_status: attempt.policyStatus, provider_result: attempt.result } as Row] : [], rowCount: attempt ? 1 : 0 };
    }
    if (sql.includes("INSERT INTO kit5_card_attempts")) {
      const attempt: Kit5CardAttempt = {
        scenarioId: String(values[0]), customerId: String(values[1]), actionId: String(values[2]),
        sequenceIndex: Number(values[3]), lifecycleId: null, status: "accepted", result: null, error: null, fundingClaimed: false,
        requestFacts: values[4] ? JSON.parse(String(values[4])) : null,
        policyStatus: "not_started", policyError: null, policyDecisionId: null, bridgeId: null,
        retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
      };
      this.attempts.set(this.key(attempt.scenarioId, attempt.actionId), attempt);
      return { rows: [this.row(attempt) as Row], rowCount: 1 };
    }
    if (sql.includes("SET next_customer_index = $2")) {
      this.states.set(String(values[0]), Number(values[1]));
      return { rows: [], rowCount: 1 };
    }
    throw new Error(`Unexpected transaction query: ${sql}`);
  }

  private get(scenario: string, action: string): Kit5CardAttempt | undefined {
    return this.attempts.get(this.key(scenario, action));
  }

  private key(scenario: string, action: string): string { return `${scenario}/${action}`; }

  private row(attempt: Kit5CardAttempt): Record<string, unknown> {
    return {
      scenario_id: attempt.scenarioId, customer_id: attempt.customerId, action_id: attempt.actionId,
      sequence_index: attempt.sequenceIndex, lifecycle_id: attempt.lifecycleId, status: attempt.status,
      provider_result: attempt.result, error: attempt.error,
      funding_claimed: attempt.fundingClaimed,
      request_facts: attempt.requestFacts, policy_status: attempt.policyStatus,
      policy_error: attempt.policyError,
      policy_decision_id: attempt.policyDecisionId, bridge_id: attempt.bridgeId,
      retry_lifecycle_id: attempt.retryLifecycleId, retry_request_id: attempt.retryRequestId,
      retry_provider_result: attempt.retryResult, retry_error: attempt.retryError,
    };
  }
}

function fixture(): Kit5CustomerFixture {
  return {
    orderedCustomerIds: customerIds,
    // Fictional test-only policy facts. Production uses operator-verified mappings.
    policyFacts: () => ({ transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", merchantCategoryCode: "5411" }),
    resolveCustomer: (customerId) => customerIds.includes(customerId) ? {
      accountId: `fixture-account-${customerId}`,
      cardId: `fixture-card-${customerId}`,
      amountCents: 900_000,
      currency: "USD",
      merchant: "Fixture Supplier",
    } : null,
  };
}

function gateway(
  create: (request: IssuingSimulationRequest) => Promise<unknown> = async () => providerResponse(providerLifecycleId),
  lookup: (id: string, account: string) => Promise<unknown | null> = async () => null,
): IssuingSimulationGateway {
  return {
    createIssuingSimulation: vi.fn(create),
    getIssuingLifecycle: vi.fn(lookup),
  };
}

describe("Kit 5 next-customer persistence", () => {
  it("mounts the next-customer route and returns server-resolved attempted amount with provider identifiers", async () => {
    const pool = new MemoryPool();
    const provider = gateway();
    const route = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: "test-operator-token-0123456789abcdef-0123456789abcdef",
      kit5AllowedOrigins: ["https://demo.example"],
    }, pool, undefined, {
      pool,
      gateway: provider,
      resolveScenario: () => fixture(),
      operatorAuth: { operatorToken: "test-operator-token-0123456789abcdef-0123456789abcdef", allowedOrigins: ["https://demo.example"] },
    });
    const request = Readable.from([JSON.stringify({ scenarioId, customerId: customerIds[0], actionId: "route-action" })]) as IncomingMessage;
    Object.assign(request, {
      method: "POST", url: "/api/kit5/next-customer",
      headers: { origin: "https://demo.example", cookie: `kit5_operator_session=${createOperatorSessionCookie("test-operator-token-0123456789abcdef-0123456789abcdef")}` },
    });
    let statusCode = 0;
    let responseBody = "";
    const response = {
      headersSent: false,
      setHeader() { return this; },
      writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
      end(body: string) { responseBody = body; return this; },
    } as unknown as ServerResponse;

    route(request, response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(statusCode).toBe(200);
    expect(JSON.parse(responseBody)).toEqual({
      status: "completed",
      attemptId: "route-action",
      attemptedAmount: { amountCents: 900_000, currency: "USD" },
      result: {
        scenarioId,
        processResult: "DECLINED",
        failureReason: "INSUFFICIENT_FUNDS",
        cardTransactionEventId: "evt_fixture_1",
        cardTransactionId: "txn_fixture_1",
        cardTransactionLifecycleId: expect.any(String),
      },
      policyStatus: "not_started",
      policyError: null,
      policyDecision: null,
      bridgeId: null,
      retryLifecycleId: null,
      retryResult: null,
      retryError: null,
      humanApproved: false,
    });
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("returns the persisted card result as in_progress while policy funding is unresolved", async () => {
    const pool = new MemoryPool();
    const provider = gateway();
    const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
    const route = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: operatorToken, kit5AllowedOrigins: ["https://demo.example"],
    }, pool, undefined, {
      pool, gateway: provider, resolveScenario: () => fixture(),
      operatorAuth: { operatorToken, allowedOrigins: ["https://demo.example"] },
      bridgeFunding: {
        evaluate: vi.fn(async () => ({ status: "approved" as const, command: {
          scenarioId, actionId: "pending-route-action", customerId: customerIds[0]!,
          bridgeId: "00000000-0000-4000-8000-000000000003", amountCents: 500_000, repaymentTerm: "asap" as const,
          policyInput: { accountId: `fixture-account-${customerIds[0]}`, cardId: `fixture-card-${customerIds[0]}`,
            attemptedAmountCents: 900_000, currency: "USD", providerResult: {
              processResult: "DECLINED" as const, failureReason: "INSUFFICIENT_FUNDS",
              cardTransactionEventId: "evt", cardTransactionId: "txn", cardTransactionLifecycleId: "life",
            } },
        } })),
        reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: "00000000-0000-4000-8000-000000000003", status: "pending" as const })),
        retryCardAfterSettlement: vi.fn(async () => {}),
      },
    });
    const request = Object.assign(Readable.from([JSON.stringify({ scenarioId, customerId: customerIds[0], actionId: "pending-route-action" })]), {
      method: "POST", url: "/api/kit5/next-customer", headers: { origin: "https://demo.example",
        cookie: `kit5_operator_session=${createOperatorSessionCookie(operatorToken)}` },
    }) as IncomingMessage;
    let statusCode = 0;
    let responseBody = "";
    const response = { headersSent: false, setHeader() { return this; }, writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
      end(body: string) { responseBody = body; return this; } } as unknown as ServerResponse;

    route(request, response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(statusCode).toBe(202);
    expect(JSON.parse(responseBody)).toMatchObject({
      status: "in_progress", attemptId: "pending-route-action",
      result: { processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS", cardTransactionEventId: "evt_fixture_1" },
    });
  });

  it("returns human-review terms from the persisted policy decision row", async () => {
    const pool = new MemoryPool();
    pool.policyDecisionSummary = { outcome: "human_review", amount_cents: "700000", credit_line_cents: "900000",
      tier: "A", capital_band: "normal", door: "human", reason_codes: ["HUMAN_REVIEW_REQUIRED"], rationale: "Review required." };
    const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
    const bridgeFunding = {
      evaluate: vi.fn(async () => {
        const attempt = pool.attempts.get(`${scenarioId}/review-action`)!;
        attempt.policyStatus = "pending_review";
        attempt.policyDecisionId = "00000000-0000-4000-8000-000000000099";
        return { status: "ineligible" as const };
      }),
      reserveAndWaitForSettlement: vi.fn(), retryCardAfterSettlement: vi.fn(),
    };
    const route = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: operatorToken, kit5AllowedOrigins: ["https://demo.example"],
    }, pool, undefined, {
      pool, gateway: gateway(), resolveScenario: () => fixture(),
      operatorAuth: { operatorToken, allowedOrigins: ["https://demo.example"] }, bridgeFunding,
    });
    const request = Object.assign(Readable.from([JSON.stringify({ scenarioId, customerId: customerIds[0], actionId: "review-action" })]), {
      method: "POST", url: "/api/kit5/next-customer", headers: { origin: "https://demo.example",
        cookie: `kit5_operator_session=${createOperatorSessionCookie(operatorToken)}` },
    }) as IncomingMessage;
    let statusCode = 0;
    let responseBody = "";
    const response = { headersSent: false, setHeader() { return this; }, writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
      end(body: string) { responseBody = body; return this; } } as unknown as ServerResponse;

    route(request, response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(statusCode).toBe(202);
    expect(JSON.parse(responseBody)).toMatchObject({ status: "in_progress", policyStatus: "pending_review",
      policyDecision: { outcome: "human_review", amountCents: 700_000, creditLineCents: 900_000,
        tier: "A", capitalBand: "normal", door: "human", reasonCodes: ["HUMAN_REVIEW_REQUIRED"] } });
    expect(bridgeFunding.reserveAndWaitForSettlement).not.toHaveBeenCalled();
  });

  it("defines durable scenario ordering and action/lifecycle uniqueness", async () => {
    const migration = await readFile(new URL("../../server/src/db/migrations/015_kit5_card_attempts.sql", import.meta.url), "utf8");
    expect(migration).toContain("PRIMARY KEY (scenario_id, action_id)");
    expect(migration).toContain("UNIQUE (scenario_id, sequence_index)");
    expect(migration).toContain("lifecycle_id uuid UNIQUE");
    expect(migration).toContain("'accepted', 'submitting', 'in_progress', 'completed', 'ambiguous', 'failed'");
    expect(migration).toContain("funding_claimed boolean NOT NULL DEFAULT false");
    expect(migration).toContain("kit5_card_attempts_no_delete");
  });

  it("adds request-fact immutability and separate durable retry lifecycle after migration 015", async () => {
    const migration = await readFile(new URL("../../server/src/db/migrations/016_kit5_policy_retry_lifecycle.sql", import.meta.url), "utf8");
    expect(migration).toContain("ADD COLUMN IF NOT EXISTS request_facts jsonb");
    expect(migration).toContain("request_facts are immutable");
    expect(migration).toContain("retry_lifecycle_id uuid UNIQUE");
    expect(migration).toContain("retry_provider_result jsonb");
    expect(migration).toContain("pending_policy_decision_id uuid");
  });

  it("claims the action before one provider create and persists Airwallex's returned lifecycle", async () => {
    const pool = new MemoryPool();
    const provider = gateway(async (request) => {
      const attempt = pool.attempts.get(`${scenarioId}/action-1`);
      expect(attempt?.status).toBe("submitting");
      expect(attempt?.lifecycleId).toBeNull();
      expect(request.lifecycleId).toBeUndefined();
      return providerResponse(providerLifecycleId);
    });
    const outcome = await processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[0]!, actionId: "action-1",
    }, fixture());

    expect(outcome.status).toBe("completed");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(pool.attempts.get(`${scenarioId}/action-1`)).toMatchObject({ status: "completed", lifecycleId: providerLifecycleId });
  });

  it("returns a repeated action result without a second create", async () => {
    const pool = new MemoryPool();
    const provider = gateway();
    const input = { scenarioId, customerId: customerIds[0]!, actionId: "action-1" };
    await processNextCustomerAction(pool, provider, input, fixture());
    const repeated = await processNextCustomerAction(pool, provider, input, fixture());

    expect(repeated.status).toBe("completed");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("allows only one concurrent provider create for the same action", async () => {
    const pool = new MemoryPool();
    let signalStarted!: () => void;
    let releaseProvider!: () => void;
    const started = new Promise<void>((resolve) => { signalStarted = resolve; });
    const held = new Promise<void>((resolve) => { releaseProvider = resolve; });
    const provider = gateway(async (request) => {
      signalStarted();
      await held;
      return providerResponse(providerLifecycleId);
    });
    const input = { scenarioId, customerId: customerIds[0]!, actionId: "concurrent-action" };
    const first = processNextCustomerAction(pool, provider, input, fixture());
    await started;
    const replay = await processNextCustomerAction(pool, provider, input, fixture());
    releaseProvider();
    const completed = await first;

    expect(replay.status).toBe("in_progress");
    expect(completed.status).toBe("completed");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("reconciles a saved lifecycle without repeating provider create", async () => {
    const pool = new MemoryPool();
    const attempt: Kit5CardAttempt = {
      scenarioId, customerId: customerIds[0]!, actionId: "refresh-action", sequenceIndex: 0,
      lifecycleId: "lifecycle-saved", status: "ambiguous", result: null, error: null, fundingClaimed: false,
      requestFacts: null, policyStatus: "not_started", policyError: null, policyDecisionId: null,
      pendingPolicyDecisionId: null, policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    };
    pool.attempts.set(`${scenarioId}/${attempt.actionId}`, attempt);
    const provider = gateway(async () => { throw new Error("create must not run during reconciliation"); }, async () => providerResponse("lifecycle-saved"));

    const refreshed = await reconcileKit5CustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[0]!, actionId: attempt.actionId,
    }, fixture());

    expect(refreshed?.status).toBe("completed");
    expect(provider.getIssuingLifecycle).toHaveBeenCalledOnce();
    expect(provider.createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("serves authenticated lifecycle status refresh without requiring a new provider create", async () => {
    const pool = new MemoryPool();
    const attempt: Kit5CardAttempt = {
      scenarioId, customerId: customerIds[0]!, actionId: "status-route-action", sequenceIndex: 0,
      lifecycleId: "saved-lifecycle", status: "ambiguous", result: null, error: null, fundingClaimed: false,
      requestFacts: null, policyStatus: "not_started", policyError: null, policyDecisionId: null,
      pendingPolicyDecisionId: null, policyActorId: null, policyApprovalReason: null, bridgeId: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    };
    pool.attempts.set(`${scenarioId}/${attempt.actionId}`, attempt);
    const provider = gateway(async () => { throw new Error("create must not run during status refresh"); }, async () => providerResponse("saved-lifecycle"));
    const route = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: "test-operator-token-0123456789abcdef-0123456789abcdef",
      kit5AllowedOrigins: ["https://demo.example"],
    }, pool, undefined, {
      pool, gateway: provider, resolveScenario: () => fixture(),
      operatorAuth: {
        operatorToken: "test-operator-token-0123456789abcdef-0123456789abcdef",
        allowedOrigins: ["https://demo.example"],
      },
    });
    const request = Readable.from([JSON.stringify({ scenarioId, customerId: customerIds[0], actionId: attempt.actionId })]) as IncomingMessage;
    Object.assign(request, {
      method: "POST", url: "/api/kit5/attempt-status",
      headers: {
        origin: "https://demo.example",
        cookie: `kit5_operator_session=${createOperatorSessionCookie("test-operator-token-0123456789abcdef-0123456789abcdef")}`,
      },
    });
    let statusCode = 0;
    let responseBody = "";
    const response = {
      headersSent: false,
      setHeader() { return this; },
      writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
      end(body: string) { responseBody = body; return this; },
    } as unknown as ServerResponse;

    route(request, response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(statusCode).toBe(200);
    expect(JSON.parse(responseBody)).toMatchObject({ status: "completed", attemptId: attempt.actionId });
    expect(provider.getIssuingLifecycle).toHaveBeenCalledOnce();
    expect(provider.createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("resumes an accepted action only when no lifecycle was persisted", async () => {
    const pool = new MemoryPool();
    pool.states.set(scenarioId, 1);
    pool.attempts.set(`${scenarioId}/action-restart`, {
      scenarioId, customerId: customerIds[0]!, actionId: "action-restart", sequenceIndex: 0,
      lifecycleId: null, status: "accepted", result: null, error: null, fundingClaimed: false,
      requestFacts: { accountId: `fixture-account-${customerIds[0]}`, cardId: `fixture-card-${customerIds[0]}`,
        amountCents: 900_000, currency: "USD", merchant: "Fixture Supplier", transactionType: "AUTHORIZATION",
        transactionCategory: "PURCHASE", merchantCategoryCode: "5411" },
      policyStatus: "not_started", policyDecisionId: null, bridgeId: null,
      policyError: null,
      retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    });
    const provider = gateway();
    const resumed = await processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[0]!, actionId: "action-restart",
    }, fixture());

    expect(resumed.status).toBe("completed");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("rejects out-of-order customers and blocks advancement while an attempt is unresolved", async () => {
    const pool = new MemoryPool();
    const provider = gateway(async () => { throw new ProviderHttpError(400, "fixture rejection"); });
    const outOfOrder = await processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[1]!, actionId: "action-atlas",
    }, fixture());
    await expect(processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[0]!, actionId: "action-bloom",
    }, fixture())).rejects.toBeInstanceOf(ProviderHttpError);
    const next = await processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[1]!, actionId: "action-atlas-2",
    }, fixture());

    expect(outOfOrder).toEqual({ status: "rejected", reason: "out_of_order" });
    expect(pool.attempts.get(`${scenarioId}/action-bloom`)?.status).toBe("failed");
    expect(next).toEqual({ status: "rejected", reason: "previous_attempt_unresolved" });
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("allows the next customer after a prior customer's policy is blocked", async () => {
    const pool = new MemoryPool();
    pool.states.set(scenarioId, 1);
    pool.attempts.set(`${scenarioId}/blocked-bloom`, {
      scenarioId, customerId: customerIds[0]!, actionId: "blocked-bloom", sequenceIndex: 0,
      lifecycleId: "life-bloom", status: "completed", result: {
        processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS",
      }, error: null, fundingClaimed: true, requestFacts: null,
      policyStatus: "blocked", policyError: "active_binding_or_fresh_policy_inputs_unavailable",
      policyDecisionId: null, pendingPolicyDecisionId: null, policyActorId: null,
      policyApprovalReason: null, bridgeId: null, retryLifecycleId: null,
      retryRequestId: null, retryResult: null, retryError: null,
    });
    const provider = gateway();
    const result = await processNextCustomerAction(pool, provider, {
      scenarioId, customerId: customerIds[1]!, actionId: "atlas-after-blocked-bloom",
    }, fixture());

    expect(result.status).toBe("completed");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
  });

  it("keeps an ambiguous attempt unresolved without a provider lifecycle and never resubmits", async () => {
    const pool = new MemoryPool();
    const provider = gateway(async () => { throw new DOMException("fixture timeout", "TimeoutError"); });
    const input = { scenarioId, customerId: customerIds[0]!, actionId: "action-timeout" };
    const first = await processNextCustomerAction(pool, provider, input, fixture());
    const repeated = await processNextCustomerAction(pool, provider, input, fixture());

    expect(first.status).toBe("ambiguous");
    expect(repeated.status).toBe("ambiguous");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(provider.getIssuingLifecycle).not.toHaveBeenCalled();
  });

  it("does not reconcile an ambiguous attempt until Airwallex has returned a lifecycle", async () => {
    const pool = new MemoryPool();
    const provider = gateway(
      async () => { throw new DOMException("fixture timeout", "TimeoutError"); },
      async (id) => providerResponse(id),
    );
    const input = { scenarioId, customerId: customerIds[0]!, actionId: "action-recover" };
    await processNextCustomerAction(pool, provider, input, fixture());
    const recovered = await processNextCustomerAction(pool, provider, input, fixture());

    expect(recovered.status).toBe("ambiguous");
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(provider.getIssuingLifecycle).not.toHaveBeenCalled();
  });
});
