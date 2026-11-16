# Go service deployment

The registered `aws-demo.yml` workflow now builds the original Go-connected
product and all six Go modules. A manual run defaults to build-only. No push
or pull request can assume the AWS deployment role or deploy services.

This is source and pipeline preparation. Updating the live AWS foundation,
populating secrets and dispatching a deployment are separate operator actions.
Never treat a green container health check as provider or money readiness.

[TOC]

## Runtime layout

One private Fargate task runs six API containers, with three optional workers.
Containers in the same `awsvpc` task share localhost, so the existing service
URL checks continue to require TLS outside loopback. Each container receives
its own restricted database URL and selected signing secrets. Administrative
URLs appear only in the one-off migration task.

The task retains the existing onboarding API role's document-bucket/KMS rights,
so document storage does not regress during consolidation. IAM is task-wide;
per-container separation here applies to database and injected secrets. The
configuration-only preflight retains the zero-AWS-resource runtime role.

The existing `flowcard-demo-node-gateway` service/family and ECR repository
remain deployment-slot aliases. Their new image and public process are Go,
not Node. The legacy separate onboarding API/worker services are quiesced by
the release controller. Their exact previous revisions remain in recovery
metadata; no database or audit history is removed.

| Container | Port | Role |
| --- | --- | --- |
| `node-gateway` | 3000 | Real-login Go browser boundary |
| `onboarding-api` | 8080 | Company/onboarding API and native receipt |
| `shortfall-api` | 8082 | Native signed decline intake and detection reads |
| `underwriting-api` | 8083 | Saved deterministic evaluations |
| `funding-api` | 8085 | Offers, servicing and signed deposit/transfer receipt |
| `coordinator-api` | 8090 | Discovery, monitoring and explanations |
| `onboarding-worker`, `shortfall-worker`, `funding-worker` | None | Explicitly enabled background work |

The common image includes every owner migration and the public policy module
used by servicing. It runs as UID/GID 10001, includes the RDS CA bundle and
targets `linux/amd64`. It contains no Node backend, root `.env`, `.dev`, nested
worktree, private profile or local acceptance data.

## One-time foundation preparation

Review the Terraform plan before applying. `enable_go_stack_foundation=true`
adds three native-receiver target groups and private ALB-to-task rules, plus
the narrowly scoped deployment permissions they need. The listener rules start
behind a preview header, so foundation creation does not switch existing
callbacks. The release controller activates them only after all targets are
healthy and the operator has confirmed callback migration/disable review.

Persist the enabled setting in the reviewed rollout configuration. Terraform
owns initial service provisioning; the release controller owns active task
revisions, counts, target attachments and native-rule activation afterward.

After the reviewed apply, export only the non-secret output:

```sh
terraform -chdir=infra/environments/demo output -json go_release_foundation \
  > /private/path/go-foundation.json
gh variable set GO_RELEASE_FOUNDATION_JSON < /private/path/go-foundation.json
```

The exported object includes the actual RDS host/name, secret ARNs, private
subnets, exact task template, target groups and listener rules. The workflow
rejects a disabled, missing, placeholder, foreign-account or wrong-region
foundation before assuming AWS credentials. Existing repository AWS account,
region, deploy-role, frontend bucket and distribution variables remain in use.

The GitHub role can modify only the three prepared Go rules. ELB describe APIs
require wildcard resources, restricted here to the selected region. ECS task
listing is restricted to the demo cluster. No secret-value, RDS-administrator
or general IAM mutation permission is added to the deployer.

## Database preparation

Use a dedicated Go migration login in `flowcard/db/migrate`, key
`go_stack_url`. It must connect to the exact deployment database over verified
TLS, own or inherit the existing onboarding schema owner, create the new Go
schemas/NOLOGIN roles, and grant SELECT on the preserved roster/card mappings.
It must have CREATEROLE for owned runtime-login provisioning. Never grant the
RDS master/superuser role to a runtime or migration login.

An administrator must review this one-time preparation against the actual
database and owner names. The preserved legacy mapping tables must exist.
The migration pipeline never rewrites the historical Node migrations.

```sql
-- Example for the existing flowcard_treasury database. Set the migration
-- password privately, never as a literal in this file or a shell argument.
CREATE ROLE flowcard_go_migrate LOGIN NOSUPERUSER NOCREATEDB CREATEROLE
  NOREPLICATION NOBYPASSRLS;
GRANT CONNECT, CREATE ON DATABASE flowcard_treasury TO flowcard_go_migrate;
REVOKE CREATE, TEMPORARY ON DATABASE flowcard_treasury FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO flowcard_go_migrate;
GRANT SELECT ON public.customer_roster, public.issuing_card_bindings
  TO flowcard_go_migrate WITH GRANT OPTION;
-- Verify the existing owner first. Do not substitute an administrator role.
GRANT flowcard_migrate_onboarding TO flowcard_go_migrate;
GRANT onboarding_service TO flowcard_go_migrate WITH ADMIN OPTION;
```

If other Go grant roles already exist under a different creator, grant the
migration login ADMIN OPTION on those specific NOLOGIN roles after review.
Do not alter existing passwords or adopt unrelated runtime users. Provisioned
runtime logins must use distinct `flowcard_go_...` usernames. Their ownership
comments are bound to the deployment database and service. Existing users with
different ownership or administrative rights cause a refusal.

## Secret fields

Append new fields to the existing secret JSON objects, preserving the legacy
fields for explicit recovery. Values are populated out of band and never
managed by Terraform, GitHub artifacts, build arguments or browser variables.
Use random signing keys of at least 32 bytes, distinct across roles. Database
passwords must be printable ASCII and at least 24 bytes; URL-encode them.

`flowcard/db/app` requires these seven restricted URLs:

```text
go_gateway_url
go_onboarding_api_url
go_onboarding_worker_url
go_shortfall_url
go_underwriting_url
go_servicing_url
go_coordinator_url
```

All point at the same exact RDS host/database with `sslmode=verify-full` and
the container's RDS CA bundle. Runtime roles have no database/schema creation,
temporary-table, owner, superuser, replication or role-creation rights. The
gateway receives only its six allowlisted auth/binding/receipt tables. Owner
NOLOGIN groups grant the other services' schema permissions.

The app secret also contains distinct keys:

```text
go_shortfall_secret
go_underwriting_secret
go_coordinator_reader_secret
go_servicing_coordinator_secret
go_servicing_reader_secret
go_operator_secret
go_customer_secret
```

The existing raw `flowcard/service-auth` secret authenticates only the gateway
to onboarding. It must differ from these keys and from the native webhook key.

`flowcard/airwallex` retains its old fields and requires:

```text
client_id
api_key
account_id
shortfall_webhook_secret
servicing_webhook_secret
onboarding_webhook_secret
```

Each webhook key must match the explicitly reviewed subscription for its
exact URL. Neither an old root setting nor a new random value establishes
provider acceptance. Legacy account/RFI callback takeover remains a separate
credential review. Confirm those old URLs are disabled or explicitly migrated
before selecting the release's callback-cutover confirmation.

`flowcard/db/migrate` contains `go_stack_url` and `go_bootstrap_json`. On the
first release, the latter contains an invited operator's `operator_username`
and `operator_password`. The tool hashes it through the real auth provisioner;
it never creates a customer binding or financial consent. Once active
operators exist, `{}` is accepted and their credentials are not overwritten.

## Runtime and money readiness

First deployment leaves background workers and financial dispatch off.
An empty servicing schema can expose honest read/liveness routes with an
unsupported provider until its operator imports a verified facility. It gains
no provider identity, capital or financial authority. Missing business data
is unavailable, never a mock fallback.

The servicing private listener is an explicit SANDBOX opt-in behind the private
task security group. Its default local configuration remains loopback-only.
Internal service URLs remain localhost and preserve their existing TLS rules.

Before enabling workers, prepare their actual owner data, mapping and provider
contracts. Old queued onboarding work must be reviewed rather than silently
adopted. Money execution remains disabled independently even when workers are
selected. Fresh sandbox execution profiles, grants and case/actor bindings need
their own verified installation setup. The completed local sandbox book is
retained locally and is never uploaded implicitly.

## Release procedure

Ordinary push/PR CI tests all six Go modules using isolated PostgreSQL runners,
the product, release state machine, common image and owned-container smoke.
It has no OIDC permission. The smoke runs migrations twice, creates restricted
logins, starts all six actual APIs and verifies real secure-cookie login plus
an authenticated Go onboarding read. It removes only marked temporary data.

To build sealed artifacts without deployment:

```sh
gh workflow run aws-demo.yml --ref develop -f deploy=false
```

The registered workflow path is retained so dispatch from develop works
without updating the production `main` branch. Select deployment only after
the foundation, secrets, database and callback review are complete:

```sh
gh workflow run aws-demo.yml --ref develop \
  -f deploy=true -f confirm_callback_cutover=true -f enable_workers=false
```

The privileged job downloads the sealed artifacts, verifies source/architecture,
archive and frontend hashes, and then assumes the existing scoped OIDC role.
It publishes the exact common image to the legacy gateway/migration ECR slots.
Two one-off preflights pass before any writer is stopped. The controller then
quiesces all three old service slots, runs all six forward migrators, provisions
restricted logins and updates only the gateway slot to the new Go task.

Every selected API/container and all four ALB targets must be healthy before
native rules are activated. The Go-enabled original frontend is uploaded and
invalidated last. Public checks verify unauthenticated Go session reads and
invalid webhook signatures are rejected. These checks perform no provider
money operation and do not set `provider_acceptance_verified=true`.

## Failure and recovery

Each phase writes `go-deployed-release.json` under the release evidence
artifact. It contains exact previous service revisions/counts/attachments,
registered task definitions, deterministic one-off client tokens, task ARNs,
completed phases and previous native-rule conditions. It contains references,
not secret values. The artifact is uploaded even on failure.

A failure stops the sequence. It never automatically restarts an old financial
writer, reverses schema migrations, deletes a book or claims full release
success. Inspect the recorded tasks and actual database/service state before
choosing an explicit recovery action. Preserve unknown outcomes and request
identities; a new task or provider request is not a reconciliation substitute.

## AWS references

- [Fargate task networking](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-task-networking.html).
- [Multiple ECS target groups](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/register-multiple-targetgroups.html).
- [ELB IAM resource support](https://docs.aws.amazon.com/service-authorization/latest/reference/list_elbv2.html).
- [ECS IAM actions and cluster conditions](https://docs.aws.amazon.com/service-authorization/latest/reference/list_ecs.html).
