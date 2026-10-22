import { describe, expect, it, vi } from "vitest";
import type { AirwallexGateway } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { calculateBridgeFeeCents } from "../../server/src/collections/fee.js";
import { applySettledCollectionCharge, processSettledDeposit } from "../../server/src/collections/service.js";
import { processWebhookEvent } from "../../server/src/webhooks/process.js";

const bridgeId = "00000000-0000-4000-8000-000000000001";
const deposit = {
  depositId: "dep_1", customerId: "acct_1", accountId: "acct_1", amountCents: 800_000,
  settledAt: new Date("2026-10-10T00:00:00.000Z"),
};

describe("bridge collection fees", () => {
  it("applies the seven-day minimum and the trigger-time daily fee", () => {
    const fundedAt = new Date("2026-10-01T00:00:00.000Z");
    expect(calculateBridgeFeeCents({ principalCents: 500_000, fundedAt, triggeredAt: new Date("2026-10-04T00:00:00.000Z") })).toBe(1_750);
    expect(calculateBridgeFeeCents({ principalCents: 600_000, fundedAt, triggeredAt: new Date("2026-10-10T00:00:00.000Z") })).toBe(2_700);
  });

  it("caps a new attempt at both the triggering deposit and current balance, and creates one fee charge", async () => {
    const db = collectionPool({ availableCents: 2_000, principalCents: 600_000, feeCents: 0 });
    const result = await processSettledDeposit(db.pool, gateway(2_000), deposit);

    expect(result).toMatchObject({ created: true, chargedCents: 2_000 });
    expect(db.attempts).toBe(1);
    expect(db.operations).toEqual([{ operationType: "fee", amountCents: 2_000 }]);
    expect(db.chargePayloads).toEqual([{ source: "acct_1", reason: "professional_business_services", reference: `Bridge fee ${bridgeId}` }]);
    expect(db.statements.some((sql) => sql.includes("UNIQUE (bridge_id, deposit_id)"))).toBe(false);

    const duplicate = await processSettledDeposit(db.pool, gateway(2_000), deposit);
    expect(duplicate.created).toBe(false);
    expect(db.attempts).toBe(1);
  });

  it("continues servicing an already-funded bridge after customer deactivation", async () => {
    const db = collectionPool({ availableCents: 2_000, principalCents: 600_000, feeCents: 0, rosterActive: false });
    const result = await processSettledDeposit(db.pool, gateway(2_000), deposit);

    expect(result).toMatchObject({ created: true, chargedCents: 2_000 });
    expect(db.statements.some((sql) => sql.includes("customer_roster"))).toBe(false);
  });

  it("collects Nova's $27 trigger-time fee from an $8K deposit, then queues $6K principal after settlement", async () => {
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0 });
    const attempt = await processSettledDeposit(db.pool, gateway(800_000), deposit);
    expect(attempt.chargedCents).toBe(2_700);
    expect(db.operations).toEqual([{ operationType: "fee", amountCents: 2_700 }]);
    expect(db.ledgerEvents).toContainEqual(expect.objectContaining({ type: "fee_assessed", amountCents: 2_700 }));

    const client = await db.pool.connect();
    await applySettledCollectionCharge(client, {
      id: "fee-operation", bridge_id: bridgeId, operation_type: "fee", amount_cents: 2_700,
    }, deposit.settledAt, 800_000);

    expect(db.ledgerEvents).toContainEqual(expect.objectContaining({ type: "fee_repaid", amountCents: 2_700 }));
    expect(db.operations).toEqual([
      { operationType: "fee", amountCents: 2_700 },
      { operationType: "principal", amountCents: 600_000 },
    ]);
  });

  it("maps a settled connected-account deposit to the internal customer and keeps provider account scope", async () => {
    const internalCustomerId = "nova-internal-customer";
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0, customerId: internalCustomerId });
    const eventId = "evt-deposit-end-to-end";
    const rawBody = Buffer.from(JSON.stringify({
      id: eventId,
      name: "deposit.settled",
      created_at: deposit.settledAt.toISOString(),
      data: { object: { id: deposit.depositId, account_id: deposit.accountId, global_account_id: "verified-global-account" } },
    }));
    const pool: DatabasePool = {
      ...db.pool,
      query: async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
        if (sql.includes("FROM webhook_inbox")) return rows([{
          provider_event_id: eventId, event_type: "deposit.settled", raw_body: rawBody, processed_at: null,
        }] as Row[]);
        if (sql.includes("FROM kit5_repayment_deposits")) return rows([kit5DepositRow(internalCustomerId) as Row]);
        return db.pool.query<Row>(sql, values);
      },
      connect: async () => {
        const client = await db.pool.connect();
        return {
          release: () => client.release(),
          query: async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
            if (sql.includes("UPDATE kit5_repayment_deposits")) return rows([kit5DepositRow(internalCustomerId) as Row]);
            if (sql.includes("FROM kit5_repayment_deposit_events")) return rows([{ deposit_operation_id: "deposit-operation-1" } as Row]);
            return client.query<Row>(sql, values);
          },
        };
      },
    };
    const airwallex = gateway(800_000);
    let depositLookups = 0;
    const balanceRead = vi.fn(async () => ({ availableCents: 800_000, currency: "USD" as const }));
    airwallex.getBalance = balanceRead;
    airwallex.getDeposit = async (depositId, accountId) => {
      depositLookups++;
      expect([depositId, accountId]).toEqual([deposit.depositId, deposit.accountId]);
      return {
        id: depositId,
        amountCents: deposit.amountCents,
        currency: "USD",
        status: "settled",
        settledAt: new Date("2026-10-08T00:00:00.000Z"),
      };
    };

    await processWebhookEvent(pool, airwallex, eventId);
    await processWebhookEvent(pool, airwallex, eventId);

    expect(depositLookups).toBe(2);
    expect(db.attempts).toBe(1);
    expect(db.operations).toEqual([{ operationType: "fee", amountCents: 2_100 }]);
    expect(db.ledgerEvents).toContainEqual(expect.objectContaining({
      type: "fee_assessed",
      amountCents: 2_100,
      occurredAt: new Date("2026-10-08T00:00:00.000Z"),
    }));
    expect(db.chargePayloads[0]?.source).toBe(deposit.accountId);
    expect(balanceRead).toHaveBeenCalledWith(deposit.accountId, "USD");
    expect(db.statements.some((sql) => sql.includes("WHERE customer_id = $1") && sql.includes("bridge_cases"))).toBe(true);
    expect(db.statements.some((sql) => sql.includes("SET processed_at = now()"))).toBe(true);
  });

  it("schedules a generic signed deposit with the uniquely mapped internal customer when no Kit 5 association exists", async () => {
    const internalCustomerId = "customer-internal-42";
    const db = collectionPool({
      availableCents: 800_000,
      principalCents: 600_000,
      feeCents: 0,
      customerId: internalCustomerId,
      rosterMatches: [internalCustomerId],
    });
    const eventId = "evt-generic-deposit-no-kit5-association";
    const rawBody = Buffer.from(JSON.stringify({
      id: eventId,
      name: "deposit.settled",
      created_at: deposit.settledAt.toISOString(),
      data: { object: { id: deposit.depositId, account_id: deposit.accountId, global_account_id: "verified-global-account" } },
    }));
    const pool: DatabasePool = {
      ...db.pool,
      query: async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
        if (sql.includes("FROM webhook_inbox")) return rows([{
          provider_event_id: eventId, event_type: "deposit.settled", raw_body: rawBody, processed_at: null,
        }] as Row[]);
        return db.pool.query<Row>(sql, values);
      },
    };
    const airwallex = gateway(800_000);
    const depositLookup = vi.fn(async (depositId: string, _accountId: string) => ({
      id: depositId,
      amountCents: deposit.amountCents,
      currency: "USD" as const,
      status: "settled" as const,
      settledAt: deposit.settledAt,
    }));
    const balanceLookup = vi.fn(async () => ({ availableCents: 800_000, currency: "USD" as const }));
    airwallex.getDeposit = depositLookup;
    airwallex.getBalance = balanceLookup;

    await processWebhookEvent(pool, airwallex, eventId);

    expect(depositLookup).toHaveBeenCalledWith(deposit.depositId, deposit.accountId);
    expect(balanceLookup).toHaveBeenCalledWith(deposit.accountId, "USD");
    expect(db.bridgeCustomerQueries).toContain(internalCustomerId);
    expect(db.attempts).toBe(1);
    expect(db.operations).toEqual([{ operationType: "fee", amountCents: 2_700 }]);
    expect(db.chargePayloads[0]?.source).toBe(deposit.accountId);
  });

  it.each([{ rosterMatches: [] }, { rosterMatches: ["nova-one", "nova-two"] }, { rosterMatches: [""] }])(
    "fails closed when the connected account has no unique valid active customer mapping ($rosterMatches)", async ({ rosterMatches }) => {
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0, rosterMatches });
    const eventId = "evt-deposit-unmapped";
    const rawBody = Buffer.from(JSON.stringify({ id: eventId, name: "deposit.settled",
      created_at: deposit.settledAt.toISOString(), data: { object: { id: deposit.depositId, account_id: deposit.accountId, global_account_id: "verified-global-account" } } }));
    const pool: DatabasePool = { ...db.pool, query: async <Row>(sql: string, values?: readonly unknown[]) => {
      if (sql.includes("FROM webhook_inbox")) return rows([{ provider_event_id: eventId, event_type: "deposit.settled", raw_body: rawBody, processed_at: null }] as Row[]);
      return db.pool.query<Row>(sql, values);
    } };
    const airwallex = gateway(800_000);
    const getDeposit = vi.fn(airwallex.getDeposit);
    airwallex.getDeposit = getDeposit;

    await expect(processWebhookEvent(pool, airwallex, eventId)).rejects.toThrow(/exactly one active customer|binding is malformed/);

    expect(getDeposit).not.toHaveBeenCalled();
    expect(db.attempts).toBe(0);
    expect(db.operations).toEqual([]);
    },
  );

  it("rejects a SETTLED provider deposit without a valid settled_at timestamp", async () => {
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0 });
    const eventId = "evt-deposit-missing-settlement-time";
    const rawBody = Buffer.from(JSON.stringify({
      id: eventId,
      name: "deposit.settled",
      data: { object: { id: deposit.depositId, account_id: deposit.accountId, global_account_id: "verified-global-account" } },
    }));
    const pool: DatabasePool = {
      ...db.pool,
      query: async <Row>(sql: string, values?: readonly unknown[]): Promise<QueryResult<Row>> => {
        if (sql.includes("FROM webhook_inbox")) return rows([{
          provider_event_id: eventId, event_type: "deposit.settled", raw_body: rawBody, processed_at: null,
        }] as Row[]);
        return db.pool.query<Row>(sql, values);
      },
    };
    const airwallex = gateway(800_000);
    airwallex.getDeposit = async (depositId) => ({
      id: depositId,
      amountCents: deposit.amountCents,
      currency: "USD",
      status: "settled",
      settledAt: new Date(Number.NaN),
    });

    await expect(processWebhookEvent(pool, airwallex, eventId)).rejects.toThrow("valid settled_at timestamp");
    expect(db.attempts).toBe(0);
    expect(db.statements.some((sql) => sql.includes("SET processed_at = now()"))).toBe(false);
  });

  it("does not reduce fees or principal or queue principal while the fee is pending", async () => {
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 2_700 });
    const result = await processSettledDeposit(db.pool, gateway(800_000), deposit);
    expect(result.chargedCents).toBe(2_700);
    expect(db.operations.map((operation) => operation.operationType)).toEqual(["fee"]);
    expect(db.ledgerEvents.map((event) => event.type)).toEqual([]);
  });

  it("reserves pending fee and principal charges against later deposits until their status settles", async () => {
    const pendingFee = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 2_700, pendingFeeCents: 2_700 });
    const feeAttempt = await processSettledDeposit(pendingFee.pool, gateway(800_000), deposit);
    expect(feeAttempt.chargedCents).toBe(0);
    expect(pendingFee.operations).toEqual([]);

    const pendingPrincipal = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0, feeAssessed: true, pendingPrincipalCents: 600_000 });
    const principalAttempt = await processSettledDeposit(pendingPrincipal.pool, gateway(800_000), deposit);
    expect(principalAttempt.chargedCents).toBe(0);
    expect(pendingPrincipal.operations).toEqual([]);
  });

  it("queues principal only after the fee charge settles and keeps it within the same deposit cap", async () => {
    const db = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0 });
    const client = await db.pool.connect();
    await applySettledCollectionCharge(client, {
      id: "fee-operation", bridge_id: bridgeId, operation_type: "fee", amount_cents: 2_700,
    }, new Date("2026-10-10T00:00:00.000Z"), 800_000);

    expect(db.ledgerEvents.map((event) => event.type)).toEqual(["fee_repaid"]);
    expect(db.operations).toEqual([{ operationType: "principal", amountCents: 600_000 }]);
  });

  it("caps fee-first and following principal collection to the exact $8K triggering deposit", async () => {
    const db = collectionPool({ availableCents: 1_000_000, principalCents: 1_000_000, feeCents: 0 });
    const triggerTime = new Date("2026-10-07T00:00:00.000Z");
    const triggered = await processSettledDeposit(db.pool, gateway(1_000_000), {
      ...deposit, amountCents: 800_000, bridgeId, settledAt: triggerTime,
    });
    expect(triggered.chargedCents).toBe(3_500);
    const client = await db.pool.connect();
    await applySettledCollectionCharge(client, {
      id: "fee-operation", bridge_id: bridgeId, operation_type: "fee", amount_cents: 3_500,
    }, triggerTime, 1_000_000);
    expect(db.operations).toEqual([
      { operationType: "fee", amountCents: 3_500 },
      { operationType: "principal", amountCents: 796_500 },
    ]);
    expect(db.operations.reduce((sum, operation) => sum + operation.amountCents, 0)).toBe(800_000);
  });

  it.each([
    { term: "15_days" as const, dueAt: null, before: new Date("2026-10-15T23:59:59Z"), after: new Date("2026-10-16T00:00:00Z") },
    { term: "30_days" as const, dueAt: null, before: new Date("2026-10-30T23:59:59Z"), after: new Date("2026-10-31T00:00:00Z") },
    { term: "fixed_date" as const, dueAt: new Date("2026-10-20T00:00:00Z"), before: new Date("2026-10-19T23:59:59Z"), after: new Date("2026-10-20T00:00:00Z") },
  ])("waits for a settled deposit at or after the $term repayment due time", async ({ term, dueAt, before, after }) => {
    const earlyDb = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0, feeAssessed: true, repaymentTerm: term, repaymentDueAt: dueAt });
    const earlyResult = await processSettledDeposit(earlyDb.pool, gateway(800_000), { ...deposit, settledAt: before });
    expect(earlyResult.created).toBe(false);
    expect(earlyDb.attempts).toBe(0);

    const dueDb = collectionPool({ availableCents: 800_000, principalCents: 600_000, feeCents: 0, feeAssessed: true, repaymentTerm: term, repaymentDueAt: dueAt });
    const dueResult = await processSettledDeposit(dueDb.pool, gateway(800_000), { ...deposit, depositId: `${deposit.depositId}-${term}`, settledAt: after });
    expect(dueResult.created).toBe(true);
    expect(dueResult.chargedCents).toBe(600_000);
  });
});

function gateway(availableCents: number): AirwallexGateway {
  return {
    listConnectedAccounts: async () => [],
    createTransfer: async () => { throw new Error("not used"); },
    createCharge: async () => { throw new Error("not used"); },
    getTransferByRequestId: async () => null,
    getChargeByRequestId: async () => null,
    getBalance: async () => ({ availableCents, currency: "USD" }),
    getDeposit: async () => ({ id: "dep_1", amountCents: deposit.amountCents, currency: "USD", status: "settled", settledAt: deposit.settledAt }),
    retrySandboxCard: async () => { throw new Error("not used"); },
  };
}

function collectionPool(input: { availableCents: number; principalCents: number; feeCents: number; feeAssessed?: boolean; pendingFeeCents?: number; pendingPrincipalCents?: number; repaymentTerm?: "15_days" | "30_days" | "fixed_date" | "asap"; repaymentDueAt?: Date | null; rosterActive?: boolean; customerId?: string; rosterMatches?: string[] }) {
  const attempts = new Set<string>();
  const statements: string[] = [];
  const operations: Array<{ operationType: string; amountCents: number }> = [];
  const chargePayloads: Record<string, unknown>[] = [];
  const ledgerEvents: Array<{ type: string; amountCents: number; occurredAt: unknown }> = [];
  const bridgeCustomerQueries: unknown[] = [];
  let attemptCount = 0;
  const respond = async <Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> => {
    statements.push(sql);
    if (sql.includes("airwallex_connected_account_id")) return rows((input.rosterMatches ?? [input.customerId ?? "acct_1"]).map((customer_id) => ({ customer_id })) as Row[]);
    if (sql.includes("customer_roster")) return rows([{ is_active: input.rosterActive ?? true }] as Row[]);
    if (sql.includes("FROM bridge_cases") && sql.includes("WHERE customer_id")) {
      bridgeCustomerQueries.push(values[0]);
      return rows([{ id: bridgeId, customer_id: input.customerId ?? "acct_1", amount_cents: input.principalCents, repayment_term: input.repaymentTerm ?? "asap", repayment_due_at: input.repaymentDueAt ?? null, status: "funded" }] as Row[]);
    }
    if (sql.includes("INSERT INTO collection_attempts")) {
      const key = `${values[1]}:${values[2]}`;
      if (attempts.has(key)) return rows([]);
      attempts.add(key);
      attemptCount++;
      return rows([{ id: values[0] }] as Row[]);
    }
    if (sql.includes("FROM bridge_projections")) {
      if (sql.includes("principal_outstanding_cents")) return rows([{ principal_outstanding_cents: input.principalCents, fee_outstanding_cents: input.feeCents }] as Row[]);
    }
    if (sql.includes("AS exists")) return rows([{ exists: input.feeCents > 0 || input.feeAssessed === true, funded_at: new Date("2026-10-01T00:00:00.000Z") }] as Row[]);
    if (sql.includes("SUM(amount_cents)") && sql.includes("provider_operations")) return rows([
      ...(input.pendingFeeCents ? [{ operation_type: "fee", pending_cents: input.pendingFeeCents }] : []),
      ...(input.pendingPrincipalCents ? [{ operation_type: "principal", pending_cents: input.pendingPrincipalCents }] : []),
    ] as Row[]);
    if (sql.includes("INSERT INTO ledger_events")) {
      ledgerEvents.push({ type: String(values[0]), amountCents: Number(values[2]), occurredAt: values[6] });
      return { rows: [{ id: "1" }] as Row[], rowCount: 1 };
    }
    if (sql.includes("INSERT INTO provider_operations")) {
      operations.push({ operationType: String(values[2]), amountCents: Number(values[4]) });
    }
    if (sql.includes("INSERT INTO outbox_commands")) chargePayloads.push(JSON.parse(String(values[1])) as Record<string, unknown>);
    if (sql.includes("SELECT facility_amount_cents")) return rows([{ facility_amount_cents: 5_000_000, reserve_floor_bps: 2_000 }] as Row[]);
    if (sql.includes("SELECT id, customer_id FROM bridge_cases")) return rows([{ id: bridgeId, customer_id: "acct_1" }] as Row[]);
    if (sql.includes("SELECT id, event_type, bridge_id")) {
      return rows([
        { id: 1, event_type: "transfer_settled", bridge_id: bridgeId, amount_cents: input.principalCents, currency: "USD", provenance: "provider", source_id: "funded", payload: {}, occurred_at: new Date("2026-10-01T00:00:00.000Z") },
        ...(input.feeCents ? [{ id: 2, event_type: "fee_assessed", bridge_id: bridgeId, amount_cents: input.feeCents, currency: "USD", provenance: "collection_trigger", source_id: "fee", payload: {}, occurred_at: new Date() }] : []),
      ] as Row[]);
    }
    if (sql.includes("SELECT id, deposit_id, account_id")) return rows([{ id: "attempt-1", deposit_id: deposit.depositId, account_id: deposit.accountId, deposit_amount_cents: deposit.amountCents, available_balance_cents: input.availableCents, fee_operation_id: "fee-operation" }] as Row[]);
    if (sql.includes("FROM bridge_projections")) return rows([{ principal_outstanding_cents: input.principalCents, fee_outstanding_cents: 0 }] as Row[]);
    return { rows: [], rowCount: 1 };
  };
  const client: DatabaseClient = { query: respond, release() {} };
  const pool: DatabasePool = {
    query: respond,
    connect: async () => client,
    end: async () => {},
  };
  return { pool, statements, operations, chargePayloads, ledgerEvents, bridgeCustomerQueries, get attempts() { return attemptCount; } };
}

function rows<Row>(items: Row[]): QueryResult<Row> { return { rows: items, rowCount: items.length }; }

function kit5DepositRow(customerId: string) {
  return {
    id: "deposit-operation-1", scenario_id: "kit5-target", customer_id: customerId,
    action_id: "nova-action", bridge_id: bridgeId, account_id: deposit.accountId,
    global_account_id: "verified-global-account", amount_cents: 800_000, currency: "USD",
    statement_ref: "kit5-saved-reference", status: "settled", provider_deposit_id: deposit.depositId,
    provider_result: null, error: null, settled_at: deposit.settledAt,
  };
}
