import { FinancialReadClient } from '../../../services/financial/client.js';
import { FinancialCommandClient } from '../../../services/financial/commands.js';
import type { ToneName } from './tone.js';
export const financeRead = new FinancialReadClient();
export const financeCommand = new FinancialCommandClient();
export function stateTone(status: string): ToneName {
  return ['ELIGIBLE', 'ACTIVE', 'REPAID', 'SETTLED', 'READY'].includes(status)
    ? 'positive'
    : ['INELIGIBLE', 'FAILED_CONFIRMED'].includes(status)
      ? 'critical'
      : [
            'REVIEW_REQUIRED',
            'UNKNOWN',
            'HELD',
            'FUNDING_PENDING',
            'AWAITING_APPROVAL',
            'AWAITING_ACCEPTANCE',
            'RESERVED',
          ].includes(status)
        ? 'warning'
        : 'neutral';
}
export const human = (value: string) =>
  value
    .toLowerCase()
    .split('_')
    .map((x) => x.charAt(0).toUpperCase() + x.slice(1))
    .join(' ');
