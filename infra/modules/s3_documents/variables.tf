variable "bucket_name" {
  type        = string
  description = "Name of the S3 bucket for private onboarding documents"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.bucket_name))
    error_message = "bucket_name must follow standard AWS S3 DNS naming rules."
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
