import path from "node:path";
import { createRequire } from "node:module";
import { access, readFile, readdir } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolveAllowedRealPath, resolveFeatureFile } from "./filesystem-policy.mjs";
import { validateDataInputManifest } from "./manifest-validation.mjs";

const labRoot = path.dirname(fileURLToPath(import.meta.url));
const vitasilFrontendDir = process.env.VITASIL_FRONTEND_DIR;
const portText = process.env.FEATURE_LAB_PORT;

if (!vitasilFrontendDir) {
  throw new Error("VITASIL_FRONTEND_DIR must point to the Vitasil frontend directory. Start Feature Lab with the Vitasil dev:feature-lab script.");
}
if (!portText || !/^\d+$/.test(portText)) {
  throw new Error("FEATURE_LAB_PORT must be an explicit TCP port number.");
}

const vitasilRoot = path.resolve(vitasilFrontendDir);
const sourceRoot = path.join(vitasilRoot, "src");
const featureRoot = path.join(sourceRoot, "features");
const requireFromVitasil = createRequire(path.join(vitasilRoot, "package.json"));

for (const requiredFile of [path.join(vitasilRoot, "package.json"), featureRoot]) {
  try {
    await access(requiredFile);
  } catch {
    throw new Error(`Cannot read configured Vitasil frontend path: ${requiredFile}`);
  }
}

async function loadVitasilPlugin(packageName) {
  const packageEntry = requireFromVitasil.resolve(packageName);
  const loaded = await import(pathToFileURL(packageEntry).href);
  return loaded.default ?? loaded;
}

const [react, vue, tailwind] = await Promise.all([
  loadVitasilPlugin("@vitejs/plugin-react"),
  loadVitasilPlugin("@vitejs/plugin-vue"),
  loadVitasilPlugin("@tailwindcss/vite"),
]);

const registryVirtualID = "virtual:feature-lab/registry";
const registryResolvedID = `\0${registryVirtualID}`;
const entryNames = ["index.tsx", "index.ts", "index.jsx", "index.js"];

async function featureEntries() {
  const directories = (await readdir(featureRoot, { withFileTypes: true }))
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .sort((left, right) => left.localeCompare(right));
  const output = [];

  for (const directoryName of directories) {
    const directory = path.join(featureRoot, directoryName);
    const manifestPath = path.join(directory, "manifest.json");
    let manifest;
    let error = "";
    let entryPath = "";
    let runtimeAbi = "legacy-react";
    let styles = [];
    let html = "";

    try {
      manifest = JSON.parse(await readFile(manifestPath, "utf8"));
      if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)) {
        throw new Error("manifest.json must contain an object");
      }
      if (manifest.key !== directoryName) {
        throw new Error(`manifest key must match feature directory \"${directoryName}\"`);
      }
      validateDataInputManifest(manifest);
    } catch (reason) {
      error = `manifest.json: ${reason instanceof Error ? reason.message : String(reason)}`;
    }

    if (!error) {
      try {
        const runtime = manifest.runtime;
        if (runtime === undefined) {
          for (const candidate of entryNames) {
            try {
              entryPath = await resolveFeatureFile(directory, candidate, "legacy source entry");
              break;
            } catch {
              // Try the next filename, matching build-features.mjs.
            }
          }
          if (!entryPath) throw new Error("missing legacy React source entry (expected index.tsx, index.ts, index.jsx, or index.js)");
        } else {
          if (!runtime || typeof runtime !== "object" || Array.isArray(runtime)) throw new Error("runtime must be an object");
          if (!["static-module-v1", "static-html-v1"].includes(runtime.abi)) throw new Error("unsupported runtime ABI " + String(runtime.abi ?? ""));
          runtimeAbi = runtime.abi;
          if (typeof runtime.entry !== "string" || !runtime.entry.trim()) throw new Error("runtime.entry must be a relative file path");

          entryPath = await resolveFeatureFile(directory, runtime.entry, "runtime.entry");
          if (runtime.styles !== undefined && !Array.isArray(runtime.styles)) throw new Error("runtime.styles must be an array of relative file paths");
          const stylePaths = runtime.styles ?? [];
          for (const [index, stylePath] of stylePaths.entries()) {
            if (typeof stylePath !== "string" || path.extname(stylePath).toLowerCase() !== ".css") {
              throw new Error("runtime.styles[" + index + "] must reference a CSS file");
            }
          }
          styles = await Promise.all(stylePaths.map((stylePath, index) => resolveFeatureFile(directory, stylePath, "runtime.styles[" + index + "]")));
          if (runtimeAbi === "static-html-v1") html = await readFile(entryPath, "utf8");
        }
      } catch (reason) {
        error = "feature runtime: " + (reason instanceof Error ? reason.message : String(reason));
        entryPath = "";
        styles = [];
        html = "";
      }
    }

    output.push({
      key: directoryName,
      manifest: manifest ?? null,
      error,
      runtimeAbi,
      entryPath,
      styles,
      html,
    });
  }
  return output;
}

function viteFileSpecifier(filePath) {
  const normalized = path.resolve(filePath).replaceAll(path.sep, "/");
  return `/@fs/${normalized.replace(/^\/+/, "").split("/").map(encodeURIComponent).join("/")}`;
}

function featureRegistryPlugin() {
  let runtimeFiles = new Set();
  return {
    name: "feature-lab-registry",
    enforce: "pre",
    resolveId(id) {
      if (id === registryVirtualID) return registryResolvedID;
    },
    async load(id) {
      if (id !== registryResolvedID) return null;
      const entries = await featureEntries();
      runtimeFiles = new Set(entries.flatMap((entry) => [
        ...(entry.runtimeAbi === "legacy-react" || entry.error ? [] : [entry.entryPath]),
        ...entry.styles,
      ]));
      const moduleLines = [
        `export const vitasilSourceRoot = ${JSON.stringify(sourceRoot)};`,
        "export const features = [",
      ];
      for (const entry of entries) {
        const styleLoads = entry.styles.map((stylePath) => `import(${JSON.stringify(viteFileSpecifier(stylePath))})`);
        const load = !entry.entryPath || entry.error
          ? "null"
          : entry.runtimeAbi === "static-html-v1"
            ? `async () => { await Promise.all([${styleLoads.join(",")}]); return { html: ${JSON.stringify(entry.html)} }; }`
            : entry.runtimeAbi === "static-module-v1"
              ? `async () => { await Promise.all([${styleLoads.join(",")}]); return import(${JSON.stringify(viteFileSpecifier(entry.entryPath))}); }`
              : `() => import(${JSON.stringify(viteFileSpecifier(entry.entryPath))})`;
        moduleLines.push(`  { key: ${JSON.stringify(entry.key)}, manifest: ${JSON.stringify(entry.manifest)}, error: ${JSON.stringify(entry.error)}, runtimeAbi: ${JSON.stringify(entry.runtimeAbi)}, entryPath: ${JSON.stringify(entry.entryPath)}, load: ${load} },`);
        if (entry.entryPath) this.addWatchFile(entry.entryPath);
        for (const stylePath of entry.styles) this.addWatchFile(stylePath);
        const manifestPath = path.join(featureRoot, entry.key, "manifest.json");
        try { this.addWatchFile(manifestPath); } catch { /* Missing manifests are reported in the registry. */ }
      }
      moduleLines.push("];");
      return moduleLines.join("\n");
    },
    transform(code, id) {
      const normalizedId = path.resolve(id.split("?", 1)[0]);
      if (normalizedId !== path.join(labRoot, "src/styles.css")) return null;
      return code.replace("/* VITASIL_SOURCE_SCAN is replaced by the Vite config before Tailwind runs. */", `@source "${sourceRoot.replaceAll("\\", "/")}";`);
    },
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        let requestURL;
        try {
          requestURL = new URL(request.url ?? "/", "http://127.0.0.1");
        } catch {
          response.statusCode = 400;
          response.end("Bad Request");
          return;
        }
        if (!requestURL.pathname.startsWith("/@fs/")) {
          next();
          return;
        }
        try {
          const filePath = decodeURIComponent(requestURL.pathname.slice("/@fs".length));
          await resolveAllowedRealPath(filePath, [labRoot, vitasilRoot]);
          next();
        } catch {
          response.statusCode = 403;
          response.end("Forbidden");
        }
      });
      server.watcher.add(featureRoot);
      server.watcher.on("all", (event, filePath) => {
        const normalized = path.resolve(filePath);
        if (path.dirname(normalized) !== featureRoot || !["addDir", "unlinkDir"].includes(event)) return;
        const module = server.moduleGraph.getModuleById(registryResolvedID);
        if (module) server.moduleGraph.invalidateModule(module);
        server.ws.send({ type: "full-reload" });
      });
    },
    handleHotUpdate({ file, server }) {
      const normalized = path.resolve(file);
      if (normalized.endsWith(`${path.sep}manifest.json`) && normalized.startsWith(`${featureRoot}${path.sep}`)) {
        const module = server.moduleGraph.getModuleById(registryResolvedID);
        if (module) server.moduleGraph.invalidateModule(module);
        server.ws.send({ type: "full-reload" });
        return [];
      }
      if (runtimeFiles.has(normalized) && normalized.startsWith(`${featureRoot}${path.sep}`)) {
        server.ws.send({ type: "full-reload" });
        return [];
      }
    },
  };
}

const reactEntry = requireFromVitasil.resolve("react");
const reactDOMClient = requireFromVitasil.resolve("react-dom/client");
const reactRuntime = requireFromVitasil.resolve("react/jsx-runtime");
const reactDevRuntime = requireFromVitasil.resolve("react/jsx-dev-runtime");

export default {
  root: labRoot,
  plugins: [tailwind(), react(), vue(), featureRegistryPlugin()],
  resolve: {
    alias: [
      { find: /^@\//, replacement: `${sourceRoot}/` },
      { find: /^react$/, replacement: reactEntry },
      { find: /^react\/jsx-runtime$/, replacement: reactRuntime },
      { find: /^react\/jsx-dev-runtime$/, replacement: reactDevRuntime },
      { find: /^react-dom\/client$/, replacement: reactDOMClient },
      { find: /^react-dom$/, replacement: requireFromVitasil.resolve("react-dom") },
    ],
    dedupe: ["react", "react-dom"],
  },
  optimizeDeps: {
    include: [
      "@base-ui/react/button",
      "@base-ui/react/input",
      "@base-ui/react/merge-props",
      "@base-ui/react/navigation-menu",
      "@base-ui/react/separator",
      "@base-ui/react/use-render",
      "class-variance-authority",
      "cn",
      "embla-carousel-react",
      "lucide-react",
    ],
  },
  server: {
    host: "127.0.0.1",
    port: Number(portText),
    strictPort: true,
    fs: { allow: [labRoot, vitasilRoot] },
  },
};
