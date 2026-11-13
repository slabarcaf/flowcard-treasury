output "vpc_id" {
  value       = aws_vpc.main.id
  description = "ID of the created demo VPC"
}

output "public_subnet_ids" {
  value       = aws_subnet.public[*].id
  description = "IDs of the public subnets (spanning 2 AZs)"
}

output "private_subnet_ids" {
  value       = aws_subnet.private[*].id
  description = "IDs of the private application & data subnets (spanning 2 AZs)"
}

output "nat_gateway_id" {
  value       = aws_nat_gateway.main.id
  description = "ID of the single managed NAT gateway"
}

output "nat_public_ip" {
  value       = aws_eip.nat.public_ip
  description = "Public IP address of the managed NAT gateway"
}

output "service_discovery_namespace_id" {
  value       = aws_service_discovery_private_dns_namespace.internal.id
  description = "ID of the ECS private service discovery DNS namespace"
}

output "service_discovery_namespace_name" {
  value       = aws_service_discovery_private_dns_namespace.internal.name
  description = "Name of the ECS private service discovery DNS namespace (flowcard.internal)"
}
