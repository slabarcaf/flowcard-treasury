variable "github_owner_name" {
  type        = string
  description = "GitHub owner name used by the OIDC subject"
  default     = "slabarcaf"
}

variable "github_repository_name" {
  type        = string
  description = "GitHub repository name used by the OIDC subject"
  default     = "flowcard-treasury"
}

variable "github_owner_id" {
  type        = string
  description = "Immutable GitHub owner ID used by the OIDC subject"
  default     = "207892885"
}

variable "github_repository_id" {
  type        = string
  description = "Immutable GitHub repository ID used by the OIDC subject"
  default     = "1427370701"
}

variable "role_name" {
  type        = string
  description = "Name of the IAM role assumed by GitHub Actions"
  default     = "FlowCardGitHubDeployer"
}

variable "aws_region" {
  type        = string
  description = "AWS region for scoped resource ARNs"
  default     = "us-west-2"
}

variable "aws_account_id" {
  type        = string
  description = "AWS account ID for scoped resource ARNs"
  default     = ""
}

variable "ecs_cluster_arn" {
  type        = string
  description = "ARN of the ECS Fargate cluster"
  default     = "*"
}

variable "ecs_cluster_name" {
  type        = string
  description = "Name of the ECS Fargate cluster"
  default     = "flowcard-demo-cluster"
}

variable "ecr_repository_arns" {
  type        = list(string)
  description = "List of ECR repository ARNs for container image deployments"
  default     = []

  validation {
    condition     = length(var.ecr_repository_arns) > 0
    error_message = "ecr_repository_arns must be non-empty and specify scoped repository ARNs (no wildcard fallback)."
  }
}

variable "execution_role_arns" {
  type        = list(string)
  description = "List of dedicated ECS execution role ARNs that the deployer can pass to ECS"
  default     = []
}

variable "task_role_arns" {
  type        = list(string)
  description = "List of dedicated ECS task role ARNs that the deployer can pass to ECS"
  default     = []
}

variable "frontend_bucket_arn" {
  type        = string
  description = "ARN of the S3 frontend assets bucket"
  default     = "*"
}

variable "cloudfront_distribution_arn" {
  type        = string
  description = "Optional ARN of the CloudFront distribution for cache invalidation"
  default     = null

  validation {
    condition = var.cloudfront_distribution_arn == null || can(regex(
      "^arn:[^:]+:cloudfront::[0-9]{12}:distribution/[A-Z0-9]+$",
      var.cloudfront_distribution_arn
    ))
    error_message = "cloudfront_distribution_arn must be null or an exact distribution ARN."
  }
}
