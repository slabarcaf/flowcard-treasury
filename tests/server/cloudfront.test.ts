import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { resolve, join } from "node:path";
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, chmodSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import vm from "node:vm";

const WORKTREE_ROOT = resolve(__dirname, "../..");
const DEPLOY_FRONTEND_SCRIPT = resolve(WORKTREE_ROOT, "infra/scripts/deploy-frontend.sh");
const CLOUDFRONT_MAIN_TF = resolve(WORKTREE_ROOT, "infra/modules/cloudfront/main.tf");
const S3_FRONTEND_MAIN_TF = resolve(WORKTREE_ROOT, "infra/modules/s3_frontend/main.tf");
const ALB_MAIN_TF = resolve(WORKTREE_ROOT, "infra/modules/alb/main.tf");

const VALID_ACCOUNT = "123456789012";

describe("CloudFront, S3 OAC & Routing Tier (Task 7)", () => {
  describe("SPA Navigation Rewrite Function (Executed via node:vm on Raw HCL Code)", () => {
    const cfText = readFileSync(CLOUDFRONT_MAIN_TF, "utf8");

    // Extract exact code block from aws_cloudfront_function.spa_rewrite in main.tf
    const match = cfText.match(/code\s+=\s+<<-EOT([\s\S]*?)EOT/);
    if (!match) {
      throw new Error("Could not extract CloudFront function code from infra/modules/cloudfront/main.tf");
    }
    const rawHandlerCode = match[1].trim();

    // Create a VM context to run the actual function
    function runCloudFrontFunction(event: {
      request: {
        method?: string;
        uri: string;
        headers?: Record<string, { value: string }>;
      };
    }): { method?: string; uri: string; headers?: Record<string, { value: string }> } {
      const sandbox = {
        event: {
          request: {
            method: event.request.method || "GET",
            uri: event.request.uri,
            headers: event.request.headers || {},
          },
        },
      };
      vm.createContext(sandbox);
      const script = new vm.Script(`
        ${rawHandlerCode}
        var result = handler(event);
      `);
      script.runInContext(sandbox);
      return (sandbox as unknown as { result: typeof sandbox.event.request }).result;
    }

    it("rewrites standard extensionless HTML navigation routes to /index.html", () => {
      expect(runCloudFrontFunction({ request: { uri: "/" } }).uri).toBe("/index.html");
      expect(runCloudFrontFunction({ request: { uri: "/dashboard" } }).uri).toBe("/index.html");
      expect(runCloudFrontFunction({ request: { uri: "/companies" } }).uri).toBe("/index.html");
      expect(runCloudFrontFunction({ request: { uri: "/onboarding/review" } }).uri).toBe("/index.html");
      expect(runCloudFrontFunction({ request: { uri: "/login" } }).uri).toBe("/index.html");
    });

    it("preserves static files with file extensions as-is without rewriting", () => {
      expect(runCloudFrontFunction({ request: { uri: "/assets/index-Bf6t8a2.js" } }).uri).toBe("/assets/index-Bf6t8a2.js");
      expect(runCloudFrontFunction({ request: { uri: "/assets/index-D9z2.css" } }).uri).toBe("/assets/index-D9z2.css");
      expect(runCloudFrontFunction({ request: { uri: "/vite.svg" } }).uri).toBe("/vite.svg");
      expect(runCloudFrontFunction({ request: { uri: "/favicon.ico" } }).uri).toBe("/favicon.ico");
      expect(runCloudFrontFunction({ request: { uri: "/robots.txt" } }).uri).toBe("/robots.txt");
    });

    it("guards non-GET and non-HEAD methods: non-GET requests are NEVER rewritten", () => {
      expect(runCloudFrontFunction({ request: { method: "POST", uri: "/submit" } }).uri).toBe("/submit");
      expect(runCloudFrontFunction({ request: { method: "PUT", uri: "/update" } }).uri).toBe("/update");
      expect(runCloudFrontFunction({ request: { method: "DELETE", uri: "/delete" } }).uri).toBe("/delete");
      expect(runCloudFrontFunction({ request: { method: "PATCH", uri: "/patch" } }).uri).toBe("/patch");
      expect(runCloudFrontFunction({ request: { method: "HEAD", uri: "/dashboard" } }).uri).toBe("/index.html");
    });

    it("guards reserved prefixes: /api, /docs, /webhooks, /assets are NEVER rewritten", () => {
      expect(runCloudFrontFunction({ request: { uri: "/api" } }).uri).toBe("/api");
      expect(runCloudFrontFunction({ request: { uri: "/api/unknown-endpoint" } }).uri).toBe("/api/unknown-endpoint");
      expect(runCloudFrontFunction({ request: { uri: "/docs" } }).uri).toBe("/docs");
      expect(runCloudFrontFunction({ request: { uri: "/docs/unknown" } }).uri).toBe("/docs/unknown");
      expect(runCloudFrontFunction({ request: { uri: "/webhooks" } }).uri).toBe("/webhooks");
      expect(runCloudFrontFunction({ request: { uri: "/webhooks/unknown" } }).uri).toBe("/webhooks/unknown");
      expect(runCloudFrontFunction({ request: { uri: "/assets" } }).uri).toBe("/assets");
      expect(runCloudFrontFunction({ request: { uri: "/assets/missing-file" } }).uri).toBe("/assets/missing-file");
    });

    it("guards Accept: application/json: API clients expecting JSON are NEVER rewritten", () => {
      const jsonReq = {
        request: {
          uri: "/unknown-service",
          headers: {
            accept: { value: "application/json" },
          },
        },
      };
      expect(runCloudFrontFunction(jsonReq).uri).toBe("/unknown-service");

      const mixedReq = {
        request: {
          uri: "/unknown-service",
          headers: {
            accept: { value: "application/json, text/plain, */*" },
          },
        },
      };
      expect(runCloudFrontFunction(mixedReq).uri).toBe("/unknown-service");
    });

    it("default Accept-absent semantics: browser navigation or missing Accept rewrites to /index.html", () => {
      // Browser navigation explicitly requesting text/html
      const browserReq = {
        request: {
          uri: "/dashboard",
          headers: {
            accept: { value: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8" },
          },
        },
      };
      expect(runCloudFrontFunction(browserReq).uri).toBe("/index.html");

      // Request without Accept header (e.g. CLI curl or bookmark navigation)
      const noAcceptReq = {
        request: {
          uri: "/dashboard",
          headers: {},
        },
      };
      expect(runCloudFrontFunction(noAcceptReq).uri).toBe("/index.html");
    });
  });

  describe("CloudFront Module Static Architecture Guarantees", () => {
    const cfText = readFileSync(CLOUDFRONT_MAIN_TF, "utf8");
    const s3Text = readFileSync(S3_FRONTEND_MAIN_TF, "utf8");
    const albText = readFileSync(ALB_MAIN_TF, "utf8");

    it("enforces ZERO distribution-wide custom_error_response blocks masking API errors", () => {
      expect(cfText).not.toContain("custom_error_response");
    });

    it("configures Origin Access Control with s3 type and sigv4 always-signing", () => {
      expect(cfText).toContain('origin_access_control_origin_type = "s3"');
      expect(cfText).toContain('signing_behavior                  = "always"');
      expect(cfText).toContain('signing_protocol                  = "sigv4"');
    });

    it("routes bare /api and /api/* to private ALB with no-cache policy and all HTTP methods", () => {
      expect(cfText).toContain('path_pattern     = "/api"');
      expect(cfText).toContain('path_pattern     = "/api/*"');
      expect(cfText).toContain('target_origin_id = "alb-vpc-origin"');
      expect(cfText).toContain('cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id');
      expect(cfText).toContain('["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]');
    });

    it("routes /docs/api* to private ALB for OpenAPI docs and assets", () => {
      expect(cfText).toContain('path_pattern     = "/docs/api*"');
      expect(cfText).toContain('target_origin_id = "alb-vpc-origin"');
      expect(cfText).toContain('cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id');
    });

    it("routes /webhooks/airwallex without compression to preserve HMAC verification", () => {
      expect(cfText).toContain('path_pattern     = "/webhooks/airwallex"');
      expect(cfText).toContain('target_origin_id = "alb-vpc-origin"');
      expect(cfText).toContain("compress                 = false");
    });

    it("routes /assets/* to S3 with long-lived immutable cache policy", () => {
      expect(cfText).toContain('path_pattern     = "/assets/*"');
      expect(cfText).toContain('target_origin_id = "s3-frontend"');
      expect(cfText).toContain("aws_cloudfront_cache_policy.static_assets.id");
    });

    it("forwards all viewer headers, cookies, and query strings to ALB Node gateway", () => {
      expect(cfText).toContain('header_behavior = "allViewer"');
      expect(cfText).toContain('cookie_behavior = "all"');
      expect(cfText).toContain('query_string_behavior = "all"');
    });

    it("enforces HSTS, X-Content-Type-Options, X-Frame-Options DENY, and CSP", () => {
      expect(cfText).toContain("strict_transport_security");
      expect(cfText).toContain("content_type_options");
      expect(cfText).toContain('frame_option = "DENY"');
      expect(cfText).toContain("default-src 'self'");
      expect(cfText).toContain("connect-src 'self'");
    });

    it("S3 frontend bucket policy enforces TLS and allows CloudFront OAC read access", () => {
      expect(s3Text).toContain("aws:SecureTransport");
      expect(s3Text).toContain('"Deny"');
      expect(s3Text).toContain("cloudfront.amazonaws.com");
      expect(s3Text).toContain("s3:GetObject");
      expect(s3Text).toContain("AWS:SourceArn");
      expect(s3Text).toContain('"AWS:SourceArn" = var.cloudfront_distribution_arn');
      expect(s3Text).not.toContain("distribution/*");
      const demoConfig = readFileSync(resolve(WORKTREE_ROOT, "infra/environments/demo/main.tf"), "utf8");
      const frontendModule = demoConfig.match(/module "s3_frontend" \{([\s\S]*?)\n\}/)?.[1];
      expect(frontendModule).toContain("cloudfront_distribution_arn = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_arn : null");
    });

    it("ALB Security Group enforces precondition requiring vpc_origin_security_group_id when enable_vpc_origin_sg_only is true", () => {
      expect(albText).toContain("!var.enable_vpc_origin_sg_only || var.vpc_origin_security_group_id != null");
    });

    it("ALB Security Group strictly disallows CloudFront prefix list when enable_vpc_origin_sg_only is true", () => {
      expect(albText).toContain("!var.enable_vpc_origin_sg_only && var.cloudfront_prefix_list_id != null");
    });
  });

  describe("deploy-frontend.sh argument and pre-flight validation", () => {
    it("fails closed when --confirm-approval is missing", () => {
      const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--account-id", VALID_ACCOUNT,
        "--profile", "demo",
        "--region", "us-west-2",
        "--bucket", "flowcard-demo-frontend-assets",
        "--distribution-id", "E1234567890",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("human confirmation required. Provide --confirm-approval");
    });

    it("fails closed when --account-id is missing or not 12 digits", () => {
      const res1 = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--confirm-approval",
        "--profile", "demo",
        "--region", "us-west-2",
      ], { encoding: "utf8" });
      expect(res1.status).toBe(1);
      expect(res1.stderr).toContain("--account-id is required");

      const res2 = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--confirm-approval",
        "--account-id", "not-12-digits",
        "--profile", "demo",
        "--region", "us-west-2",
      ], { encoding: "utf8" });
      expect(res2.status).toBe(1);
      expect(res2.stderr).toContain("must be exactly 12 digits");
    });

    it("fails closed when --profile is missing", () => {
      const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
        "--confirm-approval",
        "--account-id", VALID_ACCOUNT,
        "--region", "us-west-2",
      ], { encoding: "utf8" });

      expect(res.status).toBe(1);
      expect(res.stderr).toContain("--profile is required");
    });

    it("fails closed when caller account does not match authorized account ID", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-mismatch-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "999999999999", "Arn": "arn:aws:iam::999999999999:role/WrongRole"}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not match authorized account");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed when CloudFront distribution ARN does not match authorized account ID", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-dist-mismatch-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  # Returns distribution belonging to different account 999999999999
  echo '{"Distribution": {"ARN": "arn:aws:cloudfront::999999999999:distribution/E1234567890", "DistributionConfig": {"Origins": {"Items": [{"DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com"}]}}}}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not match authorized account");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed BEFORE any S3 write when target bucket appears only in comment/unrelated field with wrong origin", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-comment-only-"));
      try {
        const logFile = join(tmpDir, "commands.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "aws $@" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  # Target bucket appears only in Comment, but actual origin is completely different
  cat <<'EOF'
{
  "Distribution": {
    "ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890",
    "DistributionConfig": {
      "Comment": "Distribution for target bucket flowcard-demo-frontend-assets",
      "Origins": {
        "Items": [
          {
            "Id": "wrong-origin",
            "DomainName": "completely-different-bucket.s3.us-west-2.amazonaws.com",
            "OriginAccessControlId": "E12345OAC"
          }
        ]
      }
    }
  }
}
EOF
  exit 0
fi
if [[ "$1" == "s3" ]]; then
  echo "MUTATION OCCURRED: $@" >> "${logFile}"
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not contain an S3 origin configured for bucket 'flowcard-demo-frontend-assets' with Origin Access Control (OAC) enabled");

        // Verify zero S3 write calls were executed before failure
        const log = readFileSync(logFile, "utf8");
        expect(log).not.toContain("s3 sync");
        expect(log).not.toContain("s3 cp");
        expect(log).not.toContain("MUTATION OCCURRED");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed BEFORE any S3 write when origin domain contains bucket name as substring or collision prefix (collisionprefixbucket.example)", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-collision-prefix-"));
      try {
        const logFile = join(tmpDir, "commands.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "aws $@" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  # Domain name contains bucket name as substring / collision prefix (e.g. prefix-bucket.example.com)
  cat <<'EOF'
{
  "Distribution": {
    "ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890",
    "DistributionConfig": {
      "Origins": {
        "Items": [
          {
            "Id": "collision-origin",
            "DomainName": "collisionprefixflowcard-demo-frontend-assets.example.com",
            "OriginAccessControlId": "E12345OAC"
          }
        ]
      }
    }
  }
}
EOF
  exit 0
fi
if [[ "$1" == "s3" ]]; then
  echo "MUTATION OCCURRED: $@" >> "${logFile}"
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not contain an S3 origin configured for bucket 'flowcard-demo-frontend-assets' with Origin Access Control (OAC) enabled");

        // Verify zero S3 write calls were executed before failure
        const log = readFileSync(logFile, "utf8");
        expect(log).not.toContain("s3 sync");
        expect(log).not.toContain("s3 cp");
        expect(log).not.toContain("MUTATION OCCURRED");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed BEFORE any S3 write when matching bucket origin lacks Origin Access Control (OAC)", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-missing-oac-"));
      try {
        const logFile = join(tmpDir, "commands.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "aws $@" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  # Exact matching domain name but OriginAccessControlId is empty/missing
  cat <<'EOF'
{
  "Distribution": {
    "ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890",
    "DistributionConfig": {
      "Origins": {
        "Items": [
          {
            "Id": "s3-no-oac",
            "DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com",
            "OriginAccessControlId": ""
          }
        ]
      }
    }
  }
}
EOF
  exit 0
fi
if [[ "$1" == "s3" ]]; then
  echo "MUTATION OCCURRED: $@" >> "${logFile}"
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("does not contain an S3 origin configured for bucket 'flowcard-demo-frontend-assets' with Origin Access Control (OAC) enabled");

        // Verify zero S3 write calls were executed before failure
        const log = readFileSync(logFile, "utf8");
        expect(log).not.toContain("s3 sync");
        expect(log).not.toContain("s3 cp");
        expect(log).not.toContain("MUTATION OCCURRED");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fails closed when build input files/manifest are missing or empty", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-empty-build-"));
      try {
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  echo '{"Distribution": {"ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890", "DistributionConfig": {"Origins": {"Items": [{"DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com", "OriginAccessControlId": "E12345OAC"}]}}}}'
  exit 0
fi
exit 1
`);
        chmodSync(mockAws, 0o755);

        // Empty build dir
        const distDir = join(tmpDir, "dist");
        mkdirSync(distDir);

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
          "--build-dir", distDir,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(1);
        expect(res.stderr).toContain("is missing or empty");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("fully executes fake CLI upload and invalidation path with precise headers and dynamic paths", () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "deploy-front-full-run-"));
      try {
        const logFile = join(tmpDir, "commands.log");
        const mockAws = join(tmpDir, "aws");
        writeFileSync(mockAws, `#!/usr/bin/env bash
echo "aws $@" >> "${logFile}"
if [[ "$1" == "sts" && "$2" == "get-caller-identity" ]]; then
  echo '{"Account": "${VALID_ACCOUNT}", "Arn": "arn:aws:iam::${VALID_ACCOUNT}:role/MockDeployer"}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "get-bucket-location" ]]; then
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "get-distribution" ]]; then
  echo '{"Distribution": {"ARN": "arn:aws:cloudfront::${VALID_ACCOUNT}:distribution/E1234567890", "DistributionConfig": {"Origins": {"Items": [{"DomainName": "flowcard-demo-frontend-assets.s3.us-west-2.amazonaws.com", "OriginAccessControlId": "E12345OAC"}]}}}}'
  exit 0
fi
if [[ "$1" == "s3api" && "$2" == "put-object" ]]; then
  [[ "$*" == *"--expected-bucket-owner ${VALID_ACCOUNT}"* ]] || exit 2
  shift 2
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --bucket|--key|--body|--cache-control|--content-type|--expected-bucket-owner)
        [[ $# -ge 2 ]] || exit 2
        shift 2
        ;;
      *) echo "Unsupported PutObject argument: $1" >&2; exit 2 ;;
    esac
  done
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "create-invalidation" ]]; then
  echo '{"Invalidation": {"Id": "I1234567890ABC"}}'
  exit 0
fi
if [[ "$1" == "cloudfront" && "$2" == "wait" && "$3" == "invalidation-completed" ]]; then
  exit 0
fi
echo "Unexpected mock aws command: $@" >&2
exit 1
`);
        chmodSync(mockAws, 0o755);

        // Create realistic build dist structure
        const distDir = join(tmpDir, "dist");
        mkdirSync(distDir);
        mkdirSync(join(distDir, "assets"));
        writeFileSync(join(distDir, "index.html"), "<html><body>FlowCard</body></html>");
        writeFileSync(join(distDir, "vite.svg"), "<svg>icon</svg>");
        writeFileSync(join(distDir, "robots.txt"), "User-agent: *\nDisallow: /");
        writeFileSync(join(distDir, "assets", "index-Bf6t8a2.js"), "console.log('main');");
        writeFileSync(join(distDir, "assets", "index-D9z2.css"), "body { margin: 0; }");
        mkdirSync(join(distDir, "images"));
        writeFileSync(join(distDir, "images", "brand mark.svg"), "<svg>brand</svg>");

        const res = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
          "--build-dir", distDir,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });

        expect(res.status).toBe(0);
        expect(res.stdout).toContain("Caller account matches authorized account");
        expect(res.stdout).toContain("Bucket s3://flowcard-demo-frontend-assets belongs to authorized account");
        expect(res.stdout).toContain("CloudFront distribution E1234567890 verified");
        expect(res.stdout).toContain("Invalidation submitted successfully (ID: I1234567890ABC)");
        expect(res.stdout).toContain("Frontend deployment and invalidation completed successfully.");

        // Read command execution log
        const log = readFileSync(logFile, "utf8");
        const uploads = log.split("\n").filter((line) => line.includes("s3api put-object"));
        expect(uploads).toHaveLength(6);
        for (const upload of uploads) {
          expect(upload).toContain("--expected-bucket-owner 123456789012");
        }
        expect(log).not.toContain("s3 sync");
        expect(log).not.toContain("s3 cp");
        expect(log).toContain("--key assets/index-Bf6t8a2.js --body " + distDir + "/assets/index-Bf6t8a2.js --cache-control public, max-age=31536000, immutable --content-type application/javascript");
        expect(log).toContain("--key assets/index-D9z2.css --body " + distDir + "/assets/index-D9z2.css --cache-control public, max-age=31536000, immutable --content-type text/css");
        expect(log).toContain("--key images/brand mark.svg --body " + distDir + "/images/brand mark.svg --cache-control public, max-age=86400 --content-type image/svg+xml");
        expect(uploads.at(-1)).toContain("--key index.html --body " + distDir + "/index.html --cache-control public, max-age=0, must-revalidate --content-type text/html");
        // Verify invalidation called with exact paths (never /*)
        expect(log).toContain("cloudfront create-invalidation --distribution-id E1234567890 --paths /index.html /");
        expect(log).toContain("cloudfront wait invalidation-completed --distribution-id E1234567890 --id I1234567890ABC");
        expect(log).not.toContain("--paths /*");

        writeFileSync(mockAws, readFileSync(mockAws, "utf8").replace(
          '{"Invalidation": {"Id": "I1234567890ABC"}}',
          '{"Invalidation": {"Id": ""}}',
        ));
        chmodSync(mockAws, 0o755);
        writeFileSync(logFile, "");
        const missingInvalidation = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval",
          "--account-id", VALID_ACCOUNT,
          "--profile", "demo",
          "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890",
          "--build-dir", distDir,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });
        expect(missingInvalidation.status).toBe(1);
        expect(missingInvalidation.stderr).toContain("did not return an invalidation ID");
        expect(readFileSync(logFile, "utf8")).not.toContain("cloudfront wait invalidation-completed");

        writeFileSync(mockAws, readFileSync(mockAws, "utf8").replace(
          'if [[ "$1" == "s3api" && "$2" == "put-object" ]]; then',
          'if [[ "$1" == "s3api" && "$2" == "put-object" ]]; then\n  echo "PutObject owner check failed" >&2\n  exit 17',
        ));
        writeFileSync(logFile, "");
        const failedUpload = spawnSync(DEPLOY_FRONTEND_SCRIPT, [
          "--confirm-approval", "--account-id", VALID_ACCOUNT,
          "--profile", "demo", "--region", "us-west-2",
          "--bucket", "flowcard-demo-frontend-assets",
          "--distribution-id", "E1234567890", "--build-dir", distDir,
        ], {
          encoding: "utf8",
          env: { ...process.env, PATH: `${tmpDir}:${process.env.PATH}` },
        });
        expect(failedUpload.status).toBe(17);
        expect(failedUpload.stderr).toContain("PutObject owner check failed");
        const failedLog = readFileSync(logFile, "utf8");
        expect(failedLog).not.toContain("--key index.html");
        expect(failedLog).not.toContain("cloudfront create-invalidation");
      } finally {
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });
});
