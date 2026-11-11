import { emptyAddress } from './contracts.js';
import type { CompanyInput, OnboardingDetailsInput, PersonInput } from './contracts.js';

export function newPerson(): PersonInput {
  return { id: crypto.randomUUID(), first_name: '', last_name: '', role: '', date_of_birth: '',
    identifications: [{ id: crypto.randomUUID(), type: '', number: '', issuing_country: '' }] };
}
export function newCompany(): CompanyInput {
  return { legal_name: '', registration_country: 'US', entity_type: '',
    business_profile: { website: '', description: '', registered_address: { ...emptyAddress(), country: 'US' } },
    registration_identifiers: [{ type: 'EIN', value: '', issuing_country: 'US' }], people: [newPerson()] };
}
export function validEin(value: string): boolean { return /^\d{2}-?\d{7}$/.test(value.trim()); }
export function normalizeTaxId(value: string): string { return value.replace(/[\s-]/g, ''); }

export function stepIssues(step: number, company: CompanyInput, details: OnboardingDetailsInput): string[] {
  const issues: string[] = [];
  if (step === 0) {
    if (company.legal_name.trim().length < 2) issues.push('Enter the legal company name.');
    if (!company.entity_type || company.registration_country !== 'US') issues.push('Choose a company type and United States as the country of incorporation.');
    const profile = company.business_profile;
    if (!profile?.website || !profile.description.trim()) issues.push('Provide your website and business description.');
    const address = profile?.registered_address;
    if (!address?.line_1.trim() || !address.city.trim() || !address.state || !address.postal_code || address.country !== 'US') issues.push('Complete the US registered address, including the state and ZIP code.');
    if (!validEin(company.registration_identifiers?.[0]?.value ?? '')) issues.push('Enter a 9-digit EIN, for example 12-3456789.');
  }
  if (step === 1) {
    if (!company.people?.length) issues.push('Add at least one person associated with the company.');
    for (const [index, person] of (company.people ?? []).entries()) {
      if (!person.first_name.trim() || !person.last_name.trim() || !person.role.trim() || !person.date_of_birth) issues.push(`Complete the name, job title and birth date for person ${index + 1}.`);
      if (!person.identifications?.length) issues.push(`Add identification for person ${index + 1}.`);
      for (const ident of person.identifications ?? []) {
        if (!ident.type || !ident.number.trim() || !ident.issuing_country) issues.push(`Complete the identification for person ${index + 1}.`);
        if (['SSN', 'ITIN'].includes(ident.type) && (ident.issuing_country !== 'US' || !/^\d{9}$/.test(normalizeTaxId(ident.number)))) issues.push(`Enter a 9-digit US ${ident.type} for person ${index + 1}.`);
      }
    }
  }
  if (step === 2) {
    if (!details.industry_category_code || !/^ICCV3_[A-Z0-9]+$/.test(details.industry_category_code)) issues.push('Choose your industry from the list.');
    if (!details.intended_products?.length) issues.push('Select at least one way you plan to use FlowCard.');
    if (!details.estimated_monthly_revenue.amount?.trim() || !Number.isFinite(Number(details.estimated_monthly_revenue.amount)) || Number(details.estimated_monthly_revenue.amount) < 0 || !details.estimated_monthly_revenue.currency) issues.push('Provide estimated monthly revenue and select its currency.');
    if (!company.people?.some(person => person.id === details.primary_contact_person_id)) issues.push('Select a primary contact.');
    for (const [index, person] of (company.people ?? []).entries()) {
      const data = details.people?.find(item => item.person_id === person.id);
      if (!data?.platform_roles?.length) issues.push(`Choose the company relationship for person ${index + 1}.`);
      if (!data?.residential_address?.line_1.trim() || !data.residential_address.city.trim() || !data.residential_address.postal_code || !data.residential_address.country || (data.residential_address.country === 'US' && !data.residential_address.state)) issues.push(`Complete the residential address for person ${index + 1}.`);
    }
  }
  return issues;
}
