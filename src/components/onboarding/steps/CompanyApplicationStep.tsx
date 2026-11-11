import { emptyAddress } from '../../../services/onboarding/contracts.js';
import type { CompanyInput } from '../../../services/onboarding/contracts.js';
import { REGISTRATION_COUNTRIES, US_STATES } from '../../../services/onboarding/options.js';
import { AddressFields, SelectField, TextField } from '../ApplicationFields.js';

export function CompanyApplicationStep({ value, onChange }: { value: CompanyInput; onChange: (value: CompanyInput) => void }) {
  const profile = value.business_profile ?? { website: '', description: '', registered_address: { ...emptyAddress(), country: 'US' } };
  const identifier = value.registration_identifiers?.[0] ?? { type: 'EIN', value: '', issuing_country: 'US' };
  return <div className="space-y-5">
    <TextField label="Legal company name" minLength={2} maxLength={200} value={value.legal_name} onChange={e => onChange({ ...value, legal_name: e.target.value })} />
    <TextField label="Trading / display name" optional maxLength={200} value={value.trading_name ?? ''} onChange={e => onChange({ ...value, trading_name: e.target.value })} />
    <SelectField label="Company type" options={[
      { value: 'LLC', label: 'Limited liability company' }, { value: 'CORPORATION', label: 'Corporation' },
      { value: 'PARTNERSHIP', label: 'Partnership' }, { value: 'SOLE_PROPRIETORSHIP', label: 'Sole proprietorship' },
    ]} value={value.entity_type ?? ''} onChange={e => onChange({ ...value, entity_type: e.target.value })} />
    <SelectField label="Country of incorporation" options={REGISTRATION_COUNTRIES} value={value.registration_country ?? ''} onChange={e => onChange({ ...value, registration_country: e.target.value })} />
    <p className="text-xs text-slate-400">Currently available for US-registered businesses.</p>
    <SelectField label="State of incorporation" optional options={US_STATES} value={value.registration_state ?? ''} onChange={e => onChange({ ...value, registration_state: e.target.value })} />
    <TextField label="Business website" type="url" placeholder="https://example.com" maxLength={500} value={profile.website} onChange={e => onChange({ ...value, business_profile: { ...profile, website: e.target.value } })} />
    <label className="block text-sm text-slate-300 space-y-2"><span>Business description *</span><textarea required maxLength={2000} rows={4} placeholder="Tell us what your company does." className="revolut-input w-full" value={profile.description} onChange={e => onChange({ ...value, business_profile: { ...profile, description: e.target.value } })} /></label>
    <section className="rounded-xl border border-slate-700 p-5 space-y-4">
      <h2 className="font-medium">Registered business address</h2>
      <AddressFields requireState usOnly value={profile.registered_address ?? { ...emptyAddress(), country: 'US' }} onChange={address => onChange({ ...value, business_profile: { ...profile, registered_address: address } })} />
    </section>
    <section className="rounded-xl border border-slate-700 p-5 space-y-4">
      <h2 className="font-medium">Tax registration</h2>
      <SelectField label="Registration type" options={[{ value: 'EIN', label: 'Employer Identification Number (EIN)' }]} value={identifier.type} onChange={e => onChange({ ...value, registration_identifiers: [{ ...identifier, type: e.target.value }] })} />
      <TextField label="EIN" inputMode="numeric" pattern="[0-9]{2}-?[0-9]{7}" placeholder="12-3456789" title="Enter 9 digits, with an optional hyphen after the first 2 digits." maxLength={10} value={identifier.value} onChange={e => onChange({ ...value, registration_identifiers: [{ ...identifier, value: e.target.value }] })} />
      <SelectField label="Issuing country" options={REGISTRATION_COUNTRIES} value={identifier.issuing_country} onChange={e => onChange({ ...value, registration_identifiers: [{ ...identifier, issuing_country: e.target.value }] })} />
    </section>
  </div>;
}
