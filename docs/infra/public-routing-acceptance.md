# Public Routing and HTTPS Gateway Checks (Task 7)

Run command examples from the repository root.

## 1. Overview

This document defines the post-provisioning verification procedures and live
acceptance commands for the FlowCard demonstration HTTPS routing tier. The
Task 9 [`demo-acceptance.md`](demo-acceptance.md) is the canonical acceptance
status matrix. No live checks in this document have been performed during the
current offline handoff.

> [!IMPORTANT]
> The checks in this document require a live, provisioned AWS deployment.
> During offline planning and development (Tasks 1–7), these checks remain open.
> Mock and static fixture verifications do NOT constitute live acceptance.

---

## 2. Live Verification Commands Checklist

After authorized provisioning, set the target distribution domain name from
Terraform outputs. Never place secret values in command-line arguments or
logs:

```bash
export CLOUDFRONT_DOMAIN=$(tofu -chdir=infra/environments/demo output -raw cloudfront_domain_name)
OPERATOR_TOKEN=$(aws secretsmanager get-secret-value --secret-id flowcard/operator --query SecretString --output text | jq -r .operator_token)
COOKIE_JAR=$(mktemp)
HEADER_FILE=$(mktemp)
TEST_DOC=$(mktemp -t flowcard-demo-doc)
chmod 600 "$COOKIE_JAR"
chmod 600 "$HEADER_FILE"
chmod 600 "$TEST_DOC"
trap 'rm -f "$COOKIE_JAR" "$HEADER_FILE" "$TEST_DOC"' EXIT
```

### 2.1 Operator Session & Secure Cookie Verification

Verify that CloudFront forwards the Origin header, respects
`KIT5_ALLOWED_ORIGINS`, and sets a secure session cookie:

```bash
# 1. Obtain session cookie via operator login
LOGIN_STATUS=$(printf '{"token":"%s"}' "$OPERATOR_TOKEN" | curl -sS -D "$HEADER_FILE" -o /dev/null -w '%{http_code}' -c "$COOKIE_JAR" \
  -H "Origin: https://${CLOUDFRONT_DOMAIN}" \
  -H "Content-Type: application/json" \
  --data-binary @- \
  "https://${CLOUDFRONT_DOMAIN}/api/kit5/session"
)
printf 'Operator login HTTP status: %s\n' "$LOGIN_STATUS"

# Expected:
# 200. Verify required cookie attributes without printing the cookie value.
python3 - "$HEADER_FILE" <<'PY'
import sys

headers = open(sys.argv[1], encoding="utf-8").read().splitlines()
cookie = next((line.split(":", 1)[1].strip() for line in headers
               if line.lower().startswith("set-cookie: kit5_operator_session=")), "")
attributes = {part.strip().lower() for part in cookie.split(";")[1:]}
required = {"path=/api", "httponly", "secure", "samesite=none"}
if not required.issubset(attributes):
    raise SystemExit("Required session-cookie attributes are missing.")
print("Required session-cookie attributes verified; value was not printed.")
PY
```

```bash
# 2. Invoke an authenticated endpoint with the session cookie
AUTH_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" \
  -H "Origin: https://${CLOUDFRONT_DOMAIN}" \
  -X POST \
  "https://${CLOUDFRONT_DOMAIN}/api/kit5/next-customer"
)
printf 'Authenticated endpoint HTTP status: %s\n' "$AUTH_STATUS"

# Expected:
# 200. Inspect response only with synthetic data and redact identifiers.
```

### 2.2 SPA Navigation Rewrite Verification (No API Masking)

Verify that HTML navigation paths return `index.html` while API 404s return
structured JSON errors:

```bash
# 1. SPA Navigation Route (rewrite to index.html)
curl -i "https://${CLOUDFRONT_DOMAIN}/dashboard"
# Expected: HTTP/2 200, Content-Type: text/html, <!DOCTYPE html>

curl -i "https://${CLOUDFRONT_DOMAIN}/companies"
# Expected: HTTP/2 200, Content-Type: text/html, <!DOCTYPE html>

# 2. API Error Route (MUST NOT return index.html)
curl -i "https://${CLOUDFRONT_DOMAIN}/api/nonexistent-route"
# Expected: HTTP/2 404, Content-Type: application/json
# {"error":"not_found"}

# 3. Missing Static File (MUST NOT return index.html)
curl -i "https://${CLOUDFRONT_DOMAIN}/assets/nonexistent-asset.js"
# Expected: HTTP/2 404 or 403 (S3 NoSuchKey / AccessDenied, never index.html)
```

### 2.3 Public Documentation & Bundled Swagger UI Assets

Verify unauthenticated access to OpenAPI specs and bundled assets:

```bash
# 1. Documentation HTML entrypoint
curl -i "https://${CLOUDFRONT_DOMAIN}/docs/api"
# Expected: HTTP/2 200, Content-Type: text/html

# 2. OpenAPI JSON specification
curl -i "https://${CLOUDFRONT_DOMAIN}/docs/api/openapi.json"
# Expected: HTTP/2 200, Content-Type: application/json, "openapi": "3.1.0"

# 3. Webhooks JSON specification
curl -i "https://${CLOUDFRONT_DOMAIN}/docs/api/webhooks.json"
# Expected: HTTP/2 200, Content-Type: application/json

# 4. Bundled Swagger UI Static Assets
curl -i "https://${CLOUDFRONT_DOMAIN}/docs/api/assets/swagger-ui.css"
# Expected: HTTP/2 200, Content-Type: text/css
```

### 2.4 External Webhook Raw Body & HMAC Verification

Verify that Airwallex webhook delivery preserves raw bytes for HMAC validation:

```bash
WEBHOOK_SECRET=$(aws secretsmanager get-secret-value --secret-id flowcard/airwallex --query SecretString --output text | jq -r .webhook_secret)
TIMESTAMP=$(date +%s%3N)
BODY='{"id":"evt_demo_test","name":"account.created","data":{}}'
SIGNATURE=$(printf '%s' "$WEBHOOK_SECRET" | python3 -c '
import hashlib
import hmac
import sys

secret = sys.stdin.buffer.read()
message = (sys.argv[1] + sys.argv[2]).encode()
print(hmac.new(secret, message, hashlib.sha256).hexdigest())
' "$TIMESTAMP" "$BODY")

WEBHOOK_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
  -H "x-signature: ${SIGNATURE}" \
  -H "x-timestamp: ${TIMESTAMP}" \
  -H "Content-Type: application/json" \
  -d "${BODY}" \
  "https://${CLOUDFRONT_DOMAIN}/webhooks/airwallex"
)
printf 'Synthetic webhook HTTP status: %s\n' "$WEBHOOK_STATUS"

# Expected: HTTP/2 200, {"status":"received"}
```

### 2.5 Multipart Document Upload Streaming

Verify that document uploads stream through CloudFront and ALB up to 15 MiB:

```bash
# Create a 5 MiB test PDF document
dd if=/dev/urandom of="$TEST_DOC" bs=1048576 count=5

UPLOAD_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
  -b "$COOKIE_JAR" \
  -H "Origin: https://${CLOUDFRONT_DOMAIN}" \
  -F "document=@${TEST_DOC};type=application/pdf" \
  -F "document_type=INCORPORATION_FILING" \
  "https://${CLOUDFRONT_DOMAIN}/api/submissions/app_synthetic_bloom/documents"
)
printf 'Synthetic multipart upload HTTP status: %s\n' "$UPLOAD_STATUS"

# Expected: 201 Created or 200 OK. Capture no response body or document ID.
```

### 2.6 Origin Security & Network Isolation

Verify that private infrastructure is isolated from direct public traffic:

```bash
# 1. Private ALB is NOT publicly accessible
export ALB_DNS=$(tofu -chdir=infra/environments/demo output -raw alb_dns_name)
curl --connect-timeout 5 "http://${ALB_DNS}/health"
# Expected: Timeout or Could not resolve host (non-routable RFC-1918 private IP)

# 2. S3 Frontend Bucket blocks direct public access
export FRONTEND_BUCKET=$(tofu -chdir=infra/environments/demo output -raw frontend_bucket_name)
curl -i "https://${FRONTEND_BUCKET}.s3.us-west-2.amazonaws.com/index.html"
# Expected: HTTP/1.1 403 Forbidden (Public Access Blocks + OAC enforce deny)
```

---

## 3. Acceptance Sign-Off Matrix

| Test Case | Description | Offline Static / Mock Evidence | Live Cloud Verification (Pending Task 8/9) |
| :--- | :--- | :--- | :--- |
| **TC-7.1** | CloudFront HTTPS default cert & domain | Validated HCL schema, `tofu validate` passed | Open (requires live distribution creation) |
| **TC-7.2** | S3 Origin Access Control (OAC) | Bucket policy enforces SigV4 OAC and TLS | Open (requires S3 asset retrieval via CloudFront) |
| **TC-7.3** | Private ALB VPC Origin tunnel | Schema validated with AWS provider 5.100 | Open (requires live ENI-to-ALB tunnel) |
| **TC-7.4** | SPA navigation route rewrite | Unit tested CloudFront Function regex | Open (requires live browser navigation test) |
| **TC-7.5** | API 404/403 error preservation | Asserted 0 `custom_error_response` blocks | Open (requires live curl /api/not-found) |
| **TC-7.6** | Dynamic API no-cache enforcement | Cache policy TTL min/default/max = 0 | Open (requires live response header inspection) |
| **TC-7.7** | Secure session cookie forward | Origin request policy forwards all cookies | Open (requires live operator login) |
| **TC-7.8** | Public OpenAPI & Swagger UI docs | Static route tests for `/docs/api*` | Open (requires live browser Swagger UI load) |
| **TC-7.9** | Airwallex webhook raw payload | Verified `compress = false` on webhook route | Open (requires live signed webhook delivery) |
| **TC-7.10** | Multipart upload streaming | Documented 15 MiB limit in Node gateway | Open (requires live 5 MiB file upload) |
| **TC-7.11** | Frontend deployment automation | 4 unit tests for `deploy-frontend.sh` | Open (requires live asset upload & invalidation) |
| **TC-7.12** | ALB Security Group narrowing | Parameterized `enable_vpc_origin_sg_only` | Open (requires live SG tightening verification) |
