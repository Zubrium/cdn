# Аудит соответствия `spec/` (2026-11-24)

> Исторический срез, не текущая матрица готовности. В текущем коде существуют browser host
> `engines/website/features/runtime/runtime.mjs`, CSV endpoint Forms и логика
> очистки сессий. Это не подтверждает их полную интеграцию и приёмку. Статусы
> и счётчики ниже относятся к времени соответствующих проверок и не должны
> использоваться как оценка текущего checkout без повторной проверки.

## Scope и правила чтения

Проверяемое состояние: ветка `readiness-20261123`, HEAD
`c662e380a5936558610df261f0996154a9a5f7fd`, чистый checkout
`/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-final-c662e38`.
Все нижеследующие baseline-результаты относятся к этому коммиту; более ранние логи
учитываются только как исторический контекст. Требования, затронутые последующими
циклами (`POST /api/locale-preference`, `CONC-008`), обновлены в записях циклов ниже
и ссылаются на более поздние HEAD; baseline-доказательства при этом не переопределяются.

Статусы используются в четырех значениях:

- **Implemented** — код, типизированный контракт и конфигурация существуют, но
  полная самостоятельная приёмка requirements не доказана.
- **Verified** — есть реальный исполненный input/result scenario с зафиксированным
  уровнем (unit, contract, integration, live E2E или static gate). `Verified`
  не автоматически означает полную acceptance всего документа.
- **Blocked** — prerequisite отсутствует или требует отдельного решения;
  `blocked` не считается `pass`.
- **Not implemented** — требуемое SPEC поведение отсутствует или доступно только
  как конфигурационный флаг без runtime-реализации.

Ключевое ограничение: `coverage 67/67 executable = 100.00%` и `83/83 gates` —
это покрытие **исполняемых ledger-сценариев и gates**, а не 100% соответствие
`spec/`. Полный знаменатель unified report — `81` сценарий; `14` из них
остаются `blocked` в статическом прогоне. В integration-прогоне `74/74`
исполняемых сценария прошли, но `7` остаются `blocked`.

## Evidence baseline

| Проверка | Результат | Evidence |
| --- | --- | --- |
| Чистый checkout, `dirty=false`, HEAD `c662e38` | Verified | `output/test-report-release-final-c662e38.json`, `output/test-report-release-final-integr-c662e38.json` |
| `git diff --check`, `make lint-core-boundary`, `make vet`, `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures`, `make build*` | Verified | `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-release-final-c662e38.log` |
| Admin `typecheck`, `lint`, `test`, `build`; QA runner unit tests; Website contract ledger | Verified | тот же лог |
| Unified gates, статический прогон | Verified: `83 pass, 0 fail, 0 blocked` | `output/test-report-release-final-c662e38.json` |
| Unified gates + integration, disposable PostgreSQL/S3 | Verified: `83 pass, 0 fail, 0 blocked`; `74 pass, 0 fail, 0 not_run, 7 blocked`, `74/74 executable` | `output/test-report-release-final-integr-c662e38-integration.json`, `/private/var/folders/.../zubrium-release-final-integr-c662e38.log` |
| Отдельные модульные проверки `core/` и `presentation/router/`: `go test`, `go vet`, `go build` с overlay | Verified | `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-modules-final-c662e38.log` |
| Живой Website/Router E2E, статический sweep | Verified: `PASS E2E-PUB-001`, `PASS E2E-RET-001` | `output/e2e/e2e-20261123T233329Z-681d30/playwright.log` |
| Живой Website/Router E2E, integration sweep | Verified: те же две PASS-строки | `output/e2e/e2e-20261123T234350Z-cdaf0b/playwright.log` |
| Проверка цели перед write-E2E | Verified: listener'ы используют checkout binary и config, build identity совпадает | `output/e2e/e2e-20261123T234350Z-cdaf0b/verify-target.log` |
| Disposable стек PostgreSQL/S3 после integration | Verified: контейнеры, сеть и private env удалены | `/private/var/folders/.../zubrium-final-integr-down.log` |
| Защита рабочих портов | Verified: явно задано `QA_BROWSER_WORKING_PORTS=8080,55432`; список валидируется и не пересекается с disposable портами | `tests/qa/browser/verify-target.sh`, `tests/qa/browser/README.md` |

Порт `5173` на машине занимал Vite другого проекта. Процесс не останавливался;
по явному разрешению пользователя preflight получил ограниченный список рабочих
портов через `QA_BROWSER_WORKING_PORTS`. Проверка владельца процесса, build
identity и функциональных сценариев не была ослаблена.

## 1. Архитектурные SPEC

### `spec/architecture/SPEC-caching.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Инварианты 1–6: cache как подсказка, авторизация до кэша, `revision + access_epoch`, fail-closed, private/ошибки не кэшируются | Implemented | `core/application/cache`, `core/presentation/cache_wiring.go`, `main/internal/composition/components.go`; integration-уровень всей цепочки авторизации не прогонялся |
| Bounded warm-up до Ready, strict/best_effort, retries, refresh, явные границы | Verified | `CACHE-001..003`, `CACHE-011` pass в финальном report; уровень unit с fake dependencies |
| Инвалидация bytes/metadata, reconnect clear, revision/CAS для Router | Verified | `CACHE-005`, `CACHE-006` pass; `core/application/cache`, `core/presentation/maintenance.go`, `presentation/router` |
| HTTP policy: `no-store`, strong ETag, `Vary`, gzip, Range, 304 | Verified | `CACHE-007`, `CACHE-008` pass на contract-уровне; `spec/presentation/openapi` |
| Метрики и отсутствие чувствительных данных в логах | Verified | `CACHE-010`, `CACHE-012` pass; `CACHE-014` дополнительно проверяет operations/gauge для `router.manifests`/`router.routes`; точные label sets через `IncCounterExact`/`SetGaugeExact`/`ObserveHistogramExact` |
| Production `targets.authorization` | Blocked | `CACHE-004` проходит с fake epoch/authorizer, но `main/config.example.yaml` задаёт `authorization: false`; включение требует отдельного согласования замороженного Core и живой epoch-проверки |
| Источник ключей `sources.published_artifacts` | Verified | `resolveWarmupKeys` в `main/internal/config/config.go` деривирует file ID из явных server-owned published artifacts, мержит с inline по file ID, сортирует и обрезает до `max_keys`; валидация в `scales.go` и `CACHE-013` PASS |
| `router_manifests`/`router_routes` как target Core warm-up | Verified | `presentation/router/warmup.go` прогревает оба кэша до Ready через проверенные `manifestCache.read`/`routeCache.resolve`, с gating по флагам и fail-closed по route/locale/file; `CACHE-014` PASS |
| Deploy proxy/CDN obey `no-store`, SSE/form/admin private | Blocked | `CACHE-009 blocked`: нет изолированного proxy/CDN и переменных `CACHE_ROUTER_TEST_*` |

### `spec/architecture/SPEC-concurrency.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Один Router process на branch, без неконтрактных публичных путей и version prefix | Verified | `CONC-012` pass с явным разрешением контрактного `POST /api/locale-preference`; topology/OpenAPI контракт и E2E target ownership pass |
| HTTP/bus admission, fail-fast `503`, per-route override, shutdown/draining | Verified | `CONC-001..003`, `CONC-009`, `CONC-011` pass; уровень unit |
| DB budget и fail-fast до API | Verified | `CONC-004` pass; `main/internal/config/scales.go`; бюджет считает все class limits |
| Sharded LRU, RWMutex, coalescing, zero-copy `cache.View`, invalidation batch | Verified | `CONC-005..007` pass, включая `-race` вCore-cache runner |
| Метрики с точными низкокардинальными label sets | Verified | `CONC-010` pass; regression на точные метки |
| Изолированные class pools `media`/`prerender`/`publication` | Verified | `CONC-008` pass: отдельный `TaskExecutor` на каждый включённый класс, class-scoped `Claim`, непересекающиеся dispatchable handler-карты; отключённые классы сохраняют fail-closed bindings для cross-class детей, но не получают pool; integration с насыщенным `media` и работающими `prerender`/`publication` на disposable PostgreSQL, бюджет проверен `CONC-004` |
| Реальная нагрузка с живым pgxpool и активными пулами | Blocked | ledger-сценарии `CONC-001..004`, `009..011` имеют уровень unit, не load-test |

### `spec/architecture/SPEC-coordinator-platform.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Core хранит нейтральные operation/task/workflow и opaque result | Verified | `core/domain/coordination`, `core/application/coordination`; boundary lint; `CORE-001` integration pass |
| Core не имеет engine/worker kinds, registry и DTO | Verified | `make lint-core-boundary`, `TestCoreDoesNotModelEngineOrWorkerComponents`, `TestRuntimeModulesUseDataBusInsteadOfImportingCore` |
| Generic OperationExecutor и static DataBus allowlist в `main` | Implemented | `main/internal/composition`, `pkg/contracts`; полный independent acceptance не исполнен |
| Class-scoped Claim внутри Core | Implemented | `core/application/coordination/workers.go` передаёт `TaskClasses` в `Claim`; SQL regression проходил на disposable PostgreSQL |
| Изоляция production пулов по class | Verified | `CONC-008` pass; `Service.Pools` и `workerTaskPools` создают независимые executor'ы с общим owner/config epoch, но с собственными `limit`/`poll_interval`/`task_timeout`/`lease_duration` |
| Все durable recovery/cancellation/retention сценарии | Implemented | код Coordinator есть; только `CORE-001` имеет integration evidence в финальном sweep |

### `spec/architecture/SPEC-engine-apis.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Production composition Core/Loader/File/Identity/Coordinator/Router | Verified | `main/internal/composition`, `make build*`, E2E PASS |
| `/api/...`, OpenAPI 3.1, typed client, cookie/CSRF/revision handling | Verified | `tests/go/main/cmd/zubrium/openapi_contract_test.go`, gates |
| Public `core-file` exact path и Loader upload | Implemented | router upload/file contracts, `LOADER-005 blocked` без отдельного live acceptance |
| Website/Content/Forms production factories | Implemented | `main/internal/composition/profile_full_generated.go` включает `engine.website/content/forms`, `components.go` регистрирует их и admin-компоненты в full-профиле; полный independent behavioral acceptance отдельно не прогонялся |
| `POST /api/locale-preference` | Verified | Явная публичная операция скомпилирована в full-профиле, управляется startup-конфигурацией, прошла unit/config/topology/OpenAPI проверки и живой E2E `E2E-LOC-001`; proof в `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-locale-preference-evidence-final/`, исходный run в `output/e2e/` и QA report |
| Router `UpdatePolicy(route_id, policy, expected_revision)` | Not implemented | отсутствует в `presentation/router`, `engines/router` и OpenAPI router |
| Внешний proxy/deployer | Blocked | выбор, конфигурация и admission policy находятся вне репозитория |

### `spec/architecture/SPEC-engine-go-contracts.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Typed facade и нейтральные DTO для Website/Content/Forms | Implemented | `engines/*/presentation`, `pkg/contracts`, boundary lint |
| Forms: глобальные схемы, visitor session, upload, immutable submit, filters | Implemented | `engines/forms`, Admin API; изолированный Forms стек описан в QA report |
| Forms: статусы, CSV, expiry, cancel, cleanup | Not implemented | открыто в `docs/TODO-admin-snapshots-content-files.md` |
| Website/Content workflow,Router locale-specific delivery | Implemented | код и контракты есть; полное поведение не доказано всей приёмкой |
| Полные behavioral acceptance | Blocked | Registration в full-профиле есть, но полного independent browser sweep и приёмки реальных feature builds нет |

### `spec/architecture/SPEC-independent-product-acceptance.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Синтетические feature ZIP: catalog/detail/featured collection | Implemented | `engines/website/features`, `WEB-023..029` host-side |
| Typed bindings, unknown fields/types failure, flat model | Verified | `WEB-015`, `WEB-016`, Website contract gates |
| Independent browser/API sweep с реальными feature builds | Blocked | host-side ABI не заменяет browser runtime, CSP/CORS и реальные ZIP в изолированной среде |
| Полный запуск без рабочих API/credentials/БД | Implemented | E2E runner использует отдельный checkout и disposable PostgreSQL/S3; `verify-target.log` подтверждает цель |

### `spec/architecture/SPEC-modular-platform.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Единственный product executable, `main` composition root | Verified | `main/cmd/zubrium`, все build targets |
| Static profiles, process-local bus, JSON envelope, allowlist | Verified | profile generator, boundary/profile gates, `make test` |
| Core изолирован от engines/workers/Presentation | Verified | `make lint-core-boundary`, `CORE-001` |
| Typed operation contracts в `pkg/contracts`, HTTP не принимает operation ID | Verified | contract gates, OpenAPI parity |
| Полный independent product acceptance | Blocked | см. `SPEC-independent-product-acceptance.md` |
| SPEC-заголовок «реализация boundary ещё требует миграции» | Implemented | расхождение: код и gate уже демонстрируют миграцию; требуется синхронизация текста SPEC владельцем |

### `spec/observability/SPEC-observability.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Единая identity, W3C trace context, typed observer | Implemented | `pkg/observability`, `main/internal/composition` |
| Correlation headers, healthz без log, worker identity, redaction | Verified | `OBS-001..004`, `OBS-007`, `OBS-009` pass |
| File mode: JSONL logs/spans/metrics, ротация | Verified | `OBS-005` pass |
| Graceful shutdown и trace isolation | Verified | `OBS-010` pass |
| OTel + Collector + OpenObserve, три потока с общим identity | Blocked | `OBS-006 blocked`: harness ожидает неаутентифицированный GET, OpenObserve v1.1.2 требует POST `/api/default/_search`; переменные `OBS_TEST_*` не заданы |
| Устойчивость при отказе Collector | Blocked | `OBS-008 blocked`: нужен управляемый отказ и совместимый поиск |

### `spec/workers/SPEC-workers.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Neutral DTO, typed operation identity, no direct Core/engine calls | Verified | boundary lint, `pkg/contracts`, worker handlers |
| Archive: ZIP/traversal/symlink/limits/digest/atomic output | Verified | `WEB-012` integration pass |
| Media: manifest-pinned profile, processor version, groups/FileSet, sandbox | Verified | `WEB-019` integration pass с Docker/Linux sandbox |
| Prerender: strict plan, trusted digest, network deny, stale rejection | Verified | `WEB-020` integration pass с headless Chromium |
| Production component registration | Implemented | `main/internal/composition` регистрирует `worker.archive`, `worker.media`, `worker.prerender`; SPEC-заголовок об отсутствии registration расходится с кодом |
| Изолированные class pools и полный lifecycle приёмки | Verified | `CONC-008` pass: активация после Ready engine'ов, class-scoped claim/execute/complete, shutdown; durable failure/recovery сценарии остаются отдельной волной `CORE-001` |

## 2. Core SPEC

### `spec/core/SPEC-core-architecture.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| DDD-слои под `core/`, без `core/internal` | Verified | структура модуля, module checks |
| Core владеет PostgreSQL/S3/cache; внешние модули получают DataBus clients | Verified | `make lint-core-boundary`, `CORE-001` integration pass, module checks |
| User без `kind`/role; levels 0/1/2; `can_manage_administrators` | Implemented | Core identity/access контракты и Admin guards; полная live permission matrix не прогонялась |
| Files immutable, FileSet только index, Loader владеет upload workflow | Implemented | Core file/Loader контракты; `LOADER-005 blocked` |
| Credential-bound sessions, digest token, HttpOnly cookie + CSRF в Presentation | Implemented | `core/identity/session`, Presentation middleware; отдельный browser permission sweep не полный |
| Fresh schema, Atlas-only DDL, DML-only grants | Implemented | `core/infrastructure/postgres/migrate.go`, historical fresh-DB evidence; отдельный Atlas CLI прогон отсутствует |
| SPEC-заголовок «текущая реализация ещё нарушает границы» | Implemented | расхождение с зелёным boundary gate; требуется синхронизация владельцем SPEC |

### `spec/core/SPEC-module-data.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| `(engine_id, namespace, scope_type, scope_id, record_key)`, CAS, TTL | Implemented | `core/infrastructure/postgres/module_data.go`, `core/domain/engine_data` |
| Selectors, GIN/keyset pagination, bounds, batch in one owner namespace | Implemented | Core application/infrastructure, Website/Content tests |
| Scope isolation и отказ неизвестной capability | Implemented | composition-bound owner scope, `pkg/contracts`, boundary lint |
| Expiry cleanup и полный набор integration сценариев | Blocked | отдельного executable ledger для module-data нет; только смежные `CORE-001`/Website scenarios |

## 3. Content и Forms

### `spec/engines/content/SPEC-content-engine.md` и `SPEC-content-editor-acceptance.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| System-scoped records/drafts/publication/history по locale | Implemented | `engines/content`, Admin Content API |
| Отдельные record ID перевода и общий `translation_id` | Implemented | Content domain/repository |
| Locale catalog revision/default validation | Implemented | Content Admin handler, contract tests |
| Page/locale-scoped selections/order, fork копирует только selections/order | Implemented | Content engine, snapshot fork E2E |
| Publication fan-out per `(route, locale)`, partial success/retry | Verified | `WEB-021` integration pass и `E2E-PUB-001` live pass |
| Draft-only данные не попадают в public runtime | Verified | publication contract/E2E assertions |
| Полный live Content acceptance | Blocked | `CONTENT-005` требует `CONTENT_TEST_*` и изолированного Admin/public API |
| Browser destructive/unsaved/keyboard matrix | Implemented | UI-POL browser QA исторического стека; полный текущий Content deep QA заблокирован `UI-POL-011` |

### `spec/engines/forms/SPEC-forms-engine.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Глобальные схемы `key + fields`, version/CAS, только `forms_schemas` | Implemented | `engines/forms`, migration/contract tests |
| Типизированные field constraints и независимые `forms_submissions` | Implemented | Forms validation/handlers |
| Visitor session, cookie transport, upload attachments, idempotent submit | Implemented | Forms public contracts, Router transport |
| Immutable accepted submission, filtered submissions by `form_id`/`key` | Verified | isolated-stack scenarios в QA report |
| Admin `/forms`, `/forms/new`, `/forms/:formId`, editable base path | Verified | browser QA в изолированном стеке |
| CSV filters/escaping/limits, expiry/cancel, удаление с cleanup | Not implemented | остаётся открытым в TODO |
| Live Forms e2e | Blocked | `FORMS-005` требует `FORMS_TEST_*` |

## 4. Router SPEC

### `spec/engines/router/SPEC-router-engine.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Router v2 без `snapshot_id`/`page_id`, только готовые artifacts/operation | Implemented | `engines/router`, `presentation/router`, OpenAPI |
| Static operation registry, unknown operations запрещены | Verified | profile gates, router contract tests |
| Exact matching/normalization, reserved `/api/admin/`, visitor не выбирает IDs | Verified | `TestFindMatchSkipsSiblingsThatArePrefixesOfTheRequest`, contract gates |
| Bounded manifest/route cache с revision fail-closed | Verified | `CACHE-006` pass |
| `public.content-runtime` GET/HEAD, server-owned locale/snapshot, no-store, ETag/304 | Implemented | Router public contract tests |
| Locale resolution и preference cookie endpoint | Verified | `POST /api/locale-preference` скомпилирован и защищён валидацией return_path/locale; cookie host-only HttpOnly SameSite=Lax с явным lifetime; `E2E-LOC-001` PASS |
| Settings API `UpdatePolicy` | Not implemented | операция отсутствует |
| Атомарная публикация route target CAS | Verified | `WEB-021`, `E2E-PUB-001` |
| Anonymous Loader upload, server-configured limits/staging | Implemented | Router/Loader wiring; `LOADER-005` live acceptance blocked |
| Внешний proxy/CDN и rate limiting deployer | Blocked | вне репозитория |

## 5. Website SPEC

### `spec/engines/website/SPEC-website-engine.md`, `SPEC-branches-snapshots.md`, `SPEC-data-model.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Server-generated page UUID, независимые snapshot trees по `page_id` | Implemented | `engines/website`, snapshot fork/persistence tests |
| Root snapshot, immutable parent, child on structural change, revision CAS | Verified | Website ledger и E2E setup/fork |
| System-wide dataModel, page-scoped placements/bindings, без route/URL | Implemented | Website engine and `/site` contracts |
| Feature ZIP strict manifest, typed `dataInputs`, dependency lock | Implemented | `engines/website/features`, `WEB-023..029` |
| Artifact deterministic `static-page-artifact/v1` | Verified | `TestWebsiteScenarioArtifactValidation`, contract gates |
| Durable rebuild fan-out, independent `(route, locale)` CAS, stale discard | Verified | `WEB-021`, `E2E-PUB-001` |
| Production Website factory / полный HTTP API | Implemented | компоненты регистрируются в full-профиле (`profile_full_generated.go`, `components.go`), реальный E2E через trusted orchestration; SPEC-заголовок об отсутствии регистрации устарел |
| Полный independent browser acceptance реальных features | Blocked | host-side ABI/контракты не заменяют browser runtime |

### `spec/engines/website/features/SPEC-feature-runtime.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Только `static-lifecycle`, неизвестный ABI fail-closed | Verified | `WEB-016`, feature runtime contract tests |
| `render/hydrate/update/dispose`, cancellation/timeout/dispose, placement isolation | Verified | host-side `WEB-016` |
| Manifest-declared HTTPS origins, typed requests, CSP/CORS | Implemented | host-side validation; реальный browser network sweep не прогонялся |
| Встроенный browser host | Not implemented | SPEC требует, host-side runtime его не заменяет |

### `spec/engines/website/SPEC-static-site-artifact.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Manifest schema, paths/content rules, deterministic validation | Verified | artifact validation contract tests |
| Immutable build/output, CAS, stale result rejection | Verified | `WEB-021` integration pass |
| Delivery exact document/assets, unknown assets rejected | Implemented | Router artifact delivery contract |

### `spec/engines/website/SPEC-public-server-rendering.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Один `<main>`, contextual escaping, hydratable initial HTML, no SPA fallback | Implemented | `html/template` renderer, `WEB-015` contract |
| Status 404/500/503, no-store/noindex на ошибках | Implemented | system-pages renderer; browser/route-level 403/503 ограничены |
| Compression/Vary/ETag/Range | Verified | `CACHE-007`, `CACHE-008` contract |
| Реальный browser hydration и CSP для feature runtime | Blocked | нет полного browser feature sweep |

### `spec/engines/website/SPEC-seo.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Canonical origin, language, noIndex, title/description/OG | Implemented | Website metadata контракты |
| `robots.txt`, sitemap, detail metadata, X-Robots-Tag | Implemented | SEO handlers/tests |
| Sitemap limits и conditional GET/HEAD | Implemented | SEO contract tests |
| Browser prerender metadata parity | Blocked | host-side tests не доказывают runtime browser parity |

### `spec/engines/website/SPEC-site-editor-workspace.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Dynamic pages/template library, model and placement editing | Implemented | Admin `/site` workflows |
| Record editing/publication, shared draft/save guards | Implemented | Admin Content/Site UI tests |
| Acceptance browser matrix 360–1920, themes, keyboard | Verified | UI-POL browser QA исторического стека |
| Полный workflow на текущем HEAD с реальными features | Blocked | `UI-POL-011` и CONTENT-005 |

### `spec/engines/website/SPEC-design-tokens.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Design token v2, typed values, GUID, group/feature uniqueness | Implemented | Website design system contracts/tests |
| Breakpoint inheritance, legacy values, parent comparison | Implemented | Admin design editor |
| Графический редактор без CSS/JSON/имен переменных | Implemented | Admin UI, `frontend/ui/styles/admin.css` |
| Runtime CSS/public theme acceptance на реальных значениях | Blocked | исторические проверки покрывали чтение/отображение, не полный runtime CSS sweep |

### `spec/engines/website/SPEC-system-pages.md`

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Core 404/500/503 documents, one main, viewport/language, noindex/no-store | Verified | историческая functional/browser acceptance на disposable PostgreSQL |
| Snapshot override 404/500 при concrete route | Implemented | systemPages config/resolution |
| Route-level 403 и 503 mappings | Not implemented | SPEC прямо фиксирует: config keys распознаются, но normal public routing их не вызывает |

## 6. Остальные модульные SPEC

### `spec/workers/SPEC-workers.md`

См. раздел 1: handlers и registration **Implemented/Verified** по `WEB-012/019/020`,
class-isolated pools **Verified** (`CONC-008`, integration evidence на disposable PostgreSQL).

### Loader / File API

| Требование | Статус | Основание и ограничение |
| --- | --- | --- |
| Resumable upload, checksum/idempotency, staging recovery | Implemented | `engines/loader`, Core staging |
| Per-group authorization, immutable bytes, FileSet membership без bytes | Implemented | Core file/access contracts |
| Restart/recovery live acceptance | Blocked | `LOADER-005` требует отдельного disposable uploader environment |
| Public form upload через Router | Implemented | Router+Loader wiring; live e2e отдельно не прогонялся |

## 7. Итоговая оценка

### Что Verified

- Полный release sweep и живой Website/Router E2E на чистом checkout `c662e38`.
- Integration-срез `website-integration` и `core-integration`: `2/2` gates, `74/74` executable scenario pass.
- Executable ledger'ы OBS/CACHE/CONC/Core-cache в объёме, зафиксированном unified report.
- `CORE-001`, `WEB-012`, `WEB-018..022` на disposable PostgreSQL/S3.
- `CONC-008` на отдельном disposable PostgreSQL: насыщенный `media` не блокирует `prerender`/`publication`; budget/конфигурация классов проверены fail-fast тестами.
- `E2E-PUB-001`, `E2E-RET-001` с точными PASS-строками и проверкой target ownership/build identity.

### Что Implemented, но не полностью принято

- Core/cache/concurrency контракты и внутренние механизмы.
- Typed DataBus contracts и изоляция Core от runtime-модулей.
- Website/Content/Forms код и Admin workflows.
- Router v2 API, route/cache/artifact delivery и feature `static-lifecycle`.
- Observability file mode и большая часть OTel-инструментации.

### Что Blocked

- `CACHE-009`: изолированный caching proxy/CDN.
- `OBS-006`, `OBS-008`: harness OpenObserve v1.1.2 и управляемый отказ Collector.
- `CONTENT-005`, `FORMS-005`, `LOADER-005`: отдельные live e2e environment.
- Полный independent browser/real-feature sweep и runtime CSS acceptance.
- Deployer-owned proxy, rate limiting и cache policy enforcement.

### Что Not implemented

- Resolver `sources.published_artifacts` и реальный warm-up targets `router_manifests`/`router_routes` в Core runner.
- Router `UpdatePolicy`.
- Forms statuses/CSV/expiry/cancel/cleanup.
- Route-level 403/503 system page overrides.
- Встроенный browser host feature runtime.

### Вердикт

Проект имеет зелёный release/E2E контур и прочную реализационную базу, но
**100% соответствие всему `spec/` не подтверждено**. Честный результат —
частичное соответствие с явно зафиксированными `Implemented`, `Verified`,
`Blocked` и `Not implemented` требованиями. `coverage 100%` относится только к
исполняемым ledger-сценариям и не должен использоваться как функциональный
показатель SPEC.

### Запись цикла: `POST /api/locale-preference`

- SPEC ID: `POST /api/locale-preference` в `spec/presentation/openapi/router.yaml`, locale resolution и preference cookie в `spec/engines/router/SPEC-router-engine.md`, публичная топология `CONC-012`.
- Изменённые файлы: полный состав зафиксирован коммитом `107bbde`, Evidence/HEAD/проверки — в [QA report](QA_REPORT.md) и [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md).
- Новая возможность: скомпилированная в full profile публичная операция с явной startup-конфигурацией, валидацией locale/return_path/route/branch/state, cookie `zubrium_locale` (`HttpOnly`, `SameSite=Lax`, host-only, явный lifetime) и `303` на return_path; Admin listener не регистрирует путь.
- Тесты: `tests/go/presentation/router/locale_preference_test.go`, `tests/go/main/internal/config/locale_preference_test.go`, topology/OpenAPI regression, ledger `E2E-LOC-001` и live Playwright.
- Результат: PASS. Живой proof — `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-locale-preference-evidence-final/`; исходный run `e2e-20261124T121627Z-bab896` в `output/e2e/`, точные строки `PASS E2E-PUB-001`, `PASS E2E-LOC-001`, `PASS E2E-RET-001` без `FAIL`. Завершающие статические проверки перечислены в QA report и прошли exit 0.
- HEAD после коммита и пуша: `107bbde` (ветка `readiness-20261123`).
- Счётчики статусов в этом документе: до цикла `49 Verified / 53 Implemented / 19 Blocked / 13 Not implemented`; после `51 Verified / 53 Implemented / 19 Blocked / 11 Not implemented`.
- Оставшиеся блокеры цикла: внешний proxy/CDN и rate limiting (`CACHE-009`), отдельные live environment для `CONTENT-005`, `FORMS-005`, `LOADER-005`, `UI-POL-011`, а также `OBS-006/008`; они не затронуты этой реализацией.

### Запись цикла: `CONC-008`

- SPEC ID: `CONC-008` в `spec/architecture/SPEC-concurrency.md` и разделы 9–11 того же документа; смежные проверки `CONC-004` (бюджет соединений) и `CONC-012` (топология без динамических handlers).
- Изменённые файлы: код активных пулов — `core/application/coordination/service.go`, `core/presentation/coordinator.go`, `main/internal/composition/{components.go,task_executor.go,worker_handlers_full.go,worker_handlers_disabled.go}`, `main/internal/config/{config.go,scales.go}`, example YAML; regression-тесты — `tests/go/core/application/coordination/{service_test.go,workers_test.go}`, `tests/go/core/infrastructure/postgres/coordinator_test.go`, `tests/go/main/internal/composition/{task_executor_test.go,profile_full_test.go}`, `tests/go/main/internal/config/concurrency_budget_scenario_test.go` и обновлённые acceptance call sites.
- Новая возможность: каждый включённый worker class получает отдельный `TaskExecutor` с собственными `limit`, `poll_interval`, `task_timeout` и `lease_duration`, общим owner/config epoch и class-scoped `Claim`; dispatched handler-карты непересекаются. Отключённый класс не создаёт pool и не может claim/complete чужую задачу, но сохраняет fail-closed registry binding, поэтому cross-class ребёнок остаётся в durable store до явного включения пула. Legacy `coordinator.task_lease/task_timeout/poll_interval` и `executor_concurrency` удалены; невалидные параметры любого пула отвергаются до старта, включая отключённые пулы и не-worker профили.
- Тесты: RED/GREEN `TestCoordinatorServiceActivatesIsolatedClassPools`, `TestCoordinatorServiceIsolatesSaturatedWorkerPool`, `TestCoordinatorServiceFailedActivationIsTerminal`; composition `TestClassScopedDataBusTaskExecutorRejectsForeignClass`, `TestClassScopedDataBusTaskExecutorResolvesCrossClassChildren`, `TestFullProfileDisabledClassKeepsResolvableFailClosedBinding`; config `TestConcurrencyClassPoolScenario/CONC-008`; live integration `TestDisposableIsolatedWorkerPoolsRunIndependently`.
- Результат: PASS. Integration на отдельном disposable PostgreSQL (`conc008-final2`, БД `zubrium_core_qa_isolated_pools_conc008_final2`) подтвердил, что насыщенный `media` с `Concurrency=1` не блокирует два `prerender` и один `publication`; соседние пулы не превышают лимиты; `media` дренируется после снятия блокировки; стек, сеть и private env удалены. Статические проверки: `git diff --check`, `python3 -m unittest tests/qa/test_scenario_ledger.py`, `bash -n tests/qa/run-website-router-e2e.sh`, `make lint-core-boundary`, `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, compile-only `full website_acceptance`, `go test -race` в `core/application/coordination`, Admin `typecheck/test/build/lint` — exit 0. Логи: `output/conc008-20261124-final/`.
- Evidence: `output/conc008-20261124-final/integration.log` содержит точные строки `=== RUN TestDisposableIsolatedWorkerPoolsRunIndependently` и `--- PASS`; `coordination-race.log`, `test-full.log`, `admin.log` и остальные логи зафиксированы там же. Секреты в логи не попали: DSN в логе заменён на redacted.
- HEAD кода после amending: `ed3ec076d8ba59d72113df93170e13ae0f2071f1` (ветка `readiness-20261123`); этот документ и матрица фиксируются отдельным evidence-коммитом.
- Счётчики статусов в этом документе: до цикла `51 Verified / 53 Implemented / 19 Blocked / 11 Not implemented`; после `54 Verified / 53 Implemented / 19 Blocked / 8 Not implemented`.
- Оставшиеся ограничения: `CONC-001..004` и `CONC-009..011` остаются unit/contract-level проверками без нагрузочного p99-прогона; `CACHE-009`, `OBS-006/008`, live environment для `CONTENT-005`, `FORMS-005`, `LOADER-005`, `UI-POL-011` и deployer-owned proxy не затронуты этим циклом.

### Запись цикла: источник `published_artifacts` и Router-таргеты прогрева

- SPEC ID: `sources.published_artifacts`, `router_manifests` и `router_routes` в `spec/architecture/SPEC-caching.md` §5.3–5.6, §7–9; исполняемые сценарии `CACHE-013` и `CACHE-014`.
- Изменённые файлы: `main/internal/config/{config.go,scales.go}`, `main/config.example.yaml`, `main/config.observability.yaml.example`, `main/internal/composition/{components.go,router_component.go,router_cache_warmup.go}`, `presentation/router/{cache.go,delivery.go,warmup.go}`, `engines/router/presentation/contract.go`; regression-тесты `tests/go/main/internal/config/{cache_warmup_source_test.go,config_test.go}`, `tests/go/main/internal/composition/cache_warmup_test.go`, `tests/go/presentation/router/cache_scenarios_test.go`; ledger'ы и документы.
- Новая возможность: file ID деривируются из явных server-owned published artifact slot-ов до `platform.Start`; Core-ключи мержатся по file ID, сортируются и обрезаются до `max_keys`, а дубли, неявные списки, дубли principals/actions и списки, превышающие `max_keys`, отвергаются до старта. Router прогревает `router.manifests` и `router.routes` внутри `routerEngineComponent.Start` до Ready через проверенные `manifestCache.read`/`routeCache.resolve`, с gating по startup-флагам, fail-closed по route/locale/file identity, `SKIPPED` для выключенных целей и разделением required/optional ошибок. Фоновый refresh не перекрывает циклы и останавливается в `Stop`; Runtime-кэши Router публикуют операции и gauge, а все метрики используют только метки `cache`, `tier`, `result`.
- Тесты: RED на отсутствующий resolver, gating и observer-адаптер; GREEN `TestWarmupSourceScenarios/CACHE-013`, `TestResolveDerivesPublishedArtifactWarmupKeys`, `TestResolveRejectsInvalidPublishedArtifactSources`, `TestRouterWarmupContractFromResolvedConfig`, `TestRouterCacheObserverUsesExactLowCardinalityLabels`, `TestCacheRouterScenario/CACHE-014` с подсценариями primed/gated/refresh/identity.
- Результат и commands: зафиксированы в [QA report](QA_REPORT.md), логи — в `output/cache-20261124-final/`.
- Ограничения: `targets.authorization` остаётся `false` до отдельного согласования `access_epoch` в замороженном Core; `CACHE-009` и внешние live environment не затронуты. Основной unified integration-прогон прошёл `83/83` gates и `70/70` executable сценариев; дополнительная фаза `--integration --only website-integration --only core-integration` не пройдена из-за нехватки места на диске при линковке и повторно использованной disposable БД с уже существующей Core-схемой — это ограничение окружения, а не кеш-цикла.
- Счётчики статусов в этом документе: до цикла `54 Verified / 53 Implemented / 19 Blocked / 8 Not implemented`; после `56 Verified / 53 Implemented / 19 Blocked / 6 Not implemented`.
