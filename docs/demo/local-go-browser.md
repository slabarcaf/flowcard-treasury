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
is started. Customer/operator accounts are real password/session records,
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

Exact local process/config/refresh commands and the verified walkthrough are
recorded in the profile's private setup report after installation. A local
fixture success establishes no live customer consent or Airwallex acceptance.
