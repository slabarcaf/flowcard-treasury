#!/usr/bin/env bash
# Mark a release successful only after backend and frontend deployment steps pass.
set -euo pipefail

MANIFEST_FILE=""
ACCOUNT_ID=""
REGION=""
COMMIT_SHA=""
FRONTEND_CHECKSUM=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest) MANIFEST_FILE="$2"; shift 2 ;;
    --account-id) ACCOUNT_ID="$2"; shift 2 ;;
    --region) REGION="$2"; shift 2 ;;
    --commit-sha) COMMIT_SHA="$2"; shift 2 ;;
    --frontend-checksum) FRONTEND_CHECKSUM="$2"; shift 2 ;;
    *) echo "ERROR: Unknown option: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "${MANIFEST_FILE}" || ! -f "${MANIFEST_FILE}" ]]; then
  echo "ERROR: --manifest must name an existing release manifest." >&2
  exit 1
fi
if [[ ! "${ACCOUNT_ID}" =~ ^[0-9]{12}$ || -z "${REGION}" || -z "${COMMIT_SHA}" ]]; then
  echo "ERROR: Valid --account-id, --region, and --commit-sha are required." >&2
  exit 1
fi
if [[ ! "${FRONTEND_CHECKSUM}" =~ ^[a-f0-9]{64}$ ]]; then
  echo "ERROR: --frontend-checksum must be a 64-character lowercase SHA-256 digest." >&2
  exit 1
fi

STATUS="$(jq -r '.status // empty' "${MANIFEST_FILE}")"
MANIFEST_ACCOUNT="$(jq -r '.account_id // empty' "${MANIFEST_FILE}")"
MANIFEST_REGION="$(jq -r '.region // empty' "${MANIFEST_FILE}")"
MANIFEST_COMMIT="$(jq -r '.target_release.commit_sha // empty' "${MANIFEST_FILE}")"
MANIFEST_CHECKSUM="$(jq -r '.frontend.dist_checksum_sha256 // empty' "${MANIFEST_FILE}")"
TARGET_CHECKSUM="$(jq -r '.target_release.frontend_checksum_sha256 // empty' "${MANIFEST_FILE}")"
FRONTEND_STATUS="$(jq -r '.frontend.status // empty' "${MANIFEST_FILE}")"
FRONTEND_BUCKET="$(jq -r '.frontend.bucket_name // empty' "${MANIFEST_FILE}")"
DISTRIBUTION_ID="$(jq -r '.frontend.distribution_id // empty' "${MANIFEST_FILE}")"

if [[ "${STATUS}" != "backend_verified_frontend_pending" || \
      "${MANIFEST_ACCOUNT}" != "${ACCOUNT_ID}" || \
      "${MANIFEST_REGION}" != "${REGION}" || \
      "${MANIFEST_COMMIT}" != "${COMMIT_SHA}" || \
      "${MANIFEST_CHECKSUM}" != "${FRONTEND_CHECKSUM}" || \
      "${TARGET_CHECKSUM}" != "${FRONTEND_CHECKSUM}" || \
      "${FRONTEND_STATUS}" != "pending" || \
      -z "${FRONTEND_BUCKET}" || -z "${DISTRIBUTION_ID}" ]]; then
  echo "ERROR: Release manifest does not match this verified backend and frontend deployment." >&2
  exit 1
fi

TEMP_FILE="$(mktemp "${MANIFEST_FILE}.tmp.XXXXXX")"
trap 'rm -f "${TEMP_FILE}"' EXIT
jq --arg completed_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" '
  .status = "deployed_and_verified" |
  .frontend.status = "published_and_invalidation_completed" |
  .release_verified_at = $completed_at
' "${MANIFEST_FILE}" > "${TEMP_FILE}"
mv "${TEMP_FILE}" "${MANIFEST_FILE}"
trap - EXIT
echo "Release metadata finalized after frontend publication and invalidation completion."
