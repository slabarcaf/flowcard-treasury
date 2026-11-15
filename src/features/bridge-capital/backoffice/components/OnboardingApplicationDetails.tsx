import type {
  AddressRecord,
  ApplicationRecord,
  CompanyPersonRecord,
  CompanyRecord,
  SubmissionRecord,
} from '../../../../services/onboarding/contracts.js';
import { applicationStatus } from '../../../../services/onboarding/contracts.js';
import {
  COUNTRY_OPTIONS,
  CURRENCY_OPTIONS,
  DOCUMENT_TYPES,
  PLATFORM_ROLE_OPTIONS,
  PRODUCT_OPTIONS,
  US_STATES,
} from '../../../../services/onboarding/options.js';
import { useIndustryOptions } from '../../../../hooks/useIndustryOptions.js';
import { color, space } from '../tokens.js';
import { Badge, Button, Card, CardHeader } from './primitives.js';
import { ApplicationReviewActions } from './ApplicationReviewActions.js';
import { formatTimestamp, submissionStatusLabel } from './onboarding-format.js';

export function OnboardingApplicationDetails({
  application,
  liveDraftCompany,
  companyError,
  companyBusy,
  submissions,
  historyBusy,
  historyError,
  onUpdated,
  onRefresh,
  onRetryHistory,
  refreshBusy,
}: {
  application: ApplicationRecord;
  liveDraftCompany?: CompanyRecord;
  companyError: string;
  companyBusy: boolean;
  submissions: SubmissionRecord[];
  historyBusy: boolean;
  historyError: string;
  onUpdated: (value: ApplicationRecord) => void;
  onRefresh: () => void;
  onRetryHistory: () => void;
  refreshBusy: boolean;
}) {
  const industries = useIndustryOptions().options;
  const company = application.company ?? (application.status === 'DRAFT' ? liveDraftCompany : undefined);

  return (
    <Card>
      <CardHeader
        title="Application details"
        meta={<Button onClick={onRefresh} disabled={refreshBusy}>Refresh application</Button>}
      />
      <ApplicationSummary application={application} />
      <div style={{ marginTop: space.xl }}>
        <ApplicationReviewActions
          key={application.id}
          application={application}
          onUpdated={onUpdated}
        />
      </div>

      {company ? (
        <div style={{ marginTop: space.xl }}>
          <CompanyDetails
            company={company}
            title={application.status === 'DRAFT' ? 'Draft company (live record)' : 'Application company snapshot'}
          />
        </div>
      ) : application.status === 'DRAFT' && companyBusy ? (
        <p role="status" style={{ color: color.textMuted, marginTop: space.xl }}>
          Loading current company details…
        </p>
      ) : (
        <p role="alert" style={{ color: color.critical, marginTop: space.xl }}>
          {companyError || (application.status === 'DRAFT'
            ? 'Current company details are unavailable.'
            : 'Historical company details are unavailable for this application.')}
        </p>
      )}

      <div style={{ marginTop: space.xl }}>
        <ApplicationDetails application={application} company={company} industries={industries} />
      </div>
      <div style={{ marginTop: space.xl }}>
        <DocumentsAndRequests application={application} company={company} />
      </div>
      <div style={{ marginTop: space.xl }}>
        <SubmissionHistory
          submissions={submissions}
          busy={historyBusy}
          error={historyError}
          onRetry={onRetryHistory}
        />
      </div>
      <div style={{ marginTop: space.xl }}>
        <Badge tone="warning">Funding unavailable</Badge>
      </div>
    </Card>
  );
}

function ApplicationSummary({ application }: { application: ApplicationRecord }) {
  return (
    <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
      <dt>Application ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{application.id}</dd>
      <dt>Status</dt><dd style={{ margin: 0 }}>{applicationStatus(application.status)} · {application.status}</dd>
      <dt>Revision</dt><dd style={{ margin: 0 }}>{application.revision}</dd>
      <dt>Created</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.created_at)}</dd>
      <dt>Updated</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.updated_at)}</dd>
      <dt>Closed</dt><dd style={{ margin: 0 }}>{formatTimestamp(application.closed_at)}</dd>
      <dt>Creator</dt><dd style={{ margin: 0 }}>{application.creator || 'Not provided'}</dd>
      <dt>Operator review flag</dt>
      <dd style={{ margin: 0 }}>
        {application.operator_review_flagged === undefined
          ? 'Not provided'
          : application.operator_review_flagged ? 'Flagged' : 'Not flagged'}
      </dd>
    </dl>
  );
}

function CompanyDetails({ company, title }: { company: CompanyRecord; title: string }) {
  const profile = company.business_profile;
  return (
    <section style={{ display: 'grid', gap: space.md }}>
      <h3 style={{ margin: 0 }}>{title}</h3>
      <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
        <dt>Company ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{company.id}</dd>
        <dt>Company status</dt><dd style={{ margin: 0 }}>{company.status || 'Not provided'}</dd>
        <dt>Revision</dt><dd style={{ margin: 0 }}>{company.revision}</dd>
        <dt>Submitted history</dt>
        <dd style={{ margin: 0 }}>
          {company.has_submitted_history === undefined ? 'Not provided' : company.has_submitted_history ? 'Yes' : 'No'}
        </dd>
        <dt>Created</dt><dd style={{ margin: 0 }}>{formatTimestamp(company.created_at)}</dd>
        <dt>Updated</dt><dd style={{ margin: 0 }}>{formatTimestamp(company.updated_at)}</dd>
        <dt>Legal name</dt><dd style={{ margin: 0 }}>{company.legal_name || 'Not provided'}</dd>
        <dt>Trading name</dt><dd style={{ margin: 0 }}>{company.trading_name || 'Not provided'}</dd>
        <dt>Company type</dt><dd style={{ margin: 0 }}>{company.entity_type || 'Not provided'}</dd>
        <dt>Country of incorporation</dt><dd style={{ margin: 0 }}>{countryLabel(company.registration_country)}</dd>
        <dt>State of incorporation</dt>
        <dd style={{ margin: 0 }}>
          {company.registration_country === 'US'
            ? stateLabel(company.registration_state)
            : company.registration_state || 'Not provided'}
        </dd>
        <dt>Business description</dt><dd style={{ margin: 0 }}>{profile?.description || 'Not provided'}</dd>
        <dt>Company industry</dt><dd style={{ margin: 0 }}>{profile?.industry || 'Not provided'}</dd>
        <dt>Website</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{profile?.website || 'Not provided'}</dd>
        <dt>Business email</dt><dd style={{ margin: 0 }}>{profile?.email || 'Not provided'}</dd>
        <dt>Business phone</dt><dd style={{ margin: 0 }}>{profile?.phone || 'Not provided'}</dd>
        <dt>Goods and services</dt><dd style={{ margin: 0 }}>{profile?.products || 'Not provided'}</dd>
        <dt>Registered address</dt><dd style={{ margin: 0 }}>{formatAddress(profile?.registered_address)}</dd>
        <dt>Operating address</dt><dd style={{ margin: 0 }}>{formatAddress(profile?.operating_address)}</dd>
        <dt>Operating countries</dt>
        <dd style={{ margin: 0 }}>{profile?.operating_countries?.map(countryLabel).join(', ') || 'Not provided'}</dd>
      </dl>

      <h4 style={{ margin: 0 }}>Registration identifiers</h4>
      {company.registration_identifiers?.length ? (
        <ul style={{ margin: 0, paddingLeft: space.xl }}>
          {company.registration_identifiers.map((identifier, index) => (
            <li key={`${identifier.type}-${identifier.issuing_country}-${index}`}>
              {identifier.type || 'Type not provided'} · {identifier.value || 'Value not provided'} · {countryLabel(identifier.issuing_country)}
            </li>
          ))}
        </ul>
      ) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}

      <h4 style={{ margin: 0 }}>People and identifications</h4>
      {company.people?.length
        ? company.people.map(person => <CompanyPersonDetails key={person.person_id} person={person} />)
        : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}
    </section>
  );
}

function CompanyPersonDetails({ person }: { person: CompanyPersonRecord }) {
  return (
    <article style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
      <strong>{personName(person)}</strong>
      <span>Person ID: {person.person_id}</span>
      <span>Job title: {person.role || 'Not provided'}</span>
      <span>Date of birth: {person.date_of_birth || 'Not provided'}</span>
      <span>Nationality: {countryLabel(person.nationality)}</span>
      <span>Email: {person.email || 'Not provided'}</span>
      <span>Phone: {person.phone || 'Not provided'}</span>
      {person.identifications?.length ? person.identifications.map(identification => (
        <span key={identification.identification_id}>
          Identification {identification.identification_id}: {identification.type || 'Type not provided'} · {countryLabel(identification.issuing_country)} · ending {identification.number?.slice(-4) || '—'} · issued {identification.issued_on || 'Not provided'} · expires {identification.expires_on || 'Not provided'}
        </span>
      )) : <span>Identifications: Not provided</span>}
    </article>
  );
}

function ApplicationDetails({
  application,
  company,
  industries,
}: {
  application: ApplicationRecord;
  company?: CompanyRecord;
  industries: { value: string; label: string }[];
}) {
  const details = application.platform?.onboarding_details;
  const companyPeople = company?.people ?? [];
  const primaryContact = companyPeople.find(person => person.person_id === details?.primary_contact_person_id);

  return (
    <section style={{ display: 'grid', gap: space.md }}>
      <h3 style={{ margin: 0 }}>Platform and onboarding details</h3>
      <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(0,3fr)', gap: space.md, margin: 0 }}>
        <dt>Company ID</dt><dd style={{ margin: 0, overflowWrap: 'anywhere' }}>{application.company_id}</dd>
        <dt>Platform</dt><dd style={{ margin: 0 }}>{application.platform?.name || 'Not provided'}</dd>
        <dt>Environment</dt><dd style={{ margin: 0 }}>{application.platform?.environment || 'Not provided'}</dd>
        <dt>Platform account</dt><dd style={{ margin: 0 }}>{application.platform?.platform_account_id || 'Not provided'}</dd>
        <dt>Platform decision</dt><dd style={{ margin: 0 }}>{application.platform?.decision || 'Not provided'}</dd>
        <dt>Industry</dt>
        <dd style={{ margin: 0 }}>
          {details?.industry_category_code
            ? `${industries.find(option => option.value === details.industry_category_code)?.label ?? details.industry_category_code} (${details.industry_category_code})`
            : 'Not provided'}
        </dd>
        <dt>Intended products</dt>
        <dd style={{ margin: 0 }}>
          {details?.intended_products?.map(code => PRODUCT_OPTIONS.find(option => option.value === code)?.label ?? code).join(', ') || 'Not provided'}
        </dd>
        <dt>Estimated monthly revenue</dt><dd style={{ margin: 0 }}>{details?.estimated_monthly_revenue.amount || 'Not provided'}</dd>
        <dt>Revenue currency</dt>
        <dd style={{ margin: 0 }}>
          {CURRENCY_OPTIONS.find(option => option.value === details?.estimated_monthly_revenue.currency)?.value || 'Not provided'}
        </dd>
        <dt>Primary contact</dt><dd style={{ margin: 0 }}>{primaryContact ? personName(primaryContact) : 'Not provided'}</dd>
        <dt>Terms and conditions</dt>
        <dd style={{ margin: 0 }}>
          {details ? details.customer_agreements.agreed_to_terms_and_conditions ? 'Accepted' : 'Not accepted' : 'Not provided'}
        </dd>
        <dt>Data use</dt>
        <dd style={{ margin: 0 }}>
          {details ? details.customer_agreements.agreed_to_data_usage ? 'Accepted' : 'Not accepted' : 'Not provided'}
        </dd>
        {details?.customer_agreements.terms_and_conditions && <>
          <dt>Agreement type</dt>
          <dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.service_agreement_type || 'Not provided'}</dd>
          <dt>Agreement timestamp</dt>
          <dd style={{ margin: 0 }}>{formatTimestamp(details.customer_agreements.terms_and_conditions.agreed_at)}</dd>
          <dt>Acceptance IP</dt>
          <dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.device_data?.ip_address || 'Not provided'}</dd>
          <dt>Acceptance user agent</dt>
          <dd style={{ margin: 0 }}>{details.customer_agreements.terms_and_conditions.device_data?.user_agent || 'Not provided'}</dd>
        </>}
      </dl>

      <h4 style={{ margin: 0 }}>Application people</h4>
      {companyPeople.length ? companyPeople.map(person => {
        const applicationPerson = details?.people?.find(item => item.person_id === person.person_id);
        const primaryID = person.identifications?.find(item => item.identification_id === applicationPerson?.primary_identification_id);
        const secondaryID = person.identifications?.find(item => item.identification_id === applicationPerson?.secondary_identification_id);
        return (
          <article key={person.person_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
            <strong>{personName(person)}</strong>
            <span>Person ID: {person.person_id}</span>
            <span>Platform roles: {applicationPerson?.platform_roles?.map(role => PLATFORM_ROLE_OPTIONS.find(option => option.value === role)?.label ?? role).join(', ') || 'Not provided'}</span>
            <span>Primary ID: {primaryID ? `${primaryID.type || 'Identification'} · ending ${primaryID.number?.slice(-4) || '—'}` : 'Not provided'}</span>
            <span>Secondary ID: {secondaryID ? `${secondaryID.type || 'Identification'} · ending ${secondaryID.number?.slice(-4) || '—'}` : 'Not provided'}</span>
            <span>Residential address: {formatAddress(applicationPerson?.residential_address)}</span>
          </article>
        );
      }) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}
    </section>
  );
}

function DocumentsAndRequests({ application, company }: { application: ApplicationRecord; company?: CompanyRecord }) {
  const companyPeople = company?.people ?? [];
  return (
    <section style={{ display: 'grid', gap: space.xl }}>
      <h3 style={{ margin: 0 }}>Documents and information requests</h3>
      <section style={{ display: 'grid', gap: space.md }}>
        <h4 style={{ margin: 0 }}>Saved documents</h4>
        {application.documents?.length ? application.documents.map(document => {
          const person = companyPeople.find(item => item.person_id === document.person_id);
          const identification = person?.identifications?.find(item => item.identification_id === document.identification_id);
          return (
            <article key={document.document_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
              <strong>{document.file_name}</strong>
              <span>Document ID: {document.document_id}</span>
              <span>Classification: {DOCUMENT_TYPES.find(option => option.value === document.type)?.label ?? document.type}</span>
              <span>Content type: {document.content_type} · Size: {document.size_bytes.toLocaleString()} bytes</span>
              <span>Person: {person ? personName(person) : document.person_id || 'Not provided'}</span>
              <span>Identification: {identification?.type ?? document.identification_id ?? 'Not provided'}</span>
              <span>Uploaded by: {document.uploaded_by} · {formatTimestamp(document.uploaded_at)}</span>
            </article>
          );
        }) : <p style={{ margin: 0, color: color.textMuted }}>No documents saved.</p>}
      </section>

      <section style={{ display: 'grid', gap: space.md }}>
        <h4 style={{ margin: 0 }}>Information requests</h4>
        {application.information_requests?.length ? application.information_requests.map(request => (
          <article key={request.information_request_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
            <strong>{request.information_request_id} · {request.status}</strong>
            <span>Received: {formatTimestamp(request.received_at)} · Resolved: {formatTimestamp(request.resolved_at)}</span>
            {request.questions.map(question => {
              const person = question.person_id ? companyPeople.find(item => item.person_id === question.person_id) : undefined;
              return <span key={question.question_id}>{question.text}{question.person_id ? ` · ${person ? personName(person) : question.person_id}` : ''}</span>;
            })}
          </article>
        )) : <p style={{ margin: 0, color: color.textMuted }}>Not provided</p>}
      </section>
    </section>
  );
}

function SubmissionHistory({
  submissions,
  busy,
  error,
  onRetry,
}: {
  submissions: SubmissionRecord[];
  busy: boolean;
  error: string;
  onRetry: () => void;
}) {
  return (
    <section style={{ display: 'grid', gap: space.md }}>
      <h3 style={{ margin: 0 }}>Submission history</h3>
      {error ? (
        <div role="alert" style={{ display: 'grid', justifyItems: 'start', gap: space.md }}>
          <p style={{ margin: 0, color: color.critical }}>{error}</p>
          <Button onClick={onRetry} disabled={busy}>Retry history</Button>
        </div>
      ) : busy && submissions.length === 0 ? (
        <p role="status" style={{ margin: 0, color: color.textMuted }}>Loading submission history…</p>
      ) : submissions.length ? submissions.map(submission => (
        <article key={submission.submission_id} style={{ display: 'grid', gap: space.sm, borderTop: `1px solid ${color.border}`, paddingTop: space.md }}>
          <strong>{submission.submission_id}</strong>
          <span>Type: {submission.type}</span>
          <span>Status: {submissionStatusLabel(submission.status)}</span>
          <span>Created: {formatTimestamp(submission.created_at)}</span>
          <span>Accepted: {formatTimestamp(submission.accepted_at)}</span>
        </article>
      )) : <p style={{ margin: 0, color: color.textMuted }}>No submissions recorded.</p>}
    </section>
  );
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
