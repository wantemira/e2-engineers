import { spawn } from "node:child_process";
import { join } from "node:path";

const { BUDDY_E2E_ROOT, BUDDY_E2E_API } = process.env;
if (!BUDDY_E2E_ROOT || !BUDDY_E2E_API) {
  throw new Error("Запустите npm run test:e2e");
}
const api = spawn(BUDDY_E2E_API, ["-root", BUDDY_E2E_ROOT], {
  env: { ...process.env, DATA_DIR: join(BUDDY_E2E_ROOT, "data") },
  stdio: "inherit",
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.once(signal, () => api.kill(signal));
}
api.once("exit", (code, signal) => { process.exitCode = signal ? 0 : (code ?? 1); });
