# GitHub OpenID Connect (OIDC) identity provider
data "aws_partition" "current" {}

resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1", "1c58a3a8518e8759bf075b76b750d4f8d731d304"]

  tags = {
    Name = "github-actions-oidc"
  }
}

# IAM Role assumed by GitHub Actions deployments
# Scoped trust policy: exact immutable repository identity and develop branch.
# The workflow also restricts dispatch to develop before any privileged job.
resource "aws_iam_role" "github_deployer" {
  name = var.role_name

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "GitHubActionsOIDC"
        Effect = "Allow"
        Principal = {
          Federated = aws_iam_openid_connect_provider.github.arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
            "token.actions.githubusercontent.com:sub" = "repo:${var.github_owner_name}@${var.github_owner_id}/${var.github_repository_name}@${var.github_repository_id}:ref:refs/heads/develop"
          }
        }
      }
    ]
  })

  tags = {
    Name = var.role_name
  }
}

# Scoped Least-Privilege IAM Policy for GitHub Actions Application Release
# Strict security boundaries:
# 1. iam:PassRole strictly restricted to exact execution and task roles with PassedToService = ecs-tasks.amazonaws.com.
# 2. secretsmanager:GetSecretValue is strictly OMITTED (the deployer does not resolve secrets; execution roles resolve at runtime).
# 3. RDS master/admin credentials and operations are strictly OMITTED.
# 4. Broad IAM administration is strictly OMITTED (cannot modify roles, policies, or account credentials).
# 5. Resource ARNs for ECR, ECS, S3 frontend, and CloudFront are explicitly narrowed.
resource "aws_iam_policy" "github_deployer" {
  name        = "${var.role_name}Policy"
  description = "Scoped least-privilege release policy for FlowCard GitHub Actions deployment"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat([
      # 1. ECR authorization token (AWS requires Resource = "*" for token generation)
      {
        Sid      = "ECRAuthToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      # 2. Scoped ECR push/pull for the 4 service repositories (no wildcard fallback)
      {
        Sid    = "ECRRepositoryAccess"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage",
          "ecr:PutImage",
          "ecr:InitiateLayerUpload",
          "ecr:UploadLayerPart",
          "ecr:CompleteLayerUpload",
          "ecr:DescribeRepositories"
        ]
        Resource = var.ecr_repository_arns
      },
      # 3. ECS Task Definition registration & inspection
      {
        Sid    = "ECSTaskRegistration"
        Effect = "Allow"
        Action = [
          "ecs:RegisterTaskDefinition",
          "ecs:DescribeTaskDefinition"
        ]
        Resource = "*"
      },
      # 4. ECS Service updates on the 3 demo services
      {
        Sid    = "ECSServiceUpdates"
        Effect = "Allow"
        Action = [
          "ecs:UpdateService",
          "ecs:DescribeServices"
        ]
        Resource = [
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:service/${var.ecs_cluster_name}/flowcard-demo-node-gateway",
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:service/${var.ecs_cluster_name}/flowcard-demo-go-api",
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:service/${var.ecs_cluster_name}/flowcard-demo-go-worker",
          var.ecs_cluster_arn
        ]
      },
      # 5. ECS RunTask restricted strictly to exact migration and synthetic seed task definitions
      # Condition: strictly bound to the demo cluster ARN
      {
        Sid    = "ECSRunMigrationAndSeedTasks"
        Effect = "Allow"
        Action = [
          "ecs:RunTask"
        ]
        Resource = [
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:task-definition/flowcard-demo-node-migration:*",
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:task-definition/flowcard-demo-go-migration:*",
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:task-definition/flowcard-demo-synthetic-seed:*"
        ]
        Condition = {
          ArnEquals = {
            "ecs:cluster" = var.ecs_cluster_arn
          }
        }
      },
      # 6. ECS Task status inspection on demo cluster
      {
        Sid    = "ECSTaskInspection"
        Effect = "Allow"
        Action = [
          "ecs:DescribeTasks"
        ]
        Resource = [
          "arn:${data.aws_partition.current.partition}:ecs:${var.aws_region}:${var.aws_account_id}:task/${var.ecs_cluster_name}/*",
          var.ecs_cluster_arn
        ]
      },
      # 7. EC2 Network Discovery (AWS EC2 Describe API does not support resource-level permissions; Resource = "*" required)
      {
        Sid    = "EC2NetworkDiscovery"
        Effect = "Allow"
        Action = [
          "ec2:DescribeSubnets",
          "ec2:DescribeSecurityGroups"
        ]
        Resource = "*"
      },
      # 8. iam:PassRole strictly scoped to exact ECS execution and task roles
      # Condition: only to ecs-tasks.amazonaws.com service principal
      {
        Sid    = "PassRoleToECSTasks"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = concat(
          var.execution_role_arns,
          var.task_role_arns
        )
        Condition = {
          StringEquals = {
            "iam:PassedToService" = "ecs-tasks.amazonaws.com"
          }
        }
      },
      # 9. S3 Frontend deployment (bucket location, object put/sync)
      {
        Sid    = "S3FrontendDeploy"
        Effect = "Allow"
        Action = [
          "s3:GetBucketLocation",
          "s3:ListBucket"
        ]
        Resource = [var.frontend_bucket_arn]
      },
      {
        Sid    = "S3FrontendObjects"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject"
        ]
        Resource = ["${var.frontend_bucket_arn}/*"]
      },
      ], var.cloudfront_distribution_arn == null ? [] : [{
        # 10. CloudFront read, invalidation creation, and completion checks stay on one exact distribution.
        Sid    = "CloudFrontInvalidation"
        Effect = "Allow"
        Action = [
          "cloudfront:GetDistribution",
          "cloudfront:CreateInvalidation",
          "cloudfront:GetInvalidation"
        ]
        Resource = [var.cloudfront_distribution_arn]
    }])
  })
}

resource "aws_iam_role_policy_attachment" "github_deployer" {
  role       = aws_iam_role.github_deployer.name
  policy_arn = aws_iam_policy.github_deployer.arn
}
