import { useEffect, useId, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import type { ApplicationRecord } from '../../../../services/onboarding/contracts.js';
import { applicationStatus } from '../../../../services/onboarding/contracts.js';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import { color, font, radius, space } from '../tokens.js';
import { Badge, Button } from './primitives.js';
import './application-review.css';

const choices = [
  { key: 'submit', title: 'Submit to Airwallex', description: 'Forward the application for review', heading: 'Submit this application to Airwallex?', confirm: 'Confirm Submission', accent: color.positive },
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
  const inFlight = useRef(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const helpId = useId();
  const editable = application.status === 'DRAFT';
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
    dialog.current?.close(); setChoice(null); setError(''); trigger.current?.focus();
  }
  async function confirm() {
    if (!choice || inFlight.current || !editable || uncertain || (choice.key !== 'submit' && !message.trim())) return;
    if (choice.key !== 'submit') {
      setError(choice.key === 'request' ? 'Not sent. The backend does not yet support saving and delivering information requests. Your message is preserved here.' : 'Not rejected. The backend does not yet support internal rejection with a reason. Your reason is preserved here.');
      return;
    }
    inFlight.current = true; setBusy(true); setError(''); setSuccess('');
    try {
      const updated = await api.submitApplication(application);
      onUpdated(updated);
      dialog.current?.close(); setChoice(null);
      setSuccess('Application status refreshed from the backend.');
      trigger.current?.focus();
    } catch (caught) {
      const unknown = !(caught instanceof OnboardingApiError) || caught.status === 0 || caught.status >= 500;
      setUncertain(unknown);
      setError(caught instanceof OnboardingApiError && caught.code === 'SUBMISSION_CONTRACT_BLOCKED'
        ? 'Not submitted. The backend has explicitly blocked Airwallex submission pending its submission contract implementation.'
        : unknown ? 'The result could not be confirmed. Reopen the application to check its saved status before retrying.'
        : 'Submission failed. Check your session and the latest application status before trying again.');
    } finally { inFlight.current = false; setBusy(false); }
  }

  return <section style={{ '--review-raised': color.surfaceSubtle, '--review-surface': color.surface, '--review-border': color.borderStrong, '--review-hover': color.surfaceHover, '--review-muted': color.textSecondary, '--review-focus': color.accent, '--review-text': color.text, display: 'grid', gap: space.md, borderBottom: `1px solid ${color.border}`, paddingBottom: space.lg } as CSSProperties} aria-label="Internal review">
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: space.lg }}>
      <h3 style={{ margin: 0 }}>Internal review</h3>
      <Badge tone={editable ? 'warning' : application.status === 'REJECTED' ? 'critical' : 'neutral'}>{editable ? 'Pending Review' : applicationStatus(application.status)}</Badge>
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
    <p style={{ margin: 0, color: color.textMuted, fontSize: font.size.sm }}>{editable ? 'Saved with FlowCard; not yet submitted to Airwallex. ' : ''}Review integrations are not yet available. No action changes status unless the backend confirms it.</p>
    {success && <p role="status" style={{ color: color.positive, margin: 0 }}>{success}</p>}
    <dialog ref={dialog} className="review-dialog" aria-labelledby={titleId} aria-describedby={helpId} onCancel={event => { event.preventDefault(); close(); }}>
      {choice && <form onSubmit={event => { event.preventDefault(); void confirm(); }} style={{ display: 'grid', gap: space.xl }} aria-busy={busy}>
        <h3 id={titleId} style={{ margin: 0, fontSize: font.size.xl }}>{choice.heading}</h3>
        <p id={helpId} style={{ margin: 0, color: color.textSecondary }}>{choice.key === 'submit' ? 'This forwards the application for KYB review. Funding remains blocked until approval. The current backend blocks submission.' : choice.key === 'reject' ? 'This action would close the application. A rejection reason is required. Internal rejection is not yet supported by the backend.' : 'Specify what the applicant needs to provide. Sending information requests is not yet supported by the backend.'}</p>
        {choice.key !== 'submit' && <label style={{ display: 'grid', gap: space.md }}>{choice.key === 'reject' ? 'Rejection reason' : 'Additional information needed'}
          <textarea className="review-textarea" required disabled={busy} rows={4} value={message} onChange={event => { const value = event.target.value; setMessages(current => ({ ...current, [choice.key]: value })); }} />
        </label>}
        {error && <p role="alert" style={{ color: color.critical, margin: 0 }}>{error}</p>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', flexWrap: 'wrap', gap: space.md }}>
          <Button onClick={close} disabled={busy}>Cancel</Button>
          <button type="submit" className="review-trigger" disabled={busy || uncertain || (choice.key !== 'submit' && !message.trim())} style={{ background: choice.key === 'reject' ? color.critical : color.accent, color: choice.key === 'reject' ? color.textInverse : color.text, borderRadius: radius.pill }}>{busy ? 'Submitting…' : choice.confirm}</button>
        </div>
      </form>}
    </dialog>
  </section>;
}
