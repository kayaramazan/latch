import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build output is embedded into latch-control via go:embed (panel package)
// and served as the customer control-plane dashboard.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/panel/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5273,
    proxy: {
      "/api": { target: "http://127.0.0.1:9000", ws: true },
      "/ops": "http://127.0.0.1:9000",
      "/webhooks": "http://127.0.0.1:9000",
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
