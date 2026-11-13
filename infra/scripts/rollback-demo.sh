#!/usr/bin/env bash
# ==============================================================================
# FlowCard AWS Demonstration Rollback Script (Executable Recovery Automation)
# ==============================================================================
#
# STRICT OPERATIONAL BOUNDARIES:
# 1. NOT automatically invoked during offline planning or Task 6 execution.
# 2. Reverts ECS service task definitions to designated previous stable revisions.
# 3. Preserves all persisted database records in RDS and all documents in S3.
# 4. Requires explicit human confirmation flag (--confirm-approval).
# 5. Requires 12-digit AWS account ID (--account-id), --profile, and --region.
# 6. Verifies AWS caller identity against authorized account ID before any mutation.
# 7. Validates numeric task definition revisions and exact service family names.
# 8. Verifies running task definitions and container stability after rollback.
# 9. Note: Rollback affects application containers; irreversible DDL migrations
#    cannot be automatically reversed.
# ==============================================================================
set -euo pipefail

CLUSTER_NAME="flowcard-demo-cluster"
REGION="${AWS_REGION:-us-west-2}"
PROFILE="${AWS_PROFILE:-}"
USE_OIDC=false
AUTHORIZED_ACCOUNT_ID="${ALLOWED_ACCOUNT_ID:-}"
CONFIRM_APPROVAL=false

NODE_SERVICE="flowcard-demo-node-gateway"
API_SERVICE="flowcard-demo-go-api"
WORKER_SERVICE="flowcard-demo-go-worker"

usage() {
  cat <<'USAGE_EOF'
Usage: rollback-demo.sh [options]

Required Options:
  --account-id <12-digits>     Authorized 12-digit AWS Account ID (regex: ^[0-9]{12}$)
  --region <region>            AWS Region (e.g. us-west-2)
  --node-revision <rev_num>    Target task definition revision number for Node gateway (digits only)
  --api-revision <rev_num>     Target task definition revision number for Go API (digits only)
  --worker-revision <rev_num>  Target task definition revision number for Go Worker (digits only)
  --confirm-approval           Explicit human authorization flag to execute rollback

Authentication (Specify one):
  --profile <profile-name>     AWS CLI credentials profile name (named human / SSO identity)
  --oidc                       Use ambient trusted OIDC credentials (GitHub Actions CI/CD)

Optional Options:
  --help                       Show this message
USAGE_EOF
  exit 1
}

NODE_REV=""
API_REV=""
WORKER_REV=""

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
    --node-revision)
      NODE_REV="$2"
      shift 2
      ;;
    --api-revision)
      API_REV="$2"
      shift 2
      ;;
    --worker-revision)
      WORKER_REV="$2"
      shift 2
      ;;
    --confirm-approval)
      CONFIRM_APPROVAL=true
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
  echo "ERROR: Rollback requires explicit confirmation via --confirm-approval flag." >&2
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

export AWS_REGION="${REGION}"
if [[ "${USE_OIDC}" == "true" ]]; then
  unset AWS_PROFILE 2>/dev/null || true
  AUTH_DESC="ambient OIDC identity"
else
  export AWS_PROFILE="${PROFILE}"
  AUTH_DESC="profile '${PROFILE}'"
fi
AWS_CMD=(aws)

# 3. Numeric Revision Format Validation
if [[ -z "${NODE_REV}" || -z "${API_REV}" || -z "${WORKER_REV}" ]]; then
  echo "ERROR: Must specify target revisions for all three services (--node-revision, --api-revision, --worker-revision)." >&2
  exit 1
fi

for pair in "NODE_REV:${NODE_REV}" "API_REV:${API_REV}" "WORKER_REV:${WORKER_REV}"; do
  name="${pair%%:*}"
  val="${pair##*:}"
  if [[ ! "${val}" =~ ^[0-9]+$ ]]; then
    echo "ERROR: ${name} must be a positive integer revision number. Got: ${val}" >&2
    exit 1
  fi
done

# 4. AWS Caller Identity & Account Guard
echo "Verifying AWS authentication and caller identity (${AUTH_DESC}, region: ${REGION})..."
CALLER_JSON="$("${AWS_CMD[@]}" sts get-caller-identity --output json)"
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

# 5. Service Reversion
echo "Initiating ECS rollback to target task revisions..."
echo "  Node Gateway: ${NODE_SERVICE}:${NODE_REV}"
echo "  Go API:       ${API_SERVICE}:${API_REV}"
echo "  Go Worker:    ${WORKER_SERVICE}:${WORKER_REV}"

# Verify target task definitions exist and belong to authorized account and region before mutations
for svc_pair in "${NODE_SERVICE}:${NODE_REV}" "${API_SERVICE}:${API_REV}" "${WORKER_SERVICE}:${WORKER_REV}"; do
  td_desc="$("${AWS_CMD[@]}" ecs describe-task-definition \
    --task-definition "${svc_pair}" \
    --output json)"
  td_arn="$(echo "${td_desc}" | jq -r '.taskDefinition.taskDefinitionArn // empty')"
  expected_td_prefix="arn:aws:ecs:${REGION}:${AUTHORIZED_ACCOUNT_ID}:task-definition/"
  if [[ "${td_arn}" != "${expected_td_prefix}"* ]]; then
    echo "ERROR: Target task definition revision '${svc_pair}' ARN (${td_arn}) does not belong to authorized account ${AUTHORIZED_ACCOUNT_ID} in region ${REGION}. Aborting." >&2
    exit 1
  fi
done

"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "${API_SERVICE}" \
  --task-definition "${API_SERVICE}:${API_REV}" > /dev/null

"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "${WORKER_SERVICE}" \
  --task-definition "${WORKER_SERVICE}:${WORKER_REV}" > /dev/null

"${AWS_CMD[@]}" ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "${NODE_SERVICE}" \
  --task-definition "${NODE_SERVICE}:${NODE_REV}" > /dev/null

# 6. Await Service Stability
echo "Waiting for services to stabilize at target revisions..."
"${AWS_CMD[@]}" ecs wait services-stable \
  --cluster "${CLUSTER_NAME}" \
  --services "${API_SERVICE}" "${WORKER_SERVICE}" "${NODE_SERVICE}"

# 7. Verification of Active Task Revisions
echo "Verifying active task revisions and container status..."
DESC_SERVICES="$("${AWS_CMD[@]}" ecs describe-services \
  --cluster "${CLUSTER_NAME}" \
  --services "${API_SERVICE}" "${WORKER_SERVICE}" "${NODE_SERVICE}" \
  --output json)"

verify_rollback_service() {
  local sname="$1"
  local target_rev="$2"
  local svc_json
  svc_json="$(echo "${DESC_SERVICES}" | jq --arg s "${sname}" '.services[] | select(.serviceName==$s)')"

  local active_td
  active_td="$(echo "${svc_json}" | jq -r '.taskDefinition // empty')"
  local running_count
  running_count="$(echo "${svc_json}" | jq -r '.runningCount // 0')"

  if [[ ! "${active_td}" =~ :${target_rev}$ ]]; then
    echo "ERROR: Service ${sname} active task definition (${active_td}) does not end in target revision :${target_rev}." >&2
    exit 1
  fi

  echo "Service ${sname}: successfully verified at revision ${target_rev} (${running_count} running tasks)."
}

verify_rollback_service "${API_SERVICE}" "${API_REV}"
verify_rollback_service "${WORKER_SERVICE}" "${WORKER_REV}"
verify_rollback_service "${NODE_SERVICE}" "${NODE_REV}"

echo "========================================================================"
echo "Rollback completed successfully. Persisted data in RDS and S3 preserved."
echo "========================================================================"
