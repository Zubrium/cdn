# Core Coordinator и composition платформы

Статус: согласованный целевой контракт. Реализация переносится внутрь Core; этот документ не означает, что система проверена или готова к выпуску. Источник истины по слоям и таблицам — [SPEC-core-architecture.md](SPEC-core-architecture.md).

## Границы

Coordinator — часть Core application. У него нет отдельного Go module, подключения к БД или владельца данных. Он принимает только нейтральные component/operation descriptors и непрозрачные JSON payload/results. Он не содержит терминов или типов Website, Snapshot, Content, Form, Router, публикации или конкретного worker.

Core владеет идентичностями, workflow, задачами, группами файлов, хранилищем, кэшем и engine-data. Движки владеют своими моделями и бизнес-правилами. Workflow между движками связывает зарегистрированные операции через данные-контракты; движки не импортируют application packages друг друга и не вызывают друг друга напрямую.

## Сборка и регистрация

Build profile v1 задаёт списки `engines`, `workers`, `apis.admin` и `apis.public`. Core включён всегда; Coordinator не является отдельным компонентом профиля. `admin.coordinator` — только необязательный административный API. Профиль содержит идентификаторы из allowlist, а не Go import paths. Генератор разрешает список в детерминированный dependency-first `BuildPlan` и статические импорты.

Каждый runtime-компонент регистрирует фабрику, ID и зависимости. Нейтральный descriptor компонента содержит ID, вид (`engine`, `worker`, `entrypoint`), версию и объявленные операции. Worker handlers имеют стабильные operation ID и версию контракта. До открытия listener composition проверяет неизвестные/дублированные компоненты, порядок зависимостей, неполные factories и конфликты handler registration.

Профиль не загружает Go код динамически. Чтобы изменить состав бинарного файла, оператор собирает новый профиль и разворачивает его с тем же YAML и подходящей конфигурационной эпохой.

## Жизненный цикл

Порядок запуска:

1. Загрузить и проверить YAML и compiled build plan.
2. Проверить конфигурационные fingerprint и ограничения Core.
3. Запустить Core: проверить/создать схему только через Atlas, открыть DML и object-store adapters, зарегистрировать modules и получить instance lease.
4. Создать и запустить выбранные компоненты в dependency order.
5. Проверить readiness всех выбранных компонентов.
6. Последней фазой активировать Core worker pool; только после этого объявить приложение готовым и принимать запросы.

Ошибка любой фазы оставляет API закрытыми и запускает cleanup в обратном порядке. Shutdown прекращает новые claims, отменяет контексты handlers, ожидает кооперативного завершения в пределах заданного срока, освобождает instance lease и только затем закрывает Core database pool. Если ожидание истекло, база остаётся открытой, пока выполняющиеся handlers завершаются.

Каждая реплика использует одну PostgreSQL database на одну логическую систему. Одинаковый fingerprint допускает параллельные реплики. Иной fingerprint запрещён при живых leases; после остановки всех реплик повышается epoch, старые capabilities и fencing tokens становятся недействительными. Fingerprint не включает секреты и включает канонический build plan и runtime-конфигурацию.

## Durable workflows

Движок получает scoped `WorkflowClient` только для собственного зарегистрированного component ID. `Start` принимает operation ID, версию, ограниченный JSON-объект и idempotency key; Core сам создаёт workflow/task IDs. Клиент не может указать owner, epoch, engine ID, SQL или произвольный task handler. `Get` и `Cancel` ограничены тем же component scope и активной конфигурационной эпохой.

Worker handler получает ограниченный DTO задачи и может вернуть JSON result, безопасный error code и ограниченный список дочерних задач. Core атомарно записывает результат родителя и новые задачи. У каждой дочерней задачи есть уникальный в пределах workflow `emission_key`, зарегистрированная операция, payload, время доступности и зависимости на существующие задачи того же workflow. При пустом списке зависимостей дочерняя задача зависит от родителя. Неизвестные задачи, чужой workflow, дубликаты и превышение лимитов отклоняются.

Повтор idempotency key с тем же operation/version/input возвращает прежний workflow; иной input конфликтует. Исполнение — at-least-once. Lease и fencing token исключают позднюю фиксацию результата старым worker, но не отменяют уже совершённые внешние side effects. Движок обязан использовать идемпотентные операции или reconciliation.

Lease и время доступности сверяются по времени PostgreSQL. Живость экземпляра выражается его `lease_until` и продлевается через renew; отдельная колонка heartbeat не хранится. Retry с exponential backoff, число попыток, concurrency, timeout, максимальный размер payload/result, число задач workflow, число детей на шаг и retention задаются конфигурацией. Потеря lease одной задачи означает смену владельца; отказ durable store или lease всей Core instance делает приложение unhealthy. Ошибка handler хранится только как bounded machine code. Panic из handler изолируется и обрабатывается как ошибка.

Workflow и задачи долговечны между перезапусками. Workflow result доступен через scoped Core API. Отдельные `Future`, Go channel, promise service или пользовательский DSL в v1 не вводятся. Внешние уведомления — только подсказка; PostgreSQL остаётся источником истины.

## Административный контроль

Когда включён `admin.coordinator`, Core предоставляет узкую capability для страницы очереди. Она не возвращает payload/result и не выдаёт права на запуск или исполнение задач. HTTP adapter каждый раз проверяет bearer credential и прямой доступ к настроенной группе: уровень 1 разрешает чтение сводок, уровень 2 — ручной retry завершившейся ошибкой задачи. Пользовательская администрация и глобальные роли не обходят групповое разрешение.

Существующий внешний контракт Admin API v1:

- `GET /api/v2/coordinator/jobs` — безопасные сводки, фильтры `type`, `state`, `limit`, `before`, cursor по времени и UUID;
- `GET /api/v2/coordinator/jobs/permissions` — `can_retry` для того же credential;
- `POST /api/v2/coordinator/jobs/{jobID}/retry` — явный retry только допустимой failed-задачи.

Ответы имеют `Cache-Control: no-store` и `X-Content-Type-Options: nosniff`. UI — клиент этого API; серверная авторизация остаётся обязательной. Эти `jobs`-имена сохранены как внешний контракт страницы очереди; внутренняя модель исполнения — workflow и tasks.

## Приёмка

Требуются исполняемые сценарии: profile parsing и генерация; ранний отказ при отсутствующей зависимости/factory/handler; порядок lifecycle; partial-start rollback; Core final activation; multi-instance lease и fingerprint mismatch; workflow idempotency, conflict, cancellation и restart; динамические дети; атомарность; retry, retention, deadline, lease takeover и fencing; API authorization, pagination, privacy и safe errors. Целевой порог — не менее 60% согласованных наборов входных данных для каждого модуля; знаменатель сначала фиксируется в [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md).

Product-specific triggers и handlers описываются в спецификациях соответствующих движков и workers, затем регистрируются как нейтральные Core operations. Это не расширяет словарь или таблицы Core.
