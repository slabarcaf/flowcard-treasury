# ==============================================================================
# ECS Fargate Cluster, Cloud Map Service Discovery & Services Module
# ==============================================================================

# 1. ECS Fargate Cluster
resource "aws_ecs_cluster" "cluster" {
  name = "flowcard-demo-cluster"

  setting {
    name  = "containerInsights"
    value = "disabled" # Disabled to prevent unnecessary CloudWatch charges in demo tier
  }

  tags = {
    Name = "flowcard-demo-cluster"
  }
}

# 2. Cloud Map Service Registration for Go API (Node -> Go API Service Discovery)
resource "aws_service_discovery_service" "go_api" {
  name = "onboarding-api"

  dns_config {
    namespace_id = var.service_discovery_namespace_id

    dns_records {
      ttl  = 10
      type = "A"
    }

    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 1
  }

  tags = {
    Name = "onboarding-api"
  }
}

# ------------------------------------------------------------------------------
# 3. Node Gateway Task Definition & Service (Private ALB Ingress on Port 3000)
# ------------------------------------------------------------------------------
resource "aws_ecs_task_definition" "node_gateway" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-node-gateway"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.node_execution_role_arn
  task_role_arn            = var.node_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name        = "node-gateway"
      image       = "${var.node_repository_url}@${var.node_image_digest}"
      essential   = true
      stopTimeout = 30 # Matches 10s graceful shutdown + drain buffer before SIGKILL
      portMappings = [
        {
          containerPort = 3000
          hostPort      = 3000
          protocol      = "tcp"
        }
      ]
      healthCheck = {
        command     = ["CMD", "node", "-e", "fetch('http://127.0.0.1:3000/health').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"]
        interval    = 15
        timeout     = 5
        retries     = 3
        startPeriod = 10
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.node_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "node"
        }
      }
      environment = concat(
        [
          { name = "NODE_ENV", value = "production" },
          { name = "PORT", value = "3000" },
          { name = "KIT5_OPERATOR_ID", value = "flowcard-operator" },
          { name = "KIT5_ALLOWED_ORIGINS", value = join(",", var.allowed_origins) },
          { name = "REMOTE_AUTH_REPAYMENT_TERM", value = "asap" },
          { name = "SALESFORCE_SNAPSHOT_MODE", value = "demo" }
        ],
        var.enable_proposed_onboarding_integration ? [
          { name = "ONBOARDING_SERVICE_URL", value = "http://onboarding-api.flowcard.internal:8080" }
        ] : []
      )
      secrets = concat(
        [
          {
            name      = "DATABASE_URL"
            valueFrom = "${var.app_secret_arn}:node_url::"
          },
          {
            name      = "KIT5_OPERATOR_TOKEN"
            valueFrom = var.operator_secret_arn
          },
          {
            name      = "AIRWALLEX_CLIENT_ID"
            valueFrom = "${var.airwallex_secret_arn}:client_id::"
          },
          {
            name      = "AIRWALLEX_API_KEY"
            valueFrom = "${var.airwallex_secret_arn}:api_key::"
          },
          {
            name      = "AIRWALLEX_WEBHOOK_SECRET"
            valueFrom = "${var.airwallex_secret_arn}:webhook_secret::"
          }
        ],
        var.enable_proposed_onboarding_integration ? [
          {
            name      = "ONBOARDING_SERVICE_SECRET"
            valueFrom = var.service_auth_secret_arn
          }
        ] : []
      )
    }
  ])

  tags = {
    Name = "flowcard-demo-node-gateway"
  }
}

resource "aws_ecs_service" "node_gateway" {
  count           = var.enable_services ? 1 : 0
  name            = "flowcard-demo-node-gateway"
  cluster         = aws_ecs_cluster.cluster.id
  task_definition = aws_ecs_task_definition.node_gateway[0].arn
  desired_count   = var.service_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.node_security_group_id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = var.alb_target_group_arn
    container_name   = "node-gateway"
    container_port   = 3000
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  tags = {
    Name = "flowcard-demo-node-gateway"
  }
}

# ------------------------------------------------------------------------------
# 4. Go API Task Definition & Service (Private Cloud Map Service on Port 8080)
# ------------------------------------------------------------------------------
resource "aws_ecs_task_definition" "go_api" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-go-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.go_api_execution_role_arn
  task_role_arn            = var.go_api_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name        = "go-api"
      image       = "${var.go_api_repository_url}@${var.go_api_image_digest}"
      essential   = true
      stopTimeout = 30
      portMappings = [
        {
          containerPort = 8080
          hostPort      = 8080
          protocol      = "tcp"
        }
      ]
      healthCheck = {
        command     = ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/healthz || exit 1"]
        interval    = 15
        timeout     = 5
        retries     = 3
        startPeriod = 5
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.go_api_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "api"
        }
      }
      environment = concat(
        [
          { name = "PORT", value = "8080" },
          { name = "AWS_REGION", value = var.aws_region }
        ],
        var.enable_proposed_onboarding_integration ? [
          { name = "S3_BUCKET_DOCUMENTS", value = var.s3_documents_bucket_name },
          { name = "S3_KMS_KEY_ID", value = var.s3_documents_kms_key_arn }
        ] : []
      )
      secrets = concat(
        [
          {
            name      = "DATABASE_URL"
            valueFrom = "${var.app_secret_arn}:onboarding_url::"
          }
        ],
        var.enable_proposed_onboarding_integration ? [
          {
            name      = "ONBOARDING_SERVICE_SECRET"
            valueFrom = var.service_auth_secret_arn
          }
        ] : []
      )
    }
  ])

  tags = {
    Name = "flowcard-demo-go-api"
  }
}

resource "aws_ecs_service" "go_api" {
  count           = var.enable_services ? 1 : 0
  name            = "flowcard-demo-go-api"
  cluster         = aws_ecs_cluster.cluster.id
  task_definition = aws_ecs_task_definition.go_api[0].arn
  desired_count   = var.service_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.go_api_security_group_id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.go_api.arn
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  tags = {
    Name = "flowcard-demo-go-api"
  }
}

# ------------------------------------------------------------------------------
# 5. Go Worker Task Definition & Service (Outbox/Inbox Processing; Zero Ingress)
# ------------------------------------------------------------------------------
resource "aws_ecs_task_definition" "go_worker" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-go-worker"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.go_worker_execution_role_arn
  task_role_arn            = var.go_worker_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name         = "go-worker"
      image        = "${var.go_worker_repository_url}@${var.go_worker_image_digest}"
      essential    = true
      command      = ["/bin/worker"]
      stopTimeout  = 30
      portMappings = []
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.go_worker_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "worker"
        }
      }
      environment = concat(
        [
          { name = "AWS_REGION", value = var.aws_region },
          { name = "AIRWALLEX_BASE_URL", value = "https://api.sandbox.airwallex.com" }
        ],
        var.enable_proposed_onboarding_integration ? [
          { name = "S3_BUCKET_DOCUMENTS", value = var.s3_documents_bucket_name },
          { name = "S3_KMS_KEY_ID", value = var.s3_documents_kms_key_arn }
        ] : []
      )
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = "${var.app_secret_arn}:onboarding_url::"
        },
        {
          name      = "AIRWALLEX_CLIENT_ID"
          valueFrom = "${var.airwallex_secret_arn}:client_id::"
        },
        {
          name      = "AIRWALLEX_API_KEY"
          valueFrom = "${var.airwallex_secret_arn}:api_key::"
        }
      ]
    }
  ])

  tags = {
    Name = "flowcard-demo-go-worker"
  }
}

resource "aws_ecs_service" "go_worker" {
  count           = var.enable_services ? 1 : 0
  name            = "flowcard-demo-go-worker"
  cluster         = aws_ecs_cluster.cluster.id
  task_definition = aws_ecs_task_definition.go_worker[0].arn
  desired_count   = var.service_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.go_worker_security_group_id]
    assign_public_ip = false
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  tags = {
    Name = "flowcard-demo-go-worker"
  }
}

# ------------------------------------------------------------------------------
# 6. One-Off Database Migration Task Definitions (Privileged DDL Secrets)
# ------------------------------------------------------------------------------

# Node Subsystem Migration Task (Executes node dist/server/src/db/migrate.js)
resource "aws_ecs_task_definition" "node_migration" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-node-migration"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.migration_execution_role_arn
  task_role_arn            = var.migration_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name         = "node-migration"
      image        = "${var.node_repository_url}@${var.node_image_digest}"
      essential    = true
      command      = ["node", "dist/server/src/db/migrate.js"]
      portMappings = []
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.migration_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "node-migration"
        }
      }
      environment = [
        { name = "NODE_ENV", value = "production" }
      ]
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = "${var.migrate_secret_arn}:node_url::"
        }
      ]
    }
  ])

  tags = {
    Name = "flowcard-demo-node-migration"
  }
}

# Go Subsystem Migration Task (Executes /bin/migrate)
resource "aws_ecs_task_definition" "go_migration" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-go-migration"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.migration_execution_role_arn
  task_role_arn            = var.migration_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name         = "go-migration"
      image        = "${var.migration_repository_url}@${var.migration_image_digest}"
      essential    = true
      command      = ["/bin/migrate"]
      portMappings = []
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.migration_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "go-migration"
        }
      }
      environment = [
        { name = "AWS_REGION", value = var.aws_region }
      ]
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = "${var.migrate_secret_arn}:onboarding_url::"
        }
      ]
    }
  ])

  tags = {
    Name = "flowcard-demo-go-migration"
  }
}

# ------------------------------------------------------------------------------
# 7. One-Off Synthetic Fixture Seed Task Definition (Idempotent Demo Data)
# ------------------------------------------------------------------------------
resource "aws_ecs_task_definition" "synthetic_seed" {
  count                    = var.enable_services ? 1 : 0
  family                   = "flowcard-demo-synthetic-seed"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = var.node_execution_role_arn
  task_role_arn            = var.node_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([
    {
      name      = "node-seed"
      image     = "${var.node_repository_url}@${var.node_image_digest}"
      essential = true
      command = [
        "node",
        "dist/server/src/db/seed.js",
        "--target-host",
        var.db_address,
        "--target-port",
        tostring(var.db_port),
        "--target-db",
        var.db_name
      ]
      portMappings = []
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.node_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "node-seed"
        }
      }
      environment = [
        { name = "NODE_ENV", value = "production" }
      ]
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = "${var.app_secret_arn}:node_url::"
        }
      ]
    }
  ])

  tags = {
    Name = "flowcard-demo-synthetic-seed"
  }
}
