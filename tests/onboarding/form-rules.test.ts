import { describe, expect, it, vi } from 'vitest';
import { newCompany, normalizeTaxId, stepIssues, validEin } from '../../src/services/onboarding/formRules.js';
import { industryOptions } from '../../src/services/onboarding/industryReference.js';
import { OnboardingApiClient, OnboardingApiError } from '../../src/services/onboarding/OnboardingApiClient.js';
import type { OnboardingDetailsInput } from '../../src/services/onboarding/contracts.js';

const details = (): OnboardingDetailsInput => ({ estimated_monthly_revenue: {}, customer_agreements: { agreed_to_data_usage: false, agreed_to_terms_and_conditions: false } });

describe('complete application intake', () => {
  it('requires final-review company information, not just the create-company minimum', () => {
    const input = newCompany();
    input.legal_name = 'Example LLC';
    expect(stepIssues(0, input, details())).toHaveLength(4);
    input.entity_type = 'LLC';
    input.business_profile = { website: 'https://example.com', description: 'Business software', registered_address: { line_1: '1 Main Street', city: 'New York', country: 'US', state: 'NY', postal_code: '10001' } };
    input.registration_identifiers![0].value = '12-3456789';
    expect(stepIssues(0, input, details())).toEqual([]);
    input.business_profile.registered_address!.state = '';
    expect(stepIssues(0, input, details())).toContain('Complete the US registered address, including the state and ZIP code.');
  });
  it('does not let missing people or missing identification slip through', () => {
    const input = newCompany();
    input.people = [];
    expect(stepIssues(1, input, details())).toHaveLength(1);
    input.people = [{ id: 'test', first_name: 'Example', last_name: 'Person', role: 'Director', date_of_birth: '1990-01-01' }];
    expect(stepIssues(1, input, details())).toEqual(['Add identification for person 1.']);
  });
  it('checks tax identification format and US issuing country', () => {
    const input = newCompany();
    input.people![0] = { id: 'test', first_name: 'Example', last_name: 'Person', role: 'Director', date_of_birth: '1990-01-01', identifications: [{ type: 'SSN', number: '123-45-6789', issuing_country: 'US' }] };
    expect(stepIssues(1, input, details())).toEqual([]);
    input.people![0].identifications![0].issuing_country = 'GB';
    expect(stepIssues(1, input, details()).join(' ')).toContain('9-digit US SSN');
    expect(normalizeTaxId('12-3456789')).toBe('123456789');
    expect(validEin('123456789')).toBe(true);
    expect(validEin('abc')).toBe(false);
  });
  it('requires use, revenue, a real primary contact, roles and residential addresses', () => {
    const input = newCompany();
    const app = details();
    expect(stepIssues(2, input, app)).toHaveLength(6);
    const person = input.people![0];
    Object.assign(app, { industry_category_code: 'ICCV3_TEST', intended_products: ['CREATE_CARDS'], estimated_monthly_revenue: { amount: '0', currency: 'USD' }, primary_contact_person_id: person.id, people: [{ person_id: person.id, platform_roles: ['BENEFICIAL_OWNER'], residential_address: { line_1: '1 Main Street', city: 'New York', state: 'NY', country: 'US', postal_code: '10001' } }] });
    expect(stepIssues(2, input, app)).toEqual([]);
  });
  it('turns verified reference trees into labels while preserving question answers and wire codes', () => {
    expect(industryOptions([{ type: 'LIST', name: 'Example sector', values: [{ type: 'QUESTION', name: 'Example activity', question: 'Which?', values: [{ type: 'CODE', answer: 'Yes', value: 'ICCV3_TESTYES' }, { type: 'code', answer: 'No', value: 'ICCV3_TESTNO' }] }] }])).toEqual([
      { value: 'ICCV3_TESTNO', label: 'Example sector › Example activity › Which? › No' },
      { value: 'ICCV3_TESTYES', label: 'Example sector › Example activity › Which? › Yes' },
    ]);
    expect(() => industryOptions([{ type: 'CODE', name: 'Unknown', value: '<string>' }])).toThrow();
    expect(() => industryOptions([])).toThrow();
  });
  it('explains a refused gateway connection as not submitted, rather than an ambiguous saved request', async () => {
    const api = new OnboardingApiClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ code: 'ONBOARDING_GATEWAY_UNREACHABLE' }), { status: 503 })));
    const error = await api.createCompany({ legal_name: 'Example' }).catch(error => error as OnboardingApiError);
    expect(error).toMatchObject({ status: 503, code: 'ONBOARDING_GATEWAY_UNREACHABLE' });
    expect((error as OnboardingApiError).message).toContain('has not been submitted');
  });
});
