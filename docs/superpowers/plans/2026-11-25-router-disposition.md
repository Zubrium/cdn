# Router core-file disposition implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Позволить оператору явно выбирать `inline`/`attachment` для `core-file` роута Router, сохранив независимость Router API от Site API.

**Architecture:** Router хранит `target.disposition` в versioned route record; Core MIME остаётся единственным источником `Content-Type`; delivery fail-closed запрещает inline для небезопасных типов. Site API не мутирует Router: неактивный переключатель удалён из snapshot UI и типов полностью.

**Tech Stack:** Go 1.26, React 18 + TypeScript, Vitest, OpenAPI 3.1, Tailwind/Radix.

**Spec:** `spec/engines/router/SPEC-router-engine.md`, `spec/engines/website/SPEC-site-editor-workspace.md`, `spec/presentation/openapi/router.yaml`.

## Global Constraints

- Core заморожен: SQL, S3, migrations и Core-domain не меняются.
- Новых публичных HTTP-путей, version prefix и compatibility aliases нет.
- Router API и Site API остаются независимыми: `/site` не создаёт и не обновляет Router routes.
- `core-file` остаётся exact-роутом с одним server-owned `file_id`; visitor не выбирает file ID, disposition или MIME.
- `inline` разрешён только для `image/jpeg`, `image/png`, `image/webp`, `image/avif`, `image/gif`, `video/mp4`, `video/webm`, `audio/mpeg`, `application/pdf`. `text/html`, `image/svg+xml`, `application/octet-stream` и прочие типы всегда `attachment`.
- `Cache-Control` остаётся `no-store`; Range/206 не входят в эту волну.
- TDD: RED-тест до production-кода; запускать полные команды модулей.

## Review Focus

- Явно заданный `inline` для `text/html` или `image/svg+xml` не должен стать XSS: --
  тест на принудительный `attachment`.
- Отсутствующее поле disposition должно давать `inline` только для безопасного MIME: --
  тест дефолта для `image/webp`.
- HEAD и GET обязаны совпадать по disposition: -- тест HEAD.
- conditional GET `304` не должен терять или менять disposition: -- тест `If-None-Match`.
- Старые route records без поля обязаны грузиться и валидироваться: -- тест legacy JSON.

---

### Task 1: Router model + delivery disposition

**Files:**
- Modify: `engines/router/features/routes/model/route.go`
- Modify: `presentation/router/delivery.go`
- Test: `engines/router/features/routes/model/route_test.go`
- Test: `presentation/router/delivery_test.go`

**Interfaces:**
- Consumes: `model.Target`, `engine.DeliveryResponse`, `CoreFiles`.
- Produces: `Target.Disposition` (`"inline" | "attachment" | ""`), `dispositionForFile(string, string) string`.

- [ ] **Step 1: Write RED tests** for: valid enum accepted on core-file; invalid enum rejected; disposition absent → `inline` only for safe MIME; explicit `attachment` always wins; unsafe MIME → `attachment`; legacy JSON without field unmarshals to `""`; GET/HEAD/304 use the same disposition.
- [ ] **Step 2: Run the tests and verify they fail** because the field/helper do not exist.
- [ ] **Step 3: Implement minimal code**: add `Disposition string \`json:"disposition,omitempty"\`` to `model.Target`; validate only for `TargetCoreFile`; add server-side MIME allowlist helper in `presentation/router/delivery.go`; use it in both `deliverCoreFile` returns.
- [ ] **Step 4: Run the tests and verify they pass**.
- [ ] **Step 5: Commit** the model + delivery change.

### Task 2: Router HTTP/OpenAPI contract

**Files:**
- Modify: `spec/presentation/openapi/router.yaml`
- Modify: `spec/engines/router/SPEC-router-engine.md`
- Test: OpenAPI contract test in `tests/go/...` (existing route contract suite).

**Interfaces:**
- Consumes: `target.disposition`.
- Produces: documented enum and header policy.

- [ ] **Step 1: Write/Update RED contract test** asserting `RouteTarget` accepts `disposition` only for `core-file`, enum `inline|attachment`, and rejects unknown values.
- [ ] **Step 2: Run it and verify it fails** against current spec.
- [ ] **Step 3: Update OpenAPI schema and SPEC-router-engine §5.3** so documented default is `inline` for the safe MIME list and explicit `attachment` overrides it.
- [ ] **Step 4: Run contract test and verify it passes**.
- [ ] **Step 5: Commit** the contract update.

### Task 3: Admin Router UI + types

**Files:**
- Modify: `frontend/admin/src/types.ts`
- Modify: `frontend/admin/src/domains/router/route-target.ts`
- Modify: `frontend/admin/src/pages/router/RouterRoutesPage.tsx`
- Test: `tests/frontend/admin/src/domains/router/route-target.test.ts` (create if absent).

**Interfaces:**
- Consumes: `RouterCoreFileRouteTarget.disposition?`.
- Produces: `buildRouterTarget` emits disposition only for `core-file`.

- [ ] **Step 1: Write RED frontend test** asserting `buildRouterTarget` keeps `disposition: "attachment"` and omits it when `inline`/unset; non-file targets never emit it.
- [ ] **Step 2: Run `pnpm --filter @website-engine/admin test -- <test path>` and verify RED**.
- [ ] **Step 3: Update TS type/draft/builder and add accessible `<select>` in RouteDialog** shown only for `targetKind === "core-file"`.
- [ ] **Step 4: Run the focused test and full admin test suite; verify green**.
- [ ] **Step 5: Commit** the Admin change.

### Task 4: Remove the inactive Site toggle

**Files:**
- Modify: `frontend/admin/src/types.ts`
- Modify: `frontend/admin/src/pages/site/FileRoutePage.tsx`
- Modify: `frontend/admin/src/pages/site/RouterPage.tsx`
- Modify: `frontend/admin/src/pages/site/route-tree.ts`
- Modify: `spec/engines/website/SPEC-site-editor-workspace.md`
- Test: affected Site frontend tests.

**Interfaces:**
- Consumes: nothing from Router.
- Produces: no `fileDisposition` in new snapshots or UI.

- [ ] **Step 1: Update RED tests** asserting no file-disposition control and no `fileDisposition` field in created route nodes.
- [ ] **Step 2: Run them and verify RED**.
- [ ] **Step 3: Remove the field from UI, types, route-tree and spec promise**; do not migrate stored legacy snapshots.
- [ ] **Step 4: Run focused + full admin tests and verify green**.
- [ ] **Step 5: Commit** the Site cleanup.

### Task 5: Acceptance + live verification

**Files:**
- Modify: `tests/ACCEPTANCE-MATRIX.md`
- Modify: `docs/QA_REPORT.md`

- [ ] **Step 1: Add executable acceptance scenarios** for inline safe image, forced attachment, unsafe MIME, HEAD/304, and existing route default.
- [ ] **Step 2: Run full Go module checks, admin typecheck/test/build, ESLint, and the live disposable-stack curl probes.**
- [ ] **Step 3: Record exact commands and outputs; do not mark blocked checks as pass.**
- [ ] **Step 4: Commit evidence and push** only after all applicable checks pass.
