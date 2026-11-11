import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react';
import type { AddressInput } from '../../services/onboarding/contracts.js';
import { COUNTRY_OPTIONS, REGISTRATION_COUNTRIES, US_STATES } from '../../services/onboarding/options.js';
import type { FormOption } from '../../services/onboarding/options.js';

export function TextField({ label, optional = false, ...props }: InputHTMLAttributes<HTMLInputElement> & { label: string; optional?: boolean }) {
  return <label className="block text-sm text-slate-300 space-y-2"><span>{label} <span className="text-slate-500">{optional ? '(optional)' : '*'}</span></span><input {...props} required={!optional} className="revolut-input w-full" /></label>;
}
export function SelectField({ label, optional = false, options, placeholder = 'Select an option', ...props }: SelectHTMLAttributes<HTMLSelectElement> & { label: string; optional?: boolean; options: FormOption[]; placeholder?: string }) {
  return <label className="block text-sm text-slate-300 space-y-2">
    <span>{label} <span className="text-slate-500">{optional ? '(optional)' : '*'}</span></span>
    <select {...props} required={!optional} className="revolut-select w-full">
      <option value="">{placeholder}</option>
      {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>
  </label>;
}
export function AddressFields({ value, onChange, requireState = false, usOnly = false }: { value: AddressInput; requireState?: boolean; usOnly?: boolean; onChange: (address: AddressInput) => void }) {
  return <div className="grid gap-4 sm:grid-cols-2">
    <SelectField label="Country" placeholder="Select country" options={usOnly ? REGISTRATION_COUNTRIES : COUNTRY_OPTIONS} value={value.country} onChange={e => onChange({ ...value, country: e.target.value, state: '' })} />
    {(['line_1', 'line_2', 'city', 'postal_code'] as const).map(key =>
      <TextField key={key} label={{ line_1: 'Street address', line_2: 'Address line 2', city: 'City', postal_code: value.country === 'US' ? 'ZIP code' : 'Postal code' }[key]} pattern={key === 'postal_code' && value.country === 'US' ? '[0-9]{5}(-[0-9]{4})?' : undefined} title={key === 'postal_code' && value.country === 'US' ? 'Enter a 5-digit ZIP code or ZIP+4, for example 10001 or 10001-1234.' : undefined} optional={key === 'line_2'} maxLength={key === 'postal_code' ? 20 : key === 'city' ? 100 : 200} value={value[key] ?? ''} onChange={e => onChange({ ...value, [key]: e.target.value })} />)}
    {value.country === 'US'
      ? <SelectField label="State" optional={!requireState} placeholder="Select state" options={US_STATES} value={value.state ?? ''} onChange={e => onChange({ ...value, state: e.target.value })} />
      : <TextField label="State / province" optional={!requireState} maxLength={100} value={value.state ?? ''} onChange={e => onChange({ ...value, state: e.target.value })} />}
  </div>;
}
export function OptionalSection({ title, checked, onChange, children }: { title: string; checked: boolean; onChange: (checked: boolean) => void; children: ReactNode }) {
  return <section className="rounded-xl border border-slate-700 p-5 space-y-4"><label className="flex items-center gap-3 text-sm text-white cursor-pointer"><input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} />{title} <span className="text-slate-500">(optional)</span></label>{checked && children}</section>;
}

export function CheckboxGroup({ label, options, value, onChange, required = false }: {
  label: string; options: FormOption[]; value: string[]; onChange: (value: string[]) => void; required?: boolean;
}) {
  return <fieldset className="space-y-3"><legend className="text-sm text-slate-300 mb-3">{label}{required ? ' *' : ' (optional)'}</legend>
    <div className="grid gap-3 sm:grid-cols-2">{options.map(option => <label key={option.value} className="flex items-start gap-3 border border-slate-700 rounded-xl p-3 text-sm cursor-pointer">
      <input type="checkbox" checked={value.includes(option.value)} onChange={event => onChange(event.target.checked ? [...value, option.value] : value.filter(item => item !== option.value))} />{option.label}
    </label>)}</div>
  </fieldset>;
}
