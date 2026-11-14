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
- `npm run server:build` builds the Node server in `.dev/server/`.
- `npm run test:acceptance` runs PostgreSQL acceptance tests.

See [repository instructions](AGENTS.md) and
[AWS infrastructure runbooks](docs/infra/demo-operations.md).

Originally built by [@yidi-li-ydl](https://github.com/yidi-li-ydl) in [yidi-li-ydl/flowcard-treasury](https://github.com/yidi-li-ydl/flowcard-treasury). The original commit history is preserved.
