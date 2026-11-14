import { useId, useRef, useState } from 'react';
import type { AddressInput, CompanyInput, DocumentUpload, FieldIssue, OnboardingDetailsInput, PersonInput, ReadinessResult } from '../../../services/onboarding/contracts.js';
import { COUNTRY_OPTIONS, CURRENCY_OPTIONS, DOCUMENT_TYPES, IDENTIFICATION_TYPES, PLATFORM_ROLE_OPTIONS, PRODUCT_OPTIONS, US_STATES } from '../../../services/onboarding/options.js';
import { useIndustryOptions } from '../../../hooks/useIndustryOptions.js';
import { SelectField } from '../ApplicationFields.js';

export function ReviewApplicationStep({ company, details, uploads, onUploadsChange, isUploaded, onEditStep, isSaving, fieldIssues = [], readiness, onVerifyAndSubmit }: {
  company: CompanyInput;
  details: OnboardingDetailsInput;
  uploads: DocumentUpload[];
  onUploadsChange: (uploads: DocumentUpload[]) => void;
  isUploaded: (key: string) => boolean;
  onEditStep: (step: number) => void;
  isSaving: boolean;
  fieldIssues?: FieldIssue[];
  readiness: ReadinessResult | null;
  onVerifyAndSubmit?: () => void;
}) {
  const people = company.people ?? [];
  const industryOptions = useIndustryOptions().options;
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadHelpId = useId();
  const [dragging, setDragging] = useState(false);
  const [fileError, setFileError] = useState('');
  const [revealedIDs, setRevealedIDs] = useState<Set<string>>(() => new Set());

  function addFiles(files: File[]) {
    if (files.some(file => !file.size || file.size > 10 * 1024 * 1024 || !['application/pdf', 'image/jpeg', 'image/png'].includes(file.type))) {
      setFileError('These files were not added. Choose non-empty PDF, JPEG or PNG files up to 10 MiB.');
      return;
    }
    setFileError('');
    onUploadsChange([...uploads, ...files.map(file => ({ key: crypto.randomUUID(), file, type: 'BUSINESS_DOCUMENT' }))]);
  }

  function updateUpload(key: string, patch: Partial<DocumentUpload>) {
    onUploadsChange(uploads.map(item => item.key === key ? { ...item, ...patch } : item));
  }

  function editButton(step: number, label: string) {
    return <button type="button" className="revolut-link" onClick={() => onEditStep(step)}>Edit {label}</button>;
  }

  function showAddress(address: AddressInput | undefined): string {
    if (!address) return 'Not provided';
    const country = COUNTRY_OPTIONS.find(option => option.value === address.country)?.label ?? address.country;
    const state = address.country === 'US'
      ? US_STATES.find(option => option.value === address.state)?.label ?? address.state
      : address.state;
    return [address.line_1, address.line_2, address.city, state, address.postal_code, country]
      .map(value => value?.trim())
      .filter(Boolean)
      .join(', ') || 'Not provided';
  }

  const profile = company.business_profile;
  const industryCode = details.industry_category_code?.trim();
  const industryLabel = industryOptions.find(option => option.value === industryCode)?.label;
  const primaryContact = people.find(person => person.id === details.primary_contact_person_id);

  return <div className="space-y-6">
    {readiness && readiness.code !== 'VALIDATION_PASSED' && <section role="alert" className="rounded-xl border border-red-400/40 p-4 text-sm text-red-100 space-y-2">
      <p>{readiness.message || readiness.code}</p>
      {fieldIssues.length > 0 && <ul className="list-disc pl-5 space-y-1">{fieldIssues.map((issue, index) => {
        const target = reviewStepForField(issue.field);
        return <li key={`${issue.field}-${index}`}>
          <code>{issue.field}</code>: {issue.message}
          {target && <button type="button" className="ml-2 revolut-link" onClick={() => onEditStep(target.step)}>Edit {target.label}</button>}
        </li>;
      })}</ul>}
    </section>}
    <section className="space-y-3">
      <div className="flex items-center justify-between"><h2 className="font-medium">Company</h2>{editButton(0, 'company details')}</div>
      <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-3 text-sm">
        <dt className="text-slate-400">Legal name</dt><dd>{company.legal_name.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Trading name</dt><dd>{company.trading_name?.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Company type</dt><dd>{company.entity_type === 'OTHER' ? company.entity_type_other?.trim() || 'Not provided' : company.entity_type?.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Country of incorporation</dt><dd>{(COUNTRY_OPTIONS.find(option => option.value === company.registration_country)?.label ?? company.registration_country?.trim()) || 'Not provided'}</dd>
        <dt className="text-slate-400">State of incorporation</dt><dd>{(US_STATES.find(option => option.value === company.registration_state)?.label ?? company.registration_state?.trim()) || 'Not provided'}</dd>
        <dt className="text-slate-400">Business website</dt><dd className="break-all">{profile?.website.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Business description</dt><dd>{profile?.description.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Registered address</dt><dd>{showAddress(profile?.registered_address)}</dd>
      </dl>
      <h3 className="text-sm font-medium">Registration identifiers</h3>
      {company.registration_identifiers?.length ? <ul className="space-y-1 text-sm">{company.registration_identifiers.map(identifier => <li key={identifier.client_id}>
        {identifier.type === 'OTHER' ? identifier.other_type?.trim() || 'Other registration' : identifier.type}: {identifier.value.trim() || 'Not provided'} ({COUNTRY_OPTIONS.find(option => option.value === identifier.issuing_country)?.label ?? (identifier.issuing_country || 'Not provided')})
      </li>)}</ul> : <p className="text-sm text-slate-400">Not provided</p>}
    </section>

    <section className="space-y-3">
      <div className="flex items-center justify-between"><h2 className="font-medium">People</h2>{editButton(1, 'people')}</div>
      {people.length ? people.map(person => <PersonSummary key={person.id} person={person} details={details} revealedIDs={revealedIDs}
        onToggleID={id => setRevealedIDs(current => {
          const next = new Set(current);
          if (next.has(id)) next.delete(id); else next.add(id);
          return next;
        })} />) : <p className="text-sm text-slate-400">Not provided</p>}
    </section>

    <section className="space-y-3">
      <div className="flex items-center justify-between"><h2 className="font-medium">Application</h2>{editButton(2, 'application details')}</div>
      <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-3 text-sm">
        <dt className="text-slate-400">Platform</dt><dd>Airwallex</dd>
        <dt className="text-slate-400">Environment</dt><dd>Sandbox</dd>
        <dt className="text-slate-400">Industry</dt><dd>{industryCode ? `${industryLabel ? `${industryLabel} (${industryCode})` : industryCode}` : 'Not provided'}</dd>
        <dt className="text-slate-400">Intended products</dt><dd>{details.intended_products?.length ? details.intended_products.map(code => PRODUCT_OPTIONS.find(option => option.value === code)?.label ?? code).join(', ') : 'Not provided'}</dd>
        <dt className="text-slate-400">Estimated monthly revenue</dt><dd>{details.estimated_monthly_revenue.amount?.trim() || 'Not provided'}</dd>
        <dt className="text-slate-400">Revenue currency</dt><dd>{CURRENCY_OPTIONS.find(option => option.value === details.estimated_monthly_revenue.currency)?.value ?? 'Not provided'}</dd>
        <dt className="text-slate-400">Primary contact</dt><dd>{primaryContact ? `${primaryContact.first_name.trim()} ${primaryContact.last_name.trim()}`.trim() || 'Not provided' : 'Not provided'}</dd>
        <dt className="text-slate-400">Terms and conditions</dt><dd>{details.customer_agreements.agreed_to_terms_and_conditions ? 'Accepted' : 'Not accepted'}</dd>
        <dt className="text-slate-400">Data use</dt><dd>{details.customer_agreements.agreed_to_data_usage ? 'Accepted' : 'Not accepted'}</dd>
      </dl>
    </section>

    <section className="rounded-xl border border-slate-700 p-5 space-y-4">
      <h2 className="font-medium">Supporting documents</h2>
      <p className="text-xs text-slate-400">PDF, JPEG or PNG, up to 10 MiB per file. Documents are optional for saving a draft.</p>
      <div
        className={`rounded-xl border-2 border-dashed p-7 text-center transition-colors ${dragging ? 'border-blue-400 bg-blue-500/10' : 'border-slate-500 bg-slate-800/40'}`}
        onDragOver={event => { event.preventDefault(); if (!fileInput.current?.matches(':disabled')) setDragging(true); }}
        onDragLeave={() => setDragging(false)}
        onDrop={event => {
          event.preventDefault(); setDragging(false);
          if (!fileInput.current?.matches(':disabled')) addFiles(Array.from(event.dataTransfer.files));
        }}
      >
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="mx-auto mb-3 h-9 w-9 text-slate-300"><path d="M12 16V4m-4 4 4-4 4 4M4 15v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4" /></svg>
        <button type="button" onClick={() => fileInput.current?.click()} aria-describedby={uploadHelpId} className="rounded-lg bg-white px-6 py-3 text-sm font-semibold text-slate-950 hover:bg-slate-200 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-400">Upload documents</button>
        <p id={uploadHelpId} className="mt-3 text-sm text-slate-400">Choose files or drag and drop them here.</p>
        <input ref={fileInput} type="file" hidden aria-label="Select supporting documents" accept="application/pdf,image/jpeg,image/png" multiple onChange={event => {
          addFiles(Array.from(event.currentTarget.files ?? []));
          event.currentTarget.value = '';
        }} />
      </div>
      {fileError && <p role="alert" className="text-sm text-red-200">{fileError}</p>}
      {uploads.length > 0 && <p role="status" className="text-sm text-slate-300">{uploads.length} {uploads.length === 1 ? 'document attached' : 'documents attached'}</p>}
      {uploads.map(upload => {
        const linkedPerson = people.find(person => person.id === upload.person_id);
        const linkedIdentification = linkedPerson?.identifications?.find(identification => identification.id === upload.identification_id);
        const documentType = DOCUMENT_TYPES.find(option => option.value === upload.type)?.label ?? upload.type;
        return <fieldset key={upload.key} disabled={isUploaded(upload.key)} className="space-y-3 border-t border-slate-700 pt-4 disabled:opacity-60">
          <div className="flex justify-between gap-3"><span className="text-sm break-all">{upload.file.name}{isUploaded(upload.key) ? ' · Uploaded' : ''}</span><button type="button" className="revolut-link" onClick={() => onUploadsChange(uploads.filter(item => item.key !== upload.key))}>Remove</button></div>
          <p className="text-xs text-slate-400">{documentType}{linkedPerson ? ` · ${linkedPerson.first_name} ${linkedPerson.last_name}` : ''}{linkedIdentification ? ` · ${identificationLabel(linkedIdentification)}` : ''}</p>
          <SelectField label="Document type" options={DOCUMENT_TYPES} value={upload.type} onChange={e => updateUpload(upload.key, { type: e.target.value, person_id: undefined, identification_id: undefined })} />
          {upload.type !== 'BUSINESS_DOCUMENT' && <SelectField label="Who does this document belong to?" options={people.map(person => ({ value: person.id, label: `${person.first_name} ${person.last_name}` }))} value={upload.person_id ?? ''} onChange={e => updateUpload(upload.key, { person_id: e.target.value || undefined, identification_id: undefined })} />}
          {upload.person_id && upload.type !== 'PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER' && <SelectField label="Identification" options={(people.find(person => person.id === upload.person_id)?.identifications ?? []).filter(ident => ident.id).map(ident => ({ value: ident.id!, label: identificationLabel(ident) }))} value={upload.identification_id ?? ''} onChange={e => updateUpload(upload.key, { identification_id: e.target.value || undefined })} />}
        </fieldset>;
      })}
    </section>

    <div className="rounded-xl border border-amber-400/30 p-4 text-sm text-amber-100 space-y-2">
      <p>Save draft stores this information with FlowCard. It does not submit the application to Airwallex.</p>
      <p>Before submission, selected files must upload successfully or be removed.</p>
      <p>A 202 response confirms the submission was queued for processing. It is not an approval.</p>
    </div>
    <div className="flex flex-col gap-3 sm:flex-row">
      <button type="submit" disabled={isSaving} className="revolut-btn-primary w-full disabled:opacity-40">{isSaving ? 'Saving draft…' : 'Save draft'}</button>
      <button type="button" disabled={!onVerifyAndSubmit || isSaving} onClick={onVerifyAndSubmit} className="revolut-btn-primary w-full disabled:opacity-40">Verify and submit</button>
    </div>
  </div>;
}

function PersonSummary({ person, details, revealedIDs, onToggleID }: {
  person: PersonInput;
  details: OnboardingDetailsInput;
  revealedIDs: Set<string>;
  onToggleID: (id: string) => void;
}) {
  const personDetails = details.people?.find(item => item.person_id === person.id);
  const roles = personDetails?.platform_roles ?? [];
  const residentialAddress = personDetails?.residential_address;

  return <article className="rounded-xl border border-slate-700 p-4 space-y-3">
    <h3 className="font-medium">{`${person.first_name.trim()} ${person.last_name.trim()}`.trim() || 'Person'}</h3>
    <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-2 text-sm">
      <dt className="text-slate-400">Job title</dt><dd>{person.role.trim() || 'Not provided'}</dd>
      <dt className="text-slate-400">Date of birth</dt><dd>{person.date_of_birth || 'Not provided'}</dd>
      <dt className="text-slate-400">Platform roles</dt><dd>{roles.length ? roles.map(role => PLATFORM_ROLE_OPTIONS.find(option => option.value === role)?.label ?? role).join(', ') : 'Not provided'}</dd>
      <dt className="text-slate-400">Residential address</dt><dd>{formatAddress(residentialAddress)}</dd>
    </dl>
    <h4 className="text-sm font-medium">Identifications</h4>
    {person.identifications?.length ? <ul className="space-y-2">{person.identifications.map((identification, index) => {
      const id = identification.id ?? `${person.id}-identification-${index}`;
      const revealed = revealedIDs.has(id);
      const number = identification.number.trim();
      const maskedNumber = number.length > 4 ? `•••• ${number.slice(-4)}` : '••••';
      return <li key={id} className="text-sm">
        <span>{identificationLabel(identification)} · {number ? revealed ? number : maskedNumber : 'Not provided'} · {COUNTRY_OPTIONS.find(option => option.value === identification.issuing_country)?.label ?? (identification.issuing_country || 'Not provided')}</span>
        {number && <button type="button" className="ml-2 revolut-link" aria-pressed={revealed} onClick={() => onToggleID(id)}>{revealed ? 'Hide number' : 'Reveal number'}</button>}
        <span className="block text-xs text-slate-400">Issued {identification.issued_on || 'Not provided'} · Expires {identification.expires_on || 'Not provided'}</span>
      </li>;
    })}</ul> : <p className="text-sm text-slate-400">Not provided</p>}
  </article>;
}

function identificationLabel(identification: { type: string; other_type?: string }): string {
  if (identification.type === 'OTHER') return identification.other_type?.trim() || 'Other identification';
  return IDENTIFICATION_TYPES.find(option => option.value === identification.type)?.label ?? (identification.type || 'Not provided');
}

function formatAddress(address: AddressInput | undefined): string {
  if (!address) return 'Not provided';
  const country = COUNTRY_OPTIONS.find(option => option.value === address.country)?.label ?? address.country;
  const state = address.country === 'US'
    ? US_STATES.find(option => option.value === address.state)?.label ?? address.state
    : address.state;
  return [address.line_1, address.line_2, address.city, state, address.postal_code, country]
    .map(value => value?.trim())
    .filter(Boolean)
    .join(', ') || 'Not provided';
}

function reviewStepForField(field: string): { step: number; label: string } | undefined {
  if (field.startsWith('documents')) return { step: 3, label: 'Documents' };
  if (field.startsWith('company.people')) return { step: 1, label: 'People' };
  if (field.startsWith('company.')) return { step: 0, label: 'Company' };
  if (field.startsWith('platform.onboarding_details')) return { step: 2, label: 'Application' };
  return undefined;
}
