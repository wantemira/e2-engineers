import { afterAll } from "vitest";
import { randomUUID } from "node:crypto";
import { writeFileSync } from "node:fs";
import { join } from "node:path";

afterAll(() => {
  writeFileSync(
    join("coverage", "raw", `unit-${randomUUID()}.json`),
    JSON.stringify(globalThis.__coverage__ || {}),
  );
});
