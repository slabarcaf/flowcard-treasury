import { useId, useRef, useState } from 'react';
import type { CompanyInput, DocumentUpload, OnboardingDetailsInput } from '../../../services/onboarding/contracts.js';
import { CURRENCY_OPTIONS, DOCUMENT_TYPES, PRODUCT_OPTIONS, US_STATES } from '../../../services/onboarding/options.js';
import { SelectField } from '../ApplicationFields.js';

export function ReviewApplicationStep({ company, details, uploads, onUploadsChange, isUploaded }: {
  company: CompanyInput; details: OnboardingDetailsInput; uploads: DocumentUpload[];
  onUploadsChange: (uploads: DocumentUpload[]) => void; isUploaded: (key: string) => boolean;
}) {
  const people = company.people ?? [];
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadHelpId = useId();
  const [dragging, setDragging] = useState(false);
  const [fileError, setFileError] = useState('');
  function addFiles(files: File[]) {
    if (files.some(file => !file.size || file.size > 10 * 1024 * 1024 || !['application/pdf', 'image/jpeg', 'image/png'].includes(file.type))) {
      setFileError('These files were not added. Choose non-empty PDF, JPEG or PNG files up to 10 MiB.');
      return;
    }
    setFileError('');
    onUploadsChange([...uploads, ...files.map(file => ({ key: crypto.randomUUID(), file, type: 'BUSINESS_DOCUMENT' }))]);
  }
  function updateUpload(key: string, patch: Partial<DocumentUpload>) { onUploadsChange(uploads.map(item => item.key === key ? { ...item, ...patch } : item)); }
  const address = company.business_profile?.registered_address;
  return <div className="space-y-6">
    <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-3 text-sm">
      <dt className="text-slate-400">Legal name</dt><dd>{company.legal_name}</dd>
      <dt className="text-slate-400">Trading name</dt><dd>{company.trading_name || 'Not provided'}</dd>
      <dt className="text-slate-400">Business website</dt><dd className="break-all">{company.business_profile?.website}</dd>
      <dt className="text-slate-400">Registered address</dt><dd>{[address?.line_1, address?.line_2, address?.city, US_STATES.find(state => state.value === address?.state)?.label, address?.postal_code, 'United States'].filter(Boolean).join(', ')}</dd>
      <dt className="text-slate-400">People</dt><dd>{people.map(person => `${person.first_name} ${person.last_name}`).join(', ')}</dd>
      <dt className="text-slate-400">Primary contact</dt><dd>{people.filter(person => person.id === details.primary_contact_person_id).map(person => `${person.first_name} ${person.last_name}`).join('')}</dd>
      <dt className="text-slate-400">Intended use</dt><dd>{PRODUCT_OPTIONS.filter(option => details.intended_products?.includes(option.value)).map(option => option.label).join(', ')}</dd>
      <dt className="text-slate-400">Monthly revenue</dt><dd>{details.estimated_monthly_revenue.amount} {CURRENCY_OPTIONS.find(option => option.value === details.estimated_monthly_revenue.currency)?.value}</dd>
    </dl>
    <section className="rounded-xl border border-slate-700 p-5 space-y-4">
      <h2 className="font-medium">Supporting documents</h2>
      <p className="text-xs text-slate-400">PDF, JPEG or PNG, up to 10 MiB per file. Additional documents may be requested during review.</p>
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
      {uploads.map(upload => <fieldset key={upload.key} disabled={isUploaded(upload.key)} className="space-y-3 border-t border-slate-700 pt-4 disabled:opacity-60">
        <div className="flex justify-between gap-3"><span className="text-sm break-all">{upload.file.name}{isUploaded(upload.key) ? ' · Uploaded' : ''}</span><button type="button" className="revolut-link" onClick={() => onUploadsChange(uploads.filter(item => item.key !== upload.key))}>Remove</button></div>
        <SelectField label="Document type" options={DOCUMENT_TYPES} value={upload.type} onChange={e => updateUpload(upload.key, { type: e.target.value, person_id: undefined, identification_id: undefined })} />
        {upload.type !== 'BUSINESS_DOCUMENT' && <SelectField label="Who does this document belong to?" options={people.map(person => ({ value: person.id, label: `${person.first_name} ${person.last_name}` }))} value={upload.person_id ?? ''} onChange={e => updateUpload(upload.key, { person_id: e.target.value, identification_id: undefined })} />}
        {upload.person_id && upload.type !== 'PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER' && <SelectField label="Identification" options={(people.find(person => person.id === upload.person_id)?.identifications ?? []).map(ident => ({ value: ident.id!, label: `${ident.type === 'DRIVERS_LICENSE' ? 'Driver’s license' : ident.type === 'PERSONAL_ID' ? 'National identity card' : ident.type === 'PASSPORT' ? 'Passport' : ident.type}` }))} value={upload.identification_id ?? ''} onChange={e => updateUpload(upload.key, { identification_id: e.target.value })} />}
      </fieldset>)}
    </section>
    <div className="rounded-xl border border-amber-400/30 p-4 text-sm text-amber-100 space-y-2">
      <p>Your information will be saved with FlowCard for review. It will not be sent to Airwallex at this step.</p>
      <p>Airwallex verification must be completed before funding can become available.</p>
    </div>
  </div>;
}
