import fs from 'node:fs/promises';
import path from 'node:path';
import { createHash, randomBytes } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import pg from 'pg';
import { renderTasks } from '../runtime/go-release-contract.mjs';

const execute = promisify(execFile);
const args = process.argv.slice(2);
const option = name => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const image = option('--image'), postgresContainer = option('--postgres-container');
if (!image || !/^[a-zA-Z0-9_.:-]+$/.test(postgresContainer ?? '') || !process.env.GO_DEPLOY_TEST_ADMIN_URL_FILE) throw Error('Explicit image, existing test container and private admin file required');
const adminURL = new URL((await fs.readFile(process.env.GO_DEPLOY_TEST_ADMIN_URL_FILE, 'utf8')).trim());
if (!['127.0.0.1', 'localhost'].includes(adminURL.hostname) || adminURL.port !== '54329' || adminURL.pathname !== '/postgres') throw Error('Only existing loopback54329 maintenance is allowed');
const marker = 'go-deploy-smoke-' + randomBytes(8).toString('hex');
const databaseName = 'flowcard_deploy_test_' + marker.slice(-16);
const directory = path.resolve('.dev/go-deploy-smoke', marker);
await fs.mkdir(directory, { recursive: true, mode: 0o700 });
const contract = JSON.parse(await fs.readFile('infra/runtime/go-stack.json', 'utf8'));
const account = '123456789012', region = 'us-west-2';
const prefix = `arn:aws:elasticloadbalancing:${region}:${account}:`;
const fake = { generation: 'go-stack-v1', go_stack_foundation_enabled: true, account_id: account, aws_region: region,
  ecs_cluster_name: 'flowcard-demo-cluster', ecs_cluster_arn: `arn:aws:ecs:${region}:${account}:cluster/flowcard-demo-cluster`,
  private_subnet_ids: ['subnet-1234abcd'], migration_task_security_group_id: 'sg-1234abcd',
  runtime_task_role_arn: `arn:aws:iam::${account}:role/FlowCardApiTaskRole`, documents_bucket_name: `flowcard-documents-${account}-${region}`, documents_kms_key_arn: `arn:aws:kms:${region}:${account}:key/12345678-1234-1234-1234-123456789012`,
    database_name: databaseName, database_host: 'fixture.us-west-2.rds.amazonaws.com',
  migration_task_definition_arn: `arn:aws:ecs:${region}:${account}:task-definition/flowcard-demo-go-migration:1`,
  ecr_repository_urls: Object.fromEntries(['flowcard-node-gateway', 'flowcard-migration'].map(n => [n, `${account}.dkr.ecr.${region}.amazonaws.com/${n}`])),
  native_target_groups: Object.fromEntries(contract.native_routes.map(r => [r.name, prefix + `targetgroup/flowcard-go-${r.name}/1234567890123456`])),
  native_rule_arns: Object.fromEntries(contract.native_routes.map(r => [r.name, prefix + `listener-rule/app/fixture/1234567890123456/1234567890123456/${r.port}`])),
  gateway_target_group_arn: prefix + 'targetgroup/flowcard-demo-node-tg/1234567890123456',
  frontend_bucket_name: `flowcard-frontend-${account}-${region}`, cloudfront_distribution_id: 'E123ABC', allowed_origins: ['https://demo.example.test'],
  secret_arns: Object.fromEntries(Object.entries({ app: 'flowcard/db/app', migrate: 'flowcard/db/migrate', service_auth: 'flowcard/service-auth', airwallex: 'flowcard/airwallex' }).map(([k, n]) => [k, `arn:aws:secretsmanager:${region}:${account}:secret:${n}-ABCDEF`])) };
const template = migration => ({ taskDefinition: { family: migration ? 'flowcard-demo-go-migration' : 'flowcard-demo-node-gateway',
  networkMode: 'awsvpc', requiresCompatibilities: ['FARGATE'], runtimePlatform: { cpuArchitecture: 'X86_64' },
  executionRoleArn: `arn:aws:iam::${account}:role/FlowCard${migration ? 'Migration' : 'Node'}ExecutionRole`,
  taskRoleArn: `arn:aws:iam::${account}:role/FlowCard${migration ? 'Migration' : 'Node'}TaskRole` } });
const tasks = renderTasks(fake, contract, template(false), template(true), { runtime: 'sha256:' + 'a'.repeat(64), migration: 'sha256:' + 'b'.repeat(64) });
const values = {};
const roles = [];
for (const b of contract.database_bindings) {
  const user = 'flowcard_go_' + marker.slice(-8) + '_' + b.name;
  const url = new URL(adminURL);
  url.hostname = '127.0.0.1'; url.port = '5432'; url.pathname = '/' + databaseName;
  url.username = user; url.password = randomBytes(24).toString('hex'); url.searchParams.delete('dbname'); url.searchParams.set('sslmode', 'disable'); url.searchParams.delete('sslrootcert');
  values['app:' + b.key] = url.toString(); roles.push({ name: user, comment: `flowcard-go-runtime:${databaseName}:${b.name}` });
}
for (const [kind, fields] of Object.entries(contract.secret_fields)) {
  for (const field of fields) if (!values[kind + ':' + field]) values[kind + ':' + field] = createHash('sha256').update(marker + ':' + kind + ':' + field).digest('hex');
}
values.service_auth = createHash('sha256').update(marker + ':service_auth').digest('hex');
const internalAdmin = new URL(adminURL);
internalAdmin.hostname = '127.0.0.1'; internalAdmin.port = '5432'; internalAdmin.pathname = '/' + databaseName;
internalAdmin.searchParams.delete('dbname'); internalAdmin.searchParams.set('sslmode', 'disable'); internalAdmin.searchParams.delete('sslrootcert');
values['migrate:go_stack_url'] = internalAdmin.toString();
values['migrate:go_bootstrap_json'] = JSON.stringify({ operator_username: 'deployment-smoke-operator', operator_password: 'synthetic-deployment-smoke-password-12345' });
const containers = [];
const admin = new pg.Client({ connectionString: adminURL.toString() });
let database;
let stage = 'setup';
const report = { owner: marker, database: databaseName, image, stages: [], all_pass: false, shared_database_modified: false };
async function envFile(definition, name) {
  const env = Object.fromEntries(definition.environment.map(v => [v.name, v.value]));
  env.EXPECTED_DATABASE_HOST = '127.0.0.1';
  // Synthetic credentials initialize the real S3 adapter; no storage call is made.
  env.AWS_ACCESS_KEY_ID = 'synthetic-smoke-access';
  env.AWS_SECRET_ACCESS_KEY = 'synthetic-smoke-secret';
  for (const item of definition.secrets) {
    const pair = Object.entries(fake.secret_arns).find(([, arn]) => item.valueFrom === arn || item.valueFrom.startsWith(arn + ':'));
    if (!pair) throw Error('Unknown fixture secret reference');
    const [kind, arn] = pair, key = item.valueFrom === arn ? '' : item.valueFrom.slice(arn.length + 1).split(':')[0];
    const value = values[kind + (key ? ':' + key : '')];
    if (!value || value.includes('\n')) throw Error('Fixture secret missing');
    env[item.name] = value;
  }
  if (name === 'probe') Object.assign(env, { GO_BOOTSTRAP_JSON: values['migrate:go_bootstrap_json'], EXPECTED_DATABASE_NAME: databaseName });
  const file = path.join(directory, name + '.env');
  await fs.writeFile(file, Object.entries(env).map(([k, v]) => k + '=' + v).join('\n') + '\n', { mode: 0o600 });
  return file;
}
async function launch(definition, name, detached = false) {
  const file = await envFile(definition, name);
  const id = marker + '-' + name;
  const command = ['run', '--platform', 'linux/amd64', '--name', id, '--label', 'com.flowcard.owner=' + marker,
    '--network', 'container:' + postgresContainer, '--env-file', file, ...(detached ? ['--detach'] : []),
    '--entrypoint', definition.entryPoint[0], image, ...definition.entryPoint.slice(1), ...(definition.command ?? [])];
  containers.push(id);
  const result = await execute('docker', command, { timeout: 180000, maxBuffer: 1024 * 1024 });
  await fs.writeFile(path.join(directory, name + '.log'), result.stdout + result.stderr, { mode: 0o600 });
}
try {
  const pgInfo = JSON.parse((await execute('docker', ['inspect', postgresContainer])).stdout)[0];
  if (!pgInfo?.State?.Running || !pgInfo.Config?.Image?.startsWith('postgres:')) throw Error('Existing test PostgreSQL container required');
  await admin.connect();
  await admin.query('CREATE DATABASE "' + databaseName + '"');
  await admin.query('COMMENT ON DATABASE "' + databaseName + '" IS \'' + marker + '\'');
  const hostURL = new URL(adminURL); hostURL.pathname = '/' + databaseName; hostURL.searchParams.delete('dbname');
  database = new pg.Client({ connectionString: hostURL.toString() }); await database.connect();
  await database.query('REVOKE CREATE,TEMPORARY ON DATABASE "' + databaseName + '" FROM PUBLIC');
  await database.query(`REVOKE CREATE ON SCHEMA public FROM PUBLIC;
    CREATE TABLE public.customer_roster(customer_id text PRIMARY KEY,airwallex_connected_account_id text NOT NULL,is_active boolean NOT NULL);
    CREATE TABLE public.issuing_card_bindings(card_id text PRIMARY KEY,customer_id text NOT NULL,airwallex_account_id text NOT NULL,is_active boolean NOT NULL);`);
  stage = 'runtime-preflight'; await launch(tasks.preflight.containerDefinitions[0], 'runtime-preflight'); report.stages.push(stage);
  stage = 'admin-preflight'; await launch(tasks.adminCheck.containerDefinitions[0], 'admin-preflight'); report.stages.push(stage);
  stage = 'migrations-and-provisioning'; await launch(tasks.migration.containerDefinitions[0], 'migrations'); report.stages.push(stage);
  stage = 'idempotent-migration-replay'; await launch(tasks.migration.containerDefinitions[0], 'migration-replay'); report.stages.push(stage);
  stage = 'start-actual-apis';
  for (const definition of tasks.runtime.containerDefinitions) await launch(definition, definition.name, true);
  stage = 'api-health';
  for (const definition of tasks.runtime.containerDefinitions) {
    let healthy = false;
    for (let attempt = 0; attempt < 15; attempt++) {
      try { await execute('docker', ['exec', marker + '-' + definition.name, ...definition.healthCheck.command.slice(1)], { timeout: 5000 }); healthy = true; break; }
      catch { await new Promise(resolve => setTimeout(resolve, 1000)); }
    }
    if (!healthy) throw Error('API health failed');
  }
  report.stages.push('six-real-api-health-checks');
  stage = 'real-login-and-go-onboarding-read';
  const definition = { ...tasks.preflight.containerDefinitions[0], command: ['probe-runtime'] };
  await launch(definition, 'probe'); report.stages.push(stage);
  report.all_pass = true;
} catch {
  report.failure_stage = stage;
  process.exitCode = 1;
} finally {
  for (const id of containers.reverse()) {
    try {
      const raw = await execute('docker', ['inspect', id]); const info = JSON.parse(raw.stdout)[0];
      if (info?.Config?.Labels?.['com.flowcard.owner'] === marker) {
        try { const logs = await execute('docker', ['logs', id]); await fs.writeFile(path.join(directory, id + '.log'), logs.stdout + logs.stderr, { mode: 0o600 }); } catch {}
        await execute('docker', ['rm', '--force', id]);
      }
    } catch {}
  }
  await database?.end();
  try {
    const marked = await admin.query("SELECT shobj_description(oid,'pg_database') marker FROM pg_database WHERE datname=$1", [databaseName]);
    if (marked.rows[0]?.marker === marker) await admin.query('DROP DATABASE "' + databaseName + '" WITH (FORCE)');
    for (const role of roles) {
      const row = (await admin.query("SELECT shobj_description(oid,'pg_authid') marker FROM pg_roles WHERE rolname=$1", [role.name])).rows[0];
      if (row?.marker === role.comment) await admin.query('DROP ROLE "' + role.name + '"');
    }
    report.database_cleanup_verified = (await admin.query('SELECT count(*)::int n FROM pg_database WHERE datname=$1', [databaseName])).rows[0].n === 0;
    report.login_cleanup_verified = (await admin.query('SELECT count(*)::int n FROM pg_roles WHERE rolname=ANY($1::text[])', [roles.map(r => r.name)])).rows[0].n === 0;
  } catch { report.cleanup_unverified = true; process.exitCode = 1; }
  await admin.end();
  await fs.writeFile(path.join(directory, 'report.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
}
console.log(JSON.stringify({ ...report, report: path.join(directory, 'report.json') }));
