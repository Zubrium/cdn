# План: устранение архитектурных нарушений из `PROBLEM.md` (2026-11-25)

## Контекст

`PROBLEM.md` — пользовательский стек нарушений между модулями платформы. Требуется исправить все однозначные нарушения и согласованные спорные места. Работа ведётся в текущем checkout, ветка `readiness-20261123`, после коммита `007a4b1`.

### Итоговые решения пользователя

1. **Прямые вызовы запрещены для всех модулей без исключения.** Все cross-module runtime-вызовы идут только через process-local DataBus с envelope, allowlist и module scope.
2. **Engine↔engine через DataBus тоже запрещено.** Такие взаимодействия выполняются только как Core Coordinator workflow из нескольких независимых задач/воркеров.
3. **Асинхронность нужна только для междвижковых взаимодействий.** Пример: изменение сайта → дождаться snapshot → pre-render → pre-render с контентом. Настройка и работа с формами остаются проще: Presentation получает HTTP-запрос, через DataBus вызывает engine, engine через DataBus вызывает Core. С воркерами этот путь несовместим: воркеры строго асинхронные.
4. Presentation→engine — через DataBus; Router→Forms/Loader больше не facade.
5. Presentation→Core и engine→Core — через DataBus; `core/` не меняется.
6. Внутренние Go-контракты можно менять; внешние HTTP/OpenAPI-контракты сохраняются.
7. Publication, Retirement и SnapshotFork переводятся на Core workflows с отдельными задачами/воркерами.
8. Расширение workflow — только регистрацией в `main`; `core/` не трогается.
9. Spec и acceptance matrix обновляются **до** кода.
10. Notifications переносятся в `presentation/admin/base/notification`; `presentation/admin/base` приводится в порядок: центральный transport не должен знать обо всех engine’ах и admin-модулях.
11. Коммиты — по проверенным чекпоинтам, с push.

### Жёсткие ограничения

- Не трогать рабочий стек `zubriumprod205553`, его БД/buckets; write-запросы — только в изолированные стеки.
- Не трогать пользовательские uncommitted-файлы: `skills-lock.json`, `.agents/skills/verification-before-completion/`, `.claude/`, `.idea/LanguageServersSettings.xml`, сам `PROBLEM.md`.
- Секреты только в приватных env/файлах `0600`, не в коммитах и отчётах.
- Статусы `pass` выставлять только после реального прогона с зафиксированным evidence.

## Цель

После выполнения:

1. Прямых `*core/presentation.Runtime` и прямых service handle’ов между модулями нет ни в engines, ни в Presentation, ни в `main/internal/composition` вне зарегистрированных bus-адаптеров.
2. Presentation→engine и engine→Core идут через DataBus; engine→engine не идут ни прямо, ни через DataBus — они разложены на Core workflow tasks/workers.
3. Core workflow операции зарегистрированы, включены в allowlist и используются; проверки module/class перенесены на registration/dispatch level, а не только в executor.
4. Presentation-модули собираются отдельно (`GOWORK=off`) и не образуют циклов по go.mod; общий cookie/CSRF/Origin пакет вынесен из Admin, публичный Router не зависит от Admin; notifications выделены в `presentation/admin/base/notification`.
5. Публичный upload не работает с ФС: байты идут через scoped stream и Core/Loader staging capability.
6. Линтеры и тесты ловят все перечисленные нарушения; Go-манифесты полные.

## Подтверждённые нарушения из `PROBLEM.md` и карт субагентов

1. **Website → Core напрямую:** `website_component.go:58-87`, `website_core_adapter.go`, `website_build.go:18-29`, `workflow_operations.go:14-86`; `RegisterWorkflowOperations` не подключён, ключей нет в allowlist.
2. **Admin → Core напрямую:** `components.go:609-616`, `admin_core_adapter.go` обходит `core.access.*`, `core.workflow.*`.
3. **Content ↔ Website:** `components.go:916-923`, `content_component.go:65-75`, `content_projections.go:60-117`, `website_build.go:66-99`, `website_build_input.go:35-48` — прямые handle’ы и startup-контекст в обе стороны.
4. **Router → Forms/Loader:** `router_component.go:49-92` — facade/`publicforms` и прямой `*loaderapp.Service`; staging в `publicupload/upload.go:143-156,601-628` пишет в ФС.
5. **Publication/Retirement/Fork:** `publication_component.go:39-51`, `retirement_component.go:42-51`, `snapshot_fork_full.go:20-83` — прямые Website/Router/Core-вызовы; вход через bus есть, но внутри снова прямой путь.
6. **Presentation cycles:** `presentation/admin/base` зависит от всех engine facade и всех admin-модулей; admin/content, admin/forms, admin/sites, admin/files и публичный router зависят от `base/admin/auth`; `admin/router → presentation/router → base`. Циклы по Go-модулям.
7. **Internal engine imports в Presentation:** baseline `tests/linter/presentation_check.go:10-15` — `engines/content/domain`, `engines/router/features/routes/{application,model}`, `engines/loader/features/files/pkg`.
8. **Глобальное состояние:** `base/transport/concurrency_metrics.go:23-29` делит dedup-карту между Admin и Public.
9. **Шина:** allowlist профильный; class проверяется только в `task_executor.go:104-106`; workflow-операции мёртвые; линтер разрешает внешний `core/presentation`; нет теста точного allowlist и запрета `operation_id` от клиента.
10. **Go-манифесты:** `workers/archive`, `engines/website`, `presentation/router` не собираются при `GOWORK=off`; локальные `replace` не зеркалированы в модулях.

## Метод

- Spec-first: сначала обновляем `spec/` и `tests/ACCEPTANCE-MATRIX.md`, затем TDD.
- TDD для каждого чекпоинта: тест → красный прогон → минимальная реализация → зелёный прогон.
- Проверка после каждого чекпоинта: `git diff --check`, затронутые `go test ./...`, `go vet ./...`, `go build ./...`, `GOWORK=off go build ./...` для затронутых модулей, Admin `pnpm` проверки.
- Один свежий ревью-проход whole-change в конце перед финальной волной документирования.

## Чекпоинты (коммиты)

### 0. Spec и acceptance под strict rule

Файлы: `spec/architecture/SPEC-modular-platform.md`, `spec/architecture/SPEC-engine-go-contracts.md`, `spec/engines/router/SPEC-router-engine.md`, `spec/engines/forms/SPEC-forms-engine.md`, `spec/core/SPEC-module-data.md`, `tests/ACCEPTANCE-MATRIX.md`.

Фиксируем:
- прямой вызов между модулями запрещён всегда;
- Presentation→engine и ←Core только через DataBus;
- engine→engine только через Core workflow tasks/workers;
- Router→Forms/Loader — через DataBus operations, facade удаляется;
- public upload bytes — через scoped stream и Core/Loader staging;
- workflow-оркестрация Content/Website/Publication/Retirement/Fork — асинхронная; формы — синхронная цепочка Presentation→engine→Core.

### 1. Шина, манифесты, линтеры

- Per-module authorization на dispatch/registration level: module ID из `NewModuleDispatcher` проверяется не только для access/files/records, но и для content, publication, retirement, task, workflow.
- Класс задачи проверяется не только в executor, но и на уровне dispatch/registration.
- Подключить `RegisterWorkflowOperations`; добавить workflow keys в allowlist профилей.
- Тест точного состава allowlist и запрета клиентского `operation_id`.
- Линтер: запрет внешних `core/presentation` без исключения; снятие/замена baseline internal-engine imports по мере выноса facade; проверка прямых service handle’ов в `main/internal/composition`.
- Починить `GOWORK=off`: `workers/archive`, `engines/website`, `presentation/router` + зеркало локальных replace.

### 2. Core через DataBus: Website и Admin

- Website ports (`BuildFiles`, `Files`, `Workflow`, `Sessions`, `Authority`, media) перевести на `contracts.Dispatcher` + существующие/новые Core bus операции.
- Admin adapters перевести на `core.access.*`, `core.workflow.*`, session/principal/credential/group operations.
- Прямые `*core/presentation.Runtime` в этих адаптерах исчезают; Core не меняется.

### 3. Engine↔engine → Core workflows

- Content↔Website build projection: убрать прямых handle’ы и startup ctx; Core workflow с tasks: snapshot → locale → placement → feature version → pre-render → pre-render with content.
- Publication, Retirement, SnapshotFork: разложить на workflow tasks/workers; worker’ы вызывают engine/Core только через DataBus.
- Внутренние engine engine-to-engine вызовы удалены полностью.

### 4. Router→Forms/Loader через DataBus и staging

- Добавить bus operations для Forms visitor/session/submit и Loader public upload ingest.
- Router Presentation вызывает их через DataBus; facade удалён.
- Loader ingest читает stream, считает SHA-256, пишет через существующий `StagedFilesCapability` Core; Router не использует `os`/`filepath`.
- Убрать `UploadStagingDirectory` из конфига и примеров; limit’ы перенести в loader/core.
- Снять соответствующий baseline internal-engine import.

### 5. Presentation isolation

- Вынести cookie/CSRF/Origin/token логику в нейтральный модуль `presentation/httpauth`; обновить 9 точек импорта и go.mod/go.work.
- Notifications → отдельный Go-модуль `presentation/admin/base/notification`; `base/transport` получает узкий `http.Handler` через injection, не импортирует notification.
- Убрать engine/admin зависимости из центрального `base/transport`: registration передаётся из `main`, `base` владеет только общим mux/guard/transport.
- Устранить глобальную dedup-карту concurrency metrics: состояние на экземпляр сервера/инъецируемый компонент.
- Вынести внутренние DTO/errors engine’ов в их публичные facade: `ReplaceLocaleConfigRequest`, route errors/targets, Loader upload types.
- Сохранить внешние HTTP-контракты и SSE behavior.

### 6. Финальная верификация и документы

- Acceptance matrix, QA report, TODO с реальным evidence.
- Полный sweep: `git diff --check`, все Go-модули `go test/vet/build`, `GOWORK=off`, Admin `typecheck/test/build`, ESLint, при необходимости изолированный live smoke.
- Push.

## Вне скоупа

- Исправление `frontend/admin/src/api.ts::prepareRenditions` (уже задокументировано как отдельный дефект в `docs/QA_REPORT.md`).
- Изменение рабочего стека и пользовательских файлов.
- Изменение `core/` без доказанного нарушения credential-bound session контракта.
