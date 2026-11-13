output "alb_arn" {
  value       = aws_lb.main.arn
  description = "ARN of the internal Application Load Balancer"
}

output "alb_dns_name" {
  value       = aws_lb.main.dns_name
  description = "Private DNS name of the internal Application Load Balancer"
}

output "alb_security_group_id" {
  value       = aws_security_group.alb.id
  description = "Security group ID of the internal ALB"
}

output "node_target_group_arn" {
  value       = aws_lb_target_group.node.arn
  description = "ARN of the target group for Node gateway ECS tasks"
}

output "alb_listener_arn" {
  value       = aws_lb_listener.http.arn
  description = "ARN of the HTTP listener on the internal ALB"
}
