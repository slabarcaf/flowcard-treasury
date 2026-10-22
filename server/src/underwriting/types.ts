/** Versioned, allowlisted values used by the deterministic underwriting policy. */
export const UNDERWRITING_PROFILE_SCHEMA_VERSION = 1 as const;
export const UNDERWRITING_FEATURE_VERSION = "underwriting-features-v1" as const;
export const PROFILE_WINDOW_DAYS = 90 as const;
export const MAX_PROFILE_AGE_MS = 24 * 60 * 60 * 1000;

export type ProfileSourceMode = "demo" | "production";
export type ProfileQueryName = "account" | "balances" | "transactions" | "expected_inflows" | "repayments";
export type ProfileQueryCompletion = Record<ProfileQueryName, { complete: boolean; pages: number }>;

export interface UnderwritingProfileData {
  averageMonthlySettledInflowsCents: number | null;
  expectedDateReliability: number | null;
  topPayerConcentration: number | null;
  lowBalanceDays: number | null;
  historyDays: number;
  eligibleSpend: "supported" | "unsupported" | "unknown";
  connectedAccountStatus: "active" | "frozen" | "closed" | "unknown";
  openRfi: boolean | null;
  repaymentHistory: "never_borrowed" | "on_time" | "late" | "unknown";
}

export interface UnderwritingProfileCandidate {
  customerId: string;
  runId: string;
  sourceMode: ProfileSourceMode;
  capturedAt: Date;
  startedAt: Date;
  sourceVersions: Readonly<Record<ProfileQueryName, string>>;
  queryCompletion: ProfileQueryCompletion;
  schemaVersion: typeof UNDERWRITING_PROFILE_SCHEMA_VERSION;
  featureVersion: string;
  windowStart: Date;
  windowEnd: Date;
  data: UnderwritingProfileData;
}

export interface PublishedUnderwritingProfile extends UnderwritingProfileCandidate {
  profileId: string;
  version: number;
  publishedAt: Date;
}

export interface FailedProfileAttempt {
  customerId: string;
  runId: string;
  sourceMode: ProfileSourceMode;
  startedAt: Date;
  finishedAt: Date;
  sourceVersions: Partial<Record<ProfileQueryName, string>>;
  queryCompletion: Partial<ProfileQueryCompletion>;
  errorMessage: string;
}

/** Rebuild a profile from the explicit policy allowlist; caller extras are dropped. */
export function allowlistProfileData(value: UnderwritingProfileData): UnderwritingProfileData {
  return {
    averageMonthlySettledInflowsCents: value.averageMonthlySettledInflowsCents,
    expectedDateReliability: value.expectedDateReliability,
    topPayerConcentration: value.topPayerConcentration,
    lowBalanceDays: value.lowBalanceDays,
    historyDays: value.historyDays,
    eligibleSpend: value.eligibleSpend,
    connectedAccountStatus: value.connectedAccountStatus,
    openRfi: value.openRfi,
    repaymentHistory: value.repaymentHistory,
  };
}

export function validateProfileCandidate(candidate: UnderwritingProfileCandidate): string[] {
  const errors: string[] = [];
  if (!candidate.customerId.trim()) errors.push("customerId is required");
  if (!candidate.runId.trim()) errors.push("runId is required");
  if (!candidate.featureVersion.trim()) errors.push("featureVersion is required");
  if (!candidate.schemaVersion || candidate.schemaVersion !== UNDERWRITING_PROFILE_SCHEMA_VERSION) errors.push("Unsupported profile schema version");
  if (!Number.isFinite(candidate.capturedAt.getTime()) || !Number.isFinite(candidate.startedAt.getTime())) errors.push("Profile timestamps must be valid");
  if (!Number.isFinite(candidate.windowStart.getTime()) || !Number.isFinite(candidate.windowEnd.getTime()) ||
      candidate.windowEnd.getTime() <= candidate.windowStart.getTime()) errors.push("Profile window must be valid");
  else if (candidate.windowEnd.getTime() - candidate.windowStart.getTime() !== PROFILE_WINDOW_DAYS * 24 * 60 * 60 * 1000) errors.push("Profile window must be exactly 90 days");
  for (const query of PROFILE_QUERY_NAMES) {
    const completion = candidate.queryCompletion?.[query];
    if (!completion || !completion.complete || !Number.isSafeInteger(completion.pages) || completion.pages < 1) {
      errors.push(`Required query ${query} is incomplete`);
    }
    if (!candidate.sourceVersions?.[query]?.trim()) errors.push(`Source version ${query} is required`);
  }
  const data = candidate.data;
  if (!data) return [...errors, "Profile data is required"];
  if (data.historyDays !== PROFILE_WINDOW_DAYS) errors.push("Profile history must cover 90 days");
  if (data.averageMonthlySettledInflowsCents !== null && (!Number.isSafeInteger(data.averageMonthlySettledInflowsCents) || data.averageMonthlySettledInflowsCents < 0)) errors.push("Monthly inflows must be non-negative integer USD cents or unknown");
  if (data.expectedDateReliability !== null && (!Number.isFinite(data.expectedDateReliability) || data.expectedDateReliability < 0 || data.expectedDateReliability > 1)) errors.push("Expected-date reliability must be a ratio from 0 through 1 or unknown");
  if (data.expectedDateReliability === null) errors.push("Expected-date reliability is unknown");
  if (data.topPayerConcentration !== null && (!Number.isFinite(data.topPayerConcentration) || data.topPayerConcentration < 0 || data.topPayerConcentration > 1)) errors.push("Top-payer concentration must be a ratio from 0 through 1 or unknown");
  if (data.lowBalanceDays !== null && (!Number.isSafeInteger(data.lowBalanceDays) || data.lowBalanceDays < 0 || data.lowBalanceDays > PROFILE_WINDOW_DAYS)) errors.push("Low-balance days must be from 0 through 90 or unknown");
  if (data.eligibleSpend === "unknown") errors.push("Eligible-spend classification is unknown");
  if (data.connectedAccountStatus === "unknown") errors.push("Connected-account standing is unknown");
  if (data.openRfi === null) errors.push("Open RFI state is unknown");
  if (data.repaymentHistory === "unknown") errors.push("Repayment history is unknown");
  return errors;
}

export const PROFILE_QUERY_NAMES: readonly ProfileQueryName[] = ["account", "balances", "transactions", "expected_inflows", "repayments"];

export function isProfileFresh(capturedAt: Date | string, now = new Date()): boolean {
  const captured = capturedAt instanceof Date ? capturedAt.getTime() : Date.parse(capturedAt);
  const age = now.getTime() - captured;
  return Number.isFinite(age) && age >= 0 && age <= MAX_PROFILE_AGE_MS;
}
