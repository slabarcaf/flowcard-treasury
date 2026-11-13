# Bootstrap Runbook

Run shell commands from the repository root. This runbook covers FlowCard's
Terraform state backend and foundational infrastructure in Oregon (`us-west-2`).

[TOC]

## Overview and architecture

FlowCard manages infrastructure state using an isolated two-phase lifecycle:

1. **Bootstrap phase (`infra/bootstrap/`)**:
   - Creates the versioned, encrypted private S3 remote state bucket and
     customer-managed KMS key.
   - Operates with a local backend (`terraform.tfstate`) during initial creation
     to avoid circular remote backend dependencies.
   - Enforces `prevent_destroy = true` on both the S3 bucket and KMS key to
     guard against accidental deletion.
   - Note: Native S3 state locking via `use_lockfile = true` applies to the
     remote S3 environment backend (`infra/environments/demo/`), whereas the
     initial bootstrap backend uses standard local state locking.
2. **Environment phase (`infra/environments/demo/`)**:
   - Uses a partial S3 backend configuration.
   - Connects to the bootstrap-created S3 state bucket and KMS key via
     `-backend-config` parameters generated from bootstrap outputs.
   - Manages foundational modules (ECR repositories, GitHub OIDC identity
     provider, CloudWatch log groups, and AWS Budget alerts).

Canonical commands in this runbook use `terraform` (the chosen configuration
engine). Local offline syntax and schema validation has been verified using
`tofu` (OpenTofu v1.13.9).

## Prerequisites and account verification

Before applying any Terraform configuration, verify local credentials and
account authorization:

1. **Named CLI Profile**: Ensure the dedicated human operator profile
   `flowcard-demo` is configured and active:
   ```bash
   aws sts get-caller-identity --profile flowcard-demo
   ```
   Confirm the returned `Account` matches your personal AWS account ID.
2. **Environment Binding**: Explicitly bind `AWS_PROFILE` so that all subsequent
   Terraform provider and backend operations run under the authorized identity:
   ```bash
   export AWS_PROFILE=flowcard-demo
   ```
   Note: Existing environment variables (`AWS_ACCESS_KEY_ID`,
   `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`) take precedence over
   `AWS_PROFILE`. Ensure these variables are unset or verify caller identity
   under the exact shell environment used to execute Terraform:
   ```bash
   aws sts get-caller-identity
   ```
3. **Account Guard**: Both `infra/bootstrap/` and `infra/environments/demo/`
   enforce a strict account guard (`allowed_account_ids`). This variable must
   contain your valid 12-digit account ID; the provider will refuse execution if
   invoked against an unlisted account.
4. **Authorization Reminder**: All `plan` and `apply` commands represent future,
   separately authorized operations. No cloud mutations may occur without
   explicit user sign-off.

## Phase 1: Provision bootstrap state storage

Execute the following steps to initialize and provision the S3 remote state
bucket and KMS key:

```bash
cd infra/bootstrap

# Explicitly bind active operator profile
export AWS_PROFILE=flowcard-demo

# Initialize with declared local backend (offline or local operation)
terraform init

# Review the execution plan with your authorized account ID and bucket name
terraform plan -var="allowed_account_ids=[\"123456789012\"]" \
               -var="state_bucket_name=flowcard-demo-terraform-state-123456789012" \
               -out=bootstrap.tfplan

# Future authorized action: apply the approved bootstrap plan
# terraform apply bootstrap.tfplan
```

### Local bootstrap state protection and optional migration

The initial run creates a local `terraform.tfstate` file in `infra/bootstrap/`.
Because this state file tracks the remote state bucket and KMS key, it must be
safeguarded:

- **Local backup**: Create an encrypted local backup copy of
  `infra/bootstrap/terraform.tfstate` and store it in a secure password manager
  or offline vault.
- **Git exclusion**: Git ignores all `*.tfstate` files via `infra/.gitignore`;
  never commit or push raw state files to source control.
- **Optional S3 Migration Procedure**: Once the remote state bucket is active,
  operators can migrate bootstrap state into S3:
  1. In `infra/bootstrap/main.tf`, replace `backend "local"` with:
     ```hcl
     backend "s3" {
       bucket       = "flowcard-demo-terraform-state-123456789012"
       key          = "bootstrap/terraform.tfstate"
       region       = "us-west-2"
       encrypt      = true
       use_lockfile = true
     }
     ```
     Notice that the key `bootstrap/terraform.tfstate` is strictly distinct from
     the demo environment state key (`environments/demo/terraform.tfstate`).
  2. Run `terraform init -migrate-state` to copy state into S3.

## Phase 2: Initialize demo environment with partial backend

The demo environment configuration uses partial backend configuration to avoid
hardcoding environment-specific bucket names or account numbers into Git:

1. **Generate backend configuration**: Create a backend configuration file
   (e.g., `backend-demo.hcl`) using outputs from Phase 1:
   ```hcl
   bucket     = "flowcard-demo-terraform-state-123456789012"
   kms_key_id = "arn:aws:kms:us-west-2:123456789012:key/xxxx-xxxx-xxxx"
   ```
2. **Initialize demo environment**:
   ```bash
   cd infra/environments/demo
   export AWS_PROFILE=flowcard-demo

   terraform init -backend-config=backend-demo.hcl
   ```
3. **Plan foundational resources**:
   ```bash
   terraform plan -var="allowed_account_ids=[\"123456789012\"]" \
                  -out=demo-foundation.tfplan
   ```

## Required IAM permissions for remote state access

When configuring CI/CD deployment roles or human operator permission sets, grant
the following scoped permissions for Terraform remote state operations:

### S3 bucket permissions

To prevent state corruption while enforcing least-privilege access, permissions
are strictly scoped to the demo state prefix:

- `s3:ListBucket`: Granted on `arn:aws:s3:::<state_bucket_name>` with a
  condition limiting `s3:prefix` to `environments/demo/`.
- `s3:GetObject` and `s3:PutObject`: Granted strictly on
  `arn:aws:s3:::<state_bucket_name>/environments/demo/terraform.tfstate`.
- `s3:GetObject`, `s3:PutObject`, and `s3:DeleteObject`: Granted strictly on
  the lockfile key
  `arn:aws:s3:::<state_bucket_name>/environments/demo/terraform.tfstate.tflock`.
  Note: `DeleteObject` is restricted exclusively to the `.tflock` object and is
  never granted on the state file or across `*`.
- Separate Bootstrap Key: If bootstrap state is migrated to S3, access to
  `bootstrap/terraform.tfstate` must be granted under a separate, explicitly
  scoped administrator policy.

### KMS key permissions

- `kms:GenerateDataKey`, `kms:Encrypt`, `kms:Decrypt`, `kms:DescribeKey` on
  `arn:aws:kms:us-west-2:<account_id>:key/<state_kms_key_id>`.

## GitHub Actions OIDC deployer role status

The OIDC module (`infra/modules/oidc/`) creates the `FlowCardGitHubDeployer`
role and establishes trust with GitHub Actions for repository
`slabarcaf/flowcard-treasury` and environment `demo`.

> [!IMPORTANT]
> Task 8 now defines a scoped release policy for the `FlowCardGitHubDeployer`
> role. The CloudFront statement is omitted when CloudFront is disabled; when
> enabled, distribution reads, invalidation creation, and invalidation status
> checks use the exact distribution ARN. This code does not prove that the role
> or its policy has been applied in AWS. See
> [`github-oidc.md`](github-oidc.md) for the current policy and
> [`demo-operations.md`](demo-operations.md) for live prerequisites.

## See also

- [Terraform S3 backend configuration](https://developer.hashicorp.com/terraform/language/backend/s3)
- [OpenTofu S3 state storage and native locking](https://opentofu.org/docs/language/settings/backends/s3/)
- [AWS IAM best practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html)
