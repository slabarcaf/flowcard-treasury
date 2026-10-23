import type { Kit5RepaymentCollectionCharge } from '../services/types.js';

export function formatCollectionCharge(charge: Kit5RepaymentCollectionCharge | null | undefined): string {
  if (!charge) return 'Waiting for deposit settlement or charge creation';
  return `${charge.status}, $${(charge.amountCents / 100).toLocaleString('en-US')} USD, provider operation ${charge.providerOperationId}${charge.providerObjectId ? `, provider charge ${charge.providerObjectId}` : ''}`;
}
