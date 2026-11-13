#!/usr/bin/env bash
# ==============================================================================
# FlowCard AWS Demonstration Coupled Rollback Coordinator
# ==============================================================================
# STRICT OPERATIONAL BOUNDARIES:
# 1. Coordinates coupled reversion of BOTH backend ECS services and frontend S3/CloudFront.
# 2. Requires explicit human confirmation flag (--confirm-approval).
# 3. Requires authorized 12-digit AWS account ID and region.
# 4. Verifies AWS caller identity before initiating any service reversion.
# 5. Reverts ECS services to exact previous task definition revisions.
# 6. Restores frontend distribution to exact previous verified build.
# 7. Database schema migrations are NEVER automatically downgraded or reversed.
# 8. Both backend and frontend reversions must succeed; reports failure if either fails.
# ==============================================================================
set -euo pipefail

REGION="${AWS_REGION:-us-west-2}"
PROFILE="${AWS_PROFILE:-}"
USE_OIDC=false
AUTHORIZED_ACCOUNT_ID="${ALLOWED_ACCOUNT_ID:-}"
CONFIRM_APPROVAL=false

MANIFEST_FILE=""
NODE_REV=""
API_REV=""
WORKER_REV=""
FRONTEND_BUILD_DIR=""
PRIOR_FRONTEND_CHECKSUM=""
BUCKET_NAME=""
DISTRIBUTION_ID=""

usage() {
  cat <<'USAGE_EOF'
Usage: rollback-release.sh [options]

Required Options:
  --account-id <12-digits>         Authorized 12-digit AWS Account ID
  --region <region>                AWS Region (e.g. us-west-2)
  --confirm-approval               Explicit human authorization flag to execute rollback

Authentication (Specify one):
  --profile <profile-name>         AWS CLI credentials profile name
  --oidc                           Use ambient trusted OIDC credentials

Target Reversion Options (Specify either --manifest OR individual revisions):
  --manifest <path>                Path to release-manifest.json containing previous release targets
  --node-revision <int>            Target task definition revision number for Node gateway
  --api-revision <int>             Target task definition revision number for Go API
  --worker-revision <int>          Target task definition revision number for Go Worker
  --frontend-build-dir <dir>       Directory containing previous verified frontend distribution build
  --bucket <name>                  Frontend S3 bucket name
  --distribution-id <id>           CloudFront distribution ID
USAGE_EOF
  exit 1
}

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
    --manifest)
      MANIFEST_FILE="$2"
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
    --frontend-build-dir)
      FRONTEND_BUILD_DIR="$2"
      shift 2
      ;;
    --prior-frontend-checksum)
      PRIOR_FRONTEND_CHECKSUM="$2"
      shift 2
      ;;
    --bucket)
      BUCKET_NAME="$2"
      shift 2
      ;;
    --distribution-id)
      DISTRIBUTION_ID="$2"
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

# 1. Approval Gate
if [[ "${CONFIRM_APPROVAL}" != "true" ]]; then
  echo "ERROR: Rollback requires explicit confirmation via --confirm-approval flag." >&2
  exit 1
fi

# 2. Account ID & Profile validation
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

# Configure execution environment
export AWS_REGION="${REGION}"
if [[ "${USE_OIDC}" == "true" ]]; then
  unset AWS_PROFILE 2>/dev/null || true
  AUTH_FLAG="--oidc"
  AUTH_DESC="ambient OIDC identity"
else
  export AWS_PROFILE="${PROFILE}"
  AUTH_FLAG="--profile ${PROFILE}"
  AUTH_DESC="profile '${PROFILE}'"
fi

# 3. Resolve Targets from Manifest if supplied
if [[ -n "${MANIFEST_FILE}" && -f "${MANIFEST_FILE}" ]]; then
  echo "Loading previous release targets from manifest: ${MANIFEST_FILE}..."

  # Check if previous release indicates initial deployment (no rollback available)
  ROLLBACK_AVAIL="$(jq -r 'if .previous_release.rollback_available != null then (.previous_release.rollback_available | tostring) else "true" end' "${MANIFEST_FILE}")"
  STATUS="$(jq -r '.previous_release.status // empty' "${MANIFEST_FILE}")"
  if [[ "${ROLLBACK_AVAIL}" == "false" || "${STATUS}" == "initial_deployment_no_prior_release" ]]; then
    echo "ERROR: Previous release metadata indicates rollback is unavailable: initial deployment with no prior release." >&2
    exit 1
  fi

  # Validate manifest account and region against authorized target
  MANIFEST_ACCOUNT="$(jq -r '.account_id // empty' "${MANIFEST_FILE}")"
  if [[ -n "${MANIFEST_ACCOUNT}" && "${MANIFEST_ACCOUNT}" != "${AUTHORIZED_ACCOUNT_ID}" ]]; then
    echo "ERROR: Manifest account ID '${MANIFEST_ACCOUNT}' does not match authorized account ID '${AUTHORIZED_ACCOUNT_ID}'! Cross-account rollback rejected." >&2
    exit 1
  fi

  MANIFEST_REGION="$(jq -r '.region // empty' "${MANIFEST_FILE}")"
  if [[ -n "${MANIFEST_REGION}" && "${MANIFEST_REGION}" != "${REGION}" ]]; then
    echo "ERROR: Manifest region '${MANIFEST_REGION}' does not match target region '${REGION}'! Cross-region rollback rejected." >&2
    exit 1
  fi

  [[ -z "${NODE_REV}" ]] && NODE_REV="$(jq -r '.previous_release.node_revision // empty' "${MANIFEST_FILE}")"
  [[ -z "${API_REV}" ]] && API_REV="$(jq -r '.previous_release.api_revision // empty' "${MANIFEST_FILE}")"
  [[ -z "${WORKER_REV}" ]] && WORKER_REV="$(jq -r '.previous_release.worker_revision // empty' "${MANIFEST_FILE}")"
  [[ -z "${FRONTEND_BUILD_DIR}" ]] && FRONTEND_BUILD_DIR="$(jq -r '.previous_release.frontend_build_dir // empty' "${MANIFEST_FILE}")"
  [[ -z "${PRIOR_FRONTEND_CHECKSUM}" ]] && PRIOR_FRONTEND_CHECKSUM="$(jq -r '.previous_release.frontend_checksum_sha256 // empty' "${MANIFEST_FILE}")"
  [[ -z "${BUCKET_NAME}" ]] && BUCKET_NAME="$(jq -r '.previous_release.bucket_name // empty' "${MANIFEST_FILE}")"
  [[ -z "${DISTRIBUTION_ID}" ]] && DISTRIBUTION_ID="$(jq -r '.previous_release.distribution_id // empty' "${MANIFEST_FILE}")"
fi

# Validate target revisions
if [[ -z "${NODE_REV}" || -z "${API_REV}" || -z "${WORKER_REV}" ]]; then
  echo "ERROR: Target revisions required for all three services (--node-revision, --api-revision, --worker-revision or via valid --manifest)." >&2
  exit 1
fi

for pair in "NODE_REV:${NODE_REV}" "API_REV:${API_REV}" "WORKER_REV:${WORKER_REV}"; do
  name="${pair%%:*}"
  val="${pair##*:}"
  if [[ ! "${val}" =~ ^[1-9][0-9]*$ ]]; then
    echo "ERROR: ${name} must be a positive integer revision number >= 1. Got: ${val}" >&2
    exit 1
  fi
done

# 4. Pre-mutation Frontend Artifact & Checksum Verification (Coupled coordinator requires both phases)
if [[ -z "${FRONTEND_BUILD_DIR}" ]]; then
  echo "ERROR: Frontend build directory is required (--frontend-build-dir or via --manifest). Coupled rollback requires both backend and frontend restore." >&2
  exit 1
fi
if [[ ! -d "${FRONTEND_BUILD_DIR}" ]]; then
  echo "ERROR: Target frontend build directory does not exist: ${FRONTEND_BUILD_DIR}. Aborting before any mutations." >&2
  exit 1
fi

if [[ -z "${PRIOR_FRONTEND_CHECKSUM}" ]]; then
  echo "ERROR: Prior frontend checksum is required (--prior-frontend-checksum or via --manifest). Aborting before any mutations." >&2
  exit 1
fi

if [[ ! "${PRIOR_FRONTEND_CHECKSUM}" =~ ^[a-f0-9]{64}$ ]]; then
  echo "ERROR: Invalid SHA-256 checksum format. Got: ${PRIOR_FRONTEND_CHECKSUM}" >&2
  exit 1
fi

if [[ "${PRIOR_FRONTEND_CHECKSUM}" == "0000000000000000000000000000000000000000000000000000000000000000" ]]; then
  echo "ERROR: Fabricated zero-checksum (all zeros) rejected. Genuine verified checksum required. Aborting before any mutations." >&2
  exit 1
fi

if [[ -z "${BUCKET_NAME}" || -z "${DISTRIBUTION_ID}" ]]; then
  echo "ERROR: Frontend S3 bucket (--bucket) and CloudFront distribution ID (--distribution-id) are required for coupled rollback." >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPUTE_HASH_BIN="${SCRIPT_DIR}/compute-frontend-hash.sh"

echo "Verifying prior frontend artifact checksum before any mutations..."
ACTUAL_FRONTEND_CHECKSUM="$("${COMPUTE_HASH_BIN}" "${FRONTEND_BUILD_DIR}")"
if [[ "${ACTUAL_FRONTEND_CHECKSUM}" != "${PRIOR_FRONTEND_CHECKSUM}" ]]; then
  echo "ERROR: Target frontend directory checksum (${ACTUAL_FRONTEND_CHECKSUM}) does not match expected prior checksum (${PRIOR_FRONTEND_CHECKSUM}). Aborting rollback before any mutations!" >&2
  exit 1
fi
echo "Frontend build checksum verified before mutations (SHA-256: ${ACTUAL_FRONTEND_CHECKSUM})."

# 5. Caller Identity Verification
echo "Verifying AWS authentication and caller identity (${AUTH_DESC}, region: ${REGION})..."
CALLER_JSON="$(aws sts get-caller-identity --output json 2>/dev/null || echo "")"
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

# 6. Governance invariant announcement
echo "=============================================================================="
echo "FlowCard Demonstration Coupled Rollback Execution"
echo "=============================================================================="
echo "GOVERNANCE NOTICE: Database schema migrations are NEVER automatically downgraded"
echo "to prevent irreversible data corruption. Persisted database records are preserved."
echo "Target ECS Revisions:"
echo "  - flowcard-demo-node-gateway: revision ${NODE_REV}"
echo "  - flowcard-demo-go-api:       revision ${API_REV}"
echo "  - flowcard-demo-go-worker:    revision ${WORKER_REV}"
echo "=============================================================================="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_SUCCESS=false
FRONTEND_SUCCESS=false

# 7. Phase 1: Revert ECS Backend Services
echo "\n>>> Phase 1: Reverting ECS Services via rollback-demo.sh..."
ROLLBACK_ARGS=(
  --confirm-approval
  --account-id "${AUTHORIZED_ACCOUNT_ID}"
  --region "${REGION}"
  --node-revision "${NODE_REV}"
  --api-revision "${API_REV}"
  --worker-revision "${WORKER_REV}"
)
if [[ "${USE_OIDC}" == "true" ]]; then
  ROLLBACK_ARGS+=(--oidc)
else
  ROLLBACK_ARGS+=(--profile "${PROFILE}")
fi

if bash "${SCRIPT_DIR}/rollback-demo.sh" "${ROLLBACK_ARGS[@]}"; then
  BACKEND_SUCCESS=true
  echo ">>> Phase 1 SUCCESS: ECS services successfully reverted and verified at target revisions."
else
  echo "ERROR: Phase 1 FAILED: ECS service rollback failed. Aborting coupled rollback." >&2
  exit 1
fi

# 8. Phase 2: Restore Frontend Static Assets
echo "\n>>> Phase 2: Restoring Frontend Static Assets via deploy-frontend.sh..."
FRONTEND_ARGS=(
  --confirm-approval
  --account-id "${AUTHORIZED_ACCOUNT_ID}"
  --region "${REGION}"
  --build-dir "${FRONTEND_BUILD_DIR}"
  --expected-checksum "${PRIOR_FRONTEND_CHECKSUM}"
  --bucket "${BUCKET_NAME}"
  --distribution-id "${DISTRIBUTION_ID}"
)
if [[ "${USE_OIDC}" == "true" ]]; then
  FRONTEND_ARGS+=(--oidc)
else
  FRONTEND_ARGS+=(--profile "${PROFILE}")
fi

if bash "${SCRIPT_DIR}/deploy-frontend.sh" "${FRONTEND_ARGS[@]}"; then
  FRONTEND_SUCCESS=true
  echo ">>> Phase 2 SUCCESS: Frontend static assets restored and CloudFront invalidation submitted."
else
  echo "ERROR: Phase 2 FAILED: Frontend asset redeployment failed." >&2
  echo "CRITICAL STATE: Backend services were reverted (Phase 1 OK), but frontend failed (Phase 2 FAIL)." >&2
  exit 1
fi

# 9. Coupled Verification Summary
if [[ "${BACKEND_SUCCESS}" == "true" && "${FRONTEND_SUCCESS}" == "true" ]]; then
  echo "\n=============================================================================="
  echo "COUPLED ROLLBACK COMPLETED SUCCESSFULLY."
  echo "All backend ECS services and frontend assets are verified at target state."
  echo "Zero database migrations were reversed; zero persisted data was modified."
  echo "=============================================================================="
  exit 0
else
  echo "\nERROR: Coupled rollback failed to verify all targets." >&2
  exit 1
fi
