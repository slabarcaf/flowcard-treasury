export type ProviderHttpErrorPhase = "authentication" | "provider_request";

export interface ProviderHttpErrorDetails {
  phase?: ProviderHttpErrorPhase;
  code?: string;
  source?: string;
  message?: string;
}

/** HTTP error returned by an Airwallex API request. */
export class ProviderHttpError extends Error {
  constructor(readonly status: number, message: string, readonly details: ProviderHttpErrorDetails = {}) {
    super(message);
    this.name = "ProviderHttpError";
  }
}

/** Extract only bounded, display-safe fields from an Airwallex error response. */
export function providerHttpErrorDetails(value: unknown, phase: ProviderHttpErrorPhase): ProviderHttpErrorDetails {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return { phase };
  const record = value as Record<string, unknown>;
  const nested = typeof record.error === "object" && record.error !== null && !Array.isArray(record.error)
    ? record.error as Record<string, unknown>
    : record;
  return {
    phase,
    ...(safeProviderCode(nested.code) ? { code: safeProviderCode(nested.code) } : {}),
    ...(safeProviderSource(nested.source) ? { source: safeProviderSource(nested.source) } : {}),
    ...(safeProviderMessage(nested.message) ? { message: safeProviderMessage(nested.message) } : {}),
  };
}

function safeProviderCode(value: unknown): string | undefined {
  return typeof value === "string" && /^[A-Za-z0-9_.-]{1,80}$/.test(value) ? value : undefined;
}

function safeProviderSource(value: unknown): string | undefined {
  return typeof value === "string" && /^[A-Za-z0-9_.\[\]/-]{1,120}$/.test(value) ? value : undefined;
}

function safeProviderMessage(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const message = value
    .replace(/\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9._-]{20,}\b/g, "[redacted]")
    .replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, "[redacted]")
    .replace(/\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b/gi, "[redacted]")
    .replace(/\b(?:\d[ -]?){12,19}\b/g, "[redacted]")
    .replace(/[\u0000-\u001f\u007f<>]/g, " ")
    .replace(/[;]+/g, ",")
    .replace(/[^A-Za-z0-9_ .,:()'\/\[\]-]/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 160);
  return message || undefined;
}
