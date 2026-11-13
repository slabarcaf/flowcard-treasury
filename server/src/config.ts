export interface ServerConfig {
  port: number;
  databaseUrl: string;
  airwallexBaseUrl: string;
  airwallexClientId: string;
  airwallexApiKey: string;
  airwallexWebhookSecret: string;
  /** Optional, server-only operator credential for Kit 5 sandbox actions. */
  kit5OperatorToken?: string;
  /** Server-side identity bound into the authenticated Kit 5 session for approvals. */
  kit5OperatorId?: string;
  /** Exact browser origins allowed to establish a Kit 5 operator session. */
  kit5AllowedOrigins?: readonly string[];
  /** Server-only Airwallex USD Global Account ID for Nova's repayment simulation. */
  kit5NovaGlobalAccountId?: string;
  /** Connected-account binding that must match Nova's active roster row. */
  kit5NovaGlobalAccountConnectedAccountId?: string;
  /** Configured Global Account currency; only USD enables the route. */
  kit5NovaGlobalAccountCurrency?: "USD";
  /** Remote authorization v2 shared secret; absent means fail closed. */
  airwallexRemoteAuthorizationSecret?: string;
  /** Optional explicit term required before an authorization can reserve a bridge. */
  remoteAuthorizationRepaymentTerm?: "15_days" | "30_days" | "asap";
  /** Bearer credential for reading stored policy decision details. */
  policyDecisionReadToken?: string;
  /** Optional read boundary for production Salesforce snapshot responses. */
  salesforceSnapshotReadToken?: string;
  /** Snapshot source selected for this server process, when configured. */
  salesforceSnapshotMode?: "demo" | "production_mcp";
  /** Upstream URL for Go onboarding service proxy (constructor injection or test). */
  onboardingServiceUrl?: string;
  /** Shared secret for service-to-service JWT assertions (constructor injection or test). */
  onboardingServiceSecret?: string;
}

/** API version required by the sandbox issuing-simulation endpoint. */
export const AIRWALLEX_ISSUING_API_VERSION = "2026-08-21";

export interface OnboardingProxyConfig {
  onboardingServiceUrl: string;
  onboardingServiceSecret: string;
}

/**
 * Validates and parses onboarding proxy upstream configuration.
 * Validates partial configuration, URL structure, and secret byte length (>= 32 bytes).
 */
export function parseOnboardingProxyConfig(
  rawUrl: string | undefined,
  rawSecret: string | undefined,
): OnboardingProxyConfig | undefined {
  const url = rawUrl?.trim();
  const secret = rawSecret?.trim();
  if (!url && !secret) {
    return undefined;
  }
  if (!url || !secret) {
    throw new Error("Both ONBOARDING_SERVICE_URL and ONBOARDING_SERVICE_SECRET must be configured together");
  }

  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new Error(`Invalid ONBOARDING_SERVICE_URL: ${url}`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error(`ONBOARDING_SERVICE_URL must use http or https protocol: ${url}`);
  }
  if (parsed.username || parsed.password) {
    throw new Error("ONBOARDING_SERVICE_URL must not contain credentials");
  }
  if (parsed.search || parsed.hash) {
    throw new Error("ONBOARDING_SERVICE_URL must not contain query parameters or hash");
  }
  if (parsed.pathname !== "" && parsed.pathname !== "/") {
    throw new Error("ONBOARDING_SERVICE_URL must not contain a path prefix");
  }

  const secretBytes = Buffer.from(secret, "utf-8");
  if (secretBytes.byteLength < 32) {
    throw new Error(`ONBOARDING_SERVICE_SECRET must be at least 32 bytes (got ${secretBytes.byteLength})`);
  }

  return {
    onboardingServiceUrl: parsed.origin,
    onboardingServiceSecret: secret,
  };
}

/**
 * Validates Airwallex base endpoint URL against platform environment boundaries.
 * In demo runtime, strictly rejects production endpoint (api.airwallex.com) regardless of NODE_ENV.
 * In production runtime, permits api.airwallex.com and rejects sandbox endpoints.
 */
export function validateAirwallexBaseUrl(
  rawUrl: string | undefined,
  targetPlatform: "sandbox" | "production" = "sandbox",
): string {
  const urlString = rawUrl?.trim() || "https://api.sandbox.airwallex.com";
  let parsed: URL;
  try {
    parsed = new URL(urlString);
  } catch {
    throw new Error(`Invalid AIRWALLEX_BASE_URL: ${urlString}`);
  }

  const isLocal = parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost";
  if (parsed.protocol !== "https:" && !isLocal) {
    throw new Error(`Strict HTTPS required for remote Airwallex base endpoint: ${urlString}`);
  }

  if (targetPlatform === "production") {
    if (!isLocal && parsed.hostname !== "api.airwallex.com") {
      throw new Error(`Production Airwallex endpoint must target api.airwallex.com, got "${parsed.hostname}"`);
    }
  } else {
    // Explicit demo sandbox boundary: reject production endpoint regardless of NODE_ENV or Salesforce mode
    if (!isLocal && parsed.hostname !== "api.sandbox.airwallex.com") {
      throw new Error(`Demo runtime Airwallex endpoint must target sandbox (api.sandbox.airwallex.com), got "${parsed.hostname}"`);
    }
  }

  return parsed.origin;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): ServerConfig {
  const required = (name: string): string => {
    const value = env[name]?.trim();
    if (!value) throw new Error(`Missing required environment variable: ${name}`);
    return value;
  };
  const port = Number(env.PORT?.trim() || "3000");
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error("PORT must be an integer between 1 and 65535");
  }

  // Explicit constructor-level sandbox boundary for demo deployment, decoupled from NODE_ENV
  const airwallexBaseUrl = validateAirwallexBaseUrl(env.AIRWALLEX_BASE_URL, "sandbox");
  const onboardingProxy = parseOnboardingProxyConfig(
    env.ONBOARDING_SERVICE_URL,
    env.ONBOARDING_SERVICE_SECRET,
  );

  return {
    port,
    databaseUrl: required("DATABASE_URL"),
    airwallexBaseUrl,
    airwallexClientId: required("AIRWALLEX_CLIENT_ID"),
    airwallexApiKey: required("AIRWALLEX_API_KEY"),
    airwallexWebhookSecret: required("AIRWALLEX_WEBHOOK_SECRET"),
    ...(env.KIT5_OPERATOR_TOKEN?.trim() ? { kit5OperatorToken: env.KIT5_OPERATOR_TOKEN.trim() } : {}),
    ...(env.KIT5_OPERATOR_ID?.trim() ? { kit5OperatorId: env.KIT5_OPERATOR_ID.trim() } : {}),
    ...(env.KIT5_ALLOWED_ORIGINS?.trim()
      ? { kit5AllowedOrigins: env.KIT5_ALLOWED_ORIGINS.split(",").map((origin) => origin.trim()).filter(Boolean) }
      : {}),
    ...(isProviderId(env.KIT5_NOVA_GLOBAL_ACCOUNT_ID?.trim())
      ? { kit5NovaGlobalAccountId: env.KIT5_NOVA_GLOBAL_ACCOUNT_ID.trim() }
      : {}),
    ...(isProviderId(env.KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID?.trim())
      ? { kit5NovaGlobalAccountConnectedAccountId: env.KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID.trim() }
      : {}),
    ...(env.KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY?.trim() === "USD"
      ? { kit5NovaGlobalAccountCurrency: "USD" as const }
      : {}),
    ...(env.AIRWALLEX_REMOTE_AUTHORIZATION_SECRET?.trim()
      ? { airwallexRemoteAuthorizationSecret: env.AIRWALLEX_REMOTE_AUTHORIZATION_SECRET.trim() }
      : {}),
    ...(env.POLICY_DECISION_READ_TOKEN?.trim()
      ? { policyDecisionReadToken: env.POLICY_DECISION_READ_TOKEN.trim() }
      : {}),
    ...(isRemoteRepaymentTerm(env.REMOTE_AUTH_REPAYMENT_TERM?.trim())
      ? { remoteAuthorizationRepaymentTerm: env.REMOTE_AUTH_REPAYMENT_TERM.trim() as "15_days" | "30_days" | "asap" }
      : {}),
    ...(env.SALESFORCE_SNAPSHOT_MODE?.trim() === "demo" || env.SALESFORCE_SNAPSHOT_MODE?.trim() === "production_mcp"
      ? { salesforceSnapshotMode: env.SALESFORCE_SNAPSHOT_MODE.trim() as "demo" | "production_mcp" }
      : {}),
    ...(env.SALESFORCE_SNAPSHOT_READ_TOKEN?.trim()
      ? { salesforceSnapshotReadToken: env.SALESFORCE_SNAPSHOT_READ_TOKEN.trim() }
      : {}),
    ...(onboardingProxy ?? {}),
  };
}

function isRemoteRepaymentTerm(value: string | undefined): value is "15_days" | "30_days" | "asap" {
  return value === "15_days" || value === "30_days" || value === "asap";
}

function isProviderId(value: string | undefined): value is string {
  return Boolean(value && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$/.test(value));
}
