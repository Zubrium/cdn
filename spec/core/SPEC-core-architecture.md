# Core: слои, данные и автоматизация

Статус: целевой контракт, прямое расположение DDD-слоёв и Core/Coordinator базовые контракты реализованы и прошли отложенные Go/Atlas проверки. Общий функциональный порог по acceptance input sets всех продуктовых модулей остаётся отдельной открытой задачей; данный документ сам по себе его не подтверждает. Evidence: [QA_REPORT.md](../../docs/QA_REPORT.md) и [ACCEPTANCE-MATRIX.md](../../tests/ACCEPTANCE-MATRIX.md).

## Назначение и границы

Core — единственный владелец постоянного состояния продукта и единственная точка доступа к PostgreSQL, S3 и общему файловому cache. Все движки, workers и entrypoints получают ограниченный контракт Core и не получают pool, DSN, SQL-интерфейсы, S3-клиент или прямой доступ к внутреннему cache. Бизнес-правила движков остаются у движков; Core не содержит понятий `snapshot`, `route`, `content`, `form` или иных предметных моделей конкретного продукта. Кэши Core, их границы, инвалидация и bounded прогрева до Ready описаны в [SPEC-caching.md](../architecture/SPEC-caching.md).

Одна PostgreSQL database соответствует одной логической системе. Несколько экземпляров Core работают с этой же системой, координируются через PostgreSQL и используют одну конфигурационную эпоху. Core не поддерживает несколько независимых систем в одной database.

Схема применяется к пустой database. Существующие базы, данные и S3-объекты не сбрасываются, не переносятся и не изменяются. При несовместимой схеме startup прекращается до выполнения DDL. Перенос существующих данных оформляется отдельной задачей с отдельной процедурой.

## Go-слои

Все слои принадлежат одному Go module `core`; это пакеты с явным направлением зависимостей, а не отдельные Go modules.

```text
presentation ──> application ──> domain
infrastructure ────────────────> domain ports
infrastructure ────────────────> application ports
```

- `domain` содержит модели, ошибки и порты хранения/времени/событий. У моделей допустимы только конструкторы с проверкой и обработкой ошибок; политики доступа, сценарии использования и инфраструктурные проверки находятся в application. Сетевые клиенты, SQL-типы, JSONB-драйверы и зависимости от движков запрещены.
- `application` содержит команды, запросы, политики доступа, валидацию Core-контрактов, сценарии использования и транзакционные границы. Здесь же живёт Coordinator как внутренний сервис Core. Application зависит от доменных контрактов и не импортирует инфраструктурные реализации.
- `infrastructure` содержит реализации портов: PostgreSQL, S3, локальный cache (байты, метаданные и результаты авторизации), clock и другие адаптеры. Все SQL и вся работа с PostgreSQL выполняются только здесь. Все DDL и grants принадлежат Atlas migration set; application/runtime выполняет только операции над данными и запускает Atlas CLI по настроенной миграционной идентичности. Контракт локального cache — [SPEC-caching.md](../architecture/SPEC-caching.md).
- `presentation` предоставляет единый стабильный Core API, типизированные DTO/capabilities, lifecycle и простой composition/DI builder. Он связывает интерфейсы application с infrastructure. Engine получает только выданную ему capability и не может выбрать чужой `engine_id` из пользовательского запроса.

Импорты направлены внутрь: `domain` не импортирует другие слои; `application` импортирует `domain`; `infrastructure` реализует порты и может импортировать `domain`/`application`; Core adapters в `main` импортируют Core только через публичный пакет `core/presentation`. Engine/worker/Presentation modules могут импортировать независимый `pkg/contracts` для versioned operation contracts process-local bus; этот контрактный модуль не импортирует Core и не расширяет его facade. Вызовы между зарегистрированными runtime-компонентами, включая обращения к Core capabilities, проходят через bus и scoped adapter; прямые вызовы остаются только внутри модульной границы. Импорты Core в компоненты не дают им доступа к инфраструктурным реализациям. Четыре слоя расположены непосредственно в `core/{domain,application,infrastructure,presentation}`; каталогов `internal` внутри Core быть не должно. Core не импортирует пакеты конкретных движков, workers, Presentation или bus и не публикует capabilities, названные в честь конкретного потребителя. Компоненты, их descriptors и обработчики передаёт внешний composition root `main`; Core работает только с общими типами и непрозрачными payload/result. Между слоями нет циклических импортов. `make lint-core-boundary` разбирает Go imports через AST всех локальных Go-модулей репозитория, запрещает Core импортировать или объявлять зависимости на любой другой локальный модуль и ограничивает импорты потребителей Core пакетом `core/presentation`; отдельное правило разрешает импорт `pkg/contracts` только для bus contracts и запрещает через него обходить Core capability. Target также входит в `make lint` и `make vet`; acceptance-тесты проверяют физическую структуру и тот же контракт.

## Регистрация компонентов и жизненный цикл

Coordinator — часть Core application и не отдельный Go module или самостоятельный владелец БД. Core Coordinator регистрирует и координирует только общие типы компонентов: `engine`, `worker`, `entrypoint` и их durable operations. Статический composition root `main/` импортирует выбранные реализации, формирует operation allowlist выбранного profile и строит process-local dispatcher с общим JSON envelope (`operation_id`, `version`, `payload`). Каждый operation adapter строго декодирует payload в typed contract владельца. Dispatcher не является частью Core и не хранит данные; он маршрутизирует только зарегистрированные операции. Межкомпонентные синхронные вызовы и обращения к Core capabilities проходят через bus; durable workflow execution остаётся в Core Coordinator. Core работает только с общими типами и непрозрачными payload/result. Динамическая загрузка Go-кода и произвольные import paths не поддерживаются.

Descriptor компонента содержит только нейтральные данные: стабильный ID, вид компонента, версию контракта, объявленные операции, зависимости, обработчики задач и принадлежащие компоненту статичные access group keys. Core проверяет уникальность ID, валидность и уникальность group keys, зависимости, совместимость контрактов и доступность всех указанных factories до побочных действий. Engine-specific поля не попадают в Core descriptor, кроме непрозрачных входов и результатов зарегистрированной операции.

Запуск выполняется как `validate configuration and build profile → validate bus contracts/handlers → construct Core infrastructure → register components and scoped Core adapters → start components → bounded cache warm-up → check readiness → activate Coordinator workers`. Прогрев кэшей Core и Router до Ready, его режимы, лимиты и fail-closed правила описаны в [SPEC-caching.md](../architecture/SPEC-caching.md). Все статические imports и контракты проверяются до открытия входящих API; runtime message bus создаётся в `main`, принимает синхронные вызовы только выбранных и зарегистрированных операций и закрывает приём до shutdown компонентов. Ошибка регистрации, startup, обязательного прогрева или readiness закрывает систему до открытия входящих API. Остановка сначала прекращает новые запросы и Coordinator claims, ограниченно ждёт активные обработчики, затем закрывает компоненты в обратном порядке зависимостей и после них Core infrastructure.

Entrypoint является адаптером внешнего протокола и не получает инфраструктуру. HTTP listener, маршрутизация, сериализация внешних API и публичные маршруты принадлежат presentation-модулям. Их ingress adapters вызывают только заранее назначенные bus operations; клиент не выбирает operation ID и не передаёт bus envelope. Core выдаёт только scoped typed capabilities через adapters в `main`, которые вызываются по bus; внутри Core вызовы его слоёв остаются прямыми. Core может регистрировать нейтральные operation/handler contracts, но не знает HTTP paths, route IDs или структуры запроса отдельного движка.

## Долговечные workflow и promises

Core использует три разных понятия. **Worker** — статически зарегистрированный компонент, operation ID, версия контракта и handler. **Task** — durable-узел внутри графа. **Workflow** — граф Tasks с одним корневым Worker-task. `TaskExecutor` исполняет только Worker-tasks. Core обрабатывает join-tasks автоматически, без Worker, handler, lease и попыток. Отдельный `PromiseRef` или Go `Future` не вводится; результат читается через Core API после перезапуска и только в scope выданной capability.

Workflow стартует зарегистрированной engine operation с версией контракта, opaque JSON input и idempotency key. Workflow сохраняет `root_task_id` и терминальный `result`. Worker-task содержит `WorkerRef` и opaque JSON payload/result. Join-task содержит только payload, состояние и зависимости. Worker handler может атомарно породить дочерние Worker- и join-tasks, включая задачи других зарегистрированных компонентов. Coordinator проверяет регистрацию Worker и версии контрактов; движки не импортируют и не вызывают друг друга напрямую.

`TaskExecutor` передаёт handler-у durable `Task`, поэтому единственный авторитетный ID запуска — `Task.ID`. Идентификаторы выполнения не являются частью выбранного Worker или authority из payload; Worker использует `Task.ID` для собственной scoped state и reconciliation.

Правила выполнения:

1. Корневой Task всегда ссылается на зарегистрированный Worker. Task с `kind=worker` обязан иметь WorkerRef; Task с `kind=join` не имеет WorkerRef и не получает handler.
2. Дочерние Tasks и зависимости создаются только при успешной фиксации результата Worker-task. Фиксация результата, закрытие lease, добавление детей и рёбер зависимостей проходят в одной PostgreSQL-транзакции.
3. У каждой дочерней записи есть уникальный emission key в пределах родителя. Worker-task без явно заданных dependencies зависит от родителя; join-task обязан иметь одну или несколько dependencies. Ссылки допустимы по существующему Task ID или emission key другого ребёнка, создаваемого той же транзакцией.
4. Core отклоняет неизвестный/чужой Task, дублированные ссылки, превышение лимитов и циклический граф. Join становится succeeded, когда все его dependencies succeeded; join никогда не claims и не проходит через TaskExecutor.
5. Повтор запроса с тем же ключом идемпотентности и тем же fingerprint возвращает прежний workflow. Повтор ключа с другим input даёт conflict.
6. TaskExecutor ограничен явными настройками concurrency, timeout, lease, max attempts и retry backoff. Источник времени для lease — PostgreSQL. Просроченный lease можно перехватить; старый fencing token не позволяет записать результат или породить дочерние задачи.
7. Семантика Worker-task — at-least-once. Внешние side effects требуют идемпотентности или reconciliation в вызываемом компоненте; Core не обещает ровно один запуск процесса.
8. Ошибка Worker-task после исчерпания попыток переводит workflow в failed и отменяет оставшиеся queued/running Tasks. Вручную можно повторить только failed Worker-task; Core сбрасывает связанные с этим отказом cancelled Tasks и затем повторно оценивает join dependencies.
9. Workflow становится succeeded, когда root и все Tasks графа succeeded. Workflow result задаёт root Worker operation. Join result формируется Core и не содержит worker output.
10. Отмена workflow — явная команда: ожидающие задачи становятся cancelled; активные handlers получают отменённый context; поздняя фиксация после отмены отвергается. Сторонний side effect, уже совершённый до отмены, компенсирует или согласует владеющий им компонент.
11. Poll/subscribe уведомления являются подсказкой; PostgreSQL остаётся источником истины. Сбой уведомлений не меняет статусы, зависимости и право читать результат.
12. Размер input/result, общее число Tasks workflow, число дочерних Tasks на один шаг и срок хранения результатов задаются явно в operational config. Секреты, большие бинарные данные и файлы в JSON input/result запрещены; передаются opaque Core file references.

Граф строится динамически по мере выполнения Worker handlers; условная логика остаётся внутри зарегистрированной operation, а её результатом служит ограниченный набор дочерних Tasks. Произвольный пользовательский DSL, циклы графа и динамическая загрузка кода запрещены. Терминальный результат workflow выбирает его зарегистрированный root Worker и сохраняет в durable workflow record.

## PostgreSQL: схемы и таблицы

Core владеет одной Atlas migration history, которая создаёт и проверяет следующие bounded-context schemas. Внутри схемы имена таблиц короткие; префикс `core_` и повторение имени схемы в имени таблицы не используются.

| Schema | Таблицы первой версии | Владелец и назначение |
|---|---|---|
| `access` | `users`, `bootstrap`, `credentials`, `groups`, `grants` | Идентичности и прямые уровни доступа. Единственный тип пользователя; `kind` и ролей нет. Активные группы объявляются startup YAML и зарегистрированными при запуске движками. Отсутствующая/выключенная группа закрывает доступ, но не удаляет назначения и связи файлов. |
| `files` | `objects`, `memberships` | Неизменяемые байты, метаданные, физический S3 key, checksum, служебная revision и принадлежность статичным access groups. |
| `uploads` | `requests`, `staged` | Core idempotency/finalization state и непрозрачные временные S3 objects. Не хранит consumer-specific сессии, номера частей или предметные workflow. |
| `file_index` | `sets`, `members` | Поисковые наборы и membership; наборы не выдают доступ к файлу. |
| `engine_data` | `records` и `revision_seq` | Общая JSONB модель данных движков, ограниченная зарегистрированными engine/namespace capabilities. Собственных engine schemas/tables/migrations нет. |
| `coordination` | `runtime`, `instances`, `workflows`, `tasks`, `dependencies` | Единственная логическая система на БД, leases, durable workflow, динамические задачи и граф зависимостей. |

`access.sessions` хранит общий механизм отзываемых сессий использования одного конкретного credential. Запись привязана к `credential_id`; хранятся только хеш токена, principal через credential, сроки idle/absolute expiry и отметка отзыва. У сессий нет полей `browser`, `cookie`, CSRF или HTTP Origin. Внешний способ предъявления токена выбирает Presentation.

`access.groups` содержит группы по глобально уникальному каноническому имени (`group_key`); отдельные идентификаторы групп не задаются в YAML и не принимаются от клиента. Startup YAML объявляет группы, нужные оператору для интеграций и инфраструктурных сервисов, и может задавать bootstrap-уровень для первоначального администратора. Включённые движки объявляют принадлежащие им группы через нейтральные descriptors при регистрации профиля до запуска Core и reconciliation. Эти объявления образуют объединение: одинаковое имя из YAML и одного или нескольких движков обозначает одну и ту же группу. Явный YAML bootstrap-уровень, включая `0`, имеет приоритет над начальным engine уровнем `2`; engine-only группа получает уровень `2` при первом создании. Конфликтующих уровней нет, поскольку объявление движка не задаёт отдельное значение bootstrap-уровня. Имена не должны переиспользоваться для другого назначения.

Core активирует группу, если её объявляет хотя бы один источник. Если имя отсутствует среди объявлений всех источников, группа становится неактивной: её grants и file memberships сохраняются, но не дают доступа. Повторное объявление того же имени реактивирует прежнюю запись с сохранёнными grants и memberships; это не считается новым добавлением и не переинициализирует административные назначения. Новая группа создаётся при первом объявлении; bootstrap-назначение применяется по существующей политике первоначального администратора и явно заданного YAML-уровня. Конфликтующие bootstrap-уровни для одной группы в YAML недопустимы.

Все foreign keys, индексы, checks, владельцы объектов и grants явно заданы Atlas. Runtime login имеет только DML/sequence permissions, ограниченные перечисленными Core schemas; он не может менять DDL, grants, роли или Atlas history. Группа, отсутствующая во всех источниках конфигурации/регистрации, не удаляется каскадно и не даёт доступ.

Ранее существовавшие таблицы публикаций и Router targets удаляются из общей Core/coordination модели. Маршрут, активная цель и специфичные generation принадлежат Router и сохраняются в его namespace `engine_data.records`. Для безопасной активации Core предоставляет общую fenced/CAS-команду над зарегистрированной engine record: она сверяет текущую lease/fencing epoch и expected record revision в одной транзакции. Её параметры остаются нейтральными и не содержат `route_id`, `file_id` или publication-specific типов.

`system_id` удаляется из coordination rows: одно подключение к БД соответствует одной системе. Единая строка `coordination.runtime` сохраняет non-secret configuration fingerprint и монотонную epoch. Несовпадение fingerprint при активных leases запрещает startup; после остановки всех экземпляров новая конфигурация увеличивает epoch, а старые незавершённые задачи переводятся в безопасное терминальное состояние по правилам workflow.

## Оправдание полей и удаление лишнего

Каждое хранимое поле должно участвовать в проверяемом инварианте, API-представлении, CAS, аудитном требовании или восстановлении после сбоя. В частности:

- file object `revision` остаётся, поскольку защищает metadata CAS и помогает проверять cache invalidation; это не версия содержимого.
- upload request `request_hash` остаётся, чтобы один idempotency key нельзя было повторно применить к другому содержимому.
- `files.objects.updated_at`, `uploads.requests.updated_at` и `uploads.staged.updated_at` остаются как курсоры восстановления и очистки после неопределённого исхода S3/DB операции. У этих таблиц удалён неиспользуемый `created_at`.
- У `access.groups` удалён неиспользуемый `created_at`: активность группы определяется текущим объединением объявлений startup YAML и зарегистрированных движков, а отдельный аудит её создания не требуется.
- У `file_index.members` удалён неиспользуемый `added_at`: порядок и время добавления членства не входят в контракт поиска.
- У `coordination.runtime` удалён неиспользуемый `updated_at`: конфигурационное состояние полностью описывают fingerprint и epoch. У `coordination.instances` удалён дублирующий `heartbeat_at`: живость экземпляра задаёт единственный `lease_until`, продлеваемый Core.
- Worker-task lease, epoch и fencing token остаются для takeover и запрета записи устаревшего handler; join-task lease не создаётся.
- engine record `payload_version`, `index_data`, `revision` и expiry остаются, поскольку поддерживают версию сериализации движка, разрешённый поиск, CAS/ABA safety и TTL.
- `file_index.sets` не хранит `created_by`: набором управляет его static management group, а отдельного audit требования в v1 нет.

Поля сверены с SQL-запросами и контрактами; regression test запрещает повторное появление удалённых operational timestamps и проверяет recovery index. Ни имя, ни дублирование префикса не являются достаточным основанием удалить поле, которое поддерживает указанный инвариант. Нет неявных defaults для размера payload, cache, lease, concurrency и хранения workflow result; все параметры кэша и прогрева задаются явно согласно [SPEC-caching.md](../architecture/SPEC-caching.md).

## Единые инварианты хранилища

- Go-код не содержит SQL DDL (`CREATE`, `ALTER`, `DROP`, schema grants). Только Atlas migration files определяют схему.
- Engine данные изменяются только через scoped Core contract; каждый запрос и batch привязан к зарегистрированной capability, проверяет размеры и разрешённые поля, применяет CAS и параметризованные запросы.
- Байты файлов остаются в S3. Core повторно проверяет актуальные права перед чтением/commit; отсутствие PostgreSQL проверки закрывает доступ, независимо от локального cache. Кэш результатов авторизации, если включён, использует `revision + access_epoch` и не заменяет эту проверку; детали — [SPEC-caching.md](../architecture/SPEC-caching.md).
- S3 и PostgreSQL не считаются одной транзакцией. Операции состоят из повторяемых этапов с durable state/cleanup; неопределённый исход разрешается чтением состояния из PostgreSQL перед удалением байтов.
- Модули не обращаются к чужим namespaces и не выполняют междвижковые SQL JOIN. Межмодульные ссылки передаются как opaque IDs и разрешаются владельцем контракта.

## Приёмка

Схема принимается на свежей одноразовой PostgreSQL database: Atlas создаёт все schemas, таблицы, constraints, индексы и grants; Go sources не содержат DDL. Проверяются отказ при наличии неизвестных/неуправляемых database objects без изменения базы, отсутствие retired `core_*` таблиц и `system_id`, независимая проверка прав доступа, единое объединение групп YAML/engine registration по имени, повторное объявление одного имени как одной группы, деактивация при отсутствии всех источников и реактивация с сохранением grants/file memberships; также проверяются файлы, staging recovery, file sets, cross-engine record isolation, CAS/ABA и отказ при неизвестной capability.

Workflow acceptance включает: несколько экземпляров на одной database; одинаковый fingerprint допускает совместную работу, отличающийся при живом lease отвергается; регистрация обязательна и уникальна; повтор idempotency key; динамическое добавление зависимых задач; запрет цикла/чужого workflow/неизвестного handler; получение результатов после restart; child-task commit атомарен с parent completion; delayed execution, retry, terminal failure, cancellation, lease takeover, stale fencing rejection, DB outage fail-closed и очищение по настроенной retention policy.

Архитектурная приёмка проверяет границы пакетов: `make lint-core-boundary` подтверждает независимость Core от всех соседних модулей и единственную точку импорта потребителями `core/presentation`; ограниченный `pkg/contracts` доступен только для typed bus contracts и не даёт Core storage/capabilities. Acceptance-тесты дополнительно проверяют физическую структуру Core, процессный характер bus, отсутствие durable state в bus и отсутствие прямых/опосредованных engine-to-engine вызовов. Domain не импортирует framework/infra; application не импортирует SQL/AWS/pgx; только infrastructure содержит query/driver code; engine/worker/entrypoint imports не достигают infrastructure; `main` — единственная сборочная composition root и единственный продуктовый executable. Интеграционные проверки выполняются только на изолированных disposable DB/S3 и очищают только созданные для них идентификаторы.
