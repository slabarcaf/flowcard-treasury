import type { AirwallexUnderwritingGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import type { CustomerRosterEntry } from "../roster/types.js";
import { collectUnderwritingSources } from "./report-worker.js";
import { buildProfileCandidate, type RepaymentRecord } from "./feature-builder.js";
import type { UnderwritingProfileCandidate } from "./types.js";

interface ActiveRosterRow { customer_id: string; airwallex_connected_account_id: string; }
interface RepaymentRow { customer_id: string; due_at: Date | null; repaid_at: Date | null; status: string; }
export interface DailyProfileResult { customerId: string; candidate: UnderwritingProfileCandidate; publishable: false; blockedReason: "expected_inflow_dates_unavailable"; }

/** Builds the daily source snapshot. It deliberately does not publish incomplete reliability profiles. */
export async function runDailyUnderwritingProfileBuild(input: {
  pool: DatabasePool;
  gateway: AirwallexUnderwritingGateway;
  now?: Date;
  runId: string;
}): Promise<DailyProfileResult[]> {
  const startedAt = input.now ?? new Date();
  const capturedAt = input.now ?? new Date();
  const windowEnd = new Date(Date.UTC(capturedAt.getUTCFullYear(), capturedAt.getUTCMonth(), capturedAt.getUTCDate()));
  const windowStart = new Date(windowEnd.getTime() - 90 * 86_400_000);
  const rosterRows = await input.pool.query<ActiveRosterRow>(
    `SELECT customer_id, airwallex_connected_account_id
       FROM customer_roster WHERE is_active ORDER BY customer_id`,
  );
  const roster: CustomerRosterEntry[] = rosterRows.rows.map((row) => ({
    customerId: row.customer_id,
    salesforceAccountId: "",
    airwallexConnectedAccountId: row.airwallex_connected_account_id,
    isActive: true,
    enrolledAt: startedAt,
    enrolledBy: "system",
    enrollmentReason: "profile-build",
    deactivatedAt: null,
    deactivatedBy: null,
    deactivationReason: null,
    updatedAt: startedAt,
  }));
  const sources = await collectUnderwritingSources(input.gateway, roster, windowStart, windowEnd);
  const customerIds = roster.map((row) => row.customerId);
  const repaymentRows = customerIds.length ? (await input.pool.query<RepaymentRow>(
    `SELECT bridge.customer_id,
            bridge.repayment_due_at AS due_at,
            MAX(event.occurred_at) FILTER (WHERE event.event_type = 'principal_repaid') AS repaid_at,
            CASE WHEN bridge.status = 'failed' THEN 'failed'
                 WHEN bridge.status = 'repaid' THEN 'repaid' ELSE 'open' END AS status
       FROM bridge_cases bridge
       LEFT JOIN ledger_events event ON event.bridge_id = bridge.id
      WHERE bridge.customer_id = ANY($1::text[])
      GROUP BY bridge.id, bridge.customer_id, bridge.repayment_due_at, bridge.status
      ORDER BY bridge.customer_id, bridge.created_at`, [customerIds],
  )).rows : [];
  const repayments = groupRepayments(repaymentRows);
  return roster.map((customer) => {
    const sourceCompletion = (pages: number) => ({ complete: pages > 0, pages });
    const candidate = buildProfileCandidate({
      customerId: customer.customerId,
      runId: input.runId,
      capturedAt,
      startedAt,
      windowStart,
      windowEnd,
      accountId: customer.airwallexConnectedAccountId,
      transactions: sources.transactions,
      balances: sources.balances,
      standing: sources.standings.get(customer.customerId) ?? { status: "unknown", openRfi: null },
      repayments: repayments.get(customer.customerId) ?? [],
      sourceVersions: {
        account: sources.sourceVersions.account,
        balances: sources.sourceVersions.balances,
        transactions: sources.sourceVersions.transactions,
        expected_inflows: sources.sourceVersions.expected_inflows,
        repayments: sources.sourceVersions.repayments,
      },
      queryPages: {
        account: sourceCompletion(sources.queryPages.account),
        balances: sourceCompletion(sources.queryPages.balances),
        transactions: sourceCompletion(sources.queryPages.transactions),
        expected_inflows: { complete: false, pages: 0 },
        repayments: sourceCompletion(1),
      },
    });
    return { customerId: customer.customerId, candidate, publishable: false, blockedReason: "expected_inflow_dates_unavailable" };
  });
}

function groupRepayments(rows: readonly RepaymentRow[]): Map<string, RepaymentRecord[]> {
  const output = new Map<string, RepaymentRecord[]>();
  for (const row of rows) {
    const status = row.status === "failed" ? "failed" : row.status === "repaid" && row.due_at && row.repaid_at && row.repaid_at > row.due_at ? "late" : row.status === "repaid" ? "repaid" : "open";
    const records = output.get(row.customer_id) ?? [];
    records.push({ dueAt: row.due_at, repaidAt: row.repaid_at, status });
    output.set(row.customer_id, records);
  }
  return output;
}
