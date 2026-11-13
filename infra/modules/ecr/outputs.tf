output "repository_urls" {
  value       = { for name, repo in aws_ecr_repository.repos : name => repo.repository_url }
  description = "Map of ECR repository names to their repository URLs"
}

output "repository_arns" {
  value       = { for name, repo in aws_ecr_repository.repos : name => repo.arn }
  description = "Map of ECR repository names to their ARNs"
}
