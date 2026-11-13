# FlowCard Demo Foundation Plan Review

## Overview

This review document certifies offline static analysis of the Terraform
foundation infrastructure plan for the FlowCard demonstration environment.

## Governance and Boundaries

1. **Static Analysis Only**: No live AWS `terraform plan` or API calls were
   executed. This review certifies configuration syntax, resource topology,
   and least-privilege security properties entirely offline.
2. **Explicit Hold**: Cloud provisioning remains held pending explicit
   authorized operator execution.
3. **Unapplied Proposals**: Proposed onboarding integration variables remain
   strictly unapplied (`enable_proposed_onboarding_integration = false`).

## Certified Configuration Outputs

The foundation topology produces the following trusted release outputs
documented in `foundation-outputs.example.json`:
- ECS Cluster:
  `arn:aws:ecs:us-west-2:123456789012:cluster/flowcard-demo-cluster`
- Private Subnets: `subnet-0123456789abcdef0`, `subnet-0123456789abcdef1`
- Migration Security Group: `sg-0123456789abcdef0`
- ECR Repositories: Scoped to authorized account `123456789012` in `us-west-2`
- S3 Bucket: `flowcard-demo-frontend-assets`
- CloudFront Distribution: `E1234567890ABC`

## Release Artifact Evidence

The workflow accepts foundation outputs only from the tracked path
`infra/environments/demo/foundation-outputs.json`, with a required SHA-256
matching the submitted file. The artifact identifies an ancestor commit
containing the reviewed foundation plan and carries the canonical SHA-256 of its
foundation outputs, excluding the approval evidence object. The workflow
verifies the ancestor relationship, plan digest, full artifact digest, and a
usable pull request reference. This avoids embedding the deployment commit SHA
in the commit that contains the artifact. Runtime configuration approval
remains false unless the artifact contains a named approver, approval reference,
and approval time.

The repository has no GitHub environment protection gate. The workflow accepts
privileged execution only from a manual dispatch on the exact `develop` ref,
with explicit dispatch confirmation. The AWS trust policy independently
requires the repository's immutable OIDC subject for that branch.
