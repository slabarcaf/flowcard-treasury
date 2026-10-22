const DAILY_RATE_BPS = 5;
const MINIMUM_DAYS = 7;
const DAY_MS = 24 * 60 * 60 * 1000;

/** Calculates a trigger-time fee, rounded up to the next cent. */
export function calculateBridgeFeeCents(input: {
  principalCents: number;
  fundedAt: Date;
  triggeredAt: Date;
}): number {
  const { principalCents, fundedAt, triggeredAt } = input;
  if (!Number.isSafeInteger(principalCents) || principalCents < 0) {
    throw new Error("principalCents must be a non-negative safe integer");
  }
  if (!Number.isFinite(fundedAt.getTime()) || !Number.isFinite(triggeredAt.getTime())) {
    throw new Error("Fee timestamps must be valid dates");
  }
  const elapsedDays = Math.max(0, (triggeredAt.getTime() - fundedAt.getTime()) / DAY_MS);
  const chargeableDays = Math.max(MINIMUM_DAYS, elapsedDays);
  return Math.ceil((principalCents * DAILY_RATE_BPS * chargeableDays) / 10_000);
}
