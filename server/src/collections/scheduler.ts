import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { processSettledDeposit, type CollectionAttemptResult, type SettledDepositInput } from "./service.js";

/** Schedules the settled-deposit collection path; timed-term due-date policy is handled separately. */
export function scheduleDepositCollection(
  pool: DatabasePool,
  gateway: AirwallexGateway,
  deposit: SettledDepositInput,
): Promise<CollectionAttemptResult> {
  return processSettledDeposit(pool, gateway, deposit);
}
