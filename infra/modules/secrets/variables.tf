variable "secret_names" {
  type        = list(string)
  description = "List of Secrets Manager container names to manage"
  default = [
    "flowcard/db/app",
    "flowcard/db/migrate",
    "flowcard/airwallex",
    "flowcard/operator",
    "flowcard/service-auth"
  ]
}

variable "recovery_window_in_days" {
  type        = number
  description = "Number of days that Secrets Manager waits before permanent secret deletion"
  default     = 7

  validation {
    condition     = floor(var.recovery_window_in_days) == var.recovery_window_in_days && var.recovery_window_in_days >= 7 && var.recovery_window_in_days <= 30
    error_message = "recovery_window_in_days must be an integer between 7 and 30 days."
  }
}
