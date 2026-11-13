output "endpoint" {
  value       = aws_db_instance.main.endpoint
  description = "Connection endpoint for the RDS PostgreSQL database"
}

output "address" {
  value       = aws_db_instance.main.address
  description = "Hostname of the RDS PostgreSQL database"
}

output "port" {
  value       = aws_db_instance.main.port
  description = "Port on which the RDS PostgreSQL database accepts connections"
}

output "database_name" {
  value       = aws_db_instance.main.db_name
  description = "Name of the default PostgreSQL database"
}

output "master_user_secret_arn" {
  value       = aws_db_instance.main.master_user_secret[0].secret_arn
  description = "ARN of the Secrets Manager secret automatically managing the master user password"
}

output "security_group_id" {
  value       = aws_security_group.rds.id
  description = "Security group ID of the RDS PostgreSQL instance"
}
