# Deposit timestamp corrections

Ramil approved ignoring timestamp equality when a deposit's identity and
economic facts still match. Provider settlement timestamps can change between
canonical reads. The original deposit row and every provider observation remain
immutable evidence of what was first observed.

## Normal processing

Webhook deduplication, collection planning and the final dispatch check compare:

- Provider deposit ID, account and customer.
- Currency, gross amount, net amount and provider fee.
- Settled status and external-deposit kind.

Timestamp equality is excluded from these comparisons. A valid settlement time
is still required. Both the original and current settlement time must follow
funding before a deposit can authorize collection. Existing future-time and
observation-freshness checks remain in effect.

A distinct delivery of the same deposit records its new observation and
creates no second deposit or collection budget. Changed monetary or identity
facts still place collection on hold.

## Recover a previously held sandbox deposit

The privileged maintenance command recovers one timestamp-only hold in an
explicitly owned sandbox installation. It uses a canonical provider GET and
the normal repository locks. It sends no transfer, changes no debt or amount,
and leaves the original settlement time and observation reference unchanged.
The ordinary worker must subsequently pass its normal authority, balance,
capacity, mapping and settlement checks.

Run from the repository root:

```sh
go -C services/funding-collection run ./cmd/sandbox recheck-held-deposit \
  --environment SANDBOX \
  --env-file "$PWD/.env" \
  --manifest /absolute/owned/run/manifest.json \
  --run-id fc-sbx-owned-run \
  --test-admin-url-file /absolute/private/local-test-admin-url.txt \
  --database-name flowcard_funding_demo_sandbox_owned_run
```

The command verifies the saved owner, database name and database marker before
applying forward migrations. It accepts exactly one held deposit and active
funded bridge belonging to the saved customer and facility. All identifiers
and values come from that installation, not browser inputs. Provider execution
contract validation and the fixed sandbox host still apply.

Recovery requires matching current identity and monetary facts, a current
verified mapping, a recorded source-conflict hold, no other bridge hold or
held deposit, and no unresolved financial operation. Every historical deposit
observation must also agree on identity, status and monetary facts. A past
amount conflict cannot be cleared merely because a later GET reverts.

Migration 016 adds an append-only maintenance receipt referencing both
observations. The runtime role cannot insert these receipts. The database
permits `HELD` to `SETTLED` only when a matching privileged receipt was inserted
in that same transaction. An old receipt cannot release a later hold.
Successful maintenance removes only the verified deposit-source hold and
publishes versioned servicing changes. No financial operation is created by
the recheck itself.

The private run directory retains `deposit-timestamp-recheck.json`; PostgreSQL
retains `servicing.deposit_timestamp_rechecks` and the original observations.
An ambiguous candidate, economic mismatch or unrelated hold is a refusal,
requiring investigation rather than another deposit or direct database edits.
