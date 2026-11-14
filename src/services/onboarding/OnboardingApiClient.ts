import type { ApplicationPage, ApplicationRecord, CompanyInput, CreateApplicationRequest, CreateCompanyRequest, CompanyPage, CompanyRecord, DocumentUpload, FieldIssue, OnboardingDetailsInput, ReadinessResult, SubmissionListResponse, SubmissionReceipt, SubmissionRecord } from './contracts.js';
import { serializeApplicationDetails, serializeCompanyUpdateRequest, unsupportedCompanyClearIssues } from './payloads.js';

export class OnboardingApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly issues: FieldIssue[];
  readonly responseMessage?: string;
  constructor(
    status: number,
    code: string,
    issues: FieldIssue[] = [],
    responseMessage?: string,
  ) {
    const messages: Record<number, string> = {
      401: 'Your information has not been submitted. Please sign in to an authorized session, then try again.',
      403: 'This session is not permitted to save this application.',
      409: 'The saved record has changed or an application already exists. Refresh its status before continuing.',
      503: 'The onboarding service is not configured or is unavailable. Your entries are still on this page.',
    };
    super(code === 'STORAGE_UNAVAILABLE'
      ? 'Document upload is unavailable because file storage is not configured. Your company and application may already be saved. No file has been attached by this failed upload. For local testing, remove pending attachments and retry.'
      : code === 'ONBOARDING_GATEWAY_UNREACHABLE'
      ? 'We could not connect to the application service. Your information has not been submitted. Please try again when the service is available.'
      : messages[status] ?? (status === 422 || status === 400
      ? 'Please review the information listed below and try again.'
      : 'The request could not be completed. Check the saved status before retrying.'));
    this.name = 'OnboardingApiError';
    this.status = status;
    this.code = code;
    this.issues = issues;
    this.responseMessage = responseMessage;
  }
}

export class OnboardingApiClient {
  private readonly fetchImpl: typeof fetch;
  constructor(fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis)) { this.fetchImpl = fetchImpl; }

  private async request<T>(path: string, method = 'GET', payload?: unknown, extraHeaders: Record<string, string> = {}, expectedStatus?: number): Promise<T> {
    let response: Response;
    try {
      response = await this.fetchImpl(path, {
        method,
        credentials: 'include',
        signal: AbortSignal.timeout(30_000),
        headers: { accept: 'application/json', ...(payload === undefined || payload instanceof FormData ? {} : { 'content-type': 'application/json' }), ...extraHeaders },
        ...(payload === undefined ? {} : { body: payload instanceof FormData ? payload : JSON.stringify(payload) }),
      });
    } catch {
      throw new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN');
    }
    let value: unknown;
    try { value = await response.json(); } catch { throw new OnboardingApiError(response.status, 'INVALID_RESPONSE'); }
    const body = typeof value === 'object' && value !== null ? value as Record<string, unknown> : {};
    if (!response.ok) {
      const issues = Array.isArray(body.errors) ? body.errors.filter((issue): issue is FieldIssue =>
        typeof issue === 'object' && issue !== null && typeof issue.field === 'string' &&
        typeof issue.code === 'string' && typeof issue.message === 'string') : [];
      throw new OnboardingApiError(
        response.status,
        String(body.code ?? body.error ?? 'REQUEST_FAILED'),
        issues,
        typeof body.message === 'string' ? body.message : undefined,
      );
    }
    if (expectedStatus !== undefined && response.status !== expectedStatus) {
      throw new OnboardingApiError(response.status, 'INVALID_RESPONSE');
    }
    return value as T;
  }

  async establishOperatorSession(token: string): Promise<void> {
    await this.request('/api/kit5/session', 'POST', { token });
  }
  async listCompanies(cursor?: string): Promise<CompanyPage> {
    const query = new URLSearchParams({ limit: '100', ...(cursor ? { cursor } : {}) });
    const page = await this.request<CompanyPage>(`/api/companies?${query}`);
    if (!page || !Array.isArray(page.companies) || page.companies.some(item => !item || typeof item.id !== 'string') ||
        (page.next_cursor !== undefined && typeof page.next_cursor !== 'string')) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    return page;
  }
  async listApplications(companyId: string, cursor?: string): Promise<ApplicationPage> {
    const query = new URLSearchParams({ company_id: companyId, limit: '100', ...(cursor ? { cursor } : {}) });
    const page = await this.request<ApplicationPage>(`/api/onboarding-applications?${query}`);
    if (!page || !Array.isArray(page.applications) || page.applications.some(item => !item || item.company_id !== companyId || typeof item.status !== 'string') ||
        (page.next_cursor !== undefined && typeof page.next_cursor !== 'string')) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    return page;
  }
  async createCompany(input: CreateCompanyRequest): Promise<CompanyRecord> {
    return this.record(await this.request<CompanyRecord>('/api/companies', 'POST', input));
  }
  async updateCompany(company: CompanyRecord, input: CompanyInput, previous: CompanyInput): Promise<CompanyRecord> {
    const clearIssues = unsupportedCompanyClearIssues(previous, input);
    if (clearIssues.length) throw new OnboardingApiError(422, 'UNSUPPORTED_CLEAR', clearIssues);
    const payload = serializeCompanyUpdateRequest(input, company.revision);
    return this.record(await this.request<CompanyRecord>(`/api/companies/${encodeURIComponent(company.id)}`, 'PATCH', payload));
  }
  async createApplication(companyId: string, details: OnboardingDetailsInput): Promise<ApplicationRecord> {
    const payload: CreateApplicationRequest = {
      company_id: companyId,
      platform_name: 'AIRWALLEX',
      environment: 'sandbox',
      onboarding_details: serializeApplicationDetails(details),
    };
    return this.application(await this.request<ApplicationRecord>('/api/onboarding-applications', 'POST', payload));
  }
  async updateApplication(application: ApplicationRecord, details: OnboardingDetailsInput): Promise<ApplicationRecord> {
    return this.application(await this.request<ApplicationRecord>(`/api/onboarding-applications/${encodeURIComponent(application.id)}`, 'PATCH', {
      expected_revision: application.revision, onboarding_details: serializeApplicationDetails(details),
    }));
  }
  async getApplication(id: string): Promise<ApplicationRecord> {
    return this.application(await this.request<ApplicationRecord>(`/api/onboarding-applications/${encodeURIComponent(id)}`));
  }
  async listSubmissions(applicationId: string): Promise<SubmissionListResponse> {
    const submissions = await this.request<unknown>(`/api/onboarding-applications/${encodeURIComponent(applicationId)}/submissions`);
    if (!Array.isArray(submissions) || submissions.some(item => !isSubmissionRecord(item))) {
      throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    }
    return submissions as SubmissionRecord[];
  }
  async getCompany(id: string): Promise<CompanyRecord> {
    return this.record(await this.request<CompanyRecord>(`/api/companies/${encodeURIComponent(id)}`));
  }
  async submitApplication(applicationId: string, expectedRevision: number, idempotencyKey: string): Promise<SubmissionReceipt> {
    const receipt = await this.request<SubmissionReceipt>(
      `/api/onboarding-applications/${encodeURIComponent(applicationId)}/submit`,
      'POST',
      { expected_revision: expectedRevision },
      { 'Idempotency-Key': idempotencyKey },
      202,
    );
    if (receipt.code !== 'OK' || receipt.application_id !== applicationId ||
        typeof receipt.submission_id !== 'string' || !receipt.submission_id) {
      throw new OnboardingApiError(202, 'INVALID_RESPONSE');
    }
    return receipt;
  }
  async verify(id: string): Promise<ReadinessResult> {
    // 422 is a validation result, not a successful submission or an approval.
    try { return await this.request<ReadinessResult>(`/api/onboarding-applications/${encodeURIComponent(id)}/verify`, 'POST'); }
    catch (error) {
      if (error instanceof OnboardingApiError && error.status === 422) {
        return { code: error.code, message: error.responseMessage, errors: error.issues };
      }
      throw error;
    }
  }
  async uploadDocument(application: ApplicationRecord, upload: DocumentUpload): Promise<void> {
    if (upload.file.size <= 0 || upload.file.size > 10 * 1024 * 1024 ||
        !['application/pdf', 'image/jpeg', 'image/png'].includes(upload.file.type)) {
      throw new OnboardingApiError(422, 'INVALID_DOCUMENT', [{ field: 'documents', code: 'invalid_document', message: 'Choose a non-empty PDF, JPEG or PNG file up to 10 MiB.' }]);
    }
    const form = new FormData();
    form.set('type', upload.type);
    form.set('expected_revision', String(application.revision));
    if (upload.person_id) form.set('person_id', upload.person_id);
    if (upload.identification_id) form.set('identification_id', upload.identification_id);
    form.set('file', upload.file);
    await this.request(`/api/onboarding-applications/${encodeURIComponent(application.id)}/documents`, 'POST', form);
  }
  private record<T extends { id: string; revision: number }>(value: T): T {
    if (!value || typeof value.id !== 'string' || !value.id || !Number.isInteger(value.revision) || value.revision < 1) {
      throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    }
    return value;
  }
  private application(value: ApplicationRecord): ApplicationRecord {
    this.record(value);
    if (typeof value.status !== 'string' || typeof value.company_id !== 'string') {
      throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    }
    return value;
  }
}

function isSubmissionRecord(value: unknown): value is SubmissionRecord {
  if (!value || typeof value !== 'object') return false;
  const record = value as Record<string, unknown>;
  const company = record.company;
  if (!company || typeof company !== 'object') return false;
  const companyRecord = company as Record<string, unknown>;
  return typeof record.submission_id === 'string' && typeof record.type === 'string' &&
    typeof record.status === 'string' && typeof record.created_at === 'string' && typeof companyRecord.id === 'string';
}

/** Only explicit pre-write failures can be retried without reconciling an unknown write. */
export function isAmbiguousSaveFailure(error: OnboardingApiError): boolean {
  if (error.code === 'ONBOARDING_GATEWAY_UNREACHABLE' || error.code === 'STORAGE_UNAVAILABLE') return false;
  return error.status === 0 || error.status >= 500 || error.code === 'INVALID_RESPONSE';
}
