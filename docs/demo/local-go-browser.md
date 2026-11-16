# Local Go browser demonstration

The local developer entry uses the Go browser gateway. Existing fixture UI
remains available through `npm run dev:legacy`; hosted legacy builds are not
silently switched to unprovisioned Go services.

[TOC]

## Browser commands

```sh
npm run dev
npm run build:go
```

These use `vite.browser-gateway.config.ts`, loopback port 5175 and API proxy
`http://127.0.0.1:8094`. `BROWSER_GATEWAY_DEV_TARGET` may select another
explicit loopback gateway. A listener must already be provisioned; frontend
startup never creates users, financial bindings or backend credentials.

## Local runtime profile

The prepared profile is retained under ignored `.dev/go-browser-demo/` in the
main checkout. Private role keys, database URLs and invited-user passwords
stay in mode-0600 files. Never paste them into source, browser configuration,
logs or chat. The profile uses the existing local PostgreSQL on port 54329 and
new marker-owned logical databases/logins. Existing manual books stay intact.

The stack uses actual Go underwriting, coordinator, Funding API/worker,
browser gateway and onboarding API. Shortfall sources, the agent and provider
are explicitly synthetic DEMO components. Live provider/model execution is
not part of this installation.

Onboarding drafts are local to the new profile. No onboarding provider worker
is started. Public API documentation is served directly by the Go onboarding
listener at <http://127.0.0.1:8080/docs/api/> (HTML and OpenAPI verified 200);
the gateway keeps its explicit business-route allowlist.

Customer/operator accounts are real password/session records,
with immutable bindings verified against the actual DEMO actor registry.

`ONBOARDING_HTTP_HOST=127.0.0.1` binds this onboarding listener to loopback.
Leaving that optional variable absent preserves the existing service default.
Root `.env`, old services, databases and provider callbacks are unchanged.

## Freshness and presentation

Prepare a fresh shared underwriting manifest immediately before a walkthrough,
then use the same IDs/anchor across services. The normal shortfall/evaluation
freshness and UTC-date gates remain enabled. Expired source observations may
hold a new offer/funding request; do not edit timestamps or financial rows to
make a scenario pass. Current provider reads come from the local double.

The installation-local lifecycle helpers are retained under ignored
`.dev/go-browser-demo/`. Run these from the main checkout:

```sh
# Stop only this profile; retain its databases and saved history.
node .dev/go-browser-demo/stop.mjs

# Start a fresh synthetic book, retaining previous books and evidence.
node .dev/go-browser-demo/restart.mjs
```

The restart helper verifies the expected ports become free, then prepares a
new marked profile through the normal service CLIs. It starts a supervised
Agterm runtime. It does not kill unrelated listeners or reset a database.
These helpers are local installation artifacts, not a portable provisioner.
A new checkout requires the service setup described in their own runbooks.

Open <http://127.0.0.1:5175/#bridge-capital>. The active profile is identified by
`.dev/go-browser-demo/current.json`; its `credentials.txt` contains passwords
for `operator.demo`, `customer.demo` and deliberately unlinked `other.demo`.
Credentials change with each fresh book. Financial sources expire after five
minutes; start a new book immediately before an interactive walkthrough.
Saved reads and servicing remain available after source expiry.

## Browser walkthrough

1. Sign in as `operator.demo`. Open the `acct_bloom001` case with the saved
   ELIGIBLE assessment, then create a DEMO offer.
2. Sign out and sign in as `customer.demo`. Read the offer, review the exact
   terms, check the acceptance box and accept.
3. Sign out and return as `operator.demo`. Read the offer UUID, approve it,
   refresh the saved record, then request funding.
4. Read the returned bridge UUID. A RESERVED receipt precedes settlement;
   confirm ACTIVE and a SETTLED funding operation from current saved state.
5. For this synthetic book only, settle the prepared provider deposits using
   the local fixture helper. It changes provider-double facts and sends a
   signed durable webhook. The normal Go worker allocates the collections.

```sh
node .dev/go-browser-demo/settle-deposit.mjs 0
# Refresh the bridge and wait for the first $100 fee collection to settle.
node .dev/go-browser-demo/settle-deposit.mjs 1
# Refresh until REPAID: remaining $90 fee, then $7,000 principal.
```

## Merged-runtime verification

The actual browser journey used source run
`2ca622f0-e396-4ac5-a67c-87f7da29f4e2`, with these saved records:

- Case: `9aaa6bf0-401a-8bf1-adac-bf6882ad11d8`.
- Evaluation: `d6ebd508-f619-4fa7-9cbc-8be33e9797f9`.
- Offer: `16548a5e-e39e-4aa6-ae72-a6344485f249`.
- Bridge: `2f51b333-d904-4191-81c6-ac67c3358e77`.

Distinct password/session users accepted and approved the exact terms through
browser controls. The actual API/worker/HTTP adapter settled $7,000 funding,
then allocated $100 and $7,090 provider-double deposits fee-first. Browser
reads showed REPAID version 28, zero principal/fees, no holds and all four
operations SETTLED. An unlinked customer received HTTP 403 for that bridge.
Applications loaded from the new Go onboarding API and the draft form opened.

The browser exposed a Coordinator contract defect: no actions encoded as
`null`. The Go projection now preserves an empty array; strict frontend
validation remains enabled. The focused Coordinator regression suite passed.

Merged gateway verification passed all 18 top-level tests and 18 HTTP boundary
subtests with race detection, including owned PostgreSQL and financial
journeys. All 110 frontend tests in 12 files passed. Go and legacy frontend
builds passed. A broad root test attempt in the restricted sandbox failed on
loopback listener permissions; the legacy server suite is not claimed passing.

An earlier book retains its immutable POLICY_REVIEW_REQUIRED refusal after the
five-minute source window elapsed. The fresh successful book used new IDs and
normal preparation, without timestamp edits or policy changes. Both remain
historical evidence. Local reports/screenshots are retained under
`.dev/go-browser-demo/` and `/tmp/flowcard-go-browser-*.png`.

This establishes local synthetic execution. Live customer/provider acceptance,
real agent/model calls, card retry and hosted deployment remain separate.
