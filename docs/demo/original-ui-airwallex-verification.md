# Original UI and Airwallex sandbox verification

Verified November 16, 2026 using the original operator and customer product
interfaces, actual Go services and actual Airwallex sandbox APIs. The sandbox
integration is merged into local develop. Nothing was pushed or deployed.

## Demonstrated lifecycle

1. A genuine insufficient-funds authorization and original signed provider
   delivery reached the Go shortfall service. Canonical provider reads resolved
   the payment identity and a 700000-cent shortfall.
2. Normal underwriting imports and the actual API persisted the assessment.
   Customer history and receivables were explicitly synthetic sandbox inputs;
   policy thresholds and financial authority remained server-owned.
3. The original operator UI created the exact offer. A distinct authenticated
   customer accepted the 700000-cent principal, 19000-cent fee and 30-day term.
   The operator approved those terms and requested funding once.
4. The real funding worker and HTTP adapter settled the actual sandbox transfer
   from the funding connected account to the borrower connected account.
5. One external 719000-cent simulated Global Account deposit reached the
   signed Go intake. Normal collection settled the 19000-cent fee first and
   then the 700000-cent principal back to the funding connected account.
6. The original operator and customer screens showed REPAID, zero debt and no
   holds. Coordinator closed the linked collection case at bridge version 25.

No automatic card retry was attempted. The actual provider transfer objects,
test balances, original requests, failures and immutable observations remain
retained in the isolated sandbox installation.

## Timestamp-only hold and recovery

After fee collection, the same deposit's canonical `settled_at` changed from
08:22:43Z to 08:22:44Z. Retained raw bytes prove the timestamp was the only
changed response field, and normalization was correct. The original exact
timestamp comparison placed principal collection on hold.

Ramil approved ignoring timestamp equality when identity and economic facts
still match. The fix checks deposit/account/customer IDs, currency, gross and
net amounts, fees, settled status and external-deposit kind. Valid timestamps,
fresh observations and post-funding eligibility remain required. The original
deposit timestamp and all provider observations remain unchanged.

The [privileged recheck procedure](/docs/demo/deposit-timestamp-rechecks.md)
recorded one append-only recovery receipt and released this verified hold.
It sent no financial request or replacement deposit. The normal worker then
performed the principal transfer under its existing checks.

## Independent verification

- Account-scoped actual provider GETs matched all three saved transfer IDs,
  request IDs, destinations, exact USD amounts, observed fees and settled
  status.
- The canonical sandbox journey verifier corroborated the original declined
  payment, external deposit, distinct acceptance/approval actors, released
  reservations, zero debt and balanced ledger.
- An independent 17-assertion API/database audit passed before and after
  restart,
  including source/evaluation/offer/bridge linkage and Coordinator closure.
- A real stack restart preserved the same three operation identities, saved
  assessment, evidence, bridge version and actual provider wallet balances.
- An authenticated browser replay of the original funding request reused its
  exact saved key and payload. It returned the identical bridge and operation
  receipt, with three total transfers and no extra wallet movement.
- A privileged READ ONLY audit confirmed one recovery receipt, the preserved
  original timestamp/observation/body hash and the same zero-debt bridge.
- Customer access to operator data returned 403; a supplied customer selector
  returned 400. The mobile customer view had viewport and document width 390.

## Tests and source

The timestamp fix is commit `edc383a`, merged locally at `e21531f`. The full
funding service suite passed 58 top-level tests with no skipped test cases.
Four focused scenarios passed on merged develop. The new collection regression
fails against the old comparison via a Go source overlay and passes after the
fix. Tests include economic-conflict rejection, unrelated-hold preservation,
runtime privilege denial, recovery replay and refusal to reuse an old receipt
to clear a later hold. Build, vet, gofmt and diff checks passed.

The final frontend suite passed 374 tests in 35 files. Three legacy Node suites
targeting shared test data were excluded explicitly. The earlier integrated
checkpoint also passed 52 funding tests, 20 gateway race tests and 8 shortfall
tests. Gateway and shortfall sources were unchanged by this correction. Optional
golangci-lint remains incompatible with the target Go toolchain; goimports and
staticcheck are unavailable. No dependency or toolchain upgrades were made.

## Browser file boundary

A final controlled probe found that the development server could serve a
private runtime JSON file from `.dev`, despite the root `.env` being blocked.
The shared development-server policy now denies this checkout's `.dev` and
nested `.worktrees` directories, preserving Vite's default credential and
certificate exclusions. Checkout-relative absolute globs keep public app
source readable when the checkout itself is a Git worktree.

Real HTTP regressions cover the default, developer and product configurations,
including direct, filesystem, raw and module-import requests. All three tests
fail before the fix and pass afterward; normal HTML and app source remain
available. Private runtime contents are excluded from the verification logs.

## Retained evidence and local review

The private main-root run directory is
`.dev/product-airwallex-runs/fc-sbx-ui20261116b13dbe62/`. It contains:

- `journey-proof.json` and `journey-proof.before-restart.json`.
- `repaid-provider-transfers.json`.
- `repaid-browser-financial-audit.json` and `repaid-audit-after-restart.json`.
- `restart-and-idempotence-proof.json`.
- `browser-funding-replay-after-restart.json`.
- `deposit-timestamp-recheck.json` and
  `timestamp-recheck-preservation-audit.json`.
- The earlier source-conflict observations and refused attempts.
- Private `local-runtime.json`, containing the invited users and server
  settings.

The original UI is available at <http://127.0.0.1:18073/> in history review mode,
with financial workers, dispatch and public webhook ingress disabled. From the
repository root, `node .dev/product-airwallex-tools/history.mjs` starts the same
retained installation for a bounded six-hour review. Keep its private profile
out of Git and chat. The two run-owned temporary subscriptions were removed;
pre-existing subscriptions, ngrok and unrelated services were preserved.

Production rollout, real-customer consent/KYB, continuous CRM feeds and
real-model acceptance remain separate scopes. The agent used fixture behavior
and did not
override the saved hard-rule decision. This report establishes actual sandbox
browser acceptance for the stated financing lifecycle.
