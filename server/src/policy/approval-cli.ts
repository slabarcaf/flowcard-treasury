import type { DatabasePool } from "../db/pool.js";

export interface PendingPolicyApproval {
  decisionId: string;
  evaluationId: string;
  requestId: string;
  customerId: string;
  outcome: string;
  amountCents: number;
  evaluatedAt: Date;
  transactionId?: string;
  transactionAmountCents?: number;
  currency?: string;
  merchantCategoryCode?: string;
}

export interface RevalidatedApproval {
  outcome: string;
  amountCents: number;
  reserved: boolean;
}

export type RevalidateAndReserve = (input: PendingPolicyApproval & { actorId: string; reason: string }) => Promise<RevalidatedApproval>;

/** Operator-only local CLI. Approval is unavailable unless an exact-term transactional revalidator is supplied. */
export async function runPolicyApprovalCli(
  pool: DatabasePool,
  args: string[],
  env: NodeJS.ProcessEnv,
  write: (text: string) => void = (text) => process.stdout.write(text),
  revalidateAndReserve?: RevalidateAndReserve,
): Promise<void> {
  if (env.POLICY_APPROVAL_ENABLED?.trim().toLowerCase() !== "true") {
    throw new Error("POLICY_APPROVAL_ENABLED=true is required for operator approval commands");
  }
  const [command, ...rest] = args;
  if (!command || !["list", "inspect", "approve"].includes(command)) {
    throw new Error("Usage: policy:approval <list|inspect|approve> [--decision-id ID --actor ID --reason TEXT]");
  }
  const options = parseOptions(rest);
  if (command === "list") {
    const result = await pool.query(
      `SELECT decision_id AS "decisionId", evaluation_id AS "evaluationId",
              request_id AS "requestId", customer_id AS "customerId", outcome,
              amount_cents AS "amountCents", evaluated_at AS "evaluatedAt",
              request.card_transaction_id AS "transactionId",
              request.amount_cents AS "transactionAmountCents",
              request.currency, request.merchant_category_code AS "merchantCategoryCode"
         FROM policy_request_evaluations AS decision
         JOIN remote_authorization_requests AS request
           ON request.card_transaction_event_id = decision.request_id
        WHERE decision.outcome = 'human_review' AND decision.actor_id IS NULL
          AND NOT EXISTS (
            SELECT 1 FROM policy_request_evaluations AS approval
             WHERE approval.request_id = decision.request_id
               AND approval.actor_id IS NOT NULL AND approval.outcome = 'auto_approved'
          )
        ORDER BY decision.evaluated_at, decision.request_id`,
    );
    write(`${JSON.stringify(result.rows, null, 2)}\n`);
    return;
  }
  const decisionId = required(options, "--decision-id");
  const result = await pool.query<PendingPolicyApproval>(
    `SELECT decision.decision_id AS "decisionId", decision.evaluation_id AS "evaluationId",
            decision.request_id AS "requestId", decision.customer_id AS "customerId", decision.outcome,
            decision.amount_cents AS "amountCents", decision.evaluated_at AS "evaluatedAt",
            request.card_transaction_id AS "transactionId",
            request.amount_cents AS "transactionAmountCents",
            request.currency, request.merchant_category_code AS "merchantCategoryCode"
       FROM policy_request_evaluations AS decision
       JOIN remote_authorization_requests AS request
         ON request.card_transaction_event_id = decision.request_id
      WHERE decision.decision_id = $1`,
    [decisionId],
  );
  const pending = result.rows[0];
  if (!pending) throw new Error("policy decision not found");
  if (command === "inspect") {
    write(`${JSON.stringify(pending, null, 2)}\n`);
    return;
  }
  if (pending.outcome !== "human_review") throw new Error("only human_review decisions can be approved");
  const actorId = required(options, "--actor");
  const reason = required(options, "--reason");
  if (!revalidateAndReserve) throw new Error("trusted exact-term approval revalidator is unavailable");
  const current = await revalidateAndReserve({ ...pending, actorId, reason });
  if (current.outcome !== "auto_approved" || current.amountCents !== Number(pending.amountCents) || !current.reserved) {
    throw new Error("current policy did not reserve the exact approved terms");
  }
  write(`${JSON.stringify({ decisionId, requestId: pending.requestId, customerId: pending.customerId,
    actorId, reason, outcome: current.outcome, amountCents: current.amountCents, reserved: true })}\n`);
}

function parseOptions(args: string[]): Map<string, string> {
  const values = new Map<string, string>();
  for (let index = 0; index < args.length; index += 1) {
    const name = args[index];
    const value = args[index + 1];
    if (!name?.startsWith("--") || !value || value.startsWith("--")) throw new Error(`Expected --option value near: ${name ?? "<end>"}`);
    if (values.has(name)) throw new Error(`Duplicate option: ${name}`);
    values.set(name, value);
    index += 1;
  }
  return values;
}

function required(options: Map<string, string>, name: string): string {
  const value = options.get(name)?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
