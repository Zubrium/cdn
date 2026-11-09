# Frontend architecture

The admin is the only application frontend. Public documents are Go-rendered; browser ABI scripts are authored under `pkg/runtime/scripts`, public styles under `pkg/runtime/styles`, and all are built as static assets embedded by `pkg/runtime`. Public styles are isolated from admin CSS.

## Packages

- `frontend/ui/` — admin CSS foundation and design-system entrypoints. It does not contain route tables or application state.
- `frontend/admin/` — React 18 + TypeScript admin SPA. Screens live in `src/pages/`, API/domain logic in `src/api.ts` and `src/domains/`, and shared admin controls in `src/components/`.
- `pkg/runtime/templates/` — complete public document, metadata, placement and system-page templates. Go matches the route, resolves the snapshot/content, sets HTTP status and metadata, and renders the initial HTML. No Vite `index.html`, client route table, or SPA fallback exists for public requests.
- `pkg/runtime/scripts/` — browser-side feature ABI loader and lifecycle modules. They consume Go's inert bootstrap, mount/update/unmount placements and fetch `/api/v2/runtime` only on subsequent history transitions. React 18 is dynamically loaded only for the preserved `legacy-react-v1` ABI; other public startup paths do not load React.
- `pkg/runtime/styles/` — public-only stylesheet sources, with no imports from the admin UI package.
- `pkg/runtime/assets/` — public-only source assets such as the fallback favicon.

## Routing

There are two distinct routing layers:

1. **Admin routes:** `frontend/admin/src/app/router.tsx` uses React Router's `createBrowserRouter` with `basename: "/admin"`. It maps URLs such as `/admin/content` and `/admin/site/pages` to admin screens. The Go static handler serves the SPA shell for nested `/admin/**` URLs, then React Router selects the screen in the browser.
2. **Public site routes:** route trees are edited in `/site/pages` and stored in the selected snapshot. Go compiles and matches them in `features/sitebuilder/routes.go`; `SnapshotService.PageDocument` resolves status, SEO metadata, published data, bootstrap and placement roots for Go `html/template`. Template markup stays in embedded `.tmpl` files; Go passes typed data to named templates. The first browser render consumes this composition directly from the document. `/api/v2/runtime` remains available to other clients and for subsequent `popstate` route changes; no browser route table is introduced. Chromium prerender executes the existing feature ABI modules and serializes the ready page before the public response.

The public ABI runtime listens for browser `popstate` changes and reloads composition for an in-app history transition. Ordinary links navigate with a full document request; Go remains the authority for route matching, status, metadata and initial HTML. The initial page load does not request composition JSON a second time. A prerendered document keeps server-rendered placement markup through one browser paint opportunity before the existing ABI lifecycle mounts interactive features; this changes only scheduling, not ABI behavior.

`feature-lab/` is a development-only feature source harness. It runs with the Vitasil frontend's Vite/React/Vue toolchain on loopback, uses local fixture SDK data, and is independent of the public Go renderer. It does not register ZIPs or call the Engine API.

## Build and development

From `frontend/`:

- `pnpm dev:admin` serves the admin app on port 5173 and proxies API requests to `ADMIN_API_PROXY_TARGET` (default `http://localhost:8080`).
- `pnpm build:admin` builds the `admin.html` artifact into `pkg/static/dist`, embedded by `pkg/static` and mounted below `/admin/`.
- `pnpm build:runtime` builds only content-addressed feature ABI scripts and public CSS into `pkg/runtime/dist`; Go serves those assets and renders every public HTML document from templates.
- `pnpm build` runs both package typechecks and builds both artifacts.
- `pnpm test` and `pnpm lint` cover the frontend workspace; `pnpm --filter @website-engine/admin test` runs admin tests directly.

The workspace owns frontend build tools and its lockfile. Public scripts are authored in `pkg/runtime`; admin pages are never bundled into public assets.
