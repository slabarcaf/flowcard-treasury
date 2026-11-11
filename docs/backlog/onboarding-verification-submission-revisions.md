---
worth: later
added: 2026-11-06
---

# Handle changes between onboarding verification and submission

Decide how submission behaves when Company or Onboarding Application changes
after an operator verifies its saved data. Ramil deferred this decision during
the onboarding API brainstorm. No approach has been selected.

Options discussed: require the reviewed Company and application revisions on
the submit command and return `409 REVISION_CONFLICT` when either changes, or
validate and submit the latest saved data. The first requires another operator
review; the second may submit details the operator has not reviewed.

The revision fields previously shown in the example submit request remain
provisional. Revisit the client review experience and concurrent-edit behavior
before finalizing that request contract. Submission must capture a consistent
immutable set of company and onboarding details regardless of the chosen policy.

Keep this separate from the agreed idempotency mechanism: retries using the same
key and request must not create another Submission. Define what constitutes the
same request when finalizing the revision policy.
