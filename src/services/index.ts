export type {
  AirwallexClient,
  CrmClient,
  AgentClient,
  TransferResponse,
  ChargeResponse,
  BalanceResponse,
  CardSimulationResponse,
  DepositResponse,
  CrmNote,
  AgentSignalExtraction,
  AgentAllocationProposal,
  BridgeLedgerStatus,
  BridgeLedgerProjection,
  FacilityLedgerProjection,
  LedgerProjectionResponse,
} from './types.js';

export { BridgeApiClient } from './http/BridgeApiClient.js';

// Default to mock implementations
export { mockAirwallexClient } from './mock/AirwallexClient.js';
export { mockCrmClient } from './mock/CrmClient.js';
export { mockAgentClient } from './mock/AgentClient.js';
