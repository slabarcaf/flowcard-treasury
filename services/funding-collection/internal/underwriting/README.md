# Funding underwriting adapter

This private adapter loads and calls only the committed public policy package.
It does not copy policy rules or determine policy outcomes independently.

## Immutable dependency pin

- Module: `github.com/slabarcaf/flowcard-treasury/services/underwriting-engine`
- Dependency commit: `d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766`
- Policy ID: `bridge-capital-go-v1`
- Embedded policy SHA-256:
  `0a0cf42f72e101a458c260be8fbca315704d9a093264049acbf366f8cd4d38f0`

The funding module uses a local `replace` to `../underwriting-engine`, which
must remain the committed public module in the same repository checkout.

## Pricing binding

The funding adapter copies `Config.Pricing()`, `PolicyID()`, and `PolicyHash()`
from the same loaded public configuration. It maps only those scalar fields
into the funding domain pricing value. There is no fallback pricing catalogue
or evaluator-output probing.

## Worker recheck handoff

`ProjectNeverSentReservation` removes only an identified reservation proven
`NEVER_SENT` with its FUND operation still `QUEUED`: its facility principal,
customer principal/fee reservation, exact invoice allocations, unfunded
placeholder, and matching payment duplicate. It preserves every other debt,
reservation, invoice encumbrance, and payment obligation. The public evaluator
adds the full current candidate once through its shortfall input. The worker
must construct and verify the locked placeholder and allocation identities
before calling this pure projection, then call the funding repository's source
liquidity projection to subtract only that operation's proven local hold.
