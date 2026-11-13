# Virtual Private Cloud (VPC)
resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  lifecycle {
    precondition {
      condition     = length(var.public_subnet_cidrs) == length(var.availability_zones)
      error_message = "public_subnet_cidrs count must exactly match availability_zones count."
    }
    precondition {
      condition     = length(var.private_subnet_cidrs) == length(var.availability_zones)
      error_message = "private_subnet_cidrs count must exactly match availability_zones count."
    }
  }

  tags = {
    Name = "flowcard-demo-vpc"
  }
}

# Internet Gateway (required for public NAT gateway egress and CloudFront VPC Origin)
resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "flowcard-demo-igw"
  }
}

# Public Subnets across 2 Availability Zones
resource "aws_subnet" "public" {
  count = length(var.public_subnet_cidrs)

  vpc_id                  = aws_vpc.main.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = false

  tags = {
    Name = "flowcard-demo-public-${var.availability_zones[count.index]}"
    Type = "public"
  }
}

# Private Application & Data Subnets across 2 Availability Zones
resource "aws_subnet" "private" {
  count = length(var.private_subnet_cidrs)

  vpc_id                  = aws_vpc.main.id
  cidr_block              = var.private_subnet_cidrs[count.index]
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = false

  tags = {
    Name = "flowcard-demo-private-${var.availability_zones[count.index]}"
    Type = "private"
  }
}

# Route Table for Public Subnets
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }

  tags = {
    Name = "flowcard-demo-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  count = length(aws_subnet.public)

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Elastic IP for Single Demo NAT Gateway
resource "aws_eip" "nat" {
  domain     = "vpc"
  depends_on = [aws_internet_gateway.igw]

  tags = {
    Name = "flowcard-demo-nat-eip"
  }
}

# Single Demo NAT Gateway placed in the first public subnet
resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id

  depends_on = [aws_internet_gateway.igw]

  tags = {
    Name = "flowcard-demo-nat"
  }
}

# Route Table for Private Subnets routing 0.0.0.0/0 to the NAT Gateway
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }

  tags = {
    Name = "flowcard-demo-private-rt"
  }
}

resource "aws_route_table_association" "private" {
  count = length(aws_subnet.private)

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# S3 Gateway Endpoint: routes private S3 traffic directly without NAT charges
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.main.id
  service_name      = "com.amazonaws.us-west-2.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.private.id]

  tags = {
    Name = "flowcard-demo-s3-endpoint"
  }
}

# ECS Service Discovery Private DNS Namespace for Node -> Go API communication
resource "aws_service_discovery_private_dns_namespace" "internal" {
  name        = "flowcard.internal"
  description = "Private DNS namespace for FlowCard ECS service discovery"
  vpc         = aws_vpc.main.id

  tags = {
    Name = "flowcard-demo-service-discovery"
  }
}
