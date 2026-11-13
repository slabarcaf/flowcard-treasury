output "log_group_arns" {
  value       = { for name, lg in aws_cloudwatch_log_group.logs : name => lg.arn }
  description = "Map of CloudWatch Log Group names to their ARNs"
}

output "log_group_names" {
  value       = [for name, lg in aws_cloudwatch_log_group.logs : lg.name]
  description = "List of created CloudWatch Log Group names"
}
