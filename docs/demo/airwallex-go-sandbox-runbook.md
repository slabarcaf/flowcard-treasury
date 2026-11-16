# Go Airwallex sandbox runbook

The real-login browser journey has been verified against Airwallex sandbox:
actual insufficient-funds decline, saved assessment and offer, separate customer
acceptance and operator approval, settled funding, then deposit-driven
repayment.
The private run report records setup repairs and retained test funds.

[TOC]

## Boundaries

- Use only `https://api.sandbox.airwallex.com`. Production and AWS are outside
  this procedure. Automatic card retry is also outside scope.
- Platform P authenticates. Connected account A funds borrower B. Fee and
  principal collection flow from B to A. Account mappings are server-owned.
- Financial history and receivables are explicitly synthetic. Current account,
  card, authorization, balance, deposit and transfer facts use sandbox reads.
- Preserve root `.env` in its existing checkout. Pass its absolute path. The
  CLI reads selected literal assignments in-process, ignores cached tokens,
  refuses shell expansion and never copies the file into the worktree.
- Normal tests use doubles. Sandbox mutations require explicit CLI commands.
  Unknown mutation outcomes require reads and reconciliation, never a new key.
- The current reviewed example uses $9,000 attempted card spend, $2,000 initial
  borrower cash, $7,000 principal, $190 contract fee and a 30-day term. Exact
  route probes establish the selected zero provider fees. Other amounts or
  nonzero fees require independently verified contracts.

## Configuration and private evidence

See the service `.env.example` files for names and placeholders. Credentials,
provider IDs, webhook secrets and service assertion keys stay server-side.
Never put them in `VITE_` variables or browser storage.

A private run directory contains `manifest.json`, `local-runtime.json`, owned
subscription settings, protected provider/signed webhook samples, expiring
contracts, decision log and `journey-proof.json`. Directories require mode
0700; files require mode 0600. Keep them ignored by Git.

Shortfall needs `SHORTFALL_SANDBOX_CONTRACT_FILE`. Funding uses
`SERVICING_SANDBOX_PAYMENT_CONTRACT_FILE` and
`SERVICING_SANDBOX_EXECUTION_CONTRACT_FILE`. Contracts expire after eight hours.
The explicit delegation, funding and collection switches default off. The
legacy live-dispatch switch is also required before first send. A mode name
alone enables none of these capabilities.

The shortfall contract pins subscription, version, account, card and customer.
It resolves both observed issuing notification shapes to the unique initial
canonical authorization event and retrieves it again. Only an ACTIVE
`COMMERCIAL/CREDIT/GOOD_FUNDS_CREDIT` wallet card is supported by this profile.

The execution contract pins installation, A/B mappings, exact operation kind
and amount, numeric transfer fees, terminal `updated_at` settlement times,
receiving Global Account and cutover time. A matching authenticated scoped
response establishes transfer source, even when the native response omits
`source_account_id`. A conflicting explicit source is rejected.

Deposits must be canonical, settled external `BANK_TRANSFER` credits with an
explicit zero fee. Internal funding credits do not become repayment budgets.
Unsupported reversal/refund facts install a visible review hold and preserve
financial amounts. Signed webhooks wake reads; they never settle money alone.

## Commands

Run from the implementation worktree. These shell variables contain paths and
an ownership marker, never secret values:

```sh
awx_env=/absolute/path/to/develop-checkout/.env
awx_run=fc-sbx-UNIQUE_LOWERCASE_MARKER
awx_dir="$PWD/.dev/airwallex-sandbox/$awx_run"
awx_manifest="$awx_dir/manifest.json"
awx_database=flowcard_funding_demo_sandbox_UNIQUE_SUFFIX
awx_admin_file=/private/path/local-test-admin-url.txt
```

Each sandbox command requires `--environment SANDBOX` and `--env-file`.
Commands owning resources also require `--manifest` and `--run-id`.
Administrative local commands require the exact `--database-name` and separate
`--test-admin-url-file`. The latter accepts only localhost PostgreSQL port
54329 and the maintenance database, then verifies target name, owner and marker
before writes. Runtime logins cannot migrate, provision users or own schemas.

The CLI reports fixed safe errors. Do not replace a missing/unknown outcome
with success, print raw driver/provider errors or repeat a money mutation.

### Read-only preflight

```sh
go -C services/funding-collection run ./cmd/sandbox preflight \
  --environment SANDBOX --env-file "$awx_env"
```

This authenticates with client ID/key, using `AIRWALLEX_ACCOUNT_ID` as platform
login context, then verifies ACTIVE platform identity and required read scopes.
A configured webhook secret or MCP login alone does not prove runtime access.

### Owned provider setup

Use the common arguments above for these explicit commands, in order:

1. `setup-accounts --accept-sandbox-agreements`, only after explicit authority
   to accept required agreements for synthetic businesses. This is not a
   production customer consent or collection mandate.
2. `complete-profiles`, `activate-accounts`, `inspect-accounts`.
3. `setup-receiving`, `setup-cardholder`, `setup-card`.
4. `seed-opening-balances`, `inspect-balances`.
5. `setup-webhooks --callback-origin-file /private/path/callback-origin.txt`.

The origin file contains an approved HTTPS tunnel origin. Setup creates exactly
one shortfall and one servicing subscription for this run, preserving shared
subscriptions. Expose only the two signed webhook POST routes. Never expose the
browser gateway, administrative CLIs or unrestricted proxy paths publicly.

Capture contract samples before starting the business runtime:

```sh
go -C services/shortfall-detection run ./cmd/capture \
  --subscriptions-file "$awx_dir/capture-subscriptions.json" \
  --output-directory "$awx_dir/webhook-evidence" \
  --run-id "$awx_run" --http-addr 127.0.0.1:1234
```

The receiver is bounded to 15 minutes and 100 protected deliveries, verifies
signatures, filters owned account scopes and detects conflicting duplicates.
While it runs, use `simulate-decline`, `capture-event`,
`simulate-callback-decline`, `probe-deposit` and `probe-transfers` as separate
explicit actions. Transfers progress naturally through pending to settled.
Reinvoking a known transfer probe retrieves its existing provider object.
Unknown creation responses never authorize another create.

Probe order matters: the $190 contract deposit precedes exact $7,000 A-to-B,
$7,000 B-to-A principal and $190 B-to-A fee probes. Both balance deltas and
canonical settled GETs must match. Setup probes are excluded from loan books.
The initial borrower balance returns to $2,000 after these probes.

### Owned local books and contracts

Run `prepare-local` with the expected owned database and admin URL file. It
runs the actual migrators for all five services, provisions five restricted
logins and creates distinct invited customer/operator accounts via the native
user CLI. Their passwords remain private. Provisioning never occurs at startup.

Run `seed-local-books` with the same administrative arguments. It requires
known-empty owned books or the exact existing bootstrap journal, imports the
synthetic underwriting context, actual mappings and sandbox collection
authority, and creates a separate SYSTEM audit actor without a browser login.
It never imports historical debt or backfills shared financial data.

Then run `export-shortfall-contract`, `export-execution-contract` and
`bind-local-users`. Exporters verify current account/card and exact probe facts
plus original signed samples. Stop the capture receiver before `serve-local`.

### Review retained results

Use `serve-local --history` with the retained private manifest and run marker
to review a completed installation. It serves the original operator/customer
interface on port 18073 for up to six hours. Provider dispatch, both provider
workers, public webhook ingress and automatic snapshot publication are
disabled. The interface labels this history view. Financial execution
contracts are not loaded in this mode, so their expiry does not prevent
historical reads. Current provider wallet reads remain available.

Supply the existing optional `BROWSER_GATEWAY_ONBOARDING_URL` and
`BROWSER_GATEWAY_ONBOARDING_SECRET` together, server-side, to connect
Applications to the local Go onboarding API. The gateway retains its normal
URL, credential and role validation. Keep the secret in a private file or
process environment, never a command argument or browser variable.

Use a fresh verified installation and ordinary `serve-local` for a new
financial acceptance run. Preserve the completed ledger and provider objects.

### Browser acceptance

```sh
go -C services/funding-collection run ./cmd/sandbox serve-local \
  --environment SANDBOX --env-file "$awx_env" \
  --manifest "$awx_manifest" --run-id "$awx_run"
```

The command owns bounded child processes and stops them on exit or after 45
minutes. The browser frontend uses loopback port 18073; its Go gateway uses
18094. APIs/workers use restricted database logins. Port 1234 accepts only the
two signed callback routes. Existing manual services remain untouched.
Coordinator uses the explicit rule/fixture investigation mode, with no external
AI call. Fresh facility snapshots reflect current locked servicing books.

Run `simulate-journey-decline` once for a new genuine insufficient-funds
purchase. The live webhook, inbox, worker, Coordinator and deterministic policy
must produce a fresh eligible assessment. In separate browser sessions:

1. Operator opens the saved case and creates the offer.
2. Customer refreshes their offer list, opens the exact terms and accepts them.
3. Operator refreshes the offer version, approves and requests funding.
4. Read the bridge until the canonical provider retrieval establishes ACTIVE.
   An HTTP 202 reservation is insufficient evidence of settlement.
5. Run `simulate-income` once for the marked $7,190 external bank deposit.
6. Verify fee-first $190 and principal $7,000 collection and a REPAID bridge.
   Both customer and operator must show zero outstanding debt.

Expired terms and stale evidence remain rejected. The separately named
`simulate-recovery-decline` and `simulate-recovery-income` are explicit new
synthetic scenarios, not retries of an uncertain provider command. Record every
extra credit and retained failure. Do not use them to hide a failing guard.
A sandbox deposit GET can initially return 400 before indexing; retry its read,
preserving the original intent and provider ID.

### Reconciliation and cleanup

```sh
go -C services/funding-collection run ./cmd/sandbox verify-journey \
  --environment SANDBOX --env-file "$awx_env" \
  --manifest "$awx_manifest" --run-id "$awx_run" \
  --database-name "$awx_database"
```

This makes restricted database reads and scoped provider GETs. It joins the
original canonical decline to detection, case, assessment, offer, independent
actor decisions, three settled money operations and balanced ledger evidence.
It verifies zero debt, released customer/facility/authority/invoice reservations
and saves a private redacted proof. It never changes application financial
state.

`inventory-local` takes the same arguments and prints safe counts and retention
policy. `cleanup-local` additionally needs `--test-admin-url-file`; it refuses
financial audit evidence and active runtimes. Empty disposable installations
are gated against new connections before a final audit check. Cleanup only
drops the exact marker-owned database and LOGIN roles, never shared NOLOGIN
roles or volumes. Retain the completed acceptance database for audit review.

`cleanup-webhooks --callback-origin-file PATH` removes only the two exact
manifest-owned temporary subscriptions after matching their live URL, request
ID and version. It verifies deletion by GET and reconciles a lost response
without repeating deletion. The shared configured subscription is protected.
Then stop the owned stack and isolated browser sessions. The caller-owned ngrok
process can remain running; with the receiver closed it exposes no local app.
Accounts, cards, Global Accounts, test funds and financial evidence are
retained.

## Verified acceptance and limits

The implementation run completed on November 15, 2026 local time. Three actual
loan transfers settled: $7,000 funding, $190 fee and $7,000 principal. The
customer browser showed REPAID, zero debt and current accrual. A full owned
stack restart preserved all three transfers, balances and four balanced ledger
transactions without another money create.

Debugging retained one expired offer, two setup-induced holds with audited
owned-installation repairs, and one proven-unsent cancelled fee intent. A
separate recovery deposit completed repayment. There were two $7,190 income
simulations, and the first credit remains unconsumed. Final available sandbox
balances were A $100,380 and B $16,190, including the separate $190 setup credit
and unused original income credit. These are test funds, not revenue from a
real customer or a successful card purchase.

The card authorization remains a decline. Production KYB/consent, real
historical
financial integrations, generalized amounts/fees, partial nonzero-fee deposits,
verified reversals/refunds and automatic card retry require separate work.
The customer workspace still uses a known bridge UUID for direct bridge reads.

## Checks

Run fixture-based Go race tests with `FUNDING_TEST_ADMIN_URL_FILE` and gateway
acceptance with `BROWSER_GATEWAY_TEST_ADMIN_URL_FILE` pointing to the existing
private localhost maintenance URL file. No DB opens from package initialization.
Absent input is explicitly unverified DB acceptance.

The final run passed 51 Funding, 20 gateway and 8 shortfall top-level Go tests,
including their subtests and actual owned PostgreSQL scenarios. All 383 frontend
tests passed. All three Go services built and passed vet; the Go browser
frontend
built. Lint passed with existing warnings. Test details and exact commands are
in the private report. No dependency, historical migration or root `.env` was
changed, and nothing was committed, merged, pushed or deployed.
