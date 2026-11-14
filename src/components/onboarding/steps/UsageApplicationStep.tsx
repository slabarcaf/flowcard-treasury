import { useState } from 'react';
import type { FieldIssue, OnboardingDetailsInput, PersonInput } from '../../../services/onboarding/contracts.js';
import { emptyAddress } from '../../../services/onboarding/contracts.js';
import { CURRENCY_OPTIONS, PLATFORM_ROLE_OPTIONS, PRODUCT_OPTIONS } from '../../../services/onboarding/options.js';
import { clearIndustryCategoryCode } from '../../../services/onboarding/formRules.js';
import { useIndustryOptions } from '../../../hooks/useIndustryOptions.js';
import { AddressFields, CheckboxGroup, FieldIssueMessages, OptionalSection, SelectField, TextField } from '../ApplicationFields.js';

export function UsageApplicationStep({ value, people, onChange, onGoToPeople, fieldIssues = [] }: {
  value: OnboardingDetailsInput;
  people: PersonInput[];
  onChange: (value: OnboardingDetailsInput) => void;
  onGoToPeople: () => void;
  fieldIssues?: FieldIssue[];
}) {
  const industries = useIndustryOptions();
  const [otherIndustryCode, setOtherIndustryCode] = useState('');
  const [otherProductCode, setOtherProductCode] = useState('');
  const [otherRoleCodes, setOtherRoleCodes] = useState<Record<string, string>>({});

  const knownProductCodes = new Set(PRODUCT_OPTIONS.map(option => option.value));
  const knownRoleCodes = new Set(PLATFORM_ROLE_OPTIONS.map(option => option.value));

  function addProductCode() {
    const code = otherProductCode.trim();
    if (!code) return;
    const products = value.intended_products ?? [];
    onChange({ ...value, intended_products: products.includes(code) ? products : [...products, code] });
    setOtherProductCode('');
  }

  function updatePerson(person: PersonInput, patch: Partial<NonNullable<OnboardingDetailsInput['people']>[number]>) {
    const current = value.people?.find(item => item.person_id === person.id) ?? { person_id: person.id };
    const updated = { ...current, ...patch };
    const peopleDetails = (value.people ?? []).filter(item => item.person_id !== person.id);
    onChange({ ...value, people: [...peopleDetails, updated] });
  }

  return <div className="space-y-5">
    <p className="text-sm text-slate-400">Platform: Airwallex · Environment: Sandbox</p>
    <SelectField label="Industry" apiField="platform.onboarding_details.industry_category_code" fieldIssues={fieldIssues} optional options={industries.options}
      placeholder={industries.loading ? 'Loading industries…' : industries.error ? 'Industry list unavailable' : 'Select your industry'}
      disabled={industries.loading || Boolean(industries.error)}
      value={industries.options.some(option => option.value === value.industry_category_code) ? value.industry_category_code ?? '' : ''}
      onChange={e => onChange({ ...value, industry_category_code: e.target.value || undefined })} />
    {industries.error && <p role="status" className="text-sm text-amber-200">The industry list is unavailable. You can enter an industry code if you have one.</p>}
    <div className="flex items-end gap-3">
      <TextField label="Other industry code" optional value={otherIndustryCode} onChange={e => setOtherIndustryCode(e.target.value)} />
      <button type="button" className="revolut-link" disabled={!otherIndustryCode.trim()} onClick={() => {
        onChange({ ...value, industry_category_code: otherIndustryCode.trim() });
        setOtherIndustryCode('');
      }}>Use code</button>
    </div>
    <FieldIssueMessages issues={fieldIssues ?? []} field="platform.onboarding_details.industry_category_code" />
    {value.industry_category_code && !industries.options.some(option => option.value === value.industry_category_code) && <div className="flex items-center gap-3 text-sm text-slate-400">
      <span>Selected industry code: {value.industry_category_code}</span>
      <button type="button" className="revolut-link" onClick={() => onChange(clearIndustryCategoryCode(value))}>Clear industry code</button>
    </div>}

    <CheckboxGroup label="How will you use FlowCard?" options={PRODUCT_OPTIONS} value={value.intended_products ?? []}
      apiField="platform.onboarding_details.intended_products" fieldIssues={fieldIssues}
      onChange={products => onChange({ ...value, intended_products: products })} />
    <div className="flex items-end gap-3">
      <TextField label="Other product code" optional value={otherProductCode} onChange={e => setOtherProductCode(e.target.value)} />
      <button type="button" className="revolut-link" disabled={!otherProductCode.trim()} onClick={addProductCode}>Add code</button>
    </div>
    {(value.intended_products ?? []).filter(code => !knownProductCodes.has(code)).map(code => <div key={code} className="flex items-center gap-3 text-sm text-slate-300">
      <span>{code}</span><button type="button" className="revolut-link" onClick={() => onChange({ ...value, intended_products: value.intended_products?.filter(item => item !== code) })}>Remove</button>
    </div>)}

    <TextField label="Estimated monthly revenue" optional type="text" inputMode="decimal" placeholder="0.00"
      apiField="platform.onboarding_details.estimated_monthly_revenue.amount" fieldIssues={fieldIssues}
      value={value.estimated_monthly_revenue.amount ?? ''}
      onChange={e => onChange({ ...value, estimated_monthly_revenue: { ...value.estimated_monthly_revenue, amount: e.target.value } })} />
    <SelectField label="Revenue currency" apiField="platform.onboarding_details.estimated_monthly_revenue.currency" fieldIssues={fieldIssues} optional options={CURRENCY_OPTIONS} value={value.estimated_monthly_revenue.currency ?? ''}
      onChange={e => onChange({ ...value, estimated_monthly_revenue: { ...value.estimated_monthly_revenue, currency: e.target.value || undefined } })} />
    <SelectField label="Primary contact" apiField="platform.onboarding_details.primary_contact_person_id" fieldIssues={fieldIssues} optional placeholder="No primary contact selected"
      options={people.map(person => ({ value: person.id, label: `${person.first_name} ${person.last_name}` }))}
      value={value.primary_contact_person_id ?? ''}
      onChange={e => onChange({ ...value, primary_contact_person_id: e.target.value || undefined })} />
    {people.length === 0 && <p className="text-sm text-slate-400">No people added. <button type="button" className="revolut-link" onClick={onGoToPeople}>Go to People</button> to add someone, or continue without a primary contact.</p>}

    {people.map(person => {
      const current = value.people?.find(item => item.person_id === person.id) ?? { person_id: person.id };
      const roles = current.platform_roles ?? [];
      const customRoles = roles.filter(role => !knownRoleCodes.has(role));
      const persistedDetails = (value.people ?? []).filter(item => (item.platform_roles ?? []).some(role => role.trim()) || item.residential_address);
      const personDetailsIndex = persistedDetails.findIndex(item => item.person_id === person.id);
      const personDetailsPath = `platform.onboarding_details.people[${personDetailsIndex < 0 ? people.indexOf(person) : personDetailsIndex}]`;
      return <section key={person.id} className="rounded-xl border border-slate-700 p-5 space-y-4">
        <h2 className="font-medium">{person.first_name} {person.last_name}</h2>
        <CheckboxGroup label="Relationship to the company" apiField={`${personDetailsPath}.platform_roles`} fieldIssues={fieldIssues} options={PLATFORM_ROLE_OPTIONS} value={roles}
          onChange={platformRoles => updatePerson(person, { platform_roles: platformRoles })} />
        <div className="flex items-end gap-3">
          <TextField label="Other relationship code" optional value={otherRoleCodes[person.id] ?? ''}
            onChange={e => setOtherRoleCodes({ ...otherRoleCodes, [person.id]: e.target.value })} />
          <button type="button" className="revolut-link" disabled={!otherRoleCodes[person.id]?.trim()} onClick={() => {
            const code = (otherRoleCodes[person.id] ?? '').trim();
            if (!roles.includes(code)) updatePerson(person, { platform_roles: [...roles, code] });
            setOtherRoleCodes({ ...otherRoleCodes, [person.id]: '' });
          }}>Add code</button>
        </div>
        {customRoles.map(role => <div key={role} className="flex items-center gap-3 text-sm text-slate-300">
          <span>{role}</span><button type="button" className="revolut-link" onClick={() => updatePerson(person, { platform_roles: roles.filter(item => item !== role) })}>Remove</button>
        </div>)}
        <OptionalSection title="Residential address" checked={Boolean(current.residential_address)} onChange={enabled => updatePerson(person, {
          residential_address: enabled ? current.residential_address ?? emptyAddress() : undefined,
        })}>
          {current.residential_address && <AddressFields fieldPrefix={`${personDetailsPath}.residential_address`} fieldIssues={fieldIssues} value={current.residential_address} onChange={address => updatePerson(person, { residential_address: address })} />}
        </OptionalSection>
      </section>;
    })}
  </div>;
}
