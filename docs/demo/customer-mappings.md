# Customer mapping administration

The retained Node roster CLI owns enrollment and customer/account/card
mapping changes. Go services validate those shared mappings; importing an
underwriting snapshot does not enroll a customer.

[TOC]

## Setup and authority

Apply the existing Node migrations through the normal migration tooling.
Configure the CLI's server-only environment in the main checkout or an
authorized process; do not copy root `.env` into a worktree. Restrict database
and CLI access to authorized operators.

Mappings use stable IDs, never company names or email matches. An actor and
reason are required for writes. This CLI is privileged administration, not a
browser customer-ownership API.

## Commands

The read-only roster listing is:

```sh
npm run roster:cli -- list
```

For an explicitly authorized enrollment, replace every placeholder with a
verified identity:

```sh
npm run roster:cli -- enroll \
  --customer-id VERIFIED_CUSTOMER_ID \
  --salesforce-account-id VERIFIED_CRM_REFERENCE \
  --airwallex-account-id VERIFIED_CONNECTED_ACCOUNT_ID \
  --actor AUTHORIZED_OPERATOR \
  --reason "Verified enrollment"
```

The stored Salesforce reference is legacy roster metadata. Enrollment does
not contact Salesforce, run a snapshot sync or activate provider capabilities.

Other supported commands are `map-account`, `bind-card`, `unbind-card` and
`deactivate`. Their required flags are defined in
`server/src/roster/commands.ts`. Mapping changes and deactivation preserve
audit records and do not erase settled financial history.

## Reconciliation and remaining cutover

The read-only provider account reconciliation implementation remains in
`server/src/reconciliation/`. It has no scheduled entrypoint or dedicated
operator CLI; do not assume a running reconciliation service.

Mapping administration must move to a trusted Go boundary before this Node
tooling is removed. Customer login-to-financial-account bindings are a
separate requirement, not something inferred from a roster listing.
