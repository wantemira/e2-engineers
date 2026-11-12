import { defineConfig } from "@playwright/test";

const origin = "http://127.0.0.1:4173";
const api = "http://127.0.0.1:18080";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.pw.ts",
  workers: 1,
  timeout: 45_000,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: origin,
    browserName: "chromium",
    channel: process.env.BUDDY_BROWSER_CHANNEL || undefined,
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "node e2e/start-api.mjs",
      url: api + "/healthz",
      timeout: 30_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5_000 },
      env: {
        HTTP_ADDR: new URL(api).host,
        FRONTEND_ORIGIN: origin,
      },
    },
    {
      command: "node node_modules/vite/bin/vite.js --config test-support/vite.config.mjs --host 127.0.0.1 --port 4173",
      url: origin,
      env: { API_PROXY_TARGET: api, VITE_API_BASE_URL: "/api/v1" },
    },
  ],
});
