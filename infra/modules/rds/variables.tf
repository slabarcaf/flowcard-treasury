variable "vpc_id" {
  type        = string
  description = "ID of the demo VPC"
}

variable "database_subnet_ids" {
  type        = list(string)
  description = "Subnet IDs spanning at least two AZs for the RDS DB subnet group"

  validation {
    condition     = length(var.database_subnet_ids) >= 2
    error_message = "RDS DB subnet group requires subnets spanning at least two Availability Zones."
  }
}

variable "authorized_security_group_ids" {
  type        = list(string)
  description = "List of task security group IDs authorized to connect to PostgreSQL on port 5432"

  validation {
    condition     = length(var.authorized_security_group_ids) > 0
    error_message = "authorized_security_group_ids must contain at least one authorized task security group ID."
  }
}

variable "engine_version" {
  type        = string
  description = "PostgreSQL engine version (e.g. 17.4; parameterized pending regional live availability gate)"
  default     = "17.4"
}

variable "allocated_storage" {
  type        = number
  description = "Allocated storage size in gigabytes (gp3)"
  default     = 20

  validation {
    condition     = floor(var.allocated_storage) == var.allocated_storage && var.allocated_storage >= 20
    error_message = "allocated_storage must be an integer of at least 20 GB for PostgreSQL."
  }
}

variable "instance_class" {
  type        = string
  description = "Database instance compute and memory capacity"
  default     = "db.t4g.micro"
}

variable "database_name" {
  type        = string
  description = "Name of the default PostgreSQL database"
  default     = "flowcard_treasury"
}

variable "master_username" {
  type        = string
  description = "Master administrative username for RDS"
  default     = "flowcard_admin"
}

variable "backup_retention_period" {
  type        = number
  description = "Backup retention period in days (1-35 days for RDS)"
  default     = 7

  validation {
    condition     = floor(var.backup_retention_period) == var.backup_retention_period && var.backup_retention_period >= 1 && var.backup_retention_period <= 35
    error_message = "backup_retention_period must be an integer between 1 and 35 days."
  }
}

variable "deletion_protection" {
  type        = bool
  description = "Enforce deletion protection on production/demo database"
  default     = true
}

variable "skip_final_snapshot" {
  type        = bool
  description = "Whether to skip creating a final snapshot on deletion"
  default     = false
}

variable "final_snapshot_identifier_prefix" {
  type        = string
  description = "Prefix for the final snapshot identifier"
  default     = "flowcard-demo-rds-final"
}
