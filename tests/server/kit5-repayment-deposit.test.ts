import { describe, expect, it, vi } from "vitest";
import type { AirwallexGateway, GlobalAccountDepositSimulationResponse } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { bindKit5DepositSettlement, createKit5RepaymentDeposit } from "../../server/src/collections/kit5-repayment-deposit.js";

const bridgeId = "00000000-0000-4000-8000-000000000001";
const input = { scenarioId: "kit5-target", customerId: "nova-internal", actionId: "nova-action", target: {
  accountId: "acct-provider", globalAccountId: "global-usd",
} };

describe("Kit 5 Nova repayment deposit", () => {
  it("persists one bridge-bound claim and sends the fixed USD amount with a deterministic reference", async () => {
    const db = new DepositPool();
    const create = vi.fn(async () => providerResponse());
    const result = await createKit5RepaymentDeposit(db.pool, gateway(create), input);

    expect(result).toMatchObject({ status: "created", bridgeId, depositId: "dep-nova-1" });
    expect(create).toHaveBeenCalledTimes(1);
    expect(create).toHaveBeenCalledWith(expect.objectContaining({
      accountId: "acct-provider", globalAccountId: "global-usd", amountCents: 800_000,
      statementRef: expect.stringMatching(/^kit5-/),
    }));
    expect(db.operation).toMatchObject({ customer_id: "nova-internal", bridge_id: bridgeId, amount_cents: 800_000, currency: "USD" });
  });

  it("reuses the durable operation for duplicate actions and never repeats provider create", async () => {
    const db = new DepositPool();
    const create = vi.fn(async () => providerResponse());
    await createKit5RepaymentDeposit(db.pool, gateway(create), input);
    const duplicate = await createKit5RepaymentDeposit(db.pool, gateway(create), input);

    expect(duplicate.status).toBe("created");
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("leaves a crash after the durable claim ambiguous and does not blindly retry create", async () => {
    const db = new DepositPool();
    const create = vi.fn(async () => { throw new Error("socket closed after request may have reached provider"); });
    const first = await createKit5RepaymentDeposit(db.pool, gateway(create), input);
    const recovered = await createKit5RepaymentDeposit(db.pool, gateway(create), input);
    const bound = await bindKit5DepositSettlement(db.pool, {
      eventId: "evt-settled-1", depositId: "dep-nova-1", accountId: "acct-provider", globalAccountId: "global-usd",
      statementRef: db.operation!.statement_ref, settledAt: new Date("2026-10-22T12:00:00Z"),
    });

    expect(first.status).toBe("ambiguous");
    expect(recovered.status).toBe("ambiguous");
    expect(bound).toEqual({ customerId: "nova-internal", bridgeId, amountCents: 800_000, accountId: "acct-provider" });
    expect(db.operation?.status).toBe("settled");
    expect(db.eventBindings.get("evt-settled-1")).toBe(db.operation?.id);
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("does not repeat create after provider success when persisting the response crashes", async () => {
    const db = new DepositPool();
    db.failCreateResponsePersistenceOnce = true;
    const create = vi.fn(async () => providerResponse());
    const first = await createKit5RepaymentDeposit(db.pool, gateway(create), input);
    const recovered = await createKit5RepaymentDeposit(db.pool, gateway(create), input);
    const bound = await bindKit5DepositSettlement(db.pool, {
      eventId: "evt-settled-2", depositId: "dep-nova-1", accountId: "acct-provider", globalAccountId: "global-usd",
      statementRef: db.operation!.statement_ref, settledAt: new Date("2026-10-22T12:00:00Z"),
    });

    expect(first.status).toBe("ambiguous");
    expect(recovered.status).toBe("ambiguous");
    expect(bound?.bridgeId).toBe(bridgeId);
    expect(db.operation?.status).toBe("settled");
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("serializes concurrent create actions onto one saved bridge operation", async () => {
    const db = new DepositPool();
    const create = vi.fn(async () => providerResponse());
    const [first, second] = await Promise.all([
      createKit5RepaymentDeposit(db.pool, gateway(create), input),
      createKit5RepaymentDeposit(db.pool, gateway(create), input),
    ]);

    expect(create).toHaveBeenCalledTimes(1);
    expect(first.bridgeId).toBe(bridgeId);
    expect(second.bridgeId).toBe(bridgeId);
    expect(db.operation).not.toBeNull();
  });

  it("binds settlement by saved account/reference and internal customer, rejecting wrong provider scope", async () => {
    const db = new DepositPool();
    await createKit5RepaymentDeposit(db.pool, gateway(vi.fn(async () => providerResponse())), input);
    await expect(bindKit5DepositSettlement(db.pool, {
      eventId: "evt-wrong-scope", depositId: "dep-nova-1", accountId: "other-account", globalAccountId: "global-usd", statementRef: db.operation!.statement_ref,
      settledAt: new Date("2026-10-22T12:00:00Z"),
    })).rejects.toThrow(/conflicts with the saved Kit 5 account/);
    expect(db.operation?.status).toBe("created");

    const settled = await bindKit5DepositSettlement(db.pool, {
      eventId: "evt-settled-3", depositId: "dep-nova-1", accountId: "acct-provider", globalAccountId: "global-usd",
      statementRef: db.operation!.statement_ref, settledAt: new Date("2026-10-22T12:00:00Z"),
    });
    expect(settled).toEqual({ customerId: "nova-internal", bridgeId, amountCents: 800_000, accountId: "acct-provider" });
    expect(db.operation?.status).toBe("settled");
    const replay = await bindKit5DepositSettlement(db.pool, {
      eventId: "evt-settled-3", depositId: "dep-nova-1", accountId: "acct-provider", globalAccountId: "global-usd",
      statementRef: db.operation!.statement_ref, settledAt: new Date("2026-10-22T12:01:00Z"),
    });
    expect(replay).toEqual(settled);
  });

  it("fails closed without a server-resolved Global Account mapping", async () => {
    const create = vi.fn(async () => providerResponse());
    await expect(createKit5RepaymentDeposit(new DepositPool().pool, gateway(create), { ...input, target: null }))
      .rejects.toThrow(/mappings are unavailable/);
    expect(create).not.toHaveBeenCalled();
  });

  it("fails closed if the active roster binds the provider account to another internal customer", async () => {
    const db = new DepositPool();
    db.rosterCustomerId = "different-internal-customer";
    const create = vi.fn(async () => providerResponse());
    await expect(createKit5RepaymentDeposit(db.pool, gateway(create), input))
      .rejects.toThrow(/active internal-customer\/connected-account binding/);
    expect(create).not.toHaveBeenCalled();
    expect(db.operation).toBeNull();
  });
});

function providerResponse(): GlobalAccountDepositSimulationResponse {
  return { id: "dep-nova-1", amountCents: 800_000, currency: "USD", depositType: "CREDIT",
    feeAmountCents: 0, status: "settled", createTime: "2026-10-22T12:00:00Z" };
}

function gateway(create: ReturnType<typeof vi.fn>): AirwallexGateway {
  return {
    listConnectedAccounts: async () => [],
    createTransfer: async () => { throw new Error("unused"); },
    createCharge: async () => { throw new Error("unused"); },
    getTransferByRequestId: async () => null,
    getChargeByRequestId: async () => null,
    getBalance: async () => ({ availableCents: 800_000, currency: "USD" }),
    getDeposit: async () => ({ id: "dep-nova-1", amountCents: 800_000, currency: "USD", status: "settled", settledAt: new Date() }),
    createGlobalAccountDepositSimulation: create,
    retrySandboxCard: async () => { throw new Error("unused"); },
  };
}

class DepositPool implements DatabasePool {
  operation: Record<string, unknown> | null = null;
  readonly eventBindings = new Map<string, string>();
  failCreateResponsePersistenceOnce = false;
  rosterCustomerId = input.customerId;
  readonly pool: DatabasePool = this;

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    return this.respond<Row>(sql, values);
  }

  async connect(): Promise<DatabaseClient> {
    return { query: async <Row>(sql: string, values: readonly unknown[] = []) => this.respond<Row>(sql, values), release() {} };
  }

  async end(): Promise<void> {}

  private async respond<Row>(sql: string, values: readonly unknown[]): Promise<QueryResult<Row>> {
    if (["BEGIN", "COMMIT", "ROLLBACK"].includes(sql)) return rows<Row>([]);
    if (sql.includes("FROM kit5_card_attempts AS attempt")) return rows([{
      customer_id: input.customerId, bridge_id: bridgeId, policy_status: "settled",
      policy_actor_id: "operator", policy_approval_reason: "approved after review",
      policy_decision_id: "decision-1", outcome: "auto_approved",
    } as Row]);
    if (sql.includes("SELECT EXISTS (")) return rows([{ ready: true } as Row]);
    if (sql.includes("FROM customer_roster")) return rows([{ customer_id: this.rosterCustomerId } as Row]);
    if (sql.includes("FROM kit5_repayment_deposits WHERE bridge_id")) {
      return rows(this.operation ? [this.operation as Row] : []);
    }
    if (sql.includes("INSERT INTO kit5_repayment_deposits")) {
      if (this.operation) return rows<Row>([]);
      this.operation = {
        id: String(values[0]), scenario_id: values[1], customer_id: values[2], action_id: values[3],
        bridge_id: values[4], account_id: values[5], global_account_id: values[6], amount_cents: values[7],
        currency: "USD", statement_ref: values[8], status: "creating", provider_deposit_id: null,
        provider_result: null, error: null, settled_at: null,
      };
      return rows([this.operation as Row]);
    }
    if (sql.includes("SET provider_deposit_id = $2")) {
      if (this.failCreateResponsePersistenceOnce) {
        this.failCreateResponsePersistenceOnce = false;
        return { rows: [], rowCount: 0 };
      }
      this.operation!.provider_deposit_id = values[1];
      this.operation!.status = "created";
      this.operation!.provider_result = JSON.parse(String(values[2]));
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SET status = 'ambiguous'")) {
      if (this.operation?.status === "creating" || this.operation?.status === "ambiguous") {
        this.operation.status = "ambiguous";
        this.operation.error = values[1];
      }
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM webhook_inbox")) return rows<Row>([]);
    if (sql.includes("FROM collection_attempts AS attempt")) return rows<Row>([]);
    if (sql.includes("INSERT INTO kit5_repayment_deposit_events")) {
      const eventId = String(values[0]);
      const operationId = String(values[1]);
      if (!this.eventBindings.has(eventId)) this.eventBindings.set(eventId, operationId);
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("FROM kit5_repayment_deposit_events")) {
      const operationId = this.eventBindings.get(String(values[0]));
      return rows(operationId ? [{ deposit_operation_id: operationId } as Row] : []);
    }
    if (sql.includes("WHERE (provider_deposit_id = $1")) {
      if (!this.operation || (this.operation.provider_deposit_id !== values[0] && this.operation.statement_ref !== values[1])) return rows<Row>([]);
      return rows([this.operation as Row]);
    }
    if (sql.includes("UPDATE kit5_repayment_deposits SET provider_deposit_id = COALESCE")) {
      if (!this.operation || this.operation.account_id !== values[3] ||
          (this.operation.provider_deposit_id && this.operation.provider_deposit_id !== values[1])) return rows<Row>([]);
      this.operation.provider_deposit_id = values[1];
      this.operation.status = "settled";
      this.operation.settled_at = values[2];
      return rows([this.operation as Row]);
    }
    if (sql.includes("WHERE id = $1")) return rows(this.operation ? [this.operation as Row] : []);
    throw new Error(`Unexpected SQL in DepositPool: ${sql}`);
  }
}

function rows<Row>(items: Row[]): QueryResult<Row> { return { rows: items, rowCount: items.length }; }
