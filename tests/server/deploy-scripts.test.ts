import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { resolve, join } from "node:path";
import { mkdtempSync, writeFileSync, chmodSync, rmSync, readFileSync, existsSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";

const WORKTREE_ROOT = resolve(__dirname, "../..");
const DEPLOY_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/deploy-demo.sh");
const ROLLBACK_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/rollback-demo.sh");
const ROLLBACK_COORDINATOR_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/rollback-release.sh");
const COMPUTE_HASH_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/compute-frontend-hash.sh");
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
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(0);
        expect(res.stdout).toContain("Backend services are verified; frontend publication remains pending.");
        expect(res.stdout).toContain("All 3 services run verified task definitions at desired count 1.");
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

  describe("Generator-to-Consumer Contract: deploy-demo.sh -> deployed-release.json -> rollback-release.sh", () => {
    it("executes deploy-demo.sh with stub AWS capturing deployed-release.json and rollback-release.sh faithfully consumes it", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "gen-consumer-integration-"));
      try {
        const logFile = join(tmpDir, "calls.log");

        // 1. Create Prior Release A frontend build
        const dirA = join(tmpDir, "frontend-release-A");
        mkdirSync(join(dirA, "assets"), { recursive: true });
        writeFileSync(join(dirA, "index.html"), "<html><body>Release A Frontend</body></html>");
        writeFileSync(join(dirA, "assets", "app.js"), "console.log('App A');");
        const hashA = spawnSync(COMPUTE_HASH_SCRIPT, [dirA], { encoding: "utf8" }).stdout.trim();
        expect(hashA).toMatch(/^[a-f0-9]{64}$/);

        // 2. Create Target Release B frontend build
        const dirB = join(tmpDir, "frontend-release-B");
        mkdirSync(join(dirB, "assets"), { recursive: true });
        writeFileSync(join(dirB, "index.html"), "<html><body>Release B Frontend Different</body></html>");
        writeFileSync(join(dirB, "assets", "app.js"), "console.log('App B');");
        const hashB = spawnSync(COMPUTE_HASH_SCRIPT, [dirB], { encoding: "utf8" }).stdout.trim();
        expect(hashB).toMatch(/^[a-f0-9]{64}$/);
        expect(hashA).not.toBe(hashB);

        // 3. Create Foundation Outputs file
        const foundationFile = join(tmpDir, "foundation-outputs.json");
        writeFileSync(foundationFile, JSON.stringify({
          account_id: VALID_ACCOUNT,
          aws_region: "us-west-2",
          ecs_cluster_arn: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:cluster/flowcard-demo-cluster`,
          ecs_cluster_name: "flowcard-demo-cluster",
          private_subnet_ids: ["subnet-11111111", "subnet-22222222"],
          migration_task_security_group_id: "sg-99999999",
          ecr_repository_urls: {
            "flowcard-node-gateway": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway`,
            "flowcard-go-api": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api`,
            "flowcard-go-worker": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker`,
            "flowcard-migration": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration`,
          },
          frontend_bucket_name: "flowcard-demo-frontend-assets",
          cloudfront_distribution_id: "E1234567890ABC"
        }));

        // 4. Mock AWS CLI to handle deploy-demo.sh and rollback-release.sh calls
        const priorManifest = join(tmpDir, "successful-release-A.json");
        writeFileSync(priorManifest, JSON.stringify({
          status: "deployed_and_verified",
          account_id: VALID_ACCOUNT,
          region: "us-west-2",
          target_release: { frontend_artifact_id: "release-A" },
          services: {
            node_gateway: { revision: 10 },
            go_api: { revision: 8 },
            go_worker: { revision: 8 },
          },
          frontend: {
            dist_checksum_sha256: hashA,
            bucket_name: "flowcard-demo-frontend-assets",
            distribution_id: "E1234567890ABC",
          },
        }));

        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "$*" >> "${logFile}"

if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "123456789012"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  echo '{"taskDefinition": {"family": "'"$4"'", "revision": 1, "taskDefinitionArn": "arn:aws:ecs:us-west-2:123456789012:task-definition/'"$4"':1", "executionRoleArn": "arn:aws:iam::123456789012:role/flowcard-demo-execution-role", "taskRoleArn": "arn:aws:iam::123456789012:role/flowcard-demo-task-role", "containerDefinitions": [{"name": "node-migration", "image": "old"}, {"name": "go-migration", "image": "old"}, {"name": "go-api", "image": "old"}, {"name": "go-worker", "image": "old"}, {"name": "node-gateway", "image": "old"}]}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "register-task-definition" ]]; then
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:123456789012:task-definition/new:99"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "run-task" ]]; then
  echo '{"tasks": [{"taskArn": "arn:aws:ecs:us-west-2:123456789012:task/task-1"}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-tasks" ]]; then
  echo '{"tasks": [{"containers": [{"name": "node-migration", "exitCode": 0}, {"name": "go-migration", "exitCode": 0}]}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "update-service" ]]; then
  echo "UPDATE_SERVICE: $*" >> "${logFile}"
  SVC_NAME=""
  TD_ARG=""
  PREV=""
  for a in "$@"; do
    if [[ "$PREV" == "--service" ]]; then
      SVC_NAME="$a"
    elif [[ "$PREV" == "--task-definition" ]]; then
      TD_ARG="$a"
    fi
    PREV="$a"
  done
  if [[ -n "$SVC_NAME" && -n "$TD_ARG" ]]; then
    echo "$TD_ARG" > "${tmpDir}/active-td-\${SVC_NAME}"
  fi
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-services" ]]; then
  if [[ -f "${tmpDir}/active-td-flowcard-demo-go-api" ]]; then
    echo '{"services": [
      {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "'"$(cat "${tmpDir}/active-td-flowcard-demo-node-gateway" 2>/dev/null || echo "arn:aws:ecs:us-west-2:123456789012:task-definition/new:99")"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-api", "taskDefinition": "'"$(cat "${tmpDir}/active-td-flowcard-demo-go-api" 2>/dev/null || echo "arn:aws:ecs:us-west-2:123456789012:task-definition/new:99")"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "'"$(cat "${tmpDir}/active-td-flowcard-demo-go-worker" 2>/dev/null || echo "arn:aws:ecs:us-west-2:123456789012:task-definition/new:99")"'", "runningCount": 1}
    ]}'
  else
    # Returns prior running services: node rev 10, api rev 8, worker rev 8
    echo '{"services": [
      {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-node-gateway:10", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-api", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-go-api:8", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "arn:aws:ecs:us-west-2:123456789012:task-definition/flowcard-demo-go-worker:8", "runningCount": 1}
    ]}'
  fi
  exit 0
elif [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  echo '{"Distribution": {"Id": "E1234567890ABC", "ARN": "arn:aws:cloudfront::123456789012:distribution/E1234567890ABC", "DistributionConfig": {"Origins": {"Items": [{"DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com", "OriginAccessControlId": "E9876543210ABC"}]}}}}'
  exit 0
elif [[ "$1" == "cloudfront" && "$2" == "create-invalidation" ]]; then
  echo "CREATE_INVALIDATION: $*" >> "${logFile}"
  echo '{"Invalidation": {"Id": "I11111"}}'
  exit 0
elif [[ "$1" == "s3api" && "$2" == "put-object" ]]; then
  echo "S3_OP: $*" >> "${logFile}"
  exit 0
fi
exit 0
`);
        chmodSync(mockAws, 0o755);

        // 5. Execute deploy-demo.sh to deploy B while recording prior A
        const deployRes = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--infra-outputs-file", foundationFile,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--frontend-checksum", hashB,
          "--frontend-artifact-id", "release-B",
          "--prior-frontend-checksum", hashA,
          "--prior-frontend-dir", dirA,
          "--prior-frontend-artifact-id", "release-A",
          "--previous-manifest", priorManifest,
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        if (deployRes.status !== 0) {
          console.error("DEPLOY_STDERR:", deployRes.stderr);
          console.log("DEPLOY_STDOUT:", deployRes.stdout);
        }
        expect(deployRes.status).toBe(0);

        // 6. Inspect generated .dev/.release-metadata/deployed-release.json
        const deployedReleaseFile = join(tmpDir, ".dev", ".release-metadata", "deployed-release.json");
        expect(existsSync(deployedReleaseFile)).toBe(true);

        const manifest = JSON.parse(readFileSync(deployedReleaseFile, "utf8"));
        expect(manifest.status).toBe("backend_verified_frontend_pending");
        expect(manifest.frontend.status).toBe("pending");
        expect(manifest.account_id).toBe(VALID_ACCOUNT);
        expect(manifest.region).toBe("us-west-2");
        expect(manifest.previous_release.rollback_available).toBe(true);
        expect(manifest.previous_release.node_revision).toBe(10);
        expect(manifest.previous_release.api_revision).toBe(8);
        expect(manifest.previous_release.worker_revision).toBe(8);
        expect(manifest.previous_release.frontend_checksum_sha256).toBe(hashA);
        expect(manifest.previous_release.frontend_build_dir).toBe(dirA);
        expect(manifest.previous_release.bucket_name).toBe("flowcard-demo-frontend-assets");
        expect(manifest.previous_release.distribution_id).toBe("E1234567890ABC");

        // 7. Test rollback-release.sh with WRONG frontend bytes (dirB): Must reject before any AWS update!
        const wrongFrontendRes = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", deployedReleaseFile,
          "--frontend-build-dir", dirB, // Mismatched bytes
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(wrongFrontendRes.status).toBe(1);
        expect(wrongFrontendRes.stderr).toContain("does not match expected prior checksum");
        expect(wrongFrontendRes.stderr).toContain("Aborting rollback before any mutations");

        // 8. Test rollback-release.sh with CROSS-ACCOUNT manifest: Must reject before update!
        const crossAccountManifest = join(tmpDir, "cross-account-manifest.json");
        writeFileSync(crossAccountManifest, JSON.stringify({
          ...manifest,
          account_id: "999999999999"
        }));

        const crossAccountRes = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", crossAccountManifest,
          "--frontend-build-dir", dirA,
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(crossAccountRes.status).toBe(1);
        expect(crossAccountRes.stderr).toContain("Cross-account rollback rejected");

        // 9. Clear log file and execute SUCCESSFUL rollback-release.sh with THAT exact generated manifest and dirA
        writeFileSync(logFile, ""); // Reset log

        const rollbackRes = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", deployedReleaseFile,
          "--frontend-build-dir", dirA,
        ], {
          encoding: "utf8",
          cwd: tmpDir,
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        if (rollbackRes.status !== 0) {
          console.error("ROLLBACK_STDERR:", rollbackRes.stderr);
          console.log("ROLLBACK_STDOUT:", rollbackRes.stdout);
        }
        expect(rollbackRes.status).toBe(0);
        expect(rollbackRes.stdout).toContain("Phase 1 SUCCESS: ECS services successfully reverted and verified at target revisions");
        expect(rollbackRes.stdout).toContain("Phase 2 SUCCESS: Frontend static assets restored and CloudFront invalidation submitted");
        expect(rollbackRes.stdout).toContain("COUPLED ROLLBACK COMPLETED SUCCESSFULLY");

        const logContent = readFileSync(logFile, "utf8");
        // Verify Phase 1 reverted to node rev 10, api rev 8, worker rev 8
        expect(logContent).toContain("update-service --cluster flowcard-demo-cluster --service flowcard-demo-node-gateway --task-definition flowcard-demo-node-gateway:10");
        expect(logContent).toContain("update-service --cluster flowcard-demo-cluster --service flowcard-demo-go-api --task-definition flowcard-demo-go-api:8");
        expect(logContent).toContain("update-service --cluster flowcard-demo-cluster --service flowcard-demo-go-worker --task-definition flowcard-demo-go-worker:8");
        // Verify Phase 2 restored frontend A
        expect(logContent).toContain("S3_OP: s3api put-object");
        expect(logContent).toContain("--bucket flowcard-demo-frontend-assets");
        expect(logContent).toContain("CREATE_INVALIDATION: cloudfront create-invalidation --distribution-id E1234567890ABC");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("Release Chain A -> B -> C: Provenance and Rollback Verification", () => {
    it("rollback of C targets B backend + B frontend, never A frontend", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "chain-abc-"));
      try {
        const logFile = join(tmpDir, "calls.log");

        // 1. Create three distinct frontend builds
        const dirA = join(tmpDir, "frontend-A");
        mkdirSync(join(dirA, "assets"), { recursive: true });
        writeFileSync(join(dirA, "index.html"), "<html>Release A</html>");
        writeFileSync(join(dirA, "assets", "a.js"), "console.log('A');");
        const hashA = spawnSync(COMPUTE_HASH_SCRIPT, [dirA], { encoding: "utf8" }).stdout.trim();

        const dirB = join(tmpDir, "frontend-B");
        mkdirSync(join(dirB, "assets"), { recursive: true });
        writeFileSync(join(dirB, "index.html"), "<html>Release B</html>");
        writeFileSync(join(dirB, "assets", "b.js"), "console.log('B');");
        const hashB = spawnSync(COMPUTE_HASH_SCRIPT, [dirB], { encoding: "utf8" }).stdout.trim();

        const dirC = join(tmpDir, "frontend-C");
        mkdirSync(join(dirC, "assets"), { recursive: true });
        writeFileSync(join(dirC, "index.html"), "<html>Release C</html>");
        writeFileSync(join(dirC, "assets", "c.js"), "console.log('C');");
        const hashC = spawnSync(COMPUTE_HASH_SCRIPT, [dirC], { encoding: "utf8" }).stdout.trim();

        expect(hashA).toMatch(/^[a-f0-9]{64}$/);
        expect(hashB).toMatch(/^[a-f0-9]{64}$/);
        expect(hashC).toMatch(/^[a-f0-9]{64}$/);
        expect(hashA).not.toBe(hashB);
        expect(hashB).not.toBe(hashC);
        expect(hashA).not.toBe(hashC);

        // 2. Foundation outputs
        const foundationFile = join(tmpDir, "foundation-outputs.json");
        writeFileSync(foundationFile, JSON.stringify({
          account_id: VALID_ACCOUNT,
          aws_region: "us-west-2",
          ecs_cluster_arn: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:cluster/flowcard-demo-cluster`,
          ecs_cluster_name: "flowcard-demo-cluster",
          private_subnet_ids: ["subnet-11111111", "subnet-22222222"],
          migration_task_security_group_id: "sg-99999999",
          ecr_repository_urls: {
            "flowcard-node-gateway": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway`,
            "flowcard-go-api": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api`,
            "flowcard-go-worker": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker`,
            "flowcard-migration": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration`,
          },
          frontend_bucket_name: "flowcard-demo-frontend-assets",
          cloudfront_distribution_id: "E1234567890ABC",
        }));

        // 3. Stateful mock AWS CLI tracking active service revisions
        const stateFile = join(tmpDir, "active-revisions.json");
        writeFileSync(stateFile, JSON.stringify({ node: 0, api: 0, worker: 0 }));

        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "$*" >> "${logFile}"

if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  FAMILY="$4"
  echo '{"taskDefinition": {"family": "'"$FAMILY"'", "revision": 1, "taskDefinitionArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/'"$FAMILY"':1", "executionRoleArn": "arn:aws:iam::${VALID_ACCOUNT}:role/exec", "taskRoleArn": "arn:aws:iam::${VALID_ACCOUNT}:role/task", "containerDefinitions": [{"name": "node-migration", "image": "old"}, {"name": "go-migration", "image": "old"}, {"name": "go-api", "image": "old"}, {"name": "go-worker", "image": "old"}, {"name": "node-gateway", "image": "old"}]}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "register-task-definition" ]]; then
  # Read current max revision and increment
  STATE="$(cat "${stateFile}")"
  NODE_REV="$(echo "$STATE" | jq -r '.node')"
  API_REV="$(echo "$STATE" | jq -r '.api')"
  WORKER_REV="$(echo "$STATE" | jq -r '.worker')"
  # Determine family from the exact --cli-input-json registration payload
  FAMILY=""
  PREV=""
  INPUT_JSON=""
  for a in "$@"; do
    if [[ "$PREV" == "--cli-input-json" ]]; then INPUT_JSON="$a"; fi
    PREV="$a"
  done
  FAMILY="$(echo "$INPUT_JSON" | jq -r '.family // empty')"
  NEW_REV=1
  if [[ "$FAMILY" == *"node-gateway"* ]]; then
    NEW_REV=$((NODE_REV + 1))
    jq --argjson r "$NEW_REV" '.node = $r' "${stateFile}" > "${stateFile}.tmp" && mv "${stateFile}.tmp" "${stateFile}"
  elif [[ "$FAMILY" == *"go-api"* ]]; then
    NEW_REV=$((API_REV + 1))
    jq --argjson r "$NEW_REV" '.api = $r' "${stateFile}" > "${stateFile}.tmp" && mv "${stateFile}.tmp" "${stateFile}"
  elif [[ "$FAMILY" == *"go-worker"* ]]; then
    NEW_REV=$((WORKER_REV + 1))
    jq --argjson r "$NEW_REV" '.worker = $r' "${stateFile}" > "${stateFile}.tmp" && mv "${stateFile}.tmp" "${stateFile}"
  else
    NEW_REV=1
  fi
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/'"$FAMILY"':'"$NEW_REV"'"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "run-task" ]]; then
  echo '{"tasks": [{"taskArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task/task-1"}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-tasks" ]]; then
  echo '{"tasks": [{"containers": [{"name": "node-migration", "exitCode": 0}, {"name": "go-migration", "exitCode": 0}]}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "update-service" ]]; then
  # Track which TD was set on each service
  SVC_NAME=""
  TD_ARG=""
  PREV=""
  for a in "$@"; do
    if [[ "$PREV" == "--service" ]]; then SVC_NAME="$a"; fi
    if [[ "$PREV" == "--task-definition" ]]; then TD_ARG="$a"; fi
    PREV="$a"
  done
  if [[ -n "$SVC_NAME" && -n "$TD_ARG" ]]; then
    echo "$TD_ARG" > "${tmpDir}/active-td-\${SVC_NAME}"
  fi
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-services" ]]; then
  STATE="$(cat "${stateFile}")"
  NODE_REV="$(echo "$STATE" | jq -r '.node')"
  API_REV="$(echo "$STATE" | jq -r '.api')"
  WORKER_REV="$(echo "$STATE" | jq -r '.worker')"
  # Filter by --services argument
  SVC=""
  SERVICE_COUNT=0
  IN_SERVICES=false
  for a in "$@"; do
    if [[ "$a" == "--services" ]]; then IN_SERVICES=true; continue; fi
    if [[ "\${IN_SERVICES}" == "true" ]]; then
      if [[ "$a" == -* ]]; then IN_SERVICES=false; else SVC="$a"; SERVICE_COUNT=$((SERVICE_COUNT + 1)); fi
    fi
  done
  ACTIVE_TD="$(cat "${tmpDir}/active-td-\${SVC}" 2>/dev/null || true)"
  if [[ "$SERVICE_COUNT" -eq 1 && -n "$ACTIVE_TD" ]]; then
    echo '{"services": [{"serviceName": "'"$SVC"'", "taskDefinition": "'"$ACTIVE_TD"'", "runningCount": 1}]}'
    exit 0
  fi
  if [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-node-gateway" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-node-gateway:'"$NODE_REV"'", "runningCount": 1}]}'
  elif [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-go-api" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-go-api", "taskDefinition": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-api:'"$API_REV"'", "runningCount": 1}]}'
  elif [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-go-worker" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-go-worker", "taskDefinition": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-worker:'"$WORKER_REV"'", "runningCount": 1}]}'
  else
    NODE_TD="$(cat "${tmpDir}/active-td-flowcard-demo-node-gateway" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-node-gateway:$NODE_REV")"
    API_TD="$(cat "${tmpDir}/active-td-flowcard-demo-go-api" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-api:$API_REV")"
    WORKER_TD="$(cat "${tmpDir}/active-td-flowcard-demo-go-worker" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-worker:$WORKER_REV")"
    echo '{"services": [
      {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "'"$NODE_TD"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-api", "taskDefinition": "'"$API_TD"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "'"$WORKER_TD"'", "runningCount": 1}
    ]}'
  fi
  exit 0
elif [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  echo '{"Distribution": {"Id": "E1234567890ABC", "ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890ABC", "DistributionConfig": {"Origins": {"Items": [{"DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com", "OriginAccessControlId": "E9876543210ABC"}]}}}}'
  exit 0
elif [[ "$1" == "cloudfront" && "$2" == "create-invalidation" ]]; then
  echo '{"Invalidation": {"Id": "I11111"}}'
  exit 0
elif [[ "$1" == "s3" ]]; then
  exit 0
fi
exit 0
`.replace(/\$\{VALID_ACCOUNT\}/g, VALID_ACCOUNT));
        chmodSync(mockAws, 0o755);

        const deployEnv = {
          ...process.env,
          PATH: `${tmpDir}:${process.env.PATH}`,
        };

        // === DEPLOY A: Initial deployment (no prior release) ===
        writeFileSync(stateFile, JSON.stringify({ node: 0, api: 0, worker: 0 }));
        writeFileSync(logFile, "");
        const deployA = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--infra-outputs-file", foundationFile,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--frontend-checksum", hashA,
          "--frontend-artifact-id", "release-A",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        if (deployA.status !== 0) {
          console.error("DEPLOY_A_STDERR:", deployA.stderr);
        }
        expect(deployA.status).toBe(0);

        const manifestA = join(tmpDir, ".dev", ".release-metadata", "deployed-release.json");
        expect(existsSync(manifestA)).toBe(true);
        const finalizedA = finalizeRelease(manifestA);
        expect(finalizedA.status).toBe(0);
        const mA = JSON.parse(readFileSync(manifestA, "utf8"));
        expect(mA.status).toBe("deployed_and_verified");
        // Release A has no prior release -> rollback_available: false
        expect(mA.previous_release.rollback_available).toBe(false);
        expect(mA.frontend.dist_checksum_sha256).toBe(hashA);
        // Save manifest A for next step
        const savedManifestA = join(tmpDir, "manifest-A.json");
        writeFileSync(savedManifestA, readFileSync(manifestA, "utf8"));

        // === DEPLOY B: Prior = A ===
        // Active backend is now Release A revisions (node:1, api:1, worker:1)
        // (already set by deploy A registration increments)
        writeFileSync(logFile, "");
        const deployB = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--infra-outputs-file", foundationFile,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--frontend-checksum", hashB,
          "--frontend-artifact-id", "release-B",
          "--prior-frontend-checksum", hashA,
          "--prior-frontend-dir", dirA,
          "--prior-frontend-artifact-id", "release-A",
          "--previous-manifest", savedManifestA,
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        if (deployB.status !== 0) {
          console.error("DEPLOY_B_STDERR:", deployB.stderr);
        }
        expect(deployB.status).toBe(0);

        const finalizedB = finalizeRelease(manifestA);
        expect(finalizedB.status).toBe(0);

        const mB = JSON.parse(readFileSync(manifestA, "utf8"));
        expect(mB.status).toBe("deployed_and_verified");
        // B's previous_release should record A's backend revisions
        expect(mB.previous_release.rollback_available).toBe(true);
        expect(mB.previous_release.frontend_checksum_sha256).toBe(hashA);
        expect(mB.previous_release.frontend_build_dir).toBe(dirA);
        expect(mB.frontend.dist_checksum_sha256).toBe(hashB);
        // B registered NEW revisions for its services
        const bNodeRev = mB.services.node_gateway.revision;
        const bApiRev = mB.services.go_api.revision;
        const bWorkerRev = mB.services.go_worker.revision;
        expect(bNodeRev).toBeGreaterThanOrEqual(2);

        // Save manifest B
        const savedManifestB = join(tmpDir, "manifest-B.json");
        writeFileSync(savedManifestB, readFileSync(manifestA, "utf8"));

        // === DEPLOY C: Prior = B ===
        writeFileSync(logFile, "");
        const deployC = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--infra-outputs-file", foundationFile,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--frontend-checksum", hashC,
          "--frontend-artifact-id", "release-C",
          "--prior-frontend-dir", dirB,
          "--prior-frontend-checksum", hashB,
          "--prior-frontend-artifact-id", "release-B",
          "--previous-manifest", savedManifestB,
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        if (deployC.status !== 0) {
          console.error("DEPLOY_C_STDERR:", deployC.stderr);
        }
        expect(deployC.status).toBe(0);

        const finalizedC = finalizeRelease(manifestA);
        expect(finalizedC.status).toBe(0);

        const mC = JSON.parse(readFileSync(manifestA, "utf8"));
        expect(mC.status).toBe("deployed_and_verified");
        // C's previous_release must have B's backend + B's frontend
        expect(mC.previous_release.rollback_available).toBe(true);
        expect(mC.previous_release.node_revision).toBe(bNodeRev);
        expect(mC.previous_release.api_revision).toBe(bApiRev);
        expect(mC.previous_release.worker_revision).toBe(bWorkerRev);
        // CRITICAL: C.previous_release.frontend must be B's, NOT A's!
        expect(mC.previous_release.frontend_checksum_sha256).toBe(hashB);
        expect(mC.previous_release.frontend_build_dir).toBe(dirB);
        expect(mC.frontend.dist_checksum_sha256).toBe(hashC);

        // === ROLLBACK C: Must use B's frontend (dirB), reject dirA ===
        const manifestC = join(tmpDir, "manifest-C.json");
        writeFileSync(manifestC, readFileSync(manifestA, "utf8"));

        // Rollback with dirA should FAIL (A's frontend != B's checksum)
        const rollbackWithA = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", manifestC,
          "--frontend-build-dir", dirA,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        expect(rollbackWithA.status).toBe(1);
        expect(rollbackWithA.stderr).toContain("does not match expected prior checksum");

        // Rollback with dirC should FAIL (C's frontend != B's checksum)
        const rollbackWithC = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", manifestC,
          "--frontend-build-dir", dirC,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        expect(rollbackWithC.status).toBe(1);
        expect(rollbackWithC.stderr).toContain("does not match expected prior checksum");

        // Rollback with dirB should SUCCEED
        writeFileSync(logFile, "");
        const rollbackWithB = spawnSync(ROLLBACK_COORDINATOR_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--manifest", manifestC,
          "--frontend-build-dir", dirB,
        ], { encoding: "utf8", cwd: tmpDir, env: deployEnv });

        if (rollbackWithB.status !== 0) {
          console.error("ROLLBACK_B_STDERR:", rollbackWithB.stderr);
        }
        expect(rollbackWithB.status).toBe(0);
        expect(rollbackWithB.stdout).toContain("COUPLED ROLLBACK COMPLETED SUCCESSFULLY");

        // Verify rollback targeted B's backend revisions
        const rollbackLog = readFileSync(logFile, "utf8");
        expect(rollbackLog).toContain(`flowcard-demo-node-gateway:${bNodeRev}`);
        expect(rollbackLog).toContain(`flowcard-demo-go-api:${bApiRev}`);
        expect(rollbackLog).toContain(`flowcard-demo-go-worker:${bWorkerRev}`);
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("does not mark a checksum-only arbitrary CLI target as a verified prior release", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "no-dir-rollback-"));
      try {
        const logFile = join(tmpDir, "calls.log");
        const foundationFile = join(tmpDir, "foundation-outputs.json");
        writeFileSync(foundationFile, JSON.stringify({
          account_id: VALID_ACCOUNT,
          aws_region: "us-west-2",
          ecs_cluster_arn: `arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:cluster/flowcard-demo-cluster`,
          ecs_cluster_name: "flowcard-demo-cluster",
          private_subnet_ids: ["subnet-11111111", "subnet-22222222"],
          migration_task_security_group_id: "sg-99999999",
          ecr_repository_urls: {
            "flowcard-node-gateway": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-node-gateway`,
            "flowcard-go-api": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-api`,
            "flowcard-go-worker": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-go-worker`,
            "flowcard-migration": `${VALID_ACCOUNT}.dkr.ecr.us-west-2.amazonaws.com/flowcard-migration`,
          },
          frontend_bucket_name: "flowcard-demo-frontend-assets",
          cloudfront_distribution_id: "E1234567890ABC",
        }));

        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "$*" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}"}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-task-definition" ]]; then
  echo '{"taskDefinition": {"family": "'"$4"'", "revision": 1, "taskDefinitionArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/'"$4"':1", "executionRoleArn": "arn:aws:iam::${VALID_ACCOUNT}:role/exec", "containerDefinitions": [{"name": "node-migration", "image": "old"}, {"name": "go-migration", "image": "old"}, {"name": "go-api", "image": "old"}, {"name": "go-worker", "image": "old"}, {"name": "node-gateway", "image": "old"}]}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "register-task-definition" ]]; then
  echo '{"taskDefinition": {"taskDefinitionArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/new:99"}}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "run-task" ]]; then
  echo '{"tasks": [{"taskArn": "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task/t1"}]}'
  exit 0
elif [[ "$1" == "ecs" && "$2" == "wait" ]]; then
  exit 0
elif [[ "$1" == "ecs" && "$2" == "describe-tasks" ]]; then
  echo '{"tasks": [{"containers": [{"name": "node-migration", "exitCode": 0}, {"name": "go-migration", "exitCode": 0}]}]}'
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
  SVC=""
  SERVICE_COUNT=0
  IN_SERVICES=false
  for a in "$@"; do
    if [[ "$a" == "--services" ]]; then IN_SERVICES=true; continue; fi
    if [[ "\${IN_SERVICES}" == "true" ]]; then
      if [[ "$a" == -* ]]; then IN_SERVICES=false; else SVC="$a"; SERVICE_COUNT=$((SERVICE_COUNT + 1)); fi
    fi
  done
  NODE_TD="$(cat "${tmpDir}/active-flowcard-demo-node-gateway" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-node-gateway:5")"
  API_TD="$(cat "${tmpDir}/active-flowcard-demo-go-api" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-api:4")"
  WORKER_TD="$(cat "${tmpDir}/active-flowcard-demo-go-worker" 2>/dev/null || echo "arn:aws:ecs:us-west-2:${VALID_ACCOUNT}:task-definition/flowcard-demo-go-worker:4")"
  if [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-node-gateway" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "'"$NODE_TD"'", "runningCount": 1}]}'
  elif [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-go-api" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-go-api", "taskDefinition": "'"$API_TD"'", "runningCount": 1}]}'
  elif [[ "$SERVICE_COUNT" -eq 1 && "$SVC" == "flowcard-demo-go-worker" ]]; then
    echo '{"services": [{"serviceName": "flowcard-demo-go-worker", "taskDefinition": "'"$WORKER_TD"'", "runningCount": 1}]}'
  else
    echo '{"services": [
      {"serviceName": "flowcard-demo-node-gateway", "taskDefinition": "'"$NODE_TD"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-api", "taskDefinition": "'"$API_TD"'", "runningCount": 1},
      {"serviceName": "flowcard-demo-go-worker", "taskDefinition": "'"$WORKER_TD"'", "runningCount": 1}
    ]}'
  fi
  exit 0
fi
exit 0
`.replace(/\$\{VALID_ACCOUNT\}/g, VALID_ACCOUNT));
        chmodSync(mockAws, 0o755);

        // Even complete CLI-supplied frontend fields are insufficient without a successful manifest.
        const res = spawnSync(DEPLOY_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--region", "us-west-2",
          "--oidc",
          "--infra-outputs-file", foundationFile,
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890ABC",
          "--frontend-checksum", "abcd" + "0".repeat(60),
          "--prior-frontend-checksum", "1111111111111111111111111111111111111111111111111111111111111111",
          "--prior-frontend-dir", tmpDir,
          "--prior-frontend-artifact-id", "unverified-release",
          "--node-digest", VALID_DIGEST,
          "--api-digest", VALID_DIGEST,
          "--worker-digest", VALID_DIGEST,
          "--migration-digest", VALID_DIGEST,
        ], { encoding: "utf8", cwd: tmpDir, env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` } });

        expect(res.status).toBe(0);
        const manifest = JSON.parse(readFileSync(join(tmpDir, ".dev", ".release-metadata", "deployed-release.json"), "utf8"));
        // Caller-supplied fields do not prove a successful prior release.
        expect(manifest.previous_release.rollback_available).toBe(false);
        expect(res.stderr).toContain("do not establish rollback provenance");
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
