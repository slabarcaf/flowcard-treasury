export interface ServerConfig {
  port: number;
  databaseUrl: string;
  airwallexBaseUrl: string;
  airwallexClientId: string;
  airwallexApiKey: string;
  airwallexWebhookSecret: string;
  /** Server-only credential for the retained onboarding operator session. */
  kit5OperatorToken?: string;
  /** Server-side actor identity bound into the onboarding session. */
  kit5OperatorId?: string;
  /** Exact browser origins allowed to establish the operator session. */
  kit5AllowedOrigins?: readonly string[];
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
    ...(onboardingProxy ?? {}),
  };
}
