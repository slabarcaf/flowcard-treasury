output "bucket_name" {
  value       = aws_s3_bucket.frontend.id
  description = "Name of the static frontend S3 bucket"
}

output "bucket_arn" {
  value       = aws_s3_bucket.frontend.arn
  description = "ARN of the static frontend S3 bucket"
}

output "bucket_regional_domain_name" {
  value       = aws_s3_bucket.frontend.bucket_regional_domain_name
  description = "Regional domain name of the frontend bucket for CloudFront origin"
}
