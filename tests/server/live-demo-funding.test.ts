import { describe, expect, it, vi } from "vitest";
import type { IssuingSimulationGateway, IssuingSimulationRequest } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import type { Kit5CardAttempt } from "../../server/src/db/repositories.js";
import { processNextCustomerAction, type Kit5BridgeFundingBoundary, type Kit5CustomerFixture } from "../../server/src/demo/next-customer.js";

const scenarioId = "kit5-target";
const customerId = "nova-fixture";
const actionId = "nova-action";

class FundingTestPool implements DatabasePool {
  readonly attempt: Kit5CardAttempt | null = null;
  private currentAttempt: Kit5CardAttempt | null = null;
  private nextIndex = 0;

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        if (["BEGIN", "COMMIT", "ROLLBACK"].includes(sql)) return { rows: [], rowCount: 0 };
        if (sql.includes("INSERT INTO kit5_demo_scenario_state")) return { rows: [], rowCount: 1 };
        if (sql.includes("SELECT next_customer_index")) return { rows: [{ next_customer_index: this.nextIndex } as Row], rowCount: 1 };
        if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
          return { rows: this.currentAttempt ? [this.row(this.currentAttempt) as Row] : [], rowCount: this.currentAttempt ? 1 : 0 };
        }
        if (sql.includes("INSERT INTO kit5_card_attempts")) {
          this.currentAttempt = {
            scenarioId: String(values[0]), customerId: String(values[1]), actionId: String(values[2]),
            sequenceIndex: Number(values[3]), lifecycleId: null, status: "accepted", result: null, error: null, fundingClaimed: false,
          };
          return { rows: [this.row(this.currentAttempt) as Row], rowCount: 1 };
        }
        if (sql.includes("UPDATE kit5_demo_scenario_state")) {
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
      if (!this.currentAttempt || this.currentAttempt.status !== "accepted") return { rows: [], rowCount: 0 };
      this.currentAttempt.status = "submitting";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET funding_claimed = true")) {
      if (!this.currentAttempt || this.currentAttempt.status !== "completed" || this.currentAttempt.fundingClaimed) return { rows: [], rowCount: 0 };
      this.currentAttempt.fundingClaimed = true;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET lifecycle_id =")) {
      if (!this.currentAttempt || this.currentAttempt.status !== "submitting") return { rows: [], rowCount: 0 };
      this.currentAttempt.lifecycleId = String(values[2]);
      this.currentAttempt.status = "in_progress";
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET status = $3, provider_result")) {
      if (!this.currentAttempt || !["submitting", "in_progress", "ambiguous"].includes(this.currentAttempt.status)) return { rows: [], rowCount: 0 };
      this.currentAttempt.status = values[2] as Kit5CardAttempt["status"];
      this.currentAttempt.result = values[3] ? JSON.parse(String(values[3])) as Record<string, unknown> : null;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2")) {
      return { rows: this.currentAttempt ? [this.row(this.currentAttempt) as Row] : [], rowCount: this.currentAttempt ? 1 : 0 };
    }
    throw new Error(`Unexpected pool query: ${sql}`);
  }

  async end(): Promise<void> {}

  private row(attempt: Kit5CardAttempt): Record<string, unknown> {
    return {
      scenario_id: attempt.scenarioId, customer_id: attempt.customerId, action_id: attempt.actionId,
      sequence_index: attempt.sequenceIndex, lifecycle_id: attempt.lifecycleId, status: attempt.status,
      provider_result: attempt.result, error: attempt.error,
      funding_claimed: attempt.fundingClaimed,
    };
  }
}

function fixture(): Kit5CustomerFixture {
  return {
    orderedCustomerIds: [customerId],
    // Fictional test-only policy facts. Production uses operator-verified mappings.
    policyFacts: () => ({ transactionType: "AUTHORIZATION", transactionCategory: "PURCHASE", merchantCategoryCode: "5411" }),
    resolveCustomer: (id) => id === customerId ? {
      accountId: "fixture-account-nova",
      cardId: "fixture-card-nova",
      amountCents: 800_000,
      currency: "USD",
      merchant: "Fixture Supplier",
    } : null,
  };
}

function gateway(failureReason: string | null = "INSUFFICIENT_FUNDS"): IssuingSimulationGateway {
  return {
    createIssuingSimulation: vi.fn(async (request: IssuingSimulationRequest) => ({
      process_result: "DECLINED",
      failure_reason: failureReason,
      card_transaction_event_id: "event-fixture",
      card_transaction_id: "transaction-fixture",
      card_transaction_lifecycle_id: request.lifecycleId ?? "life-provider-nova",
    })),
    getIssuingLifecycle: vi.fn(async () => null),
  };
}

function boundary(overrides: Partial<Kit5BridgeFundingBoundary> = {}): Kit5BridgeFundingBoundary {
  return {
    evaluate: vi.fn(async () => ({
      status: "approved" as const,
      command: {
        bridgeId: "00000000-0000-4000-8000-000000000008",
        customerId,
        amountCents: 600_000,
        repaymentTerm: "asap" as const,
      },
    })),
    reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: "fixture-bridge", status: "settled" as const })),
    retryCardAfterSettlement: vi.fn(async () => {}),
    ...overrides,
  };
}

async function run(
  provider: IssuingSimulationGateway,
  funding: Kit5BridgeFundingBoundary,
) {
  return processNextCustomerAction(new FundingTestPool(), provider, { scenarioId, customerId, actionId }, fixture(), funding);
}

describe("Kit 5 bridge funding gate", () => {
  it("evaluates policy only for provider-confirmed insufficient funds and retries only after settlement", async () => {
    const provider = gateway();
    const funding = boundary();
    const result = await run(provider, funding);

    // The mock retry callback does not persist a terminal retry result, so the action remains resumable.
    expect(result.status).toBe("in_progress");
    expect(funding.evaluate).toHaveBeenCalledWith(expect.objectContaining({
      customerId,
      actionId,
      attemptedAmountCents: 800_000,
      providerResult: expect.objectContaining({ processResult: "DECLINED", failureReason: "INSUFFICIENT_FUNDS" }),
    }));
    expect(funding.reserveAndWaitForSettlement).toHaveBeenCalledWith(expect.objectContaining({
      customerId,
      amountCents: 600_000,
    }));
    expect(funding.retryCardAfterSettlement).toHaveBeenCalledWith(expect.objectContaining({
      accountId: "fixture-account-nova",
      cardId: "fixture-card-nova",
      amountCents: 800_000,
    }));
  });

  it("does not rerun funding policy or reservation when the completed action is replayed", async () => {
    const pool = new FundingTestPool();
    const provider = gateway();
    const funding = boundary();
    const input = { scenarioId, customerId, actionId };
    await processNextCustomerAction(pool, provider, input, fixture(), funding);
    await processNextCustomerAction(pool, provider, input, fixture(), funding);

    expect(provider.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(funding.evaluate).toHaveBeenCalledOnce();
    expect(funding.reserveAndWaitForSettlement).toHaveBeenCalledOnce();
    expect(funding.retryCardAfterSettlement).toHaveBeenCalledOnce();
  });

  it("does not evaluate or bridge a different provider decline", async () => {
    const funding = boundary();
    await run(gateway("TRANSACTION_BLOCKED"), funding);

    expect(funding.evaluate).not.toHaveBeenCalled();
    expect(funding.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(funding.retryCardAfterSettlement).not.toHaveBeenCalled();
  });

  it("does not retry while the transfer is pending", async () => {
    const funding = boundary({
      reserveAndWaitForSettlement: vi.fn(async () => ({ bridgeId: "fixture-bridge", status: "pending" as const })),
    });
    await run(gateway(), funding);

    expect(funding.evaluate).toHaveBeenCalledOnce();
    expect(funding.reserveAndWaitForSettlement).toHaveBeenCalledOnce();
    expect(funding.retryCardAfterSettlement).not.toHaveBeenCalled();
  });

  it("passes the policy-owned command unchanged and permits a second issuing call only after settlement", async () => {
    const events: string[] = [];
    const command = {
      bridgeId: "00000000-0000-4000-8000-000000000008",
      customerId,
      amountCents: 600_000,
      repaymentTerm: "asap" as const,
    };
    const provider = gateway();
    const funding = boundary({
      evaluate: vi.fn(async () => {
        events.push("policy");
        return { status: "approved" as const, command };
      }),
      reserveAndWaitForSettlement: vi.fn(async (approvedCommand) => {
        events.push("reserve");
        expect(approvedCommand).toBe(command);
        return { bridgeId: command.bridgeId, status: "settled" as const };
      }),
      retryCardAfterSettlement: vi.fn(async (retry) => {
        events.push("retry");
        await provider.createIssuingSimulation({
          ...retry,
          merchant: "Fixture Supplier",
          lifecycleId: "retry-lifecycle-fixture",
        });
      }),
    });

    await run(provider, funding);

    expect(events).toEqual(["policy", "reserve", "retry"]);
    expect(funding.reserveAndWaitForSettlement).toHaveBeenCalledOnce();
    expect(funding.retryCardAfterSettlement).toHaveBeenCalledOnce();
    expect(provider.createIssuingSimulation).toHaveBeenCalledTimes(2);
    expect(provider.createIssuingSimulation).toHaveBeenLastCalledWith(expect.objectContaining({
      accountId: "fixture-account-nova",
      cardId: "fixture-card-nova",
      amountCents: 800_000,
      currency: "USD",
      lifecycleId: "retry-lifecycle-fixture",
    }));
  });

  it.each(["rejected", "ineligible"] as const)("does not create a bridge for a policy %s decision", async (status) => {
    const funding = boundary({
      evaluate: vi.fn(async () => ({ status })),
    });
    await run(gateway(), funding);

    expect(funding.reserveAndWaitForSettlement).not.toHaveBeenCalled();
    expect(funding.retryCardAfterSettlement).not.toHaveBeenCalled();
  });

  it("preserves an unexpected provider failure reason without translating it", async () => {
    const funding = boundary();
    const result = await run(gateway("CARD_INACTIVE"), funding);

    expect(result.status).toBe("completed");
    if (result.status === "completed") expect(result.result.failureReason).toBe("CARD_INACTIVE");
    expect(funding.evaluate).not.toHaveBeenCalled();
  });
});
