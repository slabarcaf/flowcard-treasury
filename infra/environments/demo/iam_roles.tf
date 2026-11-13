# ==============================================================================
# ECS Execution Roles & Task Roles (Least-Privilege Role Separation)
# ==============================================================================

# Common trust policy for all ECS task and execution roles
data "aws_iam_policy_document" "ecs_assume_role" {
  statement {
    sid     = "ECSTasksAssumeRole"
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# ------------------------------------------------------------------------------
# Dedicated ECS Execution Roles (Per-Service Secret & Image Isolation)
# ------------------------------------------------------------------------------
# The ECS execution role is used by the ECS container agent to:
# 1. Obtain an ECR authorization token (AWS IAM requires Resource = "*").
# 2. Pull container images from the specific service's ECR repository.
# 3. Stream container stdout/stderr logs to the specific service's CloudWatch log group.
# 4. Resolve task secrets from Secrets Manager.
# Dedicated execution roles guarantee that privileged secrets (e.g. migration DSN)
# cannot be resolved or accessed by runtime application tasks.
# Neither execution nor runtime task roles are granted access to the RDS master secret.

# 1. Node Gateway Execution Role
resource "aws_iam_role" "node_execution" {
  name               = "FlowCardNodeExecutionRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardNodeExecutionRole"
  }
}

resource "aws_iam_policy" "node_execution" {
  name        = "FlowCardNodeExecutionPolicy"
  description = "Scoped execution policy for Node gateway: pull image, write logs, resolve proxy secrets"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # AWS IAM note: ecr:GetAuthorizationToken does not support resource-level permissions
        # and strictly requires Resource = "*" per AWS specification.
        Sid      = "ECRAuthToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "ECRPullNodeImage"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage"
        ]
        Resource = module.ecr.repository_arns["flowcard-node-gateway"]
      },
      {
        Sid    = "WriteNodeLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${module.cloudwatch.log_group_arns["/ecs/flowcard-node-gateway"]}:*"
      },
      {
        Sid    = "ResolveNodeSecrets"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          module.secrets.secret_arns["flowcard/db/app"],
          module.secrets.secret_arns["flowcard/service-auth"],
          module.secrets.secret_arns["flowcard/operator"],
          module.secrets.secret_arns["flowcard/airwallex"]
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "node_execution" {
  role       = aws_iam_role.node_execution.name
  policy_arn = aws_iam_policy.node_execution.arn
}

# 2. Go API Execution Role
resource "aws_iam_role" "go_api_execution" {
  name               = "FlowCardApiExecutionRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardApiExecutionRole"
  }
}

resource "aws_iam_policy" "go_api_execution" {
  name        = "FlowCardApiExecutionPolicy"
  description = "Scoped execution policy for Go API: pull image, write logs, resolve runtime app secrets"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # ecr:GetAuthorizationToken requires Resource = "*" per AWS specification.
        Sid      = "ECRAuthToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "ECRPullApiImage"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage"
        ]
        Resource = module.ecr.repository_arns["flowcard-go-api"]
      },
      {
        Sid    = "WriteApiLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${module.cloudwatch.log_group_arns["/ecs/flowcard-go-api"]}:*"
      },
      {
        # Go API resolves runtime app DB credentials (flowcard/db/app), but NOT flowcard/db/migrate
        # or the RDS master administrator secret.
        Sid    = "ResolveApiSecrets"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          module.secrets.secret_arns["flowcard/db/app"],
          module.secrets.secret_arns["flowcard/service-auth"],
          module.secrets.secret_arns["flowcard/airwallex"]
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "go_api_execution" {
  role       = aws_iam_role.go_api_execution.name
  policy_arn = aws_iam_policy.go_api_execution.arn
}

# 3. Go Worker Execution Role
resource "aws_iam_role" "go_worker_execution" {
  name               = "FlowCardWorkerExecutionRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardWorkerExecutionRole"
  }
}

resource "aws_iam_policy" "go_worker_execution" {
  name        = "FlowCardWorkerExecutionPolicy"
  description = "Scoped execution policy for Go Worker: pull image, write logs, resolve runtime secrets"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # ecr:GetAuthorizationToken requires Resource = "*" per AWS specification.
        Sid      = "ECRAuthToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "ECRPullWorkerImage"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage"
        ]
        Resource = module.ecr.repository_arns["flowcard-go-worker"]
      },
      {
        Sid    = "WriteWorkerLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${module.cloudwatch.log_group_arns["/ecs/flowcard-go-worker"]}:*"
      },
      {
        # Worker resolves runtime app DB credentials and Airwallex, but NOT migration or operator secrets.
        Sid    = "ResolveWorkerSecrets"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          module.secrets.secret_arns["flowcard/db/app"],
          module.secrets.secret_arns["flowcard/airwallex"]
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "go_worker_execution" {
  role       = aws_iam_role.go_worker_execution.name
  policy_arn = aws_iam_policy.go_worker_execution.arn
}

# 4. Database Migration Execution Role
resource "aws_iam_role" "migration_execution" {
  name               = "FlowCardMigrationExecutionRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardMigrationExecutionRole"
  }
}

resource "aws_iam_policy" "migration_execution" {
  name        = "FlowCardMigrationExecutionPolicy"
  description = "Scoped execution policy for DB Migration: pull image, write logs, resolve migration DSN"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # ecr:GetAuthorizationToken requires Resource = "*" per AWS specification.
        Sid      = "ECRAuthToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "ECRPullMigrationImage"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage"
        ]
        Resource = [
          module.ecr.repository_arns["flowcard-migration"],
          module.ecr.repository_arns["flowcard-node-gateway"]
        ]
      },
      {
        Sid    = "WriteMigrationLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${module.cloudwatch.log_group_arns["/ecs/flowcard-migration"]}:*"
      },
      {
        # Migration task strictly resolves privileged migration DSN (flowcard/db/migrate).
        # Master bootstrap password is kept separate; migration task does not resolve master secret.
        Sid    = "ResolveMigrationSecret"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          module.secrets.secret_arns["flowcard/db/migrate"]
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "migration_execution" {
  role       = aws_iam_role.migration_execution.name
  policy_arn = aws_iam_policy.migration_execution.arn
}

# ------------------------------------------------------------------------------
# Dedicated ECS Task Roles (Runtime Container Identity & AWS API Access)
# ------------------------------------------------------------------------------

# 1. Node Gateway Task Role (Zero AWS Resource Privileges)
resource "aws_iam_role" "node_task" {
  name               = "FlowCardNodeTaskRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardNodeTaskRole"
  }
}

# 2. Go API Task Role (Scoped S3 Put/Get/Delete + KMS GenerateDataKey/Decrypt)
resource "aws_iam_role" "go_api_task" {
  name               = "FlowCardApiTaskRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardApiTaskRole"
  }
}

resource "aws_iam_policy" "go_api_s3" {
  name        = "FlowCardApiDocumentsPolicy"
  description = "Scoped S3 and KMS permissions for Go API onboarding document handling"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ScopedS3DocumentsObjectAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject"
        ]
        Resource = "${module.s3_documents.bucket_arn}/*"
      },
      {
        Sid    = "ScopedS3DocumentsBucketList"
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = module.s3_documents.bucket_arn
      },
      {
        Sid    = "ScopedKMSDocumentsAccess"
        Effect = "Allow"
        Action = [
          "kms:GenerateDataKey",
          "kms:Decrypt",
          "kms:DescribeKey"
        ]
        Resource = module.s3_documents.kms_key_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "go_api_s3" {
  role       = aws_iam_role.go_api_task.name
  policy_arn = aws_iam_policy.go_api_s3.arn
}

# 3. Go Worker Task Role (Scoped S3 GetObject + KMS Decrypt Only)
# Uses PostgreSQL outbox queue: zero SQS privileges, zero S3 write permissions.
resource "aws_iam_role" "go_worker_task" {
  name               = "FlowCardWorkerTaskRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardWorkerTaskRole"
  }
}

resource "aws_iam_policy" "go_worker_s3" {
  name        = "FlowCardWorkerDocumentsPolicy"
  description = "Scoped read-only S3 and KMS decrypt permissions for Go Worker"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ScopedS3DocumentsRead"
        Effect = "Allow"
        Action = [
          "s3:GetObject"
        ]
        Resource = "${module.s3_documents.bucket_arn}/*"
      },
      {
        Sid    = "ScopedKMSDocumentsDecrypt"
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:DescribeKey"
        ]
        Resource = module.s3_documents.kms_key_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "go_worker_s3" {
  role       = aws_iam_role.go_worker_task.name
  policy_arn = aws_iam_policy.go_worker_s3.arn
}

# 4. Database Migration Task Role (Zero AWS Resource Privileges)
# Restricted to DDL/DML via database credentials injected by the execution role.
resource "aws_iam_role" "migration_task" {
  name               = "FlowCardMigrationTaskRole"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "FlowCardMigrationTaskRole"
  }
}
