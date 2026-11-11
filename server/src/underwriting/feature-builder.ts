import { PROFILE_WINDOW_DAYS, UNDERWRITING_FEATURE_VERSION, UNDERWRITING_PROFILE_SCHEMA_VERSION, type UnderwritingProfileCandidate } from "./types.js";
import type { AccountStanding, BalancePoint, LedgerTransaction } from "./airwallex-source.js";

export interface RepaymentRecord { dueAt: Date | null; repaidAt: Date | null; status: "open" | "repaid" | "late" | "failed"; }

export function buildProfileCandidate(input: {
  customerId: string;
  runId: string;
  capturedAt: Date;
  startedAt: Date;
  windowStart: Date;
  windowEnd: Date;
  accountId: string;
  transactions: readonly LedgerTransaction[];
  balances: readonly BalancePoint[];
  standing: AccountStanding;
  repayments: readonly RepaymentRecord[];
  sourceVersions: UnderwritingProfileCandidate["sourceVersions"];
  queryPages: UnderwritingProfileCandidate["queryCompletion"] extends infer C ? C : never;
}): UnderwritingProfileCandidate {
  const start = input.windowStart.getTime();
  const end = input.windowEnd.getTime();
  const dedupedTransactions = dedupeById(input.transactions.filter((row) => row.accountId === input.accountId && row.occurredAt.getTime() >= start && row.occurredAt.getTime() < end));
  const dedupedBalances = dedupeBalances(input.balances.filter((row) => row.accountId === input.accountId && row.currency === "USD" && row.postedAt.getTime() >= start && row.postedAt.getTime() < end));
  const settledCredits = dedupedTransactions.filter((row) => row.kind === "credit" && row.status === "settled" && row.currency === "USD");
  const averageMonthlySettledInflowsCents = Math.round(settledCredits.reduce((sum, row) => sum + row.amountCents, 0) / 3);
  const knownPayers = settledCredits.filter((row) => row.payerKey);
  const payerConcentration = settledCredits.length === 0 ? null : knownPayers.length !== settledCredits.length ? null : maxShare(knownPayers);
  const capturedSpend = dedupedTransactions.filter((row) => row.kind === "debit" && row.currency === "USD" && row.captured);
  const spendKindsKnown = capturedSpend.every((row) => /card|issuing/.test(row.sourceType));
  const monthlySpendCents = capturedSpend.reduce((sum, row) => sum + row.amountCents, 0) / 3;
  const dailyMins = new Map<string, number>();
  for (const point of dedupedBalances) {
    const day = point.postedAt.toISOString().slice(0, 10);
    dailyMins.set(day, Math.min(dailyMins.get(day) ?? Number.POSITIVE_INFINITY, point.balanceCents));
  }
  const lowBalanceDays = dailyMins.size === 0 ? null : [...dailyMins.values()].filter((balance) => balance < monthlySpendCents * 0.1).length;
  const priorRepayments = input.repayments.filter((item) => item.status !== "open");
  const repaymentHistory = priorRepayments.length === 0 ? "never_borrowed" as const : priorRepayments.some((item) => item.status === "late" || item.status === "failed") ? "late" as const : "on_time" as const;
  const accountStatus = input.standing.status;
  const eligibleSpend = capturedSpend.length === 0 ? "unknown" as const : spendKindsKnown ? "supported" as const : "unsupported" as const;

  return {
    customerId: input.customerId,
    runId: input.runId,
    sourceMode: "production",
    capturedAt: input.capturedAt,
    startedAt: input.startedAt,
    sourceVersions: input.sourceVersions,
    queryCompletion: input.queryPages,
    schemaVersion: UNDERWRITING_PROFILE_SCHEMA_VERSION,
    featureVersion: UNDERWRITING_FEATURE_VERSION,
    windowStart: input.windowStart,
    windowEnd: input.windowEnd,
    data: {
      averageMonthlySettledInflowsCents,
      // Settlement timestamps do not establish expected deposit dates.
      expectedDateReliability: null,
      topPayerConcentration: payerConcentration,
      lowBalanceDays,
      historyDays: PROFILE_WINDOW_DAYS,
      eligibleSpend,
      connectedAccountStatus: accountStatus,
      openRfi: input.standing.openRfi,
      repaymentHistory,
    },
  };
}

function dedupeById(rows: readonly LedgerTransaction[]): LedgerTransaction[] {
  const byId = new Map<string, LedgerTransaction>();
  for (const row of rows) {
    const prior = byId.get(row.id);
    if (prior && (prior.amountCents !== row.amountCents || prior.occurredAt.getTime() !== row.occurredAt.getTime() || prior.accountId !== row.accountId)) throw new Error("Airwallex report has conflicting duplicate transaction IDs");
    byId.set(row.id, row);
  }
  return [...byId.values()];
}
function dedupeBalances(rows: readonly BalancePoint[]): BalancePoint[] {
  const byPoint = new Map<string, BalancePoint>();
  for (const row of rows) byPoint.set(`${row.postedAt.toISOString()}|${row.currency}`, row);
  return [...byPoint.values()];
}
function maxShare(rows: readonly LedgerTransaction[]): number | null {
  const total = rows.reduce((sum, row) => sum + row.amountCents, 0);
  if (total <= 0) return null;
  const byPayer = new Map<string, number>();
  for (const row of rows) byPayer.set(row.payerKey!, (byPayer.get(row.payerKey!) ?? 0) + row.amountCents);
  return Math.max(...byPayer.values()) / total;
}
