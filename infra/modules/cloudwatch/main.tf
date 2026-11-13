resource "aws_cloudwatch_log_group" "logs" {
  for_each = toset(var.log_group_names)

  name              = each.value
  retention_in_days = var.retention_in_days

  tags = {
    Name = each.value
  }
}
