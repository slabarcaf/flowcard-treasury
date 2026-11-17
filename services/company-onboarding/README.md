# Company onboarding

This Go service owns company onboarding, durable provider webhook receipt and
worker processing. Runtime never applies schema migrations. API contracts are
in the [API guide](/docs/api/README.md).

The API supports `-webhook-only` for a narrow public callback listener, and
`cmd/legacy-webhooks` provides an audit-first, digest-bound historical receipt
transfer using the existing Go inbox. See the
[retirement runbook](/docs/demo/node-webhook-retirement.md) for exact commands,
required database-name checks, preserved history and live cutover gates.
