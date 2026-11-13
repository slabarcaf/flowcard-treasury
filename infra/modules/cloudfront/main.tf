# ------------------------------------------------------------------------------
# CloudFront Origin Access Control (OAC) for S3 Frontend Bucket
# ------------------------------------------------------------------------------
resource "aws_cloudfront_origin_access_control" "frontend" {
  name                              = "flowcard-demo-frontend-oac"
  description                       = "Origin Access Control for FlowCard S3 frontend assets"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

# ------------------------------------------------------------------------------
# CloudFront VPC Origin for Private Internal Application Load Balancer
# Architecture:
# - Viewer Edge: HTTPS enforced with TLS 1.2/1.3 and HSTS.
# - Origin Transport: Private HTTP over AWS-managed VPC Origin ENIs to internal ALB (port 80).
#   origin_protocol_policy is set to "http-only". The origin_ssl_protocols block below is
#   strictly required by the AWS provider schema for vpc_origin_endpoint_config, but is
#   unused at runtime because origin_protocol_policy is "http-only". No encrypted origin
#   transport is claimed or configured; private HTTP within the VPC boundary is the intended design.
# ------------------------------------------------------------------------------
resource "aws_cloudfront_vpc_origin" "alb" {
  vpc_origin_endpoint_config {
    name                   = "flowcard-demo-alb-vpc-origin"
    arn                    = var.alb_arn
    http_port              = var.alb_http_port
    https_port             = 443
    origin_protocol_policy = "http-only"

    origin_ssl_protocols {
      quantity = 1
      items    = ["TLSv1.2"]
    }
  }

  tags = {
    Name = "flowcard-demo-alb-vpc-origin"
  }
}

# ------------------------------------------------------------------------------
# CloudFront Function: SPA Client-Side Navigation Rewrite
# Rewrites navigation routes without extensions to /index.html.
# Never touches API, docs, webhooks, or static assets with file extensions.
# Enforces method check (GET/HEAD only), reserved path guards, and Accept header guards.
# ------------------------------------------------------------------------------
resource "aws_cloudfront_function" "spa_rewrite" {
  name    = "flowcard-demo-spa-rewrite"
  runtime = "cloudfront-js-2.0"
  comment = "Rewrite SPA navigation routes without extensions to index.html"
  publish = true
  code    = <<-EOT
    function handler(event) {
        var request = event.request;
        var method = request.method;

        // Guard 1: Only rewrite GET and HEAD requests for client-side HTML navigation.
        // Non-GET requests (POST, PUT, DELETE, PATCH) should never return index.html.
        if (method !== 'GET' && method !== 'HEAD') {
            return request;
        }

        var uri = request.uri;

        // Guard 2: Explicit reserved backend API, docs, webhooks, and static asset prefixes.
        // Requests matching these prefixes must never be rewritten to index.html.
        if (uri === '/api' || uri.startsWith('/api/') ||
            uri === '/docs' || uri.startsWith('/docs/') ||
            uri === '/webhooks' || uri.startsWith('/webhooks/') ||
            uri === '/assets' || uri.startsWith('/assets/')) {
            return request;
        }

        // Guard 3: Accept header check.
        // If client explicitly requests application/json and does not accept text/html,
        // do not rewrite to index.html (return request untouched so origin can return 404 JSON).
        // Default Accept-absent semantics: If Accept header is missing or includes text/html,
        // extensionless navigation paths are rewritten to /index.html for browser navigation.
        var headers = request.headers || {};
        var accept = (headers.accept && headers.accept.value) ? headers.accept.value : '';
        if (accept.indexOf('application/json') !== -1 && accept.indexOf('text/html') === -1) {
            return request;
        }

        // Guard 4: Do not rewrite paths with file extensions (e.g. /favicon.ico, /vite.svg, /robots.txt).
        if (!uri.includes('.') || uri.indexOf('.') !== -1) {
            if (uri.indexOf('.') !== -1) {
                return request;
            }
        }

        // Rewrite valid extensionless SPA navigation route to /index.html
        request.uri = '/index.html';
        return request;
    }
  EOT
}

# ------------------------------------------------------------------------------
# Cache Policy: No-Cache (TTL = 0) for Dynamic API, Docs, and Webhooks
# ------------------------------------------------------------------------------
resource "aws_cloudfront_cache_policy" "no_cache" {
  name        = "flowcard-demo-api-no-cache"
  comment     = "Disable caching for dynamic API, docs, and webhook paths (TTL 0)"
  default_ttl = 0
  max_ttl     = 0
  min_ttl     = 0

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
    enable_accept_encoding_brotli = false
    enable_accept_encoding_gzip   = false
  }
}

# ------------------------------------------------------------------------------
# Cache Policy: Immutable Static Assets (/assets/*) with Long TTL
# ------------------------------------------------------------------------------
resource "aws_cloudfront_cache_policy" "static_assets" {
  name        = "flowcard-demo-static-assets-cache"
  comment     = "Cache immutable static assets with long TTL"
  default_ttl = 86400
  max_ttl     = 31536000
  min_ttl     = 0

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
    enable_accept_encoding_brotli = true
    enable_accept_encoding_gzip   = true
  }
}

# ------------------------------------------------------------------------------
# Origin Request Policy: Forward All Viewer Headers, Cookies, Query Strings
# ------------------------------------------------------------------------------
resource "aws_cloudfront_origin_request_policy" "alb_gateway" {
  name    = "flowcard-demo-alb-gateway-orp"
  comment = "Forward all viewer headers, cookies, and query strings to ALB Node gateway"

  cookies_config {
    cookie_behavior = "all"
  }

  headers_config {
    header_behavior = "allViewer"
  }

  query_strings_config {
    query_string_behavior = "all"
  }
}

# ------------------------------------------------------------------------------
# Response Headers Policy: Enforce Security Headers & CSP
# ------------------------------------------------------------------------------
resource "aws_cloudfront_response_headers_policy" "security_headers" {
  name    = "flowcard-demo-security-headers"
  comment = "Security headers for FlowCard demonstration distribution"

  security_headers_config {
    content_type_options {
      override = true
    }
    frame_options {
      frame_option = "DENY"
      override     = true
    }
    referrer_policy {
      referrer_policy = "strict-origin-when-cross-origin"
      override        = true
    }
    strict_transport_security {
      access_control_max_age_sec = 31536000
      include_subdomains         = true
      preload                    = true
      override                   = true
    }
    xss_protection {
      mode_block = true
      protection = true
      override   = true
    }
    content_security_policy {
      content_security_policy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none';"
      override                = false
    }
  }
}

# ------------------------------------------------------------------------------
# CloudFront Distribution: Unified HTTPS Gateway & Static Frontend
# ------------------------------------------------------------------------------
resource "aws_cloudfront_distribution" "main" {
  enabled             = var.enabled
  is_ipv6_enabled     = true
  comment             = "FlowCard demonstration HTTPS frontend and API gateway distribution"
  price_class         = var.price_class
  http_version        = "http2and3"
  default_root_object = "index.html"

  # Origin 1: S3 Frontend Bucket (OAC)
  origin {
    domain_name              = var.s3_frontend_bucket_regional_domain_name
    origin_id                = "s3-frontend"
    origin_access_control_id = aws_cloudfront_origin_access_control.frontend.id
  }

  # Origin 2: Private ALB VPC Origin
  origin {
    domain_name = var.alb_dns_name
    origin_id   = "alb-vpc-origin"

    vpc_origin_config {
      vpc_origin_id = aws_cloudfront_vpc_origin.alb.id
    }
  }

  # Ordered Behavior 1: /api -> Private ALB VPC Origin (No Cache, Pass-Through)
  # Ensures bare /api requests reach Node gateway (returning 404 JSON for unknown endpoints, not HTML)
  ordered_cache_behavior {
    path_pattern     = "/api"
    target_origin_id = "alb-vpc-origin"

    viewer_protocol_policy     = "redirect-to-https"
    allowed_methods            = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id
    origin_request_policy_id   = aws_cloudfront_origin_request_policy.alb_gateway.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.security_headers.id
    compress                   = true
  }

  # Ordered Behavior 2: /api/* -> Private ALB VPC Origin (No Cache, Pass-Through)
  ordered_cache_behavior {
    path_pattern     = "/api/*"
    target_origin_id = "alb-vpc-origin"

    viewer_protocol_policy     = "redirect-to-https"
    allowed_methods            = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id
    origin_request_policy_id   = aws_cloudfront_origin_request_policy.alb_gateway.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.security_headers.id
    compress                   = true
  }

  # Ordered Behavior 2: /docs/api* -> Private ALB VPC Origin (No Cache, Public Docs)
  ordered_cache_behavior {
    path_pattern     = "/docs/api*"
    target_origin_id = "alb-vpc-origin"

    viewer_protocol_policy     = "redirect-to-https"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id
    origin_request_policy_id   = aws_cloudfront_origin_request_policy.alb_gateway.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.security_headers.id
    compress                   = true
  }

  # Ordered Behavior 3: /webhooks/airwallex -> Private ALB VPC Origin (Raw Uncompressed Payload)
  ordered_cache_behavior {
    path_pattern     = "/webhooks/airwallex"
    target_origin_id = "alb-vpc-origin"

    viewer_protocol_policy   = "redirect-to-https"
    allowed_methods          = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = aws_cloudfront_cache_policy.no_cache.id
    origin_request_policy_id = aws_cloudfront_origin_request_policy.alb_gateway.id
    compress                 = false
  }

  # Ordered Behavior 4: /assets/* -> S3 Frontend (Immutable Bundles, Long Cache)
  ordered_cache_behavior {
    path_pattern     = "/assets/*"
    target_origin_id = "s3-frontend"

    viewer_protocol_policy     = "redirect-to-https"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD"]
    cache_policy_id            = aws_cloudfront_cache_policy.static_assets.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.security_headers.id
    compress                   = true
  }

  # Default Cache Behavior: S3 Frontend with SPA Navigation Rewrite Function
  default_cache_behavior {
    target_origin_id = "s3-frontend"

    viewer_protocol_policy     = "redirect-to-https"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD"]
    cache_policy_id            = aws_cloudfront_cache_policy.no_cache.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.security_headers.id
    compress                   = true

    function_association {
      event_type   = "viewer-request"
      function_arn = aws_cloudfront_function.spa_rewrite.arn
    }
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }

  tags = {
    Name = "flowcard-demo-cloudfront"
  }
}
