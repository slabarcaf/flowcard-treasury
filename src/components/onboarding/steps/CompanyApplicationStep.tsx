import { emptyAddress } from '../../../services/onboarding/contracts.js';
import type { CompanyInput, FieldIssue, RegistrationIdentifierInput } from '../../../services/onboarding/contracts.js';
import { newRegistrationIdentifier } from '../../../services/onboarding/formRules.js';
import { COUNTRY_OPTIONS, REGISTRATION_COUNTRIES, US_STATES } from '../../../services/onboarding/options.js';
import { AddressFields, FieldIssueMessages, OptionalSection, SelectField, TextField } from '../ApplicationFields.js';

const COMPANY_TYPES = [
  { value: 'LLC', label: 'Limited liability company' },
  { value: 'CORPORATION', label: 'Corporation' },
  { value: 'PARTNERSHIP', label: 'Partnership' },
  { value: 'SOLE_PROPRIETORSHIP', label: 'Sole proprietorship' },
  { value: 'OTHER', label: 'Other' },
];

const REGISTRATION_TYPES = [
  { value: 'EIN', label: 'Employer Identification Number (EIN)' },
  { value: 'OTHER', label: 'Other' },
];

function updateRegistration(
  value: CompanyInput,
  onChange: (value: CompanyInput) => void,
  clientId: string,
  patch: Partial<RegistrationIdentifierInput>,
) {
  onChange({
    ...value,
    registration_identifiers: (value.registration_identifiers ?? []).map(identifier =>
      identifier.client_id === clientId ? { ...identifier, ...patch } : identifier),
  });
}

export function CompanyApplicationStep({ value, onChange, savedCompany, allowProfileValueClears = false, fieldIssues = [] }: { value: CompanyInput; onChange: (value: CompanyInput) => void; savedCompany?: CompanyInput | null; allowProfileValueClears?: boolean; fieldIssues?: FieldIssue[] }) {
  const profile = value.business_profile;
  const profileSaved = Boolean(savedCompany?.business_profile);
  const registeredAddressSaved = Boolean(savedCompany?.business_profile?.registered_address);
  const registrations = value.registration_identifiers ?? [];
  const entityTypeSelection = value.entity_type === 'OTHER' || value.entity_type_other
    ? 'OTHER'
    : value.entity_type ?? '';

  return <div className="space-y-5">
    <TextField label="Legal company name" apiField="company.legal_name" fieldIssues={fieldIssues} minLength={2} maxLength={200} value={value.legal_name} onChange={e => onChange({ ...value, legal_name: e.target.value })} />
    <TextField label="Trading / display name" apiField="company.trading_name" fieldIssues={fieldIssues} optional maxLength={200} value={value.trading_name ?? ''} onChange={e => onChange({ ...value, trading_name: e.target.value })} />
    <SelectField label="Company type" apiField="company.entity_type" fieldIssues={fieldIssues} optional options={COMPANY_TYPES} value={entityTypeSelection} onChange={e => onChange({
      ...value,
      entity_type: e.target.value === 'OTHER' ? 'OTHER' : e.target.value,
      entity_type_other: e.target.value === 'OTHER' ? value.entity_type_other ?? '' : undefined,
    })} />
    {entityTypeSelection === 'OTHER' && <TextField label="Other company type" apiField="company.entity_type" fieldIssues={fieldIssues} maxLength={50} value={value.entity_type_other ?? ''} onChange={e => onChange({ ...value, entity_type_other: e.target.value })} />}
    <SelectField label="Country of incorporation" apiField="company.registration_country" fieldIssues={fieldIssues} optional options={REGISTRATION_COUNTRIES} value={value.registration_country ?? ''} onChange={e => onChange({ ...value, registration_country: e.target.value })} />
    <p className="text-xs text-slate-400">This intake currently focuses on US-registered businesses. The API accepts ISO country codes, but readiness outside the US has not been verified.</p>
    <SelectField label="State of incorporation" apiField="company.registration_state" fieldIssues={fieldIssues} optional options={US_STATES} value={value.registration_state ?? ''} onChange={e => onChange({ ...value, registration_state: e.target.value })} />

    <OptionalSection title="Business profile" checked={Boolean(profile)} disabled={profileSaved}
      help={profileSaved ? 'The current API cannot remove the saved profile object. After saving, its website and description can be cleared individually.' : undefined}
      onChange={enabled => onChange({
      ...value,
      business_profile: enabled ? profile ?? { website: '', description: '' } : undefined,
    })}>
      <TextField label="Business website" apiField="company.business_profile.website" fieldIssues={fieldIssues} optional={allowProfileValueClears} type="url" placeholder="https://example.com" maxLength={500} value={profile?.website ?? ''} onChange={e => onChange({ ...value, business_profile: { ...profile!, website: e.target.value } })} />
      <label className="block text-sm text-slate-300 space-y-2">
        <span>Business description *</span>
        <textarea required={!allowProfileValueClears} maxLength={2000} rows={4} aria-invalid={fieldIssues.some(issue => issue.field === 'company.business_profile.description')} className="revolut-input w-full" value={profile?.description ?? ''} onChange={e => onChange({ ...value, business_profile: { ...profile!, description: e.target.value } })} />
        {fieldIssues.filter(issue => issue.field === 'company.business_profile.description').map((issue, index) => <p key={index} role="alert" className="text-xs text-red-200">{issue.message}</p>)}
      </label>
      <OptionalSection title="Registered address" checked={Boolean(profile?.registered_address)} disabled={registeredAddressSaved}
        help={registeredAddressSaved ? 'The current API cannot remove a saved registered address.' : undefined}
        onChange={enabled => onChange({
        ...value,
        business_profile: {
          ...profile!,
          registered_address: enabled ? profile?.registered_address ?? { ...emptyAddress(), country: 'US' } : undefined,
        },
      })}>
        {profile?.registered_address && <AddressFields usOnly fieldPrefix="company.business_profile.registered_address" fieldIssues={fieldIssues} value={profile.registered_address} onChange={address => onChange({ ...value, business_profile: { ...profile!, registered_address: address } })} />}
      </OptionalSection>
    </OptionalSection>

    <section className="rounded-xl border border-slate-700 p-5 space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div><h2 className="font-medium">Registration identifiers</h2><p className="text-xs text-slate-400">Optional. Add EIN or another government registration number.</p></div>
        <button type="button" className="revolut-link" onClick={() => onChange({ ...value, registration_identifiers: [...registrations, newRegistrationIdentifier()] })}>Add registration</button>
      </div>
      <FieldIssueMessages issues={fieldIssues} field="company.registration_identifiers" />
      {registrations.map((identifier, index) => {
        const type = identifier.type === 'OTHER' ? identifier.other_type ?? '' : identifier.type;
        const isUSEIN = identifier.type === 'EIN' && identifier.issuing_country === 'US';
        return <div key={identifier.client_id} className="space-y-4 border-t border-slate-700 pt-4">
          <div className="flex items-center justify-between"><h3 className="text-sm font-medium">Registration {index + 1}</h3><button type="button" className="revolut-link" onClick={() => onChange({ ...value, registration_identifiers: registrations.filter(item => item.client_id !== identifier.client_id) })}>Remove</button></div>
          <SelectField label="Registration type" apiField={`company.registration_identifiers[${index}].type`} fieldIssues={fieldIssues} options={REGISTRATION_TYPES} value={identifier.type} onChange={e => updateRegistration(value, onChange, identifier.client_id, { type: e.target.value, other_type: e.target.value === 'OTHER' ? identifier.other_type ?? '' : undefined })} />
          {identifier.type === 'OTHER' && <TextField label="Other registration type" apiField={`company.registration_identifiers[${index}].type`} fieldIssues={fieldIssues} maxLength={50} value={identifier.other_type ?? ''} onChange={e => updateRegistration(value, onChange, identifier.client_id, { other_type: e.target.value })} />}
          <TextField label={isUSEIN ? 'EIN' : 'Registration value'} apiField={`company.registration_identifiers[${index}].value`} fieldIssues={fieldIssues} inputMode={isUSEIN ? 'numeric' : 'text'} pattern={isUSEIN ? '[0-9]{2}-?[0-9]{7}' : undefined} placeholder={isUSEIN ? '12-3456789' : undefined} title={isUSEIN ? 'Enter 9 digits, with an optional hyphen after the first 2 digits.' : undefined} maxLength={100} value={identifier.value} onChange={e => updateRegistration(value, onChange, identifier.client_id, { value: e.target.value })} />
          <SelectField label="Issuing country" apiField={`company.registration_identifiers[${index}].issuing_country`} fieldIssues={fieldIssues} options={COUNTRY_OPTIONS} value={identifier.issuing_country} onChange={e => updateRegistration(value, onChange, identifier.client_id, { issuing_country: e.target.value })} />
          {identifier.type === 'OTHER' && !type && <p className="text-xs text-slate-400">Enter the registration type above.</p>}
        </div>;
      })}
    </section>
  </div>;
}
