import { describe, expect, it, vi } from "vitest";
import type { LedgerEventRecord } from "../../server/src/ledger/events.js";
import { appendLedgerEvent } from "../../server/src/ledger/events.js";
import { projectLedger } from "../../server/src/ledger/projector.js";

function event(
  id: number,
  eventType: LedgerEventRecord["event_type"],
  amountCents: number,
  bridgeId: string | null = null,
): LedgerEventRecord {
  return {
    id,
    event_type: eventType,
    bridge_id: bridgeId,
    amount_cents: amountCents,
    currency: "USD",
    provenance: "test",
    source_id: `source-${id}`,
    payload: {},
    occurred_at: new Date(id),
  };
}

describe("append-only bridge ledger", () => {
  it("inserts a source event once and leaves duplicates untouched", async () => {
    const query = vi.fn().mockResolvedValue({ rows: [{ id: "1" }], rowCount: 1 });
    const appended = await appendLedgerEvent(
      { query },
      {
        eventType: "opening_exposure",
        amountCents: 2_500_000,
        provenance: "demo_seed",
        sourceId: "demo-seed-opening-exposure-v1",
      },
    );

    expect(appended).toBe(true);
    expect(query.mock.calls[0][0]).toContain("ON CONFLICT (source_id) DO NOTHING");
    expect(query.mock.calls[0][0]).toContain("INSERT INTO ledger_events");
  });

  it("projects the $50K facility, 20% floor, $25K opening exposure, and $15K capacity", () => {
    const projection = projectLedger(
      [event(1, "opening_exposure", 2_500_000)],
      { facilityAmountCents: 5_000_000, reserveFloorBps: 2_000 },
    );

    expect(projection.facility).toMatchObject({
      facilityAmountCents: 5_000_000,
      reserveFloorCents: 1_000_000,
      openingExposureCents: 2_500_000,
      settledPrincipalCents: 0,
      activeReservationsCents: 0,
      deployableCents: 1_500_000,
    });
  });

  it("holds a failed transfer reservation until both failure and return are recorded", () => {
    const bridgeId = "b1";
    const seeds = [{ bridgeId, customerId: "customer-1", amountCents: 500_000 }];
    const reserved = event(1, "bridge_reserved", 500_000, bridgeId);
    const failed = event(2, "transfer_failed", 500_000, bridgeId);
    const returned = event(3, "transfer_return_confirmed", 500_000, bridgeId);

    const held = projectLedger([reserved, failed], {
      facilityAmountCents: 5_000_000,
      reserveFloorBps: 2_000,
    }, seeds);
    expect(held.facility.activeReservationsCents).toBe(500_000);
    expect(held.bridges.get(bridgeId)?.status).toBe("suspended");

    const released = projectLedger([reserved, failed, returned], {
      facilityAmountCents: 5_000_000,
      reserveFloorBps: 2_000,
    }, seeds);
    expect(released.facility.activeReservationsCents).toBe(0);
    expect(released.bridges.get(bridgeId)?.status).toBe("failed");
  });

  it("converts a reservation to principal only when the transfer settles", () => {
    const bridgeId = "b2";
    const events = [
      event(1, "bridge_reserved", 400_000, bridgeId),
      event(2, "transfer_pending", 400_000, bridgeId),
    ];
    const pending = projectLedger(events, {
      facilityAmountCents: 5_000_000,
      reserveFloorBps: 2_000,
    }, [{ bridgeId, customerId: "customer-2", amountCents: 400_000 }]);
    expect(pending.facility.activeReservationsCents).toBe(400_000);
    expect(pending.facility.settledPrincipalCents).toBe(0);

    const settled = projectLedger([...events, event(3, "transfer_settled", 400_000, bridgeId)], {
      facilityAmountCents: 5_000_000,
      reserveFloorBps: 2_000,
    }, [{ bridgeId, customerId: "customer-2", amountCents: 400_000 }]);
    expect(settled.facility.activeReservationsCents).toBe(0);
    expect(settled.facility.settledPrincipalCents).toBe(400_000);
    expect(settled.bridges.get(bridgeId)?.principalOutstandingCents).toBe(400_000);
  });
});
