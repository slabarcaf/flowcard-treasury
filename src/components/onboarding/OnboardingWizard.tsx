import { useState } from 'react';
import type { FormEvent } from 'react';
import { LocalSessionForm } from './LocalSessionForm.js';
import { AuroraBackground } from './AuroraBackground.js';
import { useCompanyApplication } from '../../hooks/useCompanyApplication.js';
import { applicationStatus } from '../../services/onboarding/contracts.js';
import type { DocumentUpload, OnboardingDetailsInput } from '../../services/onboarding/contracts.js';
import { newCompany, stepIssues } from '../../services/onboarding/formRules.js';
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
  const locked = saved.busy || saved.uncertain || Boolean(saved.pendingSubmission || saved.submissionReceipt) ||
    Boolean(saved.savedDraft && !['DRAFT', 'INFORMATION_REQUIRED'].includes(saved.savedDraft.status));
  const submissionStatus = saved.submissionReceipt
    ? saved.submissionRecords.find(item => item.submission_id === saved.submissionReceipt?.submission_id)?.status
    : undefined;
  const people = company.people ?? [];
  function move(next: number) {
    setStep(next); setIssues([]);
    window.scrollTo({ top: 0, behavior: 'instant' });
  }

  function validateAllSteps(): boolean {
    const editingSavedCompany = saved.company !== null;
    const invalidStep = [0, 1, 2].find(page => stepIssues(page, company, details, editingSavedCompany).length);
    if (invalidStep !== undefined) {
      setStep(invalidStep);
      setIssues(stepIssues(invalidStep, company, details, editingSavedCompany));
      window.scrollTo({ top: 0, behavior: 'instant' });
      return false;
    }
    setIssues([]);
    return true;
  }

  function saveDraft() {
    if (!validateAllSteps()) return;
    void saved.saveDraft(company, details, uploads);
  }

  function verifyAndSubmit() {
    if (!validateAllSteps()) return;
    void saved.verifyAndSubmit(company, details, uploads);
  }

  function advance(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const editingSavedCompany = saved.company !== null;
    if (step === 3) {
      saveDraft();
      return;
    }
    const errors = stepIssues(step, company, details, editingSavedCompany);
    if (errors.length) { setIssues(errors); return; }
    setIssues([]);
    if (step < 3) { move(step + 1); return; }
  }
  return <div className="min-h-screen relative" style={{ backgroundColor: '#0D1421' }}>
    <AuroraBackground />
    <main className="relative z-10 w-full max-w-2xl mx-auto px-5 py-10 text-white">
      <p className="text-sm text-slate-400 mb-6">FlowCard · Business application</p>
      {saved.submissionReceipt && <section className="mb-6 rounded-xl border border-green-400/40 p-4 space-y-2" aria-live="polite">
        <p className="font-medium">Submission queued · not provider approval</p>
        <p className="text-sm break-all">Application {saved.submissionReceipt.application_id} · Submission {saved.submissionReceipt.submission_id}</p>
        {saved.busy && <p className="text-sm text-slate-400">Checking current application status…</p>}
        {saved.savedDraft && !saved.busy && !saved.submissionStatusError && <p className="text-sm text-slate-300">Current application status: {saved.savedDraft.status}</p>}
        {saved.savedDraft && saved.submissionStatusError && <p className="text-sm text-slate-400">Last retrieved application status: {saved.savedDraft.status}</p>}
        {submissionStatus && <p className="text-sm text-slate-300">Submission status: {submissionStatus === 'ACCEPTED' ? 'Accepted for processing · not final approval' : submissionStatus}</p>}
        {saved.submissionStatusError && <p role="alert" className="text-sm text-amber-200">The receipt is saved, but current application status could not be refreshed: {saved.submissionStatusError.message}</p>}
        <button type="button" disabled={saved.busy} onClick={() => void saved.refresh()} className="revolut-link">{saved.busy ? 'Refreshing…' : 'Refresh application status'}</button>
        <button type="button" onClick={onComplete} className="revolut-link">Return to home</button>
      </section>}
      {saved.pendingSubmission && !saved.submissionReceipt && <section className="mb-6 rounded-xl border border-amber-400/40 p-4 space-y-2" aria-live="polite">
        <p className="font-medium">Submission outcome not confirmed</p>
        <p className="text-sm">A retry will replay the same request and idempotency key. Do not edit or verify this draft again until the result is confirmed.</p>
        {saved.error && <p role="alert" className="text-sm text-amber-200">{saved.error.message}</p>}
        <button type="button" disabled={saved.busy} onClick={() => void saved.refresh()} className="revolut-link">{saved.busy ? 'Refreshing…' : 'Refresh application status'}</button>
        {saved.pendingSubmission.retryable
          ? <button type="button" disabled={saved.busy} onClick={() => void saved.retrySubmission()} className="revolut-btn-primary w-full disabled:opacity-40">{saved.busy ? 'Retrying submission…' : 'Retry same submission'}</button>
          : <p className="text-sm text-red-200">The key conflicts with a different request. Keep this draft and contact the operator to reconcile it.</p>}
      </section>}
      {saved.savedDraft && !saved.submissionReceipt && !saved.pendingSubmission && <section className="mb-6 rounded-xl border border-slate-700 p-4 space-y-2" aria-live="polite">
        <p className="font-medium">Saved application · {applicationStatus(saved.savedDraft.status)}</p>
        <p className="text-sm text-slate-400 break-all">Application {saved.savedDraft.id} · Status {saved.savedDraft.status}</p>
        <button type="button" disabled={saved.busy} onClick={() => void saved.refresh()} className="revolut-link">{saved.busy ? 'Refreshing…' : 'Refresh saved status'}</button>
        <button type="button" onClick={onComplete} className="revolut-link">Return to home</button>
      </section>}
      <>
        <ol className="flex gap-4 text-xs text-slate-400 mb-8" aria-label="Application progress">{STEPS.map((title, i) => <li key={title} aria-current={step === i ? 'step' : undefined} className={step === i ? 'text-white' : ''}>{i + 1}. {title}</li>)}</ol>
        <h1 className="text-2xl font-bold mb-3">{step === 3 ? 'Review your information' : `${STEPS[step]} details`}</h1>
        <p className="text-sm text-slate-400 mb-7">Complete all required information for your application. Fields marked optional can be left blank.</p>
        {signedIn && <p role="status" className="mb-5 text-sm text-green-300">Signed in for local testing. Your entries are still here. Click Save draft again.</p>}
        <form onSubmit={advance}>
          <fieldset disabled={locked}>
            {step === 0 && <CompanyApplicationStep value={company} onChange={next => { saved.clearReadiness(); setCompany(next); }} fieldIssues={saved.readiness?.errors} savedCompany={saved.savedCompanyInput} allowProfileValueClears={saved.company !== null} />}
            {step === 1 && <PeopleApplicationStep people={people} fieldIssues={saved.readiness?.errors} onChange={next => {
              saved.clearReadiness();
              const personIds = new Set(next.map(person => person.id));
              const identificationIds = new Set(next.flatMap(person => (person.identifications ?? []).map(item => item.id).filter((id): id is string => Boolean(id))));
              setCompany({ ...company, people: next });
              setDetails({ ...details,
                primary_contact_person_id: personIds.has(details.primary_contact_person_id ?? '') ? details.primary_contact_person_id : undefined,
                people: details.people?.filter(person => personIds.has(person.person_id)),
              });
              setUploads(current => current.filter(upload =>
                (!upload.person_id || personIds.has(upload.person_id)) &&
                (!upload.identification_id || identificationIds.has(upload.identification_id))));
            }} />}
            {step === 2 && <UsageApplicationStep value={details} people={people} fieldIssues={saved.readiness?.errors} onChange={next => { saved.clearReadiness(); setDetails(next); }} onGoToPeople={() => move(1)} />}
            {step === 3 && <ReviewApplicationStep company={company} details={details} fieldIssues={saved.readiness?.errors} readiness={saved.readiness} uploads={uploads} onUploadsChange={next => { saved.clearReadiness(); setUploads(next); }} isUploaded={saved.isUploaded} onEditStep={move} isSaving={saved.busy} onVerifyAndSubmit={verifyAndSubmit} />}
          </fieldset>
          {issues.length > 0 && <div role="alert" className="mt-5 rounded-xl border border-red-400/40 p-4 text-red-200 text-sm"><ul className="space-y-1">{issues.map(issue => <li key={issue}>{issue}</li>)}</ul></div>}
          <div className="flex gap-4 mt-8">
            {step > 0 && <button type="button" disabled={locked} onClick={() => move(step - 1)} className="revolut-link disabled:opacity-40">Back</button>}
            {step < 3 && <button type="submit" disabled={locked} className="revolut-btn-primary w-full disabled:opacity-40">Continue</button>}
          </div>
        </form>
      </>
      {saved.error && <div role="alert" className="mt-6 rounded-xl border border-red-400/40 p-4 text-sm text-red-200">
        <p>{saved.error.message}</p>
        {saved.error.status === 401 && import.meta.env.DEV && <LocalSessionForm onConnected={() => { saved.clearError(); setSignedIn(true); }} />}
        {saved.error.issues.length > 0 && <ul className="mt-2 space-y-1">{saved.error.issues.map((issue, i) => <li key={i}>{issue.field}: {issue.message}</li>)}</ul>}
        {saved.error.code === 'STORAGE_UNAVAILABLE' && <p className="mt-2">Your company and application were saved before the upload failed. Remove the pending files above, then retry to continue this local test without attachments.</p>}
        {saved.company && !saved.savedDraft && saved.error.code !== 'STORAGE_UNAVAILABLE' && <p className="mt-2">Company saved: {saved.company.id}. Saving this draft will continue with that company rather than create another.</p>}
        {saved.canRecover && !saved.pendingSubmission && !saved.submissionReceipt && <div className="mt-4 space-y-2">
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
