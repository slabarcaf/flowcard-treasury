variable "vpc_cidr" {
  type        = string
  description = "IPv4 CIDR block for the VPC"
  default     = "10.0.0.0/16"

  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr must be a valid IPv4 CIDR string."
  }
}

variable "availability_zones" {
  type        = list(string)
  description = "Availability zones to place subnets (must contain at least 2 unique AZs)"
  default     = ["us-west-2a", "us-west-2b"]

  validation {
    condition     = length(var.availability_zones) >= 2 && length(distinct(var.availability_zones)) == length(var.availability_zones)
    error_message = "availability_zones must contain at least 2 distinct, unique AZ names."
  }
}

variable "public_subnet_cidrs" {
  type        = list(string)
  description = "CIDR blocks for public subnets (must contain at least 2 distinct CIDRs)"
  default     = ["10.0.1.0/24", "10.0.2.0/24"]

  validation {
    condition = (
      length(var.public_subnet_cidrs) >= 2 &&
      length(distinct(var.public_subnet_cidrs)) == length(var.public_subnet_cidrs) &&
      can([for c in var.public_subnet_cidrs : cidrhost(c, 0)])
    )
    error_message = "public_subnet_cidrs must contain at least 2 distinct, valid IPv4 CIDR blocks."
  }
}

variable "private_subnet_cidrs" {
  type        = list(string)
  description = "CIDR blocks for private application subnets (at least 2 distinct CIDRs)"
  default     = ["10.0.11.0/24", "10.0.12.0/24"]

  validation {
    condition = (
      length(var.private_subnet_cidrs) >= 2 &&
      length(distinct(var.private_subnet_cidrs)) == length(var.private_subnet_cidrs) &&
      can([for c in var.private_subnet_cidrs : cidrhost(c, 0)])
    )
    error_message = "private_subnet_cidrs must contain at least 2 distinct, valid IPv4 CIDR blocks."
  }
}
