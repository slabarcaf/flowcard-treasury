# Demo Environment Costs

This document provides a line-item monthly cost estimate for FlowCard's
always-on AWS sandbox demonstration environment in Oregon (`us-west-2`). It
details transparent resource sizing assumptions, itemized AWS pricing rates
validated against official AWS pricing documentation for `us-west-2` (November
2026), calculates a total projected monthly expense, and proposes a budget alert
threshold for human operator approval.

[TOC]

## Pricing principles and baseline assumptions

The demonstration deployment is an always-on, low-traffic environment designed
for live stakeholder evaluation, operator testing, and automated integration.

- **Target region**: Oregon (`us-west-2`). All rates reflect official AWS
  on-demand pricing for `us-west-2` (November 2026).
- **Billing month standard**: 730 hours per month (standard AWS operational
  month calculation: 365 days * 24 hours / 12 months = 730 hours).
- **Traffic scale**: Low-volume demonstration workload (< 50,000 monthly
  requests, < 5 GB monthly egress, < 1 GB stored onboarding documents).
- **High-availability SLA**: None. Single-AZ compute and database deployments
  are selected to minimize ongoing expenditure while maintaining always-on
  availability. Note: While compute and RDS are Single-AZ, AWS requires an
  Application Load Balancer (ALB) to be attached to subnets in at least two
  Availability Zones.
- **Cost target**: Provisionally targeted at approximately $100–150 monthly
  before applicable taxes, credits, or free tier deductions. Gross costs are
  itemized explicitly before applying eligible free tier allowances.

## Itemized monthly cost breakdown

The table below details projected monthly expenses by service component in
`us-west-2`:

| Component | Sizing & Allocation | Unit Rate (`us-west-2`) | Monthly Cost |
|---|---|---|---|
| **VPC NAT Gateway** | 1 gateway, 730 hours | $0.045 / hour | $32.85 |
| **VPC Public IPv4 Address** | 1 public IP (attached to NAT), 730 hours | $0.005 / hour | $3.65 |
| **VPC NAT Data Processing** | Outbound deploy pulls & platform calls (~10 GB) | $0.045 / GB | $0.45 |
| **Application Load Balancer** | 1 shared ALB (attached to 2 AZ subnets), 730 hrs | $0.0225 / hour | $16.43 |
| **ALB Capacity Units (LCU)** | Minimal traffic (< 0.2 LCU average) | $0.008 / LCU-hour | $1.17 |
| **ECS Fargate: Node Gateway** | 0.25 vCPU, 0.5 GB RAM, 730 hours | $0.04048/vCPU-hr + $0.004445/GB-hr | $8.99 |
| **ECS Fargate: Go API** | 0.25 vCPU, 0.5 GB RAM, 730 hours | $0.04048/vCPU-hr + $0.004445/GB-hr | $8.99 |
| **ECS Fargate: Go Worker** | 0.25 vCPU, 0.5 GB RAM, 730 hours | $0.04048/vCPU-hr + $0.004445/GB-hr | $8.99 |
| **RDS PostgreSQL (Single-AZ)** | `db.t4g.micro` (2 vCPU, 1 GB RAM), 730 hours | $0.016 / hour | $11.68 |
| **RDS Storage (gp3)** | 20 GB general purpose SSD | $0.115 / GB-month | $2.30 |
| **RDS Automated Backups** | 10 GB excess backup storage (7-day retention) | $0.095 / GB-month (after 20 GB base) | $0.95 |
| **Amazon S3: Frontend Bucket** | Standard S3, < 1 GB, 5,000 requests | $0.023 / GB + $0.005/1K PUT, $0.0004/1K GET | $0.05 |
| **Amazon S3: Documents Bucket** | Standard S3, < 2 GB encrypted documents | $0.023 / GB + request charges | $0.08 |
| **Amazon S3: Terraform State** | State bucket & locks, versioning, 2,000 reqs | $0.023 / GB + $0.005/1K PUT, $0.0004/1K GET | $0.03 |
| **Amazon CloudFront** | 10 GB data transfer ($0.85 gross; eligible free) | $0.085 / GB gross (standard AWS free tier) | $0.85 |
| **Amazon ECR** | Container image storage (4 repositories, ~6 GB) | $0.10 / GB-month | $0.60 |
| **AWS Secrets Manager** | 6 managed secrets (5 custom containers + 1 RDS master) | $0.40 / secret-month | $2.40 |
| **AWS KMS** | 1 customer-managed key (state & documents) | $1.00 / key-month + requests | $1.05 |
| **Amazon CloudWatch Logs** | Ingestion & storage (~2 GB monthly logs, 14d) | $0.50 / GB ingested + $0.03 / GB stored | $1.06 |
| **Total Projected Gross Spend** | | | **$102.94** |

## Service-specific sizing details

### Networking and VPC

- **NAT Gateway ($32.85/mo)**: A single NAT gateway placed in one public subnet
  provides outbound internet connectivity for ECS tasks in private subnets
  (pulling images from ECR, calling external Airwallex sandbox endpoints).
- **Public IPv4 Address ($3.65/mo)**: AWS charges $0.005/hour for all public
  IPv4 addresses, including the single IP allocated to the NAT gateway.
- **Application Load Balancer ($17.60/mo)**: Serves as the private HTTP origin
  for CloudFront VPC origin integration. Although compute services run in a
  single Availability Zone, AWS networking requires the ALB to span at least two
  AZ subnets. Base hourly cost is $16.43, with LCU consumption remaining near
  minimum.

### ECS Fargate compute

- Three independent containers run 24/7 in private subnets:
  1. `flowcard-node-gateway`: 0.25 vCPU, 0.5 GB RAM (~$8.99/mo).
  2. `flowcard-go-api`: 0.25 vCPU, 0.5 GB RAM (~$8.99/mo).
  3. `flowcard-go-worker`: 0.25 vCPU, 0.5 GB RAM (~$8.99/mo).
- Combined compute subtotal: **$26.97/month**.
- Task sizes reflect lightweight compiled Go binaries and an optimized Node.js
  runtime. CPU and memory can be scaled up if profiling reveals memory
  pressure.

### Database storage and backups

- **Engine & Instance**: PostgreSQL 17 on `db.t4g.micro` (Single-AZ, ARM64
  Graviton2). Note: RDS `t4g` instances include baseline CPU performance; if
  configured with Unlimited burst credits and sustained CPU exceeds baseline,
  surplus credit charges ($0.075 per vCPU-hour) can apply. Sizing assumes
  standard low-traffic demo burst credit allowance without surplus billing.
- **Storage**: 20 GB `gp3` storage with default 3,000 IOPS and 125 MB/s
  throughput included at no extra cost ($2.30/mo).
- **Backup Retention**: 7 days automated daily snapshots. AWS provides backup
  storage free up to 100% of the total provisioned database storage (20 GB
  free base allocation). Estimating an additional 10 GB of cumulative daily
  differential snapshots at $0.095/GB-month yields **$0.95/month**.
- Combined database subtotal: **$14.93/month**.

### Security, storage, and telemetry

- **AWS Secrets Manager ($2.40/mo)**: 6 total managed secrets at $0.40/month
  each:
  1. `flowcard/db/app`: runtime application database credentials (DML).
  2. `flowcard/db/migrate`: privileged migration database credentials (DDL).
  3. `flowcard/airwallex`: Airwallex sandbox API credentials & webhook secret.
  4. `flowcard/operator`: Kit 5 operator session login credential.
  5. `flowcard/service-auth`: Node-to-Go service JWT signing secret.
  6. `rds!db-...`: RDS-managed master password secret generated automatically
     by AWS Secrets Manager (`manage_master_user_password = true`). This is a
     bootstrap administrative credential reserved for initial DBA setup and
     is never granted to runtime tasks or migration tasks.
- **AWS KMS ($1.05/mo)**: Single customer-managed key (CMK) for Terraform
  backend state encryption and S3 onboarding document encryption (SSE-KMS).
- **Amazon S3 ($0.16/mo)**: Frontend assets bucket, onboarding documents
  bucket, and Terraform state bucket with native S3 state locking and
  versioning.
- **Amazon ECR ($0.60/mo)**: Container image storage across 4 repositories
  (`flowcard-node-gateway`, `flowcard-go-api`, `flowcard-go-worker`, and
  `flowcard-migration`) with lifecycle policies expiring untagged digests.
- **Amazon CloudFront ($0.85/mo gross)**: 10 GB outbound data transfer at
  $0.085/GB. While standard AWS accounts receive an ongoing 1 TB free tier
  allowance for CloudFront, the gross cost is accounted here before credits.
- **CloudWatch Logs ($1.06/mo)**: Centralized container log streaming with an
  enforced 14-day retention expiration to prevent indefinite storage
  accumulation.

## Proposed budget alert threshold

To provide spend visibility and alert operators to cost anomalies, the
deployment proposes configuring an AWS Budget in Task 3:

- **Proposed monthly budget amount**: **$150.00**.
- **Alert notifications**:
  - Alert 1 (Actual): Sent when actual accrued spend reaches 80% ($120.00).
  - Alert 2 (Forecasted): Sent when forecasted monthly spend is projected to
    exceed 100% ($150.00).
  - Alert 3 (Actual Threshold): Sent when actual accrued spend reaches 100%
    ($150.00).
- **Clarification**: AWS Budgets provides notifications and alerts only; it does
  not enforce an automated hard cap or stop live running resources.
- **Decision status**: The $150.00 limit is a **proposal** awaiting Ramil's
  explicit confirmation before provisioning the budget alert resource in
  Terraform.

## See also

- [AWS Fargate pricing for us-west-2](https://aws.amazon.com/fargate/pricing/)
- [Amazon RDS for PostgreSQL pricing for us-west-2](https://aws.amazon.com/rds/postgresql/pricing/)
- [Elastic Load Balancing pricing for us-west-2](https://aws.amazon.com/elasticloadbalancing/pricing/)
- [Amazon VPC pricing and NAT Gateway costs](https://docs.aws.amazon.com/vpc/latest/userguide/nat-gateway-pricing.html)
- [AWS Secrets Manager pricing](https://aws.amazon.com/secrets-manager/pricing/)
- [AWS Key Management Service pricing](https://aws.amazon.com/kms/pricing/)
- [Amazon CloudFront pricing](https://aws.amazon.com/cloudfront/pricing/)
