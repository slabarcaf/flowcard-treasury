import { describe, expect, it } from "vitest";
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
const FINALIZE_RELEASE_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/finalize-release.sh");

const VALID_DIGEST = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const VALID_ACCOUNT = "123456789012";

function workflowStepRunBlock(content: string, stepName: string): string {
  const lines = content.split("\n");
  const stepIndex = lines.findIndex((line) => line.includes(`name: ${stepName}`));
  if (stepIndex < 0) throw new Error(`Workflow step not found: ${stepName}`);
  const runIndex = lines.findIndex((line, index) => index > stepIndex && line.trim() === "run: |");
  if (runIndex < 0) throw new Error(`Workflow step has no run block: ${stepName}`);
  const runIndent = lines[runIndex].length - lines[runIndex].trimStart().length;
  const body: string[] = [];
  for (const line of lines.slice(runIndex + 1)) {
    const indent = line.length - line.trimStart().length;
    if (line.trim() && indent <= runIndent) break;
    body.push(line.slice(Math.min(line.length, runIndent + 2)));
  }
  return body.join("\n");
}

describe("Task 8: GitHub Actions CI/CD Release & OIDC Security Tier", () => {
  describe("GitHub Actions Workflow Definition (.github/workflows/aws-demo.yml)", () => {
    it("workflow YAML file exists and is readable", () => {
      expect(existsSync(WORKFLOW_PATH)).toBe(true);
      const examplePath = resolve(WORKTREE_ROOT, "infra/environments/demo/foundation-outputs.example.json");
      const example = JSON.parse(readFileSync(examplePath, "utf8")) as Record<string, unknown>;
      const outputsPath = resolve(WORKTREE_ROOT, "infra/environments/demo/foundation-outputs.json");
      const outputs = JSON.parse(readFileSync(outputsPath, "utf8")) as Record<string, unknown>;
      const expectedKeys = [
        "account_id",
        "aws_region",
        "cloudfront_distribution_id",
        "ecr_repository_urls",
        "ecs_cluster_arn",
        "ecs_cluster_name",
        "frontend_bucket_name",
        "initial_task_definition_arns",
        "migration_task_security_group_id",
        "private_subnet_ids",
      ].sort();
      expect(Object.keys(example).sort()).toEqual(expectedKeys);
      expect(Object.keys(outputs).sort()).toEqual(expectedKeys);
      expect(example).not.toHaveProperty("approval_evidence");
      expect(JSON.stringify(example)).toContain("REPLACE_WITH_");
      expect(outputs.account_id).toBe("968198283143");
      expect(outputs.aws_region).toBe("us-west-2");
      expect(outputs).not.toHaveProperty("approval_evidence");
      expect(JSON.stringify(outputs)).not.toContain("REPLACE_WITH_");
    });

    it("enforces explicit workflow_dispatch trigger with required confirm_approval input", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("workflow_dispatch:");
      expect(content).toContain("confirm_approval:");
      expect(content).toContain("required: true");
      expect(content).not.toContain("push:");
      expect(content).not.toContain("pull_request:");
      expect(content.match(/include-hidden-files: true/g)).toHaveLength(4);
      expect(content).toContain("path: .dev/dist/\n          include-hidden-files: true");
      expect(content).toContain("path: .image-archives/\n          include-hidden-files: true");
      expect(content).toContain("path: .dev/.release-metadata/release-manifest.json\n          include-hidden-files: true");
      expect(content).toContain("path: .dev/.release-metadata/\n          include-hidden-files: true");
      expect(content).not.toContain("path: .dev/\n");
    });

    it("enforces develop branch and repository restriction guards on jobs", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("if: github.ref == 'refs/heads/develop' && github.repository == 'slabarcaf/flowcard-treasury'");
      expect(content).toContain("inputs.confirm_approval == true");
      expect(content).toContain("workflow_dispatch:");
      expect(content).not.toContain("pull_request:");
    });

    it("enforces release concurrency serialization with cancel-in-progress: false", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("concurrency:");
      expect(content).toContain("group: aws-demo-release");
      expect(content).toContain("cancel-in-progress: false");
    });

    it("enforces top-level read-only permissions and scopes id-token: write strictly to deploy-demo job", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toMatch(/permissions:\s+contents:\s+read/);

      const deploySection = content.split("deploy-demo:")[1] || "";
      expect(deploySection).not.toContain("environment: demo");
      expect(deploySection).toContain("id-token: write");

      const unprivilegedSection = content.split("deploy-demo:")[0] || "";
      expect(unprivilegedSection).not.toContain("id-token: write");
    });

    it("pins all 3rd-party GitHub Actions to immutable 40-character commit SHAs", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const usesLines = content.split("\n").filter((l) => l.trim().startsWith("uses:"));

      expect(usesLines.length).toBeGreaterThanOrEqual(8);
      for (const line of usesLines) {
        const actionRef = line.split("uses:")[1].trim().split(" ")[0];
        const [_actionName, pin] = actionRef.split("@");
        expect(pin).toBeDefined();
        expect(pin).toMatch(/^[a-f0-9]{40}$/);
      }
    });

    it("configures compatible toolchain versions: Terraform >= 1.10, Node >= 20.19, Go from go.mod", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toMatch(/terraform_version:\s*["']1\.10\.[0-9]+["']/);
      expect(content).toMatch(/node-version:\s*["']20\.19\.[0-9]+["']/);
      expect(content).toContain("go-version-file: services/company-onboarding/go.mod");
    });

    it("configures isolated disposable PostgreSQL 54329 services container with test_onboarding credentials and POLICY_ACCEPTANCE_DATABASE_URL", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("postgres:16-alpine");
      expect(content).toContain("POSTGRES_DB: flowcard_onboarding_test");
      expect(content).toContain("POSTGRES_USER: test_onboarding");
      expect(content).toContain("POSTGRES_PASSWORD: test_password");
      expect(content).toContain("54329:5432");
      expect(content).toContain("--health-cmd pg_isready");
      expect(content).toContain("POLICY_ACCEPTANCE_DATABASE_URL: postgres://test_onboarding:test_password@localhost:54329/flowcard_onboarding_test?sslmode=disable");
      expect(content).not.toContain("flowcard_test");
      expect(content).not.toContain("TEST_DATABASE_URL");
      expect(content).not.toContain("ONBOARDING_TEST_DATABASE_URL");
      expect(content).not.toMatch(/(?<!POLICY_ACCEPTANCE_)DATABASE_URL:/);
    });

    it("includes OpenAPI specification freshness check gate", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("go run ./cmd/openapi -check");
    });

    it("enforces recursive infrastructure formatting check across all infra scopes", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("terraform fmt -check -recursive infra");
    });

    it("pins immutable commit SHA (github.sha) across checkout steps in all jobs", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const checkoutBlocks = content.split("actions/checkout@").slice(1);
      for (const block of checkoutBlocks) {
        expect(block).toContain("ref: ${{ github.sha }}");
      }
    });

    it("builds container images explicitly for linux/amd64 (AWS Fargate X86_64) with faithful contexts", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("--platform linux/amd64");
      expect(content).toContain("-f Dockerfile .");
      expect(content).toContain("-f services/company-onboarding/Dockerfile services/company-onboarding");
    });

    it("uses official secure AWS ECR login with password stdin piping", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("aws ecr get-login-password");
      expect(content).toContain("docker login --username AWS --password-stdin");
    });

    it("verifies frontend artifact checksum against manifest before release mutation", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("EXPECTED_FRONTEND_CHECKSUM");
      expect(content).toContain("ACTUAL_FRONTEND_CHECKSUM");
      expect(content).toContain("sha256sum");
    });

    it("publishes release manifest and displays step summary with activation preconditions before deployment approval", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("release-manifest.json");
      expect(content).toContain("$GITHUB_STEP_SUMMARY");
      expect(content).toContain("manual_dispatch_confirmed");
      expect(content).toContain("activation_preconditions");
      expect(content).toContain("pending_manual_foundation");
    });

    it("saves container archives with sha256 checksums in unprivileged job and loads without rebuild in deploy job", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      // Build job saves archives
      expect(content).toContain("docker save \"flowcard-node-gateway:${COMMIT}\" | gzip > .image-archives/node-gateway.tar.gz");
      expect(content).toContain("docker save \"flowcard-go-runtime:${COMMIT}\" | gzip > .image-archives/go-runtime.tar.gz");
      expect(content).toContain("NODE_ARCHIVE_SHA=");
      expect(content).toContain("GO_ARCHIVE_SHA=");
      expect(content).toContain("name: container-images");

      // Deploy job downloads and loads archives without rebuild
      const deploySection = content.split("deploy-demo:")[1] || "";
      expect(deploySection).toContain("name: container-images");
      expect(deploySection).toContain("docker load -i .image-archives/node-gateway.tar.gz");
      expect(deploySection).toContain("docker load -i .image-archives/go-runtime.tar.gz");
      expect(deploySection).not.toContain("docker build");
    });

    it("enforces deterministic fail-closed activation gate before OIDC role assumption", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";

      // Gate step must appear BEFORE OIDC assumption step
      const gateIdx = deploySection.indexOf("Verify Activation Preconditions & Governance Gates (Fail Closed)");
      const oidcIdx = deploySection.indexOf("Assume AWS Deployer Role via GitHub OIDC");
      expect(gateIdx).toBeGreaterThan(0);
      expect(oidcIdx).toBeGreaterThan(gateIdx);
      const variablesIdx = deploySection.indexOf("Validate Repository Deployment Variables");
      expect(variablesIdx).toBeGreaterThan(gateIdx);
      expect(variablesIdx).toBeLessThan(oidcIdx);
      expect(deploySection).toContain('AWS_ACCOUNT_ID}" == "968198283143"');
      expect(deploySection).toContain('AWS_REGION}" == "us-west-2"');

      // Gate checks foundation review and runtime approval
      expect(deploySection).toContain("FOUNDATION_STATUS");
      expect(deploySection).toContain('if [[ "${FOUNDATION_STATUS}" != "verified" ]]');
      expect(deploySection).toContain('if [[ "${FOUNDATION_REVIEWED}" != "true" ]]');
      expect(deploySection).toContain('if [[ "${CONFIG_APPROVED}" != "true" ]]');
    });

    it("passes trusted foundation release outputs artifact to deploy-demo.sh and uploads recovery metadata on failure", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";
      expect(deploySection).toContain("--infra-outputs-file");
      expect(deploySection).toContain("foundation_outputs_artifact");
      expect(deploySection).toContain("Upload Release Recovery Metadata");
      expect(deploySection).toContain("if: always()");
      expect(deploySection).toContain("name: release-recovery-metadata");
    });

    it("downloads, validates, and merges foundation outputs artifact before the activation gate", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";

      const downloadStepIdx = deploySection.indexOf("Download, Validate & Merge Foundation Outputs Artifact (Fail Closed)");
      const gateIdx = deploySection.indexOf("Verify Activation Preconditions & Governance Gates (Fail Closed)");
      expect(downloadStepIdx).toBeGreaterThan(0);
      expect(gateIdx).toBeGreaterThan(downloadStepIdx);

      expect(deploySection).toContain("No foundation outputs artifact provided via inputs.foundation_outputs_artifact");
      expect(deploySection).toContain("Example fixture template");
      expect(deploySection).toContain("Foundation artifact checksum mismatch");
    });

    it("finalizes whole-release status only after frontend upload and invalidation complete", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";
      const frontendIdx = deploySection.indexOf("name: Execute Frontend Deployment (deploy-frontend.sh with OIDC)");
      const finalizeIdx = deploySection.indexOf("name: Finalize Successful Frontend and Backend Release");
      const recoveryIdx = deploySection.indexOf("name: Upload Release Recovery Metadata (Preserve State On Failure)");
      expect(frontendIdx).toBeGreaterThan(0);
      expect(finalizeIdx).toBeGreaterThan(frontendIdx);
      expect(recoveryIdx).toBeGreaterThan(finalizeIdx);
      expect(deploySection).toContain("infra/scripts/finalize-release.sh");
      expect(readFileSync(FINALIZE_RELEASE_SCRIPT, "utf8")).toContain('"backend_verified_frontend_pending"');
      expect(readFileSync(DEPLOY_FRONTEND_SCRIPT, "utf8")).toContain("wait invalidation-completed");
    });

    it("passes all user inputs through step env blocks, never raw expressions in run text", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";

      // Find all run: blocks in deploy section and verify no inputs.* expressions inside
      const runBlocks: string[] = [];
      let inRun = false;
      let runIndent = 0;
      for (const line of deploySection.split("\n")) {
        const stripped = line.trimStart();
        const indent = line.length - line.trimStart().length;
        if (stripped.startsWith("run: |")) {
          inRun = true;
          runIndent = indent;
          continue;
        }
        if (inRun) {
          if (indent <= runIndent && stripped.length > 0 && !stripped.startsWith("#")) {
            inRun = false;
          } else {
            runBlocks.push(line);
          }
        }
      }

      const runText = runBlocks.join("\n");
      // No inputs.* expressions should appear inside run: blocks
      expect(runText).not.toMatch(/\$\{\{[^}]*inputs\./);
      // No format() with inputs should appear inside run: blocks
      expect(runText).not.toMatch(/format\([^)]*inputs\./);
    });

    it("merges runtime_configuration.approved from trusted foundation artifact evidence", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";
      expect(deploySection).toContain("runtime_configuration.approved");
      expect(deploySection).toContain("approval_evidence.runtime_configuration.approved");
    });

    it("restricts the foundation artifact to the tracked repository path", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain('FOUNDATION_PATH}" != "infra/environments/demo/foundation-outputs.json"');
      expect(content).toContain("Foundation outputs must be tracked in the reviewed repository commit");
    });

    it("requires a valid 64-hex SHA-256 for foundation artifact checksum", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      expect(content).toContain("Foundation artifact SHA-256 checksum is required");
      expect(content).toContain("Foundation artifact SHA-256 must be a valid 64-hex digest");
    });

    it("executes foundation validation with hostile path input as literal data", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const runBlock = workflowStepRunBlock(content, "Download, Validate & Merge Foundation Outputs Artifact (Fail Closed)");
      const tmpDir = mkdtempSync(join(tmpdir(), "foundation-shell-input-"));
      const marker = join(tmpDir, "injected");
      try {
        mkdirSync(join(tmpDir, ".dev", ".release-metadata"), { recursive: true });
        writeFileSync(join(tmpDir, ".dev", ".release-metadata", "release-manifest.json"), "{}\n");
        const hostilePath = `$(touch ${marker})`;
        const result = spawnSync("bash", ["-c", runBlock], {
          cwd: tmpDir,
          encoding: "utf8",
          env: {
            ...process.env,
            FOUNDATION_PATH: hostilePath,
            FOUNDATION_SHA: "",
            PREV_MANIFEST: "",
            PREV_CHECKSUM: "",
            PREV_DIR: "",
            PREV_ARTIFACT_ID: "",
            AUTHORIZED_ACCOUNT: "",
            TARGET_REGION: "us-west-2",
            GITHUB_SHA: "a".repeat(40),
          },
        });
        expect(result.status).toBe(1);
        expect(existsSync(marker)).toBe(false);
        expect(result.stderr).toContain("trusted repository path");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("rejects a missing foundation payload and a missing mandatory checksum", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const runBlock = workflowStepRunBlock(content, "Download, Validate & Merge Foundation Outputs Artifact (Fail Closed)");
      const tmpDir = mkdtempSync(join(tmpdir(), "foundation-required-inputs-"));
      try {
        mkdirSync(join(tmpDir, ".dev", ".release-metadata"), { recursive: true });
        writeFileSync(join(tmpDir, ".dev", ".release-metadata", "release-manifest.json"), "{}\n");
        const baseEnv = {
          ...process.env,
          FOUNDATION_PATH: "infra/environments/demo/foundation-outputs.json",
          FOUNDATION_SHA: "",
          PREV_MANIFEST: "",
          PREV_CHECKSUM: "",
          PREV_DIR: "",
          PREV_ARTIFACT_ID: "",
          AUTHORIZED_ACCOUNT: "",
          TARGET_REGION: "us-west-2",
          GITHUB_SHA: "a".repeat(40),
        };
        const missingPayload = spawnSync("bash", ["-c", runBlock], { cwd: tmpDir, encoding: "utf8", env: baseEnv });
        expect(missingPayload.status).toBe(1);
        expect(missingPayload.stderr).toContain("file does not exist");

        mkdirSync(join(tmpDir, "infra", "environments", "demo"), { recursive: true });
        writeFileSync(join(tmpDir, "infra", "environments", "demo", "foundation-outputs.json"), "{}\n");
        const missingChecksum = spawnSync("bash", ["-c", runBlock], { cwd: tmpDir, encoding: "utf8", env: baseEnv });
        expect(missingChecksum.status).toBe(1);
        expect(missingChecksum.stderr).toContain("SHA-256 checksum is required");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("accepts ancestor-bound foundation evidence without obsolete environment attestation", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const runBlock = workflowStepRunBlock(content, "Download, Validate & Merge Foundation Outputs Artifact (Fail Closed)");
      const gateBlock = workflowStepRunBlock(content, "Verify Activation Preconditions & Governance Gates (Fail Closed)");
      const tmpDir = mkdtempSync(join(tmpdir(), "foundation-evidence-"));
      const manifestPath = join(tmpDir, ".dev", ".release-metadata", "release-manifest.json");
      const foundationDir = join(tmpDir, "infra", "environments", "demo");
      const artifact = join(foundationDir, "foundation-outputs.json");
      const initialManifest = () => JSON.stringify({
        foundation_manifest: { status: "pending_manual_foundation" },
        activation_preconditions: { develop_branch_dispatch: false, manual_dispatch_confirmed: false, foundation_manifest_reviewed: false },
        runtime_configuration: { approved: false },
      });
      try {
        mkdirSync(foundationDir, { recursive: true });
        mkdirSync(join(tmpDir, ".dev", ".release-metadata"), { recursive: true });
        const foundationPlan = join(foundationDir, "foundation-plan-review.md");
        writeFileSync(foundationPlan, "offline-reviewed-plan\n");
        const planHash = spawnSync("sha256sum", [foundationPlan], { encoding: "utf8" }).stdout.split(" ")[0];
        const initResult = spawnSync("git", ["init", "--quiet"], { cwd: tmpDir, encoding: "utf8" });
        expect(initResult.status).toBe(0);
        expect(spawnSync("git", ["config", "user.email", "offline-test@example.invalid"], { cwd: tmpDir }).status).toBe(0);
        expect(spawnSync("git", ["config", "user.name", "Offline Test"], { cwd: tmpDir }).status).toBe(0);
        writeFileSync(manifestPath, initialManifest());
        const addSourceResult = spawnSync("git", ["add", "-f", "infra/environments/demo/foundation-plan-review.md", ".dev/.release-metadata/release-manifest.json"], { cwd: tmpDir, encoding: "utf8" });
        expect(addSourceResult.status).toBe(0);
        const sourceCommitResult = spawnSync("git", ["-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "review foundation plan"], { cwd: tmpDir, encoding: "utf8" });
        expect(sourceCommitResult.status, sourceCommitResult.stderr).toBe(0);
        const sourceCommit = spawnSync("git", ["rev-parse", "HEAD"], { cwd: tmpDir, encoding: "utf8" }).stdout.trim();

        const reviewedOutputs = {
          account_id: VALID_ACCOUNT,
          aws_region: "us-west-2",
          ecs_cluster_arn: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:cluster/demo`,
          ecs_cluster_name: "demo",
          private_subnet_ids: ["subnet-0a00123456789abcd", "subnet-0b00123456789abcd"],
          migration_task_security_group_id: "sg-0a00123456789abcd",
          ecr_repository_urls: {
            "flowcard-node-gateway": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway`,
            "flowcard-go-api": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api`,
            "flowcard-go-worker": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker`,
            "flowcard-migration": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration`,
          },
          frontend_bucket_name: "flowcard-test-frontend",
          cloudfront_distribution_id: "EABC123",
          initial_task_definition_arns: {
            node_gateway: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-node-gateway:1`,
            go_api: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-api:1`,
            go_worker: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-worker:1`,
            node_migration: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-node-migration:1`,
            go_migration: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-migration:1`,
            synthetic_seed: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-synthetic-seed:1`,
          },
        };
        const canonicalContent = spawnSync("jq", ["-cS", "."], { input: JSON.stringify(reviewedOutputs), encoding: "utf8" }).stdout;
        const reviewedContentHash = spawnSync("sha256sum", [], { input: canonicalContent, encoding: "utf8" }).stdout.split(" ")[0];
        const artifactValue = {
          ...reviewedOutputs,
          approval_evidence: {
            status: "verified",
            verified_by: "alewkinr",
            reviewed_source_commit: sourceCommit,
            review_reference: "https://github.com/slabarcaf/flowcard-treasury/pull/17",
            reviewed_at: "2026-11-11T12:00:00Z",
            foundation_plan_sha256: planHash,
            reviewed_foundation_content_sha256: reviewedContentHash,
            runtime_configuration: {
              approved: true,
              approved_by: "owner@example.invalid",
              approval_reference: "decision-42",
              approved_at: "2026-11-11T12:00:00Z",
            },
          },
        };

        const releaseAHash = "e".repeat(64);
        const priorManifestPath = join(tmpDir, "prior-release.json");
        writeFileSync(priorManifestPath, JSON.stringify({
          status: "backend_verified_frontend_pending",
          account_id: VALID_ACCOUNT,
          region: "us-west-2",
          services: { node_gateway: { revision: 5 }, go_api: { revision: 4 }, go_worker: { revision: 4 } },
          frontend: { status: "pending", bucket_name: "flowcard-test-frontend", distribution_id: "EABC123", dist_checksum_sha256: releaseAHash },
          target_release: { commit_sha: "f".repeat(40), frontend_artifact_id: "release-A", frontend_checksum_sha256: releaseAHash },
        }));

        const runMerge = (priorRelease = false, targetOverrides: Record<string, string> = {}) => {
          writeFileSync(manifestPath, initialManifest());
          const artifactHash = spawnSync("sha256sum", [artifact], { encoding: "utf8" }).stdout.split(" ")[0];
          const deploymentCommitResult = spawnSync("git", ["rev-parse", "HEAD"], { cwd: tmpDir, encoding: "utf8" });
          return spawnSync("bash", ["-c", runBlock], {
            cwd: tmpDir,
            encoding: "utf8",
            env: {
              ...process.env,
              FOUNDATION_PATH: "infra/environments/demo/foundation-outputs.json",
              FOUNDATION_SHA: artifactHash,
              PREV_MANIFEST: priorRelease ? priorManifestPath : "",
              PREV_CHECKSUM: priorRelease ? releaseAHash : "",
              PREV_DIR: priorRelease ? "prior-frontend-build" : "",
              PREV_ARTIFACT_ID: priorRelease ? "release-A" : "",
              AUTHORIZED_ACCOUNT: VALID_ACCOUNT,
              TARGET_REGION: "us-west-2",
              TARGET_FRONTEND_BUCKET: "flowcard-test-frontend",
              TARGET_CLOUDFRONT_DISTRIBUTION_ID: "EABC123",
              GITHUB_SHA: deploymentCommitResult.stdout.trim(),
              ...targetOverrides,
            },
          });
        };

        const commitArtifact = (message: string) => {
          writeFileSync(artifact, JSON.stringify(artifactValue));
          spawnSync("git", ["add", "infra/environments/demo/foundation-outputs.json"], { cwd: tmpDir });
          const commitResult = spawnSync("git", ["-c", "commit.gpgsign=false", "commit", "--quiet", "-m", message], { cwd: tmpDir, encoding: "utf8" });
          expect(commitResult.status).toBe(0);
        };

        commitArtifact("add foundation output and runtime approval");
        const result = runMerge();
        expect(result.status).toBe(0);
        let merged = JSON.parse(readFileSync(manifestPath, "utf8"));
        expect(merged.foundation_manifest.status).toBe("verified");
        expect(merged.activation_preconditions.foundation_manifest_reviewed).toBe(true);
        expect(merged.activation_preconditions.develop_branch_dispatch).toBe(true);
        expect(merged.activation_preconditions.manual_dispatch_confirmed).toBe(true);
        expect(merged).not.toHaveProperty("activation_preconditions.github_environment_configured");
        expect(merged.foundation_manifest).not.toHaveProperty("github_environment_verification");
        expect(merged.runtime_configuration.approved).toBe(true);

        const mismatchedTarget = runMerge(false, { TARGET_FRONTEND_BUCKET: "other-test-frontend" });
        expect(mismatchedTarget.status).toBe(1);
        expect(mismatchedTarget.stderr).toContain("conflicts with repository deployment targets");

        const placeholderArtifact = {
          ...artifactValue,
          private_subnet_ids: ["subnet-REPLACE_WITH_PRIVATE_SUBNET_ID"],
        };
        writeFileSync(artifact, JSON.stringify(placeholderArtifact));
        const placeholderResult = runMerge();
        expect(placeholderResult.status).toBe(1);
        expect(placeholderResult.stderr).toContain("contains placeholders");

        const missingResourceField = {
          ...artifactValue,
          migration_task_security_group_id: undefined,
        };
        writeFileSync(artifact, JSON.stringify(missingResourceField));
        const missingFieldResult = runMerge();
        expect(missingFieldResult.status).toBe(1);
        expect(missingFieldResult.stderr).toContain("configuration is incomplete");

        writeFileSync(artifact, JSON.stringify(artifactValue));
        const restoredValidArtifact = runMerge();
        expect(restoredValidArtifact.status).toBe(0);
        merged = JSON.parse(readFileSync(manifestPath, "utf8"));

        const gateResult = spawnSync("bash", ["-c", gateBlock], { cwd: tmpDir, encoding: "utf8" });
        expect(gateResult.status).toBe(0);

        // Backend success without frontend completion must not make A a rollback target.
        const pendingMerge = runMerge(true);
        expect(pendingMerge.status).toBe(0);
        merged = JSON.parse(readFileSync(manifestPath, "utf8"));
        expect(merged.previous_release.rollback_available).toBe(false);
        expect(merged.previous_release.provenance_manifest_status).toBe("backend_verified_frontend_pending");

        const finalizeResult = spawnSync("bash", [FINALIZE_RELEASE_SCRIPT,
          "--manifest", priorManifestPath,
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--commit-sha", "f".repeat(40),
          "--frontend-checksum", releaseAHash,
        ], { cwd: tmpDir, encoding: "utf8" });
        expect(finalizeResult.status).toBe(0);
        const finalizedPrior = JSON.parse(readFileSync(priorManifestPath, "utf8"));
        expect(finalizedPrior.status).toBe("deployed_and_verified");
        expect(finalizedPrior.frontend.status).toBe("published_and_invalidation_completed");

        const finalizedMerge = runMerge(true);
        expect(finalizedMerge.status).toBe(0);
        merged = JSON.parse(readFileSync(manifestPath, "utf8"));
        expect(merged.previous_release.rollback_available).toBe(true);
        expect(merged.previous_release.frontend_checksum_sha256).toBe(releaseAHash);

        writeFileSync(manifestPath, JSON.stringify({
          foundation_manifest: { status: "verified" },
          activation_preconditions: { foundation_manifest_reviewed: true },
          runtime_configuration: { approved: false },
        }));
        const blockedGate = spawnSync("bash", ["-c", gateBlock], { cwd: tmpDir, encoding: "utf8" });
        expect(blockedGate.status).toBe(1);
        expect(blockedGate.stderr).toContain("Runtime configuration is not approved");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("passes hostile workflow inputs as literal deploy-demo arguments", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const runBlock = workflowStepRunBlock(content, "Execute Backend Deployment (deploy-demo.sh with OIDC)");
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-input-arguments-"));
      const scriptDir = join(tmpDir, "infra", "scripts");
      const capture = join(tmpDir, "args.txt");
      const marker = join(tmpDir, "injected");
      try {
        mkdirSync(scriptDir, { recursive: true });
        writeFileSync(join(scriptDir, "deploy-demo.sh"), "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\n");
        chmodSync(join(scriptDir, "deploy-demo.sh"), 0o755);
        const hostile = `$(touch ${marker})`;
        const result = spawnSync("bash", ["-c", runBlock], {
          cwd: tmpDir,
          encoding: "utf8",
          env: {
            ...process.env,
            CAPTURE_ARGS: capture,
            FOUNDATION_PATH: hostile,
            PREV_MANIFEST: hostile,
            PREV_CHECKSUM: hostile,
            PREV_DIR: hostile,
            PREV_ARTIFACT_ID: hostile,
            AWS_ACCOUNT_ID: VALID_ACCOUNT,
            AWS_REGION: "us-west-2",
            FRONTEND_BUCKET: "demo-frontend",
            CLOUDFRONT_DIST_ID: "E1234567890ABC",
            FRONTEND_CHECKSUM: "c".repeat(64),
            NODE_DIGEST: "sha256:" + "a".repeat(64),
            API_DIGEST: "sha256:" + "b".repeat(64),
            WORKER_DIGEST: "sha256:" + "c".repeat(64),
            MIGRATION_DIGEST: "sha256:" + "d".repeat(64),
            GITHUB_RUN_ID: "123",
            GITHUB_RUN_ATTEMPT: "1",
          },
        });
        expect(result.status).toBe(0);
        expect(existsSync(marker)).toBe(false);
        const args = readFileSync(capture, "utf8");
        expect(args).toContain(`--prior-frontend-dir\n${hostile}\n`);
        expect(args).toContain(`--previous-manifest\n${hostile}\n`);
        expect(args).toContain(`--prior-frontend-checksum\n${hostile}\n`);
        expect(args).toContain(`--prior-frontend-artifact-id\n${hostile}\n`);
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("reads prior release provenance from deployed state (services/frontend), not previous_release of prior manifest", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";
      const mergeBlock = deploySection.split("Merging previous release manifest provenance")[1]?.split("elif")[0] || "";
      // Must read from services.node_gateway.revision, not previous_release.node_revision first
      expect(mergeBlock).toContain("$pm[0].services.node_gateway.revision");
      expect(mergeBlock).toContain("$pm[0].services.go_api.revision");
      expect(mergeBlock).toContain("$pm[0].services.go_worker.revision");
      // Previous release's own target is .frontend; never its previous_release target.
      expect(mergeBlock).toContain("$pm[0].frontend.dist_checksum_sha256");
      expect(mergeBlock).toContain('$pm[0].status == "deployed_and_verified"');
      expect(mergeBlock).toContain("target_release.frontend_artifact_id");
      expect(deploySection).toContain("PREV_MANIFEST_CHECKSUM");
      // Must NOT prefer previous_release for revisions
      expect(mergeBlock).not.toContain("$pm[0].previous_release.node_revision //");
    });

    it("uses jq --arg for shell variable interpolation instead of string embedding", () => {
      const content = readFileSync(WORKFLOW_PATH, "utf8");
      const deploySection = content.split("deploy-demo:")[1] || "";
      // The foundation path merge should use --arg fpath
      expect(deploySection).toContain("--arg fpath");
      expect(deploySection).toContain("--arg chk");
      expect(deploySection).toContain("--arg dir");
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
    it("deploy-demo.sh rejects execution when neither --profile nor --oidc is supplied", () => {
      const res = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8", env: { ...process.env, AWS_PROFILE: "" } });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("Either --profile <name> or --oidc must be specified");
    });

    it("deploy-demo.sh accepts --oidc flag and proceeds past credential argument validation", () => {
      const res = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--oidc",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });

      expect(res.stderr).not.toContain("Either --profile <name> or --oidc must be specified");
      expect(res.stderr).not.toContain("Unknown option");
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
      const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--confirm-approval",
        "--oidc",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890",
      ], { encoding: "utf8" });

      expect(res.stderr).not.toContain("--profile is required");
      expect(res.stderr).not.toContain("Unknown option");
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
      const res = spawnSync(ROLLBACK_SCRIPT, [
        "--confirm-approval",
        "--oidc",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
      ], { encoding: "utf8" });

      expect(res.stderr).not.toContain("Either --profile <name> or --oidc must be specified");
      expect(res.stderr).not.toContain("Unknown option");
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
        "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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
        "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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
        "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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

      const h1 = spawnSync(helper, [resolve(WORKTREE_ROOT, ".dev/dist")], { encoding: "utf8" }).stdout.trim();
      expect(h1).toMatch(/^[a-f0-9]{64}$/);

      // Clone dist contents to a completely different temporary root
      const tmpDir = mkdtempSync(join(tmpdir(), "frontend-hash-copy-"));
      try {
        spawnSync("cp", ["-r", `${resolve(WORKTREE_ROOT, ".dev/dist")}/.`, tmpDir]);
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
      const genuineChecksum = spawnSync(helper, [resolve(WORKTREE_ROOT, ".dev/dist")], { encoding: "utf8" }).stdout.trim();

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
          "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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
          "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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
          "--frontend-build-dir", resolve(WORKTREE_ROOT, ".dev/dist"),
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
