# Local connected product UI

The preserved product interface uses the Go browser gateway and the existing
service-owned reads/commands. Its developer interface remains at `#developer`.

[TOC]

## Entries

```sh
npm run dev
npm run dev:product
npm run dev:developer
npm run dev:legacy
```

The normal/product entry uses loopback port 5176 and the explicit Go gateway
proxy target. The developer configuration uses 5175; open `#developer` as an
operator. The legacy entry is an isolated fixture reference and is not a
financial fallback. The currently running original 5173 instance and its data
remain preserved until deliberate port handoff.

The API stack and invited users must already be provisioned through owner
CLIs. Frontend startup does not create accounts, bindings, funding authority
or credentials. See the [read contracts](/docs/api/demo-ui-read-models.md).

## Current installed profile

Main checkout's ignored `.dev/product-go-demo/current.json` selects the local
profile. Its `credentials.txt` contains passwords for `operator.demo`,
`customer.demo` and unlinked `other.demo`, mode 0600. Never publish these files.

The current supervised installation serves actual Go APIs/workers with a
local provider double. An Airwallex sandbox profile requires the separately
verified provider setup and delegation; changing a label or environment
variable is insufficient.

From the main checkout, installed lifecycle helpers are:

```sh
node .dev/product-go-demo/stop.mjs
node .dev/product-go-demo/restart.mjs
```

Stop affects only its verified owned supervisor and retains data. Restart
creates a new marked book with new IDs, timestamps, users and private keys;
previous books and evidence remain. These helpers are installation-local,
not a portable production provisioner. Do not resume or edit old financial
books to get past a refusal.

Source facts expire after five minutes. Prepare a new book immediately before
new funding, with binaries/browser steps already ready. Historical reads and
servicing continue after source expiry with explicit freshness metadata.

## Walkthrough

1. Open `#bridge-capital` and sign in as operator. Select the saved eligible
   customer case, then create a bridge offer.
2. Sign out and sign in as the linked customer. Review the saved offer and
   explicitly accept its exact amount, fee, date and disclosures.
3. Return as operator, review the saved offer, approve exact terms and request
   funding. A queue receipt precedes actual settlement.
4. The customer discovers the bridge under Home or Bridges after reload.
   Observe operations and debt from the owner, not a browser-calculated loan.
5. For the local double only, normal prepared provider deposit controls and
   signed wakes exercise repayment:

```sh
node .dev/product-go-demo/settle-deposit.mjs 0
# Wait for the first settled fee collection.
node .dev/product-go-demo/settle-deposit.mjs 1
```

The Go worker allocates actual recorded collections fee-first. Check REPAID,
zero debt, operation certainty and matching history versions. Provider cash
is a separate scoped read and never replaces credit limits or facility capital.

See [verification and limits](/docs/demo/product-go-ui-verification.md).
Autonomous decisions and current Airwallex progress are retained under the
implementation worktree's ignored `.dev/implementation/` directory.
