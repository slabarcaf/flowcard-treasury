---
goal: Build confidence-gated AI evidence extraction from Salesforce snapshots
version: 1.0
date_created: 2026-10-22
last_updated: 2026-10-22
owner: Ramil
status: Planned
tags: [feature, ai-evidence, salesforce, signal-extraction]
---

# 20261022 AI evidence pipeline implementation plan

![Status: Planned](https://img.shields.io/badge/status-Planned-blue)

[TOC]

## Overview

Build a server-side AI evidence pipeline that reads each customer's completed
daily Salesforce snapshot and produces typed, quote-backed signals. The model
may select the strongest claim and report a calibrated confidence score. The
deterministic policy remains responsible for ratings, lines, eligibility, and
funding decisions; the model has no policy or transaction authority.

This plan does not implement AI allocation proposals or change underwriting
formulas and thresholds. It maps approved evidence into the existing
deterministic signal input shape and supplies a separate readiness gate.

The demo replays synthetic fixtures. Production calls an approved live model
provider through a provider-neutral adapter. Each signal type starts in review
mode and can graduate to automatic use independently after it meets the
per-type precision gate.

## Context from discovery

- The repo is a React, TypeScript, and Vite frontend. `src/services/` contains
  mock CRM and agent clients, and `src/components/SignalExtraction.tsx`
  presents the demo extraction flow.
- The [Airwallex bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
  establishes the server-side TypeScript and PostgreSQL foundation.
- The [Salesforce snapshot plan](/docs/plans/20261021-salesforce-snapshot-implementation-plan.md)
  owns the internal customer roster and atomic daily Salesforce snapshots. This
  plan consumes its published snapshot contract and does not add another CRM
  sync path.
- The authoritative behavior is described in the [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md)
  and the [requirements gap analysis](/docs/requirements/Airwallex%20Hackathon_req_2.md).
- The [Typesafe.ai signal-definition backlog item](/docs/backlog/work-with-typesafe-jev-on-signal-definitions.md)
  records the future collaboration with Jev. Its result gates production
  automation for the affected signal types.
- The root package provides `npm test`, `npm run build`, and `npm run lint`.
  Server tests use Vitest and the server structure established by the upstream
  plans.

## Preconditions and external gates

- Complete the server and database foundation in the Airwallex bridge ledger
  plan and the canonical Salesforce snapshot work in the Salesforce snapshot
  plan before integrating the daily evidence runner.
- Use the current proposed signal mapping to build fixtures and the closed
  catalog interface. Before production auto-application, finalize the catalog,
  meanings, and representative labeled examples through the Typesafe.ai Jev
  backlog item.
- Keep the live model adapter provider-neutral until a production endpoint is
  approved for Salesforce text and its data-handling terms are verified.
  Production credentials belong in server configuration, never in the browser.
- Validate live Salesforce field mappings through the existing MCP before
  relying on those fields in production evidence packets.

## Requirements and decisions

- **REQ-001 — Snapshot input:** Run after a complete, fresh, atomic customer
  snapshot has been published. Read the canonical snapshot from the internal
  database. The model does not call Salesforce MCP or any other tools.
- **REQ-002 — Minimal evidence packet:** Send only allowlisted fields relevant
  to signals: linked risk-relevant Cases and up to five recent call Activities
  from the prior 90 days, including controlled categories, dates, and bounded
  narrative text. Exclude email threads and attachments.
- **REQ-003 — Per-customer request:** Make one model call for each customer's
  deduplicated evidence packet. Use a deterministic evidence order. If the
  packet exceeds its configured size limit, do not truncate it; mark that
  customer's extraction failed and block new bridge decisions for that
  customer. Continue processing other customers.
- **REQ-004 — Exact duplicate handling:** Build a deterministic canonical
  serialization from customer ID, source type, and every model-visible field.
  Exclude Salesforce record IDs and snapshot-ingestion timestamps from the
  fingerprint. Use SHA-256 to find candidates, then compare canonical values
  exactly before collapsing. Keep every source ID as provenance. Do not use
  fuzzy or semantic duplicate matching.
- **REQ-005 — Closed signal catalog:** Emit only approved signal types. Return
  relevant evidence outside the catalog as `unmapped` for review. New signal
  types require a versioned catalog update and their own qualification.
- **REQ-006 — Multiple signals:** A source may support multiple distinct
  signals. Each signal has its own type, quote set, source references, and
  confidence score. Competing claims for the same signal identity are
  evaluated together; distinct counterparties remain separate signals.
- **REQ-007 — Attribution:** The linked Account fixes the customer identity.
  The model may name a counterparty only when the text states it explicitly.
  Cross-record joins require the same explicit counterparty after deterministic
  normalization of case, punctuation, and whitespace. Do not infer aliases.
  Include exact evidence from every record used in a joined signal.
- **REQ-008 — Conflicts and supersession:** Keep conflicting claims active
  until a human clears one. The model selects the strongest supported claim
  for the current signal and retains alternatives for audit. A high-confidence
  selection may pass its gate while both claims remain recorded. Recency,
  Case closure, and model judgment do not clear an existing claim.
- **REQ-009 — Evidence provenance:** Every text-derived signal must include
  an exact quote and source reference. The backend verifies each quote against
  the referenced source text. A mismatch receives bounded retries; exhausted
  retries produce a failed result.
- **REQ-010 — Untrusted source text:** Salesforce text is evidence, never
  instructions. The model has no tools. It may extract a supported fact from a
  note that also contains instruction-like text, subject to the same validation
  and confidence gates.
- **REQ-011 — Result states:** A successful extraction with no qualifying
  signal is `no_signal`. Unmapped evidence and uncertain evidence are distinct
  review states. Model errors, invalid output after bounded retries, and
  oversized packets are `failed`. Never treat failure as `no_signal` or reuse
  a stale result as a successful extraction.
- **REQ-012 — Customer decision gate:** `review_required`, `unmapped`, and
  `failed` block new bridge decisions for that customer until resolved. A
  successful `no_signal` result does not block. Other customers continue.
- **REQ-013 — Policy boundary:** The model never sets ratings, tiers, credit
  lines, eligibility, bridge amounts, or transfer commands. Only deterministic
  policy consumes approved signals. Positive evidence cannot raise a line
  without human approval.
- **REQ-014 — Human review:** One reviewer records `confirmed`, `corrected`,
  or `cleared`, with the corrected signal when needed. Preserve the original
  model result and reviewer identity, time, and source references. A correction
  takes effect on the next daily run, then the model reprocesses the evidence.
  If the same corrected-away result recurs against unchanged evidence, send it
  to review again.
- **REQ-015 — Confidence score:** Return one numeric score per signal, meaning
  the probability that the entire selected result is correct: signal type,
  value, customer and counterparty attribution, source quotes, and selection
  among competing claims. Calibrate scores separately by signal type from
  human-labeled examples.
- **REQ-016 — Qualification and monitoring:** Initially run live outputs in
  review-only mode. Promote each signal type independently only when its
  one-sided exact 95% confidence lower bound for precision is at least 95%.
  Use random audits per type, audit all auto-applied results when a type is
  below its configured quota, and evaluate only labels from the rolling 90-day
  window. The minimum sample depends on observed errors; 59 audited results
  with zero errors are the minimum that can clear the bound.
- **REQ-017 — Version changes:** Version the model identifier, prompt,
  catalog, output schema, packet builder, and calibration mapping. A changed
  configuration starts in review-only mode; requalify types independently.
  Keep the previous qualified configuration active when the provider can pin
  and continue serving it.

## Development approach

Use the existing TypeScript server and PostgreSQL patterns established by the
upstream plans. Keep model access behind a server-only provider interface and
use fixtures for deterministic demo and test runs. Implement tasks sequentially;
each code task adds or updates focused unit or database tests before moving on.
No live Salesforce or model calls belong in automated tests.

## Solution overview

The daily runner loads one completed customer snapshot and constructs a bounded
evidence packet. It canonicalizes and collapses exact duplicate records while
preserving provenance. A server-side extractor calls either the fixture
provider or an approved live provider, then validates the closed schema, source
references, exact quotes, attribution, and calibrated confidence.

Validated signal claims and their alternatives are stored with snapshot and
configuration versions. A reviewer CLI records decisions and corrections.
The daily policy integration consumes only qualified signals. A customer with
unresolved review, unmapped evidence, failed extraction, or an oversized packet
is blocked from new bridge decisions until the issue is resolved.

## Technical details

### Evidence packet

The packet contains the internal customer ID, snapshot ID, source type,
Salesforce source ID, source date, controlled categories, bounded text, and
only the other allowlisted fields approved by the snapshot schema. A packet
entry may group exact duplicate records and carries all original source IDs.
Names are normalized only for case, whitespace, and punctuation when checking
explicit counterparty equality. Legal suffixes and other name tokens remain.

### Signal and review records

Persist each result with the customer and snapshot IDs, signal identity,
counterparty when explicit, values, exact quote spans, all source references,
raw model score, calibrated score, configuration version, and selection
rationale. Retain non-selected and conflicting claims. A human clear changes
the active set on the next daily run but preserves the cleared claim and review
history.

### Calibration and audit

Use review-only warm-up labels to fit a per-type calibrator. Keep calibration
examples separate from qualification examples. Use an exact one-sided binomial
lower bound on independent qualification and audit labels. If the 90-day sample
is too small or the bound falls below 95%, the type returns to review-only
mode. Select and version the calibration mapping using held-out data; never fit
and certify on the same examples.

### Operator review

V1 uses an operator-only server CLI for confirmed, corrected, and cleared
review actions, matching the roster CLI pattern. The browser displays
fixture-backed evidence and signal output in the existing extraction panel.
Do not expose production review or model credentials through the unauthenticated
demo frontend. A protected production review UI requires a separate auth scope.

## Testing strategy

- Unit-test canonical serialization, duplicate detection, name normalization,
  packet limits, quote validation, schema parsing, and status transitions.
- Test model behavior through fake and fixture providers, including instruction
  text, multiple signals, explicit cross-record joins, conflicting claims,
  malformed output, retries, and provider failure.
- Test persistence and review actions against the PostgreSQL test database,
  including next-daily-run corrections and repeat-error routing.
- Test confidence calculations with zero and nonzero errors, per-type
  qualification, 90-day label expiration, insufficient samples, promotion,
  demotion, and requalification after config changes.
- Test that `review_required`, `unmapped`, and `failed` block only the affected
  customer, while valid `no_signal` and other customers continue.
- Run the repository commands `npm test`, `npm run build`, and `npm run lint`
  during implementation. Do not contact the live Salesforce MCP or model in
  tests.

## What goes where

- **Implementation Steps:** Server contracts, packet construction, exact
  deduplication, provider adapter, extraction, persistence, review, calibration,
  daily integration, demo display, and tests.
- **External gates:** Approve a production model endpoint and its data-handling
  terms; finalize the signal catalog with Jev; provide server credentials and
  confirm live Salesforce field mappings. These gates do not block fixture
  development.

## Implementation steps

### Task 1: Add versioned evidence and signal contracts

**Files:**

- Create: `server/src/ai-evidence/types.ts`
- Create: `server/src/ai-evidence/catalog.ts`
- Create: `server/src/ai-evidence/schema.ts`
- Create: `tests/server/ai-evidence/schema.test.ts`

- [x] Define versioned extraction statuses and per-signal result types.
- [x] Define closed catalog loading and the initial proposed signal mapping.
- [x] Define evidence references, quote spans, counterparty identity, raw score,
  calibrated score, alternatives, and model configuration metadata.
- [x] Reject unknown signal types into the `unmapped` review path.
- [x] Test valid, empty, unknown-type, and invalid result shapes.
- [x] Run focused tests before Task 2.

### Task 2: Build and deduplicate bounded customer packets

**Files:**

- Create: `server/src/ai-evidence/packet.ts`
- Create: `server/src/ai-evidence/canonicalize.ts`
- Create: `server/src/ai-evidence/dedupe.ts`
- Create: `tests/server/ai-evidence/packet.test.ts`

- [x] Build one packet from a completed, fresh, canonical customer snapshot.
- [x] Include only snapshot-approved Case and call-Activity fields and bounded
  text; exclude attachments and email threads.
- [x] Canonicalize every model-visible field with stable key and value order.
- [x] Use SHA-256 to index same-customer, same-source-type duplicate candidates;
  verify canonical equality before collapsing and preserve every source ID.
- [x] Apply the provider-configured size cap without truncation. Mark an
  oversized packet failed for that customer.
- [x] Test exact duplicates, differing dates/categories, cross-customer records,
  provenance retention, deterministic ordering, and oversized packets.
- [x] Run focused tests before Task 3.

### Task 3: Add fixture and production model provider interfaces

**Files:**

- Create: `server/src/ai-evidence/provider.ts`
- Create: `server/src/ai-evidence/fixture-provider.ts`
- Create: `server/src/ai-evidence/prompt.ts`
- Create: `server/src/ai-evidence/config.ts`
- Create: `tests/server/ai-evidence/provider.test.ts`

- [x] Define a provider-neutral server interface with explicit model and
  configuration version metadata.
- [x] Add deterministic synthetic fixtures for direct, ambiguous, conflicting,
  irrelevant, duplicate, and superseded evidence cases.
- [x] Define the live-provider adapter boundary; enable it only when the
  provider and data-handling terms are approved.
- [x] Keep all model credentials server-side and deny tool access to the model.
- [x] Delimit Salesforce text as untrusted evidence and instruct the model to
  ignore instructions embedded in it.
- [x] Test fixture selection, production configuration gating, and prompt
  construction without a live provider call.
- [x] Run focused tests before Task 4.

### Task 4: Implement extraction, attribution, and output validation

**Files:**

- Create: `server/src/ai-evidence/extract.ts`
- Create: `server/src/ai-evidence/validate.ts`
- Create: `server/src/ai-evidence/normalize-counterparty.ts`
- Create: `tests/server/ai-evidence/extract.test.ts`

- [x] Make one bounded extraction call per customer packet; allow multiple
  distinct signals from a source and separate signals for distinct
  counterparties.
- [x] Bind customer identity from the snapshot Account link. Require explicit
  counterparty text and match cross-record parties only after deterministic
  case, whitespace, and punctuation normalization.
- [x] Let the model select the strongest claim for a signal identity while
  retaining competing claims and their evidence references.
- [x] Require exact quotes for every text-derived signal and verify each quote
  against the referenced source text.
- [x] Validate signal type, customer, source IDs, quote spans, values, raw
  score, and cross-record joins. Retry invalid output within the bounded retry
  policy.
- [x] Emit `no_signal` only after successful extraction; emit `review_required`,
  `unmapped`, or `failed` distinctly.
- [x] Test prompt-injection text, unsupported claims, mismatched customer IDs,
  quote mismatch, invalid aliases, conflicts, multi-signal records, and retries.
- [x] Run focused tests before Task 5.

### Task 5: Persist evidence claims and review history

**Files:**

- Create: `server/src/db/migrations/004_ai_evidence.sql`
- Create: `server/src/ai-evidence/repository.ts`
- Create: `server/src/ai-evidence/review.ts`
- Create: `tests/server/ai-evidence/repository.test.ts`
- Create: `tests/server/ai-evidence/review.test.ts`

- [x] Store immutable extraction outputs, active and non-selected claims,
  evidence references, snapshot IDs, and configuration versions.
- [x] Store reviewer ID, timestamp, action, corrected signal, and clear reason
  while preserving the original model output.
- [x] Apply corrections and clears at the next daily run; do not rewrite the
  source snapshot or silently replace extraction history.
- [x] Retain a marker for corrected-away output on unchanged evidence so a
  repeated result routes to review again.
- [x] Test migrations, persistence, reviewer actions, next-run application,
  retained history, and repeat-error detection.
- [x] Run focused database tests before Task 6. PostgreSQL runtime verification
  unavailable in this environment (no `psql`/`postgres`; Docker daemon socket
  denied); focused fake-pool transaction tests and migration assertions pass.

### Task 6: Add operator review actions

**Files:**

- Create: `server/src/ai-evidence/review-cli.ts`
- Modify: `server/src/main.ts`
- Create: `tests/server/ai-evidence/review-cli.test.ts`

- [x] Add operator-only list, inspect, confirm, correct, and clear commands.
- [x] Require an explicit reviewer identity and structured disposition for each
  reviewed customer.
- [x] Keep unresolved review and unmapped results visible until disposition.
- [x] Ensure review actions do not call policy, Airwallex, or Salesforce write
  operations.
- [x] Test each disposition, permission/config failure, and audit record.
- [x] Run focused tests before Task 7.

### Task 7: Implement per-type confidence calibration and qualification

**Files:**

- Create: `server/src/ai-evidence/calibration.ts`
- Create: `server/src/ai-evidence/qualification.ts`
- Create: `server/src/ai-evidence/audit-sampler.ts`
- Create: `tests/server/ai-evidence/calibration.test.ts`
- Create: `tests/server/ai-evidence/qualification.test.ts`

- [x] Fit a versioned calibration mapping separately for each signal type using
  human-labeled warm-up examples.
- [x] Keep calibration-fit examples separate from held-out qualification
  examples and production audit outcomes.
- [x] Compute per-type precision with the exact one-sided 95% binomial lower
  confidence bound; qualify only when the bound is at least 95%.
- [x] Use a rolling 90-day label window and a configured per-type audit quota;
  audit all auto-applied results when a type is below quota.
- [x] Require the sample count implied by observed errors; 59 correct out of 59
  is the minimum zero-error case.
- [x] Start in review-only mode. Promote each type independently; demote a type
  to review-only when the bound fails or the recent sample is insufficient.
- [x] Test per-type separation, fit/evaluation separation, confidence bounds,
  error-dependent sample needs, 90-day expiry, quotas, promotion, and demotion.
- [x] Run focused tests before Task 8.

### Task 8: Integrate the daily runner and customer decision gate

**Files:**

- Create: `server/src/ai-evidence/runner.ts`
- Modify: `server/src/snapshots/sync-worker.ts`
- Create: `server/src/ai-evidence/decision-gate.ts`
- Create: `server/src/ai-evidence/policy-input.ts`
- Modify: `server/src/main.ts`
- Create: `tests/server/ai-evidence/runner.test.ts`
- Create: `tests/server/ai-evidence/decision-gate.test.ts`
- Create: `tests/server/ai-evidence/policy-input.test.ts`

- [ ] Run extraction only after each customer's daily snapshot is complete and
  fresh; never refresh Salesforce directly from the model path.
- [ ] Continue the batch for other customers when one packet or model call
  fails.
- [ ] Block new bridge decisions for a customer with `review_required`,
  `unmapped`, `failed`, oversized input, or an unqualified signal type until
  review or qualification resolves it.
- [ ] Allow a valid `no_signal` result to proceed with existing deterministic
  profile data.
- [ ] Pass only qualified signals into deterministic policy; do not modify
  policy calculations, tiers, lines, amounts, or transfer actions.
- [ ] Map only source-approved signal meanings into the existing `Signals`
  input type; send every other interpretation to review.
- [ ] Test customer-level isolation, snapshot freshness, all result-state gates,
  and continued processing of other customers.
- [ ] Run focused tests before Task 9.

### Task 9: Wire deterministic demo fixtures to the extraction panel

**Files:**

- Modify: `src/services/types.ts`
- Modify: `src/services/mock/AgentClient.ts`
- Modify: `src/components/SignalExtraction.tsx`
- Modify: `src/seed.ts`
- Create: `tests/ai-evidence/agent-client.test.ts`

- [ ] Replay synthetic extraction fixtures in demo mode; do not call a live
  model from the browser.
- [ ] Keep `AgentClient.proposeAllocation` and model allocation behavior out of
  this plan.
- [ ] Display signal type, exact quote, source reference, counterparty,
  confidence status, alternatives, and review/blocked state.
- [ ] Include deterministic examples for `no_signal`, `unmapped`, conflicts,
  multi-signal output, correction, and failure.
- [ ] Test fixture selection and display data mapping.
- [ ] Run focused tests and `npm run build` before Task 10.

### Task 10: Verify acceptance criteria

- [ ] Verify the daily snapshot is the only source of production evidence.
- [ ] Verify every automatic signal has an approved type, validated quote,
  source provenance, explicit attribution, and qualified per-type score.
- [ ] Verify exact duplicates collapse without losing source IDs and fuzzy
  duplicates remain separate.
- [ ] Verify conflicting claims remain active until a reviewer clears one.
- [ ] Verify no model path can set policy or call a transaction service.
- [ ] Verify `review_required`, `unmapped`, and `failed` block only the affected
  customer; `no_signal` is a successful result.
- [ ] Verify per-type promotion and demotion using the rolling 90-day bound.
- [ ] Run `npm test`, `npm run build`, and `npm run lint`.

### Task 11: Update documentation and plan status

- [ ] Document the signal result schema, review states, calibration gate, and
  model configuration version key.
- [ ] Document fixture mode, the approved production provider gate, and the
  required server configuration.
- [ ] Update the README with the server-side evidence flow and the operator
  review CLI.
- [ ] Update this plan with actual files, test commands, and any justified
  scope changes.
- [ ] Move the completed plan to `docs/plans/completed/` after implementation.

## Post-completion gates

- Resolve the Typesafe.ai Jev signal-definition backlog item before enabling
  production auto-application for the final catalog.
- Approve the production model provider and verify its data-handling terms
  before sending Salesforce text to it.
- Confirm final Salesforce fields and values through the existing MCP.
- Add a protected reviewer UI only after an authentication design exists; V1
  review actions remain operator-only through the server CLI.
