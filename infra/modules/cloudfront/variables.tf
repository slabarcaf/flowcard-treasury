variable "enabled" {
  type        = bool
  description = "Whether the CloudFront distribution is enabled"
  default     = true
}

variable "s3_frontend_bucket_id" {
  type        = string
  description = "Name/ID of the static frontend S3 bucket"
}

variable "s3_frontend_bucket_arn" {
  type        = string
  description = "ARN of the static frontend S3 bucket"
}

variable "s3_frontend_bucket_regional_domain_name" {
  type        = string
  description = "Regional domain name of the static frontend S3 bucket (for OAC origin)"
}

variable "alb_arn" {
  type        = string
  description = "ARN of the private internal Application Load Balancer"
}

variable "alb_dns_name" {
  type        = string
  description = "DNS name of the private internal Application Load Balancer"
}

variable "alb_http_port" {
  type        = number
  description = "HTTP port of the private ALB"
  default     = 80
}

variable "price_class" {
  type        = string
  description = "CloudFront price class"
  default     = "PriceClass_100"
}
