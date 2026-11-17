# Retire Node snapshot and evidence pipelines

Scope follows [the retirement design](/docs/designs/node-readmodel-retirement.md).
Implementation is owned directly by the addressed agent in the isolated
`chore/retire-node-readmodels` worktree, based on develop `8662dd1`.

[TOC]

- [x] Inventory runtime startup, browser imports and retained mapping tooling.
- [x] Verify focused baseline checks (56 passed).
- [x] Move frontend-only fixtures without changing their data (hashes match).
- [x] Remove legacy workers, publishers, evidence CLI and exclusive tests.
- [x] Return explicit snapshot retirement and remove startup/config wiring.
- [x] Verify builds and retained boundary tests; preserve migrations and seeds
  (167 passed; one shared-DB test deliberately excluded).
- [x] Record exact results and remaining Node responsibilities for review in
  [the verification note](/docs/demo/node-readmodel-retirement.md).
- [x] Integrate the tested source into develop under Ramil's authorization:
  source `c6d4831`, merge `7d2bba4`. Post-merge builds and retained suite passed
  (167 tests; one shared-DB test excluded). The initial docs-proxy timing
  failure and successful bounded rerun are recorded in the verification note.

Local integration is complete. No process restart, database mutation,
deployment or real provider/model call was performed.
