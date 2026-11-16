# Browser gateway

Invite-only real OPERATOR and CUSTOMER accounts with PostgreSQL sessions. This
service owns browser authentication and an exact operator onboarding proxy.
Customers receive financial access only after CLI-verified DEMO binding. The
dedicated opt-in frontend reads saved Coordinator/Funding records. Local DEMO
commands use exact terms, role bindings and durable recovery.

[TOC]

## Configuration and commands

Use the same Go toolchain and dependency versions as `underwriting-engine`.
No new dependencies are required. Run commands from `services/browser-gateway`.
The configuration names and placeholders are in `.env.example`. Supply secrets
through the local environment, without copying the repository root `.env` into
a worktree. Never put database URLs in command arguments, tracked files or logs.

An administrator must prepare a dedicated database and separate privileged and
restricted login connections. No account is created by API startup. The
migrator creates only `browser_gateway` and tracks numbered SQL SHA256 checksums
under an advisory transaction lock. It never creates global runtime roles.
The expected database name is mandatory and checked before any migration or
user write. `DATABASE_URL` is used only by the API;
`BROWSER_GATEWAY_ADMIN_DATABASE_URL` is used only by administrative CLIs.

```sh
go run ./cmd/migrate --database-name flowcard_browser
```

On that dedicated database, grant an administrator-created restricted login
only these rights. Replace the database and role identifiers in this SQL.
Apply database-wide revocations only to the dedicated new gateway database.
Do not change permissions in an existing shared service database.

```sql
REVOKE ALL ON DATABASE flowcard_browser FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE flowcard_browser TO browser_runtime;
GRANT USAGE ON SCHEMA browser_gateway TO browser_runtime;
GRANT SELECT ON browser_gateway.users TO browser_runtime;
GRANT SELECT, INSERT, DELETE ON browser_gateway.sessions TO browser_runtime;
GRANT SELECT ON browser_gateway.servicing_bindings TO browser_runtime;
GRANT SELECT, INSERT ON browser_gateway.financial_commands,
    browser_gateway.financial_receipts, browser_gateway.customer_offers
    TO browser_runtime;
```

The runtime login must own no database, application schema or relation, have
no administrative or predefined privileged role memberships, and lack CREATE,
TEMP, user writes, session updates and unrelated table/sequence access.
`/readyz` rejects excess rights, including column grants and switched-role
administrative connections. API startup requires the same check. Recheck
readiness after any administrative privilege change.

Provision only invited users through the privileged CLI. Feed a password from a
pipe or private input file. Terminal devices are rejected to prevent terminal
echo. The following paths are placeholders, not files to create in source:

```sh
go run ./cmd/users create --username operator.one --role OPERATOR \
  --password-stdin --database-name flowcard_browser < /private/path/password

go run ./cmd/users create --username customer.one --role CUSTOMER \
  --password-stdin --database-name flowcard_browser < /private/path/password

go run ./cmd/users disable --username customer.one \
  --database-name flowcard_browser
```

Each role belongs to a distinct UUID user. Role changes are rejected by a
schema trigger. Usernames are case-sensitive lowercase ASCII and must match
`^[a-z0-9][a-z0-9._-]{2,63}$`. Passwords require at least 15 Unicode code points
and at most 1024 UTF-8 bytes. There are no composition rules or normalization.
Stdin removes exactly one trailing LF and its optional preceding CR. All other
whitespace is retained, including a second newline. Passwords use a versioned
PBKDF2-HMAC-SHA256 verifier, 600,000 iterations, a random 16-byte salt and a
32-byte verifier. CLI failures contain fixed safe messages.

After review and deliberate provisioning, start a foreground API with only its
restricted runtime environment:

```sh
go run ./cmd/api
```

Default listener: `127.0.0.1:8094`. Header, read, write, idle, database
connection and shutdown timeouts are bounded. There is no automatic migration,
account creation, financial action or public signup.
Existing services and their schemas are independent.

## HTTP contract and security limits

| Route | Result |
| --- | --- |
| `POST /api/session/login` | `{username,password}`; 200, cookie and public session |
| `GET /api/session/me` | 200, public session; 401 for missing or invalid session |
| `POST /api/session/logout` | 204, revoke presented session and clear cookie |
| `GET /healthz` | 200 liveness |
| `GET /readyz` | 200 restricted DB readiness, otherwise safe 503 |

The public session is `{user:{id,username,role},expires_at}`. Responses contain
no token, salt, verifier, database URL or customer/actor binding. Browser
Authorization, actor and forwarding headers supply no authority. Auth responses
use `Cache-Control: no-store` and vary on Origin, Sec-Fetch-Site and Referer.

Auth POSTs require an exact configured Origin. GET/HEAD may omit Origin only
with `Sec-Fetch-Site: same-origin` and an HTTP(S), credential-free Referer whose
origin exactly matches the allowlist. Explicit empty/null/untrusted or duplicate
Origin never falls back. Credentialed CORS echoes only a validated exact
origin; no wildcard or cross-origin preflight workflow is provided in Task A.
Login JSON is bounded to 8 KiB and rejects unknown/duplicate keys, trailing
values, invalid UTF-8, non-string credentials and malformed JSON. Logout accepts
an empty body only (whitespace is allowed).

Cookies are host-only `flowcard_session`, Path `/api`, HttpOnly and SameSite
Lax. They are Secure by default. `BROWSER_GATEWAY_ALLOW_LOOPBACK_HTTP=true`
disables Secure only if the listener and every allowed origin are loopback.
Invalid settings fail closed. This option is for deliberate local HTTP previews.
Every non-loopback frontend origin must use HTTPS, even with Secure cookies.

Sessions are random 32-byte values encoded as 43-character base64url cookies.
Only the SHA256 digest of the random bytes is stored. Absolute expiry is eight
hours, with no refresh. Every lookup joins the current active user, so disabling
a user immediately denies subsequent requests. Logout affects only the current
session. Expired rows are denied; automatic row cleanup and password rotation
are future administrative work.

Login rejects bad/disabled credentials with the same 401. Unknown/disabled users
perform dummy derivation. Storage failures return safe 503 on login, me and
logout, without clearing the session cookie. Before derivation, a fixed-window
limiter allows five
attempts per username and twenty per direct peer per minute. It ignores
X-Forwarded-For. Its TTL map holds at most 1000 entries and fails closed at
capacity. Four concurrent password derivations are allowed. Limits return 429
and `Retry-After: 60`. Limits are process-local and reset on restart; run a
single gateway for this installation. No trusted reverse-proxy contract exists.

## Verification

Routine unit checks require no database. Database acceptance is explicitly
UNVERIFIED when its admin file variable is absent, even when other tests pass.
An owning test opens PostgreSQL only when that scenario runs, without init or
TestMain database work.

```sh
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go build ./...
GOPROXY=off GOSUMDB=off go vet ./...
```

For actual acceptance, supply a private maintenance URL file through the test
variable. The file must identify `/postgres` on the existing loopback PostgreSQL
port 54329. Its value is never printed. Tests create only unique marked
databases and LOGIN roles, run the actual migrator and restricted runtime, and
remove only exact resources after matching markers and database ownership.
They never drop shared NOLOGIN roles or change existing service data.

```sh
BROWSER_GATEWAY_TEST_ADMIN_URL_FILE=/private/path/test-admin-url.txt \
  GOPROXY=off GOSUMDB=off go test ./... -count=1 -v
```

The three independent owned scenarios exercise session persistence and restart,
logout, disable, expiry and distinct roles; real HTTP Origin/body/forgery/rate
denials; migration idempotence/checksums, expected-target protection, runtime
write/DDL denial and administrative readiness refusal. All listeners are
short-lived loopback test listeners. Tests do not provision real users or make
provider, AWS or model requests.

## Operator onboarding boundary

Optional server-only `BROWSER_GATEWAY_ONBOARDING_URL` and
`BROWSER_GATEWAY_ONBOARDING_SECRET` must both be set or both absent. The secret
matches the existing onboarding service's `ONBOARDING_SERVICE_SECRET` and is
at least 32 bytes. The destination is an exact HTTP(S) origin without userinfo,
path prefix, query or fragment. HTTP requires loopback; other destinations
require HTTPS. Redirects are refused. The browser cannot choose a destination.

Only current active OPERATOR sessions reach implemented company, application,
document, submission and platform-account routes. The method/path allowlist
mirrors existing Go handlers; webhooks, internal/admin routes and arbitrary
prefix routing are excluded. Documentation proxy is deferred. Missing or
expired sessions return 401, customers 403, storage outages 503. Auth checks
precede upstream calls. Existing exact Origin/Referer rules still apply.

Every request receives a fresh HS256 assertion: existing issuer
`flowcard-node-proxy`, audience `flowcard-onboarding-service`, authenticated
immutable UUID subject, random jti and 30-second expiry. Browser Cookie,
Authorization, actor and forwarding headers are discarded. JSON/multipart
bytes, exact query and allowed revision/idempotency headers are preserved.
JSON is bounded to 1 MiB, document multipart to 15 MiB; onboarding separately
enforces its actual 10 MiB file limit. Encoded or ambiguous paths are rejected
before routing. Upstream calls have a 30-second deadline and no blind retry.

Document responses stream with cancellation and selected content headers.
Upstream status/receipt/error bodies are preserved. Set-Cookie, Location and
authentication/identity headers are stripped. Upstream redirects become safe
502, timeout becomes 504, and other transport failure becomes safe 502. Failed
writes may have reached the service; inspect saved state before retrying.

## Dedicated Go frontend preview recipe

These are post-review manual commands, not evidence of a running preview.
No persistent gateway, onboarding service or real users were started by this
source delivery. Preserve existing manual listeners and data. Before choosing
ports, inspect availability without stopping any process:

```sh
lsof -nP -iTCP:8094 -iTCP:5175 -sTCP:LISTEN
```

If either port is busy, choose free ports and update both the private gateway
origin/listener settings and the development override. Use a separately
reviewed onboarding runtime/database target and secret. Keep all server-only
settings in a private environment file outside source, never the root `.env`.
Its gateway settings include the following names and example public values:

```text
DATABASE_URL=<restricted gateway runtime URL>
BROWSER_GATEWAY_HTTP_ADDR=127.0.0.1:8094
BROWSER_GATEWAY_ALLOWED_ORIGINS=http://127.0.0.1:5175
BROWSER_GATEWAY_ALLOW_LOOPBACK_HTTP=true
BROWSER_GATEWAY_ONBOARDING_URL=<reviewed onboarding origin>
BROWSER_GATEWAY_ONBOARDING_SECRET=<existing server-only service secret>
```

After deliberate review, migrations and invitation provisioning, start the
gateway in one foreground terminal with that private runtime environment.
Do not supply the administrative URL to the runtime process. Start the
frontend from the repository root in another foreground terminal:

```sh
VITE_BROWSER_GATEWAY_ENABLED=true \
  BROWSER_GATEWAY_DEV_TARGET=http://127.0.0.1:8094 \
  npm run dev -- --config vite.browser-gateway.config.ts
```

Open `http://127.0.0.1:5175`. This dedicated config binds loopback and refuses
an occupied port. Its optional development target must be a loopback HTTP
origin. The public VITE flag enables UI capability only and contains no secret.
When absent, the original app and original Vite/Node target remain unchanged.

Operators use Applications, New company draft or Financial records. Hash links
pass through the same role boundary; customers see Account not linked and no
operator/financial
controls. Financial fixture screens are excluded. The page restores `/me`,
offers logout and reauthentication, and uses proper username/current-password
autocomplete. Password state is cleared after every attempt. No password or
raw session token is stored in browser storage. Drafts remain mounted through
failed login, expired-session errors or 503. Reauthentication never retries a
write automatically. Confirm uncertain saved state before retrying manually.
Logout or a change of authenticated UUID unmounts displayed company/application
data. Logout 503 preserves the current display and reports uncertainty.

Task B adds focused proxy tests, frontend session/role/error tests and a real
onboarding production-handler/repository journey behind the gateway. The
onboarding test uses a temporary Go overlay, normal migrations and actual JWT
auth; it writes no onboarding source file. Existing shared migration-role
attributes are checked unchanged. Test listeners are loopback and temporary;
no provider worker, submission or provider request runs in that real journey.

## Read-only financial workspace

Task C adds exact GET routes behind current active OPERATOR sessions:

| Browser route | Source |
| --- | --- |
| `/api/coordinator/cases` | Coordinator saved case list with opaque cursor |
| `/api/coordinator/cases/{id}` | Saved assessment, rules and investigation |
| `/api/servicing/changes?after_seq=N` | Funding's committed discovery feed |
| `/api/offers/{id}` | Exact saved terms and aggregate/offer versions |
| `/api/bridges/{id}` | Current backend balances, guards and source timestamps |
| `/api/bridges/{id}/collections` | Version-bound collection history/cursor |
| `/api/operations/{id}` | Saved operation and retry/hold state |

No financial command, callback or broad prefix is forwarded. Customers receive
403, invalid sessions 401, unavailable auth storage 503. All original Origin,
header, timeout, cancellation, redirect and no-store protections remain.
Read JSON is limited to 2 MiB. Upstream reader-key 401/403 becomes controlled
503 because the already-verified browser session must remain intact.

Optional server-only configuration uses independent pairs:

- `BROWSER_GATEWAY_COORDINATOR_URL` and
  `BROWSER_GATEWAY_COORDINATOR_READ_SECRET`: existing
  `COORDINATOR_READER_SECRET` for the Coordinator reader API.
- `BROWSER_GATEWAY_SERVICING_URL` and
  `BROWSER_GATEWAY_SERVICING_READ_SECRET`: existing
  `SERVICING_COORDINATOR_SECRET` for fixed subject `coordinator-reader`.

The Funding key is its coordinator-role key, not `SERVICING_READER_SECRET`
(the latter belongs to the underwriting read model). Gateway assertions have
fixed issuer `flowcard-go`, the owning service's exact audience, fixed
`coordinator-reader` subject, fresh random jti and maximum 30-second lifetime.
No browser can choose a subject, service, role, upstream URL or signing key.
Keep both pairs absent to leave sources explicitly unavailable. Each configured
origin must satisfy the same no-prefix/userinfo/query/fragment and TLS rules
as onboarding. Do not put any of these settings in VITE variables.

Use the private preview recipe above, adding these paired server settings
only after verifying the target service and key. Open Financial records, or
use `#bridge-capital` / `#financial-records` in the opt-in Go app. The original
Vite/Node profile and default legacy app remain separate. Customer hashes still
render Account not linked and expose no financial navigation.

The new typed client has GET methods only. It uses real saved records, never
legacy MockAdapter financial data. Funding's change feed supplies discovery
IDs; no nonexistent offer-list API is invented. Detail retains immutable offer
revision versus aggregate version, exact terms hash, source mode, snapshots,
nullable values, guards and backend next-step text. Saved assessment does not
become approval/funding authority. Commentary remains separately labelled.
Amounts are safe integer cents and render exactly, including at JavaScript's
safe-integer boundary. Totals/exposure/debt are not reconstructed client-side.
A missing amount is Unavailable, not zero. Collections must match the displayed
bridge version; 409 requires manual refresh and restart. A source outage never
becomes an empty successful list or a fixture fallback. No request is retried
automatically. Logout or identity change removes displayed financial data.

Final verification includes 15 Go tests plus 18 HTTP subtests with race
detection and 41 focused frontend tests. The new owned financial scenario uses
production Coordinator/Funding JWT readers and repositories, normal migrations
and explicit saved-read fixtures. Its bridge is VOID_UNFUNDED with a revoked
synthetic authority, zero debt and empty history. These fixtures prove read
contracts, not underwriting or financial transition acceptance. No provider,
model, scheduler or worker runs. Shared role attributes remain unchanged.
A separate headless browser walkthrough verified actual reads, precision/unknown
labels, source outage, logout clearing, customer hash denial and backend 403.
Its browser/listeners and all three owned database/login pairs were removed.

The complete file inventory, commands, fixture/production distinctions and
remaining Task D boundary are in `.dev/browser-auth/task-c-report.md`.

## Verified local DEMO bindings and commands

Task D adds a separately gated local DEMO profile. With the mode/key variables
absent, financial POST routes remain unregistered. To enable them, supply
`BROWSER_GATEWAY_FINANCIAL_WRITE_MODE=DEMO`, the existing distinct Funding
operator/customer keys in `BROWSER_GATEWAY_SERVICING_OPERATOR_SECRET` and
`BROWSER_GATEWAY_SERVICING_CUSTOMER_SECRET`, and the verified paired servicing
read configuration. All three role keys must differ. The upstream must be
loopback. SANDBOX/remote command profiles fail closed.

The actual Funding actor self-read must also report a DEMO installation with
MOCK_HTTP loopback provider. Gateway runtime has no Funding SQL access and
never creates actors or financial customer mappings. An administrator uses
normal Funding provisioning first, then binds the existing actor to an invited
user through the gateway's privileged CLI. No company-name or provider-ID join
is inferred. Apply the new numbered gateway migration before runtime startup.

```sh
go run ./cmd/migrate --database-name flowcard_browser

go run ./cmd/bindings create --username operator.one \
  --subject EXISTING_OPERATOR_AUTHENTICATION_SUBJECT \
  --database-name flowcard_browser

go run ./cmd/bindings create --username customer.one \
  --subject EXISTING_CUSTOMER_AUTHENTICATION_SUBJECT \
  --database-name flowcard_browser

go run ./cmd/bindings disable --username customer.one \
  --database-name flowcard_browser
```

These are post-review commands using private administrative/server settings,
not evidence of real provisioning. The mandatory expected gateway database is
checked before any write. Creation verifies the actual active actor, user's
immutable role, mapped customer and installation through the authenticated
Funding API. The binding is immutable apart from disabling and each subject/
actor belongs to one gateway user. Reassignment requires separately reviewed
provisioning, not a browser role/customer switch. Disabling needs only the
administrative DB connection, so it works during source outages.

Funding's existing actor importer supports reassignment. The additive service
assertion constraint checks actor ID/type/customer and mode/facility/source
binding against the current registry. It only narrows the already-authenticated
subject and cannot grant identity or role. Existing direct CLI callers remain
compatible; gateway delegation always carries constraints. Backend actor drift,
inactive mapping or changed installation denies subsequent delegated requests.

New public gateway endpoints include authenticated servicing binding status,
this user's durable command history, and this customer's indexed offer IDs.
Only verified CUSTOMER sessions can read their offered terms/bridges/history
using their own constrained actor credential. They cannot read broad change
feeds, Coordinator data or another customer's offer. Unlinked users retain
no financial authority. Operator C reads remain separate from write binding.

Financial POSTs are exactly create-offer, acceptance, approval, funding,
collection-review and reconciliation. Server derives the principal from its
binding; request JSON cannot provide actor/customer/role, amount or policy.
Create-offer source is checked against the saved Coordinator case/evaluation,
then Funding independently derives/rechecks terms. Actual create/read receipts
now expose source mode/customer ID so the gateway verifies the customer index
against its owning financial source. Customer acceptance and operator approval
use distinct users and exact saved terms hash/aggregate version. Backend owns
all consent guards, policy, reservation, settlement and ledger accounting.

Each canonical intent is committed before sending, with original path/payload,
request hash, user/binding and idempotency key. Definitive receipts and verified
customer index entries are appended atomically. Runtime can SELECT bindings
and SELECT/INSERT intents/receipts/index only; it cannot alter/delete them or
provision users/bindings. Unknown results survive restart. Conflicting body/key
returns 409; manual retry reuses the same saved payload/key. No transport error
causes a blind retry, fake settlement or new-key replay. Cached originals are
recovered before current Coordinator source checks. History remains readable
from local ownership during source outages; new commands require current actor
verification. Recovery is bounded to 50 entries with unknown outcomes first.
Customer index shows the latest 50 IDs explicitly; detail is always re-read.

The customer page displays exact DEMO amount/fee/total/due date, revision,
aggregate version, hash, disclosure/waterfall/late-fee metadata and expiry.
Acceptance starts unchecked. Operator approval requires explicit action and
reason; funding/review/reconcile remain separate. A snapshot locks after any
attempt. Unknown commands block new actions for that record until recovered.
Known queued/reserved receipts are labelled as receipts, not settled funds.
No password, session token or financial payload is stored in browser storage.

Owned verification drives real sessions/bindings, actual Funding API, durable
operations, real worker and production Go adapter against the local provider
double. Only mock payment/deposit/transfer observations are controlled; normal
signed local webhook wakes and worker commands produce every ledger effect.
The journey covers lost receipt/restart replay, cross-customer/role/hash/drift
and disable denials, $7,000 funding, $190 fee, $100 then $7,090 deposits,
fee-first collection and zero-debt REPAID closure. It establishes DEMO fixture
execution only, not live consent/provider acceptance or actual card retry.

The isolated browser verified consent, distinct operator approval, durable
manual recovery and customer privacy. An initial funding attempt correctly
rejected stale provider evidence. A subsequent owned browser walkthrough
explicitly refreshed the local provider double's payment observation, requested
funding, then read an ACTIVE bridge with $7,000 principal/$190 fee and a SETTLED
operation. The RESERVED receipt alone was never treated as settlement. The
separate API journey proves full repayment. No production guard was changed.
