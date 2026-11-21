# QA-отчёт

## Текущий проход — Core и границы модулей (2026-11-21)

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
- Централизованные проверки runtime после правок: `go test -count=3` и `go test -race -count=1` для `engines/website/features/runtime`, `go vet`/`go build` этого пакета, `go test -count=1 -overlay` для `workers/archive`, а также `TestWebsiteScenarioLedger` с 29 WEB-ID — все пройдены. Новые WEB-023..029 имеют `covered`/`executed` и содержат точные исполняемые subtests.
- Disposable контейнер `core-hardening-7d86bd2-core-qa-postgres-1` и его сеть удалены после acceptance; активных сессий в 27 QA-БД перед удалением не было. Контейнер `websiteengine-core-qa-postgres-1` на `127.0.0.1:55432` не изменялся.

### Найденные и исправленные дефекты

- **Coordinator, product:** `Service.Start` оставлял `started=true` после неудачного старта; `validTaskOutcome` проверял лимиты зависимостей по отдельности и пропускал сумму `200+200`. Оба воспроизведены RED-тестами и закрыты GREEN.
- **Engine-data, product:** PostgreSQL-адаптер отвергал легальные точечные `engine_id`, несовместимые с application и Atlas; `Store.List`/`ListExpired` не проверяли keyset-страницу перед возвратом курсора и проекции. Добавлены fail-closed проверки.
- **Website, product:** `ListSnapshots` проверял существование site/page раньше валидации курсора, из-за чего malformed cursor для неизвестного сайта давал `not found` вместо `invalid`; валидация курсора перенесена до lookup.
- **Тестовые фикстуры (не product-дефекты):** неверный namespace `loader.v2` вместо `loader/v2`, строковое сравнение нормализованного JSONB и неквалифицированное имя функции триггера при `search_path=pg_catalog`; в Website-тесте счётчик записей сравнивался с хардкодом вместо начального состояния.
- **Инфраструктура проверок:** в `main/Makefile` исправлены путь boundary-линтера, coverage-guard, `-count=1` и недостающие DSN `TEST_CORE_ENGINE_DATA_DATABASE_URL`, `TEST_COORDINATOR_LIFECYCLE_DATABASE_URL`; добавлен centralized acceptance entrypoint.
- **Композиция `main`:** устаревший alias `routerhttp` в `router_component.go` и несуществующий символ `compiledDevelopmentFixtures` в `devfixtures_enabled.go` блокировали сборку Router/devfixtures; заменены на реальные символы `router`/`DevelopmentFixturesEnabled`.
- **Централизованные тесты контрактов:** исправлены пути к spec/handler-файлам, слишком жёсткая проверка `info.version == 2.0.0`, сравнение catch-all-маршрутов Sites и проверка Coordinator-группы через `ValidateBuildPlan` вместо `registerComponents`. Единственное ослабление — явный список двух ещё не подключённых endpoint'ов, описанный в ограничениях ниже.
- **Content:** снят блокер сборки переходом на `StoredWorkspace.PageSelections` и подключением отдельного trusted `SchemaSnapshots`-провайдера вместо повторного использования engine API в качестве источника projections.
- **Content, ревью:** handler превращал любую ошибку trusted-провайдера проекций в `ErrUnavailable` (503), теряя легальные `401/403/404/400`. Класс ошибки сохраняется и маппится `writeEngineError`; добавлен параметризованный регрессионный тест.
- **Content, ревью:** `CanonicalLocale` не проверял лимит `maxLength: 64` из `content.yaml`. Проверка длины канонического BCP-47 тега добавлена после канонизации; регрессионный тест использует валидный 101-символьный тег.
- **Feature runtime, product:** dependency resolver молча пере-resolve'л импортированную запись lock при изменённом digest; теперь несовпадение version/digest/API version отклоняется `ErrLock`. Token values публичного context принимали CSS-инъекцию (`url(...)`, `javascript:` и т.п.); добавлена fail-closed проверка с `ErrUnsupportedToken`. Artifact validation принимала `assets: null`; пустой/invalid assets map отклоняется.

### Состояние на конец прохода

- Все запланированные Core-проверки и сборки, а также проверки нового `static-lifecycle` runtime пройдены. Content-разблокировка включена в sweep; полные `make test` и `make vet` в текущем грязном дереве останавливаются на незавершённых locale-изменениях Content, не связанных с ABI. Новый коммит и push не создавались по прямому запрету пользователя.

### Ограничения

- Полные `make test` и `make vet` после ABI-правок не зелёные из-за незавершённых пользовательских locale-изменений вне зоны задачи: `engines/content/repository/module_data.go` использует неконстантный `prefix`, не импортирует `strings`; `engines/content/application/service.go` оставил неиспользуемый `reflect`; stale-тесты Content domain и `presentation/admin/content` не передают обязательные `Locale`/`TranslationID`, `?locale=` и `expected_selection_revision`. Эти файлы не изменялись в текущей задаче и не вызваны `static-lifecycle`. Отдельно проверенные модули `engines/website/features/runtime` и `workers/archive` проходят тесты/vet/build; все сборки из корня проходят.
- Host-side проверки `static-lifecycle` не заменяют browser/Vitasil acceptance: реальные CSP `connect-src`, typed request/response, prerender без внешней сети и реальные feature builds не прогонялись. WEB-015 и WEB-016 остаются открытыми в ledger.
- `pnpm exec eslint admin/` не прошёл по двум уже существующим inline-disable: для `react-hooks/exhaustive-deps` не зарегистрировано определение правила в `MediaPickerDialog.tsx` и `MediaPage.tsx`. Это вне зоны Core и не является регрессией текущих изменений.
- Trusted-провайдер проекций подключён только в fixture-ветке композиции. В full-профиле Content API не подключён, поэтому текущее состояние fail-closed; при подключении реального Content engine потребуется внешний `SchemaSnapshots`.
- `spec/presentation/openapi/content.yaml` объявляет обязательный query-параметр `locale` для publish/history, но handler его не использует; для history `?locale=...` отклоняется как `invalid_query` (400). Предшествующее расхождение контракта и реализации не исправлялось.
- Browser-, Vitasil- и release-проверки в этом проходе не запускались.
- Два endpoint'а описаны в OpenAPI/спецификации, но не подключены в этом checkout: `POST /api/locale-preference` (registered public operation Router) и `DELETE /api/admin/sites/{siteID}/pages/{pageID}` (durable page retirement). Они вынесены в явный список ожидающих операций в контрактном тесте; это не считается пройденной проверкой соответствующих сценариев.
- `spec/presentation/openapi/admin-notifications.yaml` использует схему `AdminBearer` и ключ `x-reject-cookie-and-bearer-together`, тогда как остальные контракты используют `BearerCredential` и `x-rejects-cookie-and-bearer-together`. Расхождение не исправлялось: оно вне зоны Core и требует синхронизации с Presentation.
- Профиль `full` выбирает модули, отвергаемые `validateRunnableComposition`; `RegisterWorkerHandlers` и scoped streams не подключены в композиции. Это вне зоны текущей задачи Core.
- Acceptance с PostgreSQL/S3 требует отдельных disposable ресурсов; рабочие инстансы не использовались.
- Публичный Router остаётся за внешним proxy; per-IP rate limiting не реализован в Router и является ответственностью deployer.
