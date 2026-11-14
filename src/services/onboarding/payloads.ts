import type {
  AddressInput,
  CompanyInput,
  CreateCompanyRequest,
  EditCompanyRequest,
  FieldIssue,
  OnboardingDetailsInput,
  PlatformOnboardingDetailsRequest,
} from './contracts.js';
import { normalizeTaxId } from './formRules.js';

function optionalText(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

function serializeAddress(address: AddressInput): AddressInput {
  return {
    line_1: address.line_1.trim(),
    ...(optionalText(address.line_2) ? { line_2: address.line_2!.trim() } : {}),
    city: address.city.trim(),
    ...(optionalText(address.state) ? { state: address.state!.trim() } : {}),
    postal_code: address.postal_code.trim(),
    country: address.country.trim(),
  };
}

export function serializeCompanyRequest(company: CompanyInput): CreateCompanyRequest {
  const profile = company.business_profile;
  const identifiers = company.registration_identifiers ?? [];
  const people = company.people ?? [];
  const entityType = company.entity_type === 'OTHER'
    ? optionalText(company.entity_type_other)
    : optionalText(company.entity_type);

  return {
    legal_name: company.legal_name.trim(),
    ...(optionalText(company.trading_name) ? { trading_name: company.trading_name!.trim() } : {}),
    ...(entityType ? { entity_type: entityType } : {}),
    ...(optionalText(company.registration_country) ? { registration_country: company.registration_country!.trim() } : {}),
    ...(optionalText(company.registration_state) ? { registration_state: company.registration_state!.trim() } : {}),
    ...(profile ? {
      business_profile: {
        website: profile.website.trim(),
        description: profile.description.trim(),
        ...(profile.registered_address ? { registered_address: serializeAddress(profile.registered_address) } : {}),
      },
    } : {}),
    ...(identifiers.length ? {
      registration_identifiers: identifiers.map(identifier => {
        const type = identifier.type === 'OTHER' ? optionalText(identifier.other_type) ?? '' : identifier.type.trim();
        const value = identifier.value.trim();
        return {
          type,
          value: type === 'EIN' && identifier.issuing_country === 'US' ? normalizeTaxId(value) : value,
          issuing_country: identifier.issuing_country.trim(),
        };
      }),
    } : {}),
    ...(people.length ? {
      people: people.map(person => ({
        id: person.id,
        first_name: person.first_name.trim(),
        last_name: person.last_name.trim(),
        role: person.role.trim(),
        date_of_birth: person.date_of_birth.trim(),
        ...(person.identifications?.length ? {
          identifications: person.identifications.map(identification => {
            const type = identification.type === 'OTHER'
              ? optionalText(identification.other_type) ?? ''
              : identification.type.trim();
            const number = identification.number.trim();
            return {
              ...(identification.id ? { id: identification.id } : {}),
              type,
              number: ['SSN', 'ITIN'].includes(type) && identification.issuing_country === 'US'
                ? normalizeTaxId(number)
                : number,
              issuing_country: identification.issuing_country.trim(),
              ...(optionalText(identification.issued_on) ? { issued_on: identification.issued_on!.trim() } : {}),
              ...(optionalText(identification.expires_on) ? { expires_on: identification.expires_on!.trim() } : {}),
            };
          }),
        } : {}),
      })),
    } : {}),
  };
}

export function serializeCompanyUpdateRequest(
  company: CompanyInput,
  expectedRevision: number,
): EditCompanyRequest {
  const createPayload = serializeCompanyRequest(company);
  const updatePayload: EditCompanyRequest = {
    expected_revision: expectedRevision,
    legal_name: createPayload.legal_name,
    ...(company.trading_name !== undefined ? { trading_name: company.trading_name.trim() } : {}),
    ...(company.entity_type !== undefined ? {
      entity_type: company.entity_type === 'OTHER'
        ? company.entity_type_other?.trim() ?? ''
        : company.entity_type.trim(),
    } : {}),
    ...(company.registration_country !== undefined ? { registration_country: company.registration_country.trim() } : {}),
    ...(company.registration_state !== undefined ? { registration_state: company.registration_state.trim() } : {}),
    ...(company.business_profile ? {
      business_profile: {
        website: company.business_profile.website.trim(),
        description: company.business_profile.description.trim(),
        ...(company.business_profile.registered_address
          ? { registered_address: serializeAddress(company.business_profile.registered_address) }
          : {}),
      },
    } : {}),
    ...(company.registration_identifiers !== undefined
      ? { registration_identifiers: createPayload.registration_identifiers ?? [] }
      : {}),
    ...(company.people !== undefined ? { people: createPayload.people ?? [] } : {}),
  };
  return updatePayload;
}

export function unsupportedCompanyClearIssues(
  previous: CompanyInput,
  current: CompanyInput,
): FieldIssue[] {
  const issues: FieldIssue[] = [];
  if (previous.business_profile && !current.business_profile) {
    issues.push({
      field: 'business_profile',
      code: 'unsupported_clear',
      message: 'The current API cannot remove a saved business profile. Keep the section enabled and clear individual supported fields instead.',
    });
  } else if (previous.business_profile?.registered_address && !current.business_profile?.registered_address) {
    issues.push({
      field: 'business_profile.registered_address',
      code: 'unsupported_clear',
      message: 'The current API cannot remove a saved registered address. Keep the address section enabled to preserve the saved value.',
    });
  }
  return issues;
}

export function serializeApplicationDetails(
  details: OnboardingDetailsInput,
): PlatformOnboardingDetailsRequest {
  const productCodes = (details.intended_products ?? [])
    .map(code => code.trim())
    .filter(Boolean);
  const personDetails = (details.people ?? []).flatMap(person => {
    const platformRoles = (person.platform_roles ?? [])
      .map(role => role.trim())
      .filter(Boolean);
    if (!platformRoles.length && !person.residential_address) return [];

    return [{
      person_id: person.person_id,
      ...(platformRoles.length ? { platform_roles: platformRoles } : {}),
      ...(person.residential_address
        ? { residential_address: serializeAddress(person.residential_address) }
        : {}),
    }];
  });

  return {
    ...(optionalText(details.industry_category_code)
      ? { industry_category_code: details.industry_category_code!.trim() }
      : {}),
    ...(productCodes.length ? { intended_products: productCodes } : {}),
    estimated_monthly_revenue: {
      ...(optionalText(details.estimated_monthly_revenue.amount)
        ? { amount: details.estimated_monthly_revenue.amount!.trim() }
        : {}),
      ...(optionalText(details.estimated_monthly_revenue.currency)
        ? { currency: details.estimated_monthly_revenue.currency!.trim() }
        : {}),
    },
    ...(optionalText(details.primary_contact_person_id)
      ? { primary_contact_person_id: details.primary_contact_person_id!.trim() }
      : {}),
    ...(personDetails.length ? { people: personDetails } : {}),
    customer_agreements: {
      agreed_to_terms_and_conditions: details.customer_agreements.agreed_to_terms_and_conditions,
      agreed_to_data_usage: details.customer_agreements.agreed_to_data_usage,
    },
  };
}
