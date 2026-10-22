import type { DatabasePool } from "../../db/pool.js";

interface JsonResponse {
  writeHead(status: number, headers: Record<string, string>): unknown;
  end(body: string): unknown;
}

const BRIDGE_STATUSES = new Set(["reserved", "pending", "suspended", "funded", "collecting", "repaid", "failed"]);

interface FacilityRow {
  currency: string;
  facility_amount_cents: string | number;
  reserve_floor_cents: string | number;
  opening_exposure_cents: string | number;
  settled_principal_cents: string | number;
  active_reservations_cents: string | number;
  deployable_cents: string | number;
}

interface BridgeRow {
  bridge_id: string;
  customer_id: string;
  status: string;
  principal_outstanding_cents: string | number;
  fee_outstanding_cents: string | number;
}

function cents(value: string | number): number {
  const parsed = typeof value === "number" ? value : Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error("invalid ledger projection");
  return parsed;
}

/** Returns the persisted, read-only USD ledger snapshot consumed by browser clients. */
export async function handleLedgerProjection(
  response: JsonResponse,
  pool: DatabasePool,
): Promise<void> {
  try {
    const [facilityResult, bridgeResult] = await Promise.all([
      pool.query<FacilityRow>(
        `SELECT currency, facility_amount_cents, reserve_floor_cents,
                opening_exposure_cents, settled_principal_cents,
                active_reservations_cents, deployable_cents
           FROM facility_projections WHERE currency = 'USD'`,
      ),
      pool.query<BridgeRow>(
        `SELECT bridge_id, customer_id, status,
                principal_outstanding_cents, fee_outstanding_cents
           FROM bridge_projections ORDER BY bridge_id`,
      ),
    ]);
    const facility = facilityResult.rows[0];
    if (!facility) {
      send(response, 503, { error: "ledger_projection_unavailable" });
      return;
    }
    const bridges = bridgeResult.rows.map((row) => {
      if (!BRIDGE_STATUSES.has(row.status)) throw new Error("invalid ledger projection");
      return {
        bridgeId: row.bridge_id,
        customerId: row.customer_id,
        status: row.status,
        principalOutstandingCents: cents(row.principal_outstanding_cents),
        feeOutstandingCents: cents(row.fee_outstanding_cents),
      };
    });
    send(response, 200, {
      facility: {
        currency: facility.currency,
        facilityAmountCents: cents(facility.facility_amount_cents),
        reserveFloorCents: cents(facility.reserve_floor_cents),
        openingExposureCents: cents(facility.opening_exposure_cents),
        settledPrincipalCents: cents(facility.settled_principal_cents),
        activeReservationsCents: cents(facility.active_reservations_cents),
        deployableCents: cents(facility.deployable_cents),
      },
      bridges,
    });
  } catch {
    send(response, 500, { error: "ledger_projection_unavailable" });
  }
}

function send(response: JsonResponse, status: number, body: Record<string, unknown>): void {
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
