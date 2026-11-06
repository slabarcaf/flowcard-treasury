---
worth: later
added: 2026-11-06
---

# Connected account updates after activation

Revisit how Company changes should reach an existing Airwallex connected
account after activation. Ramil deferred this flow during the company-onboarding
brainstorm. No decision has been made about synchronization, amendment handling,
supported fields, or the domain model for updates.

[TOC]

## Scope

The intended accounts are US business connected accounts under FlowCard's
Airwallex platform account. Company is the internal legal entity; Platform
Account represents its connected account. Updates to FlowCard's own platform
account and payment-store configuration must be distinguished from updates to
these connected accounts.

## Investigation so far

Public Airwallex documentation was searched first, followed by a recheck through
the Airwallex Docs MCP `search_public_docs` tool on November 6, 2026. No account
was modified and no update behavior was tested against the platform.

- The connected-account update reference describes
  `POST /api/v1/accounts/{id}/update` as collecting information before
  submission for activation. Initial verification uses
  `POST /api/v1/accounts/{id}/submit`.
- The account amendment reference describes
  `POST /api/v1/account/amendments/create` for accounts that passed
  verification.
  Its documented targets are `primary_contact` and
  `account_details.store_details`. Its statuses are `PENDING`, `APPROVED`, and
  `REJECTED`; retrieval uses
  `GET /api/v1/account/amendments/{amendment_id}`. The public Accounts reference
  lists amendment pending, approved, and rejected webhook events.
- This establishes an amendment review mechanism, but its applicability to our
  US business connected-account setup remains unconfirmed. Store details are
  a distinct platform concept and must not be treated as Company business
  details without a verified mapping.
- Neither lookup established a supported post-activation update path for legal
  name, EIN, registered address, or associated people. Their presence in an
  update request schema does not establish that they remain editable after
  activation. Whether a separate review or support process applies is unknown.
- The public submit reference mentions updating primary contact after activation
  for individual accounts. That exception must not be generalized to our
  business connected accounts.
- The unversioned public amendment page failed to load during web research;
  an accessible `2023-10-01` reference was inspected. The MCP later returned an
  amendment schema, but did not establish the applicable version or platform
  entitlement for our setup.

## Open decisions and verification

Confirm the account scope, authentication and on-behalf-of requirements, API
version, platform enablement, and field-by-field behavior with current Airwallex
documentation or Airwallex support. Establish which changes apply immediately,
which require asynchronous review, and which require another process.

Then decide whether to implement post-activation updates, how local Company
revisions relate to platform-approved information, and whether an amendment
needs its own tracked domain operation. Earlier suggestions of automatic or
explicit synchronization were proposals only and were not approved.

## See also

- [Update a connected account](https://www.airwallex.com/docs/api/scale/accounts/update)
- [Submit an account for activation](https://www.airwallex.com/docs/api/scale/accounts/submit)
- [Create an account amendment](https://www.airwallex.com/docs/api/scale/accounts/create_amendments)
- [Versioned account amendment reference inspected during research](https://www.airwallex.com/docs/api/2023-10-01/scale/accounts/create_amendments)
