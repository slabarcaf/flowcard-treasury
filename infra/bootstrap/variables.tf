variable "aws_region" {
  type        = string
  description = "AWS region for deployment and backend resources"
  default     = "us-west-2"
}

variable "allowed_account_ids" {
  type        = list(string)
  description = "Target AWS Account ID(s). Must be non-empty and contain 12-digit account strings."

  validation {
    condition     = length(var.allowed_account_ids) > 0 && can([for id in var.allowed_account_ids : regex("^\\d{12}$", id)])
    error_message = "allowed_account_ids must be a non-empty list of valid 12-digit AWS Account IDs."
  }
}

variable "state_bucket_name" {
  type        = string
  description = "Name of the S3 bucket for Terraform remote state"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.state_bucket_name))
    error_message = "state_bucket_name must follow standard AWS S3 DNS naming rules."
  }
}

variable "kms_key_deletion_window_in_days" {
  type        = number
  description = "Waiting period before KMS key deletion (7-30 days)"
  default     = 30

  validation {
    condition     = floor(var.kms_key_deletion_window_in_days) == var.kms_key_deletion_window_in_days && var.kms_key_deletion_window_in_days >= 7 && var.kms_key_deletion_window_in_days <= 30
    error_message = "kms_key_deletion_window_in_days must be an integer between 7 and 30 days."
  }
}
