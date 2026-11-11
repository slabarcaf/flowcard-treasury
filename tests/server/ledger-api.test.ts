import { describe, expect, it, vi } from "vitest";
import type { DatabasePool } from "../../server/src/db/pool.js";
import { createRequestHandler } from "../../server/src/http/server.js";

const facility = {
  currency: "USD",
  facility_amount_cents: "5000000",
  reserve_floor_cents: "1000000",
  opening_exposure_cents: "2500000",
  settled_principal_cents: "500000",
  active_reservations_cents: "400000",
  deployable_cents: "600000",
};
const bridges = [{
  bridge_id: "bridge-1",
  customer_id: "customer-1",
  status: "collecting",
  principal_outstanding_cents: "500000",
  fee_outstanding_cents: 7500,
}];

function mockResponse() {
  return { writeHead: vi.fn(), end: vi.fn() };
}

describe("read-only ledger projection route", () => {
  it("returns the persisted facility and bridge status contract", async () => {
    const query = vi.fn()
      .mockResolvedValueOnce({ rows: [facility], rowCount: 1 })
      .mockResolvedValueOnce({ rows: bridges, rowCount: 1 });
    const pool = { query } as unknown as DatabasePool;
    const handler = createRequestHandler({
      port: 3000,
      databaseUrl: "postgres://localhost/test",
      airwallexClientId: "server-only-client",
      airwallexApiKey: "server-only-secret",
      airwallexWebhookSecret: "server-only-webhook-secret",
    }, pool);
    const response = mockResponse();
    handler({ method: "GET", url: "/api/ledger" } as never, response as never);
    await vi.waitFor(() => expect(response.end).toHaveBeenCalled());

    expect(response.writeHead).toHaveBeenCalledWith(200, {
      "content-type": "application/json",
      "cache-control": "no-store",
    });
    expect(JSON.parse(response.end.mock.calls[0][0])).toEqual({
      facility: {
        currency: "USD",
        facilityAmountCents: 5_000_000,
        reserveFloorCents: 1_000_000,
        openingExposureCents: 2_500_000,
        settledPrincipalCents: 500_000,
        activeReservationsCents: 400_000,
        deployableCents: 600_000,
      },
      bridges: [{
        bridgeId: "bridge-1",
        customerId: "customer-1",
        status: "collecting",
        principalOutstandingCents: 500_000,
        feeOutstandingCents: 7_500,
      }],
    });
    expect(query).toHaveBeenCalledTimes(2);
    expect(query.mock.calls.map(([sql]) => sql)).toEqual(expect.arrayContaining([
      expect.stringContaining("FROM facility_projections WHERE currency = 'USD'"),
      expect.stringContaining("FROM bridge_projections ORDER BY bridge_id"),
    ]));
  });

  it("returns unavailable when the facility projection is not initialized", async () => {
    const pool = {
      query: vi.fn().mockResolvedValueOnce({ rows: [], rowCount: 0 }).mockResolvedValueOnce({ rows: [], rowCount: 0 }),
    } as unknown as DatabasePool;
    const handler = createRequestHandler({
      port: 3000,
      databaseUrl: "postgres://localhost/test",
      airwallexClientId: "client",
      airwallexApiKey: "key",
      airwallexWebhookSecret: "secret",
    }, pool);
    const response = mockResponse();
    handler({ method: "GET", url: "/api/ledger" } as never, response as never);
    await vi.waitFor(() => expect(response.end).toHaveBeenCalled());
    expect(response.writeHead).toHaveBeenCalledWith(503, expect.any(Object));
    expect(response.end).toHaveBeenCalledWith(JSON.stringify({ error: "ledger_projection_unavailable" }));
  });

  it("does not expose a write method for browser funding", () => {
    const query = vi.fn();
    const handler = createRequestHandler({
      port: 3000,
      databaseUrl: "postgres://localhost/test",
      airwallexClientId: "client",
      airwallexApiKey: "key",
      airwallexWebhookSecret: "secret",
    }, { query } as unknown as DatabasePool);
    const response = mockResponse();
    handler({ method: "POST", url: "/api/ledger" } as never, response as never);
    expect(response.writeHead).toHaveBeenCalledWith(404, expect.any(Object));
    expect(query).not.toHaveBeenCalled();
  });
});
