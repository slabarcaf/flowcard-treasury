import type { OnboardingApiClient } from './OnboardingApiClient.js';
import type { ApplicationRecord, CompanyInput, CompanyRecord, DocumentUpload, OnboardingDetailsInput } from './contracts.js';

/** One in-memory save operation. Never persist identity information in browser storage. */
export class SaveApplication {
  company: CompanyRecord | null = null;
  application: ApplicationRecord | null = null;
  private readonly uploaded = new Set<string>();
  isUploaded(key: string): boolean { return this.uploaded.has(key); }

  async run(api: OnboardingApiClient, input: CompanyInput, details: OnboardingDetailsInput,
    uploads: DocumentUpload[], onCompanySaved: (company: CompanyRecord) => void): Promise<ApplicationRecord> {
    if (!this.company) {
      this.company = await api.createCompany(input);
      onCompanySaved(this.company);
    }
    if (!this.application) this.application = await api.createApplication(this.company.id, details);
    for (const upload of uploads) {
      if (this.uploaded.has(upload.key)) continue;
      this.application = await api.getApplication(this.application.id);
      await api.uploadDocument(this.application, upload);
      this.uploaded.add(upload.key);
    }
    if (uploads.length) this.application = await api.getApplication(this.application.id);
    return this.application;
  }
}
