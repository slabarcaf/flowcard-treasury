# Repository guidance

FlowCard is a bridge-financing underwriting platform. Keep credentials server-side; never expose them in browser code, fixtures, tracked files, or logs. Do not copy the ignored root `.env` into worktrees. Keep routine development and automated tests fixture-based. Add schema changes as new numbered SQL migrations, and follow the [development guide](docs/development.md) for database and server setup.

## Project structure

- `src/kit5/` contains the default Kit 5 demo; `src/gd/` preserves the GD original.
- Shared UI lives in `src/components/`; changes can affect both demos.
- `src/policy/` contains frontend demo policy; `shared/policy/` contains rules shared with the server.
- Server APIs and integrations live in `server/src/`; tests live in `tests/`.
- SQL migrations live in `server/src/db/migrations/`.
- `src/features/bridge-capital/` contains Bridge Capital policy, adapters, and generated demo data. Its policy configuration is the source for thresholds; the older Kit 5 policy remains separate.
- Read [editing guidance](docs/EDITING.md) before frontend changes.

## Development commands

Use Node 20.19+ or 22.12+ and npm. Install dependencies with `npm install`.

- `npm run dev` starts Vite at `http://localhost:5173`.
- `npm test` runs Vitest; `npm run lint` runs oxlint.
- `npm run build` type-checks and builds frontend assets under `.dev/dist/`.
- `npm run server:build` compiles server output under `.dev/server/`; `npm run server:start` builds and starts the server.
- `npm run test:acceptance` runs PostgreSQL policy acceptance tests. Set `POLICY_ACCEPTANCE_DATABASE_URL` to a local test database whose name includes `test`.
- `npm run preview` serves the frontend production build locally.
- `npm run bridge:stats` prints Bridge Capital demo customer statistics through Vitest's resolver.

## Product conventions

- The app uses hash-based routes in `src/app-routing.ts` and view state in `src/App.tsx`; preserve direct links such as `#bridge-capital`, `#portal`, and `#kit5`.
- Bridge Capital policy is implemented as pure functions over its specification. `src/features/bridge-capital/data/` contains the `DataAdapter`, in-memory `MockAdapter`, and seeded generator.
- `src/policy/` is the older Kit 5 policy with deliberately different tiers, fees, and capital bands. Kit 5 continues to use it; Bridge Capital does not.
- TypeScript uses ESM with explicit `.js` extensions in relative imports.
- Do not add runtime or development dependencies without user approval.

## Testing limits

- Add at most three focused test cases for changed behavior.
- Reuse existing test frameworks and fixtures. Do not add test dependencies, infrastructure, or unrelated coverage.
- Do not test trivial getters, framework behavior, or unchanged code.
- If more than three cases appear necessary, explain why and ask first.
- Run independent routine checks concurrently where possible; do not use live services for fixture-based tests.

## Infrastructure and environment

- AWS configuration and operations are documented in `docs/infra/`; infrastructure code, Terraform state, and plans remain under `infra/`.
- Keep environment variables in the root `.env`, sourced when needed. Dockerfiles and Compose files must use `.env` as a file rather than listing variables individually.
- Add only necessary environment variables; ask if a new variable is needed to control behavior. In `.env`, add a brief two-to-five-word comment for each variable.
- Every new worktree must be created from `develop`, never from another worktree, and stored under `.worktrees/<branch-name>`. `main` is production-ready.

## Agent workflow

- Use brainstorming guidance for ideation, architecture, or product requirements.
- Prepare an implementation plan with the Planning Make skill when available.
- For planned execution, follow the Planning Exec workflow, with the project-specific adjustment that implementation happens in a new Codex session in agterm in the same workspace using `AGTERM_WORKSPACE_ID`, never a spawned subagent. Keep communication with the lead session through the requested agterm handoff.
