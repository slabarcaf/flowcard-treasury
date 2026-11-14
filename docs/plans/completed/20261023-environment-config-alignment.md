# Environment Configuration Alignment Plan

This plan aligns environment handling and Compose wiring with the updated
`AGENTS.md`. It uses one root `.env` for both services and makes sandbox-live
the only Compose configuration.

## Contents

- [Overview](#overview)
- [Context](#context)
- [Development approach](#development-approach)
- [Verification](#verification)
- [Solution overview](#solution-overview)
- [Technical details](#technical-details)
- [Implementation steps](#implementation-steps)
- [Post-completion](#post-completion)

## Overview

- Keep one local root `.env` as the source for environment values. Add a safe
  tracked `.env.example` with brief comments for clean setup.
- Use sandbox-live as the only Compose mode. Merge its settings into
  `compose.yaml` and remove the separate override file.
- Pass the full root `.env` to both the Postgres and ledger services through
  `env_file`.
- Protect the local `.env` in the repository `.gitignore`, update its comments
  without changing existing values, and align setup documentation.
- Do not change underwriting behavior, database schema, provider mappings, or
  deployment configuration in this iteration.
- Add no automated test files and run no application test suite for this
  configuration and documentation change.

## Context

- `compose.yaml` currently defines a local demo database and ledger.
  `compose.sandbox-live.yaml` overlays the sandbox-live settings.
- The root `.env` is ignored by the global Git config. The repository
  [`.gitignore`](/.gitignore) does not explicitly ignore it. Existing comments
  label groups rather than individual variables.
- The [Dockerfile](/Dockerfile) sets `NODE_ENV=production` directly. Move this
  setting to the root `.env`.
- The [README](/README.md) and [frontend development instructions](/AGENTS.md#frontend-development)
  describe current setup and need to document the single Compose mode.
- `.worktrees/` is already ignored. If implementation uses a worktree, create
  it from `develop` under `.worktrees/` and do not copy the root `.env` into it.
- Preserve the user-edited `AGENTS.md` content and include it in the eventual
  repository change set.

**Resolved database URL decision:** Keep `DATABASE_URL` pointed at the Compose
service hostname `database`. Add `DATABASE_URL_HOST` for host-side `psql` and
server commands that need a host-reachable address.

## Development approach

- **Testing approach:** Regular implementation followed by configuration and
  documentation checks.
- Complete each task before starting the next. Do not add automated tests or
  test files in this iteration.
- Use the same root `.env` for both Compose services. Do not create generated
  service env files or a second env source.
- Keep the existing `.env` values private. Add 2–5-word comments without
  changing or printing the values.
- Inventory variable names and consumers before adding entries. Ask Ramil
  before adding a new variable when its need or scope is unclear. The host URL
  variable is approved as described above.
- Use sandbox-live as the only Compose mode. Do not start containers or call
  external providers during verification.

## Verification

- Check that `.env` is ignored and `.env.example` remains trackable.
- Validate the single Compose configuration with `docker compose config -q`
  using safe example values. Do not start containers.
- Run the Markdown source checker on updated documentation.
- Do not run `npm test`, the PostgreSQL acceptance suite, lint, or builds for
  this config and documentation-only change.

## Solution overview

Use the root `.env` directly as the `env_file` for both Compose services. Merge
the sandbox-live settings into `compose.yaml` and remove
`compose.sandbox-live.yaml`. Remove per-variable `environment` maps and the
separate demo Compose configuration.

The Compose file remains the only launch mode. Existing fixture behavior in the
application and fixture-based tests remain unchanged; this plan adds no test
files and makes no provider calls.

## Technical details

- Create a tracked `.env.example` with safe placeholders, only required local
  and sandbox-live variables, and 2–5-word comments for each variable.
- Add `.env` and `.env.*` to `.gitignore`, with an exception for
  `.env.example`.
- Add the existing Postgres initialization settings, the Compose `DATABASE_URL`,
  and host-side `DATABASE_URL_HOST` to the root `.env`. Preserve existing values
  when editing the local file.
- Set `env_file: .env` for both database and ledger services. Pass the full
  file to both services, as requested.
- Move `NODE_ENV=production` from the Dockerfile into the root `.env`.
- Use `DATABASE_URL` for the Compose service hostname `database` and
  `DATABASE_URL_HOST` for host-side `psql` and server commands.
- Preserve the sandbox-live port and required settings in the single Compose
  file. Let existing server configuration continue to fail closed when
  required runtime credentials or mappings are unavailable.
- Do not create env preparation scripts, generated env files, automated test
  files, or new dependencies.

## Implementation steps

### Task 1: Protect and document the local env file

**Files:**
- Modify: `.gitignore`
- Create: `.env.example`
- Modify: local ignored `.env`

- [x] Inventory env variable names and consumers in app code, scripts, Compose,
      Dockerfile, and setup docs without printing local values.
- [x] Add ignore rules for `.env` and `.env.*`, with an exception for
      `.env.example`.
- [x] Create `.env.example` with safe placeholders, required settings, and
      2–5-word comments.
- [x] Add or update comments in the local `.env`, preserving all existing
      values and keeping the file untracked.
- [x] Confirm no unused variable is removed without checking its consumers.

### Task 2: Consolidate Compose to sandbox-live

**Files:**
- Modify: `compose.yaml`
- Delete: `compose.sandbox-live.yaml`
- Modify: `Dockerfile`
- Modify: `.env.example`

- [x] Merge the sandbox-live ports and service settings into `compose.yaml`.
- [x] Remove the demo Compose configuration and per-variable `environment`
      maps.
- [x] Set `env_file: .env` for both Postgres and ledger services.
- [x] Move `NODE_ENV=production` to the root `.env` and remove the Dockerfile
      `ENV` declaration.
- [x] Keep `DATABASE_URL` for the Compose service hostname `database` and use
      `DATABASE_URL_HOST` for host-side `psql` and server commands.
- [x] Confirm the existing sandbox-live server gates remain fail-closed.

### Task 3: Verify repository alignment

**Files:**
- Modify: `compose.yaml`
- Modify: `.gitignore`
- Modify: `.env.example`

- [x] Verify `.env` is ignored and `.env.example` is trackable.
- [x] Validate the single Compose configuration with safe example values.
- [x] Confirm no generated env files, test files, or new dependencies were
      added.
- [x] Run the Markdown source checker after documentation changes.
- [x] Update this plan as tasks are completed.

### Task 4: Update setup documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/EDITING.md`
- Modify: `.env.example`

- [x] Document creating and sourcing the one root `.env` file.
- [x] Document the sandbox-live Compose command and required settings.
- [x] Remove references to separate Compose modes and generated env files.
- [x] Keep examples free of actual credentials and provider identifiers.
- [x] Move this plan to `docs/plans/completed/` after all work is complete.

## Post-completion

- Keep the root `.env` local and untracked. Never copy it into a worktree or
  include its values in logs, fixtures, or documentation.
- Provider sandbox acceptance, deployment, and external configuration changes
  require a separate request.
