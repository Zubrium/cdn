# Core Coordinator и composition платформы

Статус: согласованный целевой контракт; реализация ещё не соответствует ему полностью. Core остаётся независимым от классификации engines, workers и Presentation. Полноту подтверждают только текущие evidence в [QA_REPORT.md](../../docs/QA_REPORT.md) и [ACCEPTANCE-MATRIX.md](../../tests/ACCEPTANCE-MATRIX.md).

## Границы и термины

Coordinator — часть Core application. Он не является отдельным Go module, не открывает собственную БД и не содержит типов, registry или ветвлений для engines, workers, entrypoints либо Presentation. Core хранит только нейтральные workflow/task state и непрозрачные ссылки на зарегистрированные operations.

- **Operation** — versioned operation ID из статического DataBus allowlist выбранного profile. Core хранит ID/version и opaque input/result; он не знает, какому engine, worker или HTTP API принадлежит operation.
- **Task** — одна durable-запись workflow. Operation-task ссылается на operation ID/version и доверенный class ID; join-task является barrier для dependencies и не вызывает handler.
- **Workflow** — durable DAG tasks с одним корневым operation-task. Core управляет транзакциями, leases, retry, fencing, cancellation и восстановлением. Код обработки выбирает composition через DataBus.
- **Task class** — opaque стабильный идентификатор пула исполнения. Его назначает только trusted composition при регистрации operation. Клиент и входной payload не могут выбирать class.

Классификация endpoint-ов и владельцев остаётся в `main` и статических DataBus contracts. Core не импортирует пакеты `engines/`, `workers/`, `presentation/`, `main/` или `pkg/contracts`; runtime вызовы между Core и остальными модулями проходят через bus adapters. Внутри Core application и infrastructure вызовы остаются прямыми по границам Go-модуля.

## Сборка, allowlist и lifecycle

`main/` — единственный product executable и composition root. Независимый Go module `pkg/contracts` задаёт versioned operations; `main` компилирует profile в статический allowlist и создаёт process-local JSON request/response bus. Core adapters в `main` переводят только разрешённые Core operations между bus envelope и Core application ports. Modules получают только typed DataBus clients с привязанным composition scope. Owner/class нельзя передавать в request payload или выбирать из HTTP.

Profile v1 описывает статические компоненты и API sets для генератора сборки. Эти сведения остаются в `main`; Core получает только общие значения своей конфигурации, например record namespaces, access groups, limits и class-scoped execution config, без полей `kind=engine/worker`. Генератор разрешает profile в детерминированные импорты и dependency-first build plan. Динамическая загрузка Go-кода и runtime import paths запрещены.

Startup выполняется в порядке: проверить YAML и build plan; проверить fingerprint/лимиты; запустить Core и Atlas; собрать DataBus allowlist и typed adapters; выдать модулям scoped clients; запустить компоненты; проверить readiness; активировать durable operation executor; затем открыть API. Любая ошибка закрывает ingress и приводит к cleanup в обратном порядке. Shutdown прекращает новые claims, закрывает приём новых вызовов, отменяет активные handlers, ограниченно ожидает их завершения и только затем освобождает lease и Core resources.

Пулы задаются явными `task_classes` и `class_concurrency`; скрытые concurrency defaults запрещены. Lease, timeout, polling/retry, max attempts, workflow/task limits, retention и administrative group также задаются конфигурацией. Изоляция claim по class обязательна: конкурентные независимые классы не блокируют друг друга.

Реплики одной логической системы используют одну PostgreSQL database и один fingerprint. Отличающийся fingerprint запрещён при живых leases; после остановки всех реплик повышается epoch и fencing tokens старого запуска становятся недействительными.

## Runtime DataBus

Bus синхронный, process-local и request/response; он маршрутизирует только операции, включённые статическим allowlist профиля и зарегистрированные typed adapter-ом. Envelope содержит `operation_id`, `version`, `payload`; payload строго декодируется и валидируется. Неизвестные версии, лишние/дублированные поля, некорректные значения, отсутствующий scope и закрытый dispatcher отклоняются fail-closed. HTTP adapter вызывает заранее выбранную operation, никогда не принимает operation ID/envelope от клиента.

Caller authority и owner scope прикрепляет composition вне JSON. Операционный handler повторно проверяет свою authorization policy. Большие file bodies передаются только через scoped stream capability с limit, backpressure, cancellation и однозначным владельцем закрытия. Bus не хранит durable state и не гарантирует доставку после остановки процесса.

Coordinator вызывает operation handler через Core-neutral executor port, реализация которого в `main` выполняет typed DataBus call. Core не вызывает engine/worker Go interfaces и не хранит component callbacks. Durable state, retry, lease и recovery остаются в Core; краткий runtime вызов handler проходит через bus.

## Durable workflows

Владелец создаёт workflow через scope-bound `WorkflowClient`. `Start` принимает зарегистрированные operation ID/version, ограниченный JSON input и idempotency key. Core назначает workflow/task IDs и берёт trusted task class из startup registration, а не из запроса. `Get`/`Cancel` доступны только в том же owner scope и epoch.

Operation handler получает durable `Task.ID`, opaque payload и ограниченные runtime capabilities; возвращает JSON result, bounded machine error и ограниченный список дочерних tasks. `Task.ID` — единственный authority для recovery/idempotency. У каждого child уникальный `emission_key`; dependencies внутри того же workflow ссылаются на Task ID либо на sibling emission keys, созданные той же транзакцией. Operation-task без dependencies наследует зависимость от parent; join требует хотя бы одну dependency и не получает handler, lease или попытки.

Фиксация результата в одной PostgreSQL-транзакции закрывает parent lease, сохраняет result, создаёт children и dependency edges. Core отклоняет чужие ссылки, дубликаты, превышение настроенных лимитов и циклы. Join становится succeeded, когда все dependencies succeeded. Ошибка операции после исчерпания attempts переводит workflow в failed и отменяет оставшиеся queued/running tasks. Workflow успешен, когда root и все tasks DAG завершены успешно.

Повтор idempotency key с тем же fingerprint возвращает прежний workflow; иной input даёт conflict. Семантика at-least-once. Lease и fencing token запрещают позднюю фиксацию старым claim, но не отменяют уже выполненные внешние side effects; их владелец обязан применять idempotency/reconciliation.

PostgreSQL является источником времени для leases. Exponential backoff, pool/class concurrency, timeout, max attempts, payload/result limits, workflow/task/child limits и retention заданы конфигурацией. Отказ durable store, просроченный instance lease или нарушение fencing делает Core unhealthy. Ошибки handler сохраняются только как bounded machine codes; panic изолируется и трактуется как ошибка.

Workflow и tasks переживают restart. Result читается через scoped API. Отдельные Promise/Future service, Go channels как источник истины и пользовательский workflow DSL не вводятся. Уведомления — только подсказки; PostgreSQL остаётся источником истины.

## Административный контроль

При включённом `admin.coordinator` Admin API выдаёт безопасные workflow/task summaries, список, детали и retry только failed operation-task. Payload/result и storage IDs не выдаются. Каждый запрос повторно проверяет credential и прямой уровень в настроенной группе: уровень 1 читает summaries, уровень 2 разрешает retry. Join вручную не перезапускается.

Admin не выбирает handler, owner, task class или произвольную operation для запуска. Durable workflows создаются typed product operations после собственной domain authorization. Административный retry работает только с уже существующим task ID в scope workflow.

Контракт routes остаётся `/api/coordinator/workflows`, `/api/coordinator/workflows/permissions`, `/api/coordinator/workflows/{workflowID}` и `/api/coordinator/workflows/{workflowID}/tasks/{taskID}/retry`; ответы `no-store`, `nosniff`, с безопасной pagination и без приватных payload/result.

## Приёмка

Нужны executable сценарии для profile generation/allowlist; отсутствующей operation/версии; lifecycle rollback/readiness; scope spoofing; class-scoped concurrent claims; fingerprint/epoch; idempotent/conflicting start; delayed work; join barriers; sibling references; cycle/foreign-workflow rejection; atomic child commit; cancellation; restart/result recovery; lease takeover/fencing; retry/terminal failure; deadline/retention; store outage; Admin permissions/pagination/privacy/safe errors. Tests доказывают отсутствие Core imports и runtime calls из `engines/`, `workers/`, `presentation/`, а также отсутствие engine/worker classifications и catalogs в Core.

Все integration/E2E acceptance выполняются только на fresh disposable PostgreSQL/S3/Chromium. Подмножество зелёных сценариев не подтверждает release готовность; точные команды и evidence записываются в QA report.
