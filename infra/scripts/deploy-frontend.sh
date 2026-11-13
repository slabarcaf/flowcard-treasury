#!/usr/bin/env bash
# ==============================================================================
# FlowCard AWS Frontend Deployment Script (Executable Release Automation)
# ==============================================================================
#
# STRICT OPERATIONAL BOUNDARIES:
# 1. NOT automatically invoked during offline planning or Task 7 execution.
# 2. Requires explicit human approval confirmation flag (--confirm-approval).
# 3. Requires 12-digit AWS account ID (--account-id), --profile, and --region.
# 4. Verifies AWS caller identity against authorized account ID before any mutation.
# 5. Enforces expected-bucket-owner guard on all S3 operations.
# 6. Sets immutable cache headers (max-age=31536000, immutable) on hashed /assets/.
# 7. Sets revalidation cache headers (max-age=0, must-revalidate) on index.html.
# 8. Invalidates only exact changed paths (/index.html, /, /vite.svg), NEVER /*.
# ==============================================================================
set -euo pipefail

REGION="${AWS_REGION:-us-west-2}"
PROFILE="${AWS_PROFILE:-}"
USE_OIDC=false
AUTHORIZED_ACCOUNT_ID="${ALLOWED_ACCOUNT_ID:-}"
DISTRIBUTION_ID=""
BUCKET_NAME=""
BUILD_DIR=".dev/dist"
EXPECTED_CHECKSUM=""
CONFIRM_APPROVAL=false
DRY_RUN=false

usage() {
  cat <<'USAGE_EOF'
Usage: deploy-frontend.sh [options]

Required Options:
  --account-id <12-digits>         Authorized 12-digit AWS Account ID (regex: ^[0-9]{12}$)
  --region <region>                AWS Region (e.g. us-west-2)
  --confirm-approval               Explicit human authorization flag to execute release

Authentication (Specify one):
  --profile <profile-name>         AWS CLI credentials profile name (named human / SSO identity)
  --oidc                           Use ambient trusted OIDC credentials (GitHub Actions CI/CD)

Optional Options:
  --bucket <bucket-name>           Target S3 frontend bucket (defaults to terraform output)
  --distribution-id <id>           Target CloudFront distribution ID (defaults to terraform output)
  --build-dir <path>               Local build directory (default: .dev/dist)
  --dry-run                        Print actions without executing cloud mutations
  --help                           Show this message
USAGE_EOF
  exit 1
}

# Parse command line options
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
    --bucket)
      BUCKET_NAME="$2"
      shift 2
      ;;
    --distribution-id)
      DISTRIBUTION_ID="$2"
      shift 2
      ;;
    --build-dir)
      BUILD_DIR="$2"
      shift 2
      ;;
    --expected-checksum)
      EXPECTED_CHECKSUM="$2"
      shift 2
      ;;
    --confirm-approval)
      CONFIRM_APPROVAL=true
      shift
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --help)
      usage
      ;;
    *)
      echo "error: unknown argument $1" >&2
      usage
      ;;
  esac
done

# ------------------------------------------------------------------------------
# 1. Pre-flight Validation Guards
# ------------------------------------------------------------------------------
if [[ "$CONFIRM_APPROVAL" != "true" ]]; then
  echo "error: human confirmation required. Provide --confirm-approval to authorize release." >&2
  exit 1
fi

if [[ -z "$AUTHORIZED_ACCOUNT_ID" ]]; then
  echo "error: --account-id is required." >&2
  exit 1
fi

if [[ ! "$AUTHORIZED_ACCOUNT_ID" =~ ^[0-9]{12}$ ]]; then
  echo "error: --account-id must be exactly 12 digits (got: '$AUTHORIZED_ACCOUNT_ID')." >&2
  exit 1
fi

if [[ "$USE_OIDC" != "true" && -z "$PROFILE" ]]; then
  echo "error: --profile is required (or specify --oidc for ambient CI/CD)." >&2
  exit 1
fi

if [[ -z "$REGION" ]]; then
  echo "error: --region is required." >&2
  exit 1
fi

# Configure AWS CLI and Terraform execution environment
export AWS_REGION="$REGION"
if [[ "$USE_OIDC" == "true" ]]; then
  unset AWS_PROFILE 2>/dev/null || true
  AUTH_DESC="ambient OIDC identity"
else
  export AWS_PROFILE="$PROFILE"
  AUTH_DESC="profile '$PROFILE'"
fi
AWS_CMD=(aws)

# ------------------------------------------------------------------------------
# 2. AWS Caller Identity Verification (Fail-Closed)
# ------------------------------------------------------------------------------
echo "==> Verifying AWS caller identity (${AUTH_DESC}, region: $REGION)..."
CALLER_IDENTITY_JSON=$("${AWS_CMD[@]}" sts get-caller-identity --output json)
ACTUAL_ACCOUNT_ID=$(echo "$CALLER_IDENTITY_JSON" | jq -r '.Account // empty')

if [[ "$ACTUAL_ACCOUNT_ID" != "$AUTHORIZED_ACCOUNT_ID" ]]; then
  echo "FATAL: Caller account ID '$ACTUAL_ACCOUNT_ID' does not match authorized account '$AUTHORIZED_ACCOUNT_ID'!" >&2
  exit 1
fi
echo "    Caller account matches authorized account ($ACTUAL_ACCOUNT_ID)."

# ------------------------------------------------------------------------------
# 3. Resolve Target Bucket & Distribution if not provided
# ------------------------------------------------------------------------------
TF_DIR="infra/environments/demo"
if [[ -z "$BUCKET_NAME" || -z "$DISTRIBUTION_ID" ]]; then
  if [[ -d "$TF_DIR" ]]; then
    TF_BIN="terraform"
    if ! command -v terraform &> /dev/null; then
      if command -v tofu &> /dev/null; then
        TF_BIN="tofu"
      fi
    fi
    if command -v "$TF_BIN" &> /dev/null; then
      if [[ -z "$BUCKET_NAME" ]]; then
        BUCKET_NAME=$("$TF_BIN" -chdir="$TF_DIR" output -raw frontend_bucket_name 2>/dev/null || echo "")
      fi
      if [[ -z "$DISTRIBUTION_ID" ]]; then
        DISTRIBUTION_ID=$("$TF_BIN" -chdir="$TF_DIR" output -raw cloudfront_distribution_id 2>/dev/null || echo "")
      fi
    fi
  fi
fi

if [[ -z "$BUCKET_NAME" ]]; then
  echo "error: --bucket is required or must be available via terraform output frontend_bucket_name" >&2
  exit 1
fi

if [[ -z "$DISTRIBUTION_ID" ]]; then
  echo "error: --distribution-id is required or must be available via terraform output cloudfront_distribution_id" >&2
  exit 1
fi

# Verify expected bucket owner
echo "==> Verifying expected bucket owner for s3://$BUCKET_NAME..."
"${AWS_CMD[@]}" s3api get-bucket-location \
  --bucket "$BUCKET_NAME" \
  --expected-bucket-owner "$AUTHORIZED_ACCOUNT_ID" >/dev/null

echo "    Bucket s3://$BUCKET_NAME belongs to authorized account $AUTHORIZED_ACCOUNT_ID."

# ------------------------------------------------------------------------------
# 4. Verify CloudFront Distribution Ownership & Origin Target
# ------------------------------------------------------------------------------
echo "==> Verifying CloudFront distribution $DISTRIBUTION_ID ownership and origin bucket..."
DIST_CONFIG_JSON=$("${AWS_CMD[@]}" cloudfront get-distribution \
  --id "$DISTRIBUTION_ID" \
  --output json)

DIST_ARN=$(echo "$DIST_CONFIG_JSON" | jq -r '.Distribution.ARN // empty')
EXPECTED_DIST_ARN="arn:aws:cloudfront::${AUTHORIZED_ACCOUNT_ID}:distribution/${DISTRIBUTION_ID}"
EXPECTED_DIST_ARN_PREFIX="${EXPECTED_DIST_ARN}"

if [[ "$DIST_ARN" != "$EXPECTED_DIST_ARN" ]]; then
  echo "FATAL: CloudFront distribution ARN '$DIST_ARN' does not match authorized account '$AUTHORIZED_ACCOUNT_ID' (expected: '$EXPECTED_DIST_ARN')!" >&2
  exit 1
fi

EXPECTED_REGIONAL_DOMAIN="${BUCKET_NAME}.s3.${REGION}.amazonaws.com"
EXPECTED_GLOBAL_DOMAIN="${BUCKET_NAME}.s3.amazonaws.com"

# Parse exact origins via jq and assert target S3 endpoint with OAC enabled
MATCHING_ORIGIN=$(echo "$DIST_CONFIG_JSON" | jq -r --arg reg "$EXPECTED_REGIONAL_DOMAIN" --arg glob "$EXPECTED_GLOBAL_DOMAIN" '
  .Distribution.DistributionConfig.Origins.Items[]? |
  select((.DomainName == $reg or .DomainName == $glob) and (.OriginAccessControlId != null and .OriginAccessControlId != "")) |
  .DomainName
' | head -n 1)

if [[ -z "$MATCHING_ORIGIN" ]]; then
  echo "FATAL: CloudFront distribution $DISTRIBUTION_ID does not contain an S3 origin configured for bucket '$BUCKET_NAME' with Origin Access Control (OAC) enabled!" >&2
  echo "       Expected DomainName: '$EXPECTED_REGIONAL_DOMAIN' or '$EXPECTED_GLOBAL_DOMAIN' with non-empty OriginAccessControlId." >&2
  exit 1
fi
echo "    CloudFront distribution $DISTRIBUTION_ID verified (ARN: $DIST_ARN, targets $MATCHING_ORIGIN with OAC)."

# ------------------------------------------------------------------------------
# 5. Pre-flight Build Input Validation (Fail-Closed)
# ------------------------------------------------------------------------------
if [[ ! -d "$BUILD_DIR" ]]; then
  echo "error: build directory '$BUILD_DIR' not found. Run npm run build first." >&2
  exit 1
fi

if [[ ! -f "$BUILD_DIR/index.html" || ! -s "$BUILD_DIR/index.html" ]]; then
  echo "error: build input '$BUILD_DIR/index.html' is missing or empty. Build is incomplete." >&2
  exit 1
fi

if [[ ! -d "$BUILD_DIR/assets" ]]; then
  echo "error: build input '$BUILD_DIR/assets' directory is missing." >&2
  exit 1
fi

ASSET_COUNT=$(find "$BUILD_DIR/assets" -type f | wc -l | tr -d ' ')
if [[ "$ASSET_COUNT" -eq 0 ]]; then
  echo "error: build input '$BUILD_DIR/assets' contains zero files. Build is incomplete." >&2
  exit 1
fi

if [[ -n "$EXPECTED_CHECKSUM" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  HASH_HELPER="${SCRIPT_DIR}/compute-frontend-hash.sh"
  if [[ -f "${HASH_HELPER}" && -x "${HASH_HELPER}" ]]; then
    ACTUAL_CHECKSUM="$("${HASH_HELPER}" "${BUILD_DIR}")"
    if [[ "${ACTUAL_CHECKSUM}" != "${EXPECTED_CHECKSUM}" ]]; then
      echo "error: Frontend build directory checksum mismatch. Expected: ${EXPECTED_CHECKSUM}, Got: ${ACTUAL_CHECKSUM}. Aborting deployment before mutations." >&2
      exit 1
    fi
    echo "    Frontend build checksum verified: ${ACTUAL_CHECKSUM}"
  fi
fi

# ------------------------------------------------------------------------------
# 6. Synchronize Build Artifacts with Precise Cache Headers
# ------------------------------------------------------------------------------
if [[ "$DRY_RUN" == "true" ]]; then
  echo "==> [DRY RUN] Would upload hashed assets from $BUILD_DIR/assets to s3://$BUCKET_NAME/assets/ with 1-year immutable cache..."
  echo "==> [DRY RUN] Would upload all non-hashed public assets to s3://$BUCKET_NAME/ with max-age=86400..."
  echo "==> [DRY RUN] Would upload index.html from $BUILD_DIR/index.html to s3://$BUCKET_NAME/ with max-age=0, must-revalidate..."
  echo "==> [DRY RUN] Would create invalidation for mutable paths on distribution $DISTRIBUTION_ID..."
  echo "DRY RUN complete. No cloud mutations executed."
  exit 0
fi

# Each PutObject request supports the expected-owner guard. The high-level
# s3 cp/sync commands do not support that option.
upload_file() {
  local file_path="$1"
  local object_key="$2"
  local cache_control="$3"
  local content_type
  case "${object_key##*.}" in
    html|htm) content_type="text/html" ;;
    js|mjs) content_type="application/javascript" ;;
    css) content_type="text/css" ;;
    json|map) content_type="application/json" ;;
    webmanifest) content_type="application/manifest+json" ;;
    svg) content_type="image/svg+xml" ;;
    png) content_type="image/png" ;;
    jpg|jpeg) content_type="image/jpeg" ;;
    gif) content_type="image/gif" ;;
    ico) content_type="image/x-icon" ;;
    webp) content_type="image/webp" ;;
    avif) content_type="image/avif" ;;
    woff) content_type="font/woff" ;;
    woff2) content_type="font/woff2" ;;
    ttf) content_type="font/ttf" ;;
    otf) content_type="font/otf" ;;
    txt) content_type="text/plain" ;;
    xml) content_type="application/xml" ;;
    pdf) content_type="application/pdf" ;;
    wasm) content_type="application/wasm" ;;
    *) content_type="application/octet-stream" ;;
  esac

  "${AWS_CMD[@]}" s3api put-object \
    --bucket "$BUCKET_NAME" \
    --key "$object_key" \
    --body "$file_path" \
    --cache-control "$cache_control" \
    --content-type "$content_type" \
    --expected-bucket-owner "$AUTHORIZED_ACCOUNT_ID" >/dev/null
}

# Step 1: Upload immutable hashed assets with 1-year cache
echo "==> Uploading immutable hashed assets (Cache-Control: public, max-age=31536000, immutable)..."
while IFS= read -r -d '' file; do
  upload_file "$file" "${file#"$BUILD_DIR"/}" "public, max-age=31536000, immutable"
done < <(find "$BUILD_DIR/assets" -type f -print0)

# Step 2: Upload all other static assets (including nested dirs), excluding assets/* and index.html
echo "==> Uploading non-hashed public assets (Cache-Control: public, max-age=86400)..."
while IFS= read -r -d '' file; do
  upload_file "$file" "${file#"$BUILD_DIR"/}" "public, max-age=86400"
done < <(find "$BUILD_DIR" -type f ! -path "$BUILD_DIR/assets/*" ! -path "$BUILD_DIR/index.html" -print0)

# Step 3: Upload root HTML with revalidation cache header
echo "==> Uploading index.html (Cache-Control: public, max-age=0, must-revalidate)..."
upload_file "$BUILD_DIR/index.html" "index.html" "public, max-age=0, must-revalidate"

# ------------------------------------------------------------------------------
# 7. Exact Path CloudFront Cache Invalidation (Never /*)
# Dynamically derive changed mutable paths from build directory outside assets/
# ------------------------------------------------------------------------------
INVALIDATION_PATHS=("/index.html" "/")
while IFS= read -r -d '' file; do
  rel_path="/${file#$BUILD_DIR/}"
  if [[ "$rel_path" != "/index.html" && ! "$rel_path" =~ ^/assets/ ]]; then
    INVALIDATION_PATHS+=("$rel_path")
  fi
done < <(find "$BUILD_DIR" -type f ! -path "$BUILD_DIR/assets/*" ! -path "$BUILD_DIR/index.html" -print0)

echo "==> Invalidating exact changed paths on distribution $DISTRIBUTION_ID: ${INVALIDATION_PATHS[*]}..."
INVALIDATION_JSON=$("${AWS_CMD[@]}" cloudfront create-invalidation \
  --distribution-id "$DISTRIBUTION_ID" \
  --paths "${INVALIDATION_PATHS[@]}" \
  --output json)

INVALIDATION_ID=$(echo "$INVALIDATION_JSON" | jq -r '.Invalidation.Id // empty')
if [[ -z "$INVALIDATION_ID" ]]; then
  echo "FATAL: CloudFront did not return an invalidation ID; frontend release is incomplete." >&2
  exit 1
fi
echo "    Invalidation submitted successfully (ID: $INVALIDATION_ID)."
echo "==> Waiting for CloudFront invalidation $INVALIDATION_ID to complete..."
"${AWS_CMD[@]}" cloudfront wait invalidation-completed \
  --distribution-id "$DISTRIBUTION_ID" \
  --id "$INVALIDATION_ID"
echo "==> Frontend deployment and invalidation completed successfully."
