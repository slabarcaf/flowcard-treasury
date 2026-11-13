variable "aws_region" {
  type        = string
  description = "AWS Region for ECS log streaming and resource location"
  default     = "us-west-2"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID where ECS tasks and Cloud Map namespace are hosted"
}

variable "service_discovery_namespace_id" {
  type        = string
  description = "Cloud Map private DNS namespace ID for service registration"
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Private subnet IDs across 2 AZs for task ENI allocation"
}

variable "enable_services" {
  type        = bool
  description = "Controls whether ECS task definitions and services are provisioned. Kept false during bootstrap foundation apply before container images exist."
  default     = false
}

variable "alb_listener_arn" {
  type        = string
  description = "ARN of the ALB listener for Node gateway readiness dependency"
  default     = ""
}

variable "cpu_architecture" {
  type        = string
  description = "Target Linux CPU architecture for ECS Fargate tasks (X86_64 or ARM64)"
  default     = "X86_64"

  validation {
    condition     = contains(["X86_64", "ARM64"], var.cpu_architecture)
    error_message = "cpu_architecture must be either 'X86_64' or 'ARM64'."
  }
}

variable "node_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Node gateway (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.node_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.node_image_digest))
    error_message = "node_image_digest must be a valid, immutable sha256: digest (latest/tags are prohibited)."
  }
}

variable "go_api_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Go API service (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.go_api_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.go_api_image_digest))
    error_message = "go_api_image_digest must be a valid, immutable sha256: digest (latest/tags are prohibited)."
  }
}

variable "go_worker_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for Go background worker (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.go_worker_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.go_worker_image_digest))
    error_message = "go_worker_image_digest must be a valid, immutable sha256: digest (latest/tags are prohibited)."
  }
}

variable "migration_image_digest" {
  type        = string
  description = "Immutable SHA-256 image digest for migration tasks (e.g. sha256:...)"
  default     = null

  validation {
    condition     = var.migration_image_digest == null || can(regex("^sha256:[a-f0-9]{64}$", var.migration_image_digest))
    error_message = "migration_image_digest must be a valid, immutable sha256: digest (latest/tags are prohibited)."
  }
}

variable "node_repository_url" {
  type        = string
  description = "ECR repository URL for Node gateway"
}

variable "go_api_repository_url" {
  type        = string
  description = "ECR repository URL for Go API"
}

variable "go_worker_repository_url" {
  type        = string
  description = "ECR repository URL for Go worker"
}

variable "migration_repository_url" {
  type        = string
  description = "ECR repository URL for migration image"
}

variable "node_execution_role_arn" {
  type        = string
  description = "IAM execution role ARN for Node gateway"
}

variable "node_task_role_arn" {
  type        = string
  description = "IAM task role ARN for Node gateway"
}

variable "go_api_execution_role_arn" {
  type        = string
  description = "IAM execution role ARN for Go API"
}

variable "go_api_task_role_arn" {
  type        = string
  description = "IAM task role ARN for Go API"
}

variable "go_worker_execution_role_arn" {
  type        = string
  description = "IAM execution role ARN for Go worker"
}

variable "go_worker_task_role_arn" {
  type        = string
  description = "IAM task role ARN for Go worker"
}

variable "migration_execution_role_arn" {
  type        = string
  description = "IAM execution role ARN for migration tasks"
}

variable "migration_task_role_arn" {
  type        = string
  description = "IAM task role ARN for migration tasks"
}

variable "node_security_group_id" {
  type        = string
  description = "Security group ID for Node gateway task"
}

variable "go_api_security_group_id" {
  type        = string
  description = "Security group ID for Go API task"
}

variable "go_worker_security_group_id" {
  type        = string
  description = "Security group ID for Go worker task"
}

variable "migration_security_group_id" {
  type        = string
  description = "Security group ID for database migration tasks"
}

variable "alb_target_group_arn" {
  type        = string
  description = "Target group ARN on the internal ALB for Node gateway"
}

variable "node_log_group_name" {
  type        = string
  description = "CloudWatch log group name for Node gateway"
}

variable "go_api_log_group_name" {
  type        = string
  description = "CloudWatch log group name for Go API"
}

variable "go_worker_log_group_name" {
  type        = string
  description = "CloudWatch log group name for Go worker"
}

variable "migration_log_group_name" {
  type        = string
  description = "CloudWatch log group name for migration tasks"
}

variable "app_secret_arn" {
  type        = string
  description = "Secrets Manager ARN for runtime application database credentials (flowcard/db/app)"
}

variable "migrate_secret_arn" {
  type        = string
  description = "Secrets Manager ARN for privileged migration credentials (flowcard/db/migrate)"
}

variable "service_auth_secret_arn" {
  type        = string
  description = "Secrets Manager ARN for service-to-service JWT signing secret (flowcard/service-auth)"
}

variable "operator_secret_arn" {
  type        = string
  description = "Secrets Manager ARN for operator login credential (flowcard/operator)"
}

variable "airwallex_secret_arn" {
  type        = string
  description = "Secrets Manager ARN for Airwallex sandbox credentials (flowcard/airwallex)"
}

variable "s3_documents_bucket_name" {
  type        = string
  description = "Private S3 bucket name for onboarding documents"
}

variable "s3_documents_kms_key_arn" {
  type        = string
  description = "Customer-managed KMS key ARN for onboarding document encryption"
}

variable "service_desired_count" {
  type        = number
  description = "Desired instance count for ECS services (defaults to 0 to allow initial creation before image publication)"
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

variable "allowed_origins" {
  type        = list(string)
  description = "Allowed origins for CORS on Node gateway"
  default     = ["https://demo.flowcard.internal"]
}

variable "db_address" {
  type        = string
  description = "Hostname/address of the RDS PostgreSQL database"
  default     = "127.0.0.1"
}

variable "db_port" {
  type        = number
  description = "Port of the RDS PostgreSQL database"
  default     = 5432
}

variable "db_name" {
  type        = string
  description = "Database name of the RDS PostgreSQL database"
  default     = "flowcard_treasury"
}
