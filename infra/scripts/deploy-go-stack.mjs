import fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { pathToFileURL } from 'node:url';
import { repository, renderTasks, validateFoundation, validateManifest, verifyArchive } from '../runtime/go-release-contract.mjs';

const execute = promisify(execFile);
const names = ['flowcard-demo-node-gateway', 'flowcard-demo-go-api', 'flowcard-demo-go-worker'];

function services(response, f) {
  if (response.failures?.length || response.services?.length !== 3 || names.some(n => !response.services.some(s => s.serviceName === n))) throw Error('Expected three existing deployment slots');
  for (const s of response.services) if (s.clusterArn !== f.ecs_cluster_arn || !s.taskDefinition?.startsWith(`arn:aws:ecs:${f.aws_region}:${f.account_id}:task-definition/${s.serviceName}:`)) throw Error('Existing service binding mismatch');
  return response.services;
}

// The injected operations are a fixture seam for the rollout state machine.
// Production uses only execFile argument arrays and the ordinary AWS CLI.
export async function deployGoStack(input, ops) {
  const { foundation: f, manifest: m, contract, digests, output, workers = false } = input;
  validateFoundation(f);
  validateManifest(m, input.commit);
  if (!input.confirmApproval || !input.confirmCallbackCutover) throw Error('Explicit release and callback-cutover approval required');
  const state = { generation: 'go-stack-v1', repository, commit_sha: input.commit, image_tag: m.image_tag,
    account_id: f.account_id, region: f.aws_region, status: 'preflight', phase: 'identity',
    provider_acceptance_verified: false, financial_execution_enabled: false, workers_enabled: workers,
    task_definitions: {}, one_off_tasks: {}, previous_services: [], completed: [] };
  await fs.mkdir(output, { recursive: true });
  const save = async () => fs.writeFile(path.join(output, 'go-deployed-release.json'), JSON.stringify(state, null, 2) + '\n', { mode: 0o600 });
  const phase = async value => { state.phase = value; await save(); };
  const aws = args => ops.aws([...args, '--region', f.aws_region, '--output', 'json']);
  const register = async (name, definition) => {
    const file = path.resolve(output, name + '-task.json');
    await fs.writeFile(file, JSON.stringify(definition, null, 2) + '\n', { mode: 0o600 });
    const result = await aws(['ecs', 'register-task-definition', '--cli-input-json', 'file://' + file]);
    const value = result.taskDefinition?.taskDefinitionArn;
    if (!value?.startsWith(`arn:aws:ecs:${f.aws_region}:${f.account_id}:task-definition/${definition.family}:`)) throw Error('Registered task identity invalid');
    state.task_definitions[name] = value; await save(); return value;
  };
  const oneOff = async (name, definition) => {
    await phase(name);
    const taskDefinition = await register(name, definition);
    const token = createHash('sha256').update(m.image_tag + ':' + name).digest('hex');
    state.one_off_tasks[name] = { client_token: token, task_definition: taskDefinition }; await save();
    const network = { awsvpcConfiguration: { subnets: f.private_subnet_ids, securityGroups: [f.migration_task_security_group_id], assignPublicIp: 'DISABLED' } };
    const result = await aws(['ecs', 'run-task', '--cluster', f.ecs_cluster_arn, '--task-definition', taskDefinition,
      '--launch-type', 'FARGATE', '--client-token', token, '--network-configuration', JSON.stringify(network)]);
    const task = result.tasks?.[0]?.taskArn;
    if (result.failures?.length || result.tasks?.length !== 1 || !task?.startsWith(`arn:aws:ecs:${f.aws_region}:${f.account_id}:task/${f.ecs_cluster_name}/`)) throw Error('One-off task launch unverified');
    state.one_off_tasks[name].task_arn = task; await save();
    await aws(['ecs', 'wait', 'tasks-stopped', '--cluster', f.ecs_cluster_arn, '--tasks', task]);
    const stopped = await aws(['ecs', 'describe-tasks', '--cluster', f.ecs_cluster_arn, '--tasks', task]);
    const t = stopped.tasks?.[0];
    if (stopped.failures?.length || t?.taskArn !== task || t.taskDefinitionArn !== taskDefinition || t.lastStatus !== 'STOPPED' ||
        t.containers?.length !== 1 || t.containers[0].exitCode !== 0) throw Error('One-off task did not exit successfully');
    state.completed.push(name); await save();
  };
  try {
    const identity = await aws(['sts', 'get-caller-identity']);
    if (identity.Account !== f.account_id || !identity.Arn?.includes(':assumed-role/FlowCardGitHubDeployer/')) throw Error('Wrong AWS account or deployment role');
    const current = services(await aws(['ecs', 'describe-services', '--cluster', f.ecs_cluster_arn, '--services', ...names]), f);
    state.previous_services = current.map(s => ({ service_name: s.serviceName, task_definition: s.taskDefinition,
      desired_count: s.desiredCount, load_balancers: s.loadBalancers }));
    await save();
    const runtimeTemplate = await aws(['ecs', 'describe-task-definition', '--task-definition', current.find(s => s.serviceName === names[0]).taskDefinition]);
    const migrationTemplate = await aws(['ecs', 'describe-task-definition', '--task-definition', f.migration_task_definition_arn]);
    const tasks = renderTasks(f, contract, runtimeTemplate, migrationTemplate, digests, { workers });
    await oneOff('runtime-configuration', tasks.preflight);
    await oneOff('administrative-configuration', tasks.adminCheck);
    await phase('quiesce-old-writers');
    for (const name of names) await aws(['ecs', 'update-service', '--cluster', f.ecs_cluster_arn, '--service', name, '--desired-count', '0']);
    let stopped = false;
    for (let attempt = 0; attempt < 60; attempt++) {
      const values = services(await aws(['ecs', 'describe-services', '--cluster', f.ecs_cluster_arn, '--services', ...names]), f);
      if (values.every(s => s.desiredCount === 0 && s.runningCount === 0 && s.pendingCount === 0)) { stopped = true; break; }
      await ops.sleep(5000);
    }
    if (!stopped) throw Error('Old writers did not quiesce');
    state.completed.push('old-writers-stopped'); await save();
    await oneOff('go-migrations', tasks.migration);
    await phase('update-go-runtime');
    const runtimeArn = await register('go-runtime', tasks.runtime);
    const loadBalancers = [{ targetGroupArn: f.gateway_target_group_arn, containerName: 'node-gateway', containerPort: 3000 },
      ...contract.native_routes.map(r => ({ targetGroupArn: f.native_target_groups[r.name], containerName: r.container, containerPort: r.port }))];
    await aws(['ecs', 'update-service', '--cluster', f.ecs_cluster_arn, '--service', names[0], '--task-definition', runtimeArn,
      '--desired-count', '1', '--load-balancers', JSON.stringify(loadBalancers),
      '--deployment-configuration', JSON.stringify({ deploymentCircuitBreaker: { enable: true, rollback: false } })]);
    await aws(['ecs', 'wait', 'services-stable', '--cluster', f.ecs_cluster_arn, '--services', names[0]]);
    const values = services(await aws(['ecs', 'describe-services', '--cluster', f.ecs_cluster_arn, '--services', ...names]), f);
    if (!values.every(s => s.serviceName === names[0] ? s.taskDefinition === runtimeArn && s.runningCount === 1 && s.pendingCount === 0 && s.desiredCount === 1 : s.runningCount === 0 && s.pendingCount === 0 && s.desiredCount === 0)) throw Error('Actual service rollout mismatch');
    const listed = await aws(['ecs', 'list-tasks', '--cluster', f.ecs_cluster_arn, '--service-name', names[0], '--desired-status', 'RUNNING']);
    if (listed.taskArns?.length !== 1) throw Error('Expected one runtime task');
    const actual = await aws(['ecs', 'describe-tasks', '--cluster', f.ecs_cluster_arn, '--tasks', listed.taskArns[0]]);
    const t = actual.tasks?.[0];
    if (actual.failures?.length || t?.taskDefinitionArn !== runtimeArn || t.lastStatus !== 'RUNNING' || t.containers?.length !== tasks.runtime.containerDefinitions.length) throw Error('Runtime task unverified');
    for (const c of tasks.runtime.containerDefinitions) {
      const observed = t.containers.find(x => x.name === c.name);
      if (!observed || observed.lastStatus !== 'RUNNING' || (c.healthCheck && observed.healthStatus !== 'HEALTHY') || observed.exitCode !== undefined) throw Error('Go container unhealthy');
    }
    for (const target of loadBalancers) {
      const health = await aws(['elbv2', 'describe-target-health', '--target-group-arn', target.targetGroupArn]);
      if (health.TargetHealthDescriptions?.length !== 1 || health.TargetHealthDescriptions[0].TargetHealth?.State !== 'healthy') throw Error('Go target group unhealthy');
    }
    state.completed.push('go-runtime-healthy'); await save();
    await phase('native-route-cutover');
    for (const r of contract.native_routes) {
      const prior = await aws(['elbv2', 'describe-rules', '--rule-arns', f.native_rule_arns[r.name]]);
      state.previous_routes ??= {}; state.previous_routes[r.name] = prior.Rules?.[0]?.Conditions;
      if (!state.previous_routes[r.name]) throw Error('Previous routing unverified');
      await save();
      await aws(['elbv2', 'modify-rule', '--rule-arn', f.native_rule_arns[r.name], '--conditions',
        JSON.stringify([{ Field: 'path-pattern', PathPatternConfig: { Values: [r.path] } }])]);
    }
    await phase('frontend-publication');
    await ops.frontend(f, m);
    await phase('public-boundaries');
    await ops.verifyPublic(f);
    state.completed.push('frontend-and-public-boundaries-verified');
    state.status = 'go_runtime_deployed_and_verified'; state.phase = 'complete';
    await save(); return state;
  } catch {
    state.status = 'failed_manual_recovery_required';
    await save();
    throw Error(`Go release stopped at ${state.phase}; inspect retained recovery metadata. No automatic writer rollback was performed.`);
  }
}

async function main() {
  const args = process.argv.slice(2);
  const option = name => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
  if (!args.includes('--confirm-approval') || !args.includes('--confirm-callback-cutover') || !args.includes('--oidc') ||
      process.env.GITHUB_ACTIONS !== 'true' || process.env.GITHUB_REPOSITORY !== repository ||
      process.env.GITHUB_REPOSITORY_ID !== '1427370701' || process.env.GITHUB_REF !== 'refs/heads/develop' ||
      process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || !process.env.AWS_SESSION_TOKEN || process.env.AWS_PROFILE) throw Error('Explicit develop-only manual OIDC deployment required');
  const f = validateFoundation(JSON.parse(await fs.readFile(option('--foundation'), 'utf8')));
  const m = validateManifest(JSON.parse(await fs.readFile(option('--manifest'), 'utf8')), process.env.GITHUB_SHA);
  const git = await execute('git', ['rev-parse', 'HEAD']);
  if (git.stdout.trim() !== m.commit_sha) throw Error('Checkout and artifact differ');
  const archive = path.resolve(option('--archives'), m.archive.file);
  await verifyArchive(archive, m.archive.sha256);
  const frontend = await execute('bash', ['infra/scripts/compute-frontend-hash.sh', '.dev/dist-product']);
  if (frontend.stdout.trim() !== m.frontend_checksum_sha256) throw Error('Frontend artifact checksum mismatch');
  const aws = async args => {
    try { const result = await execute('aws', args, { maxBuffer: 16 * 1024 * 1024 }); return result.stdout.trim() ? JSON.parse(result.stdout) : {}; }
    catch { throw Error('AWS operation failed'); }
  };
  const identity = await aws(['sts', 'get-caller-identity', '--region', f.aws_region, '--output', 'json']);
  if (identity.Account !== f.account_id || !identity.Arn?.includes(':assumed-role/FlowCardGitHubDeployer/')) throw Error('Wrong AWS identity');
  await execute('docker', ['load', '--input', archive]);
  const image = 'flowcard-go-stack:' + m.image_tag;
  const inspected = await execute('docker', ['inspect', '--format', '{{.Id}} {{.Os}}/{{.Architecture}}', image]);
  if (inspected.stdout.trim() !== `${m.archive.image_id} linux/amd64`) throw Error('Loaded image or platform mismatch');
  const sourceLabel = await execute('docker', ['inspect', '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}', image]);
  if (sourceLabel.stdout.trim() !== m.commit_sha) throw Error('Container source revision mismatch');
  const registry = f.ecr_repository_urls['flowcard-node-gateway'].split('/')[0];
  const login = await execute('aws', ['ecr', 'get-login-password', '--region', f.aws_region]);
  await new Promise((resolve, reject) => {
    import('node:child_process').then(({ spawn }) => {
      const child = spawn('docker', ['login', '--username', 'AWS', '--password-stdin', registry], { stdio: ['pipe', 'ignore', 'ignore'] });
      child.stdin.end(login.stdout); child.on('error', reject); child.on('exit', code => code === 0 ? resolve() : reject(Error('Registry authentication unavailable')));
    });
  });
  const digests = {};
  for (const [key, name] of [['runtime', 'flowcard-node-gateway'], ['migration', 'flowcard-migration']]) {
    const target = `${f.ecr_repository_urls[name]}:${m.image_tag}`;
    await execute('docker', ['tag', image, target]); await execute('docker', ['push', target]);
    const result = await aws(['ecr', 'describe-images', '--repository-name', name, '--image-ids', 'imageTag=' + m.image_tag, '--region', f.aws_region, '--output', 'json']);
    if (result.imageDetails?.length !== 1 || !/^sha256:[a-f0-9]{64}$/.test(result.imageDetails[0].imageDigest ?? '')) throw Error('Published digest unavailable');
    digests[key] = result.imageDetails[0].imageDigest;
  }
  const contract = JSON.parse(await fs.readFile('infra/runtime/go-stack.json', 'utf8'));
  await deployGoStack({ foundation: f, manifest: m, contract, digests, commit: m.commit_sha,
    confirmApproval: true, confirmCallbackCutover: true, workers: args.includes('--workers'), output: option('--output') }, {
    aws, sleep: ms => new Promise(resolve => setTimeout(resolve, ms)),
    frontend: async (foundation, manifest) => execute('bash', ['infra/scripts/deploy-frontend.sh', '--oidc', '--confirm-approval',
      '--account-id', foundation.account_id, '--region', foundation.aws_region, '--bucket', foundation.frontend_bucket_name,
      '--distribution-id', foundation.cloudfront_distribution_id, '--build-dir', '.dev/dist-product', '--expected-checksum', manifest.frontend_checksum_sha256]),
    verifyPublic: async foundation => {
      const origin = foundation.allowed_origins.find(x => new URL(x).hostname.endsWith('.cloudfront.net'));
      if (!origin) throw Error('Public origin missing');
      const me = await fetch(origin + '/api/session/me', { headers: { Origin: origin }, redirect: 'error', signal: AbortSignal.timeout(15000) });
      if (me.status !== 401 || (await me.json()).error !== 'unauthorized') throw Error('Go browser authentication boundary unverified');
      for (const route of contract.native_routes) {
        const response = await fetch(origin + route.path, { method: 'POST', headers: { 'Content-Type': 'application/json',
          'X-Timestamp': String(Date.now()), 'X-Signature': '0'.repeat(64) }, body: '{}', redirect: 'error', signal: AbortSignal.timeout(15000) });
        if (response.status !== 401) throw Error('Native webhook authentication boundary unverified');
      }
    },
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch(() => { console.error('Go deployment failed; inspect recovery metadata and the documented prerequisites'); process.exitCode = 1; });
}
