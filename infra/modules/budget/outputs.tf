output "budget_name" {
  value       = length(aws_budgets_budget.monthly) > 0 ? aws_budgets_budget.monthly[0].name : null
  description = "Name of the configured AWS budget, or null if budget resource is disabled"
}

output "budget_limit" {
  value       = length(aws_budgets_budget.monthly) > 0 ? "${aws_budgets_budget.monthly[0].limit_amount} ${aws_budgets_budget.monthly[0].limit_unit}" : null
  description = "Configured budget amount and currency, or null if disabled"
}
