import type { DatabaseClient } from "../db/pool.js";
import type { LedgerEventRecord } from "./events.js";

export interface BridgeProjection {
  bridgeId: string;
  customerId: string;
  status: "reserved" | "pending" | "suspended" | "funded" | "collecting" | "repaid" | "failed";
  principalOutstandingCents: number;
  feeOutstandingCents: number;
  reservationCents: number;
}

export interface FacilityProjection {
  currency: "USD";
  facilityAmountCents: number;
  reserveFloorCents: number;
  openingExposureCents: number;
  settledPrincipalCents: number;
  activeReservationsCents: number;
  deployableCents: number;
}

export interface LedgerProjection {
  facility: FacilityProjection;
  bridges: Map<string, BridgeProjection>;
}

interface BridgeSeed {
  bridgeId: string;
  customerId: string;
}

function cents(value: number | string): number {
  const parsed = typeof value === "number" ? value : Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    throw new Error("Ledger projection encountered an invalid cent amount");
  }
  return parsed;
}

/** Rebuilds the ledger view from ordered events and known bridge metadata. */
export function projectLedger(
  events: readonly LedgerEventRecord[],
  config: { facilityAmountCents: number; reserveFloorBps: number },
  bridgeSeeds: readonly BridgeSeed[] = [],
): LedgerProjection {
  if (!Number.isSafeInteger(config.facilityAmountCents) || config.facilityAmountCents <= 0) {
    throw new Error("Facility amount must be a positive safe integer");
  }
  if (!Number.isInteger(config.reserveFloorBps) || config.reserveFloorBps < 0 || config.reserveFloorBps > 10000) {
    throw new Error("Reserve floor must be between 0 and 10000 basis points");
  }

  const bridges = new Map<string, BridgeProjection>();
  for (const seed of bridgeSeeds) {
    bridges.set(seed.bridgeId, {
      bridgeId: seed.bridgeId,
      customerId: seed.customerId,
      status: "reserved",
      principalOutstandingCents: 0,
      feeOutstandingCents: 0,
      reservationCents: 0,
    });
  }

  let openingExposureCents = 0;
  let settledPrincipalCents = 0;
  let activeReservationsCents = 0;
  const failedTransfers = new Set<string>();
  const returnedTransfers = new Set<string>();
  const orderedEvents = [...events].sort((left, right) => {
    const leftId = Number(left.id);
    const rightId = Number(right.id);
    return leftId - rightId;
  });

  for (const event of orderedEvents) {
    const amount = cents(event.amount_cents);
    if (event.event_type === "opening_exposure") {
      openingExposureCents += amount;
      continue;
    }
    if (!event.bridge_id) continue;
    const bridge = bridges.get(event.bridge_id);
    if (!bridge) continue;

    switch (event.event_type) {
      case "bridge_reserved":
        if (bridge.reservationCents === 0 && bridge.principalOutstandingCents === 0) {
          bridge.reservationCents = amount;
          activeReservationsCents += amount;
        }
        bridge.status = "reserved";
        break;
      case "transfer_pending":
        bridge.status = "pending";
        break;
      case "transfer_suspended":
        bridge.status = "suspended";
        break;
      case "transfer_settled":
        if (bridge.reservationCents > 0) {
          activeReservationsCents -= bridge.reservationCents;
          bridge.reservationCents = 0;
          bridge.principalOutstandingCents = amount;
          settledPrincipalCents += amount;
        }
        bridge.status = "funded";
        break;
      case "transfer_failed":
        failedTransfers.add(event.bridge_id);
        // Keep the bridge open and its capital reserved until the return is
        // separately confirmed.
        bridge.status = returnedTransfers.has(event.bridge_id) ? "failed" : "suspended";
        if (returnedTransfers.has(event.bridge_id) && bridge.reservationCents > 0) {
          activeReservationsCents -= bridge.reservationCents;
          bridge.reservationCents = 0;
        }
        break;
      case "transfer_return_confirmed":
        returnedTransfers.add(event.bridge_id);
        if (failedTransfers.has(event.bridge_id) && bridge.reservationCents > 0) {
          activeReservationsCents -= bridge.reservationCents;
          bridge.reservationCents = 0;
          bridge.status = "failed";
        }
        break;
      case "fee_assessed":
        bridge.feeOutstandingCents += amount;
        bridge.status = "collecting";
        break;
      case "fee_repaid":
        bridge.feeOutstandingCents = Math.max(0, bridge.feeOutstandingCents - amount);
        break;
      case "principal_repaid":
        if (bridge.principalOutstandingCents > 0) {
          const repaid = Math.min(bridge.principalOutstandingCents, amount);
          bridge.principalOutstandingCents -= repaid;
          settledPrincipalCents -= repaid;
        }
        break;
    }
  }

  const reserveFloorCents = Math.floor(
    (config.facilityAmountCents * config.reserveFloorBps) / 10000,
  );
  for (const bridge of bridges.values()) {
    if (bridge.status === "collecting" || bridge.status === "repaid") {
      bridge.status = bridge.principalOutstandingCents === 0 && bridge.feeOutstandingCents === 0
        ? "repaid"
        : "collecting";
    }
  }
  const deployableCents = Math.max(
    config.facilityAmountCents -
      reserveFloorCents -
      openingExposureCents -
      settledPrincipalCents -
      activeReservationsCents,
    0,
  );
  return {
    facility: {
      currency: "USD",
      facilityAmountCents: config.facilityAmountCents,
      reserveFloorCents,
      openingExposureCents,
      settledPrincipalCents,
      activeReservationsCents,
      deployableCents,
    },
    bridges,
  };
}

interface FacilityConfigRow {
  facility_amount_cents: string | number;
  reserve_floor_bps: number;
}

interface BridgeRow {
  id: string;
  customer_id: string;
}

/** Recomputes and writes the projections from the append-only event stream. */
export async function rebuildLedgerProjections(
  client: Pick<DatabaseClient, "query">,
): Promise<LedgerProjection> {
  const configResult = await client.query<FacilityConfigRow>(
    `SELECT facility_amount_cents, reserve_floor_bps
       FROM facility_configs
      WHERE is_active AND currency = 'USD'`,
  );
  const activeConfig = configResult.rows[0];
  if (!activeConfig) throw new Error("No active USD facility configuration exists");

  const bridgeResult = await client.query<BridgeRow>(
    `SELECT id, customer_id FROM bridge_cases ORDER BY created_at, id`,
  );
  const eventResult = await client.query<LedgerEventRecord>(
    `SELECT id, event_type, bridge_id, amount_cents, currency, provenance,
            source_id, payload, occurred_at
       FROM ledger_events
      WHERE currency = 'USD'
      ORDER BY id`,
  );
  const bridgeSeeds = bridgeResult.rows.map((row) => ({
    bridgeId: row.id,
    customerId: row.customer_id,
  }));
  const projection = projectLedger(eventResult.rows, {
    facilityAmountCents: cents(activeConfig.facility_amount_cents),
    reserveFloorBps: activeConfig.reserve_floor_bps,
  }, bridgeSeeds);

  await client.query(
    `INSERT INTO facility_projections (
       currency, facility_amount_cents, reserve_floor_cents,
       opening_exposure_cents, settled_principal_cents,
       active_reservations_cents, deployable_cents, updated_at
     ) VALUES ('USD', $1, $2, $3, $4, $5, $6, now())
     ON CONFLICT (currency) DO UPDATE SET
       facility_amount_cents = EXCLUDED.facility_amount_cents,
       reserve_floor_cents = EXCLUDED.reserve_floor_cents,
       opening_exposure_cents = EXCLUDED.opening_exposure_cents,
       settled_principal_cents = EXCLUDED.settled_principal_cents,
       active_reservations_cents = EXCLUDED.active_reservations_cents,
       deployable_cents = EXCLUDED.deployable_cents,
       updated_at = now()`,
    [
      projection.facility.facilityAmountCents,
      projection.facility.reserveFloorCents,
      projection.facility.openingExposureCents,
      projection.facility.settledPrincipalCents,
      projection.facility.activeReservationsCents,
      projection.facility.deployableCents,
    ],
  );

  for (const bridge of projection.bridges.values()) {
    await client.query(
      `UPDATE bridge_cases
          SET status = $2, updated_at = now()
        WHERE id = $1`,
      [bridge.bridgeId, bridge.status],
    );
    await client.query(
      `INSERT INTO bridge_projections (
         bridge_id, customer_id, status, principal_outstanding_cents, fee_outstanding_cents, updated_at
       ) VALUES ($1, $2, $3, $4, $5, now())
       ON CONFLICT (bridge_id) DO UPDATE SET
         customer_id = EXCLUDED.customer_id,
         status = EXCLUDED.status,
         principal_outstanding_cents = EXCLUDED.principal_outstanding_cents,
         fee_outstanding_cents = EXCLUDED.fee_outstanding_cents,
         updated_at = now()`,
      [bridge.bridgeId, bridge.customerId, bridge.status, bridge.principalOutstandingCents, bridge.feeOutstandingCents],
    );
  }
  return projection;
}
