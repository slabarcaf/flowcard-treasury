# Workspace cleanup before commit

Consolidate temporary output under ignored `.dev/`, shorten root documentation,
and keep build, release, test, and infrastructure documentation paths working.
Use the existing AWS worktree and preserve all deployment implementation.

## Tasks

### Task 1: Consolidate generated artifacts and documentation

- [x] Ignore `.dev/` and move `.release-metadata`, `dist`, and `.vitest` into it.
- [x] Update producers, consumers, Docker build paths, CI, checks, and links.
- [x] Move `infra/docs` to `docs/infra` and update relative paths and references.
- [x] Consolidate unique CLAUDE and AGENTS guidance in AGENTS, retaining a short
      Claude pointer and the main checkout's newer testing budget.
- [x] Keep README very concise and move necessary operational details to docs.
- [x] Run appropriate build, release-path, documentation, and focused checks.

### Task 2: Remove obsolete worktrees safely

- [x] Verify ancestry, clean status, ignored content, active processes, and
      runtime mounts for other worktrees before removing eligible checkouts.
- [x] Remove obsolete empty `worktrees/` and stale unregistered empty trees.
- [x] Preserve the main checkout, AWS worktree, and unique user files.

### Task 3: Review and commit

- [x] Review the complete deployment and cleanup changes and staged file list.
- [x] Exclude generated output, Terraform state/plans, caches, and credentials.
- [x] Commit the reviewed local work with meaningful scoped commits.
- [x] Report commits, checks, moved artifacts, and retained worktrees.

Do not push, merge into `develop` or `main`, deploy, or mutate cloud resources.

## Cleanup evidence

Both `feat/company-onboarding` and `feat/onboarding-openapi` were ancestors of
`develop` and the AWS branch. Their tracked and untracked source was clean;
ignored files were limited to generated `dist` and `node_modules` output. No
process had either worktree as its working directory. Docker was unavailable
because its local daemon socket was absent, so container mount inspection could
not run. No containers or volumes were changed.

Generated files were preserved under the main checkout's ignored
`.dev/retired-worktrees/` before normal `git worktree remove` and merged-branch
deletion. The legacy `worktrees/` and stale unregistered fixture tree contained
only empty directories and were removed. The main checkout and AWS worktree
remain; the main checkout's existing local source edits are preserved.

The final output layout is `.dev/dist/` for frontend assets and `.dev/server/`
for backend output. Both build orders preserve the other build's files, and the
frontend contains no backend code or fixtures. Release metadata and Vitest
reports are also ignored under `.dev/`. Infrastructure documentation lives in
`docs/infra/`; the README is 26 lines and the local setup guidance is in
`AGENTS.md`. `CLAUDE.md` imports the canonical `AGENTS.md`.

Verification passed: 141 focused Node tests, cross-language Go authentication,
138 offline safety checks, both build orders, focused lint, link checks, and the
staged whitespace check. Full lint still reports findings in unchanged Swagger
vendor files; the full Go HTTP suite requires unavailable local PostgreSQL.

The lead created separate local deployment and documentation commits after
reviewing the staged patch. SSH signing failed because the configured signing
agent socket was unavailable; command-scoped `commit.gpgsign=false` produced
unsigned local commits without changing Git configuration. Nothing was pushed,
merged, or deployed. The full changed-file inventory and verification report
are retained locally at `.dev/workspace-cleanup-report.md`.
