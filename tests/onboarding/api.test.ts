import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { OnboardingApiClient, OnboardingApiError, isAmbiguousSaveFailure } from '../../src/services/onboarding/OnboardingApiClient.js';
import { SaveApplication } from '../../src/services/onboarding/SaveApplication.js';
import { applicationStatus } from '../../src/services/onboarding/contracts.js';
import type { CompanyInput, OnboardingDetailsInput } from '../../src/services/onboarding/contracts.js';

const company = { id: 'comp_test', revision: 1, legal_name: 'Example Ltd', people: [] };
const application = { id: 'app_test', revision: 2, company_id: company.id, status: 'DRAFT' };
const details: OnboardingDetailsInput = { estimated_monthly_revenue: {}, customer_agreements: { agreed_to_terms_and_conditions: false, agreed_to_data_usage: false } };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const spec = JSON.parse(readFileSync(new URL('../../docs/api/openapi.json', import.meta.url), 'utf8'));

describe('onboarding browser integration', () => {
  it('saves company then application using only current DTO keys, without triggering Airwallex submission', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(company, 201)).mockResolvedValueOnce(response(application, 201));
    const input: CompanyInput = { legal_name: 'Example Ltd', trading_name: 'Example', registration_country: 'US', entity_type: 'LLC', business_profile: { website: 'https://example.com', description: 'Software services' }, people: [{ id: 'person_a', first_name: 'Test', last_name: 'Person', role: 'Director', date_of_birth: '1990-01-01' }] };
    const operation = new SaveApplication();
    const result = await operation.run(new OnboardingApiClient(fetcher), input, details, [], vi.fn());
    expect(result.status).toBe('DRAFT');
    expect(fetcher.mock.calls.map(call => call[0])).toEqual(['/api/companies', '/api/onboarding-applications']);
    for (const [, init] of fetcher.mock.calls) expect(init?.credentials).toBe('include');
    const postedCompany = JSON.parse(String(fetcher.mock.calls[0][1]?.body));
    expect(Object.keys(postedCompany).every(key => key in spec.components.schemas.CreateCompanyRequest.properties)).toBe(true);
    expect(Object.keys(postedCompany.people[0]).sort()).toEqual(['date_of_birth', 'first_name', 'id', 'last_name', 'role']);
    expect(postedCompany).not.toHaveProperty('password');
    const postedApplication = JSON.parse(String(fetcher.mock.calls[1][1]?.body));
    expect(postedApplication).toMatchObject({ company_id: company.id, platform_name: 'AIRWALLEX', onboarding_details: details });
    expect(Object.keys(postedApplication).every(key => key in spec.components.schemas.CreateApplicationRequest.properties)).toBe(true);
  });
  it('does not create the company twice when the second request is rejected and retried', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(company, 201)).mockResolvedValueOnce(response({ code: 'VALIDATION_FAILED' }, 422)).mockResolvedValueOnce(response(application, 201));
    const operation = new SaveApplication();
    const api = new OnboardingApiClient(fetcher);
    await expect(operation.run(api, { legal_name: 'Example Ltd' }, details, [], vi.fn())).rejects.toMatchObject({ status: 422 });
    await operation.run(api, { legal_name: 'Example Ltd' }, details, [], vi.fn());
    expect(fetcher.mock.calls.filter(call => call[0] === '/api/companies')).toHaveLength(1);
  });
  it.each([{ error: 'operator_auth_required' }, { code: 'UNAUTHORIZED', message: 'denied' }])('preserves either authentication rejection and never creates an application', async body => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(body, 401));
    await expect(new SaveApplication().run(new OnboardingApiClient(fetcher), { legal_name: 'Example Ltd' }, details, [], vi.fn())).rejects.toMatchObject({ status: 401 });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it('retains field errors, including nested paths', async () => {
    const issues = [{ field: 'people[0].date_of_birth', code: 'past_date', message: 'must be in the past' }];
    const api = new OnboardingApiClient(vi.fn<typeof fetch>().mockResolvedValue(response({ code: 'VALIDATION_FAILED', errors: issues }, 422)));
    await expect(api.createCompany({ legal_name: 'Example Ltd' })).rejects.toMatchObject({ issues });
  });
  it('treats platform readiness failures as failures, not KYB approvals', async () => {
    const api = new OnboardingApiClient(vi.fn<typeof fetch>().mockResolvedValue(response({ code: 'PLATFORM_CONTRACT_INCOMPLETE' }, 422)));
    expect(await api.verify(application.id)).toEqual({ code: 'PLATFORM_CONTRACT_INCOMPLETE', errors: [] });
    expect(applicationStatus('ACTIVE')).toBe('Status unavailable');
    expect(applicationStatus('DRAFT')).toBe('Draft saved');
  });
  it('marks network failures and malformed creation responses as unknown results', async () => {
    const failing = new OnboardingApiClient(vi.fn<typeof fetch>().mockRejectedValue(new TypeError('offline')));
    await expect(failing.createCompany({ legal_name: 'Example Ltd' })).rejects.toMatchObject({ status: 0, code: 'NETWORK_RESULT_UNKNOWN' });
    const malformed = new OnboardingApiClient(vi.fn<typeof fetch>().mockResolvedValue(response({ ok: true }, 201)));
    await expect(malformed.createCompany({ legal_name: 'Example Ltd' })).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });
  it('uploads with multipart and current revision, and does not repeat successful files after a later rejection', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response(company, 201)).mockResolvedValueOnce(response(application, 201))
      .mockResolvedValueOnce(response(application)).mockResolvedValueOnce(response({ document_id: 'doc_a' }, 201))
      .mockResolvedValueOnce(response({ ...application, revision: 3 })).mockResolvedValueOnce(response({ code: 'INVALID_DOCUMENT' }, 422))
      .mockResolvedValueOnce(response({ ...application, revision: 3 })).mockResolvedValueOnce(response({ document_id: 'doc_b' }, 201))
      .mockResolvedValueOnce(response({ ...application, revision: 4 }));
    const uploads = ['a', 'b'].map(key => ({ key, type: 'BUSINESS_DOCUMENT', file: new File(['%PDF-test'], `${key}.pdf`, { type: 'application/pdf' }) }));
    const operation = new SaveApplication();
    const api = new OnboardingApiClient(fetcher);
    await expect(operation.run(api, { legal_name: 'Example Ltd' }, details, uploads, vi.fn())).rejects.toMatchObject({ status: 422 });
    await operation.run(api, { legal_name: 'Example Ltd' }, details, uploads, vi.fn());
    const posts = fetcher.mock.calls.filter(call => String(call[0]).endsWith('/documents'));
    expect(posts).toHaveLength(3);
    expect(posts.map(call => (call[1]!.body as FormData).get('expected_revision'))).toEqual(['2', '3', '3']);
    expect(posts[0][1]?.headers).not.toHaveProperty('content-type');
  });
  it('rejects empty or unsupported files before making a request', async () => {
    const fetcher = vi.fn<typeof fetch>();
    const api = new OnboardingApiClient(fetcher);
    await expect(api.uploadDocument(application, { key: 'bad', type: 'BUSINESS_DOCUMENT', file: new File([], 'empty.pdf', { type: 'application/pdf' }) })).rejects.toMatchObject({ code: 'INVALID_DOCUMENT' });
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe('treasury onboarding read integration', () => {
  it('uses the existing paginated company envelope and forwards an opaque cursor', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ companies: [company], next_cursor: 'opaque/next+page' }));
    const api = new OnboardingApiClient(fetcher);
    const page = await api.listCompanies('previous/page');
    expect(page.companies[0].id).toBe(company.id);
    const url = new URL(String(fetcher.mock.calls[0][0]), 'https://flowcard.test');
    expect(url.pathname).toBe('/api/companies');
    expect(url.searchParams.get('cursor')).toBe('previous/page');
    expect(fetcher.mock.calls[0][1]?.credentials).toBe('include');
  });
  it('queries applications only for the selected company and rejects mismatched records', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ applications: [application] }));
    const api = new OnboardingApiClient(fetcher);
    expect((await api.listApplications(company.id)).applications[0].id).toBe(application.id);
    const url = new URL(String(fetcher.mock.calls[0][0]), 'https://flowcard.test');
    expect(url.searchParams.get('company_id')).toBe(company.id);
    fetcher.mockResolvedValueOnce(response({ applications: [{ ...application, company_id: 'another' }] }));
    await expect(api.listApplications(company.id)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });
  it('establishes the original operator session without putting credentials in a URL', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ status: 'authenticated' }));
    await new OnboardingApiClient(fetcher).establishOperatorSession('synthetic-local-test-token');
    expect(fetcher.mock.calls[0][0]).toBe('/api/kit5/session');
    expect(fetcher.mock.calls[0][1]).toMatchObject({ method: 'POST', credentials: 'include' });
    expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({ token: 'synthetic-local-test-token' });
  });
  it('does not turn an invalid list response into an empty or mocked company list', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response([]));
    await expect(new OnboardingApiClient(fetcher).listCompanies()).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });
});


describe('internal review submission contract', () => {
  it('keeps readiness errors as errors and sends the stable command key and revision', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ code: 'VALIDATION_FAILED', errors: [] }, 422));
    await expect(new OnboardingApiClient(fetcher).submitApplication(application.id, application.revision, 'submit-key'))
      .rejects.toMatchObject({ code: 'VALIDATION_FAILED', status: 422 });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][0]).toBe('/api/onboarding-applications/app_test/submit');
    expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({ expected_revision: 2 });
    expect(fetcher.mock.calls[0][1]?.headers).toMatchObject({ 'Idempotency-Key': 'submit-key' });
    expect(application.status).toBe('DRAFT');
  });
  it('parses the 202 receipt separately from application status', async () => {
    const receipt = { code: 'OK', application_id: application.id, submission_id: 'sub_test' };
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(receipt, 202));
    expect(await new OnboardingApiClient(fetcher).submitApplication(application.id, application.revision, 'submit-key'))
      .toEqual(receipt);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});


describe('unconfigured document storage recovery', () => {
  it('does not classify an explicit pre-write storage rejection as an unknown write', () => {
    expect(isAmbiguousSaveFailure(new OnboardingApiError(503, 'STORAGE_UNAVAILABLE'))).toBe(false);
    expect(isAmbiguousSaveFailure(new OnboardingApiError(503, 'UNKNOWN_UPSTREAM_ERROR'))).toBe(true);
    expect(isAmbiguousSaveFailure(new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'))).toBe(true);
  });
  it('reuses the saved company and application when pending files are removed after storage failure', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(company, 201)).mockResolvedValueOnce(response(application, 201)).mockResolvedValueOnce(response(application)).mockResolvedValueOnce(response({code: 'STORAGE_UNAVAILABLE'}, 503));
    const api = new OnboardingApiClient(fetcher);
    const operation = new SaveApplication();
    const uploads = [{key: 'file', type: 'BUSINESS_DOCUMENT', file: new File(['%PDF-test'], 'test.pdf', {type: 'application/pdf'})}];
    await expect(operation.run(api, {legal_name: 'Example Ltd'}, details, uploads, vi.fn())).rejects.toMatchObject({code: 'STORAGE_UNAVAILABLE'});
    expect(await operation.run(api, {legal_name: 'Example Ltd'}, details, [], vi.fn())).toEqual(application);
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(operation.isUploaded('file')).toBe(false);
  });
});
