# FlowCard Treasury

Bridge financing underwriting demo and platform.

**Live demo:** <https://d2dogzq0d9gxdl.cloudfront.net>

## Quick start

Requires Node 20.19+ or 22.12+.

```sh
npm install
npm run dev
```

## Key commands

- `npm test` runs Vitest.
- `npm run lint` runs oxlint.
- `npm run build` type-checks and builds the frontend in `.dev/dist/`.
- `npm run server:build` cleans and builds the Node compatibility API in
  `.dev/server/`.
- `go -C services/funding-collection test -p 1 ./...` runs the Go financial
  tests with an explicitly configured owned local test database.

Node funding, approval and repayment execution is retired. Node continues to
serve onboarding proxies, operator sessions, snapshots and historical ledger
reads. See the [retirement inventory](/docs/demo/node-funding-retirement.md)
and [Go Funding runbook](/docs/demo/funding-and-collection-local.md).

See [repository instructions](AGENTS.md) and
[AWS infrastructure runbooks](docs/infra/demo-operations.md).

Originally built by [@yidi-li-ydl](https://github.com/yidi-li-ydl) in [yidi-li-ydl/flowcard-treasury](https://github.com/yidi-li-ydl/flowcard-treasury). The original commit history is preserved.
