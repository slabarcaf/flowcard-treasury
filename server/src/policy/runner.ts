import type { FacilityConfig } from "../../../shared/policy/types.js";
import type { DatabasePool } from "../db/pool.js";
import type { ExtractionResult, ProposedSignalType } from "../ai-evidence/types.js";
import type { TypeQualification } from "../ai-evidence/qualification.js";
import { listActiveCustomersForSnapshots } from "../roster/repository.js";
import { getCustomerSnapshotReadModel } from "../snapshots/freshness.js";
import { getCurrentUnderwritingProfile } from "../underwriting/repository.js";
import type { NoGoMappingReadiness } from "./gates.js";
import { joinPolicyInputs } from "./input.js";
import { computeDailyPrecheck } from "./precheck.js";
import { persistDailyPrecheck } from "./repository.js";

export type DailyPrecheckResult =
  | { customerId: string; status: "evaluated"; evaluationId: string; outcome: "ready" | "ineligible" | "blocked" }
  | { customerId: string; status: "waiting"; reason: "profile_missing" | "snapshot_missing" | "evidence_missing" }
  | { customerId: string; status: "failed"; reason: string };

/** Evaluate complete source tuples independently; absent upstream results wait for a later daily pass. */
export async function runDailyPolicyPrechecks(input: {
  pool: DatabasePool;
  facility: FacilityConfig;
  mappings: NoGoMappingReadiness;
  qualifications: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>;
  now?: Date;
}): Promise<DailyPrecheckResult[]> {
  const now = input.now ?? new Date();
  const customers = await listActiveCustomersForSnapshots(input.pool);
  const results: DailyPrecheckResult[] = [];
  for (const roster of customers) {
    try {
      const profileRead = await getCurrentUnderwritingProfile(input.pool, roster.customerId, now);
      if (!profileRead.profile) {
        results.push({ customerId: roster.customerId, status: "waiting", reason: "profile_missing" });
        continue;
      }
      const snapshotRead = await getCustomerSnapshotReadModel(input.pool, roster.customerId, now);
      if (!snapshotRead.snapshot) {
        results.push({ customerId: roster.customerId, status: "waiting", reason: "snapshot_missing" });
        continue;
      }
      const extraction = await input.pool.query<{ extraction_id: string; configuration_version: string; output: ExtractionResult }>(
        `SELECT extraction_id, configuration_version, output FROM ai_evidence_extractions
          WHERE customer_id = $1 AND snapshot_id = $2
          ORDER BY created_at DESC, extraction_id DESC LIMIT 1`,
        [roster.customerId, snapshotRead.snapshot.snapshotId],
      );
      const evidenceRow = extraction.rows[0];
      if (!evidenceRow) {
        results.push({ customerId: roster.customerId, status: "waiting", reason: "evidence_missing" });
        continue;
      }
      const evidence = evidenceRow.output;
      const policyInput = joinPolicyInputs({
        roster, profile: profileRead, snapshot: snapshotRead, evidence,
        qualifications: input.qualifications, noGoMappings: input.mappings, now,
      });
      const repayment = await input.pool.query<{ fully_repaid_on_time: string | number; ledger_sequence: string | number }>(
        `SELECT count(*)::int AS fully_repaid_on_time,
                (SELECT COALESCE(MAX(id), 0) FROM ledger_events WHERE currency = 'USD') AS ledger_sequence
           FROM (
             SELECT bridge.id, bridge.repayment_due_at,
                    MAX(event.occurred_at) FILTER (WHERE event.event_type = 'principal_repaid') AS repaid_at
               FROM bridge_cases bridge
               LEFT JOIN ledger_events event ON event.bridge_id = bridge.id
              WHERE bridge.customer_id = $1 AND bridge.status = 'repaid'
              GROUP BY bridge.id, bridge.repayment_due_at
           ) AS repayments
          WHERE repayment_due_at IS NOT NULL AND repaid_at IS NOT NULL AND repaid_at <= repayment_due_at`,
        [roster.customerId],
      );
      const decision = computeDailyPrecheck({
        policyInput, profile: profileRead.profile.data, evidenceRunId: evidenceRow.extraction_id,
        rosterUpdatedAt: roster.updatedAt, facility: input.facility, mappings: input.mappings,
          fullyRepaidOnTimeBridges: Number(repayment.rows[0]?.fully_repaid_on_time ?? 0), evaluatedAt: now,
        inputVersions: {
          rosterUpdatedAt: roster.updatedAt.toISOString(),
          profileId: profileRead.profile.profileId,
          profileVersion: profileRead.profile.version,
          profileFeatureVersion: profileRead.profile.featureVersion,
          profileAccountSourceVersion: profileRead.profile.sourceVersions.account,
          profileBalancesSourceVersion: profileRead.profile.sourceVersions.balances,
          profileTransactionsSourceVersion: profileRead.profile.sourceVersions.transactions,
          profileExpectedInflowsSourceVersion: profileRead.profile.sourceVersions.expected_inflows,
          profileRepaymentsSourceVersion: profileRead.profile.sourceVersions.repayments,
          salesforceSnapshotId: snapshotRead.snapshot.snapshotId,
          salesforceSnapshotVersion: snapshotRead.snapshot.version,
          salesforceMappingVersion: snapshotRead.snapshot.mappingVersion,
          evidenceRunId: evidenceRow.extraction_id,
          evidenceConfigurationVersion: evidenceRow.configuration_version,
          evidenceCatalogVersion: evidence.model.catalogVersion,
          evidenceSchemaVersion: evidence.schemaVersion,
          ledgerSequence: String(repayment.rows[0]?.ledger_sequence ?? 0),
        },
      });
      const evaluationId = await persistDailyPrecheck(input.pool, { ...decision, rosterUpdatedAt: roster.updatedAt });
      results.push({ customerId: roster.customerId, status: "evaluated", evaluationId, outcome: decision.outcome });
    } catch (error) {
      results.push({
        customerId: roster.customerId,
        status: "failed",
        reason: error instanceof Error ? error.message : "unknown evaluation failure",
      });
    }
  }
  return results;
}
