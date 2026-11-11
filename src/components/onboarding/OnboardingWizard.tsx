import { useState } from 'react';
import type { FormEvent } from 'react';
import { LocalSessionForm } from './LocalSessionForm.js';
import { AuroraBackground } from './AuroraBackground.js';
import { useCompanyApplication } from '../../hooks/useCompanyApplication.js';
import { customerAcknowledgement } from '../../services/onboarding/contracts.js';
import type { DocumentUpload, OnboardingDetailsInput } from '../../services/onboarding/contracts.js';
import { newCompany, normalizeTaxId, stepIssues } from '../../services/onboarding/formRules.js';
import { CompanyApplicationStep } from './steps/CompanyApplicationStep.js';
import { PeopleApplicationStep } from './steps/PeopleApplicationStep.js';
import { UsageApplicationStep } from './steps/UsageApplicationStep.js';
import { ReviewApplicationStep } from './steps/ReviewApplicationStep.js';

const STEPS = ['Company', 'People', 'Application', 'Review'];
const initialDetails = (): OnboardingDetailsInput => ({ estimated_monthly_revenue: {}, customer_agreements: { agreed_to_terms_and_conditions: false, agreed_to_data_usage: false } });

export function OnboardingWizard({ onComplete }: { onComplete: () => void }) {
  const [step, setStep] = useState(0);
  const [company, setCompany] = useState(newCompany);
  const [details, setDetails] = useState<OnboardingDetailsInput>(initialDetails);
  const [uploads, setUploads] = useState<DocumentUpload[]>([]);
  const [issues, setIssues] = useState<string[]>([]);
  const [signedIn, setSignedIn] = useState(false);
  const saved = useCompanyApplication();
  const locked = saved.busy || saved.company !== null || saved.uncertain;
  const people = company.people ?? [];
  function move(next: number) {
    setStep(next); setIssues([]);
    window.scrollTo({ top: 0, behavior: 'instant' });
  }
  function advance(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const invalidStep = step === 3 ? [0, 1, 2].find(page => stepIssues(page, company, details).length) : undefined;
    const errors = stepIssues(invalidStep ?? step, company, details);
    if (errors.length) { if (invalidStep !== undefined) setStep(invalidStep); setIssues(errors); return; }
    setIssues([]);
    if (step < 3) { move(step + 1); return; }
    void saved.save({ ...company,
      registration_identifiers: company.registration_identifiers?.map(identifier => ({ ...identifier, value: normalizeTaxId(identifier.value) })),
      people: people.map(person => ({ ...person, identifications: person.identifications?.map(ident => ({ ...ident, number: ['SSN', 'ITIN'].includes(ident.type) ? normalizeTaxId(ident.number) : ident.number })) })),
    }, details, uploads);
  }
  return <div className="min-h-screen relative" style={{ backgroundColor: '#0D1421' }}>
    <AuroraBackground />
    <main className="relative z-10 w-full max-w-2xl mx-auto px-5 py-10 text-white">
      <p className="text-sm text-slate-400 mb-6">FlowCard · Business application</p>
      {saved.application ? <section className="space-y-6" aria-live="polite">
        <h1 className="text-3xl font-bold">{customerAcknowledgement(saved.application.status)}</h1>
        <p className="text-slate-300">Your information for <strong>{company.legal_name}</strong> has been saved.</p>
        {saved.application.status === 'DRAFT' && <p className="text-slate-400">Your information is saved with FlowCard and is waiting for review. It has not been submitted to Airwallex. Funding remains unavailable.</p>}
        <p className="text-sm text-slate-400 break-all">Reference: {saved.application.id} · Status: {saved.application.status}</p>
        <button type="button" disabled={saved.busy} onClick={() => void saved.refresh()} className="revolut-btn-primary w-full">{saved.busy ? 'Checking…' : 'Refresh status'}</button>
        <button type="button" onClick={onComplete} className="revolut-link w-full">Return to home</button>
      </section> : <>
        <ol className="flex gap-4 text-xs text-slate-400 mb-8" aria-label="Application progress">{STEPS.map((title, i) => <li key={title} aria-current={step === i ? 'step' : undefined} className={step === i ? 'text-white' : ''}>{i + 1}. {title}</li>)}</ol>
        <h1 className="text-2xl font-bold mb-3">{step === 3 ? 'Review your information' : `${STEPS[step]} details`}</h1>
        <p className="text-sm text-slate-400 mb-7">Complete all required information for your application. Fields marked optional can be left blank.</p>
        {signedIn && <p role="status" className="mb-5 text-sm text-green-300">Signed in for local testing. Your entries are still here. Click Submit information again.</p>}
        <form onSubmit={advance}>
          <fieldset disabled={step === 3 ? saved.busy || saved.uncertain : locked}>
            {step === 0 && <CompanyApplicationStep value={company} onChange={setCompany} />}
            {step === 1 && <PeopleApplicationStep people={people} onChange={next => {
              const ids = new Set(next.map(person => person.id));
              setCompany({ ...company, people: next });
              setDetails({ ...details, primary_contact_person_id: ids.has(details.primary_contact_person_id ?? '') ? details.primary_contact_person_id : undefined, people: details.people?.filter(person => ids.has(person.person_id)) });
              setUploads(current => current.filter(upload => !upload.person_id || ids.has(upload.person_id)));
            }} />}
            {step === 2 && <UsageApplicationStep value={details} people={people} onChange={setDetails} />}
            {step === 3 && <ReviewApplicationStep company={company} details={details} uploads={uploads} onUploadsChange={setUploads} isUploaded={saved.isUploaded} />}
          </fieldset>
          {issues.length > 0 && <div role="alert" className="mt-5 rounded-xl border border-red-400/40 p-4 text-red-200 text-sm"><ul className="space-y-1">{issues.map(issue => <li key={issue}>{issue}</li>)}</ul></div>}
          <div className="flex gap-4 mt-8">
            {step > 0 && <button type="button" disabled={locked} onClick={() => move(step - 1)} className="revolut-link disabled:opacity-40">Back</button>}
            <button type="submit" disabled={saved.busy || saved.uncertain} className="revolut-btn-primary w-full disabled:opacity-40">{saved.busy ? 'Saving…' : step === 3 ? 'Submit information' : 'Continue'}</button>
          </div>
        </form>
      </>}
      {saved.error && <div role="alert" className="mt-6 rounded-xl border border-red-400/40 p-4 text-sm text-red-200">
        <p>{saved.error.message}</p>
        {saved.error.status === 401 && import.meta.env.DEV && <LocalSessionForm onConnected={() => { saved.clearError(); setSignedIn(true); }} />}
        {saved.error.issues.length > 0 && <ul className="mt-2 space-y-1">{saved.error.issues.map((issue, i) => <li key={i}>{issue.field}: {issue.message}</li>)}</ul>}
        {saved.error.code === 'STORAGE_UNAVAILABLE' && <p className="mt-2">Your company and application were saved before the upload failed. Remove the pending files above, then retry to continue this local test without attachments.</p>}
        {saved.company && saved.error.code !== 'STORAGE_UNAVAILABLE' && <p className="mt-2">Company saved: {saved.company.id}. Its information is locked while application creation is retried.</p>}
        {saved.canRecover && !saved.application && <div className="mt-4 space-y-2">
          <p>Continue with the application already saved on the server. Pending files on this page will not be uploaded. Files already saved on the server will remain attached.</p>
          <button type="button" disabled={saved.busy} className="revolut-btn-primary w-full disabled:opacity-40" onClick={async () => {
            if (await saved.recoverSavedApplication()) setUploads([]);
          }}>{saved.busy ? 'Checking saved application…' : 'Continue with saved application (without pending files)'}</button>
        </div>}
        {saved.uncertain && <p className="mt-2">We could not confirm the result. To avoid a duplicate application, retries are paused. Keep this page open while FlowCard checks the request.</p>}
      </div>}
    </main>
  </div>;
}
