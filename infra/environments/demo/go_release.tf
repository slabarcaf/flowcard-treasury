locals {
  go_native_routes = var.enable_go_stack_foundation ? {
    for route in jsondecode(file("${path.module}/../../runtime/go-stack.json")).native_routes : route.name => route
  } : {}
}

resource "aws_security_group_rule" "go_native_ingress" {
  for_each                 = local.go_native_routes
  type                     = "ingress"
  description              = "Native Go webhook receiver from private ALB only"
  from_port                = each.value.port
  to_port                  = each.value.port
  protocol                 = "tcp"
  source_security_group_id = module.alb.alb_security_group_id
  security_group_id        = aws_security_group.node_task.id
}

output "go_release_foundation" {
  description = "Non-secret input for the manual Go release workflow; export after reviewed foundation apply"
  value = {
    generation                       = "go-stack-v1"
    go_stack_foundation_enabled      = var.enable_go_stack_foundation
    account_id                       = data.aws_caller_identity.current.account_id
    aws_region                       = var.aws_region
    ecs_cluster_arn                  = module.ecs.cluster_arn
    ecs_cluster_name                 = module.ecs.cluster_name
    private_subnet_ids               = module.vpc.private_subnet_ids
    migration_task_security_group_id = aws_security_group.migration_task.id
    migration_task_definition_arn    = module.ecs.go_migration_task_definition_arn
    database_host                    = module.rds.address
    database_name                    = module.rds.database_name
    runtime_task_role_arn            = aws_iam_role.go_api_task.arn
    documents_bucket_name            = module.s3_documents.bucket_name
    documents_kms_key_arn            = module.s3_documents.kms_key_arn
    ecr_repository_urls              = module.ecr.repository_urls
    native_target_groups             = module.alb.go_native_target_groups
    native_rule_arns                 = module.alb.go_native_rule_arns
    gateway_target_group_arn         = module.alb.node_target_group_arn
    frontend_bucket_name             = module.s3_frontend.bucket_name
    cloudfront_distribution_id       = var.enable_cloudfront ? module.cloudfront[0].cloudfront_distribution_id : null
    allowed_origins                  = concat(var.allowed_origins, var.enable_cloudfront ? ["https://${module.cloudfront[0].cloudfront_domain_name}"] : [])
    secret_arns = {
      app          = module.secrets.secret_arns["flowcard/db/app"]
      migrate      = module.secrets.secret_arns["flowcard/db/migrate"]
      service_auth = module.secrets.secret_arns["flowcard/service-auth"]
      airwallex    = module.secrets.secret_arns["flowcard/airwallex"]
    }
  }
}
