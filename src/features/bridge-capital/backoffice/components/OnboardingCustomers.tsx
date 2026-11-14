import { useEffect, useRef, useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import type { AddressRecord, ApplicationRecord, CompanyPersonRecord, CompanyRecord, SubmissionRecord } from '../../../../services/onboarding/contracts.js';
import { useIndustryOptions } from '../../../../hooks/useIndustryOptions.js';
import { COUNTRY_OPTIONS, CURRENCY_OPTIONS, DOCUMENT_TYPES, PLATFORM_ROLE_OPTIONS, PRODUCT_OPTIONS, US_STATES } from '../../../../services/onboarding/options.js';
import { applicationStatus } from '../../../../services/onboarding/contracts.js';
import { color, font, radius, space } from '../tokens.js';
import { Badge, Button, Card, CardHeader, Table, Td, Th } from './primitives.js';
import { ApplicationReviewActions } from './ApplicationReviewActions.js';

const control: CSSProperties = { color: color.text, background: color.surfaceSubtle, padding: space.lg, border: `1px solid ${color.borderStrong}`, borderRadius: radius.md, fontFamily: font.sans };

export function OnboardingCustomers() {
  const api = useRef(new OnboardingApiClient()).current;
  const industries = useIndustryOptions().options;
  const [companies, setCompanies] = useState<CompanyRecord[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const [showConnection, setShowConnection] = useState(false);
  const [token, setToken] = useState('');
  const [company, setCompany] = useState<CompanyRecord | null>(null);
  const [applications, setApplications] = useState<ApplicationRecord[]>([]);
  const [application, setApplication] = useState<ApplicationRecord | null>(null);
  const [submissions, setSubmissions] = useState<SubmissionRecord[]>([]);
  const [detailBusy, setDetailBusy] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [appCursor, setAppCursor] = useState<string | undefined>();
  const detailRequest = useRef(0);
  const listRequest = useRef(0);
  const applicationCompany = application?.company ?? (application?.status === 'DRAFT' ? company ?? undefined : undefined);

  async function load(next?: string) {
    const request = ++listRequest.current;
    setBusy(true); setError('');
    try {
      const page = await api.listCompanies(next);
      if (listRequest.current !== request) return;
      setCompanies(current => next ? [...current, ...page.companies.filter(item => !current.some(old => old.id === item.id))] : page.companies);
      setCursor(page.next_cursor || undefined);
    } catch (caught) {
      if (listRequest.current !== request) return;
      setCompanies([]); setCursor(undefined);
      setError(caught instanceof OnboardingApiError && caught.status === 401
        ? 'Sign in with an authorized internal session to view saved applications.'
        : 'Saved applications could not be loaded. Check the backend connection and try again.');
    } finally { if (listRequest.current === request) setBusy(false); }
  }

  useEffect(() => {
    void load();
    const reload = () => { void load(); };
    window.addEventListener('focus', reload);
    window.addEventListener('flowcard:onboarding-saved', reload);
    return () => { window.removeEventListener('focus', reload); window.removeEventListener('flowcard:onboarding-saved', reload); };
    // The API instance is stable; reads synchronize this panel with the server.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  async function connect(event: FormEvent) {
    event.preventDefault();
    setBusy(true); setError('');
    try {
      await api.establishOperatorSession(token);
      setToken(''); setShowConnection(false);
      await load();
    } catch {
      setToken(''); setError('Internal sign-in failed. Check the operator credential and backend configuration.'); setBusy(false);
    }
  }

  async function readSubmissionHistory(applicationID: string, companyID: string): Promise<SubmissionRecord[]> {
    const history = await api.listSubmissions(applicationID);
    if (history.some(submission => submission.company.id !== companyID)) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    return history;
  }

  async function openCompany(id: string) {
    const request = ++detailRequest.current;
    setDetailBusy(true); setDetailError(''); setCompany(null); setApplications([]); setApplication(null); setSubmissions([]); setAppCursor(undefined);
    try {
      const [record, page] = await Promise.all([api.getCompany(id), api.listApplications(id)]);
      if (request !== detailRequest.current) return;
      if (record.id !== id || page.applications.some(item => item.company_id !== id)) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      setCompany(record); setApplications(page.applications); setAppCursor(page.next_cursor || undefined);
      if (page.applications.length > 0) {
        const firstID = page.applications[0].id;
        const first = await api.getApplication(firstID);
        if (first.id !== firstID || first.company_id !== id) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
        if (request !== detailRequest.current) return;
        setApplication(first);
        try {
          const history = await readSubmissionHistory(firstID, id);
          if (request === detailRequest.current) setSubmissions(history);
        } catch {
          if (request === detailRequest.current) setDetailError('Application loaded, but its submission history could not be loaded.');
        }
      }
    } catch { if (request === detailRequest.current) setDetailError('Company details could not be loaded. Please try again.'); }
    finally { if (request === detailRequest.current) setDetailBusy(false); }
  }

  async function openApplication(id: string) {
    const request = ++detailRequest.current;
    setDetailBusy(true); setDetailError(''); setApplication(null); setSubmissions([]);
    try {
      const record = await api.getApplication(id);
      if (record.id !== id || (company && record.company_id !== company.id)) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      if (request !== detailRequest.current) return;
      setApplication(record);
      try {
        const history = await readSubmissionHistory(id, record.company_id);
        if (request === detailRequest.current) setSubmissions(history);
      } catch {
        if (request === detailRequest.current) setDetailError('Application loaded, but its submission history could not be loaded.');
      }
    } catch { if (request === detailRequest.current) setDetailError('Application details could not be loaded.'); }
    finally { if (request === detailRequest.current) setDetailBusy(false); }
  }

  async function moreApplications() {
    if (!company || !appCursor) return;
    const request = ++detailRequest.current;
    setDetailBusy(true); setDetailError('');
    try {
      const page = await api.listApplications(company.id, appCursor);
      if (request !== detailRequest.current) return;
      if (page.applications.some(item => item.company_id !== company.id)) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      setApplications(current => [...current, ...page.applications.filter(item => !current.some(old => old.id === item.id))]);
      setAppCursor(page.next_cursor || undefined);
    } catch { if (request === detailRequest.current) setDetailError('More applications could not be loaded.'); }
    finally { if (request === detailRequest.current) setDetailBusy(false); }
  }

  function handleApplicationUpdated(updated: ApplicationRecord) {
    if (!company || updated.id !== application?.id || updated.company_id !== company.id) {
      setDetailError('Application company linkage did not match the selected company.');
      return;
    }
    const request = detailRequest.current;
    setApplication(updated);
    setApplications(current => current.map(item => item.id === updated.id ? updated : item));
    void api.listSubmissions(updated.id).then(history => {
      if (request !== detailRequest.current) return;
      if (history.some(submission => submission.company.id !== company.id)) {
        throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      }
      setSubmissions(history);
    }).catch(() => {
      if (request === detailRequest.current) setDetailError('Submission history could not be refreshed.');
    });
  }

  return <Card>
    <CardHeader title="Onboarding applications" meta="Live backend records" />
    <p style={{ color: color.textMuted, marginTop: 0 }}>This view uses saved backend records. Demo portfolio customers remain in the separate Demo portfolio view.</p>
    <div style={{ display: 'flex', gap: space.lg, marginBottom: space.xl }}>
      <Button onClick={() => void load()} disabled={busy}>{busy ? 'Loading…' : 'Refresh'}</Button>
      <Button onClick={() => setShowConnection(value => !value)}>Internal session</Button>
    </div>
    {showConnection && <form onSubmit={connect} style={{ display: 'grid', gap: space.lg, marginBottom: space.xl }}>
      <label style={{ display: 'grid', gap: space.md }}>Operator credential
        <input type="password" required value={token} autoComplete="off" onChange={event => setToken(event.target.value)} style={control} />
      </label>
      <p style={{ color: color.textMuted, margin: 0 }}>For authorized team members and local integration testing. This is not a customer sign-in.</p>
      <button type="submit" disabled={busy || !token} style={{ ...control, cursor: 'pointer' }}>Connect</button>
    </form>}
    {error && <p role="alert" style={{ color: color.critical }}>{error}</p>}
    {!busy && !error && companies.length === 0 && <p style={{ color: color.textMuted }}>No companies have been saved yet.</p>}
    {companies.length > 0 && !error && <div style={{ overflowX: 'auto' }}><Table>
      <thead><tr><Th>Company</Th><Th>Country</Th><Th>Created</Th><Th>Application</Th></tr></thead>
      <tbody>{companies.map(item => <tr key={item.id}>
        <Td><strong>{item.legal_name}</strong>{item.trading_name && <div style={{ color: color.textMuted }}>{item.trading_name}</div>}</Td>
        <Td>{countryLabel(item.registration_country)}</Td>
        <Td>{item.created_at ? new Date(item.created_at).toLocaleDateString() : '—'}</Td>
        <Td><Button onClick={() => void openCompany(item.id)} disabled={detailBusy}>Review application</Button></Td>
      </tr>)}</tbody>
    </Table></div>}
    {cursor && <Button onClick={() => void load(cursor)} disabled={busy}>Load more companies</Button>}
    {detailBusy && <p role="status">Loading details…</p>}
    {detailError && <p role="alert" style={{ color: color.critical }}>{detailError}</p>}
    {company && <section style={{ borderTop: `1px solid ${color.border}`, paddingTop: space.xl, marginTop: space.xl, display: 'grid', gap: space.lg }}>
      <CardHeader title={company.legal_name} meta={<Button onClick={() => { detailRequest.current++; setCompany(null); setApplication(null); setSubmissions([]); setDetailBusy(false); }}>Close</Button>} />
      <CompanyDetails company={company} title="Company record" />
      <div style={{ display: 'grid', gap: space.md }}>
        {applications.length === 0 ? <p>No onboarding application is saved for this company yet.</p> : applications.map(item => <div key={item.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg }}>
          <span style={{ overflowWrap: 'anywhere' }}>{item.id}</span><Badge>{applicationStatus(item.status)}</Badge><Button onClick={() => void openApplication(item.id)} disabled={detailBusy}>Details</Button>
        </div>)}
        {appCursor && <Button onClick={() => void moreApplications()} disabled={detailBusy}>Load more applications</Button>}
      </div>
      {application && <section style={{ display: 'grid', gap: space.lg, padding: space.xl, background: color.surfaceSubtle, borderRadius: radius.lg }}>
        <ApplicationReviewActions key={application.id} application={application} onUpdated={handleApplicationUpdated} />
        <h3 style={{ margin: 0 }}>Application information — {company.legal_name}</h3>
        <span style={{ color: color.textMuted, fontSize: '12px', overflowWrap: 'anywhere' }}>{application.id}</span>
        <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(120px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
          <dt>Status</dt><dd style={{ margin: 0 }}>{applicationStatus(application.status)} · {application.status}</dd>
          <dt>Company ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{application.company_id}</dd>
          <dt>Revision</dt><dd style={{ margin: 0 }}>{application.revision}</dd>
          <dt>Created</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.created_at)}</dd>
          <dt>Updated</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.updated_at)}</dd>
          <dt>Closed</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.closed_at)}</dd>
          <dt>Creator</dt><dd style={{ margin: 0 }}>{application.creator || 'Not provided'}</dd>
          <dt>Operator review flag</dt><dd style={{ margin: 0 }}>{application.operator_review_flagged ? 'Flagged' : 'Not flagged'}</dd>
          <dt>Platform</dt><dd style={{ margin: 0 }}>{application.platform?.name || 'Not provided'}</dd>
          <dt>Environment</dt><dd style={{ margin: 0 }}>{application.platform?.environment || 'Not provided'}</dd>
          <dt>Platform account</dt><dd style={{ margin: 0 }}>{application.platform?.platform_account_id || 'Not provided'}</dd>
          <dt>Platform decision</dt><dd style={{ margin: 0 }}>{application.platform?.decision || 'Not provided'}</dd>
        </dl>
        {applicationCompany
          ? <CompanyDetails company={applicationCompany} title={application.company ? 'Application company snapshot' : 'Draft company (live record)'} />
          : <p role="alert" style={{ color: color.critical, margin: 0 }}>Application company snapshot was not returned.</p>}
        <ApplicationDetails application={application} company={applicationCompany} industries={industries} />
        <SubmissionHistory submissions={submissions} />
        <DocumentList documents={application.documents ?? []} company={applicationCompany} />
        <div><Badge tone="warning">Funding unavailable</Badge></div>
      </section>}
    </section>}
  </Card>;
}

function CompanyDetails({ company, title }: { company: CompanyRecord; title: string }) {
  const profile = company.business_profile;
  return <section style={{ display: 'grid', gap: space.md }}>
    <h4 style={{ margin: 0 }}>{title}</h4>
    <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
      <dt>Company ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{company.id}</dd>
      <dt>Company status</dt><dd style={{ margin: 0 }}>{company.status || 'Not provided'}</dd>
      <dt>Revision</dt><dd style={{ margin: 0 }}>{company.revision}</dd>
      <dt>Submitted history</dt><dd style={{ margin: 0 }}>{company.has_submitted_history === undefined ? 'Not provided' : company.has_submitted_history ? 'Yes' : 'No'}</dd>
      <dt>Created</dt><dd style={{ margin: 0 }}>{formatTimestamp(company.created_at)}</dd>
      <dt>Updated</dt><dd style={{ margin: 0 }}>{formatTimestamp(company.updated_at)}</dd>
      <dt>Legal name</dt><dd style={{ margin: 0 }}>{company.legal_name || 'Not provided'}</dd>
      <dt>Trading name</dt><dd style={{ margin: 0 }}>{company.trading_name || 'Not provided'}</dd>
      <dt>Company type</dt><dd style={{ margin: 0 }}>{company.entity_type || 'Not provided'}</dd>
      <dt>Country of incorporation</dt><dd style={{ margin: 0 }}>{countryLabel(company.registration_country)}</dd>
      <dt>State of incorporation</dt><dd style={{ margin: 0 }}>{company.registration_country === 'US' ? stateLabel(company.registration_state) : company.registration_state || 'Not provided'}</dd>
      <dt>Business description</dt><dd style={{ margin: 0 }}>{profile?.description || 'Not provided'}</dd>
      <dt>Company industry</dt><dd style={{ margin: 0 }}>{profile?.industry || 'Not provided'}</dd>
      <dt>Website</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{profile?.website || 'Not provided'}</dd>
      <dt>Business email</dt><dd style={{ margin: 0 }}>{profile?.email || 'Not provided'}</dd>
      <dt>Business phone</dt><dd style={{ margin: 0 }}>{profile?.phone || 'Not provided'}</dd>
      <dt>Goods and services</dt><dd style={{ margin: 0 }}>{profile?.products || 'Not provided'}</dd>
      <dt>Registered address</dt><dd style={{ margin: 0 }}>{formatAddress(profile?.registered_address)}</dd>
      <dt>Operating address</dt><dd style={{ margin: 0 }}>{formatAddress(profile?.operating_address)}</dd>
      <dt>Operating countries</dt><dd style={{ margin: 0 }}>{profile?.operating_countries?.map(countryLabel).join(', ') || 'Not provided'}</dd>
    </dl>

    <h5 style={{ margin: 0 }}>Registration identifiers</h5>
    {company.registration_identifiers?.length ? <ul style={{ margin: 0, paddingLeft: space.xl }}>
      {company.registration_identifiers.map((identifier, index) => <li key={`${identifier.type}-${identifier.issuing_country}-${index}`}>
        {identifier.type || 'Type not provided'} · {identifier.value || 'Value not provided'} · {countryLabel(identifier.issuing_country)}
      </li>)}
    </ul> : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}

    <h5 style={{ margin: 0 }}>People and identifications</h5>
    {company.people?.length ? company.people.map(person => <CompanyPersonDetails key={person.person_id} person={person} />) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}
  </section>;
}

function CompanyPersonDetails({ person }: { person: CompanyPersonRecord }) {
  const fullName = [person.first_name, person.middle_name, person.last_name].filter(Boolean).join(' ') || 'Name not provided';
  return <article style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
    <strong>{fullName}</strong>
    <span>Person ID: {person.person_id}</span>
    <span>Job title: {person.role || 'Not provided'}</span>
    <span>Date of birth: {person.date_of_birth || 'Not provided'}</span>
    <span>Nationality: {countryLabel(person.nationality)}</span>
    <span>Email: {person.email || 'Not provided'}</span>
    <span>Phone: {person.phone || 'Not provided'}</span>
    {person.identifications?.length ? person.identifications.map(identification => <span key={identification.identification_id}>
      Identification {identification.identification_id}: {identification.type || 'Type not provided'} · {countryLabel(identification.issuing_country)} · ending {identification.number?.slice(-4) || '—'} · issued {identification.issued_on || 'Not provided'} · expires {identification.expires_on || 'Not provided'}
    </span>) : <span>Identifications: Not provided</span>}
  </article>;
}

function ApplicationDetails({ application, company, industries }: { application: ApplicationRecord; company?: CompanyRecord; industries: { value: string; label: string }[] }) {
  const details = application.platform?.onboarding_details;
  const companyPeople = company?.people ?? [];
  const primaryContact = companyPeople.find(person => person.person_id === details?.primary_contact_person_id);
  return <section style={{ display: 'grid', gap: space.md }}>
    <h4 style={{ margin: 0 }}>Saved application details</h4>
    <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
      <dt>Company ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{application.company_id}</dd>
      <dt>Application status</dt><dd style={{ margin: 0 }}>{applicationStatus(application.status)} · {application.status}</dd>
      <dt>Revision</dt><dd style={{ margin: 0 }}>{application.revision}</dd>
      <dt>Created</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.created_at)}</dd>
      <dt>Updated</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.updated_at)}</dd>
      <dt>Closed</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.closed_at)}</dd>
      <dt>Creator</dt><dd style={{ margin: 0 }}>{application.creator || 'Not provided'}</dd>
      <dt>Operator review flag</dt><dd style={{ margin: 0 }}>{application.operator_review_flagged === undefined ? 'Not provided' : application.operator_review_flagged ? 'Flagged' : 'Not flagged'}</dd>
      <dt>Platform</dt><dd style={{ margin: 0 }}>{application.platform?.name || 'Not provided'}</dd>
      <dt>Environment</dt><dd style={{ margin: 0 }}>{application.platform?.environment || 'Not provided'}</dd>
      <dt>Platform account</dt><dd style={{ margin: 0 }}>{application.platform?.platform_account_id || 'Not provided'}</dd>
      <dt>Platform decision</dt><dd style={{ margin: 0 }}>{application.platform?.decision || 'Not provided'}</dd>
      <dt>Industry</dt><dd style={{ margin: 0 }}>{details?.industry_category_code ? `${industries.find(option => option.value === details.industry_category_code)?.label ?? details.industry_category_code} (${details.industry_category_code})` : 'Not provided'}</dd>
      <dt>Intended products</dt><dd style={{ margin: 0 }}>{details?.intended_products?.map(code => PRODUCT_OPTIONS.find(option => option.value === code)?.label ?? code).join(', ') || 'Not provided'}</dd>
      <dt>Estimated monthly revenue</dt><dd style={{ margin: 0 }}>{details?.estimated_monthly_revenue.amount || 'Not provided'}</dd>
      <dt>Revenue currency</dt><dd style={{ margin: 0 }}>{CURRENCY_OPTIONS.find(option => option.value === details?.estimated_monthly_revenue.currency)?.value || 'Not provided'}</dd>
      <dt>Primary contact</dt><dd style={{ margin: 0 }}>{primaryContact ? personName(primaryContact) : 'Not provided'}</dd>
      <dt>Terms and conditions</dt><dd style={{ margin: 0 }}>{details ? details.customer_agreements.agreed_to_terms_and_conditions ? 'Accepted' : 'Not accepted' : 'Not provided'}</dd>
      <dt>Data use</dt><dd style={{ margin: 0 }}>{details ? details.customer_agreements.agreed_to_data_usage ? 'Accepted' : 'Not accepted' : 'Not provided'}</dd>
      {details?.customer_agreements.terms_and_conditions && <>
        <dt>Agreement type</dt><dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.service_agreement_type || 'Not provided'}</dd>
        <dt>Agreement timestamp</dt><dd style={{ margin: 0 }}>{formatTimestamp(details.customer_agreements.terms_and_conditions.agreed_at)}</dd>
        <dt>Acceptance IP</dt><dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.device_data?.ip_address || 'Not provided'}</dd>
        <dt>Acceptance user agent</dt><dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.device_data?.user_agent || 'Not provided'}</dd>
      </>}
    </dl>
    <h5 style={{ margin: 0 }}>Application people</h5>
    {companyPeople.length ? companyPeople.map(person => {
      const applicationPerson = details?.people?.find(item => item.person_id === person.person_id);
      return <article key={person.person_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
        <strong>{personName(person)}</strong>
        <span>Person ID: {person.person_id}</span>
        <span>Platform roles: {applicationPerson?.platform_roles?.map(role => PLATFORM_ROLE_OPTIONS.find(option => option.value === role)?.label ?? role).join(', ') || 'Not provided'}</span>
        <span>Residential address: {formatAddress(applicationPerson?.residential_address)}</span>
      </article>;
    }) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}

    <h5 style={{ margin: 0 }}>Information requests</h5>
    {application.information_requests?.length ? application.information_requests.map(request => <article key={request.information_request_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
      <strong>{request.information_request_id} · {request.status}</strong>
      <span>Received: {formatTimestamp(request.received_at)} · Resolved: {formatTimestamp(request.resolved_at)}</span>
      {request.questions.map(question => {
        const person = question.person_id ? companyPeople.find(item => item.person_id === question.person_id) : undefined;
        return <span key={question.question_id}>{question.text}{question.person_id ? ` · ${person ? personName(person) : question.person_id}` : ''}</span>;
      })}
    </article>) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}
  </section>;
}

function SubmissionHistory({ submissions }: { submissions: SubmissionRecord[] }) {
  return <section style={{ display: 'grid', gap: space.md }}>
    <h4 style={{ margin: 0 }}>Submission history</h4>
    {submissions.length ? submissions.map(submission => <article key={submission.submission_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
      <strong>{submission.submission_id}</strong>
      <span>Type: {submission.type}</span>
      <span>Status: {submissionStatusLabel(submission.status)}</span>
      <span>Created: {formatTimestamp(submission.created_at)}</span>
      <span>Accepted: {formatTimestamp(submission.accepted_at)}</span>
    </article>) : <p style={{ margin: 0, color: color.textMuted }}>No submissions recorded.</p>}
  </section>;
}

function DocumentList({ documents, company }: { documents: NonNullable<ApplicationRecord['documents']>; company?: CompanyRecord }) {
  return <section style={{ display: 'grid', gap: space.md }}>
    <h4 style={{ margin: 0 }}>Saved documents</h4>
    {documents.length ? documents.map(document => {
      const person = company?.people.find(item => item.person_id === document.person_id);
      const identification = person?.identifications?.find(item => item.identification_id === document.identification_id);
      return <article key={document.document_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
        <strong>{document.file_name}</strong>
        <span>Document ID: {document.document_id}</span>
        <span>Classification: {DOCUMENT_TYPES.find(option => option.value === document.type)?.label ?? document.type}</span>
        <span>Content type: {document.content_type} · Size: {document.size_bytes.toLocaleString()} bytes</span>
        <span>Person: {person ? personName(person) : document.person_id || 'Not provided'}</span>
        <span>Identification: {identification?.type ?? document.identification_id ?? 'Not provided'}</span>
        <span>Uploaded by: {document.uploaded_by} · {formatTimestamp(document.uploaded_at)}</span>
      </article>;
    }) : <p style={{ margin: 0, color: color.textMuted }}>No documents saved.</p>}
  </section>;
}

function personName(person: CompanyPersonRecord): string {
  return [person.first_name, person.middle_name, person.last_name].filter(Boolean).join(' ') || 'Name not provided';
}

function countryLabel(country?: string): string {
  if (!country) return 'Not provided';
  return COUNTRY_OPTIONS.find(option => option.value === country)?.label ?? country;
}

function stateLabel(state?: string): string {
  if (!state) return 'Not provided';
  return US_STATES.find(option => option.value === state)?.label ?? state;
}

function formatAddress(address?: AddressRecord): string {
  if (!address) return 'Not provided';
  const state = address.country === 'US' ? stateLabel(address.state) : address.state || 'Not provided';
  return [address.line_1, address.line_2, address.city, state, address.postal_code, countryLabel(address.country)]
    .filter((value): value is string => Boolean(value) && value !== 'Not provided')
    .join(', ') || 'Not provided';
}

function formatTimestamp(value?: string): string {
  if (!value) return 'Not provided';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function submissionStatusLabel(status: string): string {
  switch (status) {
    case 'PENDING': return 'Pending processing';
    case 'ACCEPTED': return 'Accepted for processing (not final approval)';
    case 'FAILED': return 'Failed';
    case 'UNRESOLVED': return 'Outcome unresolved';
    default: return status;
  }
}
