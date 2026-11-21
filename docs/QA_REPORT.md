# QA-отчёт

## Аудит Core и интеграционных границ — 2026-11-20

- Core централизованные Go-тесты прошли по всем физическим пакетам через `tests/go-overlay.py` (`go test -vet=off -overlay=... -count=1`) в ARM64 Linux-контейнере с Go 1.26.4; migration tests использовали Atlas v1.3.0. Отдельно из `core/` прошли `go vet ./...` и `go build ./...`. S3 acceptance прошёл через изолированный локальный `s3mock`.
- Disposable PostgreSQL acceptance прошёл на созданных для QA `zubrium_core_qa_*` базах в tmpfs-контейнере: Atlas baseline/checksum и повторный startup, runtime grants/role restrictions, Core v2 session/file/access сценарии и существующая Coordinator PostgreSQL lifecycle suite. Значения DSN и credentials не сохранялись.
- Router delivery `go test`, `go vet ./...` и `go build ./...` прошли после исправлений: удалена ссылка на несуществующее поле fallback, запрещён `text/html` для artifact assets и encoded `/api/v2/admin` не направляется в публичный delivery.
- Admin Sites API `go test`, `go vet ./...` и `go build ./...` прошли. GET-маршруты сведены в явный path dispatcher, чтобы избежать неоднозначных `ServeMux` patterns; публичные URL не изменялись. Acceptance fixtures исправлены там, где они не соответствовали требуемому `page_id` UUID.
- Persisted engine-data regressions добавлены в `TestDisposableCoreV2FileGroupSetAndCacheAcceptance`: вторая зарегистрированная module capability не может выполнить guessed-key get/put/delete или list с чужим namespace/selector; после delete/recreate stale CAS update/delete конфликтуют и не меняют новую запись. Свежая PostgreSQL acceptance-команда `go test -vet=off -overlay=/tmp/go-overlay.json -run '^TestDisposableCoreV2FileGroupSetAndCacheAcceptance$' ./presentation` прошла на отдельной disposable БД. Остаются остальные engine-data acceptance gaps (revision/batch/TTL/cursor/concurrency и полный ledger).
- Coordinator PostgreSQL lifecycle suite выполнена успешно, но denominator и полноту всех перечисленных в Core TODO failure-injection сценариев ещё требуется свести к отдельному scenario ledger.
- Широкий overlay-прогон обнаружил отдельные незакрытые сбои вне Core: Website snapshot tests (cursor error mapping и side effects), Content tests (dependency/revision conflicts), а `presentation/admin/base/transport` не собирается из-за отсутствующего `GetContentProjections` у Website/Core composition boundary. Они не учитываются как пройденные проверки Core.

## Проверки текущего состояния

- Runtime bus получил JSON envelope, статический allowlist и strict typed payload decoding. После исправления конструктора `NewAllowlisted` прошли `gofmt`, `go test ./...` и `go vet ./...` в `pkg`, а также `go test ./internal/bus` и `go vet ./internal/bus` в `main`. Тестовый файл bus находится вне модуля; штатный `tests/go-overlay.py` не запускается из-за отсутствующего каталога `presentation/admin/httpauth`, поэтому сценарии из `tests/go/main/internal/bus/dispatcher_test.go` после последнего изменения не перепроверены. В composition зарегистрирован и используется только `core.readiness`; прямые связи между runtime-компонентами ещё остаются.
- Coordinator terminology and model were updated to `Worker` registration/handler, durable `Workflow`/`Task`, `TaskExecutor`, and Core-managed join barriers. Tests passed for Core coordination/PostgreSQL (`go test -overlay`), Core presentation Coordinator paths, Admin Coordinator API, and Archive/Media/Prerender Worker modules. `go vet` and `go build` passed for Core, Admin Coordinator API, and all three Worker modules. Worker test files live outside their Go modules, so isolated temporary modfiles and overlays were used; the root Go workspace remains blocked by the absent `presentation/httpjson` module. A full Core package run reached all packages but its presentation contract test fails because this checkout has no `/cmd` directory. Main config/composition Go tests therefore could not compile through the unresolved `presentation/httpjson` dependency.
- The initial Atlas migration was edited in place with the worker/join task shape; no ALTER migration was added. The Postgres package suite verified the Atlas checksum and clean baseline. Disposable PostgreSQL integration tests, including the multi-dependency join scenario, were not run because `TEST_COORDINATOR_JOIN_DATABASE_URL` is unset.
- Admin TypeScript typecheck passed; 47 test files and 213 tests passed; the Admin production build passed.
- ESLint Admin не прошёл: для inline-disable в `MediaPickerDialog.tsx` и `MediaPage.tsx` не зарегистрировано правило `react-hooks/exhaustive-deps`.
- Workspace-wide `pnpm typecheck` не прошёл из-за отсутствующих target-исходников `pkg/runtime/scripts` (`TS18003`).
- Go workspace tests не завершились: несколько модулей всё ещё требуют отсутствующий `github.com/Zubrium/cdn/presentation/httpjson`; попытка загрузить его через Go proxy завершилась ошибкой проверки TLS-сертификата. Корневые `make test`, `make vet` и build targets недоступны: в checkout нет `Makefile`.
- Локальные Markdown-ссылки отслеживаемых проектных документов проверены после последних изменений; ссылки сторонней skill-библиотеки `.agents/` исключены.
- Статический поиск не выявил внешних `/api/v1`/`/api/v2` маршрутов. OpenAPI сохраняет `openapi: 3.1.0` и обязательные числовые `info.version`.
- Browser acceptance текущих изменений не выполнялась.

## Ограничения

Production factories для Website, Content и Forms, общий Admin UI runtime и target `pkg/runtime/scripts` отсутствуют. Полная Website/Vitasil browser acceptance, production proxy/TLS, интеграция с рабочими PostgreSQL/S3 и полный security audit не подтверждены. Публичный Router не содержит per-IP rate limiter и должен оставаться за proxy, управляемым deployer.

## Состояние артефактов

Admin production build обновил отслеживаемый `pkg/static/dist/index.html`; файл оставлен без очистки. Остальные созданные build outputs не удалялись.
