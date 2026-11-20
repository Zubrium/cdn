# Core Coordinator и composition платформы

Статус: согласованный целевой контракт. Реализация переносится внутрь Core; этот документ не означает, что система проверена или готова к выпуску. Источник истины по слоям и таблицам — [SPEC-core-architecture.md](../core/SPEC-core-architecture.md).

## Границы и термины

Coordinator — часть Core application. У него нет отдельного Go module, подключения к БД или владельца данных. Он принимает только нейтральные component/operation descriptors и непрозрачные JSON payload/results. Он не содержит терминов или типов Website, Snapshot, Content, Form, Router или публикации.

Три понятия разделены по назначению:

- **Worker** — статически зарегистрированный тип операции и его handler: `component_id + operation_id + contract_version`. Worker входит в выбранный runtime profile и не является записью запуска.
- **Task** — одна durable-запись внутри workflow. Worker-task ссылается на Worker и исполняет его handler. Join-task не ссылается на Worker и служит barrier для зависимостей.
- **Workflow** — durable граф Tasks с одним корневым Worker-task. `TaskExecutor` — единственная Core-служба, которая забирает и исполняет Worker-tasks. Join-tasks исполняет Core без handler и lease.

Core владеет идентичностями, workflow, задачами, группами файлов, хранилищем, кэшем и engine-data. Движки владеют своими моделями и бизнес-правилами. Workflow между движками связывает зарегистрированные операции через данные-контракты; движки не импортируют application packages друг друга и не вызывают друг друга напрямую.

## Сборка и регистрация

`main/` — единственный product executable и composition root новой платформы; он создаёт один внутрипроцессный typed request/response dispatcher на экземпляр runtime. Нейтральные операции и типы сообщений принадлежат независимому корневому Go-модулю `pkg` (пакет `pkg/contracts`), реализация dispatcher — `main`. Bus связывает только операции, зарегистрированные статически выбранными профилями, и не является transport между engine peers: engine/worker не могут отправлять bus-запросы другим engine/worker. Core adapters в `main` направляют разрешённые вызовы через `core/presentation`; Core не импортирует bus или компоненты.

Build profile v1 задаёт списки `engines`, `workers`, `apis.admin` и `apis.public`. Core включён всегда; Coordinator не является отдельным компонентом профиля. `admin.coordinator` — только необязательный административный API. Профиль содержит идентификаторы из allowlist, а не Go import paths. Генератор разрешает список в детерминированный dependency-first `BuildPlan` и статические импорты. Каждый runtime-компонент регистрирует фабрику, ID и зависимости. Нейтральный descriptor компонента содержит ID, вид (`engine`, `worker`, `entrypoint`), версию и объявленные операции. Worker handlers имеют стабильные operation ID и версию контракта. До побочных действий composition проверяет неизвестные/дублированные компоненты и операции, версии, зависимости, неполные factories и маршруты bus.

Профиль не загружает Go код динамически. Чтобы изменить состав бинарного файла, оператор собирает новый `main` profile и разворачивает его с тем же YAML и подходящей конфигурационной эпохой. Каждый engine/worker/Presentation module отдельно компилируется и тестируется, но не обязан выпускать собственный бинарник.

## Жизненный цикл

Порядок запуска:

1. Загрузить и проверить YAML и compiled build plan.
2. Проверить конфигурационные fingerprint и ограничения Core.
3. Запустить Core: проверить/создать схему только через Atlas, открыть DML и object-store adapters, зарегистрировать modules и получить instance lease.
4. Создать и запустить выбранные компоненты в dependency order.
5. Проверить readiness всех выбранных компонентов.
6. Последней фазой активировать Core TaskExecutor; только после этого объявить приложение готовым и принимать запросы.

Ошибка любой фазы оставляет API закрытыми и запускает cleanup в обратном порядке. Shutdown прекращает новые claims, отменяет контексты handlers, ожидает кооперативного завершения в пределах заданного срока, освобождает instance lease и только затем закрывает Core database pool. Если ожидание истекло, база остаётся открытой, пока выполняющиеся handlers завершаются.

Операционная конфигурация задаёт `worker_concurrency`, `task_lease`, `task_timeout`, polling/retry limits, max attempts, максимальное количество Tasks и детей, workflow retention, `workflow_access_group`, `workflows_page_default` и `workflows_page_maximum`. Значение группы `coordinator_jobs` сохранено в действующем YAML, чтобы не отзывать назначенные grants; это только access-group key, не тип запуска.

Каждая реплика использует одну PostgreSQL database на одну логическую систему. Одинаковый fingerprint допускает параллельные реплики. Иной fingerprint запрещён при живых leases; после остановки всех реплик повышается epoch, старые capabilities и fencing tokens становятся недействительными. Fingerprint не включает секреты и включает канонический build plan и runtime-конфигурацию.

## Runtime message bus

Bus process-local, синхронный request/response и маршрутизирует вызовы между зарегистрированными runtime-компонентами к адаптеру или capability владельца операции. Внутримодульные вызовы слоёв остаются прямыми. На границе bus используется общий JSON envelope с `operation_id`, `version` и JSON `payload`; конкретная operation строго декодирует и валидирует payload, после чего вызывает typed contract владельца. Регистрация handler-ов статична и определяется выбранным build profile; неизвестные operation/version, дубликаты, неизвестные JSON-поля, повторяющиеся ключи, некорректный payload и закрытая шина отклоняются явно. Клиент HTTP не выбирает operation ID и не видит bus envelope: ingress вызывает заранее назначенную операцию, а транспортная авторизация остаётся в Presentation.

Caller не передаёт authority, principal или group grants в payload. Scoped contract и доверенный server-side context задаются composition; владелец повторно применяет собственную авторизацию. Большие файлы не сериализуются в JSON: операция получает ограниченную scoped stream capability с явным владением/закрытием потока, backpressure, лимитами и отменой. Bus синхронно передаёт вызов и не содержит бизнес-правил, persistence, retry, ordering, cross-replica delivery или replay guarantees. Ошибки handler возвращаются вызывающему и сами по себе не означают durable work.

Долговременная межкомпонентная работа проходит через Core Coordinator, вызванный через scoped bus operation: вызывающий компонент создаёт workflow через свой `WorkflowClient`, Coordinator сохраняет состояние и назначает зарегистрированный worker handler. Bus не становится durable queue и не заменяет leases, fencing, retry или recovery Coordinator. `main` владеет registry/dispatcher и адаптерами, но не бизнес-правилами или состоянием Core. Текущий код пока использует bus только для Core readiness; прямые вызовы между компонентами в composition и HTTP adapters ещё требуют миграции и не считаются соответствующими целевому контракту.

## Durable workflows

Движок получает scoped `WorkflowClient` только для собственного зарегистрированного component ID. `Start` принимает operation ID, версию, ограниченный JSON-объект и idempotency key; Core сам создаёт workflow/task IDs. Клиент не может указать owner, epoch, engine ID, SQL или произвольный task handler. `Get` и `Cancel` ограничены тем же component scope и активной конфигурационной эпохой.

Worker handler получает ограниченный DTO Worker-task, включая Core `Task.ID`, и возвращает JSON result, безопасный error code и ограниченный список дочерних Tasks. Идентификатор запуска берётся из `Task.ID`, а не из opaque payload. Каждый дочерний Task имеет уникальный в пределах parent `emission_key`, payload и зависимости внутри того же workflow. `kind=worker` требует ссылку на зарегистрированный Worker и допустимый `max_attempts`; если зависимости не заданы, Core добавляет зависимость на parent. `kind=join` не имеет Worker/handler, не исполняется TaskExecutor и требует хотя бы одну зависимость. Его `max_attempts` не задаёт бизнес-поведение.

В одной транзакции Core закрывает lease родителя, сохраняет его result, создаёт дочерние записи и добавляет рёбра зависимостей. Дочерние Worker-tasks могут ссылаться на sibling Tasks через IDs или `DependsOnEmissions`; для последнего Core заранее выделяет IDs и разрешает ссылки в той же транзакции. Граф проверяется на дубликаты, чужие ссылки и циклы. Join автоматически становится `succeeded`, когда все его зависимости `succeeded`; он никогда не попадает в очередь claims и не получает попытки/lease. Если Worker-task исчерпал попытки, workflow становится `failed`, а оставшиеся queued/running Tasks, включая joins, отменяются. Workflow становится `succeeded`, когда корневой Task и все Tasks графа успешны.

Повтор idempotency key с тем же operation/version/input возвращает прежний workflow; иной input конфликтует. Исполнение — at-least-once. Lease и fencing token исключают позднюю фиксацию результата старым worker, но не отменяют уже совершённые внешние side effects. Движок обязан использовать идемпотентные операции или reconciliation.

Lease и время доступности сверяются по времени PostgreSQL. Живость экземпляра выражается его `lease_until` и продлевается через renew; отдельная колонка heartbeat не хранится. Retry с exponential backoff, число попыток Worker-task, concurrency TaskExecutor, timeout, максимальный размер payload/result, число Tasks workflow, число детей на шаг и retention задаются конфигурацией. Потеря lease одной задачи означает смену владельца; отказ durable store или lease всей Core instance делает приложение unhealthy. Ошибка handler хранится только как bounded machine code. Panic из handler изолируется и обрабатывается как ошибка.

Workflow и задачи долговечны между перезапусками. Workflow result доступен через scoped Core API. Отдельные `Future`, Go channel, promise service или пользовательский DSL в v1 не вводятся. Внешние уведомления — только подсказка; PostgreSQL остаётся источником истины.

## Административный контроль

Когда включён `admin.coordinator`, Core предоставляет узкую capability для списка Workflow, чтения Task-графа и retry задачи. Она не возвращает payload/result и не выдаёт права на запуск или исполнение задач. HTTP adapter каждый раз проверяет credential и прямой доступ к настроенной группе: уровень 1 разрешает читать workflow/task summaries, уровень 2 — вручную повторить failed Worker-task. Join-task вручную не перезапускается. Пользовательская администрация и глобальные роли не обходят групповое разрешение.

Ручной запуск не является операцией `admin.coordinator` и не открывает общий task-submission API. Он начинается с типизированного доменного endpoint в Presentation, проходит domain authorization и direct level `2` в настроенной группе, затем engine создаёт свой workflow через scoped Core capability. Worker-tasks добавляются как зарегистрированные children; join-task используется как barrier. Admin не выбирает worker/component/operation ID и не отправляет произвольный payload. Это сохраняет разделение ответственности: Coordinator исполняет generic contracts, а входы, проверка и запуск принадлежат domain API и engine.

Внешний контракт Admin API:

- `GET /api/coordinator/workflows` — безопасные сводки workflow, фильтры `state`, `limit`, `before`;
- `GET /api/coordinator/workflows/permissions` — `can_retry_tasks` для того же credential;
- `GET /api/coordinator/workflows/{workflowID}` — метаданные и постраничный Task-граф с WorkerRef, типом `worker/join` и зависимостями;
- `POST /api/coordinator/workflows/{workflowID}/tasks/{taskID}/retry` — retry только failed Worker-task этого workflow.

Ответы имеют `Cache-Control: no-store` и `X-Content-Type-Options: nosniff`. API скрывает payload, result и внутренние storage IDs. UI — клиент этого API; серверная авторизация остаётся обязательной. Конфигурация использует `workflow_access_group`; значение `coordinator_jobs` пока сохранено как ключ уже выданных grants и не означает отдельную сущность выполнения. После явной data migration конфигурационный ключ группы можно переименовать отдельно.

## Приёмка

Требуются исполняемые сценарии: profile parsing и генерация; ранний отказ при отсутствующей зависимости/factory/Worker handler; порядок lifecycle; partial-start rollback; Core final activation; multi-instance lease и fingerprint mismatch; workflow idempotency, conflict, cancellation и restart; Worker-task claim; join без handler/lease; barrier для нескольких dependencies; разрешение sibling emission refs; отказ при цикле; атомарность; task retry, retention, deadline, lease takeover и fencing; API authorization, pagination, graph privacy и safe errors. Целевой порог — не менее 60% согласованных наборов входных данных для каждого модуля; знаменатель сначала фиксируется в [ACCEPTANCE-MATRIX.md](../../tests/ACCEPTANCE-MATRIX.md).

Product-specific triggers и handlers описываются в спецификациях соответствующих движков и workers, затем регистрируются как нейтральные Core operations. Это не расширяет словарь или таблицы Core.
