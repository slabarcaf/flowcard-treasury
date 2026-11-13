#!/usr/bin/env bash
# ==============================================================================
# FlowCard AWS Demonstration Deployment Script (Executable Release Automation)
# ==============================================================================
#
# STRICT OPERATIONAL BOUNDARIES:
# 1. NOT automatically invoked during offline planning or Task 6 execution.
# 2. Direct CLI use requires --confirm-approval; the workflow passes it only
#    after a manual dispatch from the protected develop branch.
# 3. Requires 12-digit AWS account ID (--account-id), --profile, and --region.
# 4. Verifies AWS caller identity against authorized account ID before any mutation.
# 5. Validates immutable sha256: image digests for all services (no 'latest'/tags).
# 6. Registers exact digest-bearing task definition revisions before any updates.
# 7. Sequences Node and Go migrations with exit code 0 verification BEFORE service updates.
# 8. Sequences optional synthetic fixture seed with exit code 0 verification.
# 9. Updates services with exact task definition ARNs and scales desired_count to target.
# 10. Awaits service stability and verifies running task definitions and instance counts.
# 11. Preserves persisted database records in RDS and documents in S3.
# ==============================================================================
set -euo pipefail

CLUSTER_NAME="flowcard-demo-cluster"
REGION="${AWS_REGION:-us-west-2}"
PROFILE="${AWS_PROFILE:-}"
USE_OIDC=false
AUTHORIZED_ACCOUNT_ID="${ALLOWED_ACCOUNT_ID:-}"
CONFIRM_APPROVAL=false
APPLY_SEED=false
DESIRED_COUNT=1
FLAG_FRONTEND_ARTIFACT_ID=""
FLAG_FRONTEND_CHECKSUM=""
FLAG_BUCKET_NAME=""
FLAG_DISTRIBUTION_ID=""

usage() {
  cat <<'USAGE_EOF'
Usage: deploy-demo.sh [options]

Required Options:
  --account-id <12-digits>         Authorized 12-digit AWS Account ID (regex: ^[0-9]{12}$)
  --region <region>                AWS Region (e.g. us-west-2)
  --node-digest <sha256:...>       Immutable SHA-256 digest for Node gateway
  --api-digest <sha256:...>        Immutable SHA-256 digest for Go API
  --worker-digest <sha256:...>     Immutable SHA-256 digest for Go Worker
  --migration-digest <sha256:...>  Immutable SHA-256 digest for Migration tasks
  --confirm-approval               Explicit human authorization flag to execute release

Authentication (Specify one):
  --profile <profile-name>         AWS CLI credentials profile name (named human / SSO identity)
  --oidc                           Use ambient trusted OIDC credentials (GitHub Actions CI/CD)

Optional Options:
  --desired-count <int>            Target running container count per service (default: 1)
  --seed                           Execute idempotent synthetic fixture seed after migrations
  --help                           Show this message
USAGE_EOF
  exit 1
}

# Parse command line options
NODE_DIGEST=""
API_DIGEST=""
WORKER_DIGEST=""
MIGRATION_DIGEST=""
FLAG_SUBNET_IDS=""
FLAG_MIGRATION_SG=""
FLAG_CLUSTER_ARN=""
FLAG_ECR_URLS=""
INFRA_OUTPUTS_FILE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --account-id)
      AUTHORIZED_ACCOUNT_ID="$2"
      shift 2
      ;;
    --profile)
      PROFILE="$2"
      shift 2
      ;;
    --oidc)
      USE_OIDC=true
      shift
      ;;
    --region)
      REGION="$2"
      shift 2
      ;;
    --node-digest)
      NODE_DIGEST="$2"
      shift 2
      ;;
    --api-digest)
      API_DIGEST="$2"
      shift 2
      ;;
    --worker-digest)
      WORKER_DIGEST="$2"
      shift 2
      ;;
    --migration-digest)
      MIGRATION_DIGEST="$2"
      shift 2
      ;;
    --subnet-ids)
      FLAG_SUBNET_IDS="$2"
      shift 2
      ;;
    --migration-sg)
      FLAG_MIGRATION_SG="$2"
      shift 2
      ;;
    --cluster-arn)
      FLAG_CLUSTER_ARN="$2"
      shift 2
      ;;
    --ecr-urls)
      FLAG_ECR_URLS="$2"
      shift 2
      ;;
    --infra-outputs-file)
      INFRA_OUTPUTS_FILE="$2"
      shift 2
      ;;
    --desired-count)
      DESIRED_COUNT="$2"
      shift 2
      ;;
    --confirm-approval)
      CONFIRM_APPROVAL=true
      shift
      ;;
    --frontend-artifact-id)
      FLAG_FRONTEND_ARTIFACT_ID="$2"
      shift 2
      ;;
    --frontend-checksum)
      FLAG_FRONTEND_CHECKSUM="$2"
      shift 2
      ;;
    --bucket)
      FLAG_BUCKET_NAME="$2"
      shift 2
      ;;
    --distribution-id)
      FLAG_DISTRIBUTION_ID="$2"
      shift 2
      ;;
    --seed)
      APPLY_SEED=true
      shift
      ;;
    --help)
      usage
      ;;
    *)
      echo "ERROR: Unknown option: $1" >&2
      usage
      ;;
  esac
done

# 1. Explicit Approval Gate Check
if [[ "${CONFIRM_APPROVAL}" != "true" ]]; then
  echo "ERROR: Deployment requires explicit confirmation via --confirm-approval flag." >&2
  exit 1
fi

# 2. Account ID, Profile & Region Validation (Required BEFORE any AWS mutation)
if [[ -z "${AUTHORIZED_ACCOUNT_ID}" ]]; then
  echo "ERROR: --account-id is required." >&2
  exit 1
fi
if [[ ! "${AUTHORIZED_ACCOUNT_ID}" =~ ^[0-9]{12}$ ]]; then
  echo "ERROR: --account-id must be a valid 12-digit AWS Account ID. Got: ${AUTHORIZED_ACCOUNT_ID}" >&2
  exit 1
fi

if [[ "${USE_OIDC}" != "true" && -z "${PROFILE}" ]]; then
  echo "ERROR: Either --profile <name> or --oidc must be specified." >&2
  exit 1
fi

if [[ -z "${REGION}" ]]; then
  echo "ERROR: --region is required." >&2
  exit 1
fi

if [[ ! "${DESIRED_COUNT}" =~ ^[1-9][0-9]*$ ]]; then
  echo "ERROR: --desired-count must be a positive integer >= 1. Got: ${DESIRED_COUNT}" >&2
  exit 1
fi

# Configure AWS CLI and Terraform execution environment
export AWS_REGION="${REGION}"
if [[ "${USE_OIDC}" == "true" ]]; then
  unset AWS_PROFILE 2>/dev/null || true
  AUTH_DESC="ambient OIDC identity"
else
  export AWS_PROFILE="${PROFILE}"
  AUTH_DESC="profile '${PROFILE}'"
fi
AWS_CMD=(aws)

# 3. Immutable Digest Format Validation
validate_digest() {
  local name="$1"
  local val="$2"
  if [[ -z "${val}" ]]; then
    echo "ERROR: ${name} is required." >&2
    exit 1
  fi
  if [[ "${val}" =~ ^latest$ || "${val}" =~ :latest$ ]]; then
    echo "ERROR: ${name} prohibits mutable tag 'latest'; immutable sha256: digest required." >&2
    exit 1
  fi
  if [[ ! "${val}" =~ ^sha256:[a-f0-9]{64}$ ]]; then
    echo "ERROR: ${name} must be a valid 64-character SHA-256 digest format (sha256:<hex>). Got: ${val}" >&2
    exit 1
  fi
}

validate_digest "NODE_DIGEST" "${NODE_DIGEST}"
validate_digest "API_DIGEST" "${API_DIGEST}"
validate_digest "WORKER_DIGEST" "${WORKER_DIGEST}"
validate_digest "MIGRATION_DIGEST" "${MIGRATION_DIGEST}"

# 4. AWS Caller Identity & Account Guard
echo "Verifying AWS authentication and caller identity (${AUTH_DESC}, region: ${REGION})..."
CALLER_JSON="$("${AWS_CMD[@]}" sts get-caller-identity --output json 2>/dev/null || echo "")"
CALLER_ACCOUNT="$(echo "${CALLER_JSON}" | jq -r '.Account // empty')"

if [[ -z "${CALLER_ACCOUNT}" ]]; then
  echo "ERROR: Failed to retrieve AWS caller account from STS identity check." >&2
  exit 1
fi

if [[ "${CALLER_ACCOUNT}" != "${AUTHORIZED_ACCOUNT_ID}" ]]; then
  echo "ERROR: AWS caller account (${CALLER_ACCOUNT}) does not match authorized account (${AUTHORIZED_ACCOUNT_ID}). Aborting." >&2
  exit 1
fi
echo "Verified AWS Account: ${CALLER_ACCOUNT} in region ${REGION}."

# 5. Fetch Infrastructure Outputs (Prioritize Validated Release Artifact / Flags over State)
SUBNET_IDS="${FLAG_SUBNET_IDS}"
MIGRATION_SG="${FLAG_MIGRATION_SG}"
ECR_URLS="${FLAG_ECR_URLS:-{}}"
CLUSTER_ARN="${FLAG_CLUSTER_ARN}"

if [[ -n "${INFRA_OUTPUTS_FILE}" && -f "${INFRA_OUTPUTS_FILE}" ]]; then
  echo "Loading infrastructure outputs from validated release artifact: ${INFRA_OUTPUTS_FILE}..."
  [[ -z "${SUBNET_IDS}" ]] && SUBNET_IDS="$(jq -r '.private_subnet_ids // empty | if type=="array" then join(",") else . end' "${INFRA_OUTPUTS_FILE}")"
  [[ -z "${MIGRATION_SG}" ]] && MIGRATION_SG="$(jq -r '.migration_task_security_group_id // empty' "${INFRA_OUTPUTS_FILE}")"
  [[ "${ECR_URLS}" == "{}" ]] && ECR_URLS="$(jq -c '.ecr_repository_urls // {}' "${INFRA_OUTPUTS_FILE}")"
  [[ -z "${CLUSTER_ARN}" ]] && CLUSTER_ARN="$(jq -r '.ecs_cluster_arn // empty' "${INFRA_OUTPUTS_FILE}")"
fi

# Fallback to local Terraform state only if outputs were not provided via artifact or flags
if [[ -z "${SUBNET_IDS}" || -z "${MIGRATION_SG}" || "${ECR_URLS}" == "{}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  DEMO_DIR="${SCRIPT_DIR}/../environments/demo"

  TERRAFORM_BIN=""
  if command -v terraform >/dev/null 2>&1; then
    TERRAFORM_BIN="terraform"
  elif command -v tofu >/dev/null 2>&1; then
    TERRAFORM_BIN="tofu"
  else
    echo "ERROR: Neither Terraform ('terraform') nor OpenTofu ('tofu') executable found in PATH to resolve state outputs." >&2
    exit 1
  fi

  [[ -z "${SUBNET_IDS}" ]] && SUBNET_IDS="$(cd "${DEMO_DIR}" && "${TERRAFORM_BIN}" output -json private_subnet_ids 2>/dev/null | jq -r 'join(",")' || echo "")"
  [[ -z "${MIGRATION_SG}" ]] && MIGRATION_SG="$(cd "${DEMO_DIR}" && "${TERRAFORM_BIN}" output -raw migration_task_security_group_id 2>/dev/null || echo "")"
  [[ "${ECR_URLS}" == "{}" ]] && ECR_URLS="$(cd "${DEMO_DIR}" && "${TERRAFORM_BIN}" output -json ecr_repository_urls 2>/dev/null || echo "{}")"
  [[ -z "${CLUSTER_ARN}" ]] && CLUSTER_ARN="$(cd "${DEMO_DIR}" && "${TERRAFORM_BIN}" output -raw ecs_cluster_arn 2>/dev/null || echo "")"
fi

if [[ -z "${SUBNET_IDS}" || -z "${MIGRATION_SG}" ]]; then
  echo "ERROR: Unable to resolve private subnets or migration security group from demo environment outputs." >&2
  exit 1
fi

# Verify subnets and security group format
if [[ ! "${SUBNET_IDS}" =~ ^subnet-[a-z0-9]+(,\s*subnet-[a-z0-9]+)*$ ]]; then
  echo "ERROR: Resolved invalid private subnet IDs format: ${SUBNET_IDS}" >&2
  exit 1
fi
if [[ ! "${MIGRATION_SG}" =~ ^sg-[a-z0-9]+$ ]]; then
  echo "ERROR: Resolved invalid migration security group format: ${MIGRATION_SG}" >&2
  exit 1
fi

# Verify cluster ARN belongs to authorized account and region if present in outputs
if [[ -n "${CLUSTER_ARN}" ]]; then
  EXPECTED_CLUSTER_PREFIX="arn:aws:ecs:${REGION}:${AUTHORIZED_ACCOUNT_ID}:cluster/"
  if [[ "${CLUSTER_ARN}" != "${EXPECTED_CLUSTER_PREFIX}"* ]]; then
    echo "ERROR: Cluster ARN '${CLUSTER_ARN}' does not belong to authorized account '${AUTHORIZED_ACCOUNT_ID}' in region '${REGION}'." >&2
    exit 1
  fi
fi

NODE_REPO="$(echo "${ECR_URLS}" | jq -r '.["flowcard-node-gateway"] // empty')"
API_REPO="$(echo "${ECR_URLS}" | jq -r '.["flowcard-go-api"] // empty')"
WORKER_REPO="$(echo "${ECR_URLS}" | jq -r '.["flowcard-go-worker"] // empty')"
MIGRATION_REPO="$(echo "${ECR_URLS}" | jq -r '.["flowcard-migration"] // empty')"

if [[ -z "${NODE_REPO}" || -z "${API_REPO}" || -z "${WORKER_REPO}" || -z "${MIGRATION_REPO}" ]]; then
  echo "ERROR: Missing one or more ECR repository URLs in terraform outputs." >&2
  exit 1
fi

# Validate that all returned ECR repositories strictly belong to the authorized account and region
EXPECTED_ECR_PREFIX="${AUTHORIZED_ACCOUNT_ID}.dkr.ecr.${REGION}.amazonaws.com/"
for repo in "${NODE_REPO}" "${API_REPO}" "${WORKER_REPO}" "${MIGRATION_REPO}"; do
  if [[ "${repo}" != "${EXPECTED_ECR_PREFIX}"* ]]; then
    echo "ERROR: ECR repository URL '${repo}' does not belong to authorized account '${AUTHORIZED_ACCOUNT_ID}' in region '${REGION}'. Aborting before mutations." >&2
    exit 1
  fi
done

NODE_IMAGE="${NODE_REPO}@${NODE_DIGEST}"
API_IMAGE="${API_REPO}@${API_DIGEST}"
WORKER_IMAGE="${WORKER_REPO}@${WORKER_DIGEST}"
MIGRATION_IMAGE="${MIGRATION_REPO}@${MIGRATION_DIGEST}"

NETWORK_CONFIG="awsvpcConfiguration={subnets=[${SUBNET_IDS}],securityGroups=[${MIGRATION_SG}],assignPublicIp=DISABLED}"

# 6.5 Initialize diagnostic deployment metadata before mutations.
mkdir -p .dev/.release-metadata

cat <<PRE_EOF > .dev/.release-metadata/deployed-release.json
{
  "schema_version": "1.0",
  "status": "backend_in_progress",
  "account_id": "${AUTHORIZED_ACCOUNT_ID}",
  "region": "${REGION}",
  "cluster_name": "${CLUSTER_NAME}",
  "snapshot_at": "$(date -u +'%Y-%m-%dT%H:%M:%SZ')",
  "target_release": {
    "commit_sha": "${GITHUB_SHA:-local-unverified}",
    "node_digest": "${NODE_DIGEST}",
    "api_digest": "${API_DIGEST}",
    "worker_digest": "${WORKER_DIGEST}",
    "migration_digest": "${MIGRATION_DIGEST}",
    "frontend_checksum_sha256": "${FLAG_FRONTEND_CHECKSUM}",
    "frontend_artifact_id": "${FLAG_FRONTEND_ARTIFACT_ID}",
    "bucket_name": "${FLAG_BUCKET_NAME}",
    "distribution_id": "${FLAG_DISTRIBUTION_ID}"
  }
}
PRE_EOF

# 7. Register Exact Digest-Bearing Task Definition Revisions
register_task_definition() {
  local family="$1"
  local container_name="$2"
  local new_image="$3"

  echo "Fetching active task definition for family: ${family}..." >&2
  local current_def
  current_def="$("${AWS_CMD[@]}" ecs describe-task-definition \
    --task-definition "${family}" \
    --output json 2>/dev/null || echo "")"

  if [[ -z "${current_def}" ]]; then
    echo "ERROR: Task definition family '${family}' not found. On initial bootstrap when enable_services=false, task definitions must first be pre-registered via Terraform apply with real image digests before deployment." >&2
    exit 1
  fi

  # Validate that roles in task definition belong to the authorized account
  local exec_role task_role
  exec_role="$(echo "${current_def}" | jq -r '.taskDefinition.executionRoleArn // empty')"
  task_role="$(echo "${current_def}" | jq -r '.taskDefinition.taskRoleArn // empty')"
  local expected_iam_prefix="arn:aws:iam::${AUTHORIZED_ACCOUNT_ID}:role/"

  if [[ -n "${exec_role}" && "${exec_role}" != "${expected_iam_prefix}"* ]]; then
    echo "ERROR: Task definition ${family} execution role (${exec_role}) does not belong to authorized account ${AUTHORIZED_ACCOUNT_ID}." >&2
    exit 1
  fi
  if [[ -n "${task_role}" && "${task_role}" != "${expected_iam_prefix}"* ]]; then
    echo "ERROR: Task definition ${family} task role (${task_role}) does not belong to authorized account ${AUTHORIZED_ACCOUNT_ID}." >&2
    exit 1
  fi

  # Assert that the expected container actually exists in the task definition
  local matching_containers
  matching_containers="$(echo "${current_def}" | jq --arg cname "${container_name}" '[.taskDefinition.containerDefinitions[] | select(.name == $cname)] | length')"
  if [[ "${matching_containers}" -eq 0 ]]; then
    echo "ERROR: Expected container '${container_name}' not found in task definition family '${family}'. Registration aborted." >&2
    exit 1
  fi

  local clean_def
  clean_def="$(echo "${current_def}" | jq \
    --arg cname "${container_name}" \
    --arg img "${new_image}" \
    '.taskDefinition |
     .containerDefinitions = [.containerDefinitions[] | if .name == $cname then .image = $img else . end] |
     del(.taskDefinitionArn, .revision, .status, .requiresAttributes, .compatibilities, .registeredAt, .registeredBy)')"

  echo "Registering new task definition revision for ${family} with image ${new_image}..." >&2
  local new_td_arn
  new_td_arn="$("${AWS_CMD[@]}" ecs register-task-definition \
    --cli-input-json "${clean_def}" \
    --output json | jq -r '.taskDefinition.taskDefinitionArn // empty')"

  if [[ -z "${new_td_arn}" ]]; then
    echo "ERROR: Failed to register new task definition revision for ${family}." >&2
    exit 1
  fi
  echo "${new_td_arn}"
}

echo "Registering task definitions with immutable sha256 digests..."
NODE_MIGRATE_TD_ARN="$(register_task_definition "flowcard-demo-node-migration" "node-migration" "${NODE_IMAGE}")"
GO_MIGRATE_TD_ARN="$(register_task_definition "flowcard-demo-go-migration" "go-migration" "${MIGRATION_IMAGE}")"
API_TD_ARN="$(register_task_definition "flowcard-demo-go-api" "go-api" "${API_IMAGE}")"
WORKER_TD_ARN="$(register_task_definition "flowcard-demo-go-worker" "go-worker" "${WORKER_IMAGE}")"
NODE_TD_ARN="$(register_task_definition "flowcard-demo-node-gateway" "node-gateway" "${NODE_IMAGE}")"

SEED_TD_ARN=""
if [[ "${APPLY_SEED}" == "true" ]]; then
  SEED_TD_ARN="$(register_task_definition "flowcard-demo-synthetic-seed" "node-seed" "${NODE_IMAGE}")"
fi

# Helper function to run one-off ECS task and verify exit code 0
run_one_off_task() {
  local task_arn_def="$1"
  local container_name="$2"
  echo "Running one-off ECS task: ${task_arn_def}..."

  local run_output
  run_output="$("${AWS_CMD[@]}" ecs run-task \
    --cluster "${CLUSTER_NAME}" \
    --task-definition "${task_arn_def}" \
    --launch-type FARGATE \
    --network-configuration "${NETWORK_CONFIG}" \
    --output json)"

  local launched_task_arn
  launched_task_arn="$(echo "${run_output}" | jq -r '.tasks[0].taskArn // empty')"
  if [[ -z "${launched_task_arn}" ]]; then
    echo "ERROR: Failed to launch task ${task_arn_def}. Output: ${run_output}" >&2
    exit 1
  fi
  echo "Launched task ARN: ${launched_task_arn}. Waiting for completion..."

  "${AWS_CMD[@]}" ecs wait tasks-stopped \
    --cluster "${CLUSTER_NAME}" \
    --tasks "${launched_task_arn}"

  local desc_output
  desc_output="$("${AWS_CMD[@]}" ecs describe-tasks \
    --cluster "${CLUSTER_NAME}" \
    --tasks "${launched_task_arn}" \
    --output json)"

  local exit_code
  exit_code="$(echo "${desc_output}" | jq -r --arg c "${container_name}" '([.tasks[0].containers[] | select(.name==$c)][0].exitCode // "none") | tostring')"
  local stop_reason
  stop_reason="$(echo "${desc_output}" | jq -r '.tasks[0].stoppedReason // "none"')"

  if [[ "${exit_code}" != "0" ]]; then
    echo "ERROR: Task ${task_arn_def} (${container_name}) failed with exit code '${exit_code}'. Reason: ${stop_reason}" >&2
    exit 1
  fi
  echo "Task ${task_arn_def} completed successfully (exit code 0)."
}

# 8. Step 1: Run Node Migrations (001-021)
run_one_off_task "${NODE_MIGRATE_TD_ARN}" "node-migration"

# 9. Step 2: Run Go Migrations (001-006)
run_one_off_task "${GO_MIGRATE_TD_ARN}" "go-migration"

# 10. Step 3 (Optional): Run Packaged Synthetic Fixture Seed Task
if [[ "${APPLY_SEED}" == "true" ]]; then
  echo "Applying idempotent synthetic demonstration fixtures..."
  run_one_off_task "${SEED_TD_ARN}" "node-seed"
  echo "Synthetic fixtures seed task completed cleanly."
fi

# 11. Step 4: Sequentially Update ECS Services with exact task definition ARNs and target desired_count
echo "Updating Go API service to ${API_TD_ARN} with desired_count ${DESIRED_COUNT}..."
"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "flowcard-demo-go-api" \
  --task-definition "${API_TD_ARN}" \
  --desired-count "${DESIRED_COUNT}" > /dev/null

echo "Updating Go Worker service to ${WORKER_TD_ARN} with desired_count ${DESIRED_COUNT}..."
"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "flowcard-demo-go-worker" \
  --task-definition "${WORKER_TD_ARN}" \
  --desired-count "${DESIRED_COUNT}" > /dev/null

echo "Updating Node Gateway service to ${NODE_TD_ARN} with desired_count ${DESIRED_COUNT}..."
"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "flowcard-demo-node-gateway" \
  --task-definition "${NODE_TD_ARN}" \
  --desired-count "${DESIRED_COUNT}" > /dev/null

# 12. Step 5: Await Service Stability
echo "Waiting for ECS services to stabilize..."
"${AWS_CMD[@]}" ecs wait services-stable \
  --cluster "${CLUSTER_NAME}" \
  --services "flowcard-demo-go-api" "flowcard-demo-go-worker" "flowcard-demo-node-gateway"

# 13. Step 6: Verify Actual Deployment Running Counts and Task Definitions
echo "Verifying actual service deployments and running container counts..."
DESC_SERVICES="$("${AWS_CMD[@]}" ecs describe-services \
  --cluster "${CLUSTER_NAME}" \
  --services "flowcard-demo-go-api" "flowcard-demo-go-worker" "flowcard-demo-node-gateway" \
  --output json)"

verify_service_deployment() {
  local sname="$1"
  local expected_td="$2"
  local svc_json
  svc_json="$(echo "${DESC_SERVICES}" | jq --arg s "${sname}" '.services[] | select(.serviceName==$s)')"

  local active_td
  active_td="$(echo "${svc_json}" | jq -r '.taskDefinition // empty')"
  local running_count
  running_count="$(echo "${svc_json}" | jq -r '.runningCount // 0')"

  if [[ "${active_td}" != "${expected_td}" ]]; then
    echo "ERROR: Service ${sname} active task definition (${active_td}) does not match deployed revision (${expected_td})." >&2
    exit 1
  fi

  if [[ "${running_count}" -lt "${DESIRED_COUNT}" ]]; then
    echo "ERROR: Service ${sname} running container count (${running_count}) is less than desired count (${DESIRED_COUNT})." >&2
    exit 1
  fi

  echo "Service ${sname}: verified active TD ${active_td} with ${running_count} running tasks."
}

verify_service_deployment "flowcard-demo-go-api" "${API_TD_ARN}"
verify_service_deployment "flowcard-demo-go-worker" "${WORKER_TD_ARN}"
verify_service_deployment "flowcard-demo-node-gateway" "${NODE_TD_ARN}"

cat <<FINAL_EOF > .dev/.release-metadata/deployed-release.json
{
  "schema_version": "1.0",
  "account_id": "${AUTHORIZED_ACCOUNT_ID}",
  "region": "${REGION}",
  "cluster_name": "${CLUSTER_NAME}",
  "deployed_at": "$(date -u +'%Y-%m-%dT%H:%M:%SZ')",
  "status": "backend_verified_frontend_pending",
  "services": {
    "node_gateway": {
      "service_name": "flowcard-demo-node-gateway",
      "task_definition_arn": "${NODE_TD_ARN}",
      "revision": ${NODE_TD_ARN##*:},
      "image_digest": "${NODE_DIGEST}"
    },
    "go_api": {
      "service_name": "flowcard-demo-go-api",
      "task_definition_arn": "${API_TD_ARN}",
      "revision": ${API_TD_ARN##*:},
      "image_digest": "${API_DIGEST}"
    },
    "go_worker": {
      "service_name": "flowcard-demo-go-worker",
      "task_definition_arn": "${WORKER_TD_ARN}",
      "revision": ${WORKER_TD_ARN##*:},
      "image_digest": "${WORKER_DIGEST}"
    },
    "node_migration": {
      "task_definition_arn": "${NODE_MIGRATE_TD_ARN}",
      "image_digest": "${NODE_DIGEST}"
    },
    "go_migration": {
      "task_definition_arn": "${GO_MIGRATE_TD_ARN}",
      "image_digest": "${MIGRATION_DIGEST}"
    }
  },
  "frontend": {
    "status": "pending",
    "bucket_name": "${FLAG_BUCKET_NAME}",
    "distribution_id": "${FLAG_DISTRIBUTION_ID}",
    "dist_checksum_sha256": "${FLAG_FRONTEND_CHECKSUM}"
  },
  "target_release": {
    "commit_sha": "${GITHUB_SHA:-local-unverified}",
    "node_digest": "${NODE_DIGEST}",
    "api_digest": "${API_DIGEST}",
    "worker_digest": "${WORKER_DIGEST}",
    "migration_digest": "${MIGRATION_DIGEST}",
    "frontend_checksum_sha256": "${FLAG_FRONTEND_CHECKSUM}",
    "frontend_artifact_id": "${FLAG_FRONTEND_ARTIFACT_ID}"
  }
}
FINAL_EOF

echo "========================================================================"
echo "Backend services are verified; frontend publication remains pending."
echo "Cluster: ${CLUSTER_NAME} (${REGION})"
echo "All 3 services run verified task definitions at desired count ${DESIRED_COUNT}."
echo "========================================================================"
