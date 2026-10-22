import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { runPolicyApprovalCli } from "../../../server/src/policy/approval-cli.js";

const pending = {
  decisionId: "decision-1", evaluationId: "evaluation-1", requestId: "request-1", customerId: "customer-1",
  outcome: "human_review", amountCents: 12_300, evaluatedAt: new Date("2026-10-22T12:00:00Z"),
};

function pool(): DatabasePool {
  return {
    query: vi.fn(async <Row>(): Promise<QueryResult<Row>> => ({ rows: [pending] as Row[], rowCount: 1 })),
    connect: vi.fn(), end: vi.fn(),
  } as unknown as DatabasePool;
}

describe("policy approval CLI", () => {
  it("requires the operator command gate", async () => {
    await expect(runPolicyApprovalCli(pool(), ["list"], {})).rejects.toThrow("POLICY_APPROVAL_ENABLED=true");
  });

  it("lists and inspects only non-sensitive stored decision fields", async () => {
    const output: string[] = [];
    await runPolicyApprovalCli(pool(), ["list"], { POLICY_APPROVAL_ENABLED: "true" }, (text) => output.push(text));
    expect(output.join("")).toContain("request-1");
    expect(output.join("")).not.toContain("rationale");
    await runPolicyApprovalCli(pool(), ["inspect", "--decision-id", "decision-1"],
      { POLICY_APPROVAL_ENABLED: "true" }, (text) => output.push(text));
    expect(output.join("")).toContain("customer-1");
  });

  it("requires actor and reason and fails closed without a revalidator", async () => {
    const cliPool = pool();
    await expect(runPolicyApprovalCli(cliPool, ["approve", "--decision-id", "decision-1"],
      { POLICY_APPROVAL_ENABLED: "true" })).rejects.toThrow("--actor is required");
    await expect(runPolicyApprovalCli(cliPool,
      ["approve", "--decision-id", "decision-1", "--actor", "operator-1", "--reason", "reviewed"],
      { POLICY_APPROVAL_ENABLED: "true" })).rejects.toThrow("revalidator is unavailable");
  });

  it("passes exact stored terms to the revalidator and reports successful current approval", async () => {
    const revalidate = vi.fn(async () => ({ outcome: "auto_approved", amountCents: 12_300, reserved: true }));
    const output: string[] = [];
    await runPolicyApprovalCli(pool(),
      ["approve", "--decision-id", "decision-1", "--actor", "operator-1", "--reason", "reviewed"],
      { POLICY_APPROVAL_ENABLED: "true" }, (text) => output.push(text), revalidate);
    expect(revalidate).toHaveBeenCalledWith({ ...pending, actorId: "operator-1", reason: "reviewed" });
    expect(output.join("")).toContain('"reserved":true');
  });

  it("does not report approval when current policy fails or terms differ", async () => {
    const revalidate = vi.fn(async () => ({ outcome: "blocked", amountCents: 12_200, reserved: false }));
    await expect(runPolicyApprovalCli(pool(),
      ["approve", "--decision-id", "decision-1", "--actor", "operator-1", "--reason", "reviewed"],
      { POLICY_APPROVAL_ENABLED: "true" }, undefined, revalidate)).rejects.toThrow("did not reserve the exact approved terms");
  });
});
