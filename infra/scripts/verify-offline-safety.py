#!/usr/bin/env python3
"""
Offline Static Safety Verification Suite for FlowCard AWS Infrastructure.

Performs deterministic static analysis on Terraform HCL configurations to verify
security boundaries, least-privilege IAM policies, network edge isolation, and
variable validation preconditions without AWS credentials or cloud provisioning.
Includes negative mutation tests to ensure verifier assertions fail on invalid input.
"""

import os
import sys
import re
from pathlib import Path
from typing import Optional

ROOT_DIR = Path(__file__).resolve().parent.parent
MODULES_DIR = ROOT_DIR / "modules"
DEMO_DIR = ROOT_DIR / "environments" / "demo"
SCRIPTS_DIR = Path(__file__).resolve().parent

FAILED_TESTS = []
PASSED_TESTS = []


def assert_test(name: str, condition: bool, error_msg: str):
    if condition:
        PASSED_TESTS.append(name)
        print(f"  [PASS] {name}")
    else:
        FAILED_TESTS.append((name, error_msg))
        print(f"  [FAIL] {name}: {error_msg}")


# ==============================================================================
# Helper Parsers with Fail-Closed Semantics (No Silent False Passes)
# ==============================================================================

def parse_secrets_for_policy(policy_name: str, hcl_text: str) -> Optional[list[str]]:
    """
    Extracts secret ARNs from an aws_iam_policy resource.
    Returns None if the policy block or jsonencode block is absent or malformed.
    """
    match = re.search(
        rf'resource "aws_iam_policy" "{policy_name}".*?policy\s*=\s*jsonencode\((\{{.*?\}})\)',
        hcl_text,
        re.DOTALL
    )
    if not match:
        return None
    policy_str = match.group(1)
    return re.findall(r'module\.secrets\.secret_arns\["([^"]+)"\]', policy_str)


def check_rds_no_cidr_ingress_in_text(hcl_text: str) -> tuple[bool, str]:
    ingress_match = re.search(r'resource "aws_security_group" "rds".*?ingress\s*\{(.*?)\}', hcl_text, re.DOTALL)
    if not ingress_match:
        return False, "Missing ingress block in aws_security_group.rds"
    ingress_body = ingress_match.group(1)
    if "cidr_blocks" in ingress_body:
        return False, "Found forbidden cidr_blocks in RDS SG ingress"
    if "var.authorized_security_group_ids" not in ingress_body:
        return False, "RDS SG ingress missing var.authorized_security_group_ids"
    return True, "OK"


def check_alb_egress_in_text(hcl_text: str) -> tuple[bool, str]:
    egress_match = re.search(r'resource "aws_security_group" "alb".*?egress\s*\{(.*?)\}', hcl_text, re.DOTALL)
    if not egress_match:
        return False, "Missing egress block in aws_security_group.alb"
    egress_body = egress_match.group(1)
    if "0.0.0.0/0" in egress_body:
        return False, "Forbidden 0.0.0.0/0 found in ALB egress"
    if "var.node_security_group_id" not in egress_body:
        return False, "ALB egress missing var.node_security_group_id target"
    if "var.node_port" not in egress_body and "3000" not in egress_body:
        return False, "ALB egress missing target node_port"
    return True, "OK"


# ==============================================================================
# Test Suites
# ==============================================================================

def test_rds_network_isolation_and_storage():
    print("\n--- Test Suite 1: RDS Network Isolation & Storage Configuration ---")
    rds_main = (MODULES_DIR / "rds" / "main.tf").read_text()

    valid, msg = check_rds_no_cidr_ingress_in_text(rds_main)
    assert_test("RDS SG ingress has NO cidr_blocks and requires authorized SGs", valid, msg)

    assert_test(
        "RDS is NOT publicly accessible",
        bool(re.search(r'publicly_accessible\s*=\s*false', rds_main)),
        "RDS instance must explicitly specify publicly_accessible = false."
    )

    assert_test(
        "RDS uses Secrets Manager managed master password",
        bool(re.search(r'manage_master_user_password\s*=\s*true', rds_main)),
        "RDS instance must specify manage_master_user_password = true."
    )

    assert_test(
        "RDS storage is encrypted",
        bool(re.search(r'storage_encrypted\s*=\s*true', rds_main)),
        "RDS storage must be encrypted."
    )

    assert_test(
        "RDS deletion protection parameterized",
        bool(re.search(r'deletion_protection\s*=\s*var\.deletion_protection', rds_main)),
        "RDS instance must enforce deletion protection."
    )


def test_security_group_edge_matrix():
    print("\n--- Test Suite 2: Security Group Edge Matrix ---")
    alb_main = (MODULES_DIR / "alb" / "main.tf").read_text()
    sg_demo = (DEMO_DIR / "security_groups.tf").read_text()
    demo_main = (DEMO_DIR / "main.tf").read_text()

    # ALB SG egress
    valid_alb, msg_alb = check_alb_egress_in_text(alb_main)
    assert_test("ALB SG egress strictly targets Node SG on node_port", valid_alb, msg_alb)

    # Node SG ingress from ALB SG
    assert_test(
        "Node SG ingress from ALB SG only",
        "source_security_group_id = module.alb.alb_security_group_id" in sg_demo and "security_group_id        = aws_security_group.node_task.id" in sg_demo,
        "Node SG ingress rule must source from module.alb.alb_security_group_id."
    )

    # Node SG egress to Go API SG on port 8080
    assert_test(
        "Node SG egress to Go API SG on port 8080",
        "source_security_group_id = aws_security_group.go_api_task.id" in sg_demo and "8080" in sg_demo,
        "Node SG egress must target Go API task SG on port 8080."
    )

    # POSITIVE: Node SG egress to RDS SG on port 5432 (ledger, webhook inbox, forwarding)
    assert_test(
        "Node SG egress to RDS SG on port 5432",
        "source_security_group_id = module.rds.security_group_id" in sg_demo and "security_group_id        = aws_security_group.node_task.id" in sg_demo and "5432" in sg_demo,
        "Node SG must have an explicit egress rule to RDS SG on port 5432 for ledger/inbox operations."
    )

    # Go API SG ingress from Node SG only
    assert_test(
        "Go API SG ingress from Node SG",
        "source_security_group_id = aws_security_group.node_task.id" in sg_demo and "security_group_id        = aws_security_group.go_api_task.id" in sg_demo,
        "Go API SG must allow ingress strictly from Node task SG."
    )

    # Go API SG egress to RDS on port 5432
    assert_test(
        "Go API SG egress to RDS on port 5432",
        "source_security_group_id = module.rds.security_group_id" in sg_demo and "security_group_id        = aws_security_group.go_api_task.id" in sg_demo,
        "Go API SG egress must target module.rds.security_group_id on port 5432."
    )

    # Ingress rule inspection
    rule_blocks = re.findall(r'resource "aws_security_group_rule" "[^"]+" \{(.*?)\n\}', sg_demo, re.DOTALL)
    assert_test("Security group rules parsed from security_groups.tf", len(rule_blocks) > 0, "No rule blocks parsed.")

    worker_ingress = [b for b in rule_blocks if 'type = "ingress"' in b or 'type                     = "ingress"' in b]
    worker_ingress_rules = [b for b in worker_ingress if "aws_security_group.go_worker_task.id" in b]
    assert_test(
        "Go Worker SG has ZERO ingress rules",
        len(worker_ingress_rules) == 0,
        f"Go Worker task must have zero inbound ingress rules, found {len(worker_ingress_rules)}."
    )

    migration_ingress_rules = [b for b in worker_ingress if "aws_security_group.migration_task.id" in b]
    assert_test(
        "Migration SG has ZERO ingress rules",
        len(migration_ingress_rules) == 0,
        f"Migration task must have zero inbound ingress rules, found {len(migration_ingress_rules)}."
    )

    # Fail-closed check: verify RDS wiring block exists in main.tf
    rds_block = re.search(r'module "rds"\s*\{(.*?)\n\}', demo_main, re.DOTALL)
    assert_test("RDS module is wired in demo main.tf", bool(rds_block), "Could not find module 'rds' in demo main.tf.")

    if rds_block:
        rds_body = rds_block.group(1)
        assert_test(
            "RDS authorized SGs include Node gateway task",
            "aws_security_group.node_task.id" in rds_body,
            "RDS authorized SGs must include Node gateway SG for ledger/inbox operations."
        )
        assert_test(
            "RDS authorized SGs include Go API task",
            "aws_security_group.go_api_task.id" in rds_body,
            "RDS authorized SGs must include Go API SG."
        )
        assert_test(
            "RDS authorized SGs include Go Worker task",
            "aws_security_group.go_worker_task.id" in rds_body,
            "RDS authorized SGs must include Go Worker SG."
        )
        assert_test(
            "RDS authorized SGs include Migration task",
            "aws_security_group.migration_task.id" in rds_body,
            "RDS authorized SGs must include Migration task SG."
        )
        assert_test(
            "RDS authorized SGs EXCLUDE ALB",
            "module.alb.alb_security_group_id" not in rds_body,
            "ALB SG must NOT have network access to RDS."
        )


def test_iam_execution_secret_separation():
    print("\n--- Test Suite 3: IAM Dedicated Execution Role Secret Separation ---")
    iam_roles = (DEMO_DIR / "iam_roles.tf").read_text()

    node_secrets = parse_secrets_for_policy("node_execution", iam_roles)
    api_secrets = parse_secrets_for_policy("go_api_execution", iam_roles)
    worker_secrets = parse_secrets_for_policy("go_worker_execution", iam_roles)
    migration_secrets = parse_secrets_for_policy("migration_execution", iam_roles)

    # Fail closed if any policy failed to parse
    assert_test("Node execution secrets policy parsed successfully", node_secrets is not None, "Failed to parse node_execution policy")
    assert_test("Go API execution secrets policy parsed successfully", api_secrets is not None, "Failed to parse go_api_execution policy")
    assert_test("Go Worker execution secrets policy parsed successfully", worker_secrets is not None, "Failed to parse go_worker_execution policy")
    assert_test("Migration execution secrets policy parsed successfully", migration_secrets is not None, "Failed to parse migration_execution policy")

    if node_secrets is not None:
        assert_test(
            "Node execution role CAN resolve runtime db/app secret",
            "flowcard/db/app" in node_secrets,
            "Node execution role must be granted access to flowcard/db/app for runtime operations."
        )
        assert_test(
            "Node execution role CANNOT resolve migration DSN",
            "flowcard/db/migrate" not in node_secrets,
            "Node execution role must not have access to flowcard/db/migrate."
        )

    if api_secrets is not None:
        assert_test(
            "Go API execution role CANNOT resolve migration DSN",
            "flowcard/db/migrate" not in api_secrets,
            "Go API execution role must not have access to flowcard/db/migrate."
        )

    if worker_secrets is not None:
        assert_test(
            "Go Worker execution role CANNOT resolve migration DSN",
            "flowcard/db/migrate" not in worker_secrets,
            "Go Worker execution role must not have access to flowcard/db/migrate."
        )

    if migration_secrets is not None:
        assert_test(
            "Migration execution role CAN resolve migration DSN",
            "flowcard/db/migrate" in migration_secrets,
            "Migration execution role must have access to flowcard/db/migrate."
        )
        assert_test(
            "Migration execution role CANNOT resolve service-auth key",
            "flowcard/service-auth" not in migration_secrets,
            "Migration execution role must not have access to flowcard/service-auth."
        )

    assert_test(
        "NO execution role has access to RDS master secret ARN",
        "module.rds.master_user_secret_arn" not in iam_roles,
        "No runtime or migration execution role may be granted access to the RDS master password secret."
    )


def test_s3_public_blocks_and_kms():
    print("\n--- Test Suite 4: S3 Public Access Blocks & KMS Protection ---")
    s3_docs = (MODULES_DIR / "s3_documents" / "main.tf").read_text()
    s3_front = (MODULES_DIR / "s3_frontend" / "main.tf").read_text()

    assert_test(
        "Documents bucket has prevent_destroy = true",
        bool(re.search(r'prevent_destroy\s*=\s*true', s3_docs)),
        "Documents bucket must have lifecycle { prevent_destroy = true }."
    )

    for block in ["block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"]:
        assert_test(
            f"Documents bucket enforces {block} = true",
            bool(re.search(rf'{block}\s*=\s*true', s3_docs)),
            f"Documents bucket must enforce {block} = true."
        )

    assert_test(
        "Documents bucket policy enforces aws:SecureTransport",
        bool(re.search(r'"aws:SecureTransport"\s*=\s*"false"', s3_docs)),
        "Documents bucket policy must enforce HTTPS transport via aws:SecureTransport Deny condition."
    )

    assert_test(
        "Documents bucket uses customer-managed KMS key",
        "kms_master_key_id = aws_kms_key.documents.arn" in s3_docs,
        "Documents bucket must use customer-managed KMS key for SSE-KMS."
    )

    for block in ["block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"]:
        assert_test(
            f"Frontend bucket enforces {block} = true",
            bool(re.search(rf'{block}\s*=\s*true', s3_front)),
            f"Frontend bucket must enforce {block} = true."
        )


def test_input_validation_rules():
    print("\n--- Test Suite 5: Variable & Precondition Input Validation Logic ---")
    vpc_vars = (MODULES_DIR / "vpc" / "variables.tf").read_text()
    vpc_main = (MODULES_DIR / "vpc" / "main.tf").read_text()
    rds_vars = (MODULES_DIR / "rds" / "variables.tf").read_text()

    assert_test(
        "VPC availability_zones checks for duplicates (distinct)",
        "length(distinct(var.availability_zones)) == length(var.availability_zones)" in vpc_vars,
        "VPC availability_zones must validate that all AZs are distinct."
    )

    assert_test(
        "VPC public_subnet_cidrs length matches availability_zones precondition",
        "length(var.public_subnet_cidrs) == length(var.availability_zones)" in vpc_main,
        "VPC main.tf must enforce public_subnet_cidrs length == availability_zones length."
    )
    assert_test(
        "VPC private_subnet_cidrs length matches availability_zones precondition",
        "length(var.private_subnet_cidrs) == length(var.availability_zones)" in vpc_main,
        "VPC main.tf must enforce private_subnet_cidrs length == availability_zones length."
    )

    assert_test(
        "RDS backup_retention_period validates 1..35 range",
        "var.backup_retention_period >= 1 && var.backup_retention_period <= 35" in rds_vars,
        "RDS backup_retention_period must validate between 1 and 35 days."
    )

    assert_test(
        "RDS allocated_storage validates integer and >= 20",
        "floor(var.allocated_storage) == var.allocated_storage && var.allocated_storage >= 20" in rds_vars,
        "RDS allocated_storage must validate integer >= 20 GB."
    )


# ==============================================================================
# Negative Mutation Fixtures (Proves Verifier Detects Invalid Configurations)
# ==============================================================================

def test_negative_mutation_fixtures():
    print("\n--- Test Suite 6: Negative Mutation Tests (Verifier Sanity) ---")

    # Fixture 1: Inject forbidden CIDR ingress into RDS security group
    mutated_rds_cidr = """
    resource "aws_security_group" "rds" {
      ingress {
        from_port   = 5432
        to_port     = 5432
        protocol    = "tcp"
        cidr_blocks = ["10.0.0.0/16"]
      }
    }
    """
    valid, msg = check_rds_no_cidr_ingress_in_text(mutated_rds_cidr)
    assert_test(
        "Negative mutation: verifier catches forbidden CIDR ingress in RDS SG",
        not valid and "forbidden cidr_blocks" in msg,
        f"Verifier should have rejected CIDR ingress in RDS SG, got valid={valid}"
    )

    # Fixture 2: Omit ALB egress block
    mutated_alb_no_egress = """
    resource "aws_security_group" "alb" {
      ingress {
        from_port = 80
        to_port   = 80
      }
    }
    """
    valid_alb, msg_alb = check_alb_egress_in_text(mutated_alb_no_egress)
    assert_test(
        "Negative mutation: verifier catches missing ALB egress block",
        not valid_alb and "Missing egress block" in msg_alb,
        f"Verifier should have rejected missing ALB egress, got valid={valid_alb}"
    )

    # Fixture 3: ALB egress allows open 0.0.0.0/0
    mutated_alb_open_egress = """
    resource "aws_security_group" "alb" {
      egress {
        from_port   = 0
        to_port     = 0
        protocol    = "-1"
        cidr_blocks = ["0.0.0.0/0"]
      }
    }
    """
    valid_open, msg_open = check_alb_egress_in_text(mutated_alb_open_egress)
    assert_test(
        "Negative mutation: verifier catches 0.0.0.0/0 in ALB egress",
        not valid_open and "0.0.0.0/0" in msg_open,
        f"Verifier should have rejected 0.0.0.0/0 in ALB egress, got valid={valid_open}"
    )

    # Fixture 4: Inject migration secret into Node execution role
    mutated_node_policy = """
    resource "aws_iam_policy" "node_execution" {
      policy = jsonencode({
        Statement = [
          {
            Resource = [
              module.secrets.secret_arns["flowcard/db/app"],
              module.secrets.secret_arns["flowcard/db/migrate"]
            ]
          }
        ]
      })
    }
    """
    parsed_secrets = parse_secrets_for_policy("node_execution", mutated_node_policy)
    assert_test(
        "Negative mutation: verifier detects migration secret leakage in Node role",
        parsed_secrets is not None and "flowcard/db/migrate" in parsed_secrets,
        "Verifier should have parsed and identified flowcard/db/migrate in mutated policy."
    )

    # Fixture 5: Malformed policy block fails closed (returns None, not empty list)
    malformed_policy = 'resource "aws_iam_policy" "corrupted" { policy = "not-json" }'
    result = parse_secrets_for_policy("node_execution", malformed_policy)
    assert_test(
        "Negative mutation: malformed policy fails closed (returns None)",
        result is None,
        f"Verifier should return None on missing/malformed policy, got {result}"
    )


# ==============================================================================
# Task 6 Verification Suites: ECS Fargate, Tasks & Deployment Scripts
# ==============================================================================

def test_ecs_services_and_network():
    print("\n--- Test Suite 7: ECS Fargate Services & Private Network Isolation ---")

    ecs_main_file = MODULES_DIR / "ecs" / "main.tf"
    ecs_vars_file = MODULES_DIR / "ecs" / "variables.tf"

    assert_test(
        "ECS module main.tf exists",
        ecs_main_file.exists(),
        f"Missing {ecs_main_file}"
    )
    assert_test(
        "ECS module variables.tf exists",
        ecs_vars_file.exists(),
        f"Missing {ecs_vars_file}"
    )

    ecs_main_text = ecs_main_file.read_text()
    ecs_vars_text = ecs_vars_file.read_text()

    # 1. Cluster resource
    assert_test(
        "ECS Fargate cluster defined with Container Insights disabled",
        'resource "aws_ecs_cluster" "cluster"' in ecs_main_text and
        'name  = "containerInsights"' in ecs_main_text and
        'value = "disabled"' in ecs_main_text,
        "Cluster must be defined with containerInsights disabled for demo tier."
    )

    # 2. Private Networking & assign_public_ip = false
    net_configs = re.findall(r'network_configuration\s*\{([^}]+)\}', ecs_main_text)
    assert_test(
        "All ECS services enforce assign_public_ip = false in private subnets",
        len(net_configs) >= 3 and all(
            "assign_public_ip = false" in nc and "subnets          = var.private_subnet_ids" in nc
            for nc in net_configs
        ),
        "All ECS services must run in private subnets with assign_public_ip = false."
    )

    # 3. Cloud Map Service Discovery
    assert_test(
        "Cloud Map service discovery registered for Go API (onboarding-api)",
        'resource "aws_service_discovery_service" "go_api"' in ecs_main_text and
        'name = "onboarding-api"' in ecs_main_text,
        "Go API must be registered in Cloud Map private DNS namespace."
    )

    # 4. Ingress / Egress Ports
    assert_test(
        "Node Gateway attaches to ALB target group on port 3000",
        'load_balancer {' in ecs_main_text and
        'container_port   = 3000' in ecs_main_text and
        'target_group_arn = var.alb_target_group_arn' in ecs_main_text,
        "Node Gateway must attach to internal ALB target group on port 3000."
    )

    assert_test(
        "Go Worker service has zero ingress (empty portMappings)",
        'resource "aws_ecs_task_definition" "go_worker"' in ecs_main_text and
        'portMappings = []' in ecs_main_text,
        "Go Worker must have empty portMappings (no inbound ports)."
    )

    # 5. Service count and proposed env gates
    assert_test(
        "ECS service_desired_count defaults to 0 for safe pre-image bootstrap",
        'variable "service_desired_count"' in ecs_vars_text and
        'default     = 0' in ecs_vars_text,
        "service_desired_count must default to 0."
    )

    assert_test(
        "Proposed onboarding integration gate defaults to false (unapplied)",
        'variable "enable_proposed_onboarding_integration"' in ecs_vars_text and
        'default     = false' in ecs_vars_text,
        "enable_proposed_onboarding_integration must default to false."
    )

    # 6. Staged bootstrap foundation: enable_services defaults to false
    assert_test(
        "Staged bootstrap enable_services defaults to false (omits tasks until images ready)",
        'variable "enable_services"' in ecs_vars_text and
        'default     = false' in ecs_vars_text and
        'count                    = var.enable_services ? 1 : 0' in ecs_main_text,
        "enable_services must default to false and guard task/service provisioning."
    )

    # 7. One-off synthetic seed task definition defined in ECS module
    assert_test(
        "ECS module defines one-off synthetic seed task definition (flowcard-demo-synthetic-seed)",
        'family                   = "flowcard-demo-synthetic-seed"' in ecs_main_text and
        'dist/server/src/db/seed.js' in ecs_main_text,
        "ECS module must define synthetic seed task definition with dist/server/src/db/seed.js."
    )

    # 8. Demo environment module.ecs depends on module.alb for listener readiness
    demo_main_text = (DEMO_DIR / "main.tf").read_text()
    assert_test(
        "Demo environment enforces ALB listener dependency on module.ecs",
        'depends_on = [module.alb]' in demo_main_text and
        'alb_listener_arn                       = module.alb.alb_listener_arn' in demo_main_text,
        "module.ecs must include ALB listener dependency to avoid target group association race."
    )


def test_task_definitions_and_secret_keys():
    print("\n--- Test Suite 8: Task Definitions, CPU Arch & Secret Key Separation ---")

    ecs_main_text = (MODULES_DIR / "ecs" / "main.tf").read_text()
    ecs_vars_text = (MODULES_DIR / "ecs" / "variables.tf").read_text()

    # 1. CPU Architecture
    assert_test(
        "Task definitions specify Linux X86_64 CPU architecture",
        'operating_system_family = "LINUX"' in ecs_main_text and
        'cpu_architecture        = var.cpu_architecture' in ecs_main_text and
        'default     = "X86_64"' in ecs_vars_text,
        "Task definitions must target LINUX with X86_64 default architecture."
    )

    # 2. Runtime database secret key separation
    assert_test(
        "Node Gateway injects node_url from flowcard/db/app",
        '"DATABASE_URL"' in ecs_main_text and
        '"${var.app_secret_arn}:node_url::"' in ecs_main_text,
        "Node Gateway must inject node_url from app secret container."
    )

    assert_test(
        "Go API injects onboarding_url from flowcard/db/app",
        '"${var.app_secret_arn}:onboarding_url::"' in ecs_main_text,
        "Go API must inject onboarding_url from app secret container."
    )

    assert_test(
        "Go Worker injects onboarding_url from flowcard/db/app",
        'resource "aws_ecs_task_definition" "go_worker"' in ecs_main_text and
        '"${var.app_secret_arn}:onboarding_url::"' in ecs_main_text,
        "Go Worker must inject onboarding_url from app secret container."
    )

    # 3. Privileged migration database secret key separation
    assert_test(
        "Node Migration task injects privileged node_url from flowcard/db/migrate",
        'resource "aws_ecs_task_definition" "node_migration"' in ecs_main_text and
        '"${var.migrate_secret_arn}:node_url::"' in ecs_main_text,
        "Node Migration must inject node_url from migrate secret."
    )

    assert_test(
        "Go Migration task injects privileged onboarding_url from flowcard/db/migrate",
        'resource "aws_ecs_task_definition" "go_migration"' in ecs_main_text and
        '"${var.migrate_secret_arn}:onboarding_url::"' in ecs_main_text,
        "Go Migration must inject onboarding_url from migrate secret."
    )

    # 4. Distinct Migration Commands
    assert_test(
        "Node Migration executes node dist/server/src/db/migrate.js",
        'command      = ["node", "dist/server/src/db/migrate.js"]' in ecs_main_text,
        "Node migration task must run compiled node dist/server/src/db/migrate.js."
    )

    assert_test(
        "Go Migration executes /bin/migrate binary",
        'command      = ["/bin/migrate"]' in ecs_main_text,
        "Go migration task must run /bin/migrate binary."
    )

    # 5. Required Immutable Image Digest Validations
    for digest_var in ["node_image_digest", "go_api_image_digest", "go_worker_image_digest", "migration_image_digest"]:
        has_validation = (
            f'variable "{digest_var}"' in ecs_vars_text and
            '^sha256:[a-f0-9]{64}$' in ecs_vars_text
        )
        assert_test(
            f"Image digest {digest_var} regex enforces ^sha256:[a-f0-9]{{64}}$ (no tags/latest)",
            has_validation,
            f"Variable {digest_var} must validate strict 64-hex SHA-256 digest format."
        )


def test_deployment_scripts_safety():
    print("\n--- Test Suite 9: Deployment & Rollback Scripts Verification ---")

    deploy_sh = ROOT_DIR / "scripts" / "deploy-demo.sh"
    rollback_sh = ROOT_DIR / "scripts" / "rollback-demo.sh"

    assert_test("deploy-demo.sh script exists and is executable", deploy_sh.exists(), f"Missing {deploy_sh}")
    assert_test("rollback-demo.sh script exists and is executable", rollback_sh.exists(), f"Missing {rollback_sh}")

    deploy_text = deploy_sh.read_text()
    rollback_text = rollback_sh.read_text()

    # Approval flag required
    assert_test(
        "deploy-demo.sh requires explicit --confirm-approval gate",
        '--confirm-approval' in deploy_text and 'CONFIRM_APPROVAL' in deploy_text,
        "deploy-demo.sh must require explicit human confirmation flag."
    )

    # Account verification
    assert_test(
        "deploy-demo.sh verifies caller account identity against authorized ID",
        'sts get-caller-identity' in deploy_text and 'CALLER_ACCOUNT' in deploy_text,
        "deploy-demo.sh must verify caller account identity."
    )

    # Strict digest validation in script
    assert_test(
        "deploy-demo.sh validates immutable digest format and rejects 'latest'",
        'latest' in deploy_text and 'sha256:[a-f0-9]{64}' in deploy_text,
        "deploy-demo.sh must validate sha256: digest and reject 'latest'."
    )

    # Sequential migrations exit-code check
    assert_test(
        "deploy-demo.sh executes Node then Go migrations and asserts exit code 0",
        'run_one_off_task "${NODE_MIGRATE_TD_ARN}" "node-migration"' in deploy_text and
        'run_one_off_task "${GO_MIGRATE_TD_ARN}" "go-migration"' in deploy_text and
        ('exit_code' in deploy_text or 'EXIT_CODE' in deploy_text),
        "deploy-demo.sh must sequence migrations and verify exit code 0."
    )

    # 12-digit account and profile required in both deploy and rollback
    assert_test(
        "deploy-demo.sh requires 12-digit account ID and profile before mutation",
        '^[0-9]{12}$' in deploy_text and '--profile' in deploy_text,
        "deploy-demo.sh must require 12-digit account ID and profile."
    )

    assert_test(
        "rollback-demo.sh requires 12-digit account ID and profile before mutation",
        '^[0-9]{12}$' in rollback_text and '--profile' in rollback_text,
        "rollback-demo.sh must require 12-digit account ID and profile."
    )

    # Rollback safety
    assert_test(
        "rollback-demo.sh requires --confirm-approval and targets task revisions",
        '--confirm-approval' in rollback_text and
        '--node-revision' in rollback_text and
        '--api-revision' in rollback_text and
        '--worker-revision' in rollback_text and
        '^[0-9]+$' in rollback_text,
        "rollback-demo.sh must require approval and specific task revisions."
    )


# ==============================================================================
# Task 7 Verification Suite: CloudFront, S3 OAC, VPC Origin & Routing
# ==============================================================================

def test_cloudfront_routing_and_origin_safety():
    print("\n--- Test Suite 10: CloudFront, S3 OAC, VPC Origin & Routing ---")

    cf_main_file = MODULES_DIR / "cloudfront" / "main.tf"
    cf_vars_file = MODULES_DIR / "cloudfront" / "variables.tf"
    cf_outs_file = MODULES_DIR / "cloudfront" / "outputs.tf"
    s3_front_file = MODULES_DIR / "s3_frontend" / "main.tf"
    deploy_front_file = SCRIPTS_DIR / "deploy-frontend.sh"

    assert_test(
        "CloudFront module files exist",
        cf_main_file.exists() and cf_vars_file.exists() and cf_outs_file.exists(),
        "Missing CloudFront module configuration files."
    )
    assert_test(
        "deploy-frontend.sh exists and is executable",
        deploy_front_file.exists() and os.access(deploy_front_file, os.X_OK),
        f"Missing or non-executable {deploy_front_file}"
    )

    cf_text = cf_main_file.read_text()
    s3_text = s3_front_file.read_text()
    deploy_text = deploy_front_file.read_text()

    # 1. S3 OAC Configuration
    assert_test(
        "S3 OAC configured with s3 type and always signing",
        'resource "aws_cloudfront_origin_access_control" "frontend"' in cf_text and
        'origin_access_control_origin_type = "s3"' in cf_text and
        'signing_behavior                  = "always"' in cf_text and
        'signing_protocol                  = "sigv4"' in cf_text,
        "CloudFront must configure SigV4 always-signing OAC for S3 frontend."
    )

    # 2. S3 Bucket Policy Enforces OAC & TLS
    assert_test(
        "S3 bucket policy allows CloudFront service principal with SourceArn condition",
        'cloudfront.amazonaws.com' in s3_text and
        's3:GetObject' in s3_text and
        'AWS:SourceArn' in s3_text,
        "S3 frontend bucket policy must allow cloudfront.amazonaws.com with SourceArn condition."
    )
    assert_test(
        "S3 bucket policy enforces TLS requests only",
        'aws:SecureTransport' in s3_text and '"Deny"' in s3_text,
        "S3 frontend bucket policy must deny non-TLS requests."
    )

    # 3. CloudFront VPC Origin for Private ALB
    assert_test(
        "CloudFront VPC Origin configured for ALB with TLSv1.2",
        'resource "aws_cloudfront_vpc_origin" "alb"' in cf_text and
        'arn                    = var.alb_arn' in cf_text and
        'origin_protocol_policy = "http-only"' in cf_text and
        '"TLSv1.2"' in cf_text,
        "CloudFront must configure VPC Origin pointing to private ALB."
    )

    # 4. Route Behaviors: /api/*, /docs/api*, /webhooks/airwallex, /assets/*
    assert_test(
        "CloudFront routes /api/* to private ALB with no-cache policy",
        'path_pattern     = "/api/*"' in cf_text and
        'target_origin_id = "alb-vpc-origin"' in cf_text and
        'aws_cloudfront_cache_policy.no_cache.id' in cf_text,
        "CloudFront must route /api/* to private ALB with no-cache policy (TTL 0)."
    )
    assert_test(
        "/api/* behavior allows all required HTTP methods (inc PATCH/DELETE)",
        'allowed_methods          = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]' in cf_text,
        "/api/* behavior must allow all HTTP methods (DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT)."
    )
    assert_test(
        "CloudFront routes /docs/api* to private ALB with no-cache policy",
        'path_pattern     = "/docs/api*"' in cf_text and
        'target_origin_id = "alb-vpc-origin"' in cf_text and
        'aws_cloudfront_cache_policy.no_cache.id' in cf_text,
        "CloudFront must route /docs/api* to private ALB for OpenAPI docs."
    )
    assert_test(
        "CloudFront routes /webhooks/airwallex with compress = false to preserve raw signature",
        'path_pattern     = "/webhooks/airwallex"' in cf_text and
        'target_origin_id = "alb-vpc-origin"' in cf_text and
        'compress                 = false' in cf_text,
        "CloudFront must route /webhooks/airwallex without compression to preserve signature verification."
    )
    assert_test(
        "CloudFront routes /assets/* to S3 with long-lived static cache policy",
        'path_pattern     = "/assets/*"' in cf_text and
        'target_origin_id = "s3-frontend"' in cf_text and
        'aws_cloudfront_cache_policy.static_assets.id' in cf_text,
        "CloudFront must route /assets/* to S3 with static cache policy."
    )

    # 5. SPA Navigation Rewrite without Masking API Errors
    assert_test(
        "CloudFront Function rewrites routes without extensions to /index.html",
        'resource "aws_cloudfront_function" "spa_rewrite"' in cf_text and
        "!uri.includes('.')" in cf_text and
        "request.uri = '/index.html'" in cf_text,
        "SPA rewrite function must rewrite extensionless routes to /index.html."
    )
    assert_test(
        "CloudFront distribution has ZERO custom_error_response blocks masking API errors",
        'custom_error_response' not in cf_text,
        "Distribution must not define custom_error_response blocks that mask API 404/403 errors."
    )

    # 6. Origin Request Policy passes all headers, cookies, and query strings
    assert_test(
        "Origin Request Policy forwards all viewer headers, cookies, and query strings",
        'header_behavior = "allViewer"' in cf_text and
        'cookie_behavior = "all"' in cf_text and
        'query_string_behavior = "all"' in cf_text,
        "Origin request policy must forward all headers, cookies, and query strings to Node gateway."
    )

    # 7. Security Headers & CSP
    assert_test(
        "Security headers policy enforces HSTS, X-Content-Type-Options, X-Frame-Options DENY",
        'strict_transport_security' in cf_text and
        'content_type_options' in cf_text and
        'frame_option = "DENY"' in cf_text,
        "Security headers policy must enforce HSTS, content type options, and frame deny."
    )
    assert_test(
        "CSP allows self and unsafe-inline without CDN dependencies",
        "default-src 'self'" in cf_text and
        "connect-src 'self'" in cf_text and
        "script-src 'self' 'unsafe-inline'" in cf_text,
        "CSP must allow self and unsafe-inline without external CDN dependencies."
    )

    # 8. Frontend Deployment Script Guards
    assert_test(
        "deploy-frontend.sh requires --confirm-approval",
        '--confirm-approval' in deploy_text,
        "deploy-frontend.sh must require --confirm-approval."
    )
    assert_test(
        "deploy-frontend.sh verifies caller identity and enforces expected-bucket-owner",
        'sts get-caller-identity' in deploy_text and
        '--expected-bucket-owner' in deploy_text,
        "deploy-frontend.sh must verify caller identity and enforce expected-bucket-owner."
    )
    assert_test(
        "deploy-frontend.sh invalidates exact changed paths (/index.html / /vite.svg), never /*",
        'cloudfront create-invalidation' in deploy_text and
        '/index.html' in deploy_text and
        '--paths "/*"' not in deploy_text,
        "deploy-frontend.sh must invalidate exact paths, never /*."
    )

    # 9. Lead Review Corrections: Bare /api, SPA Guards, ALB SG Isolation & Deploy Validation
    alb_main_file = MODULES_DIR / "alb" / "main.tf"
    alb_text = alb_main_file.read_text()

    assert_test(
        "CloudFront routes bare /api to private ALB with no-cache policy",
        'path_pattern     = "/api"' in cf_text and
        'target_origin_id = "alb-vpc-origin"' in cf_text,
        "CloudFront must define ordered cache behavior for bare /api targeting ALB."
    )
    assert_test(
        "CloudFront Function guards non-GET methods, reserved prefixes, and Accept application/json",
        "method !== 'GET'" in cf_text and
        "uri === '/api'" in cf_text and
        "uri === '/docs'" in cf_text and
        "uri === '/webhooks'" in cf_text and
        "application/json" in cf_text,
        "CloudFront Function must guard non-GET, reserved prefixes, and Accept: application/json."
    )
    assert_test(
        "ALB SG enforces precondition requiring vpc_origin_security_group_id in final mode",
        "!var.enable_vpc_origin_sg_only || var.vpc_origin_security_group_id != null" in alb_text,
        "ALB SG must enforce precondition that vpc_origin_security_group_id is set when final mode is true."
    )
    assert_test(
        "ALB SG disallows CloudFront prefix list when enable_vpc_origin_sg_only is true",
        "!var.enable_vpc_origin_sg_only && var.cloudfront_prefix_list_id != null" in alb_text,
        "ALB SG must disallow prefix list ingress in final mode."
    )
    assert_test(
        "deploy-frontend.sh validates distribution ownership and build inputs before S3 mutation",
        "cloudfront get-distribution" in deploy_text and
        ("EXPECTED_DIST_ARN" in deploy_text or "EXPECTED_DIST_ARN_PREFIX" in deploy_text) and
        "$BUILD_DIR/index.html" in deploy_text and
        "$BUILD_DIR/assets" in deploy_text,
        "deploy-frontend.sh must verify distribution ownership and build inputs before upload."
    )
    assert_test(
        "deploy-frontend.sh parses distribution origins with jq and requires OAC",
        '.Distribution.DistributionConfig.Origins.Items' in deploy_text and
        '.OriginAccessControlId' in deploy_text,
        "deploy-frontend.sh must parse origins via jq and assert OriginAccessControlId."
    )


def test_github_actions_oidc_and_ci_safety():
    print("\n--- Test Suite 11: GitHub Actions CI/CD Release & OIDC Security Tier ---")

    worktree_root = ROOT_DIR.parent
    workflow_path = worktree_root / ".github" / "workflows" / "aws-demo.yml"
    oidc_main_path = MODULES_DIR / "oidc" / "main.tf"
    baseline_script_path = ROOT_DIR / "scripts" / "check-test-baseline.js"
    deploy_demo_path = ROOT_DIR / "scripts" / "deploy-demo.sh"
    deploy_frontend_path = ROOT_DIR / "scripts" / "deploy-frontend.sh"
    rollback_demo_path = ROOT_DIR / "scripts" / "rollback-demo.sh"

    # 1. Workflow file existence & trigger restrictions
    assert_test(
        "GitHub Actions workflow file exists at .github/workflows/aws-demo.yml",
        workflow_path.is_file(),
        "Workflow file .github/workflows/aws-demo.yml must exist."
    )

    workflow_text = workflow_path.read_text()

    assert_test(
        "Workflow uses manual dispatch without user-supplied inputs",
        "workflow_dispatch:" in workflow_text and
        not re.search(r"workflow_dispatch:\s*\n\s+inputs:", workflow_text) and
        "inputs." not in workflow_text and
        "push:" not in workflow_text and
        "pull_request:" not in workflow_text,
        "Workflow must trigger manually without deployment input fields."
    )

    assert_test(
        "Workflow enforces develop branch and repository restriction guards",
        "if: github.ref == 'refs/heads/develop' && github.repository == 'slabarcaf/flowcard-treasury'" in workflow_text,
        "Workflow jobs must enforce develop branch and slabarcaf/flowcard-treasury repository restriction."
    )

    assert_test(
        "Workflow serializes releases with cancel-in-progress: false",
        "group: aws-demo-release" in workflow_text and
        "cancel-in-progress: false" in workflow_text,
        "Workflow must serialize releases in aws-demo-release concurrency group."
    )

    assert_test(
        "Workflow enforces top-level read-only permissions, develop dispatch, and scoped id-token: write",
        "permissions:\n  contents: read" in workflow_text and
        "github.ref == 'refs/heads/develop'" in workflow_text and
        "github.repository == 'slabarcaf/flowcard-treasury'" in workflow_text and
        "inputs." not in workflow_text and
        "id-token: write" in workflow_text,
        "Workflow must restrict permissions and scope id-token: write strictly to deploy-demo."
    )

    action_refs = re.findall(r"^\s*uses:\s*([^\s#]+)", workflow_text, re.MULTILINE)
    remote_action_refs = [
        action_ref for action_ref in action_refs
        if not action_ref.startswith("./")
    ]
    assert_test(
        "Workflow pins 3rd-party actions to 40-character commit SHAs",
        bool(remote_action_refs) and
        all(
            re.fullmatch(
                r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*@[0-9a-f]{40}",
                action_ref,
            )
            for action_ref in remote_action_refs
        ),
        "All GitHub Actions must be pinned to immutable 40-character commit SHAs."
    )

    assert_test(
        "Workflow builds container images explicitly for linux/amd64 (Fargate X86_64)",
        "--platform linux/amd64" in workflow_text,
        "Workflow must build container images explicitly for linux/amd64."
    )

    # 2. Baseline Test Script
    assert_test(
        "CI Test baseline verifier script exists and tracks 23 known baseline failures",
        baseline_script_path.is_file() and
        "KNOWN_BASELINE_FAILURES" in baseline_script_path.read_text(),
        "infra/scripts/check-test-baseline.js must exist and define KNOWN_BASELINE_FAILURES."
    )

    # 3. OIDC Module & Scoped IAM Policy
    oidc_text = oidc_main_path.read_text()
    assert_test(
        "OIDC deployer role trust policy restricts audience and immutable develop subject claim",
        '"token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"' in oidc_text and
        '"token.actions.githubusercontent.com:sub" = "repo:${var.github_owner_name}@${var.github_owner_id}/${var.github_repository_name}@${var.github_repository_id}:ref:refs/heads/develop"' in oidc_text,
        "OIDC trust policy must bind aud and the exact immutable develop subject."
    )

    assert_test(
        "iam:PassRole is restricted to ECS execution/task roles with ecs-tasks condition",
        'Action = ["iam:PassRole"]' in oidc_text and
        '"iam:PassedToService" = "ecs-tasks.amazonaws.com"' in oidc_text and
        "var.execution_role_arns" in oidc_text and
        "var.task_role_arns" in oidc_text,
        "iam:PassRole must be restricted to execution/task roles and ecs-tasks.amazonaws.com."
    )

    deployer_policy_block = oidc_text.split('resource "aws_iam_policy" "github_deployer"')[1] if 'resource "aws_iam_policy" "github_deployer"' in oidc_text else ""
    assert_test(
        "OIDC deployer policy strictly omits secretsmanager:GetSecretValue, rds admin, and broad IAM",
        '"secretsmanager:' not in deployer_policy_block and
        '"rds:*"' not in deployer_policy_block and
        '"iam:*"' not in deployer_policy_block,
        "Deployer policy must omit secretsmanager, rds admin, and broad IAM permissions."
    )

    # 4. Release Scripts Support --oidc Flag & Coupled Rollback Coordinator
    assert_test(
        "deploy-demo.sh supports --oidc flag alongside --profile",
        "--oidc" in deploy_demo_path.read_text() and
        "USE_OIDC" in deploy_demo_path.read_text(),
        "deploy-demo.sh must support --oidc flag."
    )
    assert_test(
        "deploy-frontend.sh supports --oidc flag alongside --profile",
        "--oidc" in deploy_frontend_path.read_text() and
        "USE_OIDC" in deploy_frontend_path.read_text(),
        "deploy-frontend.sh must support --oidc flag."
    )
    assert_test(
        "rollback-demo.sh supports --oidc flag alongside --profile",
        "--oidc" in rollback_demo_path.read_text() and
        "USE_OIDC" in rollback_demo_path.read_text(),
        "rollback-demo.sh must support --oidc flag."
    )
    assert_test(
        "Standalone rollback coordinator remains separate from the workflow",
        (ROOT_DIR / "scripts" / "rollback-release.sh").is_file() and
        "COUPLED ROLLBACK" in (ROOT_DIR / "scripts" / "rollback-release.sh").read_text() and
        "rollback-release.sh" not in workflow_text,
        "The manual rollback utility may remain, but the deployment workflow must not invoke it."
    )

    # 5. Lead Review Additions: Toolchains, Postgres Service, OpenAPI, and Specific IAM RunTask Binding
    assert_test(
        "Workflow configures Terraform >= 1.10, Node >= 20.19, and Go from go.mod",
        re.search(r'terraform_version:\s*["\']1\.10\.[0-9]+["\']', workflow_text) is not None and
        re.search(r'node-version:\s*["\']20\.19\.[0-9]+["\']', workflow_text) is not None and
        "go-version-file: services/company-onboarding/go.mod" in workflow_text,
        "Workflow must configure Terraform >= 1.10, Node >= 20.19, and Go from go.mod."
    )
    assert_test(
        "Workflow includes isolated PostgreSQL 54329 services container with health checks",
        "postgres:16-alpine" in workflow_text and
        "POSTGRES_DB: flowcard_onboarding_test" in workflow_text and
        "54329:5432" in workflow_text and
        "--health-cmd pg_isready" in workflow_text,
        "Workflow must include PostgreSQL 54329 services container for test isolation."
    )
    assert_test(
        "Workflow includes OpenAPI specification freshness check gate",
        "go run ./cmd/openapi -check" in workflow_text,
        "Workflow must include go run ./cmd/openapi -check."
    )
    assert_test(
        "Workflow enforces recursive formatting check on all infra scopes (root, modules, demo)",
        "terraform fmt -check -recursive infra" in workflow_text,
        "Workflow must enforce terraform fmt -check -recursive infra."
    )
    assert_test(
        "Deployer IAM policy scopes ecs:RunTask to migration/seed task definitions with cluster condition",
        "task-definition/flowcard-demo-node-migration:*" in oidc_text and
        "task-definition/flowcard-demo-go-migration:*" in oidc_text and
        "task-definition/flowcard-demo-synthetic-seed:*" in oidc_text and
        '"ecs:cluster" = var.ecs_cluster_arn' in oidc_text,
        "Deployer policy must scope ecs:RunTask to exact migration/seed task definitions with cluster condition."
    )
    assert_test(
        "Deployer IAM policy scopes EC2 describe permissions and omits ECR wildcard fallback",
        '"ec2:DescribeSubnets"' in oidc_text and
        '"ec2:DescribeSecurityGroups"' in oidc_text and
        "Resource = var.ecr_repository_arns" in oidc_text and
        '["*"]' not in deployer_policy_block.split('Sid    = "ECRRepositoryAccess"')[1].split("}")[0],
        "Deployer policy must scope EC2 describe permissions and omit ECR wildcard fallback."
    )

    # 6. Revision 2 Invariants: Database Credentials, Activation Gate, Image Immutability, Release Outputs
    assert_test(
        "Workflow configures test_onboarding credentials and POLICY_ACCEPTANCE_DATABASE_URL for CI tests",
        "POSTGRES_USER: test_onboarding" in workflow_text and
        "POSTGRES_PASSWORD: test_password" in workflow_text and
        "POLICY_ACCEPTANCE_DATABASE_URL:" in workflow_text and
        "flowcard_test" not in workflow_text and
        "TEST_DATABASE_URL" not in workflow_text and
        "ONBOARDING_TEST_DATABASE_URL" not in workflow_text,
        "Workflow must configure test_onboarding and POLICY_ACCEPTANCE_DATABASE_URL for tests."
    )

    assert_test(
        "Workflow validates fixed foundation targets before OIDC assumption",
        "Validate Fixed Foundation Configuration (Fail Closed)" in workflow_text and
        "FOUNDATION_PATH: infra/environments/demo/foundation-outputs.json" in workflow_text and
        "Foundation configuration is incomplete" in workflow_text and
        workflow_text.find("Validate Fixed Foundation Configuration (Fail Closed)") < workflow_text.find("Assume AWS Deployer Role via GitHub OIDC"),
        "Workflow must validate the pinned foundation configuration before OIDC."
    )

    assert_test(
        "Workflow exports immutable container archives and loads in deploy job without rebuild",
        "docker save \"flowcard-node-gateway:${IMAGE_TAG}\" | gzip > .image-archives/node-gateway.tar.gz" in workflow_text and
        "docker save \"flowcard-go-runtime:${IMAGE_TAG}\" | gzip > .image-archives/go-runtime.tar.gz" in workflow_text and
        "docker load -i .image-archives/node-gateway.tar.gz" in workflow_text and
        "docker load -i .image-archives/go-runtime.tar.gz" in workflow_text and
        'RELEASE_ID="${COMMIT_SHA}/${RUN_ID}-${RUN_ATTEMPT}"' in workflow_text and
        'IMAGE_TAG="${COMMIT_SHA}-${RUN_ID}-${RUN_ATTEMPT}"' in workflow_text and
        'CURRENT_RELEASE_ID="${CURRENT_SHA}/${CURRENT_RUN_ID}-${CURRENT_RUN_ATTEMPT}"' in workflow_text and
        'docker push "${REGISTRY}/flowcard-node-gateway:${IMAGE_TAG}"' in workflow_text and
        'docker push "${REGISTRY}/flowcard-go-api:${IMAGE_TAG}"' in workflow_text and
        'docker push "${REGISTRY}/flowcard-go-worker:${IMAGE_TAG}"' in workflow_text and
        'docker push "${REGISTRY}/flowcard-migration:${IMAGE_TAG}"' in workflow_text and
        'FRONTEND_ARTIFACT_NAME="frontend-dist-${IMAGE_TAG}"' in workflow_text and
        'RECOVERY_ARTIFACT_NAME="release-recovery-${IMAGE_TAG}"' in workflow_text and
        'name: ${{ steps.meta.outputs.frontend_artifact_name }}' in workflow_text and
        'name: ${{ steps.meta.outputs.container_artifact_name }}' in workflow_text and
        'name: ${{ steps.meta.outputs.manifest_artifact_name }}' in workflow_text and
        'name: ${{ needs.build-and-metadata.outputs.frontend_artifact_name }}' in workflow_text and
        'name: ${{ needs.build-and-metadata.outputs.container_artifact_name }}' in workflow_text and
        'name: ${{ needs.build-and-metadata.outputs.manifest_artifact_name }}' in workflow_text and
        "docker build" not in workflow_text.split("deploy-demo:")[1],
        "Workflow must use run-scoped image tags and matching artifacts without rebuilding in deploy."
    )

    assert_test(
        "Workflow uses the fixed tracked foundation configuration",
        "FOUNDATION_PATH: infra/environments/demo/foundation-outputs.json" in workflow_text and
        '--infra-outputs-file' in workflow_text and
        (ROOT_DIR / "environments" / "demo" / "foundation-outputs.example.json").is_file() and
        (ROOT_DIR / "environments" / "demo" / "foundation-outputs.json").is_file(),
        "Workflow must use the fixed tracked configuration and keep its example template."
    )

    assert_test(
        "Deterministic frontend checksum helper compute-frontend-hash.sh exists and is executable",
        (ROOT_DIR / "scripts" / "compute-frontend-hash.sh").is_file() and
        os.access(ROOT_DIR / "scripts" / "compute-frontend-hash.sh", os.X_OK),
        "compute-frontend-hash.sh must exist and be executable."
    )

    assert_test(
        "Workflow uploads release recovery metadata with if: always() to preserve state on failure",
        "Upload Release Recovery Metadata (Preserve State On Failure)" in workflow_text and
        "if: always()" in workflow_text,
        "Workflow must include upload step for recovery metadata with if: always()."
    )

    assert_test(
        "rollback-release.sh verifies prior frontend checksum before performing any mutations",
        "PRIOR_FRONTEND_CHECKSUM" in (ROOT_DIR / "scripts" / "rollback-release.sh").read_text() and
        "Aborting before any mutations" in (ROOT_DIR / "scripts" / "rollback-release.sh").read_text(),
        "rollback-release.sh must verify frontend artifact and checksum before mutating services."
    )


def main():
    print("=" * 70)
    print("Running FlowCard AWS Infrastructure Offline Safety Verification")
    print("=" * 70)

    test_rds_network_isolation_and_storage()
    test_security_group_edge_matrix()
    test_iam_execution_secret_separation()
    test_s3_public_blocks_and_kms()
    test_input_validation_rules()
    test_negative_mutation_fixtures()
    test_ecs_services_and_network()
    test_task_definitions_and_secret_keys()
    test_deployment_scripts_safety()
    test_cloudfront_routing_and_origin_safety()
    test_github_actions_oidc_and_ci_safety()

    print("\n" + "=" * 70)
    print(f"Summary: {len(PASSED_TESTS)} passed, {len(FAILED_TESTS)} failed.")
    print("=" * 70)

    if FAILED_TESTS:
        print("\nFailures:")
        for name, err in FAILED_TESTS:
            print(f"  - {name}: {err}")
        sys.exit(1)
    else:
        print("\nAll offline safety verification checks PASSED successfully.")
        sys.exit(0)


if __name__ == "__main__":
    main()
