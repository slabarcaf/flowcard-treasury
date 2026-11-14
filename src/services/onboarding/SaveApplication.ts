import type { OnboardingApiClient } from './OnboardingApiClient.js';
import type { ApplicationRecord, CompanyInput, CompanyRecord, DocumentUpload, OnboardingDetailsInput } from './contracts.js';
import { serializeApplicationDetails, serializeCompanyRequest } from './payloads.js';

/** One in-memory draft save operation. Never persist identity data in browser storage. */
export class SaveApplication {
  company: CompanyRecord | null = null;
  application: ApplicationRecord | null = null;
  private savedCompanyInput: CompanyInput | null = null;
  private savedDetails: OnboardingDetailsInput | null = null;
  private readonly uploaded = new Set<string>();

  isUploaded(key: string): boolean { return this.uploaded.has(key); }
  get savedCompanySnapshot(): CompanyInput | null {
    return this.savedCompanyInput ? copy(this.savedCompanyInput) : null;
  }

  async run(
    api: OnboardingApiClient,
    input: CompanyInput,
    details: OnboardingDetailsInput,
    uploads: DocumentUpload[],
    onCompanySaved: (company: CompanyRecord) => void,
    onApplicationSaved: (application: ApplicationRecord) => void = () => {},
  ): Promise<ApplicationRecord> {
    let companyChanged = false;
    if (!this.company) {
      this.company = await api.createCompany(serializeCompanyRequest(input));
      this.savedCompanyInput = copy(input);
      onCompanySaved(this.company);
    } else if (this.savedCompanyInput && !sameCompanyInput(input, this.savedCompanyInput)) {
      const previous = this.savedCompanyInput;
      this.company = await api.getCompany(this.company.id);
      onCompanySaved(this.company);
      this.company = await api.updateCompany(this.company, input, previous);
      this.savedCompanyInput = copy(input);
      companyChanged = true;
      onCompanySaved(this.company);
    }

    if (!this.application) {
      this.application = await api.createApplication(this.company.id, details);
      this.savedDetails = copy(details);
      onApplicationSaved(this.application);
    } else {
      const detailsChanged = !this.savedDetails || !sameDetails(details, this.savedDetails);
      if (companyChanged || detailsChanged) {
        this.application = await api.getApplication(this.application.id);
        onApplicationSaved(this.application);
        if (detailsChanged) {
          this.application = await api.updateApplication(this.application, details);
          this.savedDetails = copy(details);
          onApplicationSaved(this.application);
        }
      }
    }

    for (const upload of uploads) {
      if (this.uploaded.has(upload.key)) continue;
      this.application = await api.getApplication(this.application.id);
      onApplicationSaved(this.application);
      await api.uploadDocument(this.application, upload);
      this.uploaded.add(upload.key);
    }
    if (uploads.length) {
      this.application = await api.getApplication(this.application.id);
      onApplicationSaved(this.application);
    }
    return this.application;
  }
}

function copy<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function sameCompanyInput(left: CompanyInput, right: CompanyInput): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function sameDetails(left: OnboardingDetailsInput, right: OnboardingDetailsInput): boolean {
  return JSON.stringify(serializeApplicationDetails(left)) === JSON.stringify(serializeApplicationDetails(right));
}
