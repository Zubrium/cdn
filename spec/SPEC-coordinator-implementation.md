# Core и Coordinator: реализация и незакрытые пункты

Статус: implementation scope Core/Coordinator реализован и прошёл отложенные Go/Atlas проверки. Целевой контракт находится в [SPEC-core-architecture.md](SPEC-core-architecture.md) и [SPEC-coordinator-platform.md](SPEC-coordinator-platform.md); результаты и границы приведены в [QA_REPORT.md](../docs/QA_REPORT.md) и [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md).

## Пакеты и направления зависимостей

Core остаётся одним Go module `core` с DDD пакетами:

| Слой | Путь | Ответственность |
|---|---|---|
| Domain | `core/domain` | Модели, ошибки, безопасные constructors и repository ports |
| Application | `core/application` | File/identity policies, validation and use cases, Coordinator, workflow/task execution |
| Infrastructure | `core/infrastructure` | Cache, credentials, PostgreSQL и S3 адаптеры |
| Presentation | `core/presentation` | Единый Core facade, capabilities, registry и composition binding |

Пакет `core/application/coordination` владеет lifecycle/workflow алгоритмами и не импортирует pgx или PostgreSQL. `core/presentation` связывает application interfaces с инфраструктурными адаптерами и публикует aliases для нейтральных task/component contracts и уровней доступа. Все слои находятся непосредственно в `core/{domain,application,infrastructure,presentation}`; вложенный слой или каталог `internal` в Core запрещён. Проверка `TestCoreLayersLiveDirectlyUnderModuleRoot` закрепляет физическую структуру, а `TestExternalPackagesUseOnlyPublicCoreFacade` и AST boundary lint ограничивают production-импорты потребителей публичным `core/presentation`. SQL pool, cache и S3 client используются только Core.

Отдельный `coordinator/` Go module удалён. В composition root Coordinator больше не запускается как независимый lifecycle component: `coreComponent.Start` запускает Core Coordinator, а `ActivateAfterComponents` запускает durable workers после readiness остальных компонентов. PostgreSQL адаптер Coordinator находится в `core/infrastructure/postgres`; публичный фасад связывает его с application service.

## Новая БД

Единственный migration set — `core/infrastructure/postgres/migrations/`. Начальная схема создаёт `access`, `files`, `uploads`, `file_index`, `engine_data`, `coordination`; таблицы используют короткие имена `files.memberships` и `uploads.staged`. В ней нет `system_id`, product route/publication tables, per-engine CMS tables и старого общего `core` schema. `engine_data.records` остаётся общей scoped JSONB таблицей.

Старая база не конвертируется и не изменяется. Startup отказывает на legacy или unmanaged objects. Go код содержит DML, схема и grants описываются в Atlas SQL. `atlas.sum` относится только к новому baseline; финальная проверка закреплённым Atlas CLI и изолированная миграция фиксируются в QA evidence.

Ссылки на `system_id`, site publication и route targets в исторических спецификациях не являются целевой моделью. Новые product-specific данные принадлежат namespace движка в `engine_data.records`; Core не интерпретирует их JSON payload.

## Workflow subsystem

Единственный write-контракт для фоновой работы — scoped `WorkflowClient`; отдельный flat `JobScheduler`/enqueue интерфейс удалён как параллельный путь. Клиент принимает зарегистрированную operation/version, idempotency key и ограниченный JSON input, а Core генерирует идентификаторы и связывает owner, instance и epoch.

`coordination.workflows`, `tasks` и `dependencies` хранят долговечный граф и state. WorkerPool атомарно забирает задачи, продлевает lease, проверяет cancellation, timeout, fencing token, retry policy и bounded result. Успешное завершение может атомарно добавить дочерние задачи. PostgreSQL остаётся источником истины при нескольких репликах и restart.

Admin API сохраняет текущий маршрут `/api/v2/coordinator/jobs` для совместимости страницы оператора. Это read/retry capability над безопасными сводками задач, с прямой проверкой уровня группы в каждом запросе. Оно не выдаёт Worker или WorkflowClient capability.

## Реализованный scope этого прохода

- убрать дублирующие flat enqueue API и оставить единый scoped workflow submission;
- включить Coordinator в Core startup/shutdown/readiness и запускать durable worker pool после готовности остальных компонентов;
- защитить одинаковые конкурентные idempotency keys одной durable workflow и конфликтовать при изменённом входе;
- связать регистрацию fatal-error monitor и shutdown общим runtime lock;
- переместить build profile, application, infrastructure, presentation и тесты в новую структуру без отдельного `coordinator/` module и без `internal`-каталогов в `core/`;
- синхронизировать новую схему Atlas и Core facade с generic workflow/task контрактами;
- добавить приёмочные сценарии для задержки, восстановления после рестарта, takeover/fencing, зависимостей, retention, исчерпания попыток, недоступности store и прямого расположения Core layers.

## Незакрытое

- production task handlers и runtime factories Website, Content, Forms, Router, Archive, Media и Prerender остаются отдельными интеграциями; Core принимает только нейтральные operation contracts и непрозрачные payload/results;
- общий функциональный процент по всем модулям продукта нельзя выводить из Go line coverage. Целевые acceptance input sets для модулей вне Core/Coordinator остаются в [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md) и не считаются закрытыми этим проходом.

## Команды для повторяемой проверки

Повторная проверка проводится последовательно против одной документированной одноразовой среды:

1. форматирование Go и миграционный checksum;
2. Go overlay unit suite и `go vet`;
3. свежая disposable PostgreSQL migration, grants и workflow concurrency, delayed scheduling, dependency, restart, retry, retention, lease takeover/fencing, and max-attempt tests;
4. изолированный S3 сценарий для file operations;
5. profile generation и startup readiness/rollback;
6. Admin API и браузерные сценарии только с disposable данными;
7. `git diff --check`, проверка прямой структуры слоёв под `core/`, package boundary scans и обновление QA evidence.

Каждая проверка записывается с точной командой, окружением и результатом в [TODO-admin-snapshots-content-files.md](../docs/TODO-admin-snapshots-content-files.md) и применимом QA отчёте. Ни unit tests на fake, ни статическая схема не заменяют PostgreSQL concurrency проверки.
