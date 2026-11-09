# System pages

Status: 7 November 2026 functional and focused browser acceptance passed on disposable PostgreSQL with current Vitasil `not-found@1.0.7`; route-level 403, manual screen reader and broader public accessibility matrix remain open.

## Purpose and boundaries

System pages give public visitors a useful HTML response when a route is missing, the active site cannot be rendered, or a branch has not yet been configured. They are part of the public runtime and have their own neutral presentation. They do not inherit admin navigation, branding, or scripts. They remain readable without JavaScript.

The gateway owns public host routing and access restrictions. It is not an engine-managed route and does not assign a snapshot to a branch. The engine does not infer access policy from a page name or feature.

## Core response contract

`SystemDocument(status)` returns a complete HTML document with a localized title, one semantic `<main>` landmark, viewport and language metadata, and a visible next action. It sets `robots` to `noindex,nofollow`. Route error documents are not cacheable; the HTTP document handler sends `Cache-Control: no-store`. The handler preserves the HTTP status for GET and HEAD and does not return a body for HEAD.

| Condition | Response |
| --- | --- |
| Unknown route or missing detail record | 404, “Страница не найдена” |
| Unassigned branch | Built-in 503, “Сайт ещё не настроен” |
| Runtime/composition error | 500, “Сайт временно недоступен” |
| Forbidden public request | No engine route-level source currently generates this status. The gateway/access-control layer must render its own 403 or explicitly integrate a public 403 response. The core `SystemDocument(403)` template exists, but a snapshot `systemPages["403"]` is currently not reached by public route resolution. |

The unassigned branch response is always the core 503. It cannot use a snapshot override because no snapshot payload exists; no per-branch startup-config override is supported. A 503 mapping stored in snapshot configuration does not change this rule.

## Snapshot feature overrides

`systemPages` is part of the snapshot configuration and refers to pages already in that same snapshot. For a missing route or a runtime error, the engine may render the configured 404 or 500 page when it has a concrete static route and all its placements resolve. The original error status is retained and the response remains `noindex,nofollow` and `no-store`. If the reference is absent, has no concrete route, or the feature fails, the engine returns its self-contained core fallback and does not recurse into the failing page.

A configured 404 catch-all route also keeps its composed feature HTML while returning 404. A directly visited concrete route for a system page is not reclassified solely by its page name. Configured system-page IDs are excluded from sitemap output even if the explicit page route lacks a metadata `noindex` override.

The current public router generates no 403 route-level response. Although configuration validation recognizes 403 and 503 keys, those mappings are not currently invoked by normal public routing. This is an implementation limitation; do not present those entries as functional overrides. Adding a new access-control or temporary-outage source requires a separate product/API decision.

## Acceptance criteria

- Core 404, 403, 500, and 503 documents contain their intended text, noindex metadata, and no admin-only assets.
- Unknown route and render failures preserve their HTTP status and use `no-store`.
- Unassigned branches always return the built-in 503 without snapshot identity or cache identity.
- Valid 404/500 overrides preserve the error status. Missing or failing overrides use a safe core page without recursive rendering.
- System pages are absent from sitemaps even without page-specific `noindex` metadata; ordinary indexable routes remain present.
- Public system pages remain independent of admin branding and function without JavaScript.

Automated coverage includes runtime document tests, sitebuilder unit tests, and the integration scenarios in `cmd/integration/system_pages_test.go`. On disposable database `website_engine_system_pages_qa_20261107_1b4c`, the coordinator ran `go test -count=1 -v ./cmd/integration -run '^(TestConfiguredSystemPagesAreExcludedFromSitemapWithoutNoindexOverride|TestSystemPage404FallsBackWhenOverrideIsMissingOrBroken|TestUnassignedBranchUsesBuiltInNotConfigured503|TestSystemPage500OverrideAndFallbackWhenOverrideIsBroken)$'`; all four tests passed. Preflight/postflight confirmed the exact local test database and zero other client sessions. The current-Vitasil SEO browser acceptance also verified the selected 404 override and that `/not-found` is absent from sitemap.

On 7 November a fresh current Vitasil shared stylesheet exposed a cascade bug: the Engine shell's unlayered root fallback overrode the ZIP's global site background, leaving the white 404 text unreadable. Shell fallback declarations now live in the earlier `runtime-base` layer so shared styles can override them. The current Vitasil core also had an unlayered global `a { color: inherit }` that overrode its primary button text utility; the redundant declaration was removed, and all nine feature manifests were patch-bumped to rebuild the same shared core digest. Node 20 `npm run typecheck`, `npm run lint`, and `FEATURE_BUILD_OUTPUT_DIR=… npm run build` passed. `TestSystemPagesBrowserAccessibilityAcceptance` passed with `not-found@1.0.7`: no-JS 404 override, core 500 fallback, and built-in unassigned 503; each preserved status, noindex/no-store, one main and H1, keyboard-visible focus, no admin assets and no horizontal overflow at 360/390/1280px. The 404 home action resolved to Vitasil's blue button text on its light surface, and the dark shared background was visible. Screenshot: `/tmp/website-engine-system-page-404-360.png`. The test ran against a unique local disposable PostgreSQL database and did not write to Vitasil's live API/storage. No public 403-producing route or manual screen-reader evaluation exists yet.

## Focused automated accessibility acceptance

The browser acceptance checks the rendered accessibility roles and names for the main landmark, primary heading, and home link on the Vitasil 404 override, built-in 500 fallback, and unassigned 503. It verifies keyboard focus, a visible focus indicator, the document title and heading that identify the error on navigation, reduced-motion preference, and text/action foreground-background contrast at 360, 390, and 1280 CSS pixels. Tests run without JavaScript and do not assert that assistive technology announces an alert or live region on initial navigation: the document title and heading provide the accessible navigation context. Automated Chromium accessibility-tree checks do not replace a manual VoiceOver or other screen-reader audit.

On 7 November the focused matrix ran against read-only Vitasil `not-found@1.0.7` in Chromium, with JavaScript disabled and reduced motion enabled. It checks named role locators and the main landmark's accessibility snapshot, a single visible heading and home link, keyboard focus at every width, no running animations at rest, and document title plus heading as initial navigation context. Initial navigation is not claimed to be a tested live announcement. Contrast calculations use computed solid foreground/background colors including alpha compositing; the heading threshold is 3:1 and the action threshold is 4.5:1. Results were 7.40:1 and 5.64:1 for the Vitasil heading/action and 15.21:1 for each core fallback pair. This is not a general accessibility or contrast audit, and it does not cover images, gradients, all themes, actual speech output, or interaction-triggered motion.

The first run found that the prerendered 404 override had an empty `<title>` when no metadata title was configured. The document handler now supplies the localized error title only for an empty title on an error response and preserves an explicitly configured title. `TestSystemPageDocumentTitleFallback` checks 404, 500, explicit metadata, and a successful page; the browser acceptance checks the resulting Vitasil title with JavaScript disabled.

Commands: `go test -count=1 ./cmd/integration -run '^(TestSystemPagesBrowserAccessibilityAcceptance|TestSystemPageDocumentTitleFallback)$'` with `TEST_DATABASE_URL` pointing at the unique local QA database, `TEST_FEATURE_DIR` at the existing Vitasil ZIP directory, and `PRERENDER_TEST_NODE`, `PRERENDER_TEST_MODULE`, and `PRERENDER_TEST_BROWSER` pointing to Node 20, the existing Playwright module, and Chromium. Also passed `go test -run '^$' ./cmd/integration`, `go vet ./cmd/integration ./pkg/runtime`, and Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 files, 85 tests). The disposable database `website_engine_system_pages_browser_qa_20261107_8d1f`, PostgreSQL container `system-pages-a11y-qa-20261107-8d1f` (port 35484), and its anonymous volume `2989e8a9246fbbdc6ddba40a540300abcc15b4117af4448a86c7734c5d0913ea` were removed after zero-other-session verification. The fixture used an in-memory object store; the running Vitasil stack was not touched.
