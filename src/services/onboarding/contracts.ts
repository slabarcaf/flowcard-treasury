/** Wire contracts from company-onboarding/internal/http/dto.go, develop 439d84d.
 * Required-on-create and required-before-platform-review are different contracts.
 * Do not add domain-only fields that the strict request DTO cannot accept.
 */
export interface AddressInput {
  line_1: string;
  line_2?: string;
  city: string;
  state?: string;
  postal_code: string;
  country: string;
}

export const emptyAddress = (): AddressInput => ({ line_1: '', city: '', state: '', postal_code: '', country: '' });

export interface DocumentUpload {
  key: string;
  type: string;
  file: File;
  person_id?: string;
  identification_id?: string;
}

export interface IdentificationInput {
  id?: string;
  type: string;
  number: string;
  issuing_country: string;
  issued_on?: string;
  expires_on?: string;
}

export interface PersonInput {
  id: string;
  first_name: string;
  last_name: string;
  role: string;
  date_of_birth: string;
  identifications?: IdentificationInput[];
}

export interface CompanyInput {
  legal_name: string;
  trading_name?: string;
  entity_type?: string;
  registration_country?: string;
  registration_state?: string;
  business_profile?: {
    website: string;
    description: string;
    registered_address?: AddressInput;
  };
  registration_identifiers?: { type: string; value: string; issuing_country: string }[];
  people?: PersonInput[];
}

export interface OnboardingDetailsInput {
  industry_category_code?: string;
  intended_products?: string[];
  estimated_monthly_revenue: { amount?: string; currency?: string };
  primary_contact_person_id?: string;
  people?: { person_id: string; platform_roles?: string[]; residential_address?: AddressInput }[];
  customer_agreements: {
    agreed_to_terms_and_conditions: boolean;
    agreed_to_data_usage: boolean;
    terms_and_conditions?: {
      agreed_at: string;
      service_agreement_type: string;
      device_data?: { ip_address?: string; user_agent?: string };
    };
  };
}

export interface CompanyRecord {
  id: string;
  revision: number;
  legal_name: string;
  trading_name?: string;
  entity_type?: string;
  registration_country?: string;
  registration_state?: string;
  status?: string;
  business_profile?: CompanyInput['business_profile'];
  registration_identifiers?: CompanyInput['registration_identifiers'];
  people: { person_id: string; first_name?: string; last_name?: string; role?: string; date_of_birth?: string; identifications?: { identification_id: string; type: string; number?: string; issuing_country: string; issued_on?: string; expires_on?: string }[] }[];
  created_at?: string;
}

export interface ApplicationRecord {
  id: string;
  company_id: string;
  status: string;
  revision: number;
  created_at?: string;
  platform?: { name: string; onboarding_details?: OnboardingDetailsInput };
  documents?: { document_id: string; file_name: string; type: string; content_type: string; size_bytes: number }[];
}

export interface CompanyPage { companies: CompanyRecord[]; next_cursor?: string }
export interface ApplicationPage { applications: ApplicationRecord[]; next_cursor?: string }

export interface FieldIssue { field: string; code: string; message: string }
export interface ReadinessResult { code: string; message?: string; errors?: FieldIssue[] }

/** Fail closed on unknown server statuses; never infer KYB approval from company ACTIVE. */
export function applicationStatus(status: string): string {
  return ({
    DRAFT: 'Application saved',
    SUBMISSION_PENDING: 'Submission pending',
    UNDER_REVIEW: 'Under review',
    INFORMATION_REQUIRED: 'More information required',
    COMPLETED: 'Onboarding completed',
    REJECTED: 'Application rejected',
    CANCELLED: 'Application cancelled',
  } as Record<string, string>)[status] ?? 'Status unavailable';
}

/** A customer acknowledgement after all writes succeed; not an Airwallex status. */
export function customerAcknowledgement(status: string): string {
  return status === 'DRAFT' ? 'Pending review' : applicationStatus(status);
}
