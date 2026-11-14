import { useState } from 'react';
import type { FieldIssue, IdentificationInput, PersonInput } from '../../../services/onboarding/contracts.js';
import { newIdentification, newPerson } from '../../../services/onboarding/formRules.js';
import { COUNTRY_OPTIONS, IDENTIFICATION_TYPES } from '../../../services/onboarding/options.js';
import { FieldIssueMessages, SelectField, TextField } from '../ApplicationFields.js';

const IDENTIFICATION_TYPE_OPTIONS = [...IDENTIFICATION_TYPES, { value: 'OTHER', label: 'Other' }];

export function PeopleApplicationStep({ people, onChange, fieldIssues = [] }: { people: PersonInput[]; onChange: (people: PersonInput[]) => void; fieldIssues?: FieldIssue[] }) {
  const [yesterday] = useState(() => new Date(Date.now() - 86_400_000).toISOString().slice(0, 10));

  function updatePerson(index: number, patch: Partial<PersonInput>) {
    onChange(people.map((person, personIndex) => personIndex === index ? { ...person, ...patch } : person));
  }

  function updateIdentification(personIndex: number, identificationId: string, patch: Partial<IdentificationInput>) {
    const person = people[personIndex];
    updatePerson(personIndex, {
      identifications: (person.identifications ?? []).map(identification =>
        identification.id === identificationId ? { ...identification, ...patch } : identification),
    });
  }

  return <div className="space-y-5">
    <p className="text-sm text-slate-400">People are optional for saving a company. Add a person if you want to include their details.</p>
    <FieldIssueMessages issues={fieldIssues} field="company.people" />
    {people.map((person, index) => <section key={person.id} className="rounded-xl border border-slate-700 p-5 space-y-4">
      <div className="flex justify-between"><h2 className="font-medium">Person {index + 1}</h2><button type="button" className="revolut-link" onClick={() => onChange(people.filter(item => item.id !== person.id))}>Remove</button></div>
      <TextField label="First name" apiField={`company.people[${index}].first_name`} fieldIssues={fieldIssues} maxLength={100} autoComplete="given-name" value={person.first_name} onChange={e => updatePerson(index, { first_name: e.target.value })} />
      <TextField label="Last name" apiField={`company.people[${index}].last_name`} fieldIssues={fieldIssues} maxLength={100} autoComplete="family-name" value={person.last_name} onChange={e => updatePerson(index, { last_name: e.target.value })} />
      <TextField label="Job title" apiField={`company.people[${index}].role`} fieldIssues={fieldIssues} placeholder="For example, Finance Director" maxLength={100} value={person.role} onChange={e => updatePerson(index, { role: e.target.value })} />
      <TextField label="Date of birth" apiField={`company.people[${index}].date_of_birth`} fieldIssues={fieldIssues} type="date" max={yesterday} value={person.date_of_birth} onChange={e => updatePerson(index, { date_of_birth: e.target.value })} />

      <div className="space-y-4 border-t border-slate-700 pt-4">
        <div className="flex items-center justify-between gap-3">
          <div><h3 className="text-sm font-medium">Identifications</h3><p className="text-xs text-slate-400">Optional. A person can have multiple identification records.</p></div>
          <button type="button" className="revolut-link" onClick={() => updatePerson(index, { identifications: [...(person.identifications ?? []), newIdentification()] })}>Add identification</button>
        </div>
        <FieldIssueMessages issues={fieldIssues} field={`company.people[${index}].identifications`} />
        {(person.identifications ?? []).map((identification, identificationIndex) => {
          const type = identification.type === 'OTHER' ? identification.other_type ?? '' : identification.type;
          const isTaxId = ['SSN', 'ITIN'].includes(type);
          return <div key={identification.id} className="space-y-4 border-t border-slate-700 pt-4">
            <div className="flex items-center justify-between"><h4 className="text-sm font-medium">Identification {identificationIndex + 1}</h4><button type="button" className="revolut-link" onClick={() => updatePerson(index, { identifications: (person.identifications ?? []).filter(item => item.id !== identification.id) })}>Remove</button></div>
            <SelectField label="Identification type" apiField={`company.people[${index}].identifications[${identificationIndex}].type`} fieldIssues={fieldIssues} options={IDENTIFICATION_TYPE_OPTIONS} value={identification.type} onChange={e => updateIdentification(index, identification.id!, {
              type: e.target.value,
              other_type: e.target.value === 'OTHER' ? identification.other_type ?? '' : undefined,
              issuing_country: ['SSN', 'ITIN'].includes(e.target.value) ? 'US' : identification.issuing_country,
            })} />
            {identification.type === 'OTHER' && <TextField label="Other identification type" apiField={`company.people[${index}].identifications[${identificationIndex}].type`} fieldIssues={fieldIssues} maxLength={50} value={identification.other_type ?? ''} onChange={e => updateIdentification(index, identification.id!, { other_type: e.target.value })} />}
            <TextField label={isTaxId ? `${type} number` : 'Identification number'} apiField={`company.people[${index}].identifications[${identificationIndex}].number`} fieldIssues={fieldIssues} inputMode={isTaxId ? 'numeric' : 'text'} pattern={isTaxId ? '(?:[0-9]{9}|[0-9]{3}-[0-9]{2}-[0-9]{4})' : undefined} title={isTaxId ? 'Enter 9 digits, with optional hyphens.' : undefined} maxLength={100} value={identification.number} onChange={e => updateIdentification(index, identification.id!, { number: e.target.value })} />
            <SelectField label="Issuing country" apiField={`company.people[${index}].identifications[${identificationIndex}].issuing_country`} fieldIssues={fieldIssues} options={isTaxId ? [{ value: 'US', label: 'United States' }] : COUNTRY_OPTIONS} value={identification.issuing_country} onChange={e => updateIdentification(index, identification.id!, { issuing_country: e.target.value })} />
            <TextField label="Issue date" apiField={`company.people[${index}].identifications[${identificationIndex}].issued_on`} fieldIssues={fieldIssues} optional type="date" value={identification.issued_on ?? ''} onChange={e => updateIdentification(index, identification.id!, { issued_on: e.target.value })} />
            <TextField label="Expiry date" apiField={`company.people[${index}].identifications[${identificationIndex}].expires_on`} fieldIssues={fieldIssues} optional type="date" value={identification.expires_on ?? ''} onChange={e => updateIdentification(index, identification.id!, { expires_on: e.target.value })} />
          </div>;
        })}
      </div>
    </section>)}
    <button type="button" className="revolut-link" onClick={() => onChange([...people, newPerson()])}>Add person</button>
  </div>;
}
