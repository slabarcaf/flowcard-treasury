variable "vpc_id" {
  type        = string
  description = "ID of the demo VPC"
}

variable "vpc_cidr" {
  type        = string
  description = "IPv4 CIDR block of the demo VPC (used for staged ingress before CloudFront VPC Origin in Task 7)"

  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr must be a valid IPv4 CIDR string."
  }
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Subnets spanning at least 2 AZs for the private ALB"

  validation {
    condition     = length(var.private_subnet_ids) >= 2
    error_message = "ALB requires subnets spanning at least two Availability Zones."
  }
}

variable "node_port" {
  type        = number
  description = "Target port on which the Node gateway listens"
  default     = 3000

  validation {
    condition     = floor(var.node_port) == var.node_port && var.node_port > 0 && var.node_port <= 65535
    error_message = "node_port must be a valid TCP port between 1 and 65535."
  }
}

variable "node_security_group_id" {
  type        = string
  description = "Security group ID of the Node gateway task (ALB egress is restricted strictly to this SG)"
}

variable "enable_vpc_origin_sg_only" {
  type        = bool
  description = "When true, eliminates broad VPC CIDR ingress and restricts ALB ingress strictly to CloudFront VPC Origin SG / prefix list"
  default     = false
}

variable "vpc_origin_security_group_id" {
  type        = string
  description = "Security group ID of the CloudFront VPC Origin service-managed ENIs"
  default     = null
}

variable "cloudfront_prefix_list_id" {
  type        = string
  description = "Managed prefix list ID for CloudFront origin-facing IPs"
  default     = null
}
