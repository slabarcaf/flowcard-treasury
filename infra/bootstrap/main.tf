terraform {
  # Minimum Terraform 1.10 required for native S3 backend state locking (use_lockfile = true)
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "registry.terraform.io/hashicorp/aws"
      version = "~> 5.80"
    }
  }

  backend "local" {
    path = "terraform.tfstate"
  }
}

provider "aws" {
  region = var.aws_region

  # Account guard: enforces apply only to authorized personal AWS account
  allowed_account_ids = var.allowed_account_ids

  default_tags {
    tags = {
      Project     = "flowcard"
      Environment = "bootstrap"
      ManagedBy   = "terraform"
    }
  }
}
