# Website Engine Production Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` for independent tasks and integrate/review each result before proceeding. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Реализовать согласованный production workflow Website, Content, Router, внешней Presentation и workers на действующей модульной архитектуре.

**Architecture:** Межмодульные вызовы остаются в `main/` и проходят через JSON typed bus со статическим profile allowlist; крупные bodies передаются через call-scoped stream capability. Core Session остаётся только в передаваемом dispatcher-ом `context.Context`, не входит в typed request и JSON. Content owns locale-scoped state; Website produces immutable artifacts; внешний publication registry связывает route/page; Coordinator владеет durable workflows; Router выполняет locale CAS и public delivery.

**Tech Stack:** Go workspace и существующие модули; текущие React/TypeScript Admin API patterns; PostgreSQL/Core `engine_data`; Core Coordinator/files capabilities; Linux/Windows/macOS native sandbox mechanisms; FFmpeg и Chromium только в supervised workers.

**Spec:** `spec/architecture/SPEC-modular-platform.md`; `spec/engines/content/SPEC-content-engine.md`; Website specs including `SPEC-static-site-artifact.md`; `spec/engines/router/SPEC-router-engine.md`; `spec/workers/` contracts; relevant OpenAPI in `spec/presentation/openapi/`.

## Global Constraints

- Не изменять `core/`; если нужная capability отсутствует в утверждённом Core contract — остановить соответствующую операцию и сообщить точный блокер.
- Не создавать и не изменять тесты; не запускать тесты, сборки, линтеры, debug или E2E; не утверждать, что код проверен или работает.
- Не коммитить и не пушить.
- Сохранять все существующие пользовательские изменения; каждый файл с уже изменёнными рабочими правками перед изменением изучать отдельно и не перезаписывать несвязанные изменения.
- Не помещать file bytes или `core.Session` в JSON bus payload; большие тела передавать только через scoped streams, Core Session остаётся в `context.Context` и не становится полем typed request.
- Не добавлять внешние versioned URLs, произвольные operation IDs, временные fallback-ы, непроверенные sandbox fallback-ы или зависимости без необходимого конкретного контракта.
- API handlers, OpenAPI, Admin client/types и существующие пользовательские acceptance docs синхронизировать только там, где конкретное API изменение необходимо; изменённые пользователем `tests/ACCEPTANCE-MATRIX.md` и `docs/QA_REPORT.md` не перезаписывать.
- Locale удаления блокировать при immutable history и при всех draft/publication/selection/Router references; canonicalization — BCP 47 + IANA Preferred-Value.
- Worker isolation обязательна на Linux, Windows и macOS; отсутствие или неподтверждённая активность sandbox означает отказ запуска без снижения уровня изоляции.

## Review Focus

- Core Session и capability references не должны утечь в JSON, логи или сохраняемые workflow payloads; исходный `context.Context` сохраняется при dispatch, а большие bodies используют одноразовые scoped streams.
- Повторные/параллельные Content mutations не должны смешивать locales или терять CAS/idempotency; план сохраняет locale-scoped aggregate, revision и atomic Core writes.
- Ошибки/устаревшие Website builds и Router CAS не должны менять действующий artifact; workflow держит независимые per-route/per-locale результаты и переключает только успешные targets.
- Retirement/locale removal не должны оставлять публичные route targets либо терять immutable history; registry и Coordinator владеют fan-out, а Content удерживает history guard.
- Sandbox backend не должен считать наличие executable достаточным доказательством изоляции; startup проверяет effective OS controls, а macOS требует подписанный App Sandbox helper.

---

### Task 1: Завершить typed bus и call-scoped capabilities

**Files:**
- Modify: `pkg/contracts/contracts.go`, `pkg/contracts/streams.go`, `pkg/contracts/*.go`
- Modify: `main/internal/bus/*.go`
- Modify: `main/internal/composition/*.go`
- Modify: зарегистрированные `*/presentation/contract.go` и component operation bindings по мере выявления межмодульных вызовов

**Interfaces:**
- `contracts.Operation[Request, Response]`, `Envelope`, `Dispatcher`, `ScopedStreamCapability` остаются общим протоколом.
- Добавить versioned typed operation contracts для всех внешних runtime calls из Website/Content/Router/worker composition; каждый operation ID фиксирован в code и выбранном profile allowlist.
- Передавать исходный context через Dispatcher без включения Session в operation DTO; file stream refs выдавать/закрывать в пределах одного вызова.

- [ ] Инвентаризировать межмодульные runtime calls в `main/internal/composition` и module presentation dependencies; составить статический список operation contracts, не меняя Core.
- [ ] Подключить `ScopedStreams` к handlers, которым передаются/возвращаются file bodies; JSON DTO содержат только `StreamRef` и bounded metadata.
- [ ] Зарегистрировать bindings до `Dispatcher.Seal()` согласно profile allowlist; запретить вызов engine API напрямую из другого engine/worker.
- [ ] По каждому изменённому вызову вручную проследить envelope encode/decode, auth context, cancellation и resource close; тесты/сборки не запускать согласно ограничению.

### Task 2: Перевести Content на locale-scoped state

**Files:**
- Modify: `engines/content/domain/state.go` и locale/domain files
- Modify: `engines/content/application/*` для workspace, publish, history, restore, copy/fork
- Modify: `engines/content/infrastructure/*` для Core module-data records и CAS
- Modify: `engines/content/presentation/contract.go`, handlers, DTOs
- Modify: Content OpenAPI, Admin `frontend/admin/src/api.ts`, `types.ts`, domain hooks/pages
- Modify: `spec/engines/content/SPEC-content-engine.md` для принятого immutable-history removal guard

**Interfaces:**
- Locale catalog DTO: `supported_locales`, `default_locale`, `revision`; mutations используют expected revision и idempotency согласно текущим API patterns.
- Workspace, publish, history и restore требуют canonical locale; drafts/publications/history хранятся в системном scope per locale; page selections — `(page_snapshot_id, locale)` scope.
- Translation group invariant: стабильный `translation_id`, не более одной записи на locale и одна entity.

- [ ] Добавить BCP 47 canonicalization с IANA Preferred-Value и casing normalization; отклонять invalid tags и дубликаты после canonicalization.
- [ ] Реализовать locale catalog read/write/CAS, требуемую `default_locale` membership и удаление только когда нет draft/publication/history/selection/Router references; immutable history самостоятельно блокирует удаление.
- [ ] Разделить aggregate и storage records на системный locale state и page-locale selections без копирования общих records в snapshot scope; сохранить общий payload ceiling и atomic Core CAS.
- [ ] Ввести locale-aware record/translation validation, singleton initialization per locale/model revision, selection checks и очистку ссылок только внутри той же locale.
- [ ] Реализовать locale-bound workspace save, publish, history paging и restore; публикация/restore фиксируют только выбранную locale, история immutable, restore создаёт draft и не меняет public pointer.
- [ ] Реализовать copy/fork operation для selections всех поддерживаемых locale, не копируя records/publications/history; workflow inputs используют bounded versioned projections.
- [ ] Синхронизировать Content HTTP DTOs/OpenAPI/Admin client и UI с locale catalog и locale selectors; group access и destructive confirmations сохраняются.

### Task 3: Реализовать Website import, artifact build и retirement workflows

**Files:**
- Modify: `engines/website/application/unavailable.go` и Website application services
- Modify: `engines/website/presentation/contract.go`, handlers, DTOs
- Modify: `main/internal/composition/website_component.go`, `components.go` и worker composition
- Modify: `spec/presentation/openapi/website.yaml` и Admin client/Website workflows при необходимости

**Interfaces:**
- Build request/result содержит locale, locale catalog revision, deterministic input digest и immutable artifact manifest Core file ID.
- Import принимает только trusted `source_file_id` и idempotency key; source bytes читаются через Core file/stream capability.
- Retirement workflow фиксирует состояние страницы и вызывает внешний registry fan-out; snapshots/content history/files не удаляются как побочный эффект.

- [ ] Реализовать feature import pipeline поверх Core scoped file read, archive worker, ZIP/manifest validation и идемпотентной записи catalog; partial imports не становятся видимыми.
- [ ] Реализовать feature catalog/version reads из фактических engine records и projections, заменив `ErrUnavailable` только для контрактно поддержанных операций.
- [ ] Реализовать locale-aware build input lock, digest, Coordinator start/get/retry semantics и неизменяемый build result; worker failure сохраняет прежний artifact/route state.
- [ ] Реализовать renderer/prerender pipeline, который проверяет static artifact v1 document/assets, MIME/path/size/checksum, сохраняет каждый объект через Core и пишет manifest последним.
- [ ] Реализовать media derivative/job workflow как Coordinator operation с bounded inputs/outputs и Core artifact persistence.
- [ ] Добавить page retirement contract и durable state transitions, не удаляя Website snapshots/Content data/Core files; registry retirement workflow выполняет отдельно.
- [ ] Подключить Website к Coordinator и необходимым Core capabilities только через typed composition contracts; Loader остаётся ingress, Website не вызывает Loader напрямую.

### Task 4: Реализовать locale-aware Router и trusted publication registry

**Files:**
- Modify: `engines/router/features/routes/model/route.go`, repository and application services
- Modify: `engines/router/presentation/contract.go`, handlers, delivery
- Modify: `main/internal/composition/router_component.go` и новый registry/coordinator-owned package в `main/internal/composition` либо согласованном presentation package
- Modify: `spec/engines/router/SPEC-router-engine.md`, Router/OpenAPI contracts, Admin types/client/UI, только при contract changes

**Interfaces:**
- Route stores configured `default_locale` and locale→artifact targets with per-route CAS revision; replacement affects one locale mapping atomically.
- Trusted publication registry stores server-owned `(route, page)` associations, supports registration, lookup, retirement fan-out and durable per-target result; it accepts no visitor-controlled identifiers.
- Locale preference endpoint validates same-origin return path and writes configured host-only `zubrium_locale` cookie; delivery uses cookie → RFC 4647 Lookup over `Accept-Language` → configured default.

- [ ] Реализовать locale target map/default locale validation and per-locale CAS on route state; failed/stale mutation preserves all prior targets.
- [ ] Реализовать artifact manifest validation and exact route/prefix asset delivery with Core streaming; no page/snapshot interpretation or implicit path/locale fallback in Router.
- [ ] Реализовать locale preference selection, cookie policy from explicit startup config, RFC 4647 Lookup and invalid/unsupported cookie handling.
- [ ] Реализовать trusted route→page registry in external composition; only authorized Admin publication/workflow may mutate associations.
- [ ] Реализовать Coordinator fan-out for `(page, route, locale)` build+CAS activation and retirement, with independent outcomes, idempotency and stale-build rejection.
- [ ] Проверить authorization for Router operations including `router_routes` read on operations listing; keep visitor paths separate from Admin.

### Task 5: Подключить production Archive, Media и Prerender workers с fail-closed isolation

**Files:**
- Modify: `workers/archive/*`, `workers/media/*`, `workers/prerender/*`
- Add: platform-specific sandbox implementation files under `workers/media` or shared worker runtime package
- Modify: worker factories/catalog and `main/internal/composition/*`
- Modify: deployment/runtime docs only where startup prerequisites must be explicit

**Interfaces:**
- Worker factories are static and typed; every child workload receives bounded inputs and scoped capabilities only.
- Linux requires configured namespace/filesystem isolation backend and resource controls; Windows requires AppContainer plus Job Object controls; macOS uses a packaged, signed App Sandbox helper with required entitlements.
- Sandbox startup returns error if helper/backend unavailable, policy cannot be installed, process is not observed sandboxed, or required CPU/memory/process/filesystem/network controls cannot be enforced.

- [ ] Реализовать Archive worker composition с staging directory mode 0700, scoped Core file/index/data and progress capabilities, cancellation and idempotent root result aggregation.
- [ ] Реализовать Linux sandbox adapter, проверяющий наличие и применение требуемого backend/policy, resource/process limits and denied network/filesystem access before accepting workload.
- [ ] Реализовать Windows AppContainer + Job Object supervisor with explicit capabilities, resource limits, process-tree termination and fail-closed startup.
- [ ] Создать macOS helper target/entitlements/signing and launch adapter; verify helper identity/sandbox-active state and fail closed when packaging/deployment prerequisites are absent.
- [ ] Реализовать Media multi-output manifest contract and FFmpeg execution only inside sandbox supervisor; persist derived files via Core capabilities without replacing originals.
- [ ] Реализовать Chromium Prerender supervisor, process-tree/resource lifecycle, deterministic environment and artifact output; remove production reliance on `FailClosedWorker` only after the real factory path exists.
- [ ] Регистрировать factories/catalog before Coordinator startup and permit the worker profile in runnable composition only when all required handlers and sandbox backends are configured.

### Task 6: Интегрировать composition и синхронизировать контракты вручную

**Files:**
- Modify: `main/internal/composition/*`, Admin/OpenAPI/contracts as required by Tasks 1–5
- Existing user-modified `docs/QA_REPORT.md` and `tests/ACCEPTANCE-MATRIX.md`: do not overwrite; no edits unless separately inspected and explicitly necessary

**Interfaces:**
- One product executable in `main/`; modules remain independently composable; all cross-component calls pass through typed bus and static profile allowlist.
- Build/publication/retirement workflows are durable Coordinator workflows; public activation is explicit, route-target CAS is per locale.

- [ ] Wire startup order so Core Coordinator, operation handlers, worker factories, profile allowlist and services are all registered before dispatcher sealing and HTTP readiness.
- [ ] Trace each public API from OpenAPI → HTTP adapter → typed application API → bus operation (where cross-component) → Core capability/Coordinator; resolve mismatches without weakening auth, validation or size limits.
- [ ] Trace error behavior for validation, authorization, CAS conflict, cancellation, partial workflow failure, worker startup failure, stale artifact and locale fallback.
- [ ] Perform source-only review of changed files/diff and confirm no `core/` or forbidden test changes, no bytes/session JSON, no unregistered operation, no fallback isolation, no commit/push.
- [ ] Leave tests/build/lint/browser/E2E unrun and explicitly report this limitation; do not mark scenarios passed or claim production readiness.
