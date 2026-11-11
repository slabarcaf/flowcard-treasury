import { useState } from 'react';
import type { PersonInput } from '../../../services/onboarding/contracts.js';
import { COUNTRY_OPTIONS, IDENTIFICATION_TYPES } from '../../../services/onboarding/options.js';
import { newPerson } from '../../../services/onboarding/formRules.js';
import { SelectField, TextField } from '../ApplicationFields.js';

export function PeopleApplicationStep({ people, onChange }: { people: PersonInput[]; onChange: (people: PersonInput[]) => void }) {
  const [yesterday] = useState(() => new Date(Date.now() - 86_400_000).toISOString().slice(0, 10));
  function update(index: number, patch: Partial<PersonInput>) { onChange(people.map((person, i) => i === index ? { ...person, ...patch } : person)); }
  return <div className="space-y-5">
    <p className="text-sm text-slate-400">Provide the people associated with your company. At least one person and their identification are required.</p>
    {people.map((person, index) => <section key={person.id} className="rounded-xl border border-slate-700 p-5 space-y-4">
      <div className="flex justify-between"><h2 className="font-medium">Person {index + 1}</h2>{people.length > 1 && <button type="button" className="revolut-link" onClick={() => onChange(people.filter(item => item.id !== person.id))}>Remove</button>}</div>
      <TextField label="First name" maxLength={100} autoComplete="given-name" value={person.first_name} onChange={e => update(index, { first_name: e.target.value })} />
      <TextField label="Last name" maxLength={100} autoComplete="family-name" value={person.last_name} onChange={e => update(index, { last_name: e.target.value })} />
      <TextField label="Job title" placeholder="For example, Finance Director" maxLength={100} value={person.role} onChange={e => update(index, { role: e.target.value })} />
      <TextField label="Date of birth" type="date" max={yesterday} value={person.date_of_birth} onChange={e => update(index, { date_of_birth: e.target.value })} />
      {(person.identifications?.length ? person.identifications : [{ id: `${person.id}-ident`, type: '', number: '', issuing_country: '' }]).map((ident, identIndex) => {
        function updateIdent(patch: Partial<typeof ident>) { update(index, { identifications: (person.identifications?.length ? person.identifications : [ident]).map((item, i) => i === identIndex ? { ...item, ...patch } : item) }); }
        const taxId = ['SSN', 'ITIN'].includes(ident.type);
        return <div key={ident.id} className="space-y-4 pt-4 border-t border-slate-700">
          <SelectField label="Identification type" options={IDENTIFICATION_TYPES} value={ident.type} onChange={e => updateIdent({ type: e.target.value, number: '', issuing_country: ['SSN', 'ITIN'].includes(e.target.value) ? 'US' : '' })} />
          <TextField label={taxId ? `${ident.type} number` : 'Identification number'} inputMode={taxId ? 'numeric' : 'text'} pattern={taxId ? '(?:[0-9]{9}|[0-9]{3}-[0-9]{2}-[0-9]{4})' : undefined} title={taxId ? 'Enter 9 digits, with optional hyphens.' : undefined} maxLength={taxId ? 11 : 100} value={ident.number} onChange={e => updateIdent({ number: e.target.value })} />
          <SelectField label="Issuing country" options={taxId ? [{ value: 'US', label: 'United States' }] : COUNTRY_OPTIONS} value={ident.issuing_country} onChange={e => updateIdent({ issuing_country: e.target.value })} />
          {!taxId && <><TextField label="Issue date" optional type="date" value={ident.issued_on ?? ''} onChange={e => updateIdent({ issued_on: e.target.value })} /><TextField label="Expiry date" optional type="date" value={ident.expires_on ?? ''} onChange={e => updateIdent({ expires_on: e.target.value })} /></>}
        </div>;
      })}
    </section>)}
    <button type="button" className="revolut-link" onClick={() => onChange([...people, newPerson()])}>+ Add another person</button>
  </div>;
}
