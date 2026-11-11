# AGENTS.md

This repository contains FlowCard, a bridge-financing underwriting platform.

## Project structure

- `src/kit5/` contains the default Kit 5 demo. `src/gd/` preserves the GD
  original. Shared UI components live in `src/components/` and can affect both.
- `src/policy/` contains frontend demo policy. `shared/policy/` contains rules
  shared with the server. Server APIs and integrations live in `server/src/`.
- Tests live in `tests/`. SQL migrations live in
  `server/src/db/migrations/`.
- Read [`docs/EDITING.md`](/docs/EDITING.md) before frontend changes and
  [`README.md`](/README.md) for database, server, and integration setup.

## Development commands

- Use Node 20.19+ or 22.12+ and npm. Install dependencies with `npm install`.
- Start Vite with `npm run dev`. It serves the frontend at
  `http://localhost:5173`.
- Run `npm test` for Vitest, `npm run lint` for Oxlint, and `npm run build` for
  TypeScript checks plus a Vite production build in `dist/`.
- Run `npm run server:build` to compile the server. `npm run server:start`
  builds and starts it. Apply migrations separately as described in the README.
- `npm run test:acceptance` runs PostgreSQL policy acceptance tests. Set
  `POLICY_ACCEPTANCE_DATABASE_URL` to a local test database whose name includes
  `test`.
- Use `npm run preview` to preview the production frontend build locally.

## Change and data boundaries

- Keep credentials server-side. Never expose them in browser code, fixtures,
  tracked files, or logs. Do not copy the ignored root `.env` into worktrees
- Keep routine development and automated tests fixture-based. Run tests concurrently where it's possible to accelerate execution time
- Add schema changes as new numbered SQL migrations. Follow the README when
  applying migrations or configuring the local server.
- Use $brainstorming skill (if available) to help user ideate some ideas, design architecture or refine product requirements
- Always use $[Planning: Make](/Users/ramil/.codex/plugins/cache/umputun-cc-thingz/planning/3.10.3/commands/make.md) skill (if available) to prepare an implementation plan. Make skill isn't exposed via this plugin, so you'd need to check it manually in the skill files.
- Always use $Planning: Exec skill (if available) to execute on the prepared plan with one adjustment — never spawn subagents. Instead of that I want you to use $agterm skill (if available) and launch a new codex in agterm session in the same workspace (use AGTERM_WORKSPACE_ID) and work with them as a subagent. You need to pormpt this session so they use the same way of communication with you.
- Every new worktree should be created from `develop` branch. It's our working branch and `main` is a production ready version of the solution. Never create a worktree from other worktrees. Keep all the worktrees in the `.worktrees/<branch-name>` folder
- All the environment varaibles should be kept in `.env` file which will be sourced if needed. For dockerfiles or compose files never map environment variables one-by-one, reference `.env` file with all necessary variables in place.
- In the `.env` file keep brief comments (2-5 words) about what each variable configures. Don't create unnecessary variables. If not sure if there should be a new variable to control specific behavior of the solution, ask user.
