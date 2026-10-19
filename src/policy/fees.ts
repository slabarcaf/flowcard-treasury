/**
 * Calculate the bridge fee for a constant principal over a number of days.
 *
 * Formula: principal * ratePer30Days * days / 30
 * Rounds to the nearest cent.
 *
 * For the general (variable-principal) case used in monthly billing:
 *   fee = ratePer30Days * (1/30) * sum(P_d for each day d)
 * This function handles the simple constant-principal case used in the demo.
 */
export function bridgeFee(
  principal: number,
  days: number,
  ratePer30Days: number = 0.015,
): number {
  return Math.round(principal * ratePer30Days * (days / 30) * 100) / 100;
}

/**
 * General fee calculation for variable daily balances.
 * dailyBalances is an array of principal outstanding at end of each day.
 */
export function bridgeFeeVariable(
  dailyBalances: number[],
  ratePer30Days: number = 0.015,
): number {
  const sum = dailyBalances.reduce((acc, b) => acc + b, 0);
  return Math.round(ratePer30Days * (sum / 30) * 100) / 100;
}
