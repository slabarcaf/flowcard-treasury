variable "repository_names" {
  type        = list(string)
  description = "List of ECR repository names to create"
  default = [
    "flowcard-node-gateway",
    "flowcard-go-api",
    "flowcard-go-worker",
    "flowcard-migration"
  ]
}

variable "untagged_image_retention_days" {
  type        = number
  description = "Days before expiring untagged container images"
  default     = 7

  validation {
    condition     = floor(var.untagged_image_retention_days) == var.untagged_image_retention_days && var.untagged_image_retention_days > 0
    error_message = "untagged_image_retention_days must be a positive integer."
  }
}
