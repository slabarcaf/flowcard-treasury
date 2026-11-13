#!/usr/bin/env node
/**
 * FlowCard CI/CD Test Baseline Verifier
 * ==============================================================================
 * STRICT QUALITY GATE (FAIL-CLOSED):
 * Compares actual Vitest test execution and failures against the known pre-existing
 * application baseline (23 failures across 3 files related to Airwallex sandbox
 * differences).
 *
 * FAIL-CLOSED INVARIANTS:
 * 1. Fails immediately on runner/process crash, exit signal, or ENOENT.
 * 2. Fails immediately on non-test runner exit status (e.g. status 2, 127, 255).
 * 3. Fails immediately if zero total tests were executed (empty/aborted/stale run).
 * 4. Fails immediately if numRuntimeErrorTestSuites > 0 (module/compilation errors).
 * 5. Fails immediately if unhandledErrors array is non-empty.
 * 6. Fails immediately if any test suite fails without assertion results.
 * 7. Fails immediately if runner exit status is inconsistent with report failure counts
 *    (e.g. exit status 1 with 0 failed assertions, or exit status 0 with failures).
 * 8. Fails immediately on any unexpected test regression outside the known 23 tests.
 * 9. NEVER blanket ignores or suppresses test failures (no continue-on-error).
 * 10. NEVER excludes entire test files from the test suite.
 * 11. Accurately reports baseline status without falsely claiming "all green".
 * ==============================================================================
 */

import { spawnSync } from "node:child_process";
import { readFileSync, rmSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

// The exact, immutable set of 23 known baseline application failures across 3 files.
// Any failure outside this set causes immediate CI termination.
export const KNOWN_BASELINE_FAILURES = new Set([
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route persists and returns only a sanitized provider HTTP status for a definitive 4xx",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route persists the normalized Bloom attempt once and never invokes bridge funding or card retry",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route retains an ambiguous attempt and never creates a second provider attempt on replay",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps an immediate create result ambiguous when it has a missing card",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps an immediate create result ambiguous when it has a missing amount",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps an immediate create result ambiguous when it has a missing currency",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps an immediate create result ambiguous when it has a missing merchant",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route invalidates a completed replay that lacks provider identity and cannot match a lifecycle event",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route does not let manual reconciliation return an unverified completed result",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route reconciles a lifecycle found by the operator without repeating an ambiguous create",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a different card",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a different amount",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a different currency",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a different account",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a unrelated merchant transaction",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a missing card identity",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a missing transaction amount",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a missing transaction currency",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route leaves an operator lifecycle unresolved when it has a missing merchant identity",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps duplicate matching events unresolved instead of choosing one",
  "tests/server/kit5-fixture-checkpoint.test.ts :: Kit 5 Airwallex fixture checkpoint route keeps an unknown operator lifecycle unresolved and does not create another attempt",
  "tests/server/kit5-mocked-live-flow.test.ts :: Kit 5 mocked sandbox-live flow confirms one provider Bloom decline, then applies the same event through ordered fixture policy",
  "tests/server/next-customer.test.ts :: Kit 5 next-customer persistence claims the action before one provider create and persists Airwallex's returned lifecycle"
]);

/**
 * Evaluates Vitest test execution report with strict fail-closed validation.
 * @param {object} reportData - The JSON report object parsed from Vitest.
 * @param {object|null} runResult - Process execution result from spawnSync (error, signal, status).
 * @returns {object} Evaluation result containing ok, fatalError, actualFailures, unexpectedFailures.
 */
export function evaluateTestRun(reportData, runResult = null) {
  // 1. Process / Runner level error checks
  if (runResult) {
    if (runResult.error) {
      return {
        ok: false,
        fatalError: `Vitest process execution failed: ${runResult.error.message || runResult.error}`,
        actualFailures: [],
        unexpectedFailures: [],
        numTotalTests: 0,
      };
    }
    if (runResult.signal) {
      return {
        ok: false,
        fatalError: `Vitest runner terminated by signal: ${runResult.signal}`,
        actualFailures: [],
        unexpectedFailures: [],
        numTotalTests: 0,
      };
    }
    // Vitest exit codes: 0 = all pass, 1 = test failures.
    // Exit code 2, 127, 255, etc. indicates runner crash, unhandled exception, or syntax error.
    if (typeof runResult.status === "number" && runResult.status !== 0 && runResult.status !== 1) {
      return {
        ok: false,
        fatalError: `Vitest runner exited with fatal non-test error code ${runResult.status}`,
        actualFailures: [],
        unexpectedFailures: [],
        numTotalTests: 0,
      };
    }
  }

  // 2. Report data presence & test count check
  if (!reportData || typeof reportData !== "object" || Array.isArray(reportData)) {
    return {
      ok: false,
      fatalError: "Invalid or empty Vitest report data object",
      actualFailures: [],
      unexpectedFailures: [],
      numTotalTests: 0,
    };
  }

  const numTotalTests = reportData.numTotalTests ?? (reportData.testResults?.reduce((acc, f) => acc + (f.assertionResults?.length || 0), 0) ?? 0);
  if (numTotalTests === 0) {
    return {
      ok: false,
      fatalError: "Zero tests executed. Test suite collection failed, was empty, or report was stale.",
      actualFailures: [],
      unexpectedFailures: [],
      numTotalTests: 0,
    };
  }

  // 3. Runtime error test suites check (compilation / module syntax errors)
  if (reportData.numRuntimeErrorTestSuites && reportData.numRuntimeErrorTestSuites > 0) {
    return {
      ok: false,
      fatalError: `${reportData.numRuntimeErrorTestSuites} test suite(s) encountered fatal runtime/compilation errors.`,
      actualFailures: [],
      unexpectedFailures: [],
      numTotalTests,
    };
  }

  // 4. Unhandled errors check (unhandled promise rejections / exceptions outside assertions)
  if (Array.isArray(reportData.unhandledErrors) && reportData.unhandledErrors.length > 0) {
    const errorMessages = reportData.unhandledErrors.map(e => e.message || String(e)).join("; ");
    return {
      ok: false,
      fatalError: `Unhandled errors occurred during test execution: ${errorMessages}`,
      actualFailures: [],
      unexpectedFailures: [],
      numTotalTests,
    };
  }

  // 5. Evaluate individual test files and assertions
  const actualFailures = [];
  const unexpectedFailures = [];

  for (const fileResult of (reportData.testResults || [])) {
    const rawPath = fileResult.name || "";
    const normalizedPath = rawPath.includes("tests/")
      ? "tests/" + rawPath.split("tests/")[1]
      : rawPath;

    // Suite-level crash / file failure without assertions
    if (fileResult.status === "failed" && (!fileResult.assertionResults || fileResult.assertionResults.length === 0)) {
      const fileFailureId = `${normalizedPath} :: [FATAL SUITE CRASH] ${fileResult.message || "Suite crashed without assertion results"}`;
      actualFailures.push(fileFailureId);
      unexpectedFailures.push(fileFailureId);
      continue;
    }

    for (const assertion of fileResult.assertionResults || []) {
      if (assertion.status === "failed") {
        const testId = `${normalizedPath} :: ${assertion.fullName}`;
        actualFailures.push(testId);
        if (!KNOWN_BASELINE_FAILURES.has(testId)) {
          unexpectedFailures.push(testId);
        }
      }
    }
  }

  // 6. Consistency between runner status and failed assertion count
  if (runResult && typeof runResult.status === "number") {
    // If runResult.status is 0, zero failures allowed
    if (runResult.status === 0 && actualFailures.length > 0) {
      return {
        ok: false,
        fatalError: `Runner exited with status 0 (success) but report contains ${actualFailures.length} failed assertions. Inconsistent report.`,
        actualFailures,
        unexpectedFailures: actualFailures,
        numTotalTests,
      };
    }
    // If runResult.status is 1, there must be at least 1 actual assertion failure
    if (runResult.status === 1 && actualFailures.length === 0) {
      return {
        ok: false,
        fatalError: "Runner exited with status 1 (failure) but report recorded 0 failed assertions. Unhandled runner failure occurred.",
        actualFailures: [],
        unexpectedFailures: ["tests/runner :: Unhandled exit status 1 without recorded failed assertions"],
        numTotalTests,
      };
    }
  }

  // If report has numFailedTests field, verify it matches actualFailures count
  if (typeof reportData.numFailedTests === "number" && reportData.numFailedTests !== actualFailures.length) {
    return {
      ok: false,
      fatalError: `Report numFailedTests (${reportData.numFailedTests}) does not match counted failed assertions (${actualFailures.length}).`,
      actualFailures,
      unexpectedFailures: actualFailures,
      numTotalTests,
    };
  }

  const ok = unexpectedFailures.length === 0;
  return {
    ok,
    fatalError: null,
    actualFailures,
    unexpectedFailures,
    numTotalTests,
  };
}

/**
 * Backwards compatible helper for evaluating test results array.
 */
export function evaluateFailures(testResults) {
  const numTotalTests = testResults.reduce((acc, f) => acc + (f.assertionResults?.length || 0), 0);
  const res = evaluateTestRun({ testResults, numTotalTests }, null);
  return {
    actualFailures: res.actualFailures,
    unexpectedFailures: res.unexpectedFailures,
  };
}

// Run CLI when invoked directly
const isMain = process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1]);
if (isMain) {
  const reportPath = resolve(tmpdir(), `vitest-baseline-${Date.now()}.json`);
  // Ensure stale report file is removed before invocation
  try { rmSync(reportPath, { force: true }); } catch (_) {}

  console.log("======================================================================");
  console.log("FlowCard Pre-Release Test Verification (Strict Baseline Comparison)");
  console.log("======================================================================");
  console.log(`Expected baseline failures: ${KNOWN_BASELINE_FAILURES.size} known tests across 3 files.`);
  console.log("Running Vitest test suite on tests/ directory...");

  const vitestArgs = [
    "vitest",
    "run",
    "tests/",
    "--reporter=json",
    `--outputFile=${reportPath}`,
  ];

  const res = spawnSync("npx", vitestArgs, {
    stdio: ["inherit", "pipe", "pipe"],
    encoding: "utf8",
    env: { ...process.env },
  });

  if (res.error) {
    console.error("FATAL: Failed to spawn Vitest process:", res.error);
    process.exit(1);
  }

  if (res.signal) {
    console.error(`FATAL: Vitest process was terminated by signal ${res.signal}`);
    process.exit(1);
  }

  if (!existsSync(reportPath)) {
    console.error("FATAL: Vitest failed to produce JSON results file at", reportPath);
    console.error(res.stderr || res.stdout);
    process.exit(1);
  }

  let reportData;
  try {
    reportData = JSON.parse(readFileSync(reportPath, "utf8"));
  } catch (err) {
    console.error("FATAL: Failed to parse Vitest JSON report:", err);
    process.exit(1);
  } finally {
    try { rmSync(reportPath, { force: true }); } catch (_) {}
  }

  const evaluation = evaluateTestRun(reportData, res);

  if (evaluation.fatalError) {
    console.error("\n======================================================================");
    console.error(`FATAL TEST RUNNER FAILURE: ${evaluation.fatalError}`);
    console.error("Deployment halted. CI cannot proceed with runner or compilation errors.");
    console.error("======================================================================");
    process.exit(1);
  }

  console.log(`\nResults: ${evaluation.numTotalTests} tests executed, ${evaluation.actualFailures.length} actual failures.`);

  if (evaluation.unexpectedFailures.length > 0) {
    console.error("\n======================================================================");
    console.error(`BLOCKER: ${evaluation.unexpectedFailures.length} UNEXPECTED NEW TEST FAILURES DETECTED!`);
    console.error("Deployment halted. CI cannot proceed with new test regressions.");
    console.error("======================================================================");
    for (const fail of evaluation.unexpectedFailures) {
      console.error(`  [FAIL] ${fail}`);
    }
    process.exit(1);
  }

  const resolvedBaselineCount = KNOWN_BASELINE_FAILURES.size - evaluation.actualFailures.length;
  if (resolvedBaselineCount > 0) {
    console.log(`NOTICE: ${resolvedBaselineCount} baseline tests previously failing have passed.`);
  }

  console.log("\n======================================================================");
  console.log(`PASS: All ${evaluation.actualFailures.length} failures match the documented 23 baseline failures.`);
  console.log("Zero unexpected regressions. Test quality gate verified.");
  console.log("======================================================================");
  process.exit(0);
}
