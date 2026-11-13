variable "enable_budget" {
  type        = bool
  description = "Explicit flag to enable the AWS Budget resource (disabled by default until authorized)"
  default     = false
}

variable "budget_amount" {
  type        = string
  description = "Monthly budget limit in USD"
  default     = "150.0"

  validation {
    condition     = can(regex("^\\d+(\\.\\d+)?$", var.budget_amount)) && tonumber(var.budget_amount) > 0
    error_message = "budget_amount must be a valid positive numerical string."
  }
}

variable "notification_emails" {
  type        = list(string)
  description = "List of verified email addresses to receive budget notifications. Required when enable_budget is true."
  default     = []

  validation {
    condition     = var.enable_budget == false || (length(var.notification_emails) > 0 && can([for email in var.notification_emails : regex("^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$", email)]))
    error_message = "notification_emails must be non-empty and contain valid email addresses when enable_budget is true."
  }
}

variable "budget_name" {
  type        = string
  description = "Name of the AWS Budget"
  default     = "flowcard-demo-monthly-budget"
}
