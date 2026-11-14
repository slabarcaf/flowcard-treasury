import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react';
import type { AddressInput, FieldIssue } from '../../services/onboarding/contracts.js';
import { COUNTRY_OPTIONS, REGISTRATION_COUNTRIES, US_STATES } from '../../services/onboarding/options.js';
import type { FormOption } from '../../services/onboarding/options.js';

export function FieldIssueMessages({ issues, field }: { issues: FieldIssue[]; field?: string }) {
  if (!field) return null;
  const matches = issues.filter(issue => issue.field === field);
  if (!matches.length) return null;
  const id = `field-issues-${field.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
  return <ul id={id} role="alert" className="mt-1 space-y-1 text-xs text-red-200">{matches.map((issue, index) => <li key={`${issue.code}-${index}`}>{issue.message}</li>)}</ul>;
}

export function TextField({ label, optional = false, fieldIssues = [], apiField, ...props }: InputHTMLAttributes<HTMLInputElement> & { label: string; optional?: boolean; fieldIssues?: FieldIssue[]; apiField?: string }) {
  const hasIssue = Boolean(apiField && fieldIssues.some(issue => issue.field === apiField));
  const issueId = apiField ? `field-issues-${apiField.replace(/[^a-zA-Z0-9_-]/g, '-')}` : undefined;
  return <div className="block text-sm text-slate-300 space-y-2">
    <label className="block space-y-2"><span>{label} <span className="text-slate-500">{optional ? '(optional)' : '*'}</span></span><input {...props} required={!optional} aria-invalid={hasIssue} aria-describedby={hasIssue ? issueId : undefined} className="revolut-input w-full" /></label>
    <FieldIssueMessages issues={fieldIssues} field={apiField} />
  </div>;
}
export function SelectField({ label, optional = false, options, placeholder = 'Select an option', fieldIssues = [], apiField, ...props }: SelectHTMLAttributes<HTMLSelectElement> & { label: string; optional?: boolean; options: FormOption[]; placeholder?: string; fieldIssues?: FieldIssue[]; apiField?: string }) {
  const hasIssue = Boolean(apiField && fieldIssues.some(issue => issue.field === apiField));
  const issueId = apiField ? `field-issues-${apiField.replace(/[^a-zA-Z0-9_-]/g, '-')}` : undefined;
  return <div className="block text-sm text-slate-300 space-y-2">
    <label className="block space-y-2">
      <span>{label} <span className="text-slate-500">{optional ? '(optional)' : '*'}</span></span>
      <select {...props} required={!optional} aria-invalid={hasIssue} aria-describedby={hasIssue ? issueId : undefined} className="revolut-select w-full">
      <option value="">{placeholder}</option>
      {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </label>
    <FieldIssueMessages issues={fieldIssues} field={apiField} />
  </div>;
}
export function AddressFields({ value, onChange, requireState = false, usOnly = false, fieldIssues = [], fieldPrefix }: { value: AddressInput; requireState?: boolean; usOnly?: boolean; fieldIssues?: FieldIssue[]; fieldPrefix?: string; onChange: (address: AddressInput) => void }) {
  return <div className="grid gap-4 sm:grid-cols-2">
    <SelectField label="Country" apiField={fieldPrefix ? `${fieldPrefix}.country` : undefined} fieldIssues={fieldIssues} placeholder="Select country" options={usOnly ? REGISTRATION_COUNTRIES : COUNTRY_OPTIONS} value={value.country} onChange={e => onChange({ ...value, country: e.target.value, state: '' })} />
    {(['line_1', 'line_2', 'city', 'postal_code'] as const).map(key =>
      <TextField key={key} label={{ line_1: 'Street address', line_2: 'Address line 2', city: 'City', postal_code: value.country === 'US' ? 'ZIP code' : 'Postal code' }[key]} apiField={fieldPrefix ? `${fieldPrefix}.${key}` : undefined} fieldIssues={fieldIssues} pattern={key === 'postal_code' && usOnly && value.country === 'US' ? '[0-9]{5}(-[0-9]{4})?' : undefined} title={key === 'postal_code' && usOnly && value.country === 'US' ? 'Enter a 5-digit ZIP code or ZIP+4, for example 10001 or 10001-1234.' : undefined} optional={key === 'line_2'} maxLength={key === 'postal_code' ? 20 : key === 'city' ? 100 : 200} value={value[key] ?? ''} onChange={e => onChange({ ...value, [key]: e.target.value })} />)}
    {value.country === 'US'
      ? <SelectField label="State" apiField={fieldPrefix ? `${fieldPrefix}.state` : undefined} fieldIssues={fieldIssues} optional={!requireState} placeholder="Select state" options={US_STATES} value={value.state ?? ''} onChange={e => onChange({ ...value, state: e.target.value })} />
      : <TextField label="State / province" apiField={fieldPrefix ? `${fieldPrefix}.state` : undefined} fieldIssues={fieldIssues} optional={!requireState} maxLength={100} value={value.state ?? ''} onChange={e => onChange({ ...value, state: e.target.value })} />}
  </div>;
}
export function OptionalSection({ title, checked, onChange, children, disabled = false, help }: { title: string; checked: boolean; onChange: (checked: boolean) => void; children: ReactNode; disabled?: boolean; help?: string }) {
  return <section className="rounded-xl border border-slate-700 p-5 space-y-4">
    <label className={`flex items-center gap-3 text-sm text-white ${disabled ? 'cursor-not-allowed opacity-70' : 'cursor-pointer'}`}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={e => onChange(e.target.checked)} />
      {title} <span className="text-slate-500">(optional)</span>
    </label>
    {help && <p className="text-xs text-slate-400">{help}</p>}
    {checked && children}
  </section>;
}

export function CheckboxGroup({ label, options, value, onChange, required = false, fieldIssues = [], apiField }: {
  label: string; options: FormOption[]; value: string[]; onChange: (value: string[]) => void; required?: boolean; fieldIssues?: FieldIssue[]; apiField?: string;
}) {
  return <div><fieldset className="space-y-3"><legend className="text-sm text-slate-300 mb-3">{label}{required ? ' *' : ' (optional)'}</legend>
    <div className="grid gap-3 sm:grid-cols-2">{options.map(option => <label key={option.value} className="flex items-start gap-3 border border-slate-700 rounded-xl p-3 text-sm cursor-pointer">
      <input type="checkbox" checked={value.includes(option.value)} onChange={event => onChange(event.target.checked ? [...value, option.value] : value.filter(item => item !== option.value))} />{option.label}
    </label>)}</div>
  </fieldset><FieldIssueMessages issues={fieldIssues} field={apiField} /></div>;
}
