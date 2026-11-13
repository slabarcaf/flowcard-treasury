# Account Access

This guide describes human operator and automated continuous deployment access
architecture for Amazon Web Services (AWS) and GitHub for the FlowCard
demonstration environment. It outlines the planned identity architecture, local
named profile configuration, role separation, and verification procedures.

[TOC]

## Current environment inventory

A local environment inspection conducted on the development machine verified
the following tool availability and authentication states:

- **AWS CLI version**: `aws-cli/2.39.7 Python/3.15.2 Darwin/25.5.0 source/arm64`
  is installed at `/opt/homebrew/bin/aws`.
- **AWS CLI profiles**: Running `aws configure list-profiles` reported only a
  single legacy profile (`twig.ai`). Testing Security Token Service (STS)
  caller identity on that profile returned `InvalidClientTokenId` (code 254),
  confirming the token is invalid or expired. No FlowCard AWS CLI profile is
  configured locally.
- **AWS account status**: The personal AWS account is currently accessed via
  root user login in the AWS Management Console. Account-level IAM users, IAM
  Identity Center enrollment, AWS Organizations configuration, and MFA status
  remain **unverified** pending authenticated account inspection. No account
  mutations have been performed.
- **GitHub CLI version**: `gh version 2.103.3 (2026-10-26)` is installed.
- **GitHub authentication**: Authenticated as `alewkinr` with scopes
  `admin:public_key`, `gist`, `read:org`, and `repo`.
- **GitHub repository**: `slabarcaf/flowcard-treasury` is a public repository
  owned by `slabarcaf`. The authenticated user `alewkinr` holds `WRITE`
  permission (`admin: false`, `maintain: false`).
- **Target AWS region**: Oregon (`us-west-2`).

## Root account security and prerequisites

AWS security best practices recommend avoiding root credentials for routine
administrative operations and automated releases. For FlowCard, the approved
design uses a dedicated human deployment identity with temporary STS sessions.

The account owner must inspect and perform the following administrative
prerequisites directly within the AWS Management Console:

1. **Verify or enable root multi-factor authentication (MFA)**:
   - Navigate to **IAM** -> **Security credentials** -> **Multi-factor
     authentication (MFA)**.
   - Register a FIDO2 hardware security key (e.g., YubiKey) or a virtual
     authenticator application if not already configured.
2. **Verify root access keys**:
   - Inspect **Root user access keys**. If any exist, delete them. Never create
     or download root access keys.
3. **Activate IAM access to billing information**:
   - While signed in as root, open the account menu and select **Account**.
   - Scroll to **IAM User and Role Access to Billing Information**, click
     **Edit**, select **Activate IAM Access**, and click **Update**.
   - This setting allows designated deployment roles to configure budget alerts
     and monitor costs during Task 2 and Task 3.

## IAM Identity Center setup

AWS IAM Identity Center generates short-lived, rotatable temporary STS
credentials for human operator access, eliminating long-lived access keys.

> [!IMPORTANT]
> An **Organization instance** of IAM Identity Center is required for AWS
> account access and CLI SSO integration (`aws configure sso`). An account
> instance of Identity Center manages access only for internal applications and
> does not support multi-account or AWS account CLI access.
>
> Enabling an organization instance requires AWS Organizations all-features
> mode. Creating an AWS Organization changes account governance and must only be
> initiated with Ramil's explicit authorization. No Organization or Identity
> Center instance has been created yet.

### Planned console setup procedure

When authorized by Ramil, the console setup proceeds as follows:

1. Open the **IAM Identity Center** console in Oregon (`us-west-2`) or primary
   management region.
2. Select **Enable** with AWS Organizations (all-features mode).
3. Under **Settings**, record the **AWS access portal URL**
   (`https://<subdomain>.awsapps.com/start`) and the Identity Center region.
4. Under **Users**, create the human operator user (e.g., username `alewkinr` or
   operator handle) with email verification.
5. Under **Permission sets**, define a deployment permission set:
   - For initial bootstrap, choose between a scoped bootstrap policy or
     temporary `AdministratorAccess` (to be chosen explicitly before Task 3).
   - Set maximum session duration (e.g., 8 hours).
   - Name the permission set `FlowCardDeployer`.
6. Under **AWS accounts**, assign the personal AWS account to the user with the
   `FlowCardDeployer` permission set.

## Local AWS CLI profile configuration

Once IAM Identity Center is enabled and assigned, configure a dedicated named
profile `flowcard-demo` using AWS CLI v2 SSO token providers.

### Automatic configuration command

Run the interactive AWS CLI SSO configuration wizard:

```bash
aws configure sso --profile flowcard-demo
```

When prompted, provide:

- **SSO session name**: `flowcard-session`
- **SSO start URL**: `https://<subdomain>.awsapps.com/start`
- **SSO region**: `us-west-2`
- **SSO registration scopes**: `sso:account:access`

The AWS CLI initiates browser authorization (using PKCE authorization code grant
or device code depending on CLI capabilities). After approving access in the
browser, select the account and the `FlowCardDeployer` role. Set the default
client Region to `us-west-2`, output to `json`, and profile to `flowcard-demo`.

### Manual configuration reference

Alternatively, append the following block directly to `~/.aws/config`:

```ini
[profile flowcard-demo]
sso_session = flowcard-session
sso_account_id = <AWS_ACCOUNT_ID>
sso_role_name = FlowCardDeployer
region = us-west-2
output = json

[sso-session flowcard-session]
sso_start_url = https://<subdomain>.awsapps.com/start
sso_region = us-west-2
sso_registration_scopes = sso:account:access
```

### Authentication and verification

Authenticate the local session by running:

```bash
aws sso login --profile flowcard-demo
```

Verify authentication safely with STS without outputting sensitive keys,
tokens, or environment variables:

```bash
aws sts get-caller-identity --profile flowcard-demo
```

Expected JSON response structure:

```json
{
    "UserId": "AROAXXXXXXXXXXXXXXXXX:username",
    "Account": "123456789012",
    "Arn": "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_FlowCardDeployer_.../username"
}
```

## Role separation and permissions boundaries

FlowCard separates permissions across distinct identity and runtime contexts
rather than using shared credentials:

1. **Human Bootstrap & Operator Role** (`FlowCardDeployer`):
   - Scope: Temporary STS session created through IAM Identity Center.
   - Purpose: Initial S3 backend state bucket creation (native S3 locking), KMS
     keys, GitHub OIDC provider, budget alerts, and emergency maintenance.
   - Privilege selection: Scoped bootstrap vs. temporary admin to be explicitly
     chosen before Task 3.
2. **GitHub Actions Deployment Role** (`FlowCardGitHubDeployer`):
   - Scope: IAM role assumed exclusively via GitHub OpenID Connect (OIDC) token
     exchange without stored long-lived access keys.
   - Trust policy constraints:
     - `token.actions.githubusercontent.com:aud`: `sts.amazonaws.com`
     - `token.actions.githubusercontent.com:sub`:
       `repo:slabarcaf@207892885/flowcard-treasury@1427370701:ref:refs/heads/develop`
   - The workflow also requires manual dispatch, explicit confirmation, and
     the exact repository and `develop` ref.
   - Permissions: Scoped to ECS task definition registration, service updates,
     ECR image push, S3 frontend sync, and CloudFront cache invalidation.
3. **ECS Task Execution Role** (`FlowCardTaskExecutionRole`):
   - Scope: Assumed by the ECS container infrastructure agent.
   - Permissions: Pull container images from private ECR, stream logs to
     CloudWatch Logs, and fetch runtime secrets via
     `secretsmanager:GetSecretValue`.
4. **ECS Service Task Roles**:
   - Scope: Dedicated per-service IAM task roles assumed by running container
     processes at application runtime.
   - **Node Gateway Task Role** (`FlowCardNodeTaskRole`): No AWS resource
     permissions (pure HTTP/compute proxy).
   - **Go API Task Role** (`FlowCardApiTaskRole`): Scoped strictly to
     `s3:GetObject`, `s3:PutObject`, and `s3:DeleteObject` on the private
     onboarding document bucket, plus `kms:GenerateDataKey` and `kms:Decrypt` on
     the onboarding KMS key for SSE-KMS document storage.
   - **Go Worker Task Role** (`FlowCardWorkerTaskRole`): Distinct from the API
     task role. Scoped strictly to `s3:GetObject` on the onboarding document
     bucket and `kms:Decrypt` on the onboarding KMS key. The worker consumes
     tasks from the internal PostgreSQL outbox queue and does not require AWS
     SQS, image execution, or `kms:GenerateDataKey` write privileges.
5. **Database Migration Task**:
   - Scope: Standalone, one-off ECS migration task.
   - Credentials: Uses a separate privileged database secret with DDL migration
     rights, keeping runtime service credentials restricted to DML.

## GitHub Actions deployment authorization

The deployment workflow uses manual `workflow_dispatch` from the exact
`develop` ref, requires the explicit `confirm_approval` boolean, and enables
`id-token: write` only on the privileged deploy job. AWS independently restricts
the OIDC role to the repository's immutable owner/repository IDs and the exact
`develop` branch subject. Foundation review, runtime approval, and release
provenance checks remain in the workflow. GitHub environment protection and
environment secrets are not used; application credentials remain in AWS
Secrets Manager.

## Account access setup checklist

The following checklist tracks human account setup and configuration tasks:

- [ ] **Root account MFA**: Verify or enable in AWS Management Console.
- [ ] **Root access keys**: Verify absent or delete in AWS Management Console.
- [ ] **IAM billing access**: Activate under root account settings.
- [ ] **AWS Organization & Identity Center**: Obtain Ramil's explicit approval
      and enable organization instance in `us-west-2`.
- [ ] **Deployment user & permission set**: Create `FlowCardDeployer` and assign
      to personal AWS account.
- [ ] **Local CLI profile**: Configure `flowcard-demo` in `~/.aws/config`.
- [ ] **CLI authentication**: Execute `aws sso login --profile flowcard-demo`.
- [ ] **STS verification**: Confirm `aws sts get-caller-identity --profile
      flowcard-demo` succeeds without error.
- [ ] **GitHub deployment settings**: Set and read back the approved repository
      Actions variables; verify the AWS role trust matches the immutable
      `develop` subject.

## See also

- [AWS IAM root user security best practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_root-user.html)
- [AWS IAM Identity Center organization instances](https://docs.aws.amazon.com/singlesignon/latest/userguide/organization-instances-identity-center.html)
- [Configuring the AWS CLI with IAM Identity Center](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sso.html)
- [GitHub Actions deployment environments and protection rules](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)
- [Configuring OpenID Connect in Amazon Web Services](https://docs.github.com/en/actions/deployment/security-hardening-your-deployments/configuring-openid-connect-in-amazon-web-services)
