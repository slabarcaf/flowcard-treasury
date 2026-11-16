export interface ServerConfig {
  port: number;
  databaseUrl: string;
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

  const onboardingProxy = parseOnboardingProxyConfig(
    env.ONBOARDING_SERVICE_URL,
    env.ONBOARDING_SERVICE_SECRET,
  );

  return {
    port,
    databaseUrl: required("DATABASE_URL"),
    ...(env.KIT5_OPERATOR_TOKEN?.trim() ? { kit5OperatorToken: env.KIT5_OPERATOR_TOKEN.trim() } : {}),
    ...(env.KIT5_OPERATOR_ID?.trim() ? { kit5OperatorId: env.KIT5_OPERATOR_ID.trim() } : {}),
    ...(env.KIT5_ALLOWED_ORIGINS?.trim()
      ? { kit5AllowedOrigins: env.KIT5_ALLOWED_ORIGINS.split(",").map((origin) => origin.trim()).filter(Boolean) }
      : {}),
    ...(onboardingProxy ?? {}),
  };
}
