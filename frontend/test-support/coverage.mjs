import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import coverage from "istanbul-lib-coverage";
import report from "istanbul-lib-report";
import reports from "istanbul-reports";
import { createInstrumenter } from "istanbul-lib-instrument";

const dir = resolve("coverage");
const raw = join(dir, "raw");
rmSync(raw, { recursive: true, force: true });
mkdirSync(raw, { recursive: true });
const env = { ...process.env, BUDDY_TEST_COVERAGE: "1" };
for (const args of [
  ["node_modules/vitest/vitest.mjs", "run", "--config", "test-support/vite.config.mjs"],
  ["e2e/run.mjs"],
]) {
  const result = spawnSync(process.execPath, args, { env, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

const map = coverage.createCoverageMap();
const instrumenter = createInstrumenter({ esModules: true, parserPlugins: ["typescript", "jsx"] });
for (const name of readdirSync("src", { recursive: true })) {
  if (!/\.tsx?$/.test(name) || /\.test\.tsx?$/.test(name)) continue;
  const source = resolve("src", name).replaceAll("\\", "/");
  instrumenter.instrumentSync(readFileSync(source, "utf8"), source);
  map.addFileCoverage(instrumenter.lastFileCoverage());
}
for (const name of readdirSync(raw)) {
  map.merge(JSON.parse(readFileSync(join(raw, name), "utf8")));
}

const context = report.createContext({ dir, coverageMap: map });
for (const kind of ["text", "text-summary", "html", "lcovonly"]) {
  reports.create(kind).execute(context);
}
const { statements } = map.getCoverageSummary();
if (statements.covered * 100 <= 80 * statements.total) {
  console.error("Покрытие операторов фронтенда должно превышать 80%.");
  process.exitCode = 1;
}
