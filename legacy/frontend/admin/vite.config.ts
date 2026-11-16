import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

const adminApiTarget = process.env.ADMIN_API_PROXY_TARGET ?? "http://localhost:8080";
const adminRoot = fileURLToPath(new URL(".", import.meta.url));
const sharedPublic = fileURLToPath(new URL("../public", import.meta.url));

// The admin SPA is embedded in the Go binary and served at /admin/. Keep the
// base path and the embed output directory in sync with the backend.
export default defineConfig({
  root: adminRoot,
  base: "/admin/",
  publicDir: sharedPublic,
  plugins: [react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    outDir: fileURLToPath(new URL("../../pkg/static/dist", import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 900,
    rollupOptions: {
      input: {
        admin: fileURLToPath(new URL("./admin.html", import.meta.url)),
      },
    },
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "node",
  },
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      "/api": { target: adminApiTarget, changeOrigin: true },
      "/media": { target: adminApiTarget, changeOrigin: true },
      "/health": { target: adminApiTarget, changeOrigin: true },
    },
  },
  preview: { port: 4173, strictPort: false },
});
