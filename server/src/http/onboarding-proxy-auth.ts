import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

export const DEFAULT_ISSUER = "flowcard-node-proxy";
export const DEFAULT_OPERATOR_AUDIENCE = "flowcard-onboarding-service";
export const DEFAULT_WEBHOOK_AUDIENCE = "flowcard-onboarding-webhooks";
export const DEFAULT_MAX_TTL_SECONDS = 30;

export interface ServiceAssertionClaims {
  iss: string;
  aud: string;
  sub: string;
  iat: number;
  exp: number;
  jti: string;
}

export interface CreateAssertionOptions {
  secret: string;
  subject: string;
  audience?: string;
  issuer?: string;
  ttlSeconds?: number;
  now?: number; // unix timestamp in seconds
}

export interface VerifyAssertionOptions {
  secret: string;
  expectedAudience: string;
  expectedIssuer?: string;
  maxTtlSeconds?: number;
  clockSkewSeconds?: number;
  now?: number; // unix timestamp in seconds
}

/**
 * Creates a signed HS256 JWT assertion for service-to-service communication.
 * Requires secret >= 32 characters, non-empty subject, and enforces short TTL.
 */
export function createServiceAssertion(options: CreateAssertionOptions): string {
  if (!options.secret || options.secret.length < 32) {
    throw new Error("service assertion secret must be at least 32 characters");
  }
  const sub = options.subject.trim();
  if (!sub) {
    throw new Error("service assertion subject must not be empty");
  }

  const now = options.now ?? Math.floor(Date.now() / 1000);
  const ttl = Math.min(options.ttlSeconds ?? DEFAULT_MAX_TTL_SECONDS, DEFAULT_MAX_TTL_SECONDS);
  if (ttl <= 0) {
    throw new Error("service assertion ttl must be greater than 0");
  }

  const header = {
    alg: "HS256",
    typ: "JWT",
  };

  const payload: ServiceAssertionClaims = {
    iss: options.issuer ?? DEFAULT_ISSUER,
    aud: options.audience ?? DEFAULT_OPERATOR_AUDIENCE,
    sub,
    iat: now,
    exp: now + ttl,
    jti: randomBytes(16).toString("hex"),
  };

  const encodedHeader = Buffer.from(JSON.stringify(header), "utf8").toString("base64url");
  const encodedPayload = Buffer.from(JSON.stringify(payload), "utf8").toString("base64url");
  const signingInput = `${encodedHeader}.${encodedPayload}`;
  const signature = createHmac("sha256", options.secret).update(signingInput).digest("base64url");

  return `${signingInput}.${signature}`;
}

/**
 * Verifies a signed HS256 JWT assertion against expected issuer, audience, and TTL constraints.
 * Strictly rejects algorithm confusion (e.g. none, RS256), tampered payloads, or expired tokens.
 */
export function verifyServiceAssertion(token: string, options: VerifyAssertionOptions): ServiceAssertionClaims {
  if (!options.secret || options.secret.length < 32) {
    throw new Error("service assertion secret must be at least 32 characters");
  }

  const parts = token.split(".");
  if (parts.length !== 3) {
    throw new Error("malformed JWT assertion: expected 3 parts");
  }

  const [encodedHeader, encodedPayload, signature] = parts;
  if (!encodedHeader || !encodedPayload) {
    throw new Error("malformed JWT assertion: empty header or payload");
  }

  let header: { alg?: string; typ?: string };
  try {
    header = JSON.parse(Buffer.from(encodedHeader, "base64url").toString("utf8")) as { alg?: string; typ?: string };
  } catch {
    throw new Error("invalid JWT header JSON");
  }

  // Strict algorithm check: HS256 only. Disallow 'none', asymmetric algs, or unset.
  if (header.alg !== "HS256") {
    throw new Error(`unsupported or disallowed JWT algorithm: ${header.alg}`);
  }
  if (header.typ !== "JWT") {
    throw new Error(`unexpected JWT typ header: ${header.typ}`);
  }

  if (!signature) {
    throw new Error("malformed JWT assertion: empty signature");
  }

  // Verify signature with constant-time equality
  const signingInput = `${encodedHeader}.${encodedPayload}`;
  const expectedSignature = createHmac("sha256", options.secret).update(signingInput).digest("base64url");
  const sigBuf = Buffer.from(signature, "utf8");
  const expectedSigBuf = Buffer.from(expectedSignature, "utf8");

  if (sigBuf.length !== expectedSigBuf.length || !timingSafeEqual(sigBuf, expectedSigBuf)) {
    throw new Error("invalid JWT signature");
  }

  let payload: ServiceAssertionClaims;
  try {
    payload = JSON.parse(Buffer.from(encodedPayload, "base64url").toString("utf8")) as ServiceAssertionClaims;
  } catch {
    throw new Error("invalid JWT payload JSON");
  }

  const expectedIssuer = options.expectedIssuer ?? DEFAULT_ISSUER;
  if (payload.iss !== expectedIssuer) {
    throw new Error(`JWT issuer mismatch: expected ${expectedIssuer}, got ${payload.iss}`);
  }

  if (payload.aud !== options.expectedAudience) {
    throw new Error(`JWT audience mismatch: expected ${options.expectedAudience}, got ${payload.aud}`);
  }

  if (!payload.sub || typeof payload.sub !== "string" || !payload.sub.trim()) {
    throw new Error("JWT subject must be a non-empty string");
  }

  const clockSkew = options.clockSkewSeconds ?? 5;
  const maxTtl = options.maxTtlSeconds ?? DEFAULT_MAX_TTL_SECONDS;
  const now = options.now ?? Math.floor(Date.now() / 1000);

  if (typeof payload.iat !== "number" || typeof payload.exp !== "number") {
    throw new Error("JWT iat and exp claims must be numbers");
  }

  if (payload.iat > now + clockSkew) {
    throw new Error("JWT iat is in the future beyond acceptable clock skew");
  }

  if (payload.exp < now - clockSkew) {
    throw new Error("JWT assertion has expired");
  }

  if (payload.exp - payload.iat > maxTtl + clockSkew) {
    throw new Error(`JWT TTL exceeds maximum permitted duration of ${maxTtl} seconds`);
  }

  return payload;
}
