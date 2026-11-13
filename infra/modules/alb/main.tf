# Security Group for the Private ALB
# Staged boundary: In Task 4, ingress is restricted to the configurable VPC CIDR (no hardcoded 10.0.*).
# In Task 7, when CloudFront VPC Origin is created, AWS manages VPC Origin ENIs with an associated SG.
# At that stage, this ingress rule will be narrowed directly to the CloudFront VPC Origin SG
# (see https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-vpc-origins.html).
# ALB egress is strictly narrowed to the Node gateway task security group on node_port (zero 0.0.0.0/0 egress).
resource "aws_security_group" "alb" {
  name        = "flowcard-demo-alb-sg"
  description = "Security group for private ALB serving Node gateway to CloudFront VPC origin"
  vpc_id      = var.vpc_id

  lifecycle {
    precondition {
      condition     = !var.enable_vpc_origin_sg_only || var.vpc_origin_security_group_id != null
      error_message = "When enable_vpc_origin_sg_only is true, vpc_origin_security_group_id must be provided to ensure ALB ingress is not empty."
    }
  }

  dynamic "ingress" {
    for_each = var.enable_vpc_origin_sg_only ? [] : [1]
    content {
      description = "Allow inbound HTTP from VPC CIDR during staging before CloudFront VPC Origin SG narrowing"
      from_port   = 80
      to_port     = 80
      protocol    = "tcp"
      cidr_blocks = [var.vpc_cidr]
    }
  }

  dynamic "ingress" {
    for_each = var.vpc_origin_security_group_id != null ? [var.vpc_origin_security_group_id] : []
    content {
      description     = "Allow inbound HTTP strictly from CloudFront VPC Origin service-managed security group"
      from_port       = 80
      to_port         = 80
      protocol        = "tcp"
      security_groups = [ingress.value]
    }
  }

  dynamic "ingress" {
    for_each = (!var.enable_vpc_origin_sg_only && var.cloudfront_prefix_list_id != null) ? [var.cloudfront_prefix_list_id] : []
    content {
      description     = "Allow inbound HTTP from CloudFront origin-facing managed prefix list (staging mode only)"
      from_port       = 80
      to_port         = 80
      protocol        = "tcp"
      prefix_list_ids = [ingress.value]
    }
  }

  egress {
    description     = "Allow outbound HTTP strictly to Node gateway task security group"
    from_port       = var.node_port
    to_port         = var.node_port
    protocol        = "tcp"
    security_groups = [var.node_security_group_id]
  }

  tags = {
    Name = "flowcard-demo-alb-sg"
  }
}

# Internal Application Load Balancer (not accessible from public Internet)
resource "aws_lb" "main" {
  name               = "flowcard-demo-alb"
  internal           = true
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.private_subnet_ids

  enable_deletion_protection = false

  tags = {
    Name = "flowcard-demo-alb"
  }
}

# Target Group pointing to Node Gateway ECS Task
resource "aws_lb_target_group" "node" {
  name        = "flowcard-demo-node-tg"
  port        = var.node_port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    enabled             = true
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  tags = {
    Name = "flowcard-demo-node-tg"
  }
}

# HTTP Listener forwarding traffic to Node target group
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.node.arn
  }
}
