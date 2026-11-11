# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Stack

- Vite + React + TypeScript
- Tailwind CSS (via `@tailwindcss/vite`)
- Vitest for tests (`npm test`)
- oxlint for linting (`npm run lint`)

Commands:

```bash
npm run dev      # vite dev server
npm run build    # tsc -b && vite build
npm test         # vitest run
npm run lint     # oxlint
```

## Conventions

- **No router.** There is no react-router or equivalent. Switch between views with
  simple state in `src/App.tsx` (a view switch), not routes.
- **Feature folder.** Bridge Capital code lives in `src/features/bridge-capital/`.
- **No new dependencies without asking.** Do not add a package — runtime or dev — without
  checking first. Prefer the standard library, React, and what is already installed.
- TypeScript throughout; relative imports use explicit `.js` extensions (ESM, `"type": "module"`).

## Bridge Capital

- `src/features/bridge-capital/policy/` — the engine, pure functions over spec §3–§7.
  Every threshold lives in `policy.config.ts`; no policy function hard-codes one.
- `src/features/bridge-capital/data/` — `DataAdapter`, the in-memory `MockAdapter`,
  and the seeded generator behind the four demo customers.
- `src/policy/` is the older Kit 5 demo policy and is deliberately left alone: its
  tiers, fees and capital bands differ from the spec. Kit 5 keeps importing it;
  Bridge Capital never does.
- Print the demo customers' computed stats, tier and limit:

```bash
npm run bridge:stats
```

  It runs as a Vitest file because the repo's `.js` import specifiers need Vite's
  resolver; a standalone TS script would mean adding a runner dependency.

## Docs

- `docs/bridge-capital-spec.md` — Bridge Capital product spec (v1). Source of truth for
  tiers, limits, eligibility rules, pricing, capital regimes and approvals.
- `docs/EDITING.md` — editing notes for this repo.
