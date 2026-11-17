import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { SavedRules } from '../../src/features/bridge-capital/backoffice/ConnectedBackOffice.js';
import type { CaseDetail } from '../../src/services/financial/client.js';
it('keeps unknown Go rule evidence distinct from failure and eligibility', () => {
  const value: CaseDetail = {
    case_id: 'owned-case',
    mode: 'DEMO',
    kind: 'SHORTFALL',
    status: 'COMPLETED',
    stage: 'COMPLETED',
    customer_id: 'owned-customer',
    account_ref: 'customer:owned-customer',
    detection_id: 'owned-shortfall',
    bridge_id: null,
    bridge_version: null,
    refresh_needed: false,
    next_actions: [],
    updated_at: '2026-11-16T03:00:00Z',
    assessment: {
      id: 'owned-evaluation',
      customer_id: 'owned-customer',
      account_ref: 'customer:owned-customer',
      shortfall_id: 'owned-shortfall',
      source_mode: 'DEMO',
      status: 'REVIEW_REQUIRED',
      evaluated_at: '2026-11-16T03:00:00Z',
      policy_id: 'owned-policy',
      policy_hash: 'a'.repeat(64),
      term_days: 30,
      customer_snapshot_id: null,
      customer_snapshot_version: null,
      facility_snapshot_id: null,
      facility_snapshot_version: null,
      funding_authorized: false,
      requires_recheck: true,
      approval_required: false,
      principal_cents: 700000,
      estimated_fee_cents: 19000,
      total_due_cents: 719000,
      customer_limit_cents: null,
      existing_exposure_cents: null,
      post_funding_exposure_cents: null,
      available_capital_cents: null,
      rules: [
        {
          rule_id: 'RECEIVABLE_COVERAGE',
          status: 'UNKNOWN',
          reason_code: 'RECEIVABLE_COVERAGE_UNKNOWN',
          actual: { due_on: null },
          threshold: { date_required: true },
        },
      ],
      reason_codes: ['RECEIVABLE_COVERAGE_UNKNOWN'],
    },
  };
  const html = renderToStaticMarkup(createElement(SavedRules, { value }));
  expect(html).toContain('Needs information');
  expect(html).toContain('Review Required');
  expect(html).toContain('Receivable coverage');
  expect(html).not.toContain('>Fail<');
  expect(html).not.toContain('>Eligible<');
});
