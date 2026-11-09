import { defineConfig } from "@playwright/test";

// E2E against a running latchd (started by CI) with the built UI embedded.
export default defineConfig({
  testDir: "./e2e",
  timeout: 30000,
  use: { baseURL: process.env.LATCH_URL || "http://127.0.0.1:4040" },
  reporter: "line",
});
