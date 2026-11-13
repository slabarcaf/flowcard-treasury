output "bucket_name" {
  value       = aws_s3_bucket.documents.id
  description = "Name of the onboarding documents S3 bucket"
}

output "bucket_arn" {
  value       = aws_s3_bucket.documents.arn
  description = "ARN of the onboarding documents S3 bucket"
}

output "kms_key_arn" {
  value       = aws_kms_key.documents.arn
  description = "ARN of the KMS customer-managed key used for document encryption"
}

output "kms_key_id" {
  value       = aws_kms_key.documents.key_id
  description = "Key ID of the KMS customer-managed key used for document encryption"
}
