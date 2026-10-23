import type { IncomingMessage, ServerResponse } from "node:http";
import type { IssuingSimulationGateway, IssuingSimulationResponse } from "../../airwallex/client.js";
import type { AirwallexGateway } from "../../airwallex/client.js";
import type { DatabasePool } from "../../db/pool.js";
import { createKit5RepaymentDeposit, type Kit5DepositTarget } from "../../collections/kit5-repayment-deposit.js";
import { processNextCustomerAction, reconcileKit5CustomerAction, type Kit5BridgeFundingBoundary, type Kit5CustomerFixture, type NextCustomerInput } from "../../demo/next-customer.js";
import { isKit5FixtureCheckpointInput } from "../../demo/kit5-scenario-resolver.js";
import { authenticatedKit5OperatorActor, authorizeKit5OperatorRequest, type Kit5OperatorAuthConfig } from "../kit5-operator-auth.js";

const MAX_REQUEST_BYTES = 16_384;
const ACTION_ID_PATTERN = /^[A-Za-z0-9._:-]{1,120}$/;
const IDENTIFIER_PATTERN = /^[A-Za-z0-9._:-]{1,100}$/;

export interface Kit5PolicyDecisionSummary {
  outcome: "human_review" | "auto_approved" | "declined" | "blocked";
  amountCents: number;
  creditLineCents: number;
  tier: string;
  capitalBand: string | null;
  door: string | null;
  reasonCodes: string[];
  rationale: string;
}

export interface Kit5DemoRouteDependencies {
  pool: DatabasePool;
  gateway: IssuingSimulationGateway;
  resolveScenario: (scenarioId: string) => Kit5CustomerFixture | null | Promise<Kit5CustomerFixture | null>;
  resolveFixtureCheckpoint?: (input: NextCustomerInput) => Kit5CustomerFixture | null | Promise<Kit5CustomerFixture | null>;
  operatorAuth: Kit5OperatorAuthConfig;
  bridgeFunding?: Kit5BridgeFundingBoundary;
  repaymentDepositGateway?: AirwallexGateway;
  resolveRepaymentDepositTarget?: (scenarioId: string, customerId: string) => Kit5DepositTarget | null | Promise<Kit5DepositTarget | null>;
}

/** Create or reconcile the one allowlisted Bloom provider attempt, without policy funding or card retry. */
export async function handleKit5FixtureCheckpoint(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const input = validateFixtureCheckpointInput(body);
  if (!input) {
    send(response, 400, { error: "invalid_fixture_checkpoint" });
    return;
  }
  if (!isKit5FixtureCheckpointInput(input)) {
    send(response, 409, { status: "rejected", reason: "fixture_checkpoint_not_allowed" });
    return;
  }
  if (!dependencies.resolveFixtureCheckpoint) {
    send(response, 503, { error: "kit5_fixture_checkpoint_unavailable" });
    return;
  }

  try {
    const fixture = await dependencies.resolveFixtureCheckpoint(input);
    if (!fixture) {
      send(response, 409, { status: "rejected", reason: "fixture_checkpoint_mapping_unavailable" });
      return;
    }
    // Intentionally omit dependencies.bridgeFunding. This route only records
    // and reconciles the Airwallex issuing attempt.
    const outcome = await processNextCustomerAction(dependencies.pool, dependencies.gateway, input, fixture);
    if (outcome.status === "rejected") {
      send(response, 409, { status: "rejected", reason: outcome.reason });
      return;
    }
    const customer = fixture.resolveCustomer(input.customerId);
    const body = {
      status: outcome.status,
      attemptId: outcome.attempt.actionId,
      lifecycleId: outcome.attempt.lifecycleId,
      attemptedAmount: customer ? { amountCents: customer.amountCents, currency: customer.currency } : null,
      ...(outcome.status === "completed" ? { result: outcome.result } : {}),
    };
    send(response, outcome.status === "completed" ? 200 : outcome.status === "failed" ? 502 : 202, body);
  } catch {
    send(response, 503, { error: "kit5_fixture_checkpoint_unavailable" });
  }
}

/** Accept one local customer action; provider IDs and amounts are resolved server-side. */
export async function handleKit5NextCustomer(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const input = validateInput(body);
  if (!input) {
    send(response, 400, { error: "invalid_action" });
    return;
  }
  try {
    const fixture = await dependencies.resolveScenario(input.scenarioId);
    const attemptedAmount = fixture?.resolveCustomer(input.customerId);
    const outcome = await processNextCustomerAction(dependencies.pool, dependencies.gateway, input, fixture, dependencies.bridgeFunding);
    if (outcome.status === "rejected") {
      send(response, 409, { status: "rejected", reason: outcome.reason });
      return;
    }
    if (outcome.status === "completed") {
      const policyDecision = outcome.attempt.policyDecisionId
        ? await loadKit5PolicyDecisionSummary(dependencies.pool, outcome.attempt.policyDecisionId)
        : null;
      send(response, 200, {
        status: outcome.status,
        attemptId: outcome.attempt.actionId,
        attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
        result: outcome.result,
        policyStatus: outcome.attempt.policyStatus,
        policyError: outcome.attempt.policyError,
        bridgeId: outcome.attempt.bridgeId,
        retryLifecycleId: outcome.attempt.retryLifecycleId,
        retryResult: outcome.attempt.retryResult,
        retryError: outcome.attempt.retryError,
        humanApproved: Boolean(outcome.attempt.policyActorId && outcome.attempt.policyApprovalReason),
        policyDecision,
      });
      return;
    }
    send(response, outcome.status === "failed" ? 502 : 202, {
      status: outcome.status,
      attemptId: outcome.attempt.actionId,
      lifecycleId: outcome.attempt.lifecycleId,
      policyStatus: outcome.attempt.policyStatus,
      policyError: outcome.attempt.policyError,
      policyDecisionId: outcome.attempt.policyDecisionId,
      bridgeId: outcome.attempt.bridgeId,
      retryLifecycleId: outcome.attempt.retryLifecycleId,
      retryResult: outcome.attempt.retryResult,
      retryError: outcome.attempt.retryError,
      policyDecision: outcome.attempt.policyDecisionId
        ? await loadKit5PolicyDecisionSummary(dependencies.pool, outcome.attempt.policyDecisionId)
        : null,
      ...(outcome.attempt.status === "completed" && outcome.attempt.result ? { result: outcome.attempt.result } : {}),
      attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
    });
  } catch {
    send(response, 503, { error: "kit5_attempt_unavailable" });
  }
}

export async function handleKit5AttemptStatus(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const input = validateInput(body);
  if (!input) {
    send(response, 400, { error: "invalid_action" });
    return;
  }
  const fixture = await dependencies.resolveScenario(input.scenarioId);
  const attemptedAmount = fixture?.resolveCustomer(input.customerId);
  let attempt = await reconcileKit5CustomerAction(dependencies.pool, dependencies.gateway, input, fixture);
  if (!attempt) {
    send(response, 404, { error: "kit5_attempt_not_found" });
    return;
  }
  const customer = fixture?.resolveCustomer(input.customerId);
  if (attempt.status === "completed" && attempt.result && customer && dependencies.bridgeFunding?.recover) {
    await dependencies.bridgeFunding.recover({
      ...input,
      accountId: customer.accountId,
      cardId: customer.cardId,
      attemptedAmountCents: customer.amountCents,
      amountCents: customer.amountCents,
      currency: customer.currency,
      providerResult: attempt.result as unknown as IssuingSimulationResponse,
    });
    attempt = await reconcileKit5CustomerAction(dependencies.pool, dependencies.gateway, input, fixture);
  }
  if (!attempt) {
    send(response, 404, { error: "kit5_attempt_not_found" });
    return;
  }
  const fundingPending = attempt.result?.processResult === "DECLINED" && attempt.result.failureReason === "INSUFFICIENT_FUNDS" &&
    ["evaluating", "pending_review", "reserving", "reserved", "settled", "retry_submitting", "retry_ambiguous"].includes(attempt.policyStatus);
  send(response, 200, {
    status: attempt.status === "accepted" || attempt.status === "submitting" || fundingPending ? "in_progress" : attempt.status,
    attemptId: attempt.actionId,
    lifecycleId: attempt.lifecycleId,
    attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
    ...(attempt.status === "completed" && attempt.result ? { result: attempt.result } : {}),
    policyStatus: attempt.policyStatus,
    policyError: attempt.policyError,
    policyDecisionId: attempt.policyDecisionId,
    bridgeId: attempt.bridgeId,
    retryLifecycleId: attempt.retryLifecycleId,
    retryResult: attempt.retryResult,
    retryError: attempt.retryError,
    humanApproved: Boolean(attempt.policyActorId && attempt.policyApprovalReason),
    policyDecision: attempt.policyDecisionId
      ? await loadKit5PolicyDecisionSummary(dependencies.pool, attempt.policyDecisionId)
      : null,
  });
}

async function loadKit5PolicyDecisionSummary(
  pool: DatabasePool,
  decisionId: string,
): Promise<Kit5PolicyDecisionSummary | null> {
  const result = await pool.query<{
    outcome: Kit5PolicyDecisionSummary["outcome"];
    amount_cents: string | number;
    credit_line_cents: string | number;
    tier: string;
    capital_band: string | null;
    door: string | null;
    reason_codes: string[];
    rationale: string;
  }>(
    `SELECT outcome, amount_cents, credit_line_cents, tier, capital_band, door, reason_codes, rationale
       FROM policy_request_evaluations WHERE decision_id = $1::uuid`, [decisionId]);
  const row = result.rows[0];
  if (!row) return null;
  return { outcome: row.outcome, amountCents: Number(row.amount_cents), creditLineCents: Number(row.credit_line_cents),
    tier: row.tier, capitalBand: row.capital_band, door: row.door,
    reasonCodes: row.reason_codes, rationale: row.rationale };
}

/** Approve only the stored exact-term Kit 5 decision, with actor identity bound to the signed session. */
export async function handleKit5Approval(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  const actorId = authenticatedKit5OperatorActor(request, dependencies.operatorAuth);
  if (!actorId) {
    send(response, 503, { error: "kit5_operator_identity_unconfigured" });
    return;
  }
  if (!dependencies.bridgeFunding?.approveHumanReview) {
    send(response, 503, { error: "kit5_policy_approval_unavailable" });
    return;
  }
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const approval = validateApprovalInput(body);
  if (!approval) {
    send(response, 400, { error: "invalid_approval" });
    return;
  }
  try {
    const result = await dependencies.bridgeFunding.approveHumanReview({ ...approval, actorId });
    send(response, result.status === "blocked" ? 409 : 200, { ...result });
  } catch {
    send(response, 503, { error: "kit5_approval_unavailable" });
  }
}

/** Create or recover Nova's fixed-term deposit using only server-resolved provider bindings. */
export async function handleKit5RepaymentDeposit(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  if (!dependencies.repaymentDepositGateway || !dependencies.resolveRepaymentDepositTarget) {
    send(response, 503, { error: "kit5_repayment_deposit_unavailable" });
    return;
  }
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    send(response, 400, { error: "invalid_repayment_deposit" });
    return;
  }
  const record = body as Record<string, unknown>;
  if (Object.keys(record).sort().join(",") !== "actionId,customerId,scenarioId" ||
      typeof record.scenarioId !== "string" || !IDENTIFIER_PATTERN.test(record.scenarioId) ||
      typeof record.customerId !== "string" || !IDENTIFIER_PATTERN.test(record.customerId) ||
      typeof record.actionId !== "string" || !ACTION_ID_PATTERN.test(record.actionId)) {
    send(response, 400, { error: "invalid_repayment_deposit" });
    return;
  }
  try {
    const target = await dependencies.resolveRepaymentDepositTarget(record.scenarioId, record.customerId);
    const result = await createKit5RepaymentDeposit(dependencies.pool, dependencies.repaymentDepositGateway, {
      scenarioId: record.scenarioId, customerId: record.customerId, actionId: record.actionId, target,
    });
    send(response, result.status === "blocked" || result.status === "ambiguous" ? 409 : 200, { ...result });
  } catch (error) {
    const message = error instanceof Error ? error.message : "kit5_repayment_deposit_unavailable";
    send(response, 409, { error: message });
  }
}

function validateInput(value: unknown): NextCustomerInput | null {
  if (typeof value !== "object" || value === null) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.scenarioId !== "string" || !IDENTIFIER_PATTERN.test(record.scenarioId)
    || typeof record.customerId !== "string" || !IDENTIFIER_PATTERN.test(record.customerId)
    || typeof record.actionId !== "string" || !ACTION_ID_PATTERN.test(record.actionId)) {
    return null;
  }
  return { scenarioId: record.scenarioId, customerId: record.customerId, actionId: record.actionId };
}

function validateFixtureCheckpointInput(value: unknown): NextCustomerInput | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (Object.keys(record).sort().join(",") !== "actionId,customerId,scenarioId" ||
      typeof record.scenarioId !== "string" || !IDENTIFIER_PATTERN.test(record.scenarioId) ||
      typeof record.customerId !== "string" || !IDENTIFIER_PATTERN.test(record.customerId) ||
      typeof record.actionId !== "string" || !ACTION_ID_PATTERN.test(record.actionId)) return null;
  return { scenarioId: record.scenarioId, customerId: record.customerId, actionId: record.actionId };
}

function validateApprovalInput(value: unknown): { scenarioId: string; customerId: string; actionId: string; reason: string } | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  if (keys.join(",") !== "actionId,customerId,reason,scenarioId" ||
      typeof record.scenarioId !== "string" || !IDENTIFIER_PATTERN.test(record.scenarioId) ||
      typeof record.customerId !== "string" || !IDENTIFIER_PATTERN.test(record.customerId) ||
      typeof record.actionId !== "string" || !ACTION_ID_PATTERN.test(record.actionId) ||
      typeof record.reason !== "string" || !record.reason.trim() || record.reason.trim().length > 500) return null;
  return { scenarioId: record.scenarioId, customerId: record.customerId, actionId: record.actionId, reason: record.reason.trim() };
}

function readBody(request: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on("data", (chunk: Buffer | string) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += bytes.length;
      if (size > MAX_REQUEST_BYTES) {
        reject(new Error("request too large"));
        request.destroy();
        return;
      }
      chunks.push(bytes);
    });
    request.on("end", () => resolve(Buffer.concat(chunks)));
    request.on("error", reject);
  });
}

function send(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
