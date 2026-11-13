variable "bucket_name" {
  type        = string
  description = "Name of the S3 bucket for static frontend files"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.bucket_name))
    error_message = "bucket_name must follow standard AWS S3 DNS naming rules."
  }
}

variable "cloudfront_distribution_arn" {
  type        = string
  description = "ARN of the CloudFront distribution allowed to read frontend assets via OAC"
  default     = null

  validation {
    condition     = var.cloudfront_distribution_arn == null || can(regex("^arn:[a-z0-9-]+:cloudfront::[0-9]{12}:distribution/[A-Z0-9]+$", var.cloudfront_distribution_arn))
    error_message = "cloudfront_distribution_arn must be an exact distribution ARN without wildcards, or null."
  }
}
