# Airwallex Kit 5 Demo Flow Draft

This presenter flow uses the latest Kit 5 target scenario and the team's
October 22 alignment on Airwallex APIs. It separates tomorrow's fixture-based
video from the sandbox-backed flow required for the final submission.

[TOC]

## Goal and source of truth

The demo shows how FlowCard applies new evidence, fixed policy rules, scarce
capital, human review, and repayment to sequential card declines. The canonical
numbers and outcomes come from [the Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md).

The team's latest alignment is that the final submission must use real Airwallex
APIs. In the build, each **Next customer** action creates a sandbox transaction
that appears in the FlowCard dashboard and can be verified in Airwallex sandbox
records. The API path is needed for the build and final submission; tomorrow's
video may use the deterministic scenario fixtures.

## Recommended run modes

### Tomorrow's video

Use the deterministic **Kit 5 target** scenario. Keep the treasury view open,
the capital bar pinned, and the current presenter pauses for CRM re-evaluation,
auto-approval, and credit-officer approval.

### Final submission

Use sandbox-live mode. Each **Next customer** action creates a real sandbox
issuing transaction through the server. The response's provider ID, status, and
failure reason appear in the dashboard. An approved bridge then creates a
separate connected-account transfer through the bridge ledger. The browser
never receives Airwallex credentials.

In sandbox-live mode, the clicked transaction is the card attempt for that
customer. Policy-rejected and ineligible customers still have a real
card-attempt record, but receive no bridge transfer. Funded customers receive
separate transfers only after the applicable approval.

## Presenter sequence

1.  **Set the scene.** Open **Kit 5 target**. The facility is $50,000, the
    protected floor is $10,000, and $25,000 is already outstanding. The opening
    deployable amount is $15,000, in the Less band. The capital bar remains
    visible throughout.

2.  **Re-rate Bloom.** At 8:40, a Salesforce note says Bloom's largest client,
    about 65% of revenue, is disputing an invoice. The agent extracts three
    signals with quoted evidence. Bloom moves from Tier A with a $10,000 line to
    Tier C with a $4,000 line. At the 9:02 card decline, the
    $7,000 shortfall is refused because it exceeds the revised line and the
    Tier C door is closed. A $4,000 partial bridge would not clear the payment.
    Deployable capital stays at $15,000.

3.  **Fund Atlas automatically.** At 9:05, Atlas has a $5,000 shortfall. Tier A
    in the Less band is Auto. The sandbox card attempt is recorded, the policy
    approves the bridge, and the transfer settles. The card payment is retried
    after settlement. Deployable capital falls to $10,000.

4.  **Pause for Nova's approval.** At 9:11, Nova has a $6,000 shortfall. Tier B
    in the Less band is Human. After the credit officer approves the terms,
    Nova's transfer settles and the card payment is retried. Deployable capital
    falls to $4,000.

5.  **Show Pixel's history gate.** At 9:13, Pixel has a $2,000 shortfall but
    only 41 days of history against the 90-day minimum. Record the sandbox card
    attempt, then show that policy gives Pixel no line and sends no bridge
    transfer. Deployable capital stays at $4,000.

6.  **Refuse Juniper at the floor.** At 9:16, Juniper has the same $6,000
    shortfall and Tier B profile shape as Nova. The card attempt is recorded.
    The line check passes, but only $4,000 is available above the floor, so
    policy refuses the full request and sends no transfer.

7.  **Collect Nova's repayment on day +9.** An $8,000 settled deposit triggers
    collection. Charge the $27 fee first, then the $6,000 principal as separate
    charges. Total collection is capped at the triggering deposit. Returning
    principal brings deployable capital from $4,000 to $10,000; Atlas remains
    outstanding.

8.  **Close on the treasury view.** Summarize: Atlas funded automatically,
    Nova approved and repaid, Bloom declined on new evidence, Pixel ineligible,
    and Juniper refused at the floor. Show that the floor was never breached
    and that each decision and provider transaction is recorded.

## Airwallex API behavior

For sandbox-live mode, the server creates the card attempt through
`POST /api/v1/simulation/issuing/create`. Airwallex documents that the
simulation is sandbox-only and that the returned transactions can be retrieved
through its issuing transaction APIs. The
[transaction simulation guide](https://www.airwallex.com/docs/issuing/transactions/simulate-transactions-on-issued-cards)
documents a $9,999.99 maximum for a single simulated transaction.

An approved bridge uses
`POST /api/v1/connected_account_transfers/create`, owned by the existing
[Airwallex bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md).
The ledger keeps its current rules: reserve through the outbox, use stable
kebab-case operation IDs, and count a transfer as funded only when Airwallex
confirms settlement. See the
[Connected Account Transfers API](https://www.airwallex.com/docs/api/scale/connected_account_transfers).

The provider response is the source for the live transaction row. If the
provider returns an unexpected status or decline reason, the demo records and
shows that response and does not invent an insufficient-funds result or send a
bridge transfer.

## Build checks to resolve

Bloom's revised $9,000 payment is below the documented $9,999.99 simulation
limit. The current API reference's allowed `transaction_failure_reason` values
do not include `INSUFFICIENT_FUNDS`. The build must produce that result from
the connected account's actual USD funding state. Do not label a different
failure reason as insufficient funds.

The current build attaches Bloom's CRM note to the 9:02 decline flow, while the
canonical story places the note at 8:40. The live flow should surface the note
before the decline decision; the video narration can state the 8:40 arrival
while using the current pause.

## See also

- [Sandbox demo mappings and failure options](/docs/airwallex-sandbox-demo-prerequisites.md)
- [Airwallex bridge funding and collections requirements](/docs/requirements/Airwallex%20Hackathon_req_7.md)
- [Airwallex Developer MCP and Docs MCP setup](https://www.airwallex.com/docs/developer-tools/ai/developer-mcp)
- [Airwallex sandbox overview](https://www.airwallex.com/docs/developer-tools/sandbox-environment)
- [Airwallex AgentOS scope and setup](https://www.airwallex.com/docs/developer-tools/ai/agentos)
