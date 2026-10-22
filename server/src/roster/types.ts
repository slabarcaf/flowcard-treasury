export type RosterAction = "enrolled" | "deactivated";

export interface CustomerRosterEntry {
  customerId: string;
  salesforceAccountId: string;
  airwallexConnectedAccountId: string;
  isActive: boolean;
  enrolledAt: Date;
  enrolledBy: string;
  enrollmentReason: string;
  deactivatedAt: Date | null;
  deactivatedBy: string | null;
  deactivationReason: string | null;
  updatedAt: Date;
}

export interface EnrollCustomerInput {
  customerId: string;
  salesforceAccountId: string;
  airwallexConnectedAccountId: string;
  actor: string;
  reason: string;
}

export interface DeactivateCustomerInput {
  customerId: string;
  actor: string;
  reason: string;
}

export interface MapCustomerAccountInput {
  customerId: string;
  airwallexConnectedAccountId: string;
  actor: string;
  reason: string;
}

export interface CustomerAccountMappingChange {
  customerId: string;
  oldAccountId: string;
  newAccountId: string;
}
