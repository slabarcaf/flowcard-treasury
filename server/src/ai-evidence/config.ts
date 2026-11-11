export interface AiEvidenceConfig {
  mode: "fixture" | "production";
  modelId: string;
  configurationVersion: string;
  /** Credentials are loaded only by the server process and must never be serialized to a browser. */
  providerApiKey?: string;
}

export interface ProductionProviderApproval {
  providerApproved: boolean;
  dataHandlingTermsApproved: boolean;
}

/** Production remains fail-closed unless both external approval gates and server credentials exist. */
export function loadAiEvidenceConfig(
  env: NodeJS.ProcessEnv = process.env,
  approval: ProductionProviderApproval = { providerApproved: false, dataHandlingTermsApproved: false },
): AiEvidenceConfig {
  const mode = env.AI_EVIDENCE_MODE?.trim() || "fixture";
  if (mode === "fixture") {
    return { mode, modelId: "synthetic-fixture-v1", configurationVersion: "evidence-config-v1" };
  }
  if (mode !== "production") throw new Error("AI_EVIDENCE_MODE must be fixture or production");
  if (!approval.providerApproved || !approval.dataHandlingTermsApproved) {
    throw new Error("Production AI evidence provider is disabled until provider and data-handling terms are approved");
  }
  const providerApiKey = env.AI_EVIDENCE_PROVIDER_API_KEY?.trim();
  const modelId = env.AI_EVIDENCE_MODEL_ID?.trim();
  if (!providerApiKey || !modelId) throw new Error("Production AI evidence requires server-side provider credentials and model ID");
  return { mode, modelId, configurationVersion: "evidence-config-v1", providerApiKey };
}
