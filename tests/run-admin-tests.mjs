import { existsSync, lstatSync, readlinkSync, symlinkSync, unlinkSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const testModules = resolve(root, "tests/frontend/admin/node_modules");
const adminModules = resolve(root, "frontend/admin/node_modules");
let created = false;
try {
  if (!existsSync(adminModules)) throw new Error("Install frontend dependencies before running tests");
  try { lstatSync(testModules); } catch (error) {
    if (error.code !== "ENOENT") throw error;
    symlinkSync(adminModules, testModules, "dir");
    created = true;
  }
  const result = spawnSync("pnpm", ["exec", "vitest", "run", "--config", "vite.config.ts", ...process.argv.slice(2)], {
    cwd: resolve(root, "frontend/admin"), stdio: "inherit",
  });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
} finally {
  if (created && lstatSync(testModules).isSymbolicLink() && readlinkSync(testModules) === adminModules) unlinkSync(testModules);
}
