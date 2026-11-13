variable "aws_region" {
  type        = string
  description = "AWS region for demo resources"
  default     = "us-west-2"
}

variable "allowed_account_ids" {
  type        = list(string)
  description = "Target AWS Account ID(s). Must be non-empty and contain 12-digit account strings."

  validation {
    condition     = length(var.allowed_account_ids) > 0 && can([for id in var.allowed_account_ids : regex("^\\d{12}$", id)])
    error_message = "allowed_account_ids must be a non-empty list of valid 12-digit AWS Account IDs."
  }
}

variable "github_repository" {
  type        = string
  description = "GitHub repository in owner/repo format for OIDC trust"
  default     = "slabarcaf/flowcard-treasury"
}

variable "untagged_image_retention_days" {
  type        = number
  description = "Number of days before expiring untagged container images in ECR"
  default     = 7

  validation {
    condition     = floor(var.untagged_image_retention_days) == var.untagged_image_retention_days && var.untagged_image_retention_days > 0
    error_message = "untagged_image_retention_days must be a positive integer."
  }
}

variable "log_retention_in_days" {
  type        = number
  description = "CloudWatch log retention period in days (must match an AWS supported retention value)"
  default     = 14

  validation {
    condition     = contains([1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653], var.log_retention_in_days)
    error_message = "log_retention_in_days must be one of the AWS CloudWatch supported retention values."
  }
}

variable "enable_budget" {
  type        = bool
  description = "Explicit flag to provision the AWS Budget resource (disabled by default)"
  default     = false
}

variable "monthly_budget_amount" {
  type        = string
  description = "Monthly budget limit in USD for spending alerts"
  default     = "150.0"

  validation {
    condition     = can(regex("^\\d+(\\.\\d+)?$", var.monthly_budget_amount)) && tonumber(var.monthly_budget_amount) > 0
    error_message = "monthly_budget_amount must be a valid positive numerical string."
  }
}

variable "budget_notification_emails" {
  type        = list(string)
  description = "Email addresses to receive AWS budget spending alerts. Required when enable_budget is true."
  default     = []
}

# ------------------------------------------------------------------------------
# Networking & Data Variables
# ------------------------------------------------------------------------------

variable "vpc_cidr" {
  type        = string
  description = "IPv4 CIDR block for the demo VPC"
  default     = "10.0.0.0/16"

  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr must be a valid IPv4 CIDR string."
  }
}

variable "availability_zones" {
  type        = list(string)
  description = "Availability zones to place demo subnets (must contain at least 2 unique AZs)"
  default     = ["us-west-2a", "us-west-2b"]

  validation {
    condition     = length(var.availability_zones) >= 2 && length(distinct(var.availability_zones)) == length(var.availability_zones)
    error_message = "availability_zones must contain at least 2 distinct, unique AZ names."
  }
}

variable "public_subnet_cidrs" {
  type        = list(string)
  description = "CIDR blocks for public subnets (must contain at least 2 distinct CIDRs)"
  default     = ["10.0.1.0/24", "10.0.2.0/24"]

  validation {
    condition = (
      length(var.public_subnet_cidrs) >= 2 &&
      length(distinct(var.public_subnet_cidrs)) == length(var.public_subnet_cidrs) &&
      can([for c in var.public_subnet_cidrs : cidrhost(c, 0)])
    )
    error_message = "public_subnet_cidrs must contain at least 2 distinct, valid IPv4 CIDR blocks."
  }
}

variable "private_subnet_cidrs" {
  type        = list(string)
  description = "CIDR blocks for private application & data subnets (must contain at least 2 distinct CIDRs)"
  default     = ["10.0.11.0/24", "10.0.12.0/24"]

  validation {
    condition = (
      length(var.private_subnet_cidrs) >= 2 &&
      length(distinct(var.private_subnet_cidrs)) == length(var.private_subnet_cidrs) &&
      can([for c in var.private_subnet_cidrs : cidrhost(c, 0)])
    )
    error_message = "private_subnet_cidrs must contain at least 2 distinct, valid IPv4 CIDR blocks."
  }
}

variable "node_port" {
  type        = number
  description = "Target port on which the Node gateway listens"
  default     = 3000

  validation {
    condition     = floor(var.node_port) == var.node_port && var.node_port > 0 && var.node_port <= 65535
    error_message = "node_port must be a valid TCP port between 1 and 65535."
  }
}

variable "frontend_bucket_name" {
  type        = string
  description = "Name of the S3 bucket for static frontend files"
  default     = "flowcard-demo-frontend-assets"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.frontend_bucket_name))
    error_message = "frontend_bucket_name must follow standard AWS S3 DNS naming rules."
  }
}

variable "documents_bucket_name" {
  type        = string
  description = "Name of the S3 bucket for private encrypted onboarding documents"
  default     = "flowcard-demo-onboarding-documents"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.documents_bucket_name))
    error_message = "documents_bucket_name must follow standard AWS S3 DNS naming rules."
  }
}

variable "db_engine_version" {
  type        = string
  description = "PostgreSQL engine version for the demo RDS instance (parameterized, pending regional live gate)"
  default     = "17.4"
}

variable "db_instance_class" {
  type        = string
  description = "Compute and memory capacity for the demo RDS instance"
  default     = "db.t4g.micro"
}

variable "db_allocated_storage" {
  type        = number
  description = "Allocated storage size in gigabytes (gp3) for the demo RDS instance"
  default     = 20

  validation {
    condition     = floor(var.db_allocated_storage) == var.db_allocated_storage && var.db_allocated_storage >= 20
    error_message = "db_allocated_storage must be an integer of at least 20 GB for PostgreSQL."
  }
}

variable "db_backup_retention_period" {
  type        = number
  description = "Backup retention period in days (1-35 days for RDS)"
  default     = 7

  validation {
    condition     = floor(var.db_backup_retention_period) == var.db_backup_retention_period && var.db_backup_retention_period >= 1 && var.db_backup_retention_period <= 35
    error_message = "db_backup_retention_period must be an integer between 1 and 35 days."
  }
}

variable "db_name" {
  type        = string
  description = "Name of the default PostgreSQL database"
  default     = "flowcard_treasury"
}

variable "db_master_username" {
  type        = string
  description = "Master administrative username for RDS"
  default     = "flowcard_admin"
}

# ------------------------------------------------------------------------------
# Task 6: ECS Fargate Services & Container Variables
# ------------------------------------------------------------------------------

variable "cpu_architecture" {
  type        = string
  description = "Target Linux CPU architecture for ECS tasks (X86_64 or ARM64)"
  default     = "X86_64"

  validation {
    condition     = contains(["X86_64", "ARM64"], var.cpu_architecture)
    error_message = "cpu_architecture must be either 'X86_64' or 'ARM64'."
  }
}

variable "enable_services" {
  type        = bool
  description = "Controls whether ECS task definitions and services are provisioned. Kept false during bootstrap foundation apply before container images exist."
  default     = false
}

variable "node_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Node gateway (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.node_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.node_image_digest))
    error_message = "node_image_digest must be a valid, immutable sha256: digest (latest/fake values prohibited)."
  }
}

variable "go_api_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Go API service (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.go_api_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.go_api_image_digest))
    error_message = "go_api_image_digest must be a valid, immutable sha256: digest (latest/fake values prohibited)."
  }
}

variable "go_worker_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Go background worker (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.go_worker_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.go_worker_image_digest))
    error_message = "go_worker_image_digest must be a valid, immutable sha256: digest (latest/fake values prohibited)."
  }
}

variable "migration_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for migration tasks (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.migration_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.migration_image_digest))
    error_message = "migration_image_digest must be a valid, immutable sha256: digest (latest/fake values prohibited)."
  }
}

variable "service_desired_count" {
  type        = number
  description = "Desired count for ECS services (default 0 enables infrastructure creation before image publication)"
  default     = 0

  validation {
    condition     = var.service_desired_count >= 0
    error_message = "service_desired_count must be non-negative."
  }
}

variable "enable_proposed_onboarding_integration" {
  type        = bool
  description = "Gate controlling wiring of the 4 pending proposed environment variables (kept false until user approval)"
  default     = false
}

variable "enable_cloudfront" {
  type        = bool
  description = "Whether to deploy the CloudFront distribution and VPC origin"
  default     = true
}

variable "cloudfront_price_class" {
  type        = string
  description = "CloudFront price class for the demonstration distribution"
  default     = "PriceClass_100"
}

variable "enable_vpc_origin_sg_only" {
  type        = bool
  description = "When true, narrows ALB ingress strictly to CloudFront VPC Origin service-managed SG / prefix list"
  default     = false
}

variable "vpc_origin_security_group_id" {
  type        = string
  description = "Security group ID of CloudFront VPC Origin service-managed ENIs (optional override)"
  default     = null
}

variable "allowed_origins" {
  type        = list(string)
  description = "Additional allowed origins for CORS on Node gateway"
  default     = []
}
