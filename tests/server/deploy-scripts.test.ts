import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { resolve, join } from "node:path";
import { mkdtempSync, writeFileSync, chmodSync, rmSync, readFileSync, existsSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";

const WORKTREE_ROOT = resolve(__dirname, "../..");
const DEPLOY_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/deploy-demo.sh");
const ROLLBACK_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/rollback-demo.sh");
const FINALIZE_RELEASE_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/finalize-release.sh");

const VALID_DIGEST = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const VALID_ACCOUNT = "123456789012";

function finalizeRelease(manifestPath: string): ReturnType<typeof spawnSync> {
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  return spawnSync("bash", [FINALIZE_RELEASE_SCRIPT,
    "--manifest", manifestPath,
    "--account-id", manifest.account_id,
    "--region", manifest.region,
    "--commit-sha", manifest.target_release.commit_sha,
    "--frontend-checksum", manifest.frontend.dist_checksum_sha256,
  ], { encoding: "utf8" });
}

describe("Deployment and Rollback Automation Scripts (Stub CLI)", () => {
  describe("deploy-demo.sh argument and pre-flight validation", () => {
    it("fails closed when --confirm-approval is missing", () => {
      const res = spawnSync(DEPLOY_SCRIPT, [
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("requires explicit confirmation via --confirm-approval flag");

      for (const obsoleteOption of [
        "--previous-manifest",
        "--prior-frontend-checksum",
        "--prior-frontend-dir",
        "--prior-frontend-artifact-id",
      ]) {
        const obsolete = spawnSync(DEPLOY_SCRIPT, [obsoleteOption, "unused"], { encoding: "utf8" });
        expect(obsolete.status).toBe(1);
        expect(obsolete.stderr).toContain(`Unknown option: ${obsoleteOption}`);
      }
    });

    it("fails closed when --account-id is missing or not 12 digits", () => {
      const res1 = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });
      expect(res1.status).toBe(1);
      expect(res1.stderr).toContain("--account-id is required");

      const res2 = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--account-id", "not-12-digits",
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });
      expect(res2.status).toBe(1);
      expect(res2.stderr).toContain("must be a valid 12-digit AWS Account ID");
    });

    it("fails closed when neither --profile nor --oidc is specified", () => {
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

    it("fails closed when an image digest is mutable tag 'latest' or malformed", () => {
      const resLatest = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-digest", "latest",
        "--api-digest", VALID_DIGEST,
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });
      expect(resLatest.status).toBe(1);
      expect(resLatest.stderr).toContain("prohibits mutable tag 'latest'");

      const resShort = spawnSync(DEPLOY_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-digest", VALID_DIGEST,
        "--api-digest", "sha256:1234short",
        "--worker-digest", VALID_DIGEST,
        "--migration-digest", VALID_DIGEST,
      ], { encoding: "utf8" });
      expect(resShort.status).toBe(1);
      expect(resShort.stderr).toContain("must be a valid 64-character SHA-256 digest format");
    });

    it("fails closed before any mutation when STS caller identity does not match authorized account", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-mismatch-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "999999999999", "Arn": "arn:aws:iam::999999999999:user/demo"}'
  exit 0
fi
echo "Unexpected call: $*" >&2
exit 1
`);
        chmodSync(mockAws, 0o755);

        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
exit 0
`);
        chmodSync(mockTerraform, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("AWS caller account (999999999999) does not match authorized account (123456789012)");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed before any mutation when ECR repository URL belongs to mismatched account or region", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-ecr-mismatch-"));
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

        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
if [[ "$1" == "output" ]]; then
  if [[ "$2" == "-json" && "$3" == "private_subnet_ids" ]]; then
    echo '["subnet-11111111", "subnet-22222222"]'
    exit 0
  elif [[ "$2" == "-raw" && "$3" == "migration_task_security_group_id" ]]; then
    echo "sg-99999999"
    exit 0
  elif [[ "$2" == "-json" && "$3" == "ecr_repository_urls" ]]; then
    echo '{"flowcard-node-gateway":"999999999999.dkr.ecr.us-east-1.amazonaws.com/flowcard-node-gateway","flowcard-go-api":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api","flowcard-go-worker":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker","flowcard-migration":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration"}'
    exit 0
  fi
fi
exit 0
`);
        chmodSync(mockTerraform, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not belong to authorized account '123456789012' in region 'us-west-2'. Aborting before mutations");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed before registration when expected container name is missing in task definition", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-missing-container-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  # Returns container with mismatched name
  echo '{"taskDefinition": {"family": "fam", "executionRoleArn": "arn:aws:iam::123456789012:role/exec", "containerDefinitions": [{"name": "wrong-container-name", "image": "old"}]}}'
  exit 0
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
if [[ "$1" == "output" ]]; then
  if [[ "$2" == "-json" && "$3" == "private_subnet_ids" ]]; then
    echo '["subnet-11111111", "subnet-22222222"]'
    exit 0
  elif [[ "$2" == "-raw" && "$3" == "migration_task_security_group_id" ]]; then
    echo "sg-99999999"
    exit 0
  elif [[ "$2" == "-json" && "$3" == "ecr_repository_urls" ]]; then
    echo '{"flowcard-node-gateway":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway","flowcard-go-api":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api","flowcard-go-worker":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker","flowcard-migration":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration"}'
    exit 0
  fi
fi
exit 0
`);
        chmodSync(mockTerraform, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("Expected container 'node-migration' not found in task definition family");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("halts deployment immediately and stops service updates when a migration task fails", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-migration-fail-"));
      try {
        const logFile = join(tmpDir, "calls.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "$*" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  echo '{"taskDefinition": {"family": "fam", "executionRoleArn": "arn:aws:iam::123456789012:role/exec", "containerDefinitions": [{"name": "node-migration", "image": "old"}, {"name": "go-migration", "image": "old"}, {"name": "go-api", "image": "old"}, {"name": "go-worker", "image": "old"}, {"name": "node-gateway", "image": "old"}]}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "register-task-definition" ]]; then
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:123456789012:task-definition/mock:1"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "run-task" ]]; then
  echo '{"tasks": [{"taskArn": "arn:aws:ecs:us-west-2:123456789012:task/mock-task-id"}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-tasks" ]]; then
  # Migration task fails with non-zero exit code
  echo '{"tasks": [{"stoppedReason": "Essential container in task exited with code 1", "containers": [{"name": "node-migration", "exitCode": 1}]}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "update-service" ]]; then
  echo "CRITICAL DEFECT: update-service called after migration failure!" >&2
  exit 99
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
if [[ "$1" == "output" ]]; then
  if [[ "$2" == "-json" && "$3" == "private_subnet_ids" ]]; then
    echo '["subnet-11111111", "subnet-22222222"]'
    exit 0
  elif [[ "$2" == "-raw" && "$3" == "migration_task_security_group_id" ]]; then
    echo "sg-99999999"
    exit 0
  elif [[ "$2" == "-json" && "$3" == "ecr_repository_urls" ]]; then
    echo '{"flowcard-node-gateway":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway","flowcard-go-api":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api","flowcard-go-worker":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker","flowcard-migration":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration"}'
    exit 0
  fi
fi
exit 0
`);
        chmodSync(mockTerraform, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("failed with exit code '1'");
        const logContent = readFileSync(logFile, "utf8");
        expect(logContent).not.toContain("update-service");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("prioritizes terraform over tofu and binds AWS_PROFILE and AWS_REGION to outputs", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-tf-priority-"));
      try {
        const tfLog = join(tmpDir, "terraform.log");
        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
echo "TERRAFORM_CALLED PROFILE=$AWS_PROFILE REGION=$AWS_REGION ARGS=$*" >> "${tfLog}"
if [[ "$1" == "output" ]]; then
  if [[ "$2" == "-json" && "$3" == "private_subnet_ids" ]]; then
    echo '["subnet-11111111", "subnet-22222222"]'
    exit 0
  elif [[ "$2" == "-raw" && "$3" == "migration_task_security_group_id" ]]; then
    echo "sg-99999999"
    exit 0
  elif [[ "$2" == "-json" && "$3" == "ecr_repository_urls" ]]; then
    echo '{"flowcard-node-gateway":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway","flowcard-go-api":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api","flowcard-go-worker":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker","flowcard-migration":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration"}'
    exit 0
  fi
fi
exit 0
`);
        chmodSync(mockTerraform, 0o755);

        // Also create tofu binary to prove terraform was prioritized
        const mockTofu = join(tmpDir, "tofu");
        writeFileSync(mockTofu, `#!/usr/bin/env bash
echo "TOFU_CALLED" >> "${tfLog}"
exit 1
`);
        chmodSync(mockTofu, 0o755);

        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "custom-profile",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        const log = readFileSync(tfLog, "utf8");
        expect(log).toContain("TERRAFORM_CALLED");
        expect(log).toContain("PROFILE=custom-profile");
        expect(log).toContain("REGION=us-west-2");
        expect(log).not.toContain("TOFU_CALLED");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("deploy-demo.sh full release execution with mock AWS CLI", () => {
    it("executes task registration with digests, runs migrations, scales desired_count, and verifies deployment", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-full-"));
      try {
        const logFile = join(tmpDir, "calls.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "$*" >> "${logFile}"

if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  echo '{"taskDefinition": {"family": "fam", "executionRoleArn": "arn:aws:iam::123456789012:role/exec", "containerDefinitions": [{"name": "node-migration", "image": "old"}, {"name": "go-migration", "image": "old"}, {"name": "go-api", "image": "old"}, {"name": "go-worker", "image": "old"}, {"name": "node-gateway", "image": "old"}, {"name": "node-seed", "image": "old"}]}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "register-task-definition" ]]; then
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:123456789012:task-definition/mock:1"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "run-task" ]]; then
  echo '{"tasks": [{"taskArn": "arn:aws:ecs:us-west-2:123456789012:task/mock-task-id"}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-tasks" ]]; then
  echo '{"tasks": [{"containers": [{"name": "node-migration", "exitCode": 0}, {"name": "go-migration", "exitCode": 0}, {"name": "node-seed", "exitCode": 0}]}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "update-service" ]]; then
  SVC=""
  TD=""
  PREV=""
  for a in "$@"; do
    if [[ "$PREV" == "--service" ]]; then SVC="$a"; fi
    if [[ "$PREV" == "--task-definition" ]]; then TD="$a"; fi
    PREV="$a"
  done
  echo "$TD" > "${tmpDir}/active-\${SVC}"
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-services" ]]; then
  echo '{"services": [
    {"serviceName": "flowcard-demo-go-api", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/mock:1", "runningCount": 1},
    {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/mock:1", "runningCount": 1},
    {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/mock:1", "runningCount": 1}
  ]}'
  exit 0
fi
echo "Unexpected AWS CLI call: $*" >&2
exit 1
`);
        chmodSync(mockAws, 0o755);

        const mockTerraform = join(tmpDir, "terraform");
        writeFileSync(mockTerraform, `#!/usr/bin/env bash
if [[ "$1" == "output" ]]; then
  if [[ "$2" == "-json" && "$3" == "private_subnet_ids" ]]; then
    echo '["subnet-11111111", "subnet-22222222"]'
    exit 0
  elif [[ "$2" == "-raw" && "$3" == "migration_task_security_group_id" ]]; then
    echo "sg-99999999"
    exit 0
  elif [[ "$2" == "-json" && "$3" == "ecr_repository_urls" ]]; then
    echo '{"flowcard-node-gateway":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway","flowcard-go-api":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api","flowcard-go-worker":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker","flowcard-migration":"123456789012.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration"}'
    exit 0
  fi
fi
echo "Unexpected terraform call: $*" >&2
exit 1
`);
        chmodSync(mockTerraform, 0o755);

        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
          "--desired-count", "1",
          "--seed",
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(0);
        expect(res.stdout).toContain("Backend services are verified; frontend publication remains pending.");
        expect(res.stdout).toContain("All 3 services run verified task definitions at desired count 1.");
        const deployedRelease = JSON.parse(readFileSync(
          join(tmpDir, ".dev", ".release-metadata", "deployed-release.json"),
          "utf8",
        ));
        expect(deployedRelease.status).toBe("backend_verified_frontend_pending");
        expect(deployedRelease).not.toHaveProperty("previous_release");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("rollback-demo.sh argument and pre-flight validation", () => {
    it("fails closed when --confirm-approval is missing", () => {
      const res = spawnSync(ROLLBACK_SCRIPT, [
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("requires explicit confirmation via --confirm-approval flag");
    });

    it("fails closed when --account-id is missing or invalid", () => {
      const res = spawnSync(ROLLBACK_SCRIPT, [
        "--confirm-approval",
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-revision", "5",
        "--api-revision", "4",
        "--worker-revision", "4",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("--account-id is required");
    });

    it("fails closed when revisions are non-numeric", () => {
      const res = spawnSync(ROLLBACK_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--node-revision", "v5",
        "--api-revision", "4",
        "--worker-revision", "4",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("must be a positive integer revision number");
    });

    it("fails closed before any mutation when STS caller identity does not match authorized account", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-rollback-mismatch-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "999999999999"}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(ROLLBACK_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("AWS caller account (999999999999) does not match authorized account (123456789012)");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed before mutations when target revision ARN does not belong to authorized account", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-rollback-arn-mismatch-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  # Target task definition revision belongs to a different account
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:999999999999:task-definition/flowcard-demo-node-gateway:5"}}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(ROLLBACK_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not belong to authorized account 123456789012 in region us-west-2. Aborting");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("executes service reversion to exact revisions with mock AWS CLI", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "aws-mock-rollback-full-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:123456789012:task-definition/'"$4"'"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "update-service" ]]; then
  SVC=""
  TD=""
  PREV=""
  for a in "$@"; do
    if [[ "$PREV" == "--service" ]]; then SVC="$a"; fi
    if [[ "$PREV" == "--task-definition" ]]; then TD="$a"; fi
    PREV="$a"
  done
  echo "$TD" > "${tmpDir}/active-\${SVC}"
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-services" ]]; then
  echo '{"services": [
    {"serviceName": "flowcard-demo-go-api", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-go-api:4", "runningCount": 1},
    {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-go-worker:4", "runningCount": 1},
    {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-node-gateway:5", "runningCount": 1}
  ]}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(ROLLBACK_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--node-revision", "5",
          "--api-revision", "4",
          "--worker-revision", "4",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(0);
        expect(res.stdout).toContain("Rollback completed successfully. Persisted data in RDS and S3 preserved.");
        expect(res.stdout).toContain("successfully verified at revision 5");
        expect(res.stdout).toContain("successfully verified at revision 4");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("Two-Phase Release Finalization", () => {
    it("keeps backend-only releases pending and finalizes only matching frontend evidence", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "two-phase-release-"));
      const manifestPath = join(tmpDir, "deployed-release.json");
      const checksum = "a".repeat(64);
      const commit = "b".repeat(40);
      try {
        writeFileSync(manifestPath, JSON.stringify({
          status: "backend_verified_frontend_pending",
          account_id: VALID_ACCOUNT,
          region: "us-west-2",
          services: {
            node_gateway: { revision: 7 },
            go_api: { revision: 6 },
            go_worker: { revision: 6 },
          },
          frontend: {
            status: "pending",
            bucket_name: "flowcard-demo-frontend-assets",
            distribution_id: "E1234567890ABC",
            dist_checksum_sha256: checksum,
          },
          target_release: {
            commit_sha: commit,
            frontend_artifact_id: "release-123",
            frontend_checksum_sha256: checksum,
          },
        }));

        // A failed frontend step skips finalization; a mismatched finalizer also fails closed.
        const failedFrontendFinalizer = spawnSync("bash", [FINALIZE_RELEASE_SCRIPT,
          "--manifest", manifestPath,
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--commit-sha", commit,
          "--frontend-checksum", "c".repeat(64),
        ], { encoding: "utf8" });
        expect(failedFrontendFinalizer.status).toBe(1);
        expect(failedFrontendFinalizer.stderr).toContain("does not match this verified backend and frontend deployment");
        let manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
        expect(manifest.status).toBe("backend_verified_frontend_pending");
        expect(manifest.frontend.status).toBe("pending");

        const successfulFinalizer = spawnSync("bash", [FINALIZE_RELEASE_SCRIPT,
          "--manifest", manifestPath,
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--commit-sha", commit,
          "--frontend-checksum", checksum,
        ], { encoding: "utf8" });
        expect(successfulFinalizer.status).toBe(0);
        manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
        expect(manifest.status).toBe("deployed_and_verified");
        expect(manifest.frontend.status).toBe("published_and_invalidation_completed");
        expect(manifest.release_verified_at).toMatch(/^\d{4}-\d{2}-\d{2}T/);
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });
});
