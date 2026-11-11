/**
 * Allocating scarce capital (spec §6).
 *
 * "Ranking when capital is scarce: tier, then spend criticality, then receivable
 * quality (payer on-time rate, relationship length)."
 */
import type { BridgeRequest, Customer, Decision, PlatformCapital } from '../types.js';
import { CRITICALITY_RANK, TIER_RANK } from './policy.config.js';
import { computeTier } from './tiers.js';
import { evaluate } from './eligibility.js';
import { round2 } from './util.js';

export interface RankedRequest {
  /** 1-based position after sorting. */
  rank: number;
  request: BridgeRequest;
  decision: Decision;
  /** Principal actually set aside for this request. 0 when it was not funded. */
  allocated: number;
  funded: boolean;
  /** Why it was not funded, empty when it was. */
  reason: string;
}

/**
 * Receivable quality for a customer: the best payer on-time rate, with the
 * longest relationship breaking ties. 0 when there are no expected receivables.
 */
export function receivableQuality(customer: Customer): { onTimeRate: number; months: number } {
  return customer.expected_receivables.reduce(
    (best, r) => {
      if (
        r.payer_on_time_rate > best.onTimeRate ||
        (r.payer_on_time_rate === best.onTimeRate && r.relationship_months > best.months)
      ) {
        return { onTimeRate: r.payer_on_time_rate, months: r.relationship_months };
      }
      return best;
    },
    { onTimeRate: 0, months: 0 },
  );
}

/**
 * Rank requests and allocate capital to them in order.
 *
 * Each request is evaluated against the capital left after the ones above it,
 * so the reserve floor holds for the batch as a whole and a request that only
 * fits before its betters are funded is correctly refused.
 */
export function rankRequests(
  requests: BridgeRequest[],
  customers: Customer[],
  capital: PlatformCapital,
): RankedRequest[] {
  const byId = new Map(customers.map((c) => [c.id, c]));

  const sorted = [...requests].sort((a, b) => {
    const ca = byId.get(a.customer_id);
    const cb = byId.get(b.customer_id);
    if (!ca || !cb) return 0;

    const tierDiff = TIER_RANK[computeTier(cb)] - TIER_RANK[computeTier(ca)];
    if (tierDiff !== 0) return tierDiff;

    const critDiff =
      CRITICALITY_RANK[b.transaction.criticality] - CRITICALITY_RANK[a.transaction.criticality];
    if (critDiff !== 0) return critDiff;

    const qa = receivableQuality(ca);
    const qb = receivableQuality(cb);
    if (qb.onTimeRate !== qa.onTimeRate) return qb.onTimeRate - qa.onTimeRate;
    return qb.months - qa.months;
  });

  // Capital and per-customer exposure both move as we fund down the list.
  let outstanding = capital.outstanding;
  const extraByCustomer = new Map<string, number>();
  const priorDecisions: Decision[] = [];
  const out: RankedRequest[] = [];

  sorted.forEach((request, i) => {
    const customer = byId.get(request.customer_id);
    if (!customer) {
      throw new Error(`rankRequests: no customer ${request.customer_id} for request ${request.id}`);
    }

    const decision = evaluate(
      request,
      customer,
      { ...capital, outstanding },
      {
        priorDecisions,
        extraOutstanding: extraByCustomer.get(customer.id) ?? 0,
      },
    );
    priorDecisions.push(decision);

    const funded = decision.eligible && !decision.needs_human_approval;
    if (funded) {
      outstanding = round2(outstanding + request.principal);
      extraByCustomer.set(
        customer.id,
        round2((extraByCustomer.get(customer.id) ?? 0) + request.principal),
      );
    }

    out.push({
      rank: i + 1,
      request,
      decision,
      allocated: funded ? round2(request.principal) : 0,
      funded,
      reason: funded
        ? ''
        : decision.reasons.length > 0
          ? decision.reasons[0]
          : 'Needs human approval before capital moves',
    });
  });

  return out;
}
