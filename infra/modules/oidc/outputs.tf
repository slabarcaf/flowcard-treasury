output "oidc_provider_arn" {
  value       = aws_iam_openid_connect_provider.github.arn
  description = "ARN of the GitHub Actions OIDC identity provider"
}

output "deployer_role_arn" {
  value       = aws_iam_role.github_deployer.arn
  description = "ARN of the IAM role assumed by GitHub Actions for demo releases"
}

output "deployer_role_name" {
  value       = aws_iam_role.github_deployer.name
  description = "Name of the IAM role assumed by GitHub Actions for demo releases"
}
