/** Wire contracts from company-onboarding/internal/http/dto.go, develop 7c83cb8.
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
  other_type?: string;
  number: string;
  issuing_country: string;
  issued_on?: string;
  expires_on?: string;
}

export interface RegistrationIdentifierInput {
  client_id: string;
  type: string;
  other_type?: string;
  value: string;
  issuing_country: string;
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
  entity_type_other?: string;
  registration_country?: string;
  registration_state?: string;
  business_profile?: {
    website: string;
    description: string;
    registered_address?: AddressInput;
  };
  registration_identifiers?: RegistrationIdentifierInput[];
  people?: PersonInput[];
}

export interface RegistrationIdentifierRequest {
  type: string;
  value: string;
  issuing_country: string;
}

export interface IdentificationRequest {
  id?: string;
  type: string;
  number: string;
  issuing_country: string;
  issued_on?: string;
  expires_on?: string;
}

export interface PersonRequest {
  id?: string;
  first_name: string;
  last_name: string;
  role: string;
  date_of_birth: string;
  identifications?: IdentificationRequest[];
}

/** Strict POST /api/companies payload, without form-only labels or application data. */
export interface CreateCompanyRequest {
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
  registration_identifiers?: RegistrationIdentifierRequest[];
  people?: PersonRequest[];
}

/** Strict PATCH /api/companies payload, excluding every form-only property. */
export interface EditCompanyRequest {
  expected_revision: number;
  legal_name?: string;
  trading_name?: string;
  entity_type?: string;
  registration_country?: string;
  registration_state?: string;
  business_profile?: {
    website?: string;
    description?: string;
    registered_address?: AddressInput;
  };
  registration_identifiers?: RegistrationIdentifierRequest[];
  people?: PersonRequest[];
  reason?: string;
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
  };
}

export interface PlatformOnboardingDetailsRequest {
  industry_category_code?: string;
  intended_products?: string[];
  estimated_monthly_revenue: { amount?: string; currency?: string };
  primary_contact_person_id?: string;
  people?: { person_id: string; platform_roles?: string[]; residential_address?: AddressInput }[];
  customer_agreements: {
    agreed_to_terms_and_conditions: boolean;
    agreed_to_data_usage: boolean;
  };
}

export interface CreateApplicationRequest {
  company_id: string;
  platform_name: string;
  environment?: string;
  onboarding_details: PlatformOnboardingDetailsRequest;
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
  has_submitted_history?: boolean;
  business_profile?: BusinessProfileRecord;
  registration_identifiers?: { type: string; value: string; issuing_country: string }[];
  people: CompanyPersonRecord[];
  created_at?: string;
  updated_at?: string;
}

export interface AddressRecord {
  line_1?: string;
  line_2?: string;
  city?: string;
  state?: string;
  postal_code?: string;
  country?: string;
}

export interface BusinessProfileRecord {
  description?: string;
  industry?: string;
  website?: string;
  email?: string;
  phone?: string;
  registered_address?: AddressRecord;
  operating_address?: AddressRecord;
  operating_countries?: string[];
  products?: string;
}

export interface IdentificationRecord {
  identification_id: string;
  type?: string;
  issuing_country?: string;
  number?: string;
  issued_on?: string;
  expires_on?: string;
}

export interface CompanyPersonRecord {
  person_id: string;
  first_name?: string;
  middle_name?: string;
  last_name?: string;
  date_of_birth?: string;
  nationality?: string;
  email?: string;
  phone?: string;
  role?: string;
  identifications?: IdentificationRecord[];
}

export interface ApplicationDocumentRecord {
  document_id: string;
  type: string;
  person_id?: string;
  identification_id?: string;
  file_name: string;
  content_type: string;
  size_bytes: number;
  uploaded_by: string;
  uploaded_at: string;
}

export interface InformationRequestRecord {
  information_request_id: string;
  platform_reference?: string;
  status: string;
  questions: { question_id: string; platform_question_id?: string; text: string; person_id?: string }[];
  received_at: string;
  resolved_at?: string;
}

export interface ApplicationOnboardingDetailsRecord extends OnboardingDetailsInput {
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

export interface ApplicationRecord {
  id: string;
  company_id: string;
  status: string;
  revision: number;
  created_at?: string;
  updated_at?: string;
  closed_at?: string;
  creator?: string;
  operator_review_flagged?: boolean;
  company?: CompanyRecord;
  platform?: { name: string; environment?: string; platform_account_id?: string; decision?: string; onboarding_details?: ApplicationOnboardingDetailsRecord };
  documents?: ApplicationDocumentRecord[];
  information_requests?: InformationRequestRecord[];
}

export interface SubmissionRecord {
  submission_id: string;
  type: string;
  status: string;
  company: { id: string };
  created_at: string;
  accepted_at?: string;
}

/** Existing GET /api/onboarding-applications/{id}/submissions response. */
export type SubmissionListResponse = SubmissionRecord[];

export interface CompanyPage { companies: CompanyRecord[]; next_cursor?: string }
export interface ApplicationPage { applications: ApplicationRecord[]; next_cursor?: string }

export interface FieldIssue { field: string; code: string; param?: string; message: string }
export interface ReadinessResult {
  code: string;
  message?: string;
  application_revision?: number;
  company_revision?: number;
  errors?: FieldIssue[];
}

/** HTTP 202 receipt means queued for processing, not provider approval. */
export interface SubmissionReceipt {
  code: 'OK';
  application_id: string;
  submission_id: string;
}

/** Fail closed on unknown server statuses; never infer KYB approval from company ACTIVE. */
export function applicationStatus(status: string): string {
  return ({
    DRAFT: 'Draft saved',
    SUBMISSION_PENDING: 'Submission pending',
    UNDER_REVIEW: 'Under review',
    INFORMATION_REQUIRED: 'More information required',
    COMPLETED: 'Onboarding completed',
    REJECTED: 'Application rejected',
    CANCELLED: 'Application cancelled',
  } as Record<string, string>)[status] ?? 'Status unavailable';
}

/** A customer acknowledgement follows the saved application status. */
export function customerAcknowledgement(status: string): string {
  return applicationStatus(status);
}
