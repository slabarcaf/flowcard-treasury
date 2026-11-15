# Airwallex sandbox prerequisites

The local Go demonstration uses synthetic sources, fixture agents and a local
provider double. Its successful money/ledger journey does not establish live
Airwallex acceptance. No live account, payment or transfer is authorized by
this document.

[TOC]

## Current service boundaries

- [Shortfall detection](/docs/demo/shortfall-detection-local.md): live reference
  extraction and the actual subscription/event version remain unverified.
- [Underwriting](/docs/demo/underwriting-local.md): normalized snapshot imports
  and deterministic decisions; imported claims need trusted source verification.
- [Funding and collection](/docs/demo/funding-and-collection-local.md): exact
  offers, acceptance, approval, reservations, provider operations and ledger.
  Actual provider authorization/settlement contracts remain separate gates.
- [Coordinator](/docs/demo/go-coordinator-local.md): bounded investigation and
  servicing monitoring. A pending evidence receipt does not verify its facts.
- Company onboarding retains Node signature verification and durable account/RFI
  forwarding until direct Go intake and callback cutover are verified.

## Before separately authorized sandbox execution

1. Pin the exact service, operation, environment, affected accounts and expected
   result. Routine tests remain fixture-based.
2. Verify connected-account/card mappings through trusted administration,
   product enablement, API scopes and account-scoped authorization. Funding
   direction and collection debit mandates must be valid for those accounts.
3. Verify the provider request, response and webhook contract actually supported
   by the account. Configure the corresponding signing secret server-side.
4. Check durable request identities, uncertain-result recovery, callback routing
   and access to provider lookup/settlement evidence before attempting a write.
5. For onboarding, resolve supported US roles/documents, RFI enablement, storage
   and agreement requirements. Do not fabricate customer acceptance metadata.

Keep credentials and provider identifiers server-side. Do not copy the ignored
root `.env` into a worktree, tests, browser code or tracked artifacts.

## Historical instructions

The previous Kit 5 presenter/checkpoint guide is preserved
[in the Node archive](/docs/archive/node-legacy/airwallex-sandbox-demo-prerequisites.md).
Its financial/checkpoint endpoints are retired and return 410. Those examples
are historical, including the evidence-review CLI and repayment settings.

See [customer mappings](/docs/demo/customer-mappings.md),
[Node retirement](/docs/demo/node-funding-retirement.md) and
[hosted acceptance](/docs/infra/demo-acceptance.md) for the remaining boundaries.
