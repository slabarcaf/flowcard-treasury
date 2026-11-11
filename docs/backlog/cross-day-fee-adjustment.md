---
worth: later
added: 2026-10-21
---

# Cross-day fee adjustment

The current demo fixes the fee at the collection trigger, even if the principal
charge settles on a later day. Before live fee collection relies on settlement-
time accrual, decide whether to accrue through principal settlement and how to
calculate and collect any additional fee after the first fee charge settles.
Define the adjustment's idempotency key as part of that decision.
