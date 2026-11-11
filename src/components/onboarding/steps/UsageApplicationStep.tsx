import type { OnboardingDetailsInput, PersonInput } from '../../../services/onboarding/contracts.js';
import { emptyAddress } from '../../../services/onboarding/contracts.js';
import { CURRENCY_OPTIONS, PLATFORM_ROLE_OPTIONS, PRODUCT_OPTIONS } from '../../../services/onboarding/options.js';
import { useIndustryOptions } from '../../../hooks/useIndustryOptions.js';
import { AddressFields, CheckboxGroup, SelectField, TextField } from '../ApplicationFields.js';

export function UsageApplicationStep({ value, people, onChange }: { value: OnboardingDetailsInput; people: PersonInput[]; onChange: (value: OnboardingDetailsInput) => void }) {
  const industries = useIndustryOptions();
  return <div className="space-y-5">
    <SelectField label="Industry" options={industries.options} placeholder={industries.loading ? 'Loading industries…' : industries.error ? 'Industry list unavailable' : 'Select your industry'} disabled={industries.loading || industries.error} value={value.industry_category_code ?? ''} onChange={e => onChange({ ...value, industry_category_code: e.target.value })} />
    {industries.error && <div role="status" className="text-sm text-amber-200">We could not load the industry list. Please contact FlowCard.</div>}
    <CheckboxGroup label="How will you use FlowCard?" required options={PRODUCT_OPTIONS} value={value.intended_products ?? []} onChange={products => onChange({ ...value, intended_products: products })} />
    <TextField label="Estimated monthly revenue" type="number" min="0" step="0.01" placeholder="0.00" value={value.estimated_monthly_revenue.amount ?? ''} onChange={e => onChange({ ...value, estimated_monthly_revenue: { ...value.estimated_monthly_revenue, amount: e.target.value } })} />
    <SelectField label="Revenue currency" options={CURRENCY_OPTIONS} value={value.estimated_monthly_revenue.currency ?? ''} onChange={e => onChange({ ...value, estimated_monthly_revenue: { ...value.estimated_monthly_revenue, currency: e.target.value } })} />
    <SelectField label="Primary contact" options={people.map(person => ({ value: person.id, label: `${person.first_name} ${person.last_name}` }))} value={value.primary_contact_person_id ?? ''} onChange={e => onChange({ ...value, primary_contact_person_id: e.target.value })} />
    {people.map(person => {
      const current = value.people?.find(item => item.person_id === person.id) ?? { person_id: person.id, platform_roles: [], residential_address: emptyAddress() };
      function update(patch: Partial<typeof current>) { onChange({ ...value, people: [...(value.people ?? []).filter(item => item.person_id !== person.id), { ...current, ...patch }] }); }
      return <section key={person.id} className="rounded-xl border border-slate-700 p-5 space-y-4">
        <h2 className="font-medium">{person.first_name} {person.last_name}</h2>
        <CheckboxGroup label="Relationship to the company" required options={PLATFORM_ROLE_OPTIONS} value={current.platform_roles ?? []} onChange={roles => update({ platform_roles: roles })} />
        <h3 className="text-sm text-slate-300">Residential address</h3>
        <AddressFields value={current.residential_address ?? emptyAddress()} requireState={current.residential_address?.country === 'US'} onChange={address => update({ residential_address: address })} />
      </section>;
    })}
  </div>;
}
