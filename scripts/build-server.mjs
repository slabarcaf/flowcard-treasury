import { existsSync, lstatSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = path.join(root, ".dev", "server");
// This directory contains compiler output only. Clean it so deleted financial
// modules cannot survive a rebuild as executable legacy JavaScript.
for (const directory of [path.dirname(output), output]) {
  if (existsSync(directory) && (lstatSync(directory).isSymbolicLink() || !lstatSync(directory).isDirectory())) {
    throw new Error("server build output must use local directories without symlinks");
  }
}
rmSync(output, { recursive: true, force: true });
const require = createRequire(import.meta.url);
const packageFile = require.resolve("typescript/package.json");
const metadata = JSON.parse(readFileSync(packageFile, "utf8"));
const compiler = path.resolve(path.dirname(packageFile), metadata.bin.tsc);
const result = spawnSync(process.execPath, [compiler, "-p", "server/tsconfig.json"], {
  cwd: root, stdio: "inherit",
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
