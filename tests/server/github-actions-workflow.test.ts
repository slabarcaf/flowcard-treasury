import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { readFileSync, existsSync, mkdtempSync, writeFileSync, chmodSync, rmSync, mkdirSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import { evaluateTestRun, KNOWN_BASELINE_FAILURES } from "../../infra/scripts/check-test-baseline.js";

const WORKTREE_ROOT = resolve(__dirname, "../..");
const WORKFLOW_PATH = resolve(WORKTREE_ROOT, ".github/workflows/aws-demo.yml");
const OIDC_MODULE_PATH = resolve(WORKTREE_ROOT, "infra/modules/oidc/main.tf");
const DEPLOY_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/deploy-demo.sh");
const DEPLOY_FRONTEND_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/deploy-frontend.sh");
const ROLLBACK_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/rollback-demo.sh");
const ROLLBACK_COORDINATOR_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/rollback-release.sh");

const VALID_DIGEST = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const VALID_ACCOUNT = "123456789012";

let FRONTEND_FIXTURE: string;
beforeAll(() => {
  FRONTEND_FIXTURE = mkdtempSync(join(tmpdir(), "flowcard-release-frontend-"));
  writeFileSync(join(FRONTEND_FIXTURE, "index.html"), "<html>synthetic frontend fixture</html>");
});
afterAll(() => rmSync(FRONTEND_FIXTURE, { recursive: true, force: true }));


describe("Task 8: GitHub Actions CI/CD Release & OIDC Security Tier", () => {
  describe("Go-only GitHub Actions release definition", () => {
    it("retains the registered manual workflow path with explicit build-only default", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text).toContain("workflow_dispatch:");
      expect(text).not.toMatch(/^  (push|pull_request):/m);
      expect(text).toContain("deploy:\n        type: boolean\n        default: false");
      expect(text).toContain("confirm_callback_cutover:");
      expect(text).toContain("enable_workers:");
    });
    it("keeps deploy/ref/repository checks and serialized manual releases", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text).toContain("github.ref == 'refs/heads/develop'");
      expect(text).toContain("github.repository == 'slabarcaf/flowcard-treasury'");
      expect(text).toContain("if: inputs.deploy == true");
      expect(text).toContain("group: aws-demo-release");
      expect(text).toContain("cancel-in-progress: false");
    });
    it("grants OIDC only to execute-release after unprivileged CI and build", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      const release = text.indexOf("  execute-release:");
      expect(text.slice(0, release)).not.toContain("id-token: write");
      expect(text.slice(release)).toContain("id-token: write");
      expect(text).toContain("needs: [validate-and-test, build-and-metadata]");
      expect(text).toContain("uses: ./.github/workflows/go-ci.yml");
    });
    it("pins actions and exact checkout source", () => {
      for (const file of [WORKFLOW_PATH, resolve(WORKTREE_ROOT, ".github/workflows/go-ci.yml")]) {
        const text = readFileSync(file, "utf8");
        for (const match of text.matchAll(/uses:\s+([^\s#]+)/g)) {
          if (!match[1].startsWith("./")) expect(match[1]).toMatch(/^[^@]+@[a-f0-9]{40}$/);
        }
        expect(text).toContain("ref: ${{ github.sha }}");
      }
    });
    it("tests every Go module using separate ephemeral postgres jobs", () => {
      const text = readFileSync(resolve(WORKTREE_ROOT, ".github/workflows/go-ci.yml"), "utf8");
      for (const module of ["company-onboarding", "browser-gateway", "shortfall-detection", "underwriting-engine", "funding-collection", "go-coordinator"]) expect(text).toContain(module);
      expect(text).toContain("go-version-file: services/${{ matrix.module }}/go.mod");
      expect(text).toContain("54329:5432");
      expect(text).toContain("run-g-local.sh");
      expect(text).toContain("-race -p 1");
      expect(text).toContain("run ./cmd/openapi -check");
      expect(text).not.toContain("id-token: write");
    });
    it("builds the actual Go product and common amd64 image", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text).toContain("npm run build:product");
      expect(text).toContain("--platform linux/amd64");
      expect(text).toContain("--file infra/runtime/Dockerfile.go-stack");
      expect(text).not.toContain("-f Dockerfile .");
    });
    it("seals archives before credentials and never rebuilds during deployment", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text).toContain("docker save");
      expect(text).toContain("gzip -n");
      expect(text).toContain("create-go-manifest.mjs");
      expect(text.slice(text.indexOf("  execute-release:"))).not.toContain("docker build");
      const deploy = readFileSync(resolve(WORKTREE_ROOT, "infra/scripts/deploy-go-stack.mjs"), "utf8");
      expect(deploy).toContain("verifyArchive(archive");
      expect(deploy).toContain("--password-stdin");
      expect(deploy).toContain("Container source revision mismatch");
    });
    it("validates callback approval, target and artifacts before assuming AWS credentials", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text.indexOf("Verify foundation, approvals and sealed artifacts before OIDC")).toBeLessThan(text.indexOf("aws-actions/configure-aws-credentials@"));
      expect(text).toContain("GO_FOUNDATION_JSON: ${{ vars.GO_RELEASE_FOUNDATION_JSON }}");
      expect(text).toContain("CALLBACK_APPROVED: ${{ inputs.confirm_callback_cutover }}");
      expect(text).toContain("--deployment-inputs");
    });
    it("retains failure metadata without automatic rollback or secret-value retrieval", () => {
      const text = readFileSync(WORKFLOW_PATH, "utf8");
      expect(text).toContain("if: always()");
      expect(text).toContain("go-release-evidence-");
      expect(text).not.toContain("rollback-release.sh");
      const deploy = readFileSync(resolve(WORKTREE_ROOT, "infra/scripts/deploy-go-stack.mjs"), "utf8");
      expect(deploy).not.toContain("get-secret-value");
      expect(deploy).toContain("rollback: false");
      expect(deploy).toContain("failed_manual_recovery_required");
    });
  });

  describe("CI Test Baseline Gate Fail-Closed Invariants (check-test-baseline.js)", () => {
    it("identifies exactly 23 documented pre-existing baseline failures", () => {
      expect(KNOWN_BASELINE_FAILURES.size).toBe(23);
    });

    it("fails closed when zero tests were executed (empty report / aborted test run)", () => {
      const emptyReport = { numTotalTests: 0, testResults: [] };
      const res = evaluateTestRun(emptyReport);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Zero tests executed");
    });

    it("fails closed when runner process encounters an execution error (e.g. ENOENT)", () => {
      const runResult = { error: new Error("spawnSync npx ENOENT") };
      const res = evaluateTestRun({}, runResult);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Vitest process execution failed");
    });

    it("fails closed when runner process is killed by signal (e.g. SIGKILL / OOM)", () => {
      const runResult = { signal: "SIGKILL" };
      const res = evaluateTestRun({}, runResult);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Vitest runner terminated by signal: SIGKILL");
    });

    it("fails closed when test suites encounter fatal compilation or module syntax errors", () => {
      const errorReport = {
        numTotalTests: 10,
        numRuntimeErrorTestSuites: 2,
        testResults: [],
      };
      const res = evaluateTestRun(errorReport);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("encountered fatal runtime/compilation errors");
    });

    it("fails closed when unhandled errors occur outside assertion blocks", () => {
      const unhandledReport = {
        numTotalTests: 10,
        unhandledErrors: [{ message: "Unhandled promise rejection: connect ECONNREFUSED" }],
        testResults: [],
      };
      const res = evaluateTestRun(unhandledReport);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Unhandled errors occurred during test execution");
    });

    it("fails closed when a test file status is failed with zero assertion results (suite-level crash)", () => {
      const suiteCrashReport = {
        numTotalTests: 5,
        testResults: [
          {
            name: "tests/server/crashed-suite.test.ts",
            status: "failed",
            message: "Fatal crash during beforeAll hook",
            assertionResults: [],
          },
        ],
      };
      const res = evaluateTestRun(suiteCrashReport);
      expect(res.ok).toBe(false);
      expect(res.unexpectedFailures.length).toBe(1);
      expect(res.unexpectedFailures[0]).toContain("FATAL SUITE CRASH");
    });

    it("fails closed when an unexpected new test regression is detected", () => {
      const regressionReport = {
        numTotalTests: 10,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [
              {
                status: "failed",
                fullName: "unexpected regression in config loading",
              },
            ],
          },
        ],
      };
      const res = evaluateTestRun(regressionReport);
      expect(res.ok).toBe(false);
      expect(res.unexpectedFailures.length).toBe(1);
      expect(res.unexpectedFailures[0]).toContain("unexpected regression in config loading");
    });

    it("passes cleanly when actual failures match the documented 23 baseline failures", () => {
      const baselineReport = {
        numTotalTests: 300,
        testResults: [
          {
            name: "tests/server/kit5-fixture-checkpoint.test.ts",
            assertionResults: [
              {
                status: "failed",
                fullName: "Kit 5 Airwallex fixture checkpoint route persists and returns only a sanitized provider HTTP status for a definitive 4xx",
              },
            ],
          },
        ],
      };
      const res = evaluateTestRun(baselineReport);
      expect(res.ok).toBe(true);
      expect(res.fatalError).toBeNull();
      expect(res.actualFailures.length).toBe(1);
      expect(res.unexpectedFailures.length).toBe(0);
    });

    it("passes cleanly when all tests pass (0 failures)", () => {
      const allPassReport = {
        numTotalTests: 300,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [
              {
                status: "passed",
                fullName: "loads config cleanly",
              },
            ],
          },
        ],
      };
      const res = evaluateTestRun(allPassReport);
      expect(res.ok).toBe(true);
      expect(res.actualFailures.length).toBe(0);
      expect(res.unexpectedFailures.length).toBe(0);
    });

    it("fails closed when runner exits with fatal non-test error code (status 2, 127, etc.)", () => {
      const runResult = { status: 2 };
      const report = {
        numTotalTests: 50,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [{ status: "passed", fullName: "loads config cleanly" }],
          },
        ],
      };
      const res = evaluateTestRun(report, runResult);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("fatal non-test error code 2");
    });

    it("fails closed when runner exits with status 1 but report records 0 failed assertions", () => {
      const runResult = { status: 1 };
      const report = {
        numTotalTests: 50,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [{ status: "passed", fullName: "loads config cleanly" }],
          },
        ],
      };
      const res = evaluateTestRun(report, runResult);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Runner exited with status 1 (failure) but report recorded 0 failed assertions");
    });

    it("fails closed when runner exits with status 0 but report contains failed assertions", () => {
      const runResult = { status: 0 };
      const report = {
        numTotalTests: 50,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [{ status: "failed", fullName: "some failing test" }],
          },
        ],
      };
      const res = evaluateTestRun(report, runResult);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Runner exited with status 0 (success) but report contains");
    });

    it("fails closed when report numFailedTests does not match counted failed assertions", () => {
      const report = {
        numTotalTests: 50,
        numFailedTests: 5,
        testResults: [
          {
            name: "tests/server/config.test.ts",
            assertionResults: [{ status: "failed", fullName: "only one failure" }],
          },
        ],
      };
      const res = evaluateTestRun(report);
      expect(res.ok).toBe(false);
      expect(res.fatalError).toContain("Report numFailedTests (5) does not match counted failed assertions (1)");
    });

    it("fails closed when report data is null, undefined, or not an object", () => {
      expect(evaluateTestRun(null).ok).toBe(false);
      expect(evaluateTestRun(undefined).ok).toBe(false);
      expect(evaluateTestRun("stale string output").ok).toBe(false);
    });
  });

  describe("Release Automation Scripts: OIDC Mode Support", () => {
    it("deploy-demo.sh accepts --oidc flag and proceeds past credential argument validation", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "oidc-argument-test-"));
      const sentinel = join(tmpDir, "aws-called");
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
printf '%s\\n' "$*" > "$AWS_CALL_SENTINEL"
exit 42
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--oidc",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          timeout: 3000,
          env: {
            ...process.env,
            PATH: `${tmpDir}:${process.env.PATH ?? ""}`,
            AWS_PROFILE: "",
            AWS_DEFAULT_PROFILE: "",
            AWS_ACCESS_KEY_ID: "",
            AWS_SECRET_ACCESS_KEY: "",
            AWS_SESSION_TOKEN: "",
            AWS_SECURITY_TOKEN: "",
            AWS_WEB_IDENTITY_TOKEN_FILE: "",
            AWS_ROLE_ARN: "",
            AWS_CONTAINER_CREDENTIALS_RELATIVE_URI: "",
            AWS_CONTAINER_CREDENTIALS_FULL_URI: "",
            AWS_EC2_METADATA_DISABLED: "true",
            AWS_CONFIG_FILE: join(tmpDir, "config"),
            AWS_SHARED_CREDENTIALS_FILE: join(tmpDir, "credentials"),
            AWS_CALL_SENTINEL: sentinel,
          },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).not.toContain("Either --profile <name> or --oidc must be specified");
        expect(res.stderr).not.toContain("Unknown option");
        expect(res.stderr).toContain("Failed to retrieve AWS caller account from STS identity check");
        expect(readFileSync(sentinel, "utf8").trim()).toBe("sts get-caller-identity --output json");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("deploy-frontend.sh rejects execution when neither --profile nor --oidc is supplied", () => {
      const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890",
      ], { encoding: "utf8", env: { ...process.env, AWS_PROFILE: "" } });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("--profile is required (or specify --oidc for ambient CI/CD)");
    });

    it("deploy-frontend.sh accepts --oidc flag and proceeds past credential argument validation", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "oidc-frontend-argument-test-"));
      const sentinel = join(tmpDir, "aws-called");
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
printf '%s\\n' "$*" > "$AWS_CALL_SENTINEL"
exit 42
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--oidc",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          timeout: 3000,
          env: {
            ...process.env,
            PATH: `${tmpDir}:/usr/bin:/bin`,
            AWS_PROFILE: "",
            AWS_DEFAULT_PROFILE: "",
            AWS_ACCESS_KEY_ID: "",
            AWS_SECRET_ACCESS_KEY: "",
            AWS_SESSION_TOKEN: "",
            AWS_SECURITY_TOKEN: "",
            AWS_WEB_IDENTITY_TOKEN_FILE: "",
            AWS_ROLE_ARN: "",
            AWS_CONTAINER_CREDENTIALS_RELATIVE_URI: "",
            AWS_CONTAINER_CREDENTIALS_FULL_URI: "",
            AWS_EC2_METADATA_DISABLED: "true",
            AWS_CONFIG_FILE: join(tmpDir, "config"),
            AWS_SHARED_CREDENTIALS_FILE: join(tmpDir, "credentials"),
            AWS_CALL_SENTINEL: sentinel,
          },
        });

        expect(res.error).toBeUndefined();
        expect(res.status).toBe(42);
        expect(res.stderr).not.toContain("--profile is required");
        expect(res.stderr).not.toContain("Unknown option");
        expect(readFileSync(sentinel, "utf8").trim()).toBe("sts get-caller-identity --output json");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("rollback-demo.sh rejects execution when neither --profile nor --oidc is supplied", () => {
      const res = spawnSync(ROLLBACK_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
      ], { encoding: "utf8", env: { ...process.env, AWS_PROFILE: "" } });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Either --profile <name> or --oidc must be specified");
    });

    it("rollback-demo.sh accepts --oidc flag and proceeds past credential argument validation", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "oidc-rollback-argument-test-"));
      const sentinel = join(tmpDir, "aws-called");
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
printf '%s\\n' "$*" > "$AWS_CALL_SENTINEL"
exit 42
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(ROLLBACK_SCRIPT, [
          "--confirm-approval",
          "--oidc",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
        ], {
          encoding: "utf8",
          timeout: 3000,
          env: {
            ...process.env,
            PATH: `${tmpDir}:/usr/bin:/bin`,
            AWS_PROFILE: "",
            AWS_DEFAULT_PROFILE: "",
            AWS_ACCESS_KEY_ID: "",
            AWS_SECRET_ACCESS_KEY: "",
            AWS_SESSION_TOKEN: "",
            AWS_SECURITY_TOKEN: "",
            AWS_WEB_IDENTITY_TOKEN_FILE: "",
            AWS_ROLE_ARN: "",
            AWS_CONTAINER_CREDENTIALS_RELATIVE_URI: "",
            AWS_CONTAINER_CREDENTIALS_FULL_URI: "",
            AWS_EC2_METADATA_DISABLED: "true",
            AWS_CONFIG_FILE: join(tmpDir, "config"),
            AWS_SHARED_CREDENTIALS_FILE: join(tmpDir, "credentials"),
            AWS_CALL_SENTINEL: sentinel,
          },
        });

        expect(res.error).toBeUndefined();
        expect(res.status).toBe(42);
        expect(res.stderr).not.toContain("Either --profile <name> or --oidc must be specified");
        expect(res.stderr).not.toContain("Unknown option");
        expect(readFileSync(sentinel, "utf8").trim()).toBe("sts get-caller-identity --output json");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("deploy-demo.sh resolves infrastructure without Terraform in PATH when --infra-outputs-file is provided", () => {
      const outputsFile = resolve(WORKTREE_ROOT, "infra/environments/demo/foundation-outputs.example.json");
      expect(existsSync(outputsFile)).toBe(true);

      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-outputs-test-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        // Run with PATH containing mock aws and system binaries, omitting terraform/tofu
        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--oidc",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
          "--infra-outputs-file", outputsFile,
        ], {
          encoding: "utf8",
          env: {
            ...process.env,
            PATH: `${tmpDir}:/usr/bin:/bin`, // no terraform or tofu
          },
        });

        // It must NOT fail complaining about missing terraform executable
        expect(res.stderr).not.toContain("Neither Terraform ('terraform') nor OpenTofu ('tofu') executable found in PATH");
        expect(res.stdout).toContain("Loading infrastructure outputs from validated release artifact");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("Coordinated Coupled Rollback Coordinator (rollback-release.sh)", () => {
    it("rollback-release.sh exists and is executable", () => {
      expect(existsSync(ROLLBACK_COORDINATOR_SCRIPT)).toBe(true);
    });

    it("fails closed when --confirm-approval is missing", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--profile", "demo",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Rollback requires explicit confirmation via --confirm-approval flag");
    });

    it("fails closed when --account-id is missing or invalid", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--profile", "demo",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("--account-id is required");
    });

    it("fails closed when target revisions are missing", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--profile", "demo",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Target revisions required for all three services");
    });

    it("fails closed before mutations when frontend build directory does not exist", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--frontend-build-dir", "/tmp/non-existent-prior-frontend-dir-99999",
        "--prior-frontend-checksum", "1111111111111111111111111111111111111111111111111111111111111111",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890ABC",
        "--oidc",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Target frontend build directory does not exist");
      expect(res.stderr).toContain("Aborting before any mutations");
    });

    it("fails closed when prior frontend checksum is fabricated 64 zeros", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--frontend-build-dir", FRONTEND_FIXTURE,
        "--prior-frontend-checksum", "0000000000000000000000000000000000000000000000000000000000000000",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890ABC",
        "--oidc",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Fabricated zero-checksum (all zeros) rejected");
      expect(res.stderr).toContain("Aborting before any mutations");
    });

    it("fails closed when bucket or distribution ID is missing", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--frontend-build-dir", FRONTEND_FIXTURE,
        "--prior-frontend-checksum", "1111111111111111111111111111111111111111111111111111111111111111",
        "--oidc",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Frontend S3 bucket (--bucket) and CloudFront distribution ID (--distribution-id) are required");
    });

    it("fails closed when previous release indicates initial deployment with rollback_available: false", () => {
      const tmpManifest = join(tmpdir(), `initial-deploy-manifest-${Date.now()}.json`);
      writeFileSync(tmpManifest, JSON.stringify({
        schema_version: "1.0",
        previous_release: {
          rollback_available: false,
          status: "initial_deployment_no_prior_release"
        }
      }));

      try {
        const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--manifest", tmpManifest,
          "--oidc",
        ], { encoding: "utf8" });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("initial deployment with no prior release");
      } finally {
        rmSync(tmpManifest, { force: true });
      }
    });

    it("fails closed before mutations when frontend directory checksum does not match prior manifest checksum", () => {
      const res = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
        "--frontend-build-dir", FRONTEND_FIXTURE,
        "--prior-frontend-checksum", "bad1111111111111111111111111111111111111111111111111111111111111",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890ABC",
        "--oidc",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("does not match expected prior checksum");
      expect(res.stderr).toContain("Aborting rollback before any mutations");
    });

    it("compute-frontend-hash.sh produces identical SHA-256 hashes across different directory roots", () => {
      const helper = resolve(WORKTREE_ROOT, "infra/scripts/compute-frontend-hash.sh");
      expect(existsSync(helper)).toBe(true);

      const h1 = spawnSync(helper, [FRONTEND_FIXTURE], { encoding: "utf8" }).stdout.trim();
      expect(h1).toMatch(/^[a-f0-9]{64}$/);

      // Clone dist contents to a completely different temporary root
      const tmpDir = mkdtempSync(join(tmpdir(), "frontend-hash-copy-"));
      try {
        spawnSync("cp", ["-r", `${FRONTEND_FIXTURE}/.`, tmpDir]);
        const h2 = spawnSync(helper, [tmpDir], { encoding: "utf8" }).stdout.trim();
        expect(h2).toBe(h1);

        // Mutate one byte and verify hash changes
        writeFileSync(join(tmpDir, "tampered.txt"), "tampered content");
        const h3 = spawnSync(helper, [tmpDir], { encoding: "utf8" }).stdout.trim();
        expect(h3).not.toBe(h1);
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("compute-frontend-hash.sh fails closed when target build directory contains zero files", () => {
      const helper = resolve(WORKTREE_ROOT, "infra/scripts/compute-frontend-hash.sh");
      const emptyDir = mkdtempSync(join(tmpdir(), "empty-frontend-"));
      try {
        const res = spawnSync(helper, [emptyDir], { encoding: "utf8" });
        expect(res.status).toBe(1);
        expect(res.stderr).toContain("Target build directory contains zero files");
      } finally {
        rmSync(emptyDir, { recursive: true, force: true });
      }
    });

    it("compute-frontend-hash.sh detects hidden file tampering and changes directory hash", () => {
      const helper = resolve(WORKTREE_ROOT, "infra/scripts/compute-frontend-hash.sh");
      const tmpDir = mkdtempSync(join(tmpdir(), "hidden-tamper-test-"));
      try {
        writeFileSync(join(tmpDir, "index.html"), "<html><body>App</body></html>");
        mkdirSync(join(tmpDir, "assets"), { recursive: true });
        writeFileSync(join(tmpDir, "assets", "app.js"), "console.log(1);");

        const h1 = spawnSync(helper, [tmpDir], { encoding: "utf8" }).stdout.trim();
        expect(h1).toMatch(/^[a-f0-9]{64}$/);

        // Inject unverified hidden file
        writeFileSync(join(tmpDir, ".unverified-hidden-tamper"), "malicious-code");
        const h2 = spawnSync(helper, [tmpDir], { encoding: "utf8" }).stdout.trim();
        expect(h2).toMatch(/^[a-f0-9]{64}$/);
        expect(h2).not.toBe(h1);
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("executes coupled rollback coordination with stub children and handles phase failures safely", () => {
      const helper = resolve(WORKTREE_ROOT, "infra/scripts/compute-frontend-hash.sh");
      const genuineChecksum = spawnSync(helper, [FRONTEND_FIXTURE], { encoding: "utf8" }).stdout.trim();

      const tmpDir = mkdtempSync(join(tmpdir(), "rollback-rehearsal-"));
      try {
        // Mock AWS CLI for caller identity
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        // 1. Stub child failure: Phase 1 (ECS) fails -> Coordinator halts immediately
        const mockScriptsDir = join(tmpDir, "scripts");
        spawnSync("mkdir", ["-p", mockScriptsDir]);

        const stubRollbackDemoFail = join(mockScriptsDir, "rollback-demo.sh");
        writeFileSync(stubRollbackDemoFail, `#!/usr/bin/env bash
echo "MOCK_ROLLBACK_DEMO_FAIL" >&2
exit 1
`);
        chmodSync(stubRollbackDemoFail, 0o755);

        const stubDeployFrontend = join(mockScriptsDir, "deploy-frontend.sh");
        writeFileSync(stubDeployFrontend, `#!/usr/bin/env bash
echo "MOCK_DEPLOY_FRONTEND_CALLED" >&2
exit 0
`);
        chmodSync(stubDeployFrontend, 0o755);

        // Copy compute-frontend-hash.sh and rollback-release.sh to mock dir
        spawnSync("cp", [helper, join(mockScriptsDir, "compute-frontend-hash.sh")]);
        const testCoordinator = join(mockScriptsDir, "rollback-release.sh");
        spawnSync("cp", [ROLLBACK_COORDINATOR_SCRIPT, testCoordinator]);

        const resFailPhase1 = spawnSync(testCoordinator, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
          "--frontend-build-dir", FRONTEND_FIXTURE,
          "--prior-frontend-checksum", genuineChecksum,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--oidc",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(resFailPhase1.status).toBe(1);
        expect(resFailPhase1.stderr).toContain("Phase 1 FAILED: ECS service rollback failed");
        expect(resFailPhase1.stderr).not.toContain("MOCK_DEPLOY_FRONTEND_CALLED");
        expect(resFailPhase1.stdout).not.toContain("COUPLED ROLLBACK COMPLETED SUCCESSFULLY");

        // 2. Stub child failure: Phase 1 succeeds, Phase 2 (Frontend) fails -> Exits 1 with critical diagnostic
        const stubRollbackDemoPass = join(mockScriptsDir, "rollback-demo.sh");
        writeFileSync(stubRollbackDemoPass, `#!/usr/bin/env bash
echo "MOCK_ROLLBACK_DEMO_SUCCESS"
exit 0
`);
        chmodSync(stubRollbackDemoPass, 0o755);

        const stubDeployFrontendFail = join(mockScriptsDir, "deploy-frontend.sh");
        writeFileSync(stubDeployFrontendFail, `#!/usr/bin/env bash
echo "MOCK_DEPLOY_FRONTEND_FAIL" >&2
exit 1
`);
        chmodSync(stubDeployFrontendFail, 0o755);

        const resFailPhase2 = spawnSync(testCoordinator, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
          "--frontend-build-dir", FRONTEND_FIXTURE,
          "--prior-frontend-checksum", genuineChecksum,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--oidc",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(resFailPhase2.status).toBe(1);
        expect(resFailPhase2.stderr).toContain("Phase 2 FAILED: Frontend asset redeployment failed");
        expect(resFailPhase2.stderr).toContain("CRITICAL STATE: Backend services were reverted (Phase 1 OK), but frontend failed (Phase 2 FAIL)");
        expect(resFailPhase2.stdout).not.toContain("COUPLED ROLLBACK COMPLETED SUCCESSFULLY");

        // 3. Both phases succeed -> Coordinator verifies coupled completion
        writeFileSync(stubDeployFrontend, `#!/usr/bin/env bash
echo "MOCK_DEPLOY_FRONTEND_SUCCESS"
exit 0
`);
        chmodSync(stubDeployFrontend, 0o755);

        const resSuccess = spawnSync(testCoordinator, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
          "--frontend-build-dir", FRONTEND_FIXTURE,
          "--prior-frontend-checksum", genuineChecksum,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--oidc",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(resSuccess.status).toBe(0);
        expect(resSuccess.stdout).toContain("COUPLED ROLLBACK COMPLETED SUCCESSFULLY");
        expect(resSuccess.stdout).toContain("Zero database migrations were reversed; zero persisted data was modified");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("OIDC IAM Scoped Least-Privilege Policy Invariants", () => {
    it("OIDC deployer role restricts trust policy to immutable repository identity, develop branch, and sts audience", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).toContain('"token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"');
      expect(content).toContain('"token.actions.githubusercontent.com:sub" = "repo:${var.github_owner_name}@${var.github_owner_id}/${var.github_repository_name}@${var.github_repository_id}:ref:refs/heads/develop"');
    });

    it("iam:PassRole is strictly scoped to exact ECS execution/task roles with ecs-tasks condition", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).toContain('Action = ["iam:PassRole"]');
      expect(content).toContain('"iam:PassedToService" = "ecs-tasks.amazonaws.com"');
      expect(content).toContain("var.execution_role_arns");
      expect(content).toContain("var.task_role_arns");
    });

    it("ecs:RunTask is strictly scoped to exact migration and seed task definitions with cluster condition", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).toContain('Sid    = "ECSRunMigrationAndSeedTasks"');
      expect(content).toContain('Action = [\n          "ecs:RunTask"\n        ]');
      expect(content).toContain("task-definition/flowcard-demo-node-migration:*");
      expect(content).toContain("task-definition/flowcard-demo-go-migration:*");
      expect(content).toContain("task-definition/flowcard-demo-synthetic-seed:*");
      expect(content).toContain('"ecs:cluster" = var.ecs_cluster_arn');
    });

    it("EC2 network discovery permissions are explicitly scoped for subnets and security groups", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).toContain('Sid    = "EC2NetworkDiscovery"');
      expect(content).toContain('"ec2:DescribeSubnets"');
      expect(content).toContain('"ec2:DescribeSecurityGroups"');
    });

    it("ECR repository access has NO wildcard fallback", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).toContain("Resource = var.ecr_repository_arns");
      expect(content).not.toContain('length(var.ecr_repository_arns) > 0 ? var.ecr_repository_arns : ["*"]');
    });

    it("scopes CloudFront invalidation polling to the exact distribution and omits it when disabled", () => {
      const policy = readFileSync(OIDC_MODULE_PATH, "utf8");
      const variables = readFileSync(resolve(WORKTREE_ROOT, "infra/modules/oidc/variables.tf"), "utf8");
      const demoMain = readFileSync(resolve(WORKTREE_ROOT, "infra/environments/demo/main.tf"), "utf8");
      const cloudfrontBlock = policy.split('Sid    = "CloudFrontInvalidation"')[1] || "";

      expect(policy).toContain("var.cloudfront_distribution_arn == null ? [] :");
      expect(cloudfrontBlock).toContain('"cloudfront:GetDistribution"');
      expect(cloudfrontBlock).toContain('"cloudfront:CreateInvalidation"');
      expect(cloudfrontBlock).toContain('"cloudfront:GetInvalidation"');
      expect(cloudfrontBlock).toContain("Resource = [var.cloudfront_distribution_arn]");
      expect(cloudfrontBlock).not.toContain('Resource = ["*"]');
      expect(variables).toContain('description = "Optional ARN of the CloudFront distribution for cache invalidation"');
      expect(variables).toContain("default     = null");
      expect(demoMain).toContain("var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_arn : null");
    });

    it("strictly omits secretsmanager actions from the deployer policy statements", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      const deployerPolicyBlock = content.split('resource "aws_iam_policy" "github_deployer"')[1] || "";
      expect(deployerPolicyBlock).not.toContain('"secretsmanager:');
      expect(deployerPolicyBlock).not.toContain("secretsmanager:GetSecretValue");
    });

    it("strictly omits rds:* master database administrator permissions from deployer policy", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).not.toContain('"rds:*"');
      expect(content).not.toContain("rds:ModifyDBInstance");
      expect(content).not.toContain("rds:DeleteDBInstance");
    });

    it("strictly omits broad IAM admin permissions from deployer policy", () => {
      const content = readFileSync(OIDC_MODULE_PATH, "utf8");
      expect(content).not.toContain('"iam:*"');
      expect(content).not.toContain('"iam:CreateRole"');
      expect(content).not.toContain('"iam:AttachRolePolicy"');
    });
  });
});
