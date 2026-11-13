output "secret_arns" {
  value       = { for name, secret in aws_secretsmanager_secret.containers : name => secret.arn }
  description = "Map of secret container names to their ARNs"
}

output "secret_names" {
  value       = [for name, secret in aws_secretsmanager_secret.containers : secret.name]
  description = "List of managed secret container names"
}
