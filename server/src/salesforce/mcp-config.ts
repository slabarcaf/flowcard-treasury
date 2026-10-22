import { SALESFORCE_MAPPING_VERIFIED } from "./field-map.js";
import { parseTextLengthConfig, requireProductionSalesforceReadiness } from "./validate.js";

export interface SalesforceMcpConfig {
  serverName: string;
  queryToolName: string;
  maxAttempts: number;
  retryDelayMs: number;
}

const MAX_MCP_ATTEMPTS = 10;
const MAX_RETRY_DELAY_MS = 60_000;

/** Load explicit host-provided MCP tool identifiers; this module never configures REST. */
export function loadSalesforceMcpConfig(env: NodeJS.ProcessEnv = process.env): SalesforceMcpConfig {
  const required = (key: string): string => {
    const value = env[key]?.trim();
    if (!value) throw new Error(`Production Salesforce MCP configuration is missing ${key}`);
    return value;
  };
  const maxAttempts = parsePositiveInteger(env.SALESFORCE_MCP_MAX_ATTEMPTS, 3, "SALESFORCE_MCP_MAX_ATTEMPTS");
  const retryDelayMs = parsePositiveInteger(env.SALESFORCE_MCP_RETRY_DELAY_MS, 100, "SALESFORCE_MCP_RETRY_DELAY_MS", true);
  if (maxAttempts > MAX_MCP_ATTEMPTS) throw new Error(`SALESFORCE_MCP_MAX_ATTEMPTS must not exceed ${MAX_MCP_ATTEMPTS}`);
  if (retryDelayMs > MAX_RETRY_DELAY_MS) throw new Error(`SALESFORCE_MCP_RETRY_DELAY_MS must not exceed ${MAX_RETRY_DELAY_MS}`);
  return {
    serverName: required("SALESFORCE_MCP_SERVER_NAME"),
    queryToolName: required("SALESFORCE_MCP_QUERY_TOOL_NAME"),
    maxAttempts,
    retryDelayMs,
  };
}

/** Production entrypoints must call this before constructing or scheduling the adapter. */
export function requireSalesforceMcpProductionReadiness(env: NodeJS.ProcessEnv = process.env): SalesforceMcpConfig {
  const config = loadSalesforceMcpConfig(env);
  requireProductionSalesforceReadiness(parseTextLengthConfig(env));
  if (!SALESFORCE_MAPPING_VERIFIED) {
    throw new Error("Production Salesforce MCP is blocked until the provisional Salesforce mapping is verified");
  }
  if (env.SALESFORCE_MCP_CAPABILITIES_VERIFIED?.trim().toLowerCase() !== "true") {
    throw new Error("Production Salesforce MCP is blocked until unattended, read-only pagination capabilities are verified");
  }
  return config;
}

function parsePositiveInteger(value: string | undefined, defaultValue: number, name: string, allowZero = false): number {
  if (value === undefined || value.trim() === "") return defaultValue;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < (allowZero ? 0 : 1)) {
    throw new Error(`${name} must be a ${allowZero ? "non-negative" : "positive"} integer`);
  }
  return parsed;
}

