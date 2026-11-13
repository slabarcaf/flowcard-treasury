variable "log_group_names" {
  type        = list(string)
  description = "List of CloudWatch Log Group names to create"
  default = [
    "/ecs/flowcard-node-gateway",
    "/ecs/flowcard-go-api",
    "/ecs/flowcard-go-worker",
    "/ecs/flowcard-migration"
  ]
}

variable "retention_in_days" {
  type        = number
  description = "CloudWatch log retention period in days (must match an AWS supported retention value)"
  default     = 14

  validation {
    condition     = contains([1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653], var.retention_in_days)
    error_message = "retention_in_days must be one of the AWS CloudWatch supported retention values (e.g., 7, 14, 30, 60, 90, 365)."
  }
}
