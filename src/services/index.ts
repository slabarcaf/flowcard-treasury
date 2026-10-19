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
} from './types.js';

// Default to mock implementations
export { mockAirwallexClient } from './mock/AirwallexClient.js';
export { mockCrmClient } from './mock/CrmClient.js';
export { mockAgentClient } from './mock/AgentClient.js';
