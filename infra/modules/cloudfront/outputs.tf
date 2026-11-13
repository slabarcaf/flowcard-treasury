output "cloudfront_distribution_id" {
  value       = aws_cloudfront_distribution.main.id
  description = "ID of the CloudFront distribution"
}

output "cloudfront_distribution_arn" {
  value       = aws_cloudfront_distribution.main.arn
  description = "ARN of the CloudFront distribution"
}

output "cloudfront_domain_name" {
  value       = aws_cloudfront_distribution.main.domain_name
  description = "Default generated domain name of the CloudFront distribution"
}

output "vpc_origin_id" {
  value       = aws_cloudfront_vpc_origin.alb.id
  description = "ID of the CloudFront VPC Origin for the private ALB"
}

output "origin_access_control_id" {
  value       = aws_cloudfront_origin_access_control.frontend.id
  description = "ID of the CloudFront Origin Access Control for S3 frontend"
}
