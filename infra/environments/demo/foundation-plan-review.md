# FlowCard Demo Foundation Plan Review

## Overview

This document preserves the historical offline static review of the Terraform
foundation for the FlowCard demonstration environment. It does not certify
current AWS state, authorize a change, or establish deployment acceptance.

## Governance and Boundaries

1. **Historical static analysis**: No live AWS `terraform plan` or API calls
   were executed for this review. Its findings cover configuration syntax,
   resource topology, and least-privilege properties only.
2. **No live authorization**: This review is not an approval to provision,
   deploy, or modify cloud resources.
3. **Historical proposal state**: This review recorded
   `enable_proposed_onboarding_integration = false`. Later approval and the
   November 12 operator checkpoint report the integration enabled and values
   provisioned. This document does not independently verify that live state.

## Certified Configuration Outputs

The historical review used the following representative values from
`foundation-outputs.example.json`. They are examples, not live resource IDs or
deployment inputs:
- ECS Cluster:
  `arn:aws:ecs:us-west-2:123456789012:cluster/flowcard-demo-cluster`
- Private Subnets: `subnet-0123456789abcdef0`, `subnet-0123456789abcdef1`
- Migration Security Group: `sg-0123456789abcdef0`
- ECR Repositories: Scoped to authorized account `123456789012` in `us-west-2`
- S3 Bucket: `flowcard-demo-frontend-assets`
- CloudFront Distribution: `E1234567890ABC`

## Current Tracked Configuration

The workflow reads the real, non-secret resource references from the tracked
`infra/environments/demo/foundation-outputs.json` file in the pinned commit.
It validates the account, region, and resource fields against the existing
repository variables before OIDC. Dispatch requires no uploaded output,
manually supplied checksum, ancestor commit, pull request, or reviewer-evidence
object. Artifact checksums still protect build files transferred between
workflow jobs.

The repository has no GitHub environment protection gate. A no-input manual
dispatch from `develop` starts the workflow; repository and branch checks and
the immutable OIDC subject remain enforced. The first live run of the
simplified workflow is still pending.
