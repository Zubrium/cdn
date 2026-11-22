# QA-отчёт

## Текущий проход — durable Website build, integration и browser QA (2026-11-22)

### Подтверждённые проверки

- `python3 tests/qa/run-test-suite.py ci --integration --report output/test-report-web012.json`: exit 0; `gates: 67 pass, 0 fail, 0 blocked`; `scenarios: 26 pass, 0 fail, 4 not_run, 0 blocked`; `coverage 26/26 executable = 100.00%`. Полный `tests/qa/run-website-red.sh integration` на одноразовых PostgreSQL/S3: exit 0, включены REAL `WEB-012`, `WEB-018`, `CORE-001`. `WEB-019`, `WEB-020`, `WEB-021`, `WEB-022` остаются blocked и исключены только из знаменателя executable coverage.
- `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.qa.test_run_test_suite tests.qa.test_scenario_ledger`: 28 тестов, OK. `python3 tests/qa/scenario-ledger.py tests/qa/website-scenarios.json`: `denominator: 29`, `covered: 25` (86.21%), `gaps: 0`, `blockers: 4`, `execution_pass: 25`. Core ledger: `1/1`, 100.00%.
- `tests/qa/run-website-red.sh contract`: exit 0 (ledger validator, Go contract-сценарии, `TestWebsiteAdminHTTPAcceptanceWEB017`, отдельный Vitest UI-файл). `PYTHONDONTWRITEBYTECODE=1` используется для локальных unittest-прогонов, чтобы не создавать `__pycache__` в исходниках.
- RED/GREEN-тесты durable build и owner-scoped result прошли с централизованным overlay: `TestWebsiteBuildPageHandlerEmitsPinnedPrerenderChild`, `TestWebsiteBuildPageHandlerRejectsInvalidTaskAndPayload`, `TestWebsiteBuildWorkflowResolvesPrerenderEmissionKey`, `TestWebsiteBuildWorkflowFailsWhenPrerenderEmissionCannotBeResolved`, `TestWebsiteLocaleCatalogPinsRevisionOnlyForConfiguredLocale`, `TestWebsiteBuildWorkflowResolvesOnlyOwnedEmissionKey`, `TestDisposableOwnerScopedTaskResult/CORE-001`.
- Все 19 модулей `go.work` по отдельности прошли `go test ./...`, `go vet ./...`, `go build ./...`; `main` дополнительно прошёл `go test/vet/build -tags=full ./...`, включая компиляцию root handler `engine.website/build-page/v1` и fail-closed child handlers.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `pnpm lint`, `pnpm --filter @website-engine/admin test` (48 файлов, 218 тестов) и `pnpm --filter @website-engine/admin build` — PASS. Build перезаписал `pkg/static/dist` и ассеты.
- `make lint-core-boundary`: exit 0. `git diff --check`: exit 0.
- Browser QA выполнен в отдельном checkout с build identity current run; disposable PostgreSQL и `moto` S3 использовались только для изолированного стека. Admin: `/admin`, `/admin/content`, `/admin/forms`, `/admin/submissions`, `/admin/media`, `/admin/coordinator/workflows`, `/admin/router/routes`, `/admin/site/pages`, `/admin/snapshots/registry`, `/admin/snapshots/tree`, `/admin/settings`, `/admin/access/users` — desktop 1440/light, без горизонтального overflow. Responsive 360/390/768/1024/1280/1920 px на репрезентативных экранах, темы system/light/dark, цикл `ThemeToggle`, видимый фокус по первому `Tab`, диалоги с закрытием по `Escape`, переключатель `Сетка/Список` — проверены. `/healthz` отдаёт ожидаемый `X-Zubrium-Build-Identity`. Публичный listener на проверенных путях отдаёт `404 {"error":"not_found"}` и не экспонирует `/healthz`, `/readyz` или Admin session.
- Write-проверка browser QA выполнялась только в изолированной disposable БД: создание страницы через Admin API вернуло `201`, последующее чтение `/api/admin/pages`, `/api/admin/snapshots` и `/api/admin/snapshots/{id}` вернуло `200`; страница отрисовалась в редакторе. Credential и приватные env в отчёт не попадают.

### Фактическое состояние durable build

- Core предоставляет owner-scoped `WorkflowClient.GetTaskResult`; владелец, instance lease и config epoch берутся только из сконструированного клиента. `CORE-001` подтверждает базу данных, миграции и owner-scoped чтение результата в живом PostgreSQL.
- `engines/website/workflow` регистрирует immutable root-операцию `engine.website/build-page/v1`, проверяет strict typed payload и эмитит child `worker.prerender/render-page/v1` с `render-plan/v1`, `plan_digest` и стабильным `prerender:<build_id>` emission key.
- `main/internal/composition` инъектирует workflow adapter, locale catalog из доверенного Content service context и константы операции/версии; в не-full профилях capability не инъектируется. Условный порядок ребер full profile учитывает `admin.files → engine.forms/engine.website/engine.router` и `engine.router → engine.forms/engine.content`.
- Website разрешает сгенерированный Core `Task.ID` по owner-scoped emission key, закрепляет его в `website/builds` и читает результат child только через `GetTaskResult`. Artifact manifest фиксируется в job только после typed validation.
- `WEB-012`: `BeginFeatureImport` фиксирует CAS-запись и idempotency receipt по request-only fingerprint, стартует owner-scoped `feature-import/v1` с минимальным payload и reconcile-ит durable результат. Feature version становится `ready` только после проверенного результата archive worker и подтверждённого удаления source file; повтор task не создаёт дублирующих output. Настоящий `archive.Handler` включается только при явной trusted-конфигурации full profile, остальные профили остаются fail-closed.

### Найденные и исправленные дефекты текущего прохода

- **Full profile:** кондиционально упорядоченные ребра и созданный до финальной активации `NewWorkflowClient` устранили отказ живой композиции; компоненты без асинхронных ошибок получили открытый failure stream вместо ошибочно фатального закрытия канала.
- **Admin notifications:** same-origin `EventSource` не шлёт `Origin`; добавлен `TrustedReadOrigin` с проверкой `Sec-Fetch-Site: same-origin` и разрешённого `Referer`, write-запросы по-прежнему требуют точный `Origin` и CSRF. SSE держится открытым без console errors.
- **Website persistence:** `ListSnapshots` без `page_id` теперь возвращает снимки всех page trees вместо пустого результата; добавлен `TestListSnapshotsWithoutPageFilterListsEveryPageTree`.
- **Website/Core revisions:** декодеры Website сравнивали локальный `index.revision` с глобальным `engine_data.revision_seq`, из-за чего созданная страница возвращала `201`, но последующие чтения давали `503`. Инвариант заменён на проверку положительной ревизии в index; добавлен `TestCreatePageSurvivesCoreGlobalRevisionSequence` с эмуляцией глобальной sequence.
- **Site editor payload:** пустая страница создавалась с `metadata: {}`, который Admin отвергал как несоответствующий page-snapshot контракту; пустой payload теперь содержит `formatVersion: 1` и `siteName`.
- **Admin responsive:** `span.sr-only` в action column таблицы расширял layout viewport до `innerWidth=503` при `client=390`; `.table-wrap` получил `position: relative`, повторная проверка на 390 px дала `scroll=390`, `inner=390`.
- **Admin content:** запрос locale config больше не выполняется без выбранного snapshot, что убирает ожидаемый `404 /api/admin/content/locales` на пустом экране.

### Ограничения текущего прохода

- `WEB-019`, `WEB-020`, `WEB-021`, `WEB-022` остаются `blocked`: нет supervised media isolation, prerender sandbox/network policy, outer orchestration с per-`(route, locale)` Router CAS и page retirement workflow. Эти статусы не должны трактоваться как pass. `WEB-012` исполнен на disposable prerequisite и переведён в `pass`.
- `moto` выбран для browser QA, потому что MinIO в этом стеке возвращает `GetPublicAccessBlock ... NotImplemented`, а Core `RequirePrivate` требует этот API. Реальные S3-провайдеры в browser QA не использовались.
- `GET /api/admin/model → 404 {"error":"not_found"}` и `GET /api/admin/features → 503 {"error":"unavailable"}` остаются ожидаемыми для пустого реестра; UI показывает корректные empty/error состояния. Это не считается пройденной приёмкой соответствующих модулей.
- `/admin/coordinator/workflows` один раз завис при быстром sequential navigation; свежий reload, network и snapshot прошли чисто. Явный product-дефект не воспроизведён, случай зафиксирован как незавершённый harness/browser flake.
- Реальный supervising prerender sandbox, Chromium/network policy, Router activation, media supervisor, crash/recovery и page retirement не прогонялись. Host-side durable build тесты не заменяют полный live E2E.
- Release checks и полный cybersecurity audit в этом проходе не выполнялись; blocked сценарии остаются видимыми в отчёте и `diagnose`.

## Текущий проход — изоляция `main`, профили и runtime-интеграции (2026-11-21)

### Подтверждённые проверки

- `make lint-core-boundary` PASS: `Architecture boundaries OK: Core, cross-engine, runtime bus and compiled profiles checked across 19 repository-local Go modules.` Линтер в `tests/linter` проверяет импорты, stale/невалидные YAML-профили и сгенерированные файлы, прямой вызов raw `Dispatch` вне `main`, а также новые внутренние импорты engines из внешнего Presentation (для четырёх существующих legacy-импортов сохранён явный baseline).
- Из корня PASS: `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-full`, `make test-router`, `make test-dev-fixtures`. Все 19 модулей `go.work` отдельно прошли `go test ./...`, `go vet ./...` и `go build ./...`.
- Новые package-тесты PASS: `pkg/contracts`, `main/internal/bus` (scoped streams, consumed/closed refs, workflow operations, published-content operation), `main/internal/composition` (Content projection, profile composition, workflow wiring, чтение Content только через typed bus operation), `presentation/router/publiccontentruntime` (GET/HEAD, ETag/304, server-side route config, отказ visitor input).
- OpenAPI `spec/presentation/openapi/router.yaml` и `tests/ACCEPTANCE-MATRIX.md` синхронизированы с `public.content-runtime`: только GET/HEAD, `page_snapshot_id`/`locale` из server config, strict config schema без неизвестных полей, `no-store`, quoted strong ETag и 304 по `If-None-Match`. Контрактный тест Router прошёл в составе `make test`.
- Публичный Content runtime не хранит прямой ссылки на Content engine: Router-side adapter вызывает запечатанный dispatcher через статическую операцию `content.published.read`; allowlist операции включается только при выбранном `public.content-runtime`.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — PASS.

### Ограничения текущего прохода

- Worker handlers скомпилированы и выбираются профилем, однако archive/media/prerender остаются fail-closed без server-side execution configuration; реальная изоляция Media runner и prerender pipeline не реализованы в этой задаче.
- Full-профиль проходит сборку и статические тесты композиции, но не прогонялся против живого PostgreSQL/S3 и реальных feature builds. Trusted Content projections проверены unit-тестами на stub Website API.
- `pnpm lint` в `frontend/` падает до проверки кода: отсутствует каталог `pkg/runtime/scripts`. Прямой `npx eslint --config eslint.config.js admin` даёт две существующие ошибки `react-hooks/exhaustive-deps` в `MediaPickerDialog.tsx` и `MediaPage.tsx`; ESLint-конфигурация вне зоны изменений.
- Integration-, browser- и release-проверки, а также disposable PostgreSQL/S3 acceptance в этом проходе не запускались; их статус не меняется на `pass`.
- Коммит и push выполняются только по прямому запросу пользователя после проверки `git status`.

## Ранее зафиксированный проход — удаление мультисайтности (2026-11-21)

### Подтверждённые проверки

- Все 19 модулей `go.work` отдельно прошли `go test ./...`, `go vet ./...` и `go build ./...`: `core`, `engines/*`, `workers/*`, `presentation/*`, `pkg`, `main`.
- Из корня повторно прошли `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures`, `make lint-core-boundary`.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — PASS.
- OpenAPI-контракт и маршруты: `TestOpenAPIOperationsMatchRegisteredHTTPHandlers` проходит; `spec/presentation/openapi/website.yaml` содержит только плоские `/api/admin/...` пути без site-сегмента.
- `python3 tests/qa/scenario-ledger.py tests/qa/website-scenarios.json`: 29 сценариев `WEB-001..WEB-029`, `covered: 14`, `execution_pass: 11`, `execution_fail: 3`, `not_run: 9`, `blocked: 6`.
- `tests/qa/run-website-red.sh contract`: Go-часть (ledger + contract-сценарии) PASS.
- Поиск `site_id|siteID|SiteID|site_ids|/api/admin/sites|sites.yaml` по репозиторию (кроме `node_modules`/`dist`) не дал следов в коде, контрактах и сценариях; в конфиг-тесте осталась только негативная проверка, что `website.site_ids` больше не принимается.

### Ограничения текущего прохода

- `tests/qa/run-website-red.sh unit`: 11 сценариев pass, 3 сценария fail — `WEB-004` (stale CAS), `WEB-006` (relation traversal binding), `WEB-008` (route field в template). Те же три провала воспроизведены на чистом `HEAD` с тем же overlay; это pre-existing red-сценарии, не регрессия удаления мультисайтности.
- `tests/qa/run-website-red.sh contract` завершается ненулевым кодом из-за отсутствующего UI-селектора `tests/frontend/admin/src/pages/site/website-acceptance.test.tsx`; этого файла нет и на чистом `HEAD`. Go-часть контрактного прогона проходит.
- `pnpm exec eslint admin/` падает на два существующих inline-disable для не зарегистрированного правила `react-hooks/exhaustive-deps` в `MediaPickerDialog.tsx` и `MediaPage.tsx`; ESLint-конфигурация вне зоны изменений.
- Integration-прогон с disposable PostgreSQL/S3, browser- и release-проверки в этом проходе не запускались; `DELETE /api/admin/pages/{pageID}` остаётся ожидающей операцией в контрактном тесте.

## Ранее зафиксированный проход — Core и границы модулей (2026-11-21)

### Подтверждённые проверки

- Полный набор Core-проверок прошёл через `QA_ENV_FILE=<private> tests/qa/run-core-acceptance.sh run`: из `core/` выполнены `go test -count=1 -overlay=<generated> ./...`, `go vet -overlay=<generated> ./...` и `go build ./...`. Скрипт берёт DSN только из приватного env-файла, значения в отчёт не попадают.
- Acceptance выполнялся на отдельном disposable PostgreSQL-контейнере `core-hardening-7d86bd2` (tmpfs, 27 task-specific БД `zubrium_core_qa_*`, снимок Atlas CLI зафиксирован на v1.3.0). Перед финальным прогоном БД пересозданы. Проверены чистый baseline/контрольную сумму и повторный startup, ранний отказ на грязной схеме без DDL, runtime-привилегии и ограничение роли, Core v2 session/file/access/cache, Coordinator lifecycle и failure injection, engine-data persistence. S3-round-trip прошёл через локальный `s3mock`; внешние S3endpoint/credentials не использовались.
- `make lint-core-boundary` прошёл: `Architecture boundaries OK: Core has no repository-local module dependencies; checked Core API and cross-engine imports across 19 repository-local Go modules.`
- Denominator Coordinator зафиксирован в `tests/qa/core-coordinator-scenarios.json`: все 16 сценариев имеют status `covered` и fresh evidence; `TestDisposableCoordinatorLifecycleSuite` прошёл 8/8 подтестов на отдельной свежей БД.
- Website: `go test`, `go vet` и `go build` из `engines/website` прошли с централизованным overlay после исправления порядка валидации курсора и test fixture. Forms, Loader, Router, Archive, Media, Prerender, presentation/router, admin/sites, admin/files, admin/ui, admin/forms, admin/router и `pkg` прошли `go test` с overlay.
- Admin SPA: `pnpm --filter @website-engine/admin typecheck`, `test` и `build` прошли.
- Корневой `Makefile` — тонкий wrapper над `main/Makefile`; модули остаются изолированными, композиция живёт только в `main`.
- Sweep до ABI-правок: `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures` и `make lint-core-boundary` из корня — все с exit code 0. Admin SPA: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — все PASS.
- После ABI-правок повторно пройдены из корня: `make lint-core-boundary`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures` — все с exit code 0.
- Content/locale-refactor ранее собирался: Website-проекции приходят через отдельную trusted-зависимость `ContentSchemas`; `VerifyScope` вызывается в handler и engine, level-проверку выполняет доверенный провайдер/engine, а handler сохраняет класс полученной ошибки. Текущие незавершённые locale-изменения в грязном дереве ломают тесты/vet Content (см. ограничения); ABI-правки их не касались.
- Target ABI `static-lifecycle` реализован в одном Go-пакете `engines/website/features/runtime`: manifest/ABI, ZIP preflight, secret-free context, lifecycle supervisor, dependency lock, publication CAS, artifact validation, fail-closed process executor и встроенный browser host. Прежнее имя `static-lifecycle-v2` отклоняется без compatibility alias как в runtime, так и в Archive.
- Централизованные проверки runtime после правок: `go test -count=3` и `go test -race -count=1` для `engines/website/features/runtime`, `go vet`/`go build` этого пакета, `go test -count=1 -overlay` для `workers/archive`, а также `TestWebsiteScenarioLedger` с 29 WEB-ID — все пройдены. Новые WEB-023..029 имеют `covered`/`pass` и содержат точные исполняемые subtests; формулировки ledger приведены к текущей схеме, новый прогон в этом проходе не выполнялся.
- Disposable контейнер `core-hardening-7d86bd2-core-qa-postgres-1` и его сеть удалены после acceptance; активных сессий в 27 QA-БД перед удалением не было. Контейнер `websiteengine-core-qa-postgres-1` на `127.0.0.1:55432` не изменялся.

### Найденные и исправленные дефекты

- **Coordinator, product:** `Service.Start` оставлял `started=true` после неудачного старта; `validTaskOutcome` проверял лимиты зависимостей по отдельности и пропускал сумму `200+200`. Оба воспроизведены RED-тестами и закрыты GREEN.
- **Engine-data, product:** PostgreSQL-адаптер отвергал легальные точечные `engine_id`, несовместимые с application и Atlas; `Store.List`/`ListExpired` не проверяли keyset-страницу перед возвратом курсора и проекции. Добавлены fail-closed проверки.
- **Website, product:** `ListSnapshots` проверял существование страницы раньше валидации курсора, из-за чего malformed cursor для неизвестной страницы давал `not found` вместо `invalid`; валидация курсора перенесена до lookup.
- **Тестовые фикстуры (не product-дефекты):** неверный namespace `loader.v2` вместо `loader/v2`, строковое сравнение нормализованного JSONB и неквалифицированное имя функции триггера при `search_path=pg_catalog`; в Website-тесте счётчик записей сравнивался с хардкодом вместо начального состояния.
- **Инфраструктура проверок:** в `main/Makefile` исправлены путь boundary-линтера, coverage-guard, `-count=1` и недостающие DSN `TEST_CORE_ENGINE_DATA_DATABASE_URL`, `TEST_COORDINATOR_LIFECYCLE_DATABASE_URL`; добавлен centralized acceptance entrypoint.
- **Композиция `main`:** устаревший alias `routerhttp` в `router_component.go` и несуществующий символ `compiledDevelopmentFixtures` в `devfixtures_enabled.go` блокировали сборку Router/devfixtures; заменены на реальные символы `router`/`DevelopmentFixturesEnabled`.
- **Централизованные тесты контрактов:** исправлены пути к spec/handler-файлам, слишком жёсткая проверка `info.version == 2.0.0`, сравнение catch-all-маршрутов Website и проверка Coordinator-группы через `ValidateBuildPlan` вместо `registerComponents`. Единственное ослабление — явный список двух ещё не подключённых endpoint'ов, описанный в ограничениях ниже.
- **Content:** снят блокер сборки переходом на `StoredWorkspace.PageSelections` и подключением отдельного trusted `SchemaSnapshots`-провайдера вместо повторного использования engine API в качестве источника projections.
- **Content, ревью:** handler превращал любую ошибку trusted-провайдера проекций в `ErrUnavailable` (503), теряя легальные `401/403/404/400`. Класс ошибки сохраняется и маппится `writeEngineError`; добавлен параметризованный регрессионный тест.
- **Content, ревью:** `CanonicalLocale` не проверял лимит `maxLength: 64` из `content.yaml`. Проверка длины канонического BCP-47 тега добавлена после канонизации; регрессионный тест использует валидный 101-символьный тег.
- **Feature runtime, product:** dependency resolver молча пере-resolve'л импортированную запись lock при изменённом digest; теперь несовпадение version/digest/API version отклоняется `ErrLock`. Token values публичного context принимали CSS-инъекцию (`url(...)`, `javascript:` и т.п.); добавлена fail-closed проверка с `ErrUnsupportedToken`. Artifact validation принимала `assets: null`; пустой/invalid assets map отклоняется.

### Состояние на конец прохода

- Все запланированные Core-проверки и сборки, а также проверки нового `static-lifecycle` runtime пройдены. Content-разблокировка включена в sweep; перечисленные ниже ограничения относятся к тому проходу. Коммит и push выполняются по прямому запросу пользователя после текущего прохода.

### Ограничения

- Перечисленные блокеры ABI-прохода (`prefix`, отсутствующий `strings`, неиспользуемый `reflect`, stale-тесты Content) устранены в текущем проходе; `engines/content` и `presentation/admin/content` проходят `go test`, `go vet` и `go build`.
- Host-side проверки `static-lifecycle` не заменяют browser acceptance: реальные CSP `connect-src`, typed request/response, prerender без внешней сети и реальные feature builds не прогонялись. WEB-015 и WEB-016 остаются открытыми в ledger.
- `pnpm exec eslint admin/` не прошёл по двум уже существующим inline-disable: для `react-hooks/exhaustive-deps` не зарегистрировано определение правила в `MediaPickerDialog.tsx` и `MediaPage.tsx`. Это вне зоны Core и не является регрессией текущих изменений.
- Trusted-провайдер проекций подключён только в fixture-ветке композиции. В full-профиле Content API не подключён, поэтому текущее состояние fail-closed; при подключении реального Content engine потребуется внешний `SchemaSnapshots`.
- `spec/presentation/openapi/content.yaml` объявляет обязательный query-параметр `locale` для publish/history, но handler его не использует; для history `?locale=...` отклоняется как `invalid_query` (400). Предшествующее расхождение контракта и реализации не исправлялось.
- Browser- и release-проверки в этом проходе не запускались.
- Два endpoint'а описаны в OpenAPI/спецификации, но не подключены в этом checkout: `POST /api/locale-preference` (registered public operation Router) и `DELETE /api/admin/pages/{pageID}` (durable page retirement). Они вынесены в явный список ожидающих операций в контрактном тесте; это не считается пройденной проверкой соответствующих сценариев.
- `spec/presentation/openapi/admin-notifications.yaml` использует схему `AdminBearer` и ключ `x-reject-cookie-and-bearer-together`, тогда как остальные контракты используют `BearerCredential` и `x-rejects-cookie-and-bearer-together`. Расхождение не исправлялось: оно вне зоны Core и требует синхронизации с Presentation.
- Профиль `full` выбирает модули, отвергаемые `validateRunnableComposition`; `RegisterWorkerHandlers` и scoped streams не подключены в композиции. Это вне зоны текущей задачи Core.
- Acceptance с PostgreSQL/S3 требует отдельных disposable ресурсов; рабочие инстансы не использовались.
- Публичный Router остаётся за внешним proxy; per-IP rate limiting не реализован в Router и является ответственностью deployer.
