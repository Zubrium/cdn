# Feature Lab

Status: manifest format 2 and `dataInputs` fixtures are implemented. The latest recorded browser preview on 2026-11-09 covered ten Vitasil source features after public document rendering moved to Go `html/template`. Later on 2026-11-09, `catalog` and `main-data` source/manifests changed and their ZIPs were rebuilt; the updated pair still needs a Feature Lab browser recheck. Feature Lab remains an independent source/runtime preview launched from the Vitasil frontend package; it is not the public document renderer.

## Purpose and boundaries

Feature Lab shortens the authoring loop for features without requiring the Engine process. It loads feature directories and `manifest.json` files from the configured Vitasil checkout. It does not copy features, mutate feature manifests, register builds, call Engine APIs, or change the feature ABI. Public Go `html/template` document rendering is separate from this source preview and does not change the lifecycle/context contract.

Feature Lab mounts its preview into an empty iframe host, so React-backed features use their normal `createRoot` path there. The public Engine instead starts from the Chromium-prerendered `#root`; React-backed feature helpers use `hydrateRoot` on that existing markup. Feature Lab checks authoring/runtime behavior, not Go document output, public hydration or prerender cache behavior; those remain separate Engine/Vitasil browser acceptance criteria.

The lab is development-only and is not part of the public runtime or document renderer. It listens on loopback, uses fixture data in browser memory, and provides local mock implementations of `SiteSDK`. A form submission is logged in the page and never leaves the browser. Feature source can still issue its own network requests; current features must use the injected SDK for Engine operations. Public HTML, ABI scripts, assets and isolated styles are owned by `pkg/runtime` and built/served separately.

Storybook/Ladle are not part of the first implementation. Feature Lab covers the feature-level contract (format 2 manifest, arguments, typed `dataInputs`, projected runtime content, SDK and route context). Fixtures are local examples derived from typed runtime ports; they are not compatibility fixtures and do not treat feature manifests as CMS record editors. A component story catalog can be added separately if component documentation becomes a distinct need.

Feature Lab supports the existing `legacy-react-v1` source contract and the `static-module-v1` / `static-html-v1` contracts described in [SPEC-feature-runtime.md](SPEC-feature-runtime.md). These are the current ABI values; this spec adds no ABI and the lab must not rewrite manifest/runtime contracts. Static module preview uses the module's own bundled framework runtime; Feature Lab neither installs nor selects React, Vue, Svelte, or another framework for it. The lab iframe is a feature-authoring harness, not a public page/slot composition; public `#root`, landmark and placement-root requirements are checked by Engine browser acceptance. This is a local ABI harness, not proof that an Engine archive was registered or rendered by the public runtime. Archive validation and Engine integration checks remain separate.

## Connection and configuration

- Each feature directory contains `manifest.json`. When `runtime` is absent, Feature Lab keeps the existing discovery rule for a root `index.tsx`, `index.ts`, `index.jsx`, or `index.js` entry, in that priority order, and uses the Vitasil React toolchain for the existing `legacy-react-v1` source contract.
- When `manifest.runtime` is present, `runtime.abi` must be `static-module-v1` or `static-html-v1`. `runtime.entry` and each optional `runtime.styles` item must be a relative path to an existing file inside that feature directory. The source preview accepts Vite-supported TypeScript/JavaScript entries, including current Vitasil `index.ts` module entries, and also accepts prebuilt files such as `dist/entry.js` and `dist/styles.css`.
- Vite transpilation/import resolution in source-preview mode is a development convenience, not Engine archive behavior. It uses packages already installed in the configured Vitasil frontend; Feature Lab does not install packages or resolve remote URLs. For registered `static-module-v1` ZIPs, framework imports and runtime dependencies must be bundled into local ESM assets according to [SPEC-feature-runtime.md](SPEC-feature-runtime.md).
- `VITASIL_FRONTEND_DIR` is required and identifies the Vitasil `frontend` directory. The launch script sets it to its current package directory. No machine-specific absolute path is committed.
- Vite and Tailwind host tooling, and React packages used by the Feature Lab UI and React source/adapter, are resolved from the Vitasil frontend package. A static-module source entry is transformed and its local imports are resolved by Vite; a prebuilt archive artifact is expected to contain its runtime dependencies. No package dependency is added.
- The Engine repository contains the application, Vite configuration, feature-discovery plugin and documentation under `feature-lab/`.

## Preview contract

### Existing React source ABI

Feature Lab imports the default export from a source feature entry and passes the existing `FeatureRuntimeProps` contract without changing feature semantics:

- `content` is a local fixture generated only from manifest format 2 `dataInputs`: each record port creates an object and each collection port creates an ordered sample array under its port key.
- Format 1 manifests and a manifest `content` property are rejected; no compatibility fallback is used.
- `arguments` comes from the manifest argument schema.
- `featureKey`, `systemAssets`, `collectionEndpoint` and `detailLink` are supplied as local runtime context.
- The feature renders through a React portal into a same-origin iframe, isolating site CSS and viewport-dependent layout from the lab. Vitasil CSS modules and core styles are mirrored into the iframe as Vite updates them.
- Feature CSS custom properties are populated from the selected manifest's typed `designTokens` defaults using the existing `--feature-<featureKey>-<tokenKey>` names. The lab does not create or persist token overrides.
- `sdk.listCollection` returns records from the editable fixture; `sdk.submitForm` records the submitted feature key and payload in a visible event log and resolves locally.
- Runtime input fixtures can be edited as JSON for `content`, `arguments`, collection records and runtime context. This is a developer preview control, not an editor schema or CMS authoring workflow. Invalid JSON is shown without replacing the last valid preview input.
- Preview width changes the iframe's actual viewport without resizing the developer controls, so media queries and viewport units behave as on the public site. Internal feature links update the simulated pathname without navigating away from the lab; modified clicks and non-self targets retain normal browser behavior.
- HMR disposes the old React root before Vite reloads the lab entry, avoiding duplicate-root warnings during iterative edits.
- Generated URL-like sample values are local paths; array records receive distinct link paths so list keys remain stable and unique.
- Navigation controls use semantic links, including links styled to look like buttons.

### Framework-neutral static module ABI

- `runtime.abi` is `static-module-v1`; `runtime.entry` points to the module source or prebuilt ESM file and `runtime.styles` optionally lists local CSS assets. The module implements the same `mount(root, context)` contract in both cases.
- Source entries and their imports are transformed by Feature Lab's existing Vite dev server. A separate feature watch/build may also write the declared artifact files while Feature Lab runs; edits to the declared entry and styles trigger a preview reload.
- Feature Lab passes the `FeatureContext` from [SPEC-feature-runtime.md](SPEC-feature-runtime.md) to the named `mount(root, context)` export. Its `sdk` uses local collection fixtures and logs form submissions in memory.
- The preview waits for the returned `ready` promise. Context changes call `update()` only after readiness; updates are serialized to preserve their order. Switching feature or resetting preview state aborts the old signal and invokes `unmount()`.
- The preview accepts any framework whose module implements this ABI. Source preview can use dependencies already installed in Vitasil's frontend toolchain; the Engine-ready built artifact must include the framework runtime and satisfy the archive rules. The lab does not add framework-specific adapters or dependencies.

### Static HTML ABI

- `runtime.abi` is `static-html-v1`; the entry file is read as a literal HTML fragment and optional local styles are loaded into the isolated preview frame.
- The lab rejects scripts, embedded documents, inline event handlers, and `javascript:` URL values before inserting the fragment. It provides no content/argument editor or SDK for this ABI.
- Static HTML is read-only in Feature Lab; edit the source fragment/styles and reload through Vite.

Fixtures are examples, not schema authoring. Manifest `dataInputs` describes only the typed runtime input shape; the snapshot's graphical ER model defines CMS entities and generated record forms. The lab does not connect to an Engine or load a snapshot model.

## Discovery, updates, and failures

- Feature directories are listed in stable alphabetical order and show manifest key, name and version.
- Missing/invalid manifest or entry errors are shown beside the affected feature; they do not hide unrelated features.
- Editing source modules and styles uses Vite HMR. Adding/removing a feature or editing its manifest refreshes the generated registry.
- A feature render error is isolated to the preview and includes its stack in the developer view; the lab controls remain usable.
- JSON parsing errors identify the input that needs correction; feature import errors identify the selected key and source path.

## Security and operational constraints

- The server binds to `127.0.0.1` with a strict port. It is not a public preview service.
- The filesystem allow-list contains only the Feature Lab directory and the configured Vitasil frontend directory.
- The lab does not read credentials, environment secrets, databases, or object storage configuration.
- No automatic data persistence is performed. Reloading restores schema-derived sample fixtures.

## Acceptance

1. Starting the documented Vitasil script discovers format 2 feature manifests, generates sample data from each `dataInputs` contract, and renders source features without an Engine process or API.
2. A prebuilt plain ESM module implementing `static-module-v1` can mount, receive fixture SDK/context, update on content and route changes, and unmount cleanly; framework-specific packages are not required by Feature Lab.
3. A `static-html-v1` fragment renders with local styles and is rejected when it contains scripts, embedded documents, inline handlers, or `javascript:` URLs.
4. Invalid manifest, missing file, module import/lifecycle, JSON input, and React render failures remain localized and do not crash feature selection.
5. Form submits and collection reads use local fixtures only; browser network inspection shows no requests to Engine APIs.
6. Preview width controls and route simulation let authors inspect responsive and path-dependent behavior.
7. Feature Lab does not build or rewrite feature ZIP archives. Feature artifact production and Engine archive/runtime acceptance remain separate steps.

## Verification

Historical source-preview verification covered earlier Vitasil inventories, including the standalone `catalog-detail` feature before the product decision to remove detail pages. Additional disposable fixtures exercised the framework-neutral contracts. The current nine-feature inventory is recorded in Vitasil's [feature data input spec](../../vitasil/docs/SPEC-feature-data-inputs.md).

- Browser smoke test: all nine modules imported and rendered; the valid-feature run had zero browser console errors and warnings.
- `catalog` rendered three fixture records, requested `/catalog` through the local SDK mock, and loaded all three sample images. `catalog-detail` and `background` also loaded their sample media inside the frame.
- `form` accepted sample values, emitted a local `sdk.submitForm` event, and displayed its success state. Network inspection showed only local Vite modules and the local sample asset; there were no Engine API requests.
- JSON recovery kept the last valid preview mounted and exposed the parse error with `aria-invalid` and an alert. An external route was rejected without changing the browser URL; valid local links updated the path while retaining the selected feature query.
- The 360-pixel preset produced an iframe `innerWidth` of 360. Manifest token defaults and Vitasil feature styles were present in the isolated frame.
- A disposable Vitasil copy exercised malformed manifests, missing entry files, module-import errors, and render errors. Each error was localized to that feature while the lab controls remained available, and selecting a valid feature afterward recovered normally.
- Repeated entry HMR updates did not produce duplicate React-root errors after adding root disposal.
- A framework-neutral plain ESM fixture exercised `static-module-v1`: mount, fixture collection SDK, feature CSS, content updates, route updates and switching away to a different ABI.
- The module's declared CSS file was changed and restored while Feature Lab was running; both changes reloaded into the iframe without restarting the host or Engine.
- Delayed-ready and slow-update fixtures confirmed that the newest context is applied only after `ready`, and that successive `update()` calls run serially.
- A `static-html-v1` fixture rendered its literal fragment and styles. An `onclick` fixture was blocked before insertion; the handler did not execute and the iframe remained empty.
- Dot-segment, Windows-separator, and symlink escapes from a runtime entry were rejected within that feature's error card.
- From `vitasil/frontend`, `pnpm typecheck`, `pnpm lint`, and `pnpm typecheck:feature-lab` passed using Node 20. The recorded archive check covered nine feature ZIPs in a disposable copy and passed `unzip -t`; the live Vitasil `dist/feature-builds` directory was not modified.

The Vitasil frontend package does not currently define a `test` script. Browser acceptance was run with the installed Playwright CLI; no test framework dependency was added. The generic ABI was exercised with plain ESM, not with separate Vue/Svelte/Angular toolchains. Feature Lab is a source-development harness, so tests against the Engine API, uploaded ZIP registration, and the public runtime remain separate integration checks and are not claimed here.

#### Current source browser regression — 9 November 2026

The ten-feature run below is historical; `catalog-detail` was subsequently removed by product decision. The Vitasil shared `Button` selects native button semantics only when it renders a `<button>`, and collection fixtures assign synthetic record IDs without requiring IDs in manifest `dataInputs`.

Checks: Node 20 `npm run typecheck`, `npm run lint`, `npm run typecheck:feature-lab`, and `node --test scripts/feature-manifest-contract.test.mjs` (7/7) passed. ZIP integration checks are recorded separately in [QA_REPORT.md](QA_REPORT.md); source preview does not replace them. The Vite process pre-existed this check and was left running.

#### Current no-detail source preview — 9 November 2026

The latest source previews loaded `catalog` and `main-data` from the loopback Feature Lab routes `http://127.0.0.1:5178/?feature=catalog` and `?feature=main-data`. Both used `static-module-v1`, rendered local fixtures, and showed no preview errors or Engine API requests. The catalog showed no breadcrumbs and no product-title navigation. The current isolated build versions are `background@2.0.5`, `catalog@2.0.26`, `footer@2.0.14`, `form@2.0.8`, `header@2.0.11`, `hero@2.0.18`, `legal-documents@1.0.7`, `main-data@2.0.16`, and `not-found@2.0.5`. The disposable Engine route checks are recorded in the Engine [QA report](QA_REPORT.md); Feature Lab preview remains a source-only check and does not imply Engine registration, content projection, or public HTML acceptance.
