import fs from 'node:fs/promises';
import { createHash } from 'node:crypto';

export const repository = 'slabarcaf/flowcard-treasury';
const arn = (region, account, service) => `arn:aws:${service}:${region}:${account}:`;

export function validateFoundation(f) {
  if (f.generation !== 'go-stack-v1' || f.go_stack_foundation_enabled !== true ||
      !/^\d{12}$/.test(f.account_id ?? '') || !/^[a-z]{2}-[a-z]+-\d$/.test(f.aws_region ?? '') ||
      f.ecs_cluster_name !== 'flowcard-demo-cluster' ||
      f.ecs_cluster_arn !== arn(f.aws_region, f.account_id, 'ecs') + 'cluster/flowcard-demo-cluster' ||
      !/^flowcard[a-z0-9_]*$/.test(f.database_name ?? '') ||
      !/^[a-z0-9.-]+\.rds\.amazonaws\.com$/.test(f.database_host ?? '') ||
      !Array.isArray(f.private_subnet_ids) || !f.private_subnet_ids.length ||
      !f.private_subnet_ids.every(x => /^subnet-[a-f0-9]+$/.test(x)) ||
      !/^sg-[a-f0-9]+$/.test(f.migration_task_security_group_id ?? '')) {
    throw Error('Go foundation is not prepared; apply the reviewed opt-in foundation first');
  }
  for (const name of ['flowcard-node-gateway', 'flowcard-migration']) {
    if (f.ecr_repository_urls?.[name] !== `${f.account_id}.dkr.ecr.${f.aws_region}.amazonaws.com/${name}`) {
      throw Error('ECR repository binding mismatch');
    }
  }
  const paths = { app: 'flowcard/db/app', migrate: 'flowcard/db/migrate', service_auth: 'flowcard/service-auth', airwallex: 'flowcard/airwallex' };
  for (const [key, path] of Object.entries(paths)) {
    const prefix = arn(f.aws_region, f.account_id, 'secretsmanager') + 'secret:' + path + '-';
    const value = f.secret_arns?.[key];
    if (typeof value !== 'string' || !value.startsWith(prefix) || !/^[A-Za-z0-9]{6}$/.test(value.slice(prefix.length))) {
      throw Error('Secret reference binding mismatch');
    }
  }
  for (const key of ['onboarding', 'shortfall', 'servicing']) {
    if (!f.native_target_groups?.[key]?.startsWith(arn(f.aws_region, f.account_id, 'elasticloadbalancing') + 'targetgroup/flowcard-') ||
        !f.native_rule_arns?.[key]?.startsWith(arn(f.aws_region, f.account_id, 'elasticloadbalancing') + 'listener-rule/app/')) {
      throw Error('Go native routing foundation missing');
    }
  }
  if (!f.gateway_target_group_arn?.startsWith(arn(f.aws_region, f.account_id, 'elasticloadbalancing') + 'targetgroup/flowcard-demo-node-tg/') ||
      !new RegExp(`^${arn(f.aws_region, f.account_id, 'ecs')}task-definition/flowcard-demo-go-migration:[0-9]+$`).test(f.migration_task_definition_arn ?? '') ||
      f.frontend_bucket_name !== `flowcard-frontend-${f.account_id}-${f.aws_region}` ||
      !/^E[A-Z0-9]+$/.test(f.cloudfront_distribution_id ?? '')) throw Error('Release target binding missing');
  if (f.runtime_task_role_arn !== `arn:aws:iam::${f.account_id}:role/FlowCardApiTaskRole` ||
      f.documents_bucket_name !== `flowcard-documents-${f.account_id}-${f.aws_region}` ||
      !f.documents_kms_key_arn?.startsWith(arn(f.aws_region, f.account_id, 'kms') + 'key/')) throw Error('Document storage binding missing');
  if (!Array.isArray(f.allowed_origins) || !f.allowed_origins.length) throw Error('Explicit browser origins required');
  for (const origin of f.allowed_origins) {
    const u = new URL(origin);
    if (u.protocol !== 'https:' || u.origin !== origin || u.username || u.password || origin.includes('*')) {
      throw Error('Secure exact browser origin required');
    }
  }
  return f;
}

export function validateManifest(m, sha) {
  if (m.generation !== 'go-stack-v1' || m.repository !== repository || m.branch !== 'develop' ||
      m.frontend_entry !== 'original-go-product' ||
      !/^[a-f0-9]{40}$/.test(m.commit_sha ?? '') || m.commit_sha !== sha ||
      !/^linux\/amd64$/.test(m.platform ?? '') ||
      !/^[a-f0-9]{40}-[0-9]+-[0-9]+$/.test(m.image_tag ?? '') ||
      !m.image_tag.startsWith(m.commit_sha + '-') ||
      !/^[a-f0-9]{64}$/.test(m.frontend_checksum_sha256 ?? '') ||
      m.archive?.file !== 'go-stack.tar.gz' ||
      !/^[a-f0-9]{64}$/.test(m.archive?.sha256 ?? '') ||
      !/^sha256:[a-f0-9]{64}$/.test(m.archive?.image_id ?? '')) {
    throw Error('Immutable exact-source Go release manifest required');
  }
  return m;
}

export async function verifyArchive(file, expected) {
  const bytes = await fs.readFile(file);
  if (createHash('sha256').update(bytes).digest('hex') !== expected) throw Error('Container archive checksum mismatch');
}

function imageRef(f, key, digest) {
  if (!/^sha256:[a-f0-9]{64}$/.test(digest ?? '')) throw Error('Immutable image digest required');
  return `${f.ecr_repository_urls[key]}@${digest}`;
}

function secret(f, value) {
  const [kind, key] = value.split(':');
  if (!f.secret_arns[kind] || (kind !== 'service_auth' && !/^[a-z][a-z0-9_]+$/.test(key ?? ''))) throw Error('Unknown secret field');
  return key ? `${f.secret_arns[kind]}:${key}::` : f.secret_arns[kind];
}

function baseTask(template, family, f) {
  const t = template.taskDefinition ?? template;
  const migration = family === 'flowcard-demo-go-migration';
  const rolePrefix = `arn:aws:iam::${f.account_id}:role/`;
  if (t.family !== family || t.networkMode !== 'awsvpc' || !t.requiresCompatibilities?.includes('FARGATE') ||
      t.runtimePlatform?.cpuArchitecture !== 'X86_64' ||
      t.executionRoleArn !== rolePrefix + (migration ? 'FlowCardMigrationExecutionRole' : 'FlowCardNodeExecutionRole') ||
      !(migration ? [rolePrefix + 'FlowCardMigrationTaskRole'] : [rolePrefix + 'FlowCardNodeTaskRole', rolePrefix + 'FlowCardApiTaskRole']).includes(t.taskRoleArn)) throw Error('Task template binding mismatch');
  return { family, networkMode: 'awsvpc', requiresCompatibilities: ['FARGATE'],
    runtimePlatform: { cpuArchitecture: 'X86_64', operatingSystemFamily: 'LINUX' },
    executionRoleArn: t.executionRoleArn, taskRoleArn: t.taskRoleArn,
    cpu: '1024', memory: '2048' };
}

export function renderTasks(f, contract, runtimeTemplate, migrationTemplate, digests, options = {}) {
  validateFoundation(f);
  if (contract.schema_version !== 1 || contract.generation !== 'go-stack-v1' || contract.services.length !== 9) throw Error('Go runtime contract invalid');
  const image = imageRef(f, 'flowcard-node-gateway', digests.runtime);
  const migrationImage = imageRef(f, 'flowcard-migration', digests.migration);
  const substitutions = { facility_id: options.facility_id ?? 'sandbox-primary', source_binding_id: options.source_binding_id ?? 'flowcard-demo-go-v1', allowed_origins: f.allowed_origins.join(','),
    aws_region: f.aws_region, documents_bucket: f.documents_bucket_name, documents_kms_key: f.documents_kms_key_arn };
  for (const key of ['facility_id', 'source_binding_id']) if (!/^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/.test(substitutions[key])) throw Error('Installation identity invalid');
  const log = (group, prefix) => ({ logDriver: 'awslogs', options: { 'awslogs-group': group, 'awslogs-region': f.aws_region, 'awslogs-stream-prefix': prefix } });
  const environment = Object.entries({ EXPECTED_DATABASE_NAME: f.database_name, EXPECTED_DATABASE_HOST: f.database_host,
    PGSSLROOTCERT: '/etc/ssl/certs/rds-global-bundle.pem' }).map(([name, value]) => ({ name, value }));
  const dbSecrets = contract.database_bindings.map(b => ({ name: b.env, valueFrom: secret(f, `app:${b.key}`) }));
  const serviceSecrets = [...new Set(contract.services.flatMap(s => Object.values(s.secrets)))];
  const keyNames = { 'app:go_shortfall_secret': 'GO_SHORTFALL_SECRET', 'app:go_underwriting_secret': 'GO_UNDERWRITING_SECRET',
    'app:go_coordinator_reader_secret': 'GO_COORDINATOR_READER_SECRET', 'app:go_servicing_coordinator_secret': 'GO_SERVICING_COORDINATOR_SECRET',
    'app:go_servicing_reader_secret': 'GO_SERVICING_READER_SECRET', 'app:go_operator_secret': 'GO_OPERATOR_SECRET', 'app:go_customer_secret': 'GO_CUSTOMER_SECRET',
    service_auth: 'ONBOARDING_SERVICE_SECRET', 'airwallex:client_id': 'AIRWALLEX_CLIENT_ID', 'airwallex:api_key': 'AIRWALLEX_API_KEY',
    'airwallex:account_id': 'AIRWALLEX_ACCOUNT_ID', 'airwallex:shortfall_webhook_secret': 'SHORTFALL_WEBHOOK_SECRET',
    'airwallex:servicing_webhook_secret': 'SERVICING_WEBHOOK_SECRET', 'airwallex:onboarding_webhook_secret': 'ONBOARDING_WEBHOOK_SECRET' };
  // Runtime preflight uses the runtime execution role. It receives no admin URL.
  const preflight = baseTask(runtimeTemplate, contract.runtime_family, f);
  preflight.family = contract.migration_family;
  preflight.taskRoleArn = `arn:aws:iam::${f.account_id}:role/FlowCardNodeTaskRole`;
  preflight.cpu = '256'; preflight.memory = '512';
  preflight.containerDefinitions = [{ name: 'go-migrate', image, essential: true, user: '10001:10001',
    entryPoint: ['/opt/flowcard/browser-gateway/deploytool'], command: ['preflight'], environment,
    secrets: [...dbSecrets, ...serviceSecrets.filter(k => keyNames[k]).map(k => ({ name: keyNames[k], valueFrom: secret(f, k) }))],
    logConfiguration: log('/ecs/flowcard-node-gateway', 'go-preflight') }];
  const migration = baseTask(migrationTemplate, contract.migration_family, f);
  migration.cpu = '256'; migration.memory = '512';
  migration.containerDefinitions = [{ name: 'go-migrate', image: migrationImage, essential: true, user: '10001:10001',
    entryPoint: ['/opt/flowcard/migrate-go-stack.sh'], environment,
    secrets: [...dbSecrets, { name: 'MIGRATION_DATABASE_URL', valueFrom: secret(f, 'migrate:go_stack_url') },
      { name: 'GO_BOOTSTRAP_JSON', valueFrom: secret(f, 'migrate:go_bootstrap_json') }],
    logConfiguration: log('/ecs/flowcard-migration', 'go-migrations') }];
  const adminCheck = structuredClone(migration);
  adminCheck.containerDefinitions[0].entryPoint = ['/opt/flowcard/browser-gateway/deploytool'];
  adminCheck.containerDefinitions[0].command = ['check-admin'];
  const runtime = baseTask(runtimeTemplate, contract.runtime_family, f);
  runtime.taskRoleArn = f.runtime_task_role_arn;
  runtime.containerDefinitions = contract.services.filter(s => !s.worker || options.workers === true).map(s => {
    const env = Object.entries(s.environment).map(([name, raw]) => ({ name, value: raw.replace(/\$\{([a-z_]+)\}/g, (_, key) => {
      if (!Object.hasOwn(substitutions, key)) throw Error('Unknown runtime substitution');
      return substitutions[key];
    }) }));
    const c = { name: s.name, image, essential: true, user: '10001:10001', stopTimeout: 30,
      entryPoint: [`/opt/flowcard/${s.module}/${s.command}`], environment: env,
      secrets: Object.entries(s.secrets).map(([name, ref]) => ({ name, valueFrom: secret(f, ref) })),
      logConfiguration: log('/ecs/flowcard-node-gateway', s.name) };
    if (s.port) {
      c.portMappings = [{ containerPort: s.port, hostPort: s.port, protocol: 'tcp' }];
      c.healthCheck = { command: ['CMD', '/opt/flowcard/browser-gateway/deploytool', 'health', `http://127.0.0.1:${s.port}${s.health}`],
        interval: 15, timeout: 5, retries: 3, startPeriod: 30 };
    }
    if (s.name === 'node-gateway') c.dependsOn = contract.services.filter(x => x.port && x.name !== s.name).map(x => ({ containerName: x.name, condition: 'HEALTHY' }));
    if (s.worker) c.dependsOn = [{ containerName: s.name.replace('-worker', '-api'), condition: 'HEALTHY' }];
    return c;
  });
  const body = JSON.stringify(runtime);
  if (body.includes(f.secret_arns.migrate) || body.includes('MIGRATION_DATABASE_URL') || body.includes('go_bootstrap_json')) throw Error('Admin credential leaked into runtime');
  return { runtime, preflight, adminCheck, migration };
}
