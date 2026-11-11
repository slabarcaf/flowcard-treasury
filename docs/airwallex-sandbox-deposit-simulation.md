# Airwallex Sandbox Deposit Simulation

Use these requests to seed the five Kit 5 connected accounts with settled
Sandbox deposits. The endpoint simulates an incoming deposit to a Global
Account; it does not move production funds. Airwallex documents the endpoint as
Sandbox-only and supports requests on behalf of connected accounts with the
`x-on-behalf-of` header. See the [Sandbox deposit guide][sandbox-guide] and
[deposit API reference][deposit-api].

## Opening balance targets

The amounts below are the planned USD balances before the demo card attempts.
The commands assume each account's available USD balance is still zero. Check
balances before running them; if a balance has changed, deposit only the
remaining amount needed to reach its target.

| Customer | Target USD balance | Connected account ID | Global Account ID |
| --- | ---: | --- | --- |
| Bloom & Co. | $2,000 | `acct_VOPv8QJ-NkOu6oAY80jrGg` | `b5d52e2a-b9a4-4ee1-ad35-08bcc98e04cb` |
| Atlas SaaS | $1,500 | `acct_xLk-Tk-fPvu-fgmIH2nP-w` | `f81df0c3-794d-4279-af12-68103803df2e` |
| Nova Agency | $1,000 | `acct_z1y8m_PhM1m8Macuhqwjiw` | `616c27fd-c6a0-43f7-adb2-886e28bafba0` |
| Pixel Studio | $500 | `acct_wCpw7fuvNVajOupVbuqp6Q` | `16db2b73-88ec-47a9-afd0-df07260c1e1e` |
| Juniper Labs | $1,000 | `acct_wiKpQavfP2-DcDVd20hzfg` | `69ef7ed8-bf93-4487-a85f-5ae89f16623c` |

These targets match the [Kit 5 scenario data](/src/kit5/scenarios.ts).

## Prepare the shell

Run from the repository root. This loads the current token from the ignored
`.env` file without putting its value in the document or command history.

```shell
set -a
source ./.env
set +a
: "${AIRWALLEX_TOKEN:?Refresh AIRWALLEX_TOKEN in .env first}"
```

## Create the settled deposits

These requests were executed on 2026-10-22. Keep them as a template for a
future fresh demo run. Running them again creates additional deposit records;
check balances and change the amounts to the remaining target before reuse.
The response includes the deposit ID, status, currency, and fee amount.

```shell
# Bloom: $2,000
curl --silent --show-error --fail-with-body \
  --request POST \
  --url 'https://api.sandbox.airwallex.com/api/v1/simulation/deposit/create' \
  --header "Authorization: Bearer ${AIRWALLEX_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'x-on-behalf-of: acct_VOPv8QJ-NkOu6oAY80jrGg' \
  --data-raw '{"amount":2000,"global_account_id":"b5d52e2a-b9a4-4ee1-ad35-08bcc98e04cb","reference":"FlowCard opening balance - Bloom","status":"SETTLED"}'

# Atlas: $1,500
curl --silent --show-error --fail-with-body \
  --request POST \
  --url 'https://api.sandbox.airwallex.com/api/v1/simulation/deposit/create' \
  --header "Authorization: Bearer ${AIRWALLEX_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'x-on-behalf-of: acct_xLk-Tk-fPvu-fgmIH2nP-w' \
  --data-raw '{"amount":1500,"global_account_id":"f81df0c3-794d-4279-af12-68103803df2e","reference":"FlowCard opening balance - Atlas","status":"SETTLED"}'

# Nova: $1,000
curl --silent --show-error --fail-with-body \
  --request POST \
  --url 'https://api.sandbox.airwallex.com/api/v1/simulation/deposit/create' \
  --header "Authorization: Bearer ${AIRWALLEX_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'x-on-behalf-of: acct_z1y8m_PhM1m8Macuhqwjiw' \
  --data-raw '{"amount":1000,"global_account_id":"616c27fd-c6a0-43f7-adb2-886e28bafba0","reference":"FlowCard opening balance - Nova","status":"SETTLED"}'

# Pixel: $500
curl --silent --show-error --fail-with-body \
  --request POST \
  --url 'https://api.sandbox.airwallex.com/api/v1/simulation/deposit/create' \
  --header "Authorization: Bearer ${AIRWALLEX_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'x-on-behalf-of: acct_wCpw7fuvNVajOupVbuqp6Q' \
  --data-raw '{"amount":500,"global_account_id":"16db2b73-88ec-47a9-afd0-df07260c1e1e","reference":"FlowCard opening balance - Pixel","status":"SETTLED"}'

# Juniper: $1,000
curl --silent --show-error --fail-with-body \
  --request POST \
  --url 'https://api.sandbox.airwallex.com/api/v1/simulation/deposit/create' \
  --header "Authorization: Bearer ${AIRWALLEX_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'x-on-behalf-of: acct_wiKpQavfP2-DcDVd20hzfg' \
  --data-raw '{"amount":1000,"global_account_id":"69ef7ed8-bf93-4487-a85f-5ae89f16623c","reference":"FlowCard opening balance - Juniper","status":"SETTLED"}'
```

## Verify balances and avoid duplicate deposits

After each deposit, check the response's `status`, `currency`, and `fee_amount`,
then read the connected account's current balance with
`GET /api/v1/balances/current` and the same `x-on-behalf-of` account ID. The
available balance can differ from the deposit amount if a fee applies.

Each successful POST creates a deposit record. If a request times out or its
result is unclear, check Global Account activity and the connected account's
balance before retrying.

## Verified execution record

On 2026-10-22, Airwallex showed one USD `CREDIT` transaction per Global
Account. All five transactions were `SETTLED`; `fee_amount` was null in the
transaction responses.

| Customer | Amount | Status | Deposit ID |
| --- | ---: | --- | --- |
| Bloom & Co. | $2,000 | `SETTLED` | `ea50a99b-23dd-46cf-be83-134dc3356332` |
| Atlas SaaS | $1,500 | `SETTLED` | `ee66a5fc-3cac-4898-bdb8-46dbb365f79d` |
| Nova Agency | $1,000 | `SETTLED` | `d8462de3-6999-4a6c-b7d1-99b70723c78b` |
| Pixel Studio | $500 | `SETTLED` | `f6baa9e7-c603-419b-bd68-d8c8ad767cc9` |
| Juniper Labs | $1,000 | `SETTLED` | `fb08e2d2-caa4-460e-9738-91caece103c3` |

The available USD balances were also read back and matched the target balances
in the opening-balance table. The Nova transaction is present on the active
Nova Global Account ID shown above.

[sandbox-guide]: https://www.airwallex.com/docs/developer-tools/sandbox-environment/deposits/simulate-deposits-to-your-global-account
[deposit-api]: https://www.airwallex.com/docs/api/simulation/deposits/create
