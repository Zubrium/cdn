# QA-отчёт

## Проверки текущего состояния

- Runtime bus получил JSON envelope, статический allowlist и strict typed payload decoding. После исправления конструктора `NewAllowlisted` прошли `gofmt`, `go test ./...` и `go vet ./...` в `pkg`, а также `go test ./internal/bus` и `go vet ./internal/bus` в `main`. Тестовый файл bus находится вне модуля; штатный `tests/go-overlay.py` не запускается из-за отсутствующего каталога `presentation/admin/httpauth`, поэтому сценарии из `tests/go/main/internal/bus/dispatcher_test.go` после последнего изменения не перепроверены. В composition зарегистрирован и используется только `core.readiness`; прямые связи между runtime-компонентами ещё остаются.
- Admin TypeScript typecheck прошёл; 46 test files и 209 tests прошли; production build прошёл.
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
