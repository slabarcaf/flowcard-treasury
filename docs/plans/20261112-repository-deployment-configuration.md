# Repository deployment configuration

Use repository Actions variables and manual dispatch from `develop` for the
single AWS demo. Keep runtime secrets in AWS Secrets Manager and temporary AWS
authentication through OIDC. Ramil approved removing the GitHub environment.

## Tasks

### Task 1: Update deployment source

- [x] Remove the workflow environment and its protection-attestation gates.
- [x] Retain foundation review, runtime approval, provenance, branch checks,
      release serialization, and least-privilege job permissions.
- [x] Match the repository's immutable OIDC subject, owner ID `207892885`,
      repository ID `1427370701`, and exact branch `refs/heads/develop`.
- [x] Update affected fixtures, checks, and current deployment documentation.
- [x] Run focused checks and independently review the final patch.

### Task 2: Configure GitHub and AWS

- [x] Set and read back repository Actions variables using `gh`.
- [x] Apply and verify the trust-only AWS change if authenticated access is
      available. Record a concrete pending command otherwise.
- [x] Record remaining release prerequisites without dispatching a deployment.

## Verified setup and remaining activation

The lead set and read back these repository Actions variables through `gh`:

| Variable | Value |
| --- | --- |
| `AWS_ACCOUNT_ID` | `968198283143` |
| `AWS_REGION` | `us-west-2` |
| `AWS_DEPLOY_ROLE_ARN` | `arn:aws:iam::968198283143:role/FlowCardGitHubDeployer` |
| `S3_FRONTEND_BUCKET` | `flowcard-frontend-968198283143-us-west-2` |
| `CLOUDFRONT_DISTRIBUTION_ID` | `ETTM257BBLQ3N` |

GitHub reports no environments and no published workflows. The repository uses
immutable OIDC subjects. The saved infrastructure plan confirms the deploy-role
ARN, bucket, and CloudFront distribution. Runtime secrets stay in AWS Secrets
Manager; no GitHub credential secrets were needed.

The implementation agent passed 74 focused workflow tests and 138 offline
safety checks. The lead reviewed the trust and workflow guards and independently
passed `tofu validate -no-color` using the cached provider. Formatting and diff
checks passed. The implementation agent's earlier provider-access limitation
was resolved by this independent validation.

Ramil approved AWS login. Login succeeded with temporary credentials for
`AccountFullAccessRole`; STS verified account `968198283143`. The lead inspected
the live role and its Terraform ownership tags, applied only its trust policy,
and verified exact JSON equality with the reviewed policy. The existing
`FlowCardGitHubDeployerPolicy` attachment is unchanged. CloudFront inspection
also confirmed distribution `ETTM257BBLQ3N` is deployed at
`d2dogzq0d9gxdl.cloudfront.net`.

The commands used for the completed trust update were:

```bash
aws iam update-assume-role-policy \
  --profile agentic-banking-hackathon \
  --role-name FlowCardGitHubDeployer \
  --policy-document file:///tmp/flowcard-repository-oidc-trust.json
aws iam get-role \
  --profile agentic-banking-hackathon \
  --role-name FlowCardGitHubDeployer \
  --query Role.AssumeRolePolicyDocument
```

The trust is managed by Terraform. The local Terraform change must be retained
alongside any matching live trust update. Do not apply an older saved plan.

The AWS worktree remains uncommitted and unpublished. Publish its reviewed
workflow and dependencies before attempting a release. GitHub also requires the
workflow file on the default branch (`main`) for manual dispatch; select
`develop` as the run ref. This requires a scoped publication decision and does
not authorize merging application changes into `main`.

The real foundation outputs artifact and its existing review/approval evidence
must also be supplied. No evidence was fabricated and no deployment was run.

See [GitHub manual workflow documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow)
for the default-branch requirement.

Use the existing AWS worktree. Preserve unrelated changes. Do not stage,
commit, push, deploy, read secret payloads, or change application data.
