terraform {
  # Minimum Terraform 1.10 required for native S3 backend state locking (use_lockfile = true)
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "registry.terraform.io/hashicorp/aws"
      version = "~> 5.80"
    }
  }

  # Partial S3 backend configuration.
  # Remote backend values (bucket, kms_key_id) are provided dynamically at initialization
  # via -backend-config=path or CLI arguments from infra/bootstrap outputs.
  # Native S3 state locking is enforced via use_lockfile = true (requires Terraform >= 1.10).
  backend "s3" {
    key          = "environments/demo/terraform.tfstate"
    region       = "us-west-2"
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region = var.aws_region

  # Account guard: enforces apply only to authorized personal AWS account
  allowed_account_ids = var.allowed_account_ids

  default_tags {
    tags = {
      Project     = "flowcard"
      Environment = "demo"
      ManagedBy   = "terraform"
    }
  }
}

data "aws_caller_identity" "current" {}

# ------------------------------------------------------------------------------
# Task 3: Foundational Infrastructure Modules
# ------------------------------------------------------------------------------

# ECR Repositories with Untagged Lifecycle Image Retention
module "ecr" {
  source = "../../modules/ecr"

  untagged_image_retention_days = var.untagged_image_retention_days
}

# GitHub Actions OIDC Identity Provider & Deployment Role
module "oidc" {
  source = "../../modules/oidc"

  aws_region             = var.aws_region
  aws_account_id         = length(var.allowed_account_ids) > 0 ? var.allowed_account_ids[0] : data.aws_caller_identity.current.account_id
  github_owner_name      = "slabarcaf"
  github_owner_id        = "207892885"
  github_repository_name = "flowcard-treasury"
  github_repository_id   = "1427370701"
  ecs_cluster_arn        = module.ecs.cluster_arn
  ecs_cluster_name       = module.ecs.cluster_name
  ecr_repository_arns    = values(module.ecr.repository_arns)
  execution_role_arns = [
    aws_iam_role.node_execution.arn,
    aws_iam_role.go_api_execution.arn,
    aws_iam_role.go_worker_execution.arn,
    aws_iam_role.migration_execution.arn,
  ]
  task_role_arns = [
    aws_iam_role.node_task.arn,
    aws_iam_role.go_api_task.arn,
    aws_iam_role.go_worker_task.arn,
    aws_iam_role.migration_task.arn,
  ]
  frontend_bucket_arn         = module.s3_frontend.bucket_arn
  cloudfront_distribution_arn = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_arn : null
}

# CloudWatch Log Groups with Enforced Retention Expiration
module "cloudwatch" {
  source = "../../modules/cloudwatch"

  retention_in_days = var.log_retention_in_days
}

# AWS Budget Alert Notification (Explicitly Controlled)
module "budget" {
  source = "../../modules/budget"

  enable_budget       = var.enable_budget
  budget_amount       = var.monthly_budget_amount
  notification_emails = var.budget_notification_emails
}

# ------------------------------------------------------------------------------
# Task 4: Networking & Managed Data Services Modules
# ------------------------------------------------------------------------------

# VPC: 2-AZ Public & Private Subnets, Single NAT Gateway, S3 Gateway Endpoint, Private DNS
module "vpc" {
  source = "../../modules/vpc"

  vpc_cidr             = var.vpc_cidr
  public_subnet_cidrs  = var.public_subnet_cidrs
  private_subnet_cidrs = var.private_subnet_cidrs
  availability_zones   = var.availability_zones
}

# Application Load Balancer: Private Internal ALB spanning 2 AZs for Node Gateway
module "alb" {
  source = "../../modules/alb"

  vpc_id                       = module.vpc.vpc_id
  vpc_cidr                     = var.vpc_cidr
  private_subnet_ids           = module.vpc.private_subnet_ids
  node_port                    = var.node_port
  node_security_group_id       = aws_security_group.node_task.id
  enable_vpc_origin_sg_only    = var.enable_vpc_origin_sg_only
  vpc_origin_security_group_id = var.vpc_origin_security_group_id
}

# RDS PostgreSQL: Single-AZ db.t4g.micro PostgreSQL with Secrets Manager master password
# Ingress is restricted strictly to authorized task security groups (Node, API, Worker, Migration).
module "rds" {
  source = "../../modules/rds"

  vpc_id              = module.vpc.vpc_id
  database_subnet_ids = module.vpc.private_subnet_ids
  authorized_security_group_ids = [
    aws_security_group.node_task.id,
    aws_security_group.go_api_task.id,
    aws_security_group.go_worker_task.id,
    aws_security_group.migration_task.id
  ]
  engine_version          = var.db_engine_version
  allocated_storage       = var.db_allocated_storage
  instance_class          = var.db_instance_class
  database_name           = var.db_name
  master_username         = var.db_master_username
  backup_retention_period = var.db_backup_retention_period
}

# S3 Documents: Customer-Managed KMS Encrypted Bucket for Onboarding Documents
module "s3_documents" {
  source = "../../modules/s3_documents"

  bucket_name = var.documents_bucket_name
}

# S3 Frontend: Private Static Web Assets Bucket for CloudFront Distribution
module "s3_frontend" {
  source = "../../modules/s3_frontend"

  bucket_name                 = var.frontend_bucket_name
  cloudfront_distribution_arn = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_arn : null
}

# Secrets Manager: 5 Managed Secret Containers for Demo Credentials
module "secrets" {
  source = "../../modules/secrets"
}

# ------------------------------------------------------------------------------
# Task 7: CloudFront Distribution, OAC & Private ALB VPC Origin
# ------------------------------------------------------------------------------
module "cloudfront" {
  count  = var.enable_cloudfront ? 1 : 0
  source = "../../modules/cloudfront"

  s3_frontend_bucket_id                   = module.s3_frontend.bucket_name
  s3_frontend_bucket_arn                  = module.s3_frontend.bucket_arn
  s3_frontend_bucket_regional_domain_name = module.s3_frontend.bucket_regional_domain_name
  alb_arn                                 = module.alb.alb_arn
  alb_dns_name                            = module.alb.alb_dns_name
  alb_http_port                           = 80
  price_class                             = var.cloudfront_price_class
}

# ------------------------------------------------------------------------------
# Task 6: ECS Fargate Cluster, Services & Migration Tasks
# ------------------------------------------------------------------------------
module "ecs" {
  source = "../../modules/ecs"

  aws_region                             = var.aws_region
  vpc_id                                 = module.vpc.vpc_id
  service_discovery_namespace_id         = module.vpc.service_discovery_namespace_id
  private_subnet_ids                     = module.vpc.private_subnet_ids
  cpu_architecture                       = var.cpu_architecture
  node_image_digest                      = var.node_image_digest
  go_api_image_digest                    = var.go_api_image_digest
  go_worker_image_digest                 = var.go_worker_image_digest
  migration_image_digest                 = var.migration_image_digest
  node_repository_url                    = module.ecr.repository_urls["flowcard-node-gateway"]
  go_api_repository_url                  = module.ecr.repository_urls["flowcard-go-api"]
  go_worker_repository_url               = module.ecr.repository_urls["flowcard-go-worker"]
  migration_repository_url               = module.ecr.repository_urls["flowcard-migration"]
  node_execution_role_arn                = aws_iam_role.node_execution.arn
  node_task_role_arn                     = aws_iam_role.node_task.arn
  go_api_execution_role_arn              = aws_iam_role.go_api_execution.arn
  go_api_task_role_arn                   = aws_iam_role.go_api_task.arn
  go_worker_execution_role_arn           = aws_iam_role.go_worker_execution.arn
  go_worker_task_role_arn                = aws_iam_role.go_worker_task.arn
  migration_execution_role_arn           = aws_iam_role.migration_execution.arn
  migration_task_role_arn                = aws_iam_role.migration_task.arn
  node_security_group_id                 = aws_security_group.node_task.id
  go_api_security_group_id               = aws_security_group.go_api_task.id
  go_worker_security_group_id            = aws_security_group.go_worker_task.id
  migration_security_group_id            = aws_security_group.migration_task.id
  alb_target_group_arn                   = module.alb.node_target_group_arn
  alb_listener_arn                       = module.alb.alb_listener_arn
  enable_services                        = var.enable_services
  node_log_group_name                    = "/ecs/flowcard-node-gateway"
  go_api_log_group_name                  = "/ecs/flowcard-go-api"
  go_worker_log_group_name               = "/ecs/flowcard-go-worker"
  migration_log_group_name               = "/ecs/flowcard-migration"
  app_secret_arn                         = module.secrets.secret_arns["flowcard/db/app"]
  migrate_secret_arn                     = module.secrets.secret_arns["flowcard/db/migrate"]
  service_auth_secret_arn                = module.secrets.secret_arns["flowcard/service-auth"]
  operator_secret_arn                    = module.secrets.secret_arns["flowcard/operator"]
  airwallex_secret_arn                   = module.secrets.secret_arns["flowcard/airwallex"]
  s3_documents_bucket_name               = module.s3_documents.bucket_name
  s3_documents_kms_key_arn               = module.s3_documents.kms_key_arn
  service_desired_count                  = var.service_desired_count
  enable_proposed_onboarding_integration = var.enable_proposed_onboarding_integration
  allowed_origins                        = concat(var.allowed_origins, var.enable_cloudfront ? ["https://${module.cloudfront[0].cloudfront_domain_name}"] : [])
  db_address                             = module.rds.address
  db_port                                = module.rds.port
  db_name                                = module.rds.database_name

  depends_on = [module.alb]
}
