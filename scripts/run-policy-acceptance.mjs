import { spawnSync } from "node:child_process";

const value = process.env.POLICY_ACCEPTANCE_DATABASE_URL?.trim();
if (!value) {
  process.stderr.write("POLICY_ACCEPTANCE_DATABASE_URL is required for PostgreSQL policy acceptance tests\n");
  process.exit(1);
}

const target = new URL(value);
if (!["localhost", "127.0.0.1", "::1"].includes(target.hostname) || !target.pathname.toLowerCase().includes("test")) {
  process.stderr.write("POLICY_ACCEPTANCE_DATABASE_URL must target a localhost database whose name includes 'test'\n");
  process.exit(1);
}

const vitest = new URL("../node_modules/vitest/vitest.mjs", import.meta.url);
const result = spawnSync(process.execPath, [vitest.pathname, "run", "tests/server/policy/acceptance.test.ts"], {
  stdio: "inherit",
  env: { ...process.env, POLICY_ACCEPTANCE_REQUIRED: "1" },
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
