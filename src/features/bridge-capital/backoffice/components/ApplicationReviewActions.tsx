import { useEffect, useId, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import type { ApplicationRecord, SubmissionReceipt } from '../../../../services/onboarding/contracts.js';
import { applicationStatus } from '../../../../services/onboarding/contracts.js';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import { color, font, radius, space } from '../tokens.js';
import { Badge, Button } from './primitives.js';
import './application-review.css';

const choices = [
  { key: 'submit', title: 'Submit to Airwallex', description: 'Queue the application for provider processing', heading: 'Submit this application to Airwallex?', confirm: 'Confirm Submission', accent: color.positive },
  { key: 'request', title: 'Request More Info', description: 'Ask the applicant for additional details', heading: 'Request additional information', confirm: 'Send Request', accent: color.warning },
  { key: 'reject', title: 'Reject Application', description: 'Close this application', heading: 'Reject this application?', confirm: 'Reject Application', accent: color.critical },
] as const;
type Choice = typeof choices[number];

function ActionIcon({ action }: { action: Choice }) {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={action.accent} strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {action.key === 'submit' ? <><path d="m22 2-7 20-4-9-9-4Z" /><path d="M22 2 11 13" /></> : action.key === 'request' ? <path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z" /> : <><circle cx="12" cy="12" r="9" /><path d="m9 9 6 6m0-6-6 6" /></>}
  </svg>;
}

export function ApplicationReviewActions({ application, onUpdated }: { application: ApplicationRecord; onUpdated: (value: ApplicationRecord) => void }) {
  const api = useRef(new OnboardingApiClient()).current;
  const [open, setOpen] = useState(false);
  const [choice, setChoice] = useState<Choice | null>(null);
  const [messages, setMessages] = useState({ request: '', reject: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const [submissionReceipt, setSubmissionReceipt] = useState<SubmissionReceipt | null>(null);
  const [submissionAttempt, setSubmissionAttempt] = useState<{ applicationId: string; expectedRevision: number; idempotencyKey: string; retryable: boolean } | null>(null);
  const inFlight = useRef(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const helpId = useId();
  const editable = application.status === 'DRAFT' && submissionReceipt === null;
  const message = choice && choice.key !== 'submit' ? messages[choice.key] : '';

  useEffect(() => {
    if (choice) dialog.current?.showModal();
  }, [choice]);
  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLButtonElement>('button')?.focus();
    const dismiss = (event: PointerEvent) => { if (!menu.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [open]);

  function close() {
    if (inFlight.current) return;
    dialog.current?.close(); setChoice(null);
    if (!uncertain) setError('');
    trigger.current?.focus();
  }

  async function refreshUncertainSubmissionStatus() {
    const attempt = submissionAttempt;
    if (!attempt || inFlight.current) return;
    inFlight.current = true; setBusy(true);
    try {
      const latest = await api.getApplication(attempt.applicationId);
      if (latest.id !== attempt.applicationId || latest.company_id !== application.company_id) {
        throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      }
      onUpdated(latest);
      setSuccess(`Application status refreshed: ${latest.status}. The submission request remains unresolved.`);
    } catch {
      setError('Application status could not be refreshed. Keep the original idempotency key and reconcile before starting another submission.');
    } finally { inFlight.current = false; setBusy(false); }
  }
  async function confirm() {
    const retryingSubmission = choice?.key === 'submit' && submissionAttempt !== null;
    if (!choice || inFlight.current || (!editable && !retryingSubmission) || submissionReceipt || (uncertain && !retryingSubmission) || (choice.key !== 'submit' && !message.trim())) return;
    if (choice.key !== 'submit') {
      setError(choice.key === 'request' ? 'Not sent. The backend does not yet support saving and delivering information requests. Your message is preserved here.' : 'Not rejected. The backend does not yet support internal rejection with a reason. Your reason is preserved here.');
      return;
    }
    inFlight.current = true; setBusy(true); setError(''); setSuccess('');
    try {
      const attempt = submissionAttempt ?? {
        applicationId: application.id,
        expectedRevision: application.revision,
        idempotencyKey: crypto.randomUUID(),
        retryable: true,
      };
      setSubmissionAttempt(attempt);
      const receipt = await api.submitApplication(attempt.applicationId, attempt.expectedRevision, attempt.idempotencyKey);
      setSubmissionReceipt(receipt);
      setSubmissionAttempt(null);
      setUncertain(false);
      dialog.current?.close(); setChoice(null);
      try {
        const updated = await api.getApplication(receipt.application_id);
        if (updated.id !== receipt.application_id || updated.company_id !== application.company_id) {
          throw new OnboardingApiError(0, 'INVALID_RESPONSE');
        }
        onUpdated(updated);
        setSuccess(`Submission ${receipt.submission_id} queued. Application status: ${updated.status}. Queueing is not provider approval.`);
      } catch {
        setSuccess(`Submission ${receipt.submission_id} was queued; current application status could not be refreshed. This is not provider approval.`);
      }
      trigger.current?.focus();
    } catch (caught) {
      const failure = caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN');
      const keepAttempt = failure.code === 'SUBMISSION_IN_PROGRESS' || failure.code === 'IDEMPOTENCY_CONFLICT' ||
        failure.code === 'INVALID_RESPONSE' || failure.status === 0 || failure.status >= 500 || failure.status === 401 || failure.status === 403;
      if (failure.code === 'REVISION_CONFLICT' || failure.code === 'APPLICATION_STATE_CONFLICT') {
        try {
          const latest = await api.getApplication(application.id);
          if (latest.id === application.id && latest.company_id === application.company_id) onUpdated(latest);
        } catch {
          // Keep the original conflict visible; a later application refresh can reconcile state.
        }
      }
      if (!keepAttempt) setSubmissionAttempt(null);
      if (failure.code === 'IDEMPOTENCY_CONFLICT' && submissionAttempt) {
        setSubmissionAttempt({ ...submissionAttempt, retryable: false });
      }
      setUncertain(keepAttempt);
      setError(failure.code === 'IDEMPOTENCY_CONFLICT'
        ? 'This key belongs to a different request. Do not create a new submission; reconcile this application first.'
        : failure.code === 'SUBMISSION_IN_PROGRESS'
        ? 'A submission is already in progress. Retry only with the same request key.'
        : failure.status === 422
        ? 'Readiness validation failed. The application remains editable; review the field errors and try again after saving corrections.'
        : keepAttempt
        ? 'The submission result could not be confirmed. Retry with the same request key or refresh the application status.'
        : 'Submission failed. Refresh the application status before trying again.');
    } finally { inFlight.current = false; setBusy(false); }
  }

  return <section style={{ '--review-raised': color.surfaceSubtle, '--review-surface': color.surface, '--review-border': color.borderStrong, '--review-hover': color.surfaceHover, '--review-muted': color.textSecondary, '--review-focus': color.accent, '--review-text': color.text, display: 'grid', gap: space.md, borderBottom: `1px solid ${color.border}`, paddingBottom: space.lg } as CSSProperties} aria-label="Internal review">
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: space.lg }}>
      <h3 style={{ margin: 0 }}>Internal review</h3>
      <Badge tone={application.status === 'REJECTED' ? 'critical' : ['DRAFT', 'SUBMISSION_PENDING'].includes(application.status) ? 'warning' : 'neutral'}>{applicationStatus(application.status)}</Badge>
      <div style={{ position: 'relative', marginLeft: 'auto' }}>
        <button ref={trigger} className="review-trigger" aria-expanded={open} aria-controls={`${titleId}-menu`} disabled={!editable || busy || uncertain} onClick={() => setOpen(value => !value)}>Review Application <span aria-hidden="true">⌄</span></button>
        {open && <div id={`${titleId}-menu`} ref={menu} className="review-menu" onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }} onKeyDown={event => {
          if (event.key === 'Escape') { setOpen(false); trigger.current?.focus(); }
          const buttons = [...event.currentTarget.querySelectorAll('button')];
          const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
          if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
            event.preventDefault();
            buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus();
          }

        }}>
          {choices.map(action => <button key={action.key} className="review-option" onClick={() => { setChoice(action); setOpen(false); setError(''); }}>
            <ActionIcon action={action} /><span><strong>{action.title}</strong><small>{action.description}</small></span><span aria-hidden="true">›</span>
          </button>)}
        </div>}
      </div>
    </div>
    <p style={{ margin: 0, color: color.textMuted, fontSize: font.size.sm }}>{application.status === 'DRAFT' ? 'Saved with FlowCard; no Submission has been queued.' : ''}Submission queues durable provider processing after readiness passes. Requests for information and rejection are not supported by the current backend.</p>
    {success && <p role="status" style={{ color: color.positive, margin: 0 }}>{success}</p>}
    {submissionReceipt && <p className="sr-only" aria-live="polite">Submission receipt {submissionReceipt.submission_id} for application {submissionReceipt.application_id}.</p>}
    {uncertain && submissionAttempt && <div role="status" style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: space.md }}>
      <p style={{ margin: 0, color: color.critical }}>{error || 'Submission outcome is unresolved.'}</p>
      {submissionAttempt.retryable && <Button disabled={busy} onClick={() => { setOpen(false); setChoice(choices.find(action => action.key === 'submit') ?? null); }}>Retry same submission</Button>}
      <Button disabled={busy} onClick={() => void refreshUncertainSubmissionStatus()}>Refresh application status</Button>
    </div>}
    <dialog ref={dialog} className="review-dialog" aria-labelledby={titleId} aria-describedby={helpId} onCancel={event => { event.preventDefault(); close(); }}>
      {choice && <form onSubmit={event => { event.preventDefault(); void confirm(); }} style={{ display: 'grid', gap: space.xl }} aria-busy={busy}>
        <h3 id={titleId} style={{ margin: 0, fontSize: font.size.xl }}>{choice.heading}</h3>
        <p id={helpId} style={{ margin: 0, color: color.textSecondary }}>{choice.key === 'submit' ? 'This queues the saved application for provider processing after readiness checks pass. Queueing is not provider approval, and funding remains blocked until final approval.' : choice.key === 'reject' ? 'This action would close the application. A rejection reason is required. Internal rejection is not yet supported by the backend.' : 'Specify what the applicant needs to provide. Sending information requests is not yet supported by the backend.'}</p>
        {choice.key !== 'submit' && <label style={{ display: 'grid', gap: space.md }}>{choice.key === 'reject' ? 'Rejection reason' : 'Additional information needed'}
          <textarea className="review-textarea" required disabled={busy} rows={4} value={message} onChange={event => { const value = event.target.value; setMessages(current => ({ ...current, [choice.key]: value })); }} />
        </label>}
        {error && <p role="alert" style={{ color: color.critical, margin: 0 }}>{error}</p>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', flexWrap: 'wrap', gap: space.md }}>
          <Button onClick={close} disabled={busy}>Cancel</Button>
          <button type="submit" className="review-trigger" disabled={busy || Boolean(submissionReceipt) || (uncertain && (!submissionAttempt || !submissionAttempt.retryable)) || (choice.key !== 'submit' && !message.trim())} style={{ background: choice.key === 'reject' ? color.critical : color.accent, color: choice.key === 'reject' ? color.textInverse : color.text, borderRadius: radius.pill }}>{busy ? 'Submitting…' : uncertain && choice.key === 'submit' && submissionAttempt?.retryable ? 'Retry same submission' : choice.confirm}</button>
        </div>
      </form>}
    </dialog>
  </section>;
}
