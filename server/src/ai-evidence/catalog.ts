import type { ProposedSignalType } from "./types.js";

/** Proposed from the source requirements; Jev and production qualification are pending. */
export const SIGNAL_CATALOG_VERSION = "proposed-v1";
export const SIGNAL_CATALOG_PRODUCTION_APPROVED = false;

export interface SignalCatalogEntry {
  description: string;
  initialMode: "review_only";
}

export const PROPOSED_SIGNAL_CATALOG: Readonly<Record<ProposedSignalType, SignalCatalogEntry>> = {
  client_payment_dispute: {
    description: "An explicitly named customer client disputes or delays payment.",
    initialMode: "review_only",
  },
  client_revenue_share: {
    description: "An explicitly stated revenue share for a named customer client.",
    initialMode: "review_only",
  },
  expected_inflow_delay: {
    description: "An explicitly stated expected delay in an incoming payment.",
    initialMode: "review_only",
  },
  positive_note: {
    description: "Explicit positive evidence that may be shown but cannot upgrade policy outcomes.",
    initialMode: "review_only",
  },
};

export function isKnownSignalType(type: string): type is ProposedSignalType {
  return Object.hasOwn(PROPOSED_SIGNAL_CATALOG, type);
}

export function loadSignalCatalog(version: string = SIGNAL_CATALOG_VERSION): typeof PROPOSED_SIGNAL_CATALOG {
  if (version !== SIGNAL_CATALOG_VERSION) throw new Error(`Unsupported signal catalog version: ${version}`);
  return PROPOSED_SIGNAL_CATALOG;
}
