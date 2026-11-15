# Go browser gateway integration

Ramil authorized local integration of both remaining worktrees on November
15, 2026. Source was committed separately and merged normally into develop.

[TOC]

## Commits

| Delivery | Source commit | Develop merge |
| --- | --- | --- |
| Legacy presenter/client cleanup | `3877772` | `9e3ec34` |
| Go login, operator onboarding and financial reads | `249024d` | `6a499b6` |

The previous Node read-model retirement was already merged at `7d2bba4` with
closeout `569ced2`. It was not duplicated.

## Combined verification

- 251 TypeScript tests in 29 files passed. The existing shared-PostgreSQL
  forwarding concurrency test was deliberately excluded.
- Gateway race suite: 15 top-level tests and 18 HTTP subtests passed, no
  skipped scenarios. These used marker-owned disposable PostgreSQL targets,
  actual production onboarding/Coordinator/Funding read handlers and local
  HTTP. Financial source rows were synthetic; no financial worker/provider
  operation was started.
- Gateway Go build/vet passed. Node build, default frontend build and opt-in
  Go frontend build all passed on the combined develop tree.
- Private reports and browser artifacts from the original A-C walkthroughs
  remain in the browser worktree. Integration did not repeat browser automation.

Logs: `/tmp/flowcard-combined-ts-tests.log`,
`/tmp/flowcard-combined-go-tests.jsonl`,
`/tmp/flowcard-combined-server-build.log`,
`/tmp/flowcard-combined-frontend-build.log` and
`/tmp/flowcard-combined-go-ui-build.log`.

## Runtime and ownership limits

The Go workspace is opt-in through `VITE_BROWSER_GATEWAY_ENABLED=true` and
`vite.browser-gateway.config.ts`. No existing service, callback, root `.env`
or persistent user record was changed or restarted by this merge.

Operator login grants onboarding and exact authenticated financial reads.
Customers remain unlinked. Trusted financial-account bindings, customer
acceptance, operator approval and financial writes remain Task D. Direct
onboarding provider-signature intake and callback cutover remain open too.

Main plan/design overlaps were backed up under `.dev/merge-20261115/` and
reconciled without losing newer webhook or retirement notes. Unrelated audit,
backlog and infrastructure changes remain outside these commits.
No push, deployment or real provider/model request was performed.
