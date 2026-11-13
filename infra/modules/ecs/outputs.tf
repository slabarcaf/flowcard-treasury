output "cluster_id" {
  description = "ID of the ECS Fargate cluster"
  value       = aws_ecs_cluster.cluster.id
}

output "cluster_name" {
  description = "Name of the ECS Fargate cluster"
  value       = aws_ecs_cluster.cluster.name
}

output "cluster_arn" {
  description = "ARN of the ECS Fargate cluster"
  value       = aws_ecs_cluster.cluster.arn
}

output "node_service_name" {
  description = "Name of the Node gateway ECS service"
  value       = try(aws_ecs_service.node_gateway[0].name, null)
}

output "go_api_service_name" {
  description = "Name of the Go API ECS service"
  value       = try(aws_ecs_service.go_api[0].name, null)
}

output "go_worker_service_name" {
  description = "Name of the Go Worker ECS service"
  value       = try(aws_ecs_service.go_worker[0].name, null)
}

output "node_task_definition_arn" {
  description = "ARN of the Node gateway ECS task definition"
  value       = try(aws_ecs_task_definition.node_gateway[0].arn, null)
}

output "go_api_task_definition_arn" {
  description = "ARN of the Go API ECS task definition"
  value       = try(aws_ecs_task_definition.go_api[0].arn, null)
}

output "go_worker_task_definition_arn" {
  description = "ARN of the Go Worker ECS task definition"
  value       = try(aws_ecs_task_definition.go_worker[0].arn, null)
}

output "node_migration_task_definition_arn" {
  description = "ARN of the Node one-off migration task definition"
  value       = try(aws_ecs_task_definition.node_migration[0].arn, null)
}

output "go_migration_task_definition_arn" {
  description = "ARN of the Go one-off migration task definition"
  value       = try(aws_ecs_task_definition.go_migration[0].arn, null)
}

output "synthetic_seed_task_definition_arn" {
  description = "ARN of the synthetic seed one-off task definition"
  value       = try(aws_ecs_task_definition.synthetic_seed[0].arn, null)
}

output "service_discovery_service_arn" {
  description = "ARN of the Go API Cloud Map service discovery entry"
  value       = aws_service_discovery_service.go_api.arn
}
