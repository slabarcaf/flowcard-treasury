import type { EvidencePacket } from "./packet.js";
import type { ModelConfigurationMetadata } from "./types.js";

export interface EvidenceProviderMetadata extends ModelConfigurationMetadata {
  configurationVersion: string;
}

/** Server-only boundary for model output. Implementations must never expose credentials to callers. */
export interface EvidenceModelProvider {
  readonly metadata: EvidenceProviderMetadata;
  readonly toolsEnabled: false;
  extract(packet: EvidencePacket): Promise<unknown>;
}

/** Contract for a future approved provider SDK adapter. This interface itself performs no network calls. */
export interface LiveEvidenceModelAdapter extends EvidenceModelProvider {
  readonly kind: "live";
}
