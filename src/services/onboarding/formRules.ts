import type { CompanyInput, IdentificationInput, OnboardingDetailsInput, PersonInput, RegistrationIdentifierInput } from './contracts.js';

export function newIdentification(): IdentificationInput {
  return { id: crypto.randomUUID(), type: '', number: '', issuing_country: '' };
}

export function newPerson(): PersonInput {
  return {
    id: crypto.randomUUID(),
    first_name: '',
    last_name: '',
    role: '',
    date_of_birth: '',
    identifications: [],
  };
}

export function newRegistrationIdentifier(): RegistrationIdentifierInput {
  return { client_id: crypto.randomUUID(), type: 'EIN', value: '', issuing_country: 'US' };
}

export function newCompany(): CompanyInput {
  return {
    legal_name: '',
    registration_country: 'US',
    registration_identifiers: [],
    people: [],
  };
}

export function validEin(value: string): boolean { return /^\d{2}-?\d{7}$/.test(value.trim()); }
export function normalizeTaxId(value: string): string { return value.replace(/[\s-]/g, ''); }

export function clearIndustryCategoryCode(details: OnboardingDetailsInput): OnboardingDetailsInput {
  return { ...details, industry_category_code: undefined };
}

function isCalendarDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00.000Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}

function isPastDate(value: string): boolean {
  return isCalendarDate(value) && value < new Date().toISOString().slice(0, 10);
}

export function isValidWebsite(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) return false;
  try {
    const website = new URL(trimmed);
    return ['http:', 'https:'].includes(website.protocol) &&
      website.hostname.includes('.') && !/\s/.test(website.host);
  } catch {
    return false;
  }
}

function rowType(type: string, otherType?: string): string {
  return type === 'OTHER' ? otherType?.trim() ?? '' : type.trim();
}

export function stepIssues(step: number, company: CompanyInput, details: OnboardingDetailsInput, editingSavedCompany = false): string[] {
  const issues: string[] = [];
  if (step === 0) {
    const legalName = company.legal_name.trim();
    if (legalName.length < 2 || legalName.length > 200) issues.push('Enter a legal company name between 2 and 200 characters.');
    if ((company.trading_name?.trim().length ?? 0) > 200) issues.push('Trading name must be 200 characters or fewer.');
    const entityType = rowType(company.entity_type ?? '', company.entity_type_other);
    if (company.entity_type === 'OTHER' && !entityType) issues.push('Enter the other company type.');
    if (entityType.length > 50) issues.push('Company type must be 50 characters or fewer.');
    if ((company.registration_state?.trim().length ?? 0) > 100) issues.push('State of incorporation must be 100 characters or fewer.');

    const profile = company.business_profile;
    if (profile) {
      const website = profile.website.trim();
      const description = profile.description.trim();
      if ((website || !editingSavedCompany) && (website.length > 500 || !isValidWebsite(website))) {
        issues.push('Enter a valid http or https business website, up to 500 characters.');
      }
      if ((!description && !editingSavedCompany) || description.length > 2000) issues.push('Enter a business description, up to 2,000 characters.');

      const address = profile.registered_address;
      if (address) {
        if (!address.line_1.trim() || address.line_1.trim().length > 200) issues.push('Enter a street address, up to 200 characters.');
        if (!address.city.trim() || address.city.trim().length > 100) issues.push('Enter a city, up to 100 characters.');
        if (!address.postal_code.trim() || address.postal_code.trim().length > 20 || !/^\d{5}(?:-\d{4})?$/.test(address.postal_code.trim())) {
          issues.push('Enter a US ZIP code or ZIP+4.');
        }
        if (address.country !== 'US') issues.push('Registered address country must be the United States for this intake.');
        if ((address.line_2?.trim().length ?? 0) > 200) issues.push('Address line 2 must be 200 characters or fewer.');
        if ((address.state?.trim().length ?? 0) > 100) issues.push('Address state must be 100 characters or fewer.');
      }
    }

    for (const [index, identifier] of (company.registration_identifiers ?? []).entries()) {
      const type = rowType(identifier.type, identifier.other_type);
      const value = identifier.value.trim();
      if (!type || type.length > 50) issues.push(`Enter a registration type of 1 to 50 characters for registration ${index + 1}.`);
      if (!value || value.length > 100) issues.push(`Enter a registration value of 1 to 100 characters for registration ${index + 1}.`);
      if (!identifier.issuing_country.trim()) issues.push(`Choose an issuing country for registration ${index + 1}.`);
      if (type === 'EIN' && identifier.issuing_country === 'US' && !validEin(value)) {
        issues.push(`Enter a 9-digit US EIN for registration ${index + 1}.`);
      }
    }
  }

  if (step === 1) {
    for (const [index, person] of (company.people ?? []).entries()) {
      const firstName = person.first_name.trim();
      const lastName = person.last_name.trim();
      const role = person.role.trim();
      if (!firstName || firstName.length > 100 || !lastName || lastName.length > 100 || !role || role.length > 100) {
        issues.push(`Enter first name, last name and job title (up to 100 characters each) for person ${index + 1}.`);
      }
      if (!isPastDate(person.date_of_birth)) issues.push(`Enter a valid past birth date for person ${index + 1}.`);

      for (const [identIndex, identification] of (person.identifications ?? []).entries()) {
        const type = rowType(identification.type, identification.other_type);
        const number = identification.number.trim();
        const label = `identification ${identIndex + 1} for person ${index + 1}`;
        if (!type || type.length > 50) issues.push(`Enter an identification type of 1 to 50 characters for ${label}.`);
        if (!number || number.length > 100) issues.push(`Enter an identification number of 1 to 100 characters for ${label}.`);
        if (!identification.issuing_country.trim()) issues.push(`Choose an issuing country for ${label}.`);
        if (['SSN', 'ITIN'].includes(type) &&
            (identification.issuing_country !== 'US' || !/^\d{9}$/.test(normalizeTaxId(number)))) {
          issues.push(`Enter a 9-digit US ${type} for ${label}.`);
        }
        if (identification.issued_on && !isCalendarDate(identification.issued_on)) issues.push(`Enter a valid issue date for ${label}.`);
        if (identification.expires_on && !isCalendarDate(identification.expires_on)) issues.push(`Enter a valid expiry date for ${label}.`);
        if (identification.issued_on && identification.expires_on && identification.expires_on <= identification.issued_on) {
          issues.push(`Expiry date must follow issue date for ${label}.`);
        }
      }
    }
  }

  if (step === 2) {
    const personIds = new Set((company.people ?? []).map(person => person.id));
    if (details.primary_contact_person_id && !personIds.has(details.primary_contact_person_id)) {
      issues.push('Primary contact must be one of the people associated with this company.');
    }
    for (const [index, personDetails] of (details.people ?? []).entries()) {
      if (!personIds.has(personDetails.person_id)) {
        issues.push(`Application details ${index + 1} refer to a person that is not on this company.`);
        continue;
      }
      const address = personDetails.residential_address;
      if (!address) continue;
      if (!address.line_1.trim() || address.line_1.trim().length > 200) issues.push(`Enter street address (up to 200 characters) for person details ${index + 1}.`);
      if (!address.city.trim() || address.city.trim().length > 100) issues.push(`Enter city (up to 100 characters) for person details ${index + 1}.`);
      if (!address.postal_code.trim() || address.postal_code.trim().length > 20) issues.push(`Enter postal code (up to 20 characters) for person details ${index + 1}.`);
      if (!/^[A-Z]{2}$/.test(address.country)) issues.push(`Choose a valid country for person details ${index + 1}.`);
      if ((address.line_2?.trim().length ?? 0) > 200) issues.push(`Address line 2 must be 200 characters or fewer for person details ${index + 1}.`);
      if ((address.state?.trim().length ?? 0) > 100) issues.push(`State must be 100 characters or fewer for person details ${index + 1}.`);
    }
  }
  return issues;
}
