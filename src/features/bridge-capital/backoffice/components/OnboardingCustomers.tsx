import { useEffect, useRef, useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import type { ApplicationRecord, CompanyRecord } from '../../../../services/onboarding/contracts.js';
import { useIndustryOptions } from '../../../../hooks/useIndustryOptions.js';
import { PLATFORM_ROLE_OPTIONS, PRODUCT_OPTIONS, US_STATES } from '../../../../services/onboarding/options.js';
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
  const [detailBusy, setDetailBusy] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [appCursor, setAppCursor] = useState<string | undefined>();
  const detailRequest = useRef(0);
  const listRequest = useRef(0);

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

  async function openCompany(id: string) {
    const request = ++detailRequest.current;
    setDetailBusy(true); setDetailError(''); setCompany(null); setApplications([]); setApplication(null); setAppCursor(undefined);
    try {
      const [record, page] = await Promise.all([api.getCompany(id), api.listApplications(id)]);
      if (request !== detailRequest.current) return;
      setCompany(record); setApplications(page.applications); setAppCursor(page.next_cursor || undefined);
      if (page.applications.length > 0) {
        const first = await api.getApplication(page.applications[0].id);
        if (request === detailRequest.current) setApplication(first);
      }
    } catch { if (request === detailRequest.current) setDetailError('Company details could not be loaded. Please try again.'); }
    finally { if (request === detailRequest.current) setDetailBusy(false); }
  }

  async function openApplication(id: string) {
    const request = ++detailRequest.current;
    setDetailBusy(true); setDetailError(''); setApplication(null);
    try {
      const record = await api.getApplication(id);
      if (request === detailRequest.current) setApplication(record);
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
      setApplications(current => [...current, ...page.applications.filter(item => !current.some(old => old.id === item.id))]);
      setAppCursor(page.next_cursor || undefined);
    } catch { if (request === detailRequest.current) setDetailError('More applications could not be loaded.'); }
    finally { if (request === detailRequest.current) setDetailBusy(false); }
  }

  return <Card>
    <CardHeader title="Onboarding applications" meta="Live backend records" />
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
        <Td>{item.registration_country || 'Not provided'}</Td>
        <Td>{item.created_at ? new Date(item.created_at).toLocaleDateString() : '—'}</Td>
        <Td><Button onClick={() => void openCompany(item.id)} disabled={detailBusy}>Review application</Button></Td>
      </tr>)}</tbody>
    </Table></div>}
    {cursor && <Button onClick={() => void load(cursor)} disabled={busy}>Load more companies</Button>}
    {detailBusy && <p role="status">Loading details…</p>}
    {detailError && <p role="alert" style={{ color: color.critical }}>{detailError}</p>}
    {company && <section style={{ borderTop: `1px solid ${color.border}`, paddingTop: space.xl, marginTop: space.xl, display: 'grid', gap: space.lg }}>
      <CardHeader title={company.legal_name} meta={<Button onClick={() => { detailRequest.current++; setCompany(null); setApplication(null); setDetailBusy(false); }}>Close</Button>} />
      <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(100px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
        <dt>Company type</dt><dd style={{ margin: 0 }}>{company.entity_type || 'Not provided'}</dd>
        <dt>Incorporation</dt><dd style={{ margin: 0 }}>{[company.registration_country, US_STATES.find(state => state.value === company.registration_state)?.label ?? company.registration_state].filter(Boolean).join(' · ') || 'Not provided'}</dd>
        <dt>Website</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{company.business_profile?.website || 'Not provided'}</dd>
        <dt>Description</dt><dd style={{ margin: 0 }}>{company.business_profile?.description || 'Not provided'}</dd>
        <dt>Registered address</dt><dd style={{ margin: 0 }}>{Object.values(company.business_profile?.registered_address ?? {}).filter(Boolean).join(', ') || 'Not provided'}</dd>
        <dt>Registration identifiers</dt><dd style={{ margin: 0 }}>{company.registration_identifiers?.map(item => `${item.type}: ${item.value} (${item.issuing_country})`).join('; ') || 'Not provided'}</dd>
        <dt>People</dt><dd style={{ margin: 0 }}>{company.people?.map(person => `${person.first_name ?? ''} ${person.last_name ?? ''}${person.role ? ` — ${person.role}` : ''}`).join('; ') || 'Not provided'}</dd>
      </dl>
      <div style={{ display: 'grid', gap: space.md }}>
        {applications.length === 0 ? <p>No onboarding application is saved for this company yet.</p> : applications.map(item => <div key={item.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg }}>
          <span style={{ overflowWrap: 'anywhere' }}>{item.id}</span><Badge>{applicationStatus(item.status)}</Badge><Button onClick={() => void openApplication(item.id)} disabled={detailBusy}>Details</Button>
        </div>)}
        {appCursor && <Button onClick={() => void moreApplications()} disabled={detailBusy}>Load more applications</Button>}
      </div>
      {application && <section style={{ display: 'grid', gap: space.lg, padding: space.xl, background: color.surfaceSubtle, borderRadius: radius.lg }}>
        <ApplicationReviewActions key={application.id} application={application} onUpdated={updated => { setApplication(updated); setApplications(current => current.map(item => item.id === updated.id ? updated : item)); }} />
        <h3 style={{ margin: 0 }}>Application information — {company.legal_name}</h3>
        <span style={{ color: color.textMuted, fontSize: '12px', overflowWrap: 'anywhere' }}>{application.id}</span>
        <p style={{ margin: 0 }}>{applicationStatus(application.status)} · {application.status}</p>
        {application.status === 'DRAFT' && <p style={{ margin: 0, color: color.textSecondary }}>Information is saved with FlowCard. It has not been submitted to Airwallex.</p>}
        <p style={{ margin: 0 }}>Industry: {application.platform?.onboarding_details?.industry_category_code ? `${industries.find(option => option.value === application.platform?.onboarding_details?.industry_category_code)?.label ?? 'Industry name unavailable'} (${application.platform.onboarding_details.industry_category_code})` : 'Not provided in this application'}</p>
        <p style={{ margin: 0 }}>Intended use: {application.platform?.onboarding_details?.intended_products?.map(value => PRODUCT_OPTIONS.find(option => option.value === value)?.label ?? value).join(', ') || 'Not provided'}</p>
        <p style={{ margin: 0 }}>Estimated monthly revenue: {application.platform?.onboarding_details?.estimated_monthly_revenue.amount || 'Not provided'} {application.platform?.onboarding_details?.estimated_monthly_revenue.currency ?? ''}</p>
        {application.platform?.onboarding_details?.people?.map(person => <div key={person.person_id} style={{ display: 'grid', gap: space.md }}>
          <strong>{company.people?.filter(item => item.person_id === person.person_id).map(item => `${item.first_name ?? ''} ${item.last_name ?? ''}`).join('') || 'Associated person'}</strong>
          <span>{person.platform_roles?.map(role => PLATFORM_ROLE_OPTIONS.find(option => option.value === role)?.label ?? role).join(', ') || 'Relationship not provided'}</span>
          <span>{person.residential_address ? Object.values(person.residential_address).filter(Boolean).join(', ') : 'Residential address not provided'}</span>
        </div>)}
        <p style={{ margin: 0 }}>Primary contact: {company.people?.filter(person => person.person_id === application.platform?.onboarding_details?.primary_contact_person_id).map(person => `${person.first_name ?? ''} ${person.last_name ?? ''}`).join('') || 'Not provided'}</p>
        {company.people?.map(person => <div key={person.person_id} style={{ display: 'grid', gap: space.md }}>
          <strong>{person.first_name} {person.last_name} · {person.role}</strong>
          <span>Date of birth: {person.date_of_birth || 'Not provided'}</span>
          {person.identifications?.map(ident => <span key={ident.identification_id}>Identification: {ident.type} · {ident.issuing_country} · ending {ident.number?.slice(-4) || '—'}{ident.issued_on ? ` · Issued ${ident.issued_on}` : ''}{ident.expires_on ? ` · Expires ${ident.expires_on}` : ''}</span>)}
        </div>)}
        <p style={{ margin: 0 }}>Files: {application.documents?.map(item => item.file_name).join(', ') || 'None'}</p>
        <div><Badge tone="warning">Funding unavailable</Badge></div>
      </section>}
    </section>}
  </Card>;
}
