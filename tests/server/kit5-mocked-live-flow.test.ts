import { describe, expect, it, vi } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import { Readable } from "node:stream";
import type { AirwallexGateway, IssuingSimulationGateway, IssuingSimulationRequest } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import type { Kit5CardAttempt, Kit5AttemptStatus } from "../../server/src/db/repositories.js";
import type { Kit5CustomerFixture } from "../../server/src/demo/next-customer.js";
import { acknowledge, approve, initialKit5State, nextEvent, reEvaluateCrm, revealCrm, start, step } from "../../src/kit5/engine.js";
import { loadScenarios } from "../../src/kit5/scenarios.js";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie } from "../../server/src/http/kit5-operator-auth.js";

const scenarioId = "kit5-target";
const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
const allowedOrigins = ["https://demo.example"];
const customers = [
  { id: "bloom-fixture", accountId: "fixture-account-bloom", cardId: "fixture-card-bloom", amountCents: 900_000 },
  { id: "atlas-fixture", accountId: "fixture-account-atlas", cardId: "fixture-card-atlas", amountCents: 650_000 },
  { id: "nova-fixture", accountId: "fixture-account-nova", cardId: "fixture-card-nova", amountCents: 700_000 },
  { id: "pixel-fixture", accountId: "fixture-account-pixel", cardId: "fixture-card-pixel", amountCents: 250_000 },
  { id: "juniper-fixture", accountId: "fixture-account-juniper", cardId: "fixture-card-juniper", amountCents: 700_000 },
] as const;

class MemoryPool implements DatabasePool {
  readonly attempts = new Map<string, Kit5CardAttempt>();
  private nextIndex = 0;

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        if (["BEGIN", "COMMIT", "ROLLBACK"].includes(sql)) return { rows: [], rowCount: 0 };
        if (sql.includes("INSERT INTO kit5_demo_scenario_state")) return { rows: [], rowCount: 1 };
        if (sql.includes("SELECT next_customer_index")) {
          return { rows: [{ next_customer_index: this.nextIndex } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
          const attempt = this.attempt(String(values[0]), String(values[1]));
          return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
        }
        if (sql.includes("WHERE scenario_id = $1 AND sequence_index = $2")) {
          const attempt = [...this.attempts.values()].find((item) => item.scenarioId === values[0] && item.sequenceIndex === values[1]);
          return { rows: attempt ? [{ status: attempt.status } as Row] : [], rowCount: attempt ? 1 : 0 };
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
            requestFacts: values[4] ? JSON.parse(String(values[4])) as Kit5CardAttempt["requestFacts"] : null,
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
        if (sql.includes("SET next_customer_index = $2")) {
          this.nextIndex = Number(values[1]);
          return { rows: [], rowCount: 1 };
        }
        throw new Error(`Unexpected client query: ${sql}`);
      },
      release: () => {},
    };
  }

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes("SET status = 'submitting'")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "accepted") return { rows: [], rowCount: 0 };
      attempt.status = "submitting";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET funding_claimed = true")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      if (!attempt || attempt.status !== "completed" || attempt.fundingClaimed) return { rows: [], rowCount: 0 };
      attempt.fundingClaimed = true;
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
      attempt.status = values[2] as Kit5AttemptStatus;
      attempt.result = values[3] ? JSON.parse(String(values[3])) as Record<string, unknown> : null;
      attempt.error = values[4] ? String(values[4]) : null;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
      const attempt = this.attempt(String(values[0]), String(values[1]));
      return { rows: attempt ? [this.row(attempt) as Row] : [], rowCount: attempt ? 1 : 0 };
    }
    throw new Error(`Unexpected pool query: ${sql}`);
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

function fixture(): Kit5CustomerFixture {
  return {
    orderedCustomerIds: customers.map((customer) => customer.id),
    // Fictional test-only policy facts. Production uses operator-verified mappings.
    policyFacts: () => ({ transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", merchantCategoryCode: "5411" }),
    resolveCustomer: (id) => {
      const customer = customers.find((item) => item.id === id);
      return customer ? {
        accountId: customer.accountId,
        cardId: customer.cardId,
        amountCents: customer.amountCents,
        currency: "USD",
        merchant: `${customer.id} fixture merchant`,
      } : null;
    },
  };
}

function responseFor(index: number): Record<string, string | null> {
  const results = [
    { process_result: "DECLINED", failure_reason: "INSUFFICIENT_FUNDS" },
    { process_result: "APPROVED", failure_reason: null },
    { process_result: "DECLINED", failure_reason: "INSUFFICIENT_FUNDS" },
    { process_result: "DECLINED", failure_reason: "LIMIT_EXCEEDED" },
    { process_result: "DECLINED", failure_reason: "MERCHANT_CATEGORY_NOT_ALLOWED" },
  ];
  return {
    ...results[index]!,
    card_transaction_event_id: `evt_fixture_${index + 1}`,
    card_transaction_id: `txn_fixture_${index + 1}`,
    card_transaction_lifecycle_id: `life_fixture_${index + 1}`,
  };
}

function requestFor(
  customerId: string,
  actionId: string,
  url = "/api/kit5/next-customer",
  requestedScenarioId = scenarioId,
) {
  return Object.assign(Readable.from([JSON.stringify({ scenarioId: requestedScenarioId, customerId, actionId })]), {
    method: "POST",
    url,
    headers: { origin: allowedOrigins[0], cookie: `kit5_operator_session=${createOperatorSessionCookie(operatorToken)}` },
  }) as IncomingMessage;
}

async function callRoute(
  route: ReturnType<typeof createRequestHandler>,
  customerId: string,
  actionId: string,
  url = "/api/kit5/next-customer",
  requestedScenarioId = scenarioId,
): Promise<{ statusCode: number; body: Record<string, unknown> }> {
  let statusCode = 0;
  let responseBody = "";
  const response = {
    headersSent: false,
    setHeader() { return this; },
    writeHead(code: number) { statusCode = code; this.headersSent = true; return this; },
    end(body: string) { responseBody = body; return this; },
  } as unknown as ServerResponse;
  route(requestFor(customerId, actionId, url, requestedScenarioId), response);
  await new Promise((resolve) => setImmediate(resolve));
  return { statusCode, body: JSON.parse(responseBody) as Record<string, unknown> };
}

describe("Kit 5 mocked sandbox-live flow", () => {
  it("confirms one provider Bloom decline, then applies the same event through ordered fixture policy", async () => {
    const pool = new MemoryPool();
    const runId = "00000000-0000-4000-8000-000000000001";
    const checkpointScenarioId = `kit5-target:${runId}`;
    const bloomCustomerId = "acct_bloom001";
    const bloomActionId = `${checkpointScenarioId}:txn_bloom_supplier_001`;
    const orderedCustomerIds = [bloomCustomerId, "acct_atlas001", "acct_nova001", "acct_pixel001", "acct_juniper001"];
    const amounts = [900_000, 650_000, 700_000, 250_000, 700_000];
    const checkpointFixture: Kit5CustomerFixture = {
      orderedCustomerIds,
      policyFacts: () => ({ transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", merchantCategoryCode: "5411" }),
      resolveCustomer: (id) => {
        const index = orderedCustomerIds.indexOf(id);
        return index < 0 ? null : {
          accountId: `fixture-account-${id}`,
          cardId: `fixture-card-${id}`,
          amountCents: amounts[index]!,
          currency: "USD",
          merchant: id === bloomCustomerId ? "Supplier" : "Fixture merchant",
          merchantCategoryCode: "5411",
        };
      },
    };
    const provider = {
      createIssuingSimulation: vi.fn(async () => ({
        process_result: "DECLINED",
        failure_reason: "INSUFFICIENT_FUNDS",
        card_transaction_event_id: "evt_bloom_checkpoint",
        card_transaction_id: "txn_bloom_checkpoint",
        card_transaction_lifecycle_id: "life_bloom_checkpoint",
        card_id: `fixture-card-${bloomCustomerId}`,
        transaction_amount: 9_000,
        transaction_currency: "USD",
        merchant: { name: "Supplier", category_code: "5411" },
        transaction_type: "AUTHORIZATION",
        transaction_category: "PURCHASE",
      })),
      getIssuingLifecycle: vi.fn(async () => null),
    };
    const fundingBoundary = {
      evaluate: vi.fn(),
      reserveAndWaitForSettlement: vi.fn(),
      retryCardAfterSettlement: vi.fn(),
    };
    const createFeeCharge = vi.fn();
    const createRepaymentDeposit = vi.fn();
    const resolveRepaymentDepositTarget = vi.fn();
    const repaymentGateway = {
      createCharge: createFeeCharge,
      createGlobalAccountDepositSimulation: createRepaymentDeposit,
    } as unknown as AirwallexGateway;
    const route = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: operatorToken, kit5AllowedOrigins: allowedOrigins,
    }, pool, undefined, {
      pool,
      gateway: provider,
      resolveScenario: () => checkpointFixture,
      resolveFixtureCheckpoint: () => checkpointFixture,
      operatorAuth: { operatorToken, allowedOrigins },
      bridgeFunding: fundingBoundary,
      repaymentDepositGateway: repaymentGateway,
      resolveRepaymentDepositTarget,
    });

    const providerAttempt = await callRoute(route, bloomCustomerId, bloomActionId,
      "/api/kit5/fixture-checkpoint", checkpointScenarioId);
    const providerResult = providerAttempt.body.result as Record<string, unknown>;
    const kit5Target = loadScenarios().find((scenario) => scenario.id === "kit5-target")!;
    const bloomEvent = kit5Target.inputs.events.find((event) => event.type === "decline")!;

    expect(providerAttempt.statusCode).toBe(200);
    expect(providerResult).toMatchObject({ processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS" });
    expect(bloomEvent).toMatchObject({ requestId: "txn_bloom_supplier_001", paymentAmount: 9_000, walletBalance: 2_000 });
    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();

    let fixtureState = start(initialKit5State(kit5Target.inputs));
    expect(nextEvent(fixtureState)?.requestId).toBe(bloomEvent.requestId);
    fixtureState = step(fixtureState);
    const bloomFixtureDecision = fixtureState.queue.find((row) => row.id === bloomEvent.requestId);
    expect(bloomFixtureDecision?.status).toBe("Blocked");
    expect(fixtureState.customerDetails[bloomEvent.requestId].reasonCodes).toContain("EVIDENCE_REVIEW_REQUIRED");

    const expectedArrivalOrder = fixtureState.events.filter((event) => event.type === "decline").map((event) => event.requestId);
    for (let guard = 0; fixtureState.queue.length < expectedArrivalOrder.length && guard < 20; guard++) {
      if (fixtureState.pause?.kind === "ack") fixtureState = acknowledge(fixtureState);
      else if (fixtureState.pause?.kind === "human") fixtureState = approve(fixtureState, fixtureState.pause.requestId);
      else if (fixtureState.pause?.kind === "crm") fixtureState = reEvaluateCrm(revealCrm(fixtureState));
      fixtureState = step(fixtureState);
    }

    expect(fixtureState.queue.map((row) => row.id)).toEqual(expectedArrivalOrder);
    expect(fixtureState.queue.find((row) => row.id === bloomEvent.requestId)?.status).toBe("Blocked");
    expect(fundingBoundary.evaluate).not.toHaveBeenCalled();
    expect(fundingBoundary.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(fundingBoundary.retryCardAfterSettlement).not.toHaveBeenCalled();
    expect(createFeeCharge).not.toHaveBeenCalled();
    expect(createRepaymentDeposit).not.toHaveBeenCalled();
    expect(resolveRepaymentDepositTarget).not.toHaveBeenCalled();
    expect(pool.attempts.size).toBe(1);
    expect(pool.attempts.get(`${checkpointScenarioId}/${bloomActionId}`)).toMatchObject({
      status: "completed",
      fundingClaimed: false,
    });
  });

  it("does not fund Bloom while evidence is unresolved and enters the bridge boundary after reviewer release", async () => {
    const cardProvider: IssuingSimulationGateway = {
      createIssuingSimulation: vi.fn(async () => ({
        process_result: "DECLINED", failure_reason: "INSUFFICIENT_FUNDS",
        card_transaction_event_id: "evt_bloom_reviewed", card_transaction_id: "txn_bloom_reviewed",
        card_transaction_lifecycle_id: "life-bloom-reviewed",
      })),
      getIssuingLifecycle: vi.fn(async () => null),
    };
    const approvedCommand = {
      scenarioId, actionId: "bloom-review-action", customerId: customers[0]!.id,
      bridgeId: "00000000-0000-4000-8000-000000000001", amountCents: 700_000, repaymentTerm: "asap" as const,
      policyInput: { accountId: customers[0]!.accountId, cardId: customers[0]!.cardId,
        attemptedAmountCents: customers[0]!.amountCents, currency: "USD",
        providerResult: { processResult: "DECLINED" as const, failureReason: "INSUFFICIENT_FUNDS",
          cardTransactionEventId: "evt_bloom_reviewed", cardTransactionId: "txn_bloom_reviewed",
          cardTransactionLifecycleId: "life-bloom-review" } },
    };
    const unresolved = {
      evaluate: vi.fn(async () => ({ status: "ineligible" as const })),
      reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: approvedCommand.bridgeId, status: "settled" as const })),
      retryCardAfterSettlement: vi.fn(async () => {}),
    };
    const unresolvedPool = new MemoryPool();
    const unresolvedRoute = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: operatorToken, kit5AllowedOrigins: allowedOrigins,
    }, unresolvedPool, undefined, { pool: unresolvedPool, gateway: cardProvider, resolveScenario: () => fixture(),
      operatorAuth: { operatorToken, allowedOrigins }, bridgeFunding: unresolved });
    const denied = await callRoute(unresolvedRoute, customers[0]!.id, "bloom-review-action");
    expect(denied.statusCode).toBe(202);
    expect(denied.body.status).toBe("in_progress");
    expect(unresolved.evaluate).toHaveBeenCalledOnce();
    expect(unresolved.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(unresolved.retryCardAfterSettlement).not.toHaveBeenCalled();

    const reviewed = {
      evaluate: vi.fn(async () => ({ status: "approved" as const, command: approvedCommand })),
      reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: approvedCommand.bridgeId, status: "settled" as const })),
      retryCardAfterSettlement: vi.fn(async () => {
        const attempt = reviewedPool.attempts.get(`${scenarioId}/bloom-review-action`);
        if (attempt) {
          attempt.policyStatus = "retry_completed";
          attempt.bridgeId = approvedCommand.bridgeId;
        }
      }),
    };
    const reviewedPool = new MemoryPool();
    const reviewedRoute = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: operatorToken, kit5AllowedOrigins: allowedOrigins,
    }, reviewedPool, undefined, { pool: reviewedPool, gateway: cardProvider, resolveScenario: () => fixture(),
      operatorAuth: { operatorToken, allowedOrigins }, bridgeFunding: reviewed });
    const accepted = await callRoute(reviewedRoute, customers[0]!.id, "bloom-review-action");
    expect(accepted.statusCode).toBe(200);
    expect(accepted.body.status).toBe("completed");
    expect(reviewed.evaluate).toHaveBeenCalledOnce();
    expect(reviewed.reserveAndWaitForSettlement).toHaveBeenCalledOnce();
    expect(reviewed.retryCardAfterSettlement).toHaveBeenCalledOnce();
  });

  it("creates one fixture-backed card attempt per customer and returns provider fields for FlowCard", async () => {
    const pool = new MemoryPool();
    const providerCalls: IssuingSimulationRequest[] = [];
    const provider: IssuingSimulationGateway = {
      createIssuingSimulation: vi.fn(async (request) => {
        const index = providerCalls.push(request) - 1;
        return responseFor(index);
      }),
      getIssuingLifecycle: vi.fn(async () => null),
    };
    const route = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5AllowedOrigins: allowedOrigins,
    }, pool, undefined, { pool, gateway: provider, resolveScenario: () => fixture(), operatorAuth: { operatorToken, allowedOrigins } });

    for (const [index, customer] of customers.entries()) {
      const actionId = `kit5-target:${customer.id}`;
      const response = await callRoute(route, customer.id, actionId);
      const result = response.body.result as Record<string, unknown>;
      const attempt = pool.attempts.get(`${scenarioId}/${actionId}`);

      expect(response.statusCode).toBe(200);
      expect(response.body).toMatchObject({
        status: "completed",
        attemptId: actionId,
        attemptedAmount: { amountCents: customer.amountCents, currency: "USD" },
        result: {
          processResult: responseFor(index).process_result,
          failureReason: responseFor(index).failure_reason || null,
          cardTransactionEventId: `evt_fixture_${index + 1}`,
          cardTransactionId: `txn_fixture_${index + 1}`,
          cardTransactionLifecycleId: `life_fixture_${index + 1}`,
        },
      });
      expect(result.cardTransactionLifecycleId).toBe(attempt?.lifecycleId);
      expect(attempt).toMatchObject({ customerId: customer.id, status: "completed" });
      expect(providerCalls[index]).toMatchObject({
        accountId: customer.accountId,
        cardId: customer.cardId,
        amountCents: customer.amountCents,
        currency: "USD",
      });
    }

    expect(provider.createIssuingSimulation).toHaveBeenCalledTimes(customers.length);
  });

  it("does not fabricate a result for an unexpected provider status or failure reason", async () => {
    const pool = new MemoryPool();
    const provider: IssuingSimulationGateway = {
      createIssuingSimulation: vi.fn(async () => ({
        process_result: "PROCESSING",
        failure_reason: "PROVIDER_REASON_UNRECOGNIZED",
        card_transaction_event_id: "evt_unexpected",
        card_transaction_id: "txn_unexpected",
        card_transaction_lifecycle_id: "life-unexpected",
      })),
      getIssuingLifecycle: vi.fn(async () => null),
    };
    const route = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5AllowedOrigins: allowedOrigins,
    }, pool, undefined, { pool, gateway: provider, resolveScenario: () => fixture(), operatorAuth: { operatorToken, allowedOrigins } });

    const response = await callRoute(route, customers[0]!.id, "unexpected-provider-result");
    const attempt = pool.attempts.get(`${scenarioId}/unexpected-provider-result`);

    expect(response.statusCode).toBe(202);
    expect(response.body).toMatchObject({ status: "ambiguous", attemptId: "unexpected-provider-result" });
    expect(response.body).not.toHaveProperty("result");
    expect(attempt).toMatchObject({ status: "ambiguous", result: null, error: "provider_result_unresolved" });
  });
});
