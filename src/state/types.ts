import type { Band, Tier, Door } from '../policy/types.js';

// ---------------------------------------------------------------------------
// Dashboard phase
// ---------------------------------------------------------------------------
export type DashboardPhase = 'idle' | 'scanned' | 'funded';

// ---------------------------------------------------------------------------
// Pipeline step — one node in the underwriting flowchart
// ---------------------------------------------------------------------------
export interface PipelineStep {
  label: string;       // "Enough data?"
  sublabel: string;    // "90 days on platform"
  status: 'pass' | 'fail' | 'skip';
  value?: string;      // "730 days", "Tier A", "$6,000"
  tooltip?: string;    // hover explanation
}

// ---------------------------------------------------------------------------
// Queue row — one per declined payment
// ---------------------------------------------------------------------------
export interface QueueRow {
  id: string;
  customerName: string;
  customerId: string;
  paymentDescription: string;
  paymentAmount: number;
  shortfall: number;
  detectedAt: string;
  tier: Tier;
  door: Door;
  allocation: number;
  status: string;
  rationale: string;
  eligible: boolean;
  ineligibleReason?: string;
}

// ---------------------------------------------------------------------------
// Activity feed event
// ---------------------------------------------------------------------------
export interface FeedEvent {
  id: string;
  time: string;
  text: string;
  type: 'decline' | 'notification' | 'transfer' | 'charge' | 'payment' | 'deposit' | 'info';
}

// ---------------------------------------------------------------------------
// Policy check item
// ---------------------------------------------------------------------------
export interface PolicyCheck {
  label: string;
  passed: boolean;
}

// ---------------------------------------------------------------------------
// Collection waterfall row
// ---------------------------------------------------------------------------
export interface CollectionRow {
  label: string;
  amount: number;
  requestId: string;
  status: string;
}

// ---------------------------------------------------------------------------
// Per-customer detail data (shown when a queue row is expanded)
// ---------------------------------------------------------------------------
export interface CustomerDetail {
  customerId: string;
  customerName: string;
  tier: Tier;
  tierChange?: { from: Tier; to: Tier };
  door: Door;
  allocation: number;
  status: string;
  rationale: string;
  policyChecks: PolicyCheck[];
  precheck: PipelineStep[];    // 5 daily precheck steps
  perRequest: PipelineStep[];  // 4 per-request steps
  showCrmNote: boolean;       // Bloom only
  showSignals: boolean;       // Bloom only
  bloomAgentPhase?: 'found' | 'revealed' | 'done';
  showApproval: boolean;      // Nova only
  awaitingApproval: boolean;  // Nova: true until funded
  ineligibleReason?: string;  // Pixel only
}

// ---------------------------------------------------------------------------
// Full dashboard state
// ---------------------------------------------------------------------------
export interface DashboardState {
  phase: DashboardPhase;

  // Capital bar
  lentOut: number;
  deployableCapital: number;
  floor: number;
  band: Band;

  // Bridge queue
  queue: QueueRow[];

  // Activity feed
  feed: FeedEvent[];

  // Per-customer detail data
  customerDetails: Record<string, CustomerDetail>;

  // Currently expanded row
  selectedCustomerId: string | null;

  // IDs of customers whose pipeline has been revealed
  processedCustomers: string[];

  // Collection
  showCollection: boolean;
  collectionRows: CollectionRow[];

  // Demo clock
  demoClock: string;
}
