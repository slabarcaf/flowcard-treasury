# Go browser gateway and real login

Selected first slice for the Node-to-Go and onboarding plans, November 15,
2026. Ramil selected real customer/operator login. Financial source data may
remain DEMO; authenticated identity must be real and server-controlled.

[TOC]

## Architecture and scope

Add `services/browser-gateway`, one Go HTTP process for browser sessions and
explicit upstream adapters. Existing services keep business rules, accounting
and internal JWT contracts. The gateway owns only users and sessions initially.
It does not import private business-service packages or financial policy.

A small gateway avoids duplicating browser authentication in each service.
Direct browser service-signing would expose credentials. Extending Node would
retain the dependency this cutover is intended to replace.

Use invite-only local accounts for this short-lived installation. Account
creation and disabling are privileged CLI operations. Password rotation remains
deferred. Public signup, email/reset infrastructure and external
identity-provider integration are outside this first slice. No existing
identity provider was found in the inspected application/dependency
configuration.

## Real identity

- Each user has a UUID, unique lowercase username, one immutable role
  `OPERATOR` or `CUSTOMER`, password verifier, active flag and creation time.
- Login accepts username/password only. Roles, customer IDs and service actor
  IDs are never accepted as login authority from the browser.
- Use standard-library PBKDF2-HMAC-SHA256, 600,000 iterations, random 16-byte
  salt and 32-byte verifier, with constant-time comparison. This is a deliberate
  dependency-free choice; OWASP generally prefers Argon2id. Store algorithm
  and work factor explicitly. Never use plain SHA256 for passwords.
- Passwords: at least 15 Unicode characters, at most 1024 UTF-8 bytes; no
  composition rules or silent truncation. CLI reads stdin without logging it.
- Sessions use 32 random bytes. Store only their SHA256 digest, user reference
  and absolute eight-hour expiry in PostgreSQL. Issue the raw token only in
  an HttpOnly, host-only `flowcard_session` cookie, Path `/api`, SameSite Lax.
- Secure cookies are default. An explicit local HTTP option is allowed only
  with a loopback listener and exclusively loopback allowed origins.
- Every session lookup joins the current active user. Disabling an account
  invalidates access immediately; logout deletes the presented session.
- Login failures are generic; unknown users perform dummy password work.
  Bound request bodies, password work and per-user/per-peer login attempts.
  Ignore forwarded identity/IP headers unless a later proxy contract enables them.

## Initial HTTP and storage contract

Proposed gateway routes, not existing service endpoints:

| Route | Behavior |
| --- | --- |
| `POST /api/session/login` | `{username,password}`; set cookie, return public user and expiry |
| `GET /api/session/me` | Public current user, role and expiry; no verifier/token |
| `POST /api/session/logout` | Revoke session, clear cookie; no other sessions affected |
| `GET /healthz` | Liveness only |
| `GET /readyz` | Configuration and restricted gateway DB readiness |

Auth responses use no-store. Mutations require exact configured Origin.
Missing-Origin authenticated GET/HEAD may use the existing strict same-origin
Fetch Metadata plus exact Referer-origin rule. Explicit untrusted/null Origin
never falls back. No wildcard CORS or role-switch endpoint.

Create a gateway-owned schema with users and sessions plus numbered migration
tracking. Runtime reads users and owns session CRUD only; no runtime DDL,
user/role provisioning or access to other services' tables. Migration/user CLI
uses a separate privileged connection and verifies its expected database name.
Default listener is loopback `127.0.0.1:8094`, verified free before launch.

## Onboarding and financial boundaries

First integrate operator-owned onboarding. The gateway authenticates an
OPERATOR, strips client identity/authorization headers and signs the existing
Go onboarding assertion with that user's server-owned subject. Retain the
existing issuer/audience contract until a separate coordinated change.
CUSTOMER sessions cannot use operator onboarding routes in this slice.

Use an explicit proxy allowlist, bounded bodies, streamed documents, fixed
configured upstreams, controlled errors and same-origin Location rewriting.
Keep idempotency keys and optimistic revisions intact. API docs remain public,
bundled and read-only; business routes stay authenticated.

Funding follows with a new typed client, not a wrapper that implements legacy
client-owned saveBridge/disburse semantics. Render backend cents/rules/terms;
reject unsafe JS integers. No client-side credit authority or silent mock
fallback when Go is unavailable. Keep the first server-selected 30-day flow.

Before customer funding writes, add an explicit trusted user-to-servicing
customer/actor binding, verified against actual backend records. Never join
companies by name or treat `comp_*`, `acct_*` and provider IDs as interchangeable.
A saved onboarding draft is not automatically an activated servicing customer.
Customer acceptance and operator approval require distinct authenticated users
and exact saved offer/version/hash. Provider mode remains DEMO until separately
authorized; real login does not establish live credit or provider acceptance.

## Sources and verification boundary

### Implemented Task C read boundary

The opt-in Go workspace adds a separate typed financial read client and screen.
Only active OPERATOR sessions may read saved Coordinator cases/evaluations and
Funding offers, bridges, operations and collection history. Customers remain
unlinked. There is no financial write route or legacy DataAdapter wrapper.

Each optional upstream has a server-only exact origin and paired secret.
Coordinator uses its existing fixed `coordinator-reader` subject, issuer
`flowcard-go` and audience `flowcard-coordinator-service`. Funding uses that
same read-only subject with audience `flowcard-funding-collection` and its
existing coordinator-role key. Do not substitute the underwriting read-model
key or browser UUID. Fresh assertions expire after 30 seconds. Browser
identity is checked independently at the gateway on every request.

Funding has no offer-list route. Discover saved IDs from its committed change
feed, preserving opaque cursors and the distinction between event/resource and
bridge versions. Collections must match the displayed bridge version or the
UI requires a fresh read. Upstream JSON is bounded to 2 MiB; reader-key failures
become controlled source-unavailable responses and do not clear browser auth.

Render supplied cents, totals, terms, source mode, versions and saved rule flags.
Reject unsafe JavaScript integers and format cents without floating-point
division. Unknown facts remain unavailable; stale accrual/observation flags
remain visible. No reconstructed exposure, fabricated zero or mock fallback.
Saved assessments remain non-authorizing, and displayed next steps are text.
Explicit DEMO labels describe synthetic books without implying provider
acceptance. Verification uses owned read fixtures and production handlers,
without financial commands, provider/model calls or running workers.

### Source references

- [Go standard PBKDF2](https://pkg.go.dev/crypto/pbkdf2).
- [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
- Existing onboarding auth and Funding contract are the upstream authorities.

Use owned disposable PostgreSQL targets and actual local HTTP for auth tests.
No root env copy, shared DB reset, credential logging, live provider request,
new dependency, deployment or whole Node deletion is part of this slice.
