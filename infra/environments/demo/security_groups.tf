# ==============================================================================
# Dedicated ECS Task Security Groups & Fine-Grained Network Edge Rules
# ==============================================================================

# ------------------------------------------------------------------------------
# 1. Node Gateway Task Security Group (HTTP Edge, Webhook Inbox & Ledger)
# ------------------------------------------------------------------------------
resource "aws_security_group" "node_task" {
  name        = "flowcard-demo-node-task-sg"
  description = "Security group for Node gateway ECS tasks"
  vpc_id      = module.vpc.vpc_id

  tags = {
    Name = "flowcard-demo-node-task-sg"
  }
}

# Ingress: Allow HTTP on node_port strictly from the internal ALB
resource "aws_security_group_rule" "node_ingress_from_alb" {
  type                     = "ingress"
  description              = "Allow HTTP from internal ALB to Node gateway"
  from_port                = var.node_port
  to_port                  = var.node_port
  protocol                 = "tcp"
  source_security_group_id = module.alb.alb_security_group_id
  security_group_id        = aws_security_group.node_task.id
}

# Egress: Allow TCP 8080 strictly to Go API task
resource "aws_security_group_rule" "node_egress_to_go_api" {
  type                     = "egress"
  description              = "Allow HTTP upstream requests to Go API"
  from_port                = 8080
  to_port                  = 8080
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.go_api_task.id
  security_group_id        = aws_security_group.node_task.id
}

# Egress: Allow PostgreSQL 5432 strictly to RDS database (for webhook inbox, ledger, and forwarding)
resource "aws_security_group_rule" "node_egress_to_rds" {
  type                     = "egress"
  description              = "Allow PostgreSQL connections strictly to RDS instance"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = module.rds.security_group_id
  security_group_id        = aws_security_group.node_task.id
}

# Egress: DNS resolution for ECS service discovery (flowcard.internal) and VPC resolver
resource "aws_security_group_rule" "node_egress_dns_udp" {
  type              = "egress"
  description       = "Allow DNS UDP queries for internal service discovery"
  from_port         = 53
  to_port           = 53
  protocol          = "udp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.node_task.id
}

resource "aws_security_group_rule" "node_egress_dns_tcp" {
  type              = "egress"
  description       = "Allow DNS TCP queries for internal service discovery"
  from_port         = 53
  to_port           = 53
  protocol          = "tcp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.node_task.id
}

# Egress: HTTPS outbound via NAT for AWS API endpoints and external Airwallex sandbox
resource "aws_security_group_rule" "node_egress_https" {
  type              = "egress"
  description       = "Allow HTTPS outbound for AWS APIs and external Airwallex sandbox"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.node_task.id
}

# ------------------------------------------------------------------------------
# 2. Go API Task Security Group (Inbound from Node only; Outbound to RDS & AWS APIs)
# ------------------------------------------------------------------------------
resource "aws_security_group" "go_api_task" {
  name        = "flowcard-demo-api-task-sg"
  description = "Security group for Go API ECS tasks"
  vpc_id      = module.vpc.vpc_id

  tags = {
    Name = "flowcard-demo-api-task-sg"
  }
}

# Ingress: Allow TCP 8080 strictly from Node gateway
resource "aws_security_group_rule" "go_api_ingress_from_node" {
  type                     = "ingress"
  description              = "Allow HTTP from Node gateway to Go API"
  from_port                = 8080
  to_port                  = 8080
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.node_task.id
  security_group_id        = aws_security_group.go_api_task.id
}

# Egress: Allow PostgreSQL 5432 strictly to RDS database
resource "aws_security_group_rule" "go_api_egress_to_rds" {
  type                     = "egress"
  description              = "Allow PostgreSQL connections strictly to RDS instance"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = module.rds.security_group_id
  security_group_id        = aws_security_group.go_api_task.id
}

resource "aws_security_group_rule" "go_api_egress_dns_udp" {
  type              = "egress"
  description       = "Allow DNS UDP queries for AWS services and RDS endpoint"
  from_port         = 53
  to_port           = 53
  protocol          = "udp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.go_api_task.id
}

resource "aws_security_group_rule" "go_api_egress_dns_tcp" {
  type              = "egress"
  description       = "Allow DNS TCP queries for AWS services and RDS endpoint"
  from_port         = 53
  to_port           = 53
  protocol          = "tcp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.go_api_task.id
}

resource "aws_security_group_rule" "go_api_egress_https" {
  type              = "egress"
  description       = "Allow HTTPS outbound for KMS, Airwallex sandbox, and S3 Gateway"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.go_api_task.id
}

# ------------------------------------------------------------------------------
# 3. Go Worker Task Security Group (Zero Ingress; Outbound to RDS, KMS & Airwallex)
# ------------------------------------------------------------------------------
resource "aws_security_group" "go_worker_task" {
  name        = "flowcard-demo-worker-task-sg"
  description = "Security group for Go Worker ECS tasks (zero inbound ingress)"
  vpc_id      = module.vpc.vpc_id

  tags = {
    Name = "flowcard-demo-worker-task-sg"
  }
}

# Egress: Allow PostgreSQL 5432 strictly to RDS database
resource "aws_security_group_rule" "go_worker_egress_to_rds" {
  type                     = "egress"
  description              = "Allow PostgreSQL connections strictly to RDS instance"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = module.rds.security_group_id
  security_group_id        = aws_security_group.go_worker_task.id
}

resource "aws_security_group_rule" "go_worker_egress_dns_udp" {
  type              = "egress"
  description       = "Allow DNS UDP queries"
  from_port         = 53
  to_port           = 53
  protocol          = "udp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.go_worker_task.id
}

resource "aws_security_group_rule" "go_worker_egress_dns_tcp" {
  type              = "egress"
  description       = "Allow DNS TCP queries"
  from_port         = 53
  to_port           = 53
  protocol          = "tcp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.go_worker_task.id
}

resource "aws_security_group_rule" "go_worker_egress_https" {
  type              = "egress"
  description       = "Allow HTTPS outbound for KMS decrypt and Airwallex sandbox APIs"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.go_worker_task.id
}

# ------------------------------------------------------------------------------
# 4. Migration Task Security Group (Zero Ingress; Outbound to RDS & AWS APIs)
# ------------------------------------------------------------------------------
resource "aws_security_group" "migration_task" {
  name        = "flowcard-demo-migration-task-sg"
  description = "Security group for Database Migration ECS tasks (zero inbound ingress)"
  vpc_id      = module.vpc.vpc_id

  tags = {
    Name = "flowcard-demo-migration-task-sg"
  }
}

# Egress: Allow PostgreSQL 5432 strictly to RDS database
resource "aws_security_group_rule" "migration_egress_to_rds" {
  type                     = "egress"
  description              = "Allow PostgreSQL connections strictly to RDS instance"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = module.rds.security_group_id
  security_group_id        = aws_security_group.migration_task.id
}

resource "aws_security_group_rule" "migration_egress_dns_udp" {
  type              = "egress"
  description       = "Allow DNS UDP queries for RDS endpoint resolution"
  from_port         = 53
  to_port           = 53
  protocol          = "udp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.migration_task.id
}

resource "aws_security_group_rule" "migration_egress_dns_tcp" {
  type              = "egress"
  description       = "Allow DNS TCP queries for RDS endpoint resolution"
  from_port         = 53
  to_port           = 53
  protocol          = "tcp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.migration_task.id
}

resource "aws_security_group_rule" "migration_egress_https" {
  type              = "egress"
  description       = "Allow HTTPS outbound for Secrets Manager resolution"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.migration_task.id
}
