export interface ServerConfig {
  port: number;
  databaseUrl: string;
  airwallexClientId: string;
  airwallexApiKey: string;
  airwallexWebhookSecret: string;
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
  return {
    port,
    databaseUrl: required("DATABASE_URL"),
    airwallexClientId: required("AIRWALLEX_CLIENT_ID"),
    airwallexApiKey: required("AIRWALLEX_API_KEY"),
    airwallexWebhookSecret: required("AIRWALLEX_WEBHOOK_SECRET"),
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
  };
}

function isRemoteRepaymentTerm(value: string | undefined): value is "15_days" | "30_days" | "asap" {
  return value === "15_days" || value === "30_days" || value === "asap";
}
