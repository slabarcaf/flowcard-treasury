export type {
  AirwallexClient,
  CrmClient,
  AgentClient,
  SalesforceSnapshotClient,
  SalesforceSnapshotReadModel,
  SalesforceSnapshotFreshness,
  SalesforceSnapshotSourceMode,
  TransferResponse,
  ChargeResponse,
  BalanceResponse,
  CardSimulationResponse,
  CardDeclineReason,
  DepositResponse,
  CrmNote,
  AgentSignalExtraction,
  AgentAllocationProposal,
  BridgeLedgerStatus,
  BridgeLedgerProjection,
  FacilityLedgerProjection,
  LedgerProjectionResponse,
} from './types.js';

export type { SalesforceSnapshotClientMode } from './salesforceSnapshotClient.js';

export { BridgeApiClient } from './http/BridgeApiClient.js';
export { SalesforceSnapshotApiClient } from './http/SalesforceSnapshotApiClient.js';
export { createSalesforceSnapshotClient } from './salesforceSnapshotClient.js';

// Default to mock implementations
export { mockAirwallexClient } from './mock/AirwallexClient.js';
export { mockCrmClient } from './mock/CrmClient.js';
export { mockAgentClient } from './mock/AgentClient.js';
export { SalesforceSnapshotFixtureClient } from './mock/SalesforceSnapshotFixtureClient.js';
