# Feature Lab

Feature Lab is a local development preview host for features in the sibling Vitasil repository. It runs from this Engine repository, does not start Website Engine, does not call Engine APIs or rewrite feature ZIPs, and is not used by the public Go document/runtime path.

## Start

From the Vitasil checkout:

```sh
cd frontend
pnpm dev:feature-lab
```

The command serves Feature Lab at `http://127.0.0.1:5178`. Override the port when needed:

```sh
FEATURE_LAB_PORT=5180 pnpm dev:feature-lab
```

The Vitasil package script supplies `VITASIL_FRONTEND_DIR`. The listener is restricted to loopback.

## Feature runtimes

Feature Lab discovers `src/features/<key>/manifest.json` and supports two ways to preview a feature:

- **Existing Vitasil source:** when `runtime` is absent, the lab loads the root `index.tsx`, `index.ts`, `index.jsx`, or `index.js` and uses the current React contract and Vitasil toolchain.
- **Framework-neutral build:** set `runtime.abi` to `static-module-v1` or `static-html-v1` and point `runtime.entry` at a built file inside the feature directory. Optional CSS files go in `runtime.styles`. The `entry` and `styles` paths may point to a `dist/` directory.

For example, a Vue, Svelte, Angular, React, or vanilla JS feature can be built by its own toolchain as a self-contained ESM bundle. The output must implement the framework-neutral `mount(root, context)` contract or be a static HTML fragment. Bundle framework dependencies with the feature; Feature Lab does not install or resolve framework packages for it. Run that toolchain's watch/build command alongside Feature Lab; updates to the declared entry and CSS files trigger a preview reload.

```json
{
  "runtime": {
    "abi": "static-module-v1",
    "entry": "dist/feature.js",
    "styles": ["dist/feature.css"]
  }
}
```

`static-module-v1` exports `mount(root, context)` and returns `{ ready, update, unmount }`, as defined in [SPEC-feature-runtime.md](../docs/SPEC-feature-runtime.md). The lab seeds `arguments` from the manifest argument schema and constructs `content` examples from typed `dataInputs`. These are runtime preview fixtures only: Feature Lab does not define an ER model, assign fields to entities, or save CMS records. In the Engine, `/site` owns model structure and input bindings; `/content` owns record values and placement selections. The lab also supplies an editable `records` fixture for `sdk.listCollection`, local `sdk.submitForm`, route context, system assets, and an abort signal. It waits for `ready`, serializes context updates, and calls `unmount()` when the feature is replaced or reset.

`static-html-v1` renders the literal fragment and styles without data binding or SDK calls. Feature Lab rejects scripts, embedded documents, inline event handlers, and `javascript:` URLs before insertion.

Feature Lab does not compile framework source. Its `content` fixture represents the feature's typed runtime input shape and never replaces the Engine's snapshot data model or the generated `/content` editor. Keep the framework build command in the feature's own toolchain; the existing Vitasil `build-features.mjs` remains the React source builder. ZIP creation and disposable Engine integration checks are separate steps.

## Preview controls

- The feature is rendered inside an isolated iframe. Existing Vitasil core/feature styles and manifest token defaults are mirrored into it.
- Preset and custom widths change the iframe viewport for responsive testing. The path control and same-origin links simulate the current route.
- Invalid JSON retains the last valid preview data. Reset fixtures rebuilds `arguments` and typed `dataInputs` examples from the manifest without persisting them.
- Collection reads and form submissions stay in browser memory; no Engine API is called.
- Legacy React, module, and static HTML failures are isolated to the preview. Manifest and feature-file edits reload the registry.

Run `pnpm typecheck:feature-lab` from the Vitasil `frontend` package. See the [contract, acceptance criteria, and verification record](../docs/SPEC-feature-lab.md).
