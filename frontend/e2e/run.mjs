import { spawn } from "node:child_process";
import { cpSync, mkdtempSync, rmSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const project = fileURLToPath(new URL("../../", import.meta.url));
const cli = fileURLToPath(import.meta.resolve("@playwright/test/cli"));
const root = mkdtempSync(join(tmpdir(), "ai-buddy-e2e-"));

function run(command, args, options) {
  return new Promise((done, reject) => {
    const child = spawn(command, args, { ...options, stdio: "inherit" });
    child.once("error", reject);
    child.once("close", (code) => done(code ?? 1));
  });
}

try {
  for (const directory of ["mocks", "skills", "teams"]) {
    cpSync(resolve(project, directory), join(root, directory), { recursive: true });
  }
  const fixture = join(root, "mocks", "pr-17.json");
  const pr = JSON.parse(readFileSync(fixture, "utf8"));
  pr.current_revision = "initial";
  writeFileSync(fixture, JSON.stringify(pr));

  const config = join(root, "teams", "demo-backend", "context.json");
  const team = JSON.parse(readFileSync(config, "utf8"));
  team.people_agent_url = "https://people.example.test/";
  writeFileSync(config, JSON.stringify(team));

  const binary = join(root, process.platform === "win32" ? "api.exe" : "api");
  const build = await run("go", ["build", "-o", binary, "./cmd/api"], {
    cwd: join(project, "backend"),
  });
  process.exitCode = build || await run(process.execPath, [cli, "test", ...process.argv.slice(2)], {
    env: { ...process.env, BUDDY_E2E_ROOT: root, BUDDY_E2E_API: binary },
  });
} finally {
  rmSync(root, { recursive: true, force: true, maxRetries: 6, retryDelay: 250 });
}
