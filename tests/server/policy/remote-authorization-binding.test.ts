import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { bindIssuingCard } from "../../../server/src/roster/repository.js";
import { createRemoteAuthorizationResolver } from "../../../server/src/policy/remote-authorization.js";
import type { AirwallexRemoteAuthorizationV2 } from "../../../server/src/http/routes/remote-authorization.js";

function poolFor(clientQuery: ReturnType<typeof vi.fn>): DatabasePool {
  const client = { query: clientQuery, release: vi.fn() };
  return { connect: vi.fn(async () => client) } as unknown as DatabasePool;
}

describe("remote authorization card binding", () => {
  it("requires an active customer/account match and records the actor and reason", async () => {
    const query = vi.fn(async (sql: string): Promise<QueryResult> => {
      if (sql.includes("SELECT customer_id FROM customer_roster")) return { rows: [{ customer_id: "customer-1" }], rowCount: 1 };
      if (sql.includes("INSERT INTO issuing_card_bindings")) return { rows: [{ card_id: "card-1" }], rowCount: 1 };
      return { rows: [], rowCount: 1 };
    });
    await bindIssuingCard(poolFor(query), {
      customerId: "customer-1", accountId: "airwallex-1", cardId: "card-1", actor: "operator-1", reason: "verified card assignment",
    });
    expect(query.mock.calls[1]?.[0]).toContain("airwallex_connected_account_id = $2 AND is_active FOR UPDATE");
    expect(query.mock.calls[2]?.[1]).toEqual(["card-1", "customer-1", "airwallex-1", "operator-1", "verified card assignment"]);
    expect(query.mock.calls[3]?.[0]).toContain("issuing_card_binding_audit");
    expect(query.mock.calls[3]?.[1]).toEqual(["card-1", "customer-1", "airwallex-1", "operator-1", "verified card assignment"]);
    expect(query).toHaveBeenLastCalledWith("COMMIT");
  });

  it("rolls back an account/customer mismatch", async () => {
    const query = vi.fn(async (): Promise<QueryResult> => ({ rows: [], rowCount: 0 }));
    await expect(bindIssuingCard(poolFor(query), {
      customerId: "customer-1", accountId: "wrong-account", cardId: "card-1", actor: "operator-1", reason: "verified",
    })).rejects.toThrow("does not match an active roster account");
    expect(query).toHaveBeenLastCalledWith("ROLLBACK");
  });

  it("does not complete an unbound event by ID after the second prior-event check", async () => {
    let priorLookups = 0;
    let concurrentAuthorizationCommitted = false;
    const query = vi.fn(async (sql: string): Promise<QueryResult> => {
      if (sql.includes("FROM remote_authorization_requests WHERE card_transaction_event_id")) {
        priorLookups += 1;
        if (priorLookups === 2) concurrentAuthorizationCommitted = true;
        return { rows: [], rowCount: 0 };
      }
      if (sql.includes("FROM customer_roster AS roster")) return { rows: [], rowCount: 0 };
      if (sql.includes("UPDATE remote_authorization_requests")) {
        return {
          rows: [{ response_status: "AUTHORIZED", status_reason: "policy_approved" }],
          rowCount: concurrentAuthorizationCommitted ? 1 : 0,
        };
      }
      if (sql.includes("SELECT response_status, status_reason FROM remote_authorization_requests")) {
        return { rows: [{ response_status: "AUTHORIZED", status_reason: "policy_approved" }], rowCount: 1 };
      }
      throw new Error(`Unexpected query: ${sql}`);
    });
    const pool = { query } as unknown as DatabasePool;
    const request: AirwallexRemoteAuthorizationV2 = {
      version: 2, accountId: "unbound-account", cardId: "unbound-card",
      cardTransactionEventId: "evt-racing-unbound", cardTransactionId: "txn-1",
      cardTransactionLifecycleId: "lifecycle-1", transactionType: "AUTHORIZATION",
      transactionCategory: "PURCHASE", transactionAmount: 30, transactionCurrency: "USD",
      merchantCategoryCode: "5411",
    };
    const resolver = createRemoteAuthorizationResolver({
      pool,
      gateway: {} as never,
      repaymentTerm: "asap",
      now: () => new Date("2026-10-22T12:00:00.000Z"),
    });

    await expect(resolver(request, "fresh-nonce", new AbortController().signal)).resolves.toEqual({
      responseStatus: "DECLINED", statusReason: "card_not_enrolled",
    });
    expect(priorLookups).toBe(2);
    expect(query).toHaveBeenCalledTimes(3);
    expect(query.mock.calls.some(([sql]) => sql.includes("UPDATE remote_authorization_requests"))).toBe(false);
  });
});
