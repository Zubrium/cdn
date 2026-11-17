import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const frontendRoot = fileURLToPath(new URL(".", import.meta.url));
const runtimeRoot = fileURLToPath(new URL("../pkg/runtime/scripts/", import.meta.url));

export default defineConfig({
  root: frontendRoot,
  base: "/",
  publicDir: fileURLToPath(new URL("../pkg/runtime/assets/", import.meta.url)),
  plugins: [react()],
  resolve: {
    alias: [
      { find: /^@\//, replacement: `${runtimeRoot}/` },
      { find: /^react$/, replacement: fileURLToPath(new URL("./node_modules/react/index.js", import.meta.url)) },
      { find: /^react\/(.*)$/, replacement: `${fileURLToPath(new URL("./node_modules/react/", import.meta.url))}$1` },
      { find: /^react-dom\/(.*)$/, replacement: `${fileURLToPath(new URL("./node_modules/react-dom/", import.meta.url))}$1` },
    ],
  },
  build: {
    outDir: fileURLToPath(new URL("../pkg/runtime/dist/", import.meta.url)),
    emptyOutDir: true,
    lib: {
      entry: fileURLToPath(new URL("../pkg/runtime/scripts/main.ts", import.meta.url)),
      formats: ["es"],
      fileName: () => "assets/public-runtime-[hash].js",
      cssFileName: "public-runtime",
    },
    rollupOptions: {
      output: {
        entryFileNames: "assets/public-runtime-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: (asset) => asset.name?.endsWith(".css")
          ? "assets/public-runtime-[hash][extname]"
          : "assets/[name]-[hash][extname]",
      },
    },
    sourcemap: false,
  },
});
