mock_provider "aws" {
  mock_resource "aws_s3_bucket" {
    defaults = {
      arn = "arn:aws:s3:::fixture-frontend"
    }
  }
}

run "allow_exact_distribution" {
  command = apply

  variables {
    bucket_name                 = "fixture-frontend"
    cloudfront_distribution_arn = "arn:aws:cloudfront::123456789012:distribution/EFIXTURE123"
  }

  assert {
    condition     = jsondecode(aws_s3_bucket_policy.frontend.policy).Statement[1].Condition.StringEquals["AWS:SourceArn"] == var.cloudfront_distribution_arn
    error_message = "OAC reads must be bound to the exact supplied distribution."
  }

  assert {
    condition     = jsondecode(aws_s3_bucket_policy.frontend.policy).Statement[0].Condition.Bool["aws:SecureTransport"] == "false" && aws_s3_bucket_public_access_block.frontend.block_public_policy
    error_message = "HTTPS enforcement and private bucket protections must remain enabled."
  }
}

run "no_distribution_no_read_grant" {
  command = apply

  variables {
    bucket_name                 = "fixture-frontend"
    cloudfront_distribution_arn = null
  }

  assert {
    condition     = length(jsondecode(aws_s3_bucket_policy.frontend.policy).Statement) == 1
    error_message = "Without a distribution, the policy must contain only the HTTPS denial."
  }
}

run "reject_wildcard_distribution" {
  command = plan

  variables {
    bucket_name                 = "fixture-frontend"
    cloudfront_distribution_arn = "arn:aws:cloudfront::123456789012:distribution/*"
  }

  expect_failures = [var.cloudfront_distribution_arn]
}
