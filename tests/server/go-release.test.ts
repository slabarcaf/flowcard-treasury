import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { renderTasks, validateFoundation, validateManifest } from '../../infra/runtime/go-release-contract.mjs';
import { deployGoStack } from '../../infra/scripts/deploy-go-stack.mjs';

const contract = JSON.parse(await readFile(resolve(__dirname, '../../infra/runtime/go-stack.json'), 'utf8'));
const account = '123456789012', region = 'us-west-2', sha = 'a'.repeat(40);
const ecs = `arn:aws:ecs:${region}:${account}:`;
const elb = `arn:aws:elasticloadbalancing:${region}:${account}:`;
const serviceNames = ['flowcard-demo-node-gateway', 'flowcard-demo-go-api', 'flowcard-demo-go-worker'];
function foundation() {
  return { generation: 'go-stack-v1', go_stack_foundation_enabled: true, account_id: account, aws_region: region,
    ecs_cluster_name: 'flowcard-demo-cluster', ecs_cluster_arn: ecs + 'cluster/flowcard-demo-cluster',
    private_subnet_ids: ['subnet-1234abcd'], migration_task_security_group_id: 'sg-1234abcd',
    runtime_task_role_arn: `arn:aws:iam::${account}:role/FlowCardApiTaskRole`, documents_bucket_name: `flowcard-documents-${account}-${region}`, documents_kms_key_arn: `arn:aws:kms:${region}:${account}:key/12345678-1234-1234-1234-123456789012`,
    database_name: 'flowcard_treasury', database_host: 'fixture.us-west-2.rds.amazonaws.com',
    migration_task_definition_arn: ecs + 'task-definition/flowcard-demo-go-migration:1',
    ecr_repository_urls: Object.fromEntries(['flowcard-node-gateway', 'flowcard-migration'].map(n => [n, `${account}.dkr.ecr.${region}.amazonaws.com/${n}`])),
    gateway_target_group_arn: elb + 'targetgroup/flowcard-demo-node-tg/1234567890123456',
    native_target_groups: Object.fromEntries(contract.native_routes.map((r: { name: string }) => [r.name, elb + `targetgroup/flowcard-go-${r.name}/1234567890123456`])),
    native_rule_arns: Object.fromEntries(contract.native_routes.map((r: { name: string }) => [r.name, elb + `listener-rule/app/fixture/listener/${r.name}`])),
    allowed_origins: ['https://demo.example.test'], frontend_bucket_name: `flowcard-frontend-${account}-${region}`,
    cloudfront_distribution_id: 'E123ABC', secret_arns: Object.fromEntries(Object.entries({ app: 'flowcard/db/app', migrate: 'flowcard/db/migrate', service_auth: 'flowcard/service-auth', airwallex: 'flowcard/airwallex' }).map(([k, n]) => [k, `arn:aws:secretsmanager:${region}:${account}:secret:${n}-ABCDEF`])) };
}
function template(migration: boolean) {
  return { taskDefinition: { family: migration ? 'flowcard-demo-go-migration' : serviceNames[0], networkMode: 'awsvpc',
    requiresCompatibilities: ['FARGATE'], runtimePlatform: { cpuArchitecture: 'X86_64' },
    executionRoleArn: `arn:aws:iam::${account}:role/FlowCard${migration ? 'Migration' : 'Node'}ExecutionRole`,
    taskRoleArn: `arn:aws:iam::${account}:role/FlowCard${migration ? 'Migration' : 'Node'}TaskRole` } };
}
const digests = { runtime: 'sha256:' + 'b'.repeat(64), migration: 'sha256:' + 'c'.repeat(64) };
function manifest() {
  return { generation: 'go-stack-v1', repository: 'slabarcaf/flowcard-treasury', branch: 'develop',
    commit_sha: sha, platform: 'linux/amd64', frontend_entry: 'original-go-product', image_tag: sha + '-1-1',
    frontend_checksum_sha256: 'd'.repeat(64), archive: { file: 'go-stack.tar.gz', image_id: 'sha256:' + 'e'.repeat(64), sha256: 'f'.repeat(64) } };
}

describe('Go deployment target and artifact contracts', () => {
  it('rejects unprepared or foreign foundations and unsealed/wrong-source manifests', () => {
    expect(() => validateFoundation(foundation())).not.toThrow();
    for (const change of [
      { go_stack_foundation_enabled: false }, { database_host: 'untrusted.example' },
      { secret_arns: { ...foundation().secret_arns, app: 'arn:aws:secretsmanager:us-east-1:999999999999:secret:foreign-ABCDEF' } },
      { allowed_origins: ['http://public.example'] }, { native_target_groups: {} },
    ]) expect(() => validateFoundation({ ...foundation(), ...change })).toThrow();
    expect(() => validateManifest(manifest(), sha)).not.toThrow();
    for (const change of [{ branch: 'main' }, { platform: 'linux/arm64' }, { commit_sha: 'b'.repeat(40) },
      { archive: { ...manifest().archive, file: '../private.tar.gz' } }, { frontend_entry: 'legacy-demo' }]) {
      expect(() => validateManifest({ ...manifest(), ...change }, sha)).toThrow();
    }
  });

  it('renders real API commands and optional workers without exposing admin credentials', () => {
    const f = foundation();
    const result = renderTasks(f, contract, template(false), template(true), digests);
    expect(result.runtime.containerDefinitions).toHaveLength(6);
    expect(result.runtime.containerDefinitions.find((c: { name: string }) => c.name === 'node-gateway').entryPoint).toEqual(['/opt/flowcard/browser-gateway/api']);
    expect(JSON.stringify(result.runtime)).not.toContain(f.secret_arns.migrate);
    expect(JSON.stringify(result.runtime)).not.toContain('MIGRATION_DATABASE_URL');
    const onboardingKey = f.secret_arns.app + ':go_onboarding_service_secret::';
    expect(result.runtime.containerDefinitions.find((x: any) => x.name === 'node-gateway').secrets).toContainEqual({ name: 'BROWSER_GATEWAY_ONBOARDING_SECRET', valueFrom: onboardingKey });
    expect(result.runtime.containerDefinitions.find((x: any) => x.name === 'onboarding-api').secrets).toContainEqual({ name: 'ONBOARDING_SERVICE_SECRET', valueFrom: onboardingKey });
    expect(JSON.stringify(result.runtime)).not.toContain(f.secret_arns.service_auth);
    expect(result.preflight.containerDefinitions[0].secrets).toContainEqual({ name: 'ONBOARDING_SERVICE_SECRET', valueFrom: onboardingKey });
    expect(JSON.stringify(result.preflight)).not.toContain(f.secret_arns.migrate);
    expect(JSON.stringify(result.migration)).toContain(f.secret_arns.migrate);
    expect(result.migration.containerDefinitions[0].entryPoint).toEqual(['/opt/flowcard/migrate-go-stack.sh']);
    expect(renderTasks(f, contract, template(false), template(true), digests, { workers: true }).runtime.containerDefinitions).toHaveLength(9);
    expect(() => renderTasks(f, contract, template(false), template(true), { ...digests, runtime: 'latest' })).toThrow();
    expect(() => renderTasks(f, contract, { taskDefinition: { ...template(false).taskDefinition, taskRoleArn: `arn:aws:iam::${account}:role/FlowCardAdministrator` } }, template(true), digests)).toThrow();
  });
});

async function fixture(fault?: 'migration' | 'health' | 'warming') {
  const f = foundation(), output = await mkdtemp(join(tmpdir(), 'flowcard-go-release-'));
  const calls: string[][] = [], definitions = new Map<string, any>(), oneOffs = new Map<string, any>();
  let revision = 10, healthReads = 0;
  const services = serviceNames.map(name => ({ serviceName: name, clusterArn: f.ecs_cluster_arn,
    taskDefinition: ecs + 'task-definition/' + name + ':1', desiredCount: 1, runningCount: 1, pendingCount: 0, loadBalancers: [] as unknown[] }));
  const value = (a: string[], key: string) => a[a.indexOf(key) + 1];
  const ops = { sleep: async (ms: number) => { calls.push(['sleep', String(ms)]); }, frontend: async () => { calls.push(['frontend']); }, verifyPublic: async () => { calls.push(['verify-public']); },
    aws: async (a: string[]) => {
      calls.push(a);
      const key = a.slice(0, 2).join(' ');
      if (key === 'sts get-caller-identity') return { Account: account, Arn: `arn:aws:sts::${account}:assumed-role/FlowCardGitHubDeployer/test` };
      if (key === 'ecs describe-services') return { services: structuredClone(services), failures: [] };
      if (key === 'ecs describe-task-definition') return template(value(a, '--task-definition').includes('go-migration'));
      if (key === 'ecs register-task-definition') {
        const d = JSON.parse(await readFile(value(a, '--cli-input-json').slice(7), 'utf8'));
        const arn = ecs + 'task-definition/' + d.family + ':' + (++revision);
        definitions.set(arn, d); return { taskDefinition: { taskDefinitionArn: arn } };
      }
      if (key === 'ecs run-task') {
        const taskArn = ecs + 'task/flowcard-demo-cluster/' + revision.toString(16).padStart(32, '0');
        const taskDefinitionArn = value(a, '--task-definition');
        const migration = definitions.get(taskDefinitionArn).containerDefinitions[0].entryPoint.includes('/opt/flowcard/migrate-go-stack.sh');
        oneOffs.set(taskArn, { taskArn, taskDefinitionArn, lastStatus: 'STOPPED', containers: [{ exitCode: fault === 'migration' && migration ? 1 : 0 }] });
        return { tasks: [{ taskArn }], failures: [] };
      }
      if (key === 'ecs wait') return {};
      if (key === 'ecs update-service') {
        const s = services.find(s => s.serviceName === value(a, '--service'))!;
        s.desiredCount = Number(value(a, '--desired-count')); s.runningCount = s.desiredCount;
        if (a.includes('--task-definition')) s.taskDefinition = value(a, '--task-definition');
        return { service: structuredClone(s) };
      }
      if (key === 'ecs list-tasks') return { taskArns: [ecs + 'task/flowcard-demo-cluster/' + 'f'.repeat(32)] };
      if (key === 'ecs describe-tasks') {
        const id = value(a, '--tasks');
        if (oneOffs.has(id)) return { tasks: [oneOffs.get(id)], failures: [] };
        const d = definitions.get(services[0].taskDefinition);
        return { tasks: [{ taskArn: id, taskDefinitionArn: services[0].taskDefinition, lastStatus: 'RUNNING',
          containers: d.containerDefinitions.map((c: any) => ({ name: c.name, lastStatus: 'RUNNING', healthStatus: 'HEALTHY' })) }], failures: [] };
      }
      if (key === 'elbv2 describe-target-health') {
        healthReads++;
        if (fault === 'warming' && healthReads <= contract.native_routes.length + 1) return { TargetHealthDescriptions: [{ TargetHealth: { State: 'initial' } }, { TargetHealth: { State: 'draining' } }] };
        return { TargetHealthDescriptions: [{ TargetHealth: { State: fault === 'health' ? 'unhealthy' : 'healthy' } }] };
      }
      if (key === 'elbv2 describe-rules') return { Rules: [{ Conditions: [{ Field: 'http-header', HttpHeaderConfig: { HttpHeaderName: 'x-flowcard-go-preview', Values: ['go-stack-v1'] } }] }] };
      if (key === 'elbv2 modify-rule') return {};
      throw Error('Unexpected fixture operation: ' + key);
    } };
  return { input: { foundation: f, manifest: manifest(), contract, digests, commit: sha, output, confirmApproval: true, confirmCallbackCutover: true }, ops, calls, output };
}

describe('Go rollout execution state machine', () => {
  it('waits for initial and draining ALB targets before routing or publication', async () => {
    const f = await fixture('warming');
    try {
      const state = await deployGoStack(f.input, f.ops);
      expect(state.status).toBe('go_runtime_deployed_and_verified');
      expect(f.calls).toContainEqual(['sleep', '5000']);
      const kinds = f.calls.map(c => c.slice(0, 2).join(' '));
      expect(kinds.filter(k => k === 'elbv2 describe-target-health').length).toBeGreaterThan(contract.native_routes.length + 1);
      expect(kinds.indexOf('elbv2 modify-rule')).toBeGreaterThan(kinds.lastIndexOf('elbv2 describe-target-health'));
      expect(kinds.indexOf('frontend')).toBeGreaterThan(kinds.lastIndexOf('elbv2 describe-target-health'));
    } finally { await rm(f.output, { recursive: true, force: true }); }
  });
  it('requires explicit release/callback approval before any AWS operation', async () => {
    const f = await fixture();
    try { await expect(deployGoStack({ ...f.input, confirmCallbackCutover: false }, f.ops)).rejects.toThrow('approval'); expect(f.calls).toHaveLength(0); }
    finally { await rm(f.output, { recursive: true, force: true }); }
  });

  it('preflights, quiesces, migrates, verifies and publishes in that order', async () => {
    const f = await fixture();
    try {
      const state = await deployGoStack(f.input, f.ops);
      expect(state.status).toBe('go_runtime_deployed_and_verified');
      expect(state.provider_acceptance_verified).toBe(false);
      expect(state.financial_execution_enabled).toBe(false);
      expect(state.completed).toEqual(['runtime-configuration', 'administrative-configuration', 'old-writers-stopped', 'go-migrations', 'go-runtime-healthy', 'frontend-and-public-boundaries-verified']);
      const kinds = f.calls.map(c => c.slice(0, 2).join(' '));
      expect(kinds.indexOf('elbv2 modify-rule')).toBeGreaterThan(kinds.lastIndexOf('elbv2 describe-target-health'));
      expect(kinds.indexOf('frontend')).toBeGreaterThan(kinds.lastIndexOf('elbv2 modify-rule'));
      expect(kinds.indexOf('verify-public')).toBeGreaterThan(kinds.indexOf('frontend'));
      const runtime = JSON.parse(await readFile(join(f.output, 'go-runtime-task.json'), 'utf8'));
      expect(runtime.containerDefinitions).toHaveLength(6);
      expect(state.previous_services).toHaveLength(3);
    } finally { await rm(f.output, { recursive: true, force: true }); }
  });

  it.each(['migration', 'health'] as const)('preserves recoverable state and blocks frontend/routing after %s failure', async fault => {
    const f = await fixture(fault);
    try {
      await expect(deployGoStack(f.input, f.ops)).rejects.toThrow('No automatic writer rollback');
      const state = JSON.parse(await readFile(join(f.output, 'go-deployed-release.json'), 'utf8'));
      expect(state.status).toBe('failed_manual_recovery_required');
      expect(state.previous_services).toHaveLength(3);
      expect(f.calls.some(c => c[0] === 'frontend' || (c[0] === 'elbv2' && c[1] === 'modify-rule'))).toBe(false);
      expect(f.calls.filter(c => c[0] === 'ecs' && c[1] === 'update-service' && c.includes('--desired-count') && c[c.indexOf('--desired-count') + 1] === '0')).toHaveLength(3);
    } finally { await rm(f.output, { recursive: true, force: true }); }
  });
});
