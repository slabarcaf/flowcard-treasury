# Authorized AWS account
allowed_account_ids = ["968198283143"]

# Approved AWS region
aws_region = "us-west-2"

# Existing frontend bucket
frontend_bucket_name = "flowcard-frontend-968198283143-us-west-2"

# Existing documents bucket
documents_bucket_name = "flowcard-documents-968198283143-us-west-2"

# Existing database engine
db_engine_version = "17.11"

# Register service definitions
enable_services = true

# Running service replicas
service_desired_count = 1

# Approved runtime integration
enable_proposed_onboarding_integration = true

# Published immutable images
node_image_digest      = "sha256:0d9a93057e93d08171fbb8fc378669c093b1a596538d16d54b55a67835027c39"
go_api_image_digest    = "sha256:9b649dcb55423a202d8f8b0ba476e9728366a80fbd0e28bb53e28d808b559872"
go_worker_image_digest = "sha256:9b649dcb55423a202d8f8b0ba476e9728366a80fbd0e28bb53e28d808b559872"
migration_image_digest = "sha256:9b649dcb55423a202d8f8b0ba476e9728366a80fbd0e28bb53e28d808b559872"

# Restrict CloudFront origin
enable_vpc_origin_sg_only = true

# Existing CloudFront origin SG
vpc_origin_security_group_id = "sg-07db7b84d272bbf7b"
