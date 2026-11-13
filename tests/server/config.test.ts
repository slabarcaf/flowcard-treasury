import { describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  loadConfig,
  parseOnboardingProxyConfig,
  validateAirwallexBaseUrl,
} from "../../server/src/config.js";
import { handleRequest } from "../../server/src/http/server.js";

const validEnv = {
  DATABASE_URL: "postgres://localhost/flowcard",
  AIRWALLEX_CLIENT_ID: "local-client",
  AIRWALLEX_API_KEY: "local-secret",
  AIRWALLEX_WEBHOOK_SECRET: "webhook-secret",
};

const approvedRuntimeFixture = JSON.parse(readFileSync(
  resolve(__dirname, "../fixtures/approved-runtime-config.json"),
  "utf8",
)) as {
  onboarding_service_url: string;
  onboarding_service_secret: string;
  s3_bucket_documents: string;
  s3_kms_key_id: string;
};

describe("server configuration", () => {
  it("reports missing required environment variables", () => {
    expect(() => loadConfig({})).toThrow("DATABASE_URL");
    const { AIRWALLEX_WEBHOOK_SECRET: _secret, ...missingWebhookSecret } = validEnv;
    expect(() => loadConfig(missingWebhookSecret)).toThrow("AIRWALLEX_WEBHOOK_SECRET");
  });

  it("loads required values from the environment", () => {
    expect(loadConfig(validEnv)).toEqual({
      port: 3000,
      databaseUrl: validEnv.DATABASE_URL,
      airwallexBaseUrl: "https://api.sandbox.airwallex.com",
      airwallexClientId: validEnv.AIRWALLEX_CLIENT_ID,
      airwallexApiKey: validEnv.AIRWALLEX_API_KEY,
      airwallexWebhookSecret: validEnv.AIRWALLEX_WEBHOOK_SECRET,
    });
  });

  it("loads the optional Salesforce snapshot read token", () => {
    expect(loadConfig({ ...validEnv, SALESFORCE_SNAPSHOT_READ_TOKEN: "  read-secret  " }).salesforceSnapshotReadToken)
      .toBe("read-secret");
  });

  it("loads server-only Kit 5 operator authentication configuration", () => {
    const config = loadConfig({
      ...validEnv,
      KIT5_OPERATOR_TOKEN: "runtime-operator-token",
      KIT5_ALLOWED_ORIGINS: "https://demo.example,https://local.example",
    });
    expect(config.kit5OperatorToken).toBe("runtime-operator-token");
    expect(config.kit5AllowedOrigins).toEqual(["https://demo.example", "https://local.example"]);
  });

  it("loads Nova Global Account settings only when their format and USD currency are valid", () => {
    const config = loadConfig({
      ...validEnv,
      KIT5_NOVA_GLOBAL_ACCOUNT_ID: " verified-global-account ",
      KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID: " verified-connected-account ",
      KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY: "USD",
    });
    expect(config.kit5NovaGlobalAccountId).toBe("verified-global-account");
    expect(config.kit5NovaGlobalAccountConnectedAccountId).toBe("verified-connected-account");
    expect(config.kit5NovaGlobalAccountCurrency).toBe("USD");

    const invalid = loadConfig({
      ...validEnv,
      KIT5_NOVA_GLOBAL_ACCOUNT_ID: "bad account id",
      KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID: "valid-account",
      KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY: "EUR",
    });
    expect(invalid.kit5NovaGlobalAccountId).toBeUndefined();
    expect(invalid.kit5NovaGlobalAccountCurrency).toBeUndefined();
  });

  it("leaves the onboarding proxy unconfigured when approved values are absent", () => {
    const config = loadConfig(validEnv);
    expect(config.onboardingServiceUrl).toBeUndefined();
    expect(config.onboardingServiceSecret).toBeUndefined();
  });

  it("loads approved onboarding proxy values through the standard loader", () => {
    const config = loadConfig({
      ...validEnv,
      ONBOARDING_SERVICE_URL: ` ${approvedRuntimeFixture.onboarding_service_url} `,
      ONBOARDING_SERVICE_SECRET: ` ${approvedRuntimeFixture.onboarding_service_secret} `,
    });
    expect(config.onboardingServiceUrl).toBe("https://onboarding-api.flowcard.internal:8080");
    expect(config.onboardingServiceSecret).toBe(approvedRuntimeFixture.onboarding_service_secret);
    // The Node gateway does not consume the Go document-storage settings.
    expect(approvedRuntimeFixture.s3_bucket_documents).toBeTruthy();
    expect(approvedRuntimeFixture.s3_kms_key_id).toBeTruthy();
  });

  it.each([
    { ONBOARDING_SERVICE_URL: "https://onboarding-api.flowcard.internal:8080" },
    { ONBOARDING_SERVICE_SECRET: "shared-test-auth-secret-at-least-32-bytes-long" },
  ])("rejects partial onboarding proxy values in the standard loader", (partial) => {
    expect(() => loadConfig({ ...validEnv, ...partial })).toThrow(
      "Both ONBOARDING_SERVICE_URL and ONBOARDING_SERVICE_SECRET must be configured together",
    );
  });

  it("rejects invalid approved onboarding proxy values in the standard loader", () => {
    expect(() => loadConfig({
      ...validEnv,
      ONBOARDING_SERVICE_URL: "not-a-url",
      ONBOARDING_SERVICE_SECRET: "shared-test-auth-secret-at-least-32-bytes-long",
    })).toThrow("Invalid ONBOARDING_SERVICE_URL");
    expect(() => loadConfig({
      ...validEnv,
      ONBOARDING_SERVICE_URL: "https://onboarding-api.flowcard.internal:8080",
      ONBOARDING_SERVICE_SECRET: "short",
    })).toThrow("ONBOARDING_SERVICE_SECRET must be at least 32 bytes");
  });
});

describe("parseOnboardingProxyConfig (independent parser)", () => {
  it("returns undefined when both url and secret are absent", () => {
    expect(parseOnboardingProxyConfig(undefined, undefined)).toBeUndefined();
    expect(parseOnboardingProxyConfig("", "")).toBeUndefined();
  });

  it("rejects partial configuration when url is provided without secret", () => {
    expect(() => parseOnboardingProxyConfig("http://127.0.0.1:8080", undefined))
      .toThrow("Both ONBOARDING_SERVICE_URL and ONBOARDING_SERVICE_SECRET must be configured together");
  });

  it("rejects partial configuration when secret is provided without url", () => {
    expect(() => parseOnboardingProxyConfig(undefined, "at-least-32-bytes-long-secret-key-1234"))
      .toThrow("Both ONBOARDING_SERVICE_URL and ONBOARDING_SERVICE_SECRET must be configured together");
  });

  it("rejects secret shorter than 32 bytes", () => {
    expect(() => parseOnboardingProxyConfig("http://127.0.0.1:8080", "too-short"))
      .toThrow("ONBOARDING_SERVICE_SECRET must be at least 32 bytes");
  });

  it("rejects malformed URLs, URLs with credentials, paths, query, or non-http protocols", () => {
    expect(() => parseOnboardingProxyConfig("not-a-url", "secret-at-least-32-bytes-long-12345")).toThrow("Invalid ONBOARDING_SERVICE_URL");
    expect(() => parseOnboardingProxyConfig("ftp://example.com", "secret-at-least-32-bytes-long-12345")).toThrow("must use http or https protocol");
    expect(() => parseOnboardingProxyConfig("http://user:pass@example.com", "secret-at-least-32-bytes-long-12345")).toThrow("must not contain credentials");
    expect(() => parseOnboardingProxyConfig("http://example.com/api", "secret-at-least-32-bytes-long-12345")).toThrow("must not contain a path prefix");
    expect(() => parseOnboardingProxyConfig("http://example.com?foo=bar", "secret-at-least-32-bytes-long-12345")).toThrow("must not contain query parameters");
  });

  it("successfully parses valid configuration and preserves secret bytes", () => {
    const parsed = parseOnboardingProxyConfig(" http://127.0.0.1:8080/ ", " super-secret-at-least-32-bytes-long-1234 ");
    expect(parsed).toEqual({
      onboardingServiceUrl: "http://127.0.0.1:8080",
      onboardingServiceSecret: "super-secret-at-least-32-bytes-long-1234",
    });
  });
});

describe("validateAirwallexBaseUrl (demo runtime boundary)", () => {
  it("defaults to sandbox endpoint when unconfigured", () => {
    expect(validateAirwallexBaseUrl(undefined, "sandbox")).toBe("https://api.sandbox.airwallex.com");
  });

  it("permits local loopback HTTP endpoints for testing", () => {
    expect(validateAirwallexBaseUrl("http://127.0.0.1:8080", "sandbox")).toBe("http://127.0.0.1:8080");
    expect(validateAirwallexBaseUrl("http://localhost:3000", "sandbox")).toBe("http://localhost:3000");
  });

  it("strictly rejects production endpoint in demo runtime", () => {
    expect(() => validateAirwallexBaseUrl("https://api.airwallex.com", "sandbox"))
      .toThrow('Demo runtime Airwallex endpoint must target sandbox (api.sandbox.airwallex.com), got "api.airwallex.com"');
  });

  it("permits production endpoint in explicit production environment and rejects sandbox endpoint", () => {
    expect(validateAirwallexBaseUrl("https://api.airwallex.com", "production")).toBe("https://api.airwallex.com");
    expect(() => validateAirwallexBaseUrl("https://api.sandbox.airwallex.com", "production"))
      .toThrow('Production Airwallex endpoint must target api.airwallex.com, got "api.sandbox.airwallex.com"');
  });

  it("rejects non-HTTPS remote URLs", () => {
    expect(() => validateAirwallexBaseUrl("http://remote.example.com", "sandbox"))
      .toThrow("Strict HTTPS required for remote Airwallex base endpoint");
  });

  it("loadConfig succeeds with NODE_ENV=production and sandbox URL, fails with production URL regardless of Salesforce mode", () => {
    // NODE_ENV=production + sandbox URL succeeds
    const config = loadConfig({
      ...validEnv,
      NODE_ENV: "production",
      AIRWALLEX_BASE_URL: "https://api.sandbox.airwallex.com",
      SALESFORCE_SNAPSHOT_MODE: "production_mcp",
    });
    expect(config.airwallexBaseUrl).toBe("https://api.sandbox.airwallex.com");

    // NODE_ENV=production + production URL fails with demo mode
    expect(() => loadConfig({
      ...validEnv,
      NODE_ENV: "production",
      AIRWALLEX_BASE_URL: "https://api.airwallex.com",
      SALESFORCE_SNAPSHOT_MODE: "demo",
    })).toThrow("Demo runtime Airwallex endpoint must target sandbox");

    // NODE_ENV=production + production URL fails with production_mcp mode
    expect(() => loadConfig({
      ...validEnv,
      NODE_ENV: "production",
      AIRWALLEX_BASE_URL: "https://api.airwallex.com",
      SALESFORCE_SNAPSHOT_MODE: "production_mcp",
    })).toThrow("Demo runtime Airwallex endpoint must target sandbox");

    // NODE_ENV=production + production URL fails without Salesforce mode
    expect(() => loadConfig({
      ...validEnv,
      NODE_ENV: "production",
      AIRWALLEX_BASE_URL: "https://api.airwallex.com",
    })).toThrow("Demo runtime Airwallex endpoint must target sandbox");
  });
});

describe("health route", () => {
  it("returns an ok response", () => {
    const response = { writeHead: vi.fn(), end: vi.fn() };
    handleRequest({ method: "GET", url: "/health" } as never, response as never);
    expect(response.writeHead).toHaveBeenCalledWith(200, { "content-type": "application/json" });
    expect(response.end).toHaveBeenCalledWith(JSON.stringify({ status: "ok" }));
  });
});
