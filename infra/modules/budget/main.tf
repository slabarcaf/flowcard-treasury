resource "aws_budgets_budget" "monthly" {
  count = var.enable_budget ? 1 : 0

  name              = var.budget_name
  budget_type       = "COST"
  limit_amount      = var.budget_amount
  limit_unit        = "USD"
  time_period_start = "2026-01-01_00:00"
  time_unit         = "MONTHLY"

  # Alert 1: 80% of budget actually accrued
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.notification_emails
  }

  # Alert 2: 100% of budget forecasted
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = var.notification_emails
  }

  # Alert 3: 100% of budget actually accrued
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.notification_emails
  }
}
