# Repository description

FlowCard is a bridge-financing underwriting platform.

## Guidance

- Keep credentials server-side; never expose them in browser code, fixtures, tracked files, or logs.
- Keep routine development and automated tests fixture-based.
- Add schema changes as new numbered SQL migrations, and follow the [development guide](docs/development.md) for database and server setup.

## Project structure

- Frontend ownership and editing guidance is in the
  [Frontend development](#frontend-development) section below.
- Server APIs and integrations live in `server/src/`
- tests live in `tests/`.
- SQL migrations live with their owning backend under
  `server/src/db/migrations/` or
  `services/company-onboarding/internal/postgres/migrations/`.

## Product conventions

- The app uses hash-based routes in `src/app-routing.ts` and view state in `src/App.tsx`; preserve direct links such as `#bridge-capital` and `#portal`.
- Bridge Capital policy is implemented as pure functions over its specification. Its policy configuration is the source for thresholds.
- TypeScript uses ESM with explicit `.js` extensions in relative imports.
- Do not add runtime or development dependencies without user approval.

## Frontend development

- **Stack:** Node.js 20.19+ or 22.12+, React 19, TypeScript 7, Vite 8, and
  Tailwind CSS 4. Tests use Vitest 5; lint uses Oxlint.
- **Official docs:**
  [Node.js](https://nodejs.org/learn/getting-started/introduction-to-nodejs),
  [React](https://react.dev/learn),
  [TypeScript](https://www.typescriptlang.org/docs/),
  [Vite](https://vite.dev/guide/),
  [Tailwind CSS](https://tailwindcss.com/docs),
  [Vitest](https://vitest.dev/guide/), and
  [Oxlint](https://oxc.rs/docs/guide/usage/linter).
- **Screens and routes:** `src/App.tsx` selects the landing, login, onboarding,
  back-office, or customer-portal screen. `src/app-routing.ts` maps
  `#bridge-capital` and `#portal`; the default route opens the landing page.
- **Screen ownership:** edit onboarding under `src/components/onboarding/`,
  back-office screens under `src/features/bridge-capital/backoffice/`, and
  customer-portal screens under `src/features/bridge-capital/portal/`.
  Back-office and portal navigation state stays within each feature.
- **Policy and data:** Bridge Capital policy and thresholds live in
  `src/features/bridge-capital/policy/`. Update focused tests under
  `tests/features/bridge-capital/` when changing policy. Demo adapters and
  seeded data live in `src/features/bridge-capital/data/`.
- **Styling and secrets:** use Tailwind classes in components and shared
  animations in `src/index.css`. Keep credentials and provider IDs on the
  server; never put them in browser code or `VITE_` variables.

## Backend development

- **Stack:** TypeScript/Node.js 20.19+ or 22.12+, Go 1.24+, and PostgreSQL 16+
  (the local Compose image uses PostgreSQL 17).
- **Official docs:** [Node.js](https://nodejs.org/learn/getting-started/introduction-to-nodejs),
  [TypeScript](https://www.typescriptlang.org/docs/), [Go](https://go.dev/doc/),
  [PostgreSQL](https://www.postgresql.org/docs/), and
  [Docker Compose](https://docs.docker.com/compose/).
- Keep credentials and provider IDs on the server. Clients must not supply
  trusted identifiers, policy decisions, or provider commands.
- Use fixtures and mocked providers for routine development and automated
  tests. Sandbox operations require verified mappings, API permissions, and
  webhook setup; see `docs/development.md` for acceptance gates.
- Apply backend schema migrations before startup. Runtime APIs and workers
  must not run DDL. Use each service's migration tooling and a privileged
  database connection.

## Database and migrations

- `DATABASE_URL` uses the Compose hostname `database`; host-side commands use
  `DATABASE_URL_HOST`.
- Add schema changes as new numbered migrations. Never delete, renumber, or
  rewrite an existing migration; add a new forward migration instead.
- Compose initializes migrations 001 through 003 only when PostgreSQL creates
  an empty volume. Apply later migrations explicitly before using dependent
  features.
- Do not delete the database volume as routine cleanup.

## Infrastructure

Local development runs in Docker Compose; the shared demo runs in AWS with
Terraform-managed infrastructure. Keep the two environments and their
configuration separate.

### Local environment

- The Vite frontend runs on the host. The root `compose.yaml` loads `.env` for
  PostgreSQL, the Node ledger, and the company-onboarding API and worker.
- Keep local environment variables in the root `.env`. Add only necessary
  variables, each with a short comment; ask before adding one that changes
  behavior. Dockerfiles and Compose must use `.env` as a file.
- Do not copy the ignored root `.env` into a worktree. The named PostgreSQL
  volume persists across Compose restarts; remove it only when deleting its
  data is intended.
- See `docs/development.md` for local startup, health checks, ports, and service
  commands.

### Demo environment

- AWS operations and runbooks live in `docs/infra/`. Terraform code, state, and
  plans stay under `infra/`.
- Use `develop` as the working branch. `main` is reserved for production.

## API specifications

- Specifications are generated from onboarding Go DTOs and operation metadata.
- Check freshness with
  `go -C services/company-onboarding run ./cmd/openapi -check`.
- After an intentional API contract change, regenerate with
  `go -C services/company-onboarding run ./cmd/openapi`.
- See the [API documentation](docs/api/README.md) for contracts and details.

## Testing limits

- Add at most three focused test cases for changed behavior.
- Reuse existing test frameworks and fixtures. Do not add test dependencies, infrastructure, or unrelated coverage.
- Do not test trivial getters, framework behavior, or unchanged code.
- If more than three cases appear necessary, explain why and ask first.
- Run independent routine checks concurrently where possible; do not use live services for fixture-based tests.

## Agent workflow

- At the start of every session, including delegated agents, read the relevant
  documents in [docs/designs/](docs/designs/) before planning or implementation.
  They record design decisions, technical rationale, and deferred questions.
  Check implementation against them and flag discrepancies before proposing
  changes to agreed behavior.
- Use brainstorming guidance for ideation, architecture, or product requirements.
- Prepare an implementation plan with the Planning Make skill when available.
- Use `agents-skills:rootie-mailbox-chat` for back-and-forth peer coordination
  with Rootie in the other agterm pane. It is for peer chat, not a one-shot task
  handoff or a read-only second opinion.
- For planned execution, follow Planning Exec in a new Codex session in this
  workspace via agterm. Pass `AGTERM_WORKSPACE_ID`, use the Rootie mailbox for
  lead communication, require the implementer's report in the lead pane plus
  notification, and independently review each task before advancing. Do not
  spawn subagents.
- Create new worktrees from `develop` under `.worktrees/<branch-name>`; never
  base one on another worktree. Do not copy the root `.env` into a worktree.
