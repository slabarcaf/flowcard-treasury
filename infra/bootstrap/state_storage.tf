# KMS customer-managed key for encrypting Terraform state at-rest
resource "aws_kms_key" "state" {
  description             = "KMS Key for FlowCard Terraform remote state encryption"
  deletion_window_in_days = var.kms_key_deletion_window_in_days
  enable_key_rotation     = true

  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name = "flowcard-state-kms-key"
  }
}

resource "aws_kms_alias" "state" {
  name          = "alias/flowcard-terraform-state"
  target_key_id = aws_kms_key.state.key_id
}

# S3 bucket for versioned, encrypted Terraform state
resource "aws_s3_bucket" "state" {
  bucket = var.state_bucket_name

  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name = "flowcard-terraform-state"
  }
}

# Enable native S3 bucket versioning
resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id

  versioning_configuration {
    status = "Enabled"
  }
}

# Enforce server-side encryption with customer-managed KMS key
resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.state.arn
      sse_algorithm     = "aws:kms"
    }
    bucket_key_enabled = true
  }
}

# Block all public access to the state bucket
resource "aws_s3_bucket_public_access_block" "state" {
  bucket = aws_s3_bucket.state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Deny insecure HTTP requests (enforce HTTPS transport)
resource "aws_s3_bucket_policy" "state" {
  bucket = aws_s3_bucket.state.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "EnforceTLSRequestsOnly"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource = [
          aws_s3_bucket.state.arn,
          "${aws_s3_bucket.state.arn}/*"
        ]
        Condition = {
          Bool = {
            "aws:SecureTransport" = "false"
          }
        }
      }
    ]
  })
}
