# Security Group for PostgreSQL Database
# Inbound TCP 5432 is restricted strictly to explicitly authorized task security groups.
# Zero CIDR block ingress is permitted. Egress is omitted as RDS instances only respond to inbound queries.
resource "aws_security_group" "rds" {
  name        = "flowcard-demo-rds-sg"
  description = "Security group for private RDS PostgreSQL database"
  vpc_id      = var.vpc_id

  ingress {
    description     = "Allow inbound PostgreSQL strictly from authorized task security groups"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = var.authorized_security_group_ids
  }

  tags = {
    Name = "flowcard-demo-rds-sg"
  }
}

# DB Subnet Group spanning 2 AZs (AWS requirement even for Single-AZ RDS)
resource "aws_db_subnet_group" "main" {
  name        = "flowcard-demo-db-subnet-group"
  description = "Database subnet group spanning multiple AZs for FlowCard demo"
  subnet_ids  = var.database_subnet_ids

  tags = {
    Name = "flowcard-demo-db-subnet-group"
  }
}

# Parameter Group enforcing PostgreSQL 17 settings and SSL connection requirements
resource "aws_db_parameter_group" "pg17" {
  name        = "flowcard-demo-pg17-params"
  family      = "postgres17"
  description = "Parameter group for PostgreSQL 17 enforcing SSL"

  parameter {
    name         = "rds.force_ssl"
    value        = "1"
    apply_method = "pending-reboot"
  }

  tags = {
    Name = "flowcard-demo-pg17-params"
  }
}

# RDS PostgreSQL Instance (Single-AZ, gp3 storage, KMS encryption)
# Uses RDS-managed master password via AWS Secrets Manager (manage_master_user_password = true)
# No plaintext password or random_password is stored in Terraform HCL or state.
# PostgreSQL version is parameterized (var.engine_version); regional live availability gate required before apply.
resource "aws_db_instance" "main" {
  identifier     = "flowcard-demo-postgres"
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  allocated_storage = var.allocated_storage
  storage_type      = "gp3"
  storage_encrypted = true

  db_name  = var.database_name
  username = var.master_username

  # RDS-managed master user password stored directly in AWS Secrets Manager
  # This managed secret is reserved for initial DBA/bootstrap setup and is never granted to runtime tasks.
  manage_master_user_password = true

  multi_az               = false
  publicly_accessible    = false
  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  parameter_group_name   = aws_db_parameter_group.pg17.name

  backup_retention_period    = var.backup_retention_period
  backup_window              = "03:00-04:00"
  maintenance_window         = "Sun:04:30-Sun:05:30"
  auto_minor_version_upgrade = true
  copy_tags_to_snapshot      = true

  deletion_protection = var.deletion_protection
  skip_final_snapshot = var.skip_final_snapshot

  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.final_snapshot_identifier_prefix}-${formatdate("YYYYMMDDhhmmss", timestamp())}"

  lifecycle {
    ignore_changes = [
      final_snapshot_identifier,
    ]
  }

  tags = {
    Name = "flowcard-demo-postgres"
  }
}
