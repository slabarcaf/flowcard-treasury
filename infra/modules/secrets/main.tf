# AWS Secrets Manager Secret Containers
# Note: Creates secret metadata containers with lifecycle protection.
# Secret values are NOT defined or managed in Terraform HCL or state;
# they must be populated out-of-band via AWS CLI or AWS Console.
resource "aws_secretsmanager_secret" "containers" {
  for_each = toset(var.secret_names)

  name                    = each.value
  description             = "Managed secret container for ${each.value}"
  recovery_window_in_days = var.recovery_window_in_days

  tags = {
    Name = each.value
  }
}
