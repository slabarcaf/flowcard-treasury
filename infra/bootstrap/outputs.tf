output "state_bucket_name" {
  value       = aws_s3_bucket.state.id
  description = "Name of the S3 bucket holding Terraform remote state"
}

output "state_bucket_arn" {
  value       = aws_s3_bucket.state.arn
  description = "ARN of the S3 bucket holding Terraform remote state"
}

output "kms_key_arn" {
  value       = aws_kms_key.state.arn
  description = "ARN of the KMS customer-managed key used for state encryption"
}

output "kms_key_id" {
  value       = aws_kms_key.state.key_id
  description = "Key ID of the KMS customer-managed key used for state encryption"
}
