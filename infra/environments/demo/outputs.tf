# ------------------------------------------------------------------------------
# Task 3: Foundational Infrastructure Outputs
# ------------------------------------------------------------------------------

output "ecr_repository_urls" {
  value       = module.ecr.repository_urls
  description = "Map of ECR repository URLs for demo container images"
}

output "github_oidc_role_arn" {
  value       = module.oidc.deployer_role_arn
  description = "ARN of the IAM role assumed by GitHub Actions demo workflow"
}

output "cloudwatch_log_group_names" {
  value       = module.cloudwatch.log_group_names
  description = "Names of the created CloudWatch log groups"
}

output "budget_name" {
  value       = module.budget.budget_name
  description = "Name of the configured AWS budget"
}

# ------------------------------------------------------------------------------
# Task 4: Networking & Load Balancing Outputs
# ------------------------------------------------------------------------------

output "vpc_id" {
  value       = module.vpc.vpc_id
  description = "ID of the demo VPC"
}

output "public_subnet_ids" {
  value       = module.vpc.public_subnet_ids
  description = "IDs of the public subnets (spanning 2 AZs)"
}

output "private_subnet_ids" {
  value       = module.vpc.private_subnet_ids
  description = "IDs of the private application & data subnets (spanning 2 AZs)"
}

output "nat_public_ip" {
  value       = module.vpc.nat_public_ip
  description = "Public IP address of the managed NAT gateway"
}

output "service_discovery_namespace_id" {
  value       = module.vpc.service_discovery_namespace_id
  description = "ID of the ECS private service discovery DNS namespace"
}

output "service_discovery_namespace_name" {
  value       = module.vpc.service_discovery_namespace_name
  description = "Name of the ECS private service discovery DNS namespace (flowcard.internal)"
}

output "alb_arn" {
  value       = module.alb.alb_arn
  description = "ARN of the internal Application Load Balancer"
}

output "alb_dns_name" {
  value       = module.alb.alb_dns_name
  description = "Private DNS name of the internal Application Load Balancer"
}

output "alb_security_group_id" {
  value       = module.alb.alb_security_group_id
  description = "Security group ID of the internal ALB"
}

output "alb_listener_arn" {
  value       = module.alb.alb_listener_arn
  description = "ARN of the HTTP listener on the internal ALB"
}

output "node_target_group_arn" {
  value       = module.alb.node_target_group_arn
  description = "ARN of the target group for Node gateway ECS tasks"
}

# ------------------------------------------------------------------------------
# Task 4: Task Security Groups Outputs
# ------------------------------------------------------------------------------

output "node_task_security_group_id" {
  value       = aws_security_group.node_task.id
  description = "Security group ID for Node gateway ECS tasks"
}

output "go_api_task_security_group_id" {
  value       = aws_security_group.go_api_task.id
  description = "Security group ID for Go API ECS tasks"
}

output "go_worker_task_security_group_id" {
  value       = aws_security_group.go_worker_task.id
  description = "Security group ID for Go Worker ECS tasks"
}

output "migration_task_security_group_id" {
  value       = aws_security_group.migration_task.id
  description = "Security group ID for Database Migration ECS tasks"
}

# ------------------------------------------------------------------------------
# Task 4: Managed Data Services & Storage Outputs
# ------------------------------------------------------------------------------

output "rds_endpoint" {
  value       = module.rds.endpoint
  description = "Connection endpoint for the RDS PostgreSQL database"
}

output "rds_address" {
  value       = module.rds.address
  description = "Hostname of the RDS PostgreSQL database"
}

output "rds_port" {
  value       = module.rds.port
  description = "Port on which the RDS PostgreSQL database accepts connections"
}

output "rds_database_name" {
  value       = module.rds.database_name
  description = "Name of the default PostgreSQL database"
}

output "rds_master_user_secret_arn" {
  value       = module.rds.master_user_secret_arn
  description = "ARN of the Secrets Manager secret automatically managing the master user password"
}

output "rds_security_group_id" {
  value       = module.rds.security_group_id
  description = "Security group ID of the RDS PostgreSQL instance"
}

output "documents_bucket_name" {
  value       = module.s3_documents.bucket_name
  description = "Name of the onboarding documents S3 bucket"
}

output "documents_bucket_arn" {
  value       = module.s3_documents.bucket_arn
  description = "ARN of the onboarding documents S3 bucket"
}

output "documents_kms_key_arn" {
  value       = module.s3_documents.kms_key_arn
  description = "ARN of the KMS customer-managed key used for document encryption"
}

output "frontend_bucket_name" {
  value       = module.s3_frontend.bucket_name
  description = "Name of the static frontend S3 bucket"
}

output "frontend_bucket_arn" {
  value       = module.s3_frontend.bucket_arn
  description = "ARN of the static frontend S3 bucket"
}

output "frontend_bucket_regional_domain_name" {
  value       = module.s3_frontend.bucket_regional_domain_name
  description = "Regional domain name of the frontend bucket for CloudFront origin"
}

output "secret_arns" {
  value       = module.secrets.secret_arns
  description = "Map of secret container names to their ARNs"
}

# ------------------------------------------------------------------------------
# Task 4: IAM Execution & Task Roles Outputs (Least Privilege Role Separation)
# ------------------------------------------------------------------------------

output "node_execution_role_arn" {
  value       = aws_iam_role.node_execution.arn
  description = "ARN of the dedicated Node Gateway ECS execution role"
}

output "go_api_execution_role_arn" {
  value       = aws_iam_role.go_api_execution.arn
  description = "ARN of the dedicated Go API ECS execution role"
}

output "go_worker_execution_role_arn" {
  value       = aws_iam_role.go_worker_execution.arn
  description = "ARN of the dedicated Go Worker ECS execution role"
}

output "migration_execution_role_arn" {
  value       = aws_iam_role.migration_execution.arn
  description = "ARN of the dedicated Database Migration ECS execution role"
}

output "node_task_role_arn" {
  value       = aws_iam_role.node_task.arn
  description = "ARN of the dedicated Node Gateway ECS task role (zero AWS permissions)"
}

output "go_api_task_role_arn" {
  value       = aws_iam_role.go_api_task.arn
  description = "ARN of the dedicated Go API ECS task role (scoped S3 documents and KMS)"
}

output "go_worker_task_role_arn" {
  value       = aws_iam_role.go_worker_task.arn
  description = "ARN of the dedicated Go Worker ECS task role (scoped S3 read and KMS decrypt)"
}

output "migration_task_role_arn" {
  value       = aws_iam_role.migration_task.arn
  description = "ARN of the dedicated Database Migration ECS task role (zero AWS permissions)"
}

# ------------------------------------------------------------------------------
# Task 6: ECS Fargate Services & Tasks Outputs
# ------------------------------------------------------------------------------

output "ecs_cluster_id" {
  value       = module.ecs.cluster_id
  description = "ID of the ECS Fargate cluster"
}

output "ecs_cluster_name" {
  value       = module.ecs.cluster_name
  description = "Name of the ECS Fargate cluster"
}

output "ecs_cluster_arn" {
  value       = module.ecs.cluster_arn
  description = "ARN of the ECS Fargate cluster"
}

output "node_service_name" {
  value       = module.ecs.node_service_name
  description = "Name of the Node Gateway ECS service"
}

output "go_api_service_name" {
  value       = module.ecs.go_api_service_name
  description = "Name of the Go API ECS service"
}

output "go_worker_service_name" {
  value       = module.ecs.go_worker_service_name
  description = "Name of the Go Worker ECS service"
}

output "node_task_definition_arn" {
  value       = module.ecs.node_task_definition_arn
  description = "ARN of the Node Gateway ECS task definition"
}

output "go_api_task_definition_arn" {
  value       = module.ecs.go_api_task_definition_arn
  description = "ARN of the Go API ECS task definition"
}

output "go_worker_task_definition_arn" {
  value       = module.ecs.go_worker_task_definition_arn
  description = "ARN of the Go Worker ECS task definition"
}

output "node_migration_task_definition_arn" {
  value       = module.ecs.node_migration_task_definition_arn
  description = "ARN of the Node one-off migration task definition"
}

output "go_migration_task_definition_arn" {
  value       = module.ecs.go_migration_task_definition_arn
  description = "ARN of the Go one-off migration task definition"
}

output "synthetic_seed_task_definition_arn" {
  value       = module.ecs.synthetic_seed_task_definition_arn
  description = "ARN of the synthetic seed one-off task definition"
}

# ------------------------------------------------------------------------------
# Task 7: CloudFront & Public Routing Tier Outputs
# ------------------------------------------------------------------------------

output "cloudfront_distribution_id" {
  value       = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_id : null
  description = "ID of the CloudFront distribution"
}

output "cloudfront_distribution_arn" {
  value       = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_arn : null
  description = "ARN of the CloudFront distribution"
}

output "cloudfront_domain_name" {
  value       = var.enable_cloudfront ? module.cloudfront[0].cloudfront_domain_name : null
  description = "Default generated domain name of the CloudFront distribution"
}

output "cloudfront_endpoint_url" {
  value       = var.enable_cloudfront ? "https://${module.cloudfront[0].cloudfront_domain_name}" : null
  description = "Public HTTPS endpoint URL for FlowCard demonstration"
}

output "vpc_origin_id" {
  value       = var.enable_cloudfront ? module.cloudfront[0].vpc_origin_id : null
  description = "ID of the CloudFront VPC Origin for private ALB"
}
