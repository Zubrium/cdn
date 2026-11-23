# Единый контракт конкуренции и устойчивости к нагрузке

Статус: целевой контракт. Этот документ — центральный источник правил многопоточности, backpressure, изоляции пулов и ограничений конкуренции для Core, engines, workers, Presentation и composition. Локальные спецификации не должны определять собственную конкуренционную политику и обязаны ссылаться на этот документ. При расхождении приоритет у `SPEC-concurrency.md`.

Реализация описанных возможностей — отдельная волна работ. До её завершения текущий `main/config.example.yaml` остаётся конфигурацией текущего кода, а этот документ задаёт целевую схему, которую нужно синхронно внедрить в код, пример конфига, OpenAPI, тесты и acceptance-матрицу. Старый evidence из `docs/QA_REPORT.md` не подтверждает сценарии этого документа.

## 1. Область и модель процесса

- Продукт остаётся одним OS-процессом на branch. Один Router process обслуживает ровно одну настроенную branch; внешний proxy terminating TLS, rate limiting и caching не создаёт дополнительные инстансы для той же branch. Горизонтальное масштабирование внутри branch не допускается этим контрактом.
- Рост RPS достигается за счёт внутренней конкуренции: параллельных HTTP-goroutine, ограниченного числа in-flight операций, шардированных кэшей, coalescing cold misses, изолированных worker-пулов и корректно спроектированного DB-пула.
- Реальная конкуренция — goroutine HTTP-серверов, фоновых циклов, `TaskExecutor`-пулов и listen-цикла инвалидации. Число OS-потоков Go runtime не является целевым параметром и не настраивается в YAML.
- Process-local bus остаётся синхронным: handler вызывается в горутине вызывающего. Добавляется только semaphore конкуренции, но не асинхронная очередь и не client-видимые operation ID.
- Core остаётся единственным владельцем PostgreSQL, S3 и процессных кэшей. Межпроцессная координация идёт через PostgreSQL и `pg_notify`, а не через общую память.
- Публичный Core-контракт, credential-bound session contract и существующие внешние HTTP-пути не меняются. Новые внутренние типы и ограничения не становятся публичным API.

## 2. Инварианты

1. Все границы конкуренции заданы явно в YAML: лимиты in-flight, размеры пулов, timeout, interval, shard count, batch size. Скрытых runtime defaults нет.
2. Перегрузка не увеличивает задержку бесконечно: при исчерпании лимита запрос получает fail-fast `503` и метрику `rejected`. Очередь ожидания и «тихое» снижение качества ответа запрещены.
3. Авторизация выполняется до использования любого кэша. Coalescing и zero-copy не обходят проверку прав, revision и `access_epoch`.
4. Кэш остаётся подсказкой. При расхождении identity, revision, epoch или недоступности проверки выполняется перечитывание либо доступ закрывается.
5. Байты `core.bytes` при targets-режиме zero-copy являются immutable для внутренних вызывающих; мутация запрещена контрактом и проверяется тестами. Нарушение — programming error, а не Workflow данных.
6. Worker-пулы изолированы по классу задач. Насыщение одного пула не блокирует остальные; общее потребление соединений остаётся в рамках DB-бюджета.
7. DB-пул — жёсткий потолок. Конфигурация отклоняется до старта, если сумма зарезервированных соединений превышает `core.max_connections`.
8. `context` и deadline передаются через все слои. Отмена shutdown прерывает ожидание semaphore, coalescing и worker-задачи.
9. Метрики имеют низкую кардинальность: `scope`, `pool`, `result`, `reason`. В них нет file ID, principal ID, route ID, URL, query, locale, User-Agent и ключей кэша.
10. `/healthz` и `/readyz` не являются объектом действия этих лимитов: liveness отвечает всегда, readiness отражает только startup-готовность, но не transient overload.
11. Один кэш не разделяется между процессами через общую память. Инвалидация приходит через PostgreSQL; при reconnect или переполнении batch кэш очищается целиком.
12. Ни один новый лимит не меняет публичный HTTP-контракт и не добавляет compatibility aliases.

## 3. Реестр точек конкуренции

| Подсистема | Текущее поведение | Целевое улучшение | Конфигурация | Перегрузка |
| --- | --- | --- | --- | --- |
| HTTP Admin/Public | Один `http.Server` на listener, goroutine на запрос; нет in-flight лимита | Global in-flight semaphore + необязательные per-route лимиты из статической таблицы | `admin.concurrency.http.*`, `router.concurrency.http.*` | `503`, `no-store`, метрика `rejected` |
| Process-local bus | `Dispatch` вызывает handler в goroutine вызывающего; `active.WaitGroup` только для drain | Semaphore до вызова handler; per-operation overrides только из static allowlist | `bus.concurrency.*` | `ErrOverloaded` → `503` на ingress |
| `core.bytes` / `core.metadata` | Один `sync.Mutex` на LRU, копия `[]byte` на `Get`/`Put` | Шардированный LRU, RWMutex, sampled promotion, zero-copy `cache.View` для bytes | `core.concurrency.cache.*` | Нет отдельной overload-ошибки; лимиты памяти остаются в `core.cache.*` |
| Cold misses | Параллельные промахи вызывают независимые чтения | Coalescing по ключу: один fetcher, остальные ожидают с timeout | `core.concurrency.cache.coalesce.*` | При ошибке fetcher все ожидающие получают ошибку; stale не используется |
| Core storage/auth | Прямые вызовы PostgreSQL, ограничение только размером пула | Semaphores на reads и authorizations + operation timeout | `core.concurrency.storage.*` | `ErrOverloaded` → `503` |
| Loader | `loader.max_concurrent_commits` ограничивает только commit goroutines | Отдельные лимиты commit/session lookup, fail-fast вместо роста ожидания | `loader.concurrency.*` | `503` |
| Worker execution | Один `TaskExecutor` с `coordinator.worker_concurrency` | Отдельный `TaskExecutor` на класс: archive, media, prerender, publication | `worker.<class>.concurrency.*` | Класс не получает больше своего `limit`; соседние классы работают |
| Invalidation listener | Одна goroutine LISTEN, точечные delete, clear на reconnect | Батчинг уведомлений с явным окном и порогом; overflow → полный clear | `core.concurrency.invalidation.*` | Fail-closed: stale запись не используется |
| Admin Query | TanStack Query в памяти вкладки | Без изменений; persistent storage и service worker запрещены | `staleTime` в Admin SPA | Нет серверного backpressure |

## 4. HTTP ingress и backpressure

1. Каждый listener имеет собственный `max_in_flight`. Semaphore захватывается после валидации запроса и до вызова доменного handler; освобождается через `defer`.
2. При исчерпании лимита origin отвечает `503` с `Cache-Control: no-store` и стандартной формой ошибки. `Retry-After` не выставляется: решение о повторе принимает внешний proxy.
3. Разрешены необязательные per-route лимиты, но ключом может быть только идентификатор маршрута из startup-конфигурации. Посетитель не передаёт route ID, operation ID или Core ID.
4. Per-route override не может увеличить global `max_in_flight`; он только уменьшает доступную конкуренцию для конкретного маршрута.
5. `request_timeout` задаёт общий deadline запроса. Он короче или равен shutdown timeout, чтобы shutdown не ждал истёкших request context.
6. Liveness и readiness не учитываются в in-flight лимитах. Устойчивое превышение лимитов видно в метриках и alarm-ах deployer, но не меняет `/readyz` без реальной startup-ошибки.
7. Внешний proxy/deployer продолжает владеть IP rate limiting, TLS и caching policy и обязан сохранять `no-store`, ETag и `Vary` origin из [SPEC-caching.md](SPEC-caching.md).

## 5. Process-local bus

1. `Dispatcher.Dispatch` остаётся синхронным и вызывается в горутине вызывающего; stream capability и outlet-контракт не меняются.
2. Перед вызовом handler acquire semaphore `bus.concurrency.max_in_flight`. При отказе возвращается типизированная ошибка `ErrOverloaded`, которая на HTTP-границе мапится в `503`.
3. `bus.concurrency.per_operation.enabled` включает лимиты для operation ID из скомпилированного allowlist. Неизвестный ID конфиг не принимает. Посетитель не может выбрать operation ID через HTTP.
4. Per-operation лимит не может превышать global. Приоритетов и asyncio-очереди нет: перегрузка всегда означает быстрый отказ.
5. `Close` по-прежнему закрывает admission, ждёт активные вызовы через `active.WaitGroup` и завершается в shutdown timeout. Semaphore не заменяет этот drain.
6. Нагрузка, отмена и отказ отражаются в `zubrium_bus_in_flight` и `zubrium_concurrency_rejected_total{scope="BUS"}` без operation payload и caller identity в метриках.

## 6. Core cache: шардирование, zero-copy, coalescing

### 6.1. Шардирование

- `core.concurrency.cache.shards` задаёт число независимых LRU-шардов. Глобальный бюджет `core.cache.max_bytes` детерминированно распределяется по шардам; остаток от деления распределяется по порядковому номеру шарда.
- Каждый шард имеет собственный `sync.RWMutex`. Поиск идёт под `RLock`; повышение записи в LRU требует `Lock`. Чтобы не делать lock upgrade на каждом hit, `lru_promotion_interval` задаёт выборочное повышение (sampled promotion): `1` — каждый hit, `N > 1` — каждый N-й hit в шарде.
- Вытеснение остаётся bounded и детерминированным: при выборочном повышении порядок LRU приблизительный, но суммарный размер и число записей никогда не превышают бюджет.
- Шардирование применяется к `core.bytes` и `core.metadata`. `loader.sessions` использует тот же принцип, если включён; его лимиты остаются в `loader.concurrency.*`.

### 6.2. Immutable zero-copy bytes

- `core.bytes` возвращает внутренний тип `cache.View` вместо копии `[]byte`. Вызывающий получает доступ к неизменяемым байтам и обязан не мутировать их и не удерживать после completion operation.
- `core.metadata` остаётся value-копией небольшой структуры: выгода zero-copy там пренебрежима, а риск гонок выше.
- `cache.View` не является частью публичного Core HTTP-контракта. Он используется только между внутренними слоями Core и Router adapter, где вызывающий код известен и покрыт тестами.
- Контракт immutable проверяется статическими тестами и `-race` прогоном: мутация view или удержание после завершения запроса считается дефектом. Возврат сырого `[]byte` наружу адаптера запрещён.
- Увеличение размера object cache по-прежнему ограничено `core.cache.max_object_bytes`; zero-copy не снимает потоковую выдачу для больших объектов.

### 6.3. Coalescing cold misses

- При `coalesce.enabled: true` параллельные промахи с одинаковым ключом объединяются: один goroutine выполняет fetch, остальные ожидают результат с `coalesce.timeout`.
- `coalesce.max_waiters` ограничивает число ожидающих на ключ. Превышение даёт fail-fast `ErrOverloaded` одному из возможных waiters, без запроса к источнику.
- Coalescing применяется только к идемпотентным read path: `core.bytes`, `core.metadata`, `router.manifests`, `router.routes`. Записи, mutations и form sessions не объединяются.
- Результат сохраняется в кэш только после успешной проверки identity. Если лидер завершился ошибкой, все ожидающие получают ту же ошибку; частичный или stale результат не используется.
- Coalescing не заменяет авторизацию: каждый ожидающий запрос сохраняет свой credential scope, и результат не передаётся между principal с разными правами без повторной проверки.

### 6.4. Батчинг инвалидации

- Одна goroutine LISTEN читает `pg_notify`. Уведомления накапливаются в окне `invalidation.batch_window` и применяются пачками не больше `invalidation.batch_size`.
- Пачка применяется к затронутым шардам под их lock. При переполнении пачки, потере соединения или ошибке применения выполняется полный `Clear` всех локальных кэшей.
- Применение инвалидации не откладывается на неопределённый срок: `batch_window` — жёсткий верхний предел задержки. Метрика `zubrium_cache_invalidation_lag_seconds` остаётся обязательной.

## 7. Core storage, DB pool и авторизация

- `core.concurrency.storage.max_concurrent_reads` и `max_concurrent_authorizations` ограничивают число одновременно выполняемых read/authorize операций до обращения к pgxpool. При исчерпании — типизированная overload-ошибка и `503`, а не ожидание свободного соединения.
- `operation_timeout` задаёт deadline для одной storage/authorization операции. Он меньше general request timeout, чтобы оставался бюджет на response.
- Авторизация остаётся обязательной и выполняется до кэша. Возможный `core.authorization` cache подчиняется правилам `revision + access_epoch` из [SPEC-caching.md](SPEC-caching.md) и до отдельного согласования замороженного Core остаётся отключённым.
- Конфигурация валидируется до старта: `core.max_connections` должен быть не меньше `1 + 2 * loader.concurrency.max_concurrent_commits + 2 * sum(worker.<class>.concurrency.limit) + core.concurrency.storage.read_reserve`. Недостаточный пул — fail-fast, а не runtime-деградация.
- Read semaphores не резервируют отдельные соединения навсегда: они ограничивают число одновременно выполняемых операций внутри уже настроенного pgxpool. `read_reserve` задаёт явный запас для этих операций и maintenance.

## 8. Loader

- Плоское `loader.max_concurrent_commits` переносится в `loader.concurrency.max_concurrent_commits` одной волной с реализацией; compatibility aliases не добавляются.
- `loader.concurrency.max_concurrent_session_lookups` ограничивает параллельный доступ к process-local session LRU. При отказе — `503` без изменения состояния сессии.
- Mutation/recovery удаляет точный ключ session cache; hit по-прежнему проверяет principal, fingerprint и Core revision. Coalescing и zero-copy на upload-сессии не применяются.
- `commit_timeout` и limiter upload staging остаются частью Loader/File API контракта; новый semaphore не снимает эти проверки.

## 9. Изолированные worker-пулы

- Каждый класс задач получает собственный `TaskExecutor`: `archive`, `media`, `prerender`, `publication` (engine.website workflow). Один и тот же owner instance/config epoch используется всеми пулами, но handler-карты непересекаются.
- Текущий `TaskStore.Claim` не различает классы: несколько executors с одним owner'ом стали бы получать чужие задачи и завершать их `handler_unavailable`. Поэтому prerequisite изоляции — внутренний class-scoped claim с явным списком разрешённых `componentID`/классов в вызове. Это изменение внутреннего контракта Core Coordination; поскольку Core после credential-bound session contract считается замороженным, оно выполняется только после отдельного согласования и regression test. До этого момента сохраняется один `TaskExecutor`, а конфигурация из этого раздела не активируется.
- `worker.<class>.concurrency.enabled: false` означает, что handlers класса не регистрируются и задачи класса не claim-ятся. Задача остаётся в durable store до явного включения пула; fallback на другой пул запрещён.
- Параметры пула: `limit`, `poll_interval`, `task_timeout`, `lease_duration`. Общий `coordinator.max_attempts` и retry policy остаются глобальными, чтобы не менять workflow semantics.
- Пулы не заимствуют лимиты друг у друга и не имеют приоритетов. Насыщение `media` не увеличивает `prerender`, но всё равно учитывается в суммарном DB-бюджете из раздела 7.
- Каждый task по-прежнему имеет fencing token и lease. Один `TaskExecutor` не может завершить задачу другого класса; cross-pool heartbeat запрещён.
- Включённые пулы перечисляются в конфиге явно. Профиль full активирует только те классы, которые собраны в бинарник; profiles без worker не регистрируют handlers и не открывают лишние соединения.

## 10. Отказные и конкурентные случаи

| Случай | Требуемое поведение |
| --- | --- |
| HTTP-запросов больше `max_in_flight` | Избыточные получают `503` сразу, без очереди и роста p99; в метрике `rejected{scope="HTTP"}`. |
| Per-route лимит меньше global | Route может только уменьшить конкуренцию; попытка увеличить отклоняется валидацией конфига. |
| Bus in-flight исчерпан | `ErrOverloaded`, `503` на ingress, `active.WaitGroup` продолжает корректный drain; сообщения не складываются в скрытую очередь. |
| Параллельные cold misses | Один fetcher на ключ, остальные ожидают с timeout; при ошибке все получают ошибку, stale не отдаётся. |
| Ключ кэша мутирует внутренний вызывающий | Это programming error: тест/race-mode падает; публичный HTTP-контракт не меняется. |
| Шард под высокой нагрузкой | RLock/Lock competition ограничена числом шардов и `lru_promotion_interval`; бюджет шарда не превышен. |
| Invalidation flood | Пачки по `batch_size`/`batch_window`; overflow или reconnect → полный clear; stale запись недоступна. |
| Один worker-класс насыщен | Только его tasks ждут lease; соседние пулы продолжают работу; общий DB-бюджет не нарушен. |
| DB pool exhaustion | Semaphore storage/loader/worker отклоняет операцию до ожидания pgxpool; `503` вместо роста latency. |
| Shutdown во время load | Admission закрывается, новые запросы получают `503`, активные context отменяются по deadline, пулы и listener останавливаются в обратном порядке зависимостей. |
| Конфиг с неявными нулями | Отвергается до старта; скрытые значения не подставляются. |

## 11. YAML-контракт

Ниже — целевая схема. Все значения обязательны в `config.example.yaml`; приложение не подменяет пропуски скрытыми defaults. Переход с плоских `coordinator.worker_concurrency`, `loader.max_concurrent_commits` и неявных HTTP-лимитов делается одной волной с кодом; compatibility aliases не добавляются.

```yaml
admin:
  concurrency:
    http:
      max_in_flight: 128
      request_timeout: 30s

router:
  concurrency:
    http:
      max_in_flight: 512
      request_timeout: 30s
      per_route:
        enabled: false
        default_max_in_flight: 64
        overrides: []        # только ID маршрутов из startup-конфигурации
    routes:
      max_concurrent_resolutions: 64

bus:
  concurrency:
    max_in_flight: 256
    per_operation:
      enabled: false
      default_max_in_flight: 32
      overrides: []          # только operation ID из статического allowlist

core:
  # Бюджет по §7: 1 + 2*4 (loader) + 2*(2+2+1+2) (worker) + 4 (read_reserve) = 27; 32 оставляет запас.
  max_connections: 32
  concurrency:
    storage:
      max_concurrent_reads: 16
      max_concurrent_authorizations: 16
      operation_timeout: 10s
      read_reserve: 4
    cache:
      shards: 8
      lru_promotion_interval: 1
      coalesce:
        enabled: true
        max_waiters: 32
        timeout: 5s
    invalidation:
      batch_size: 256
      batch_window: 10ms

loader:
  concurrency:
    max_concurrent_commits: 4
    max_concurrent_session_lookups: 8

worker:
  archive:
    concurrency:
      enabled: true
      limit: 2
      poll_interval: 1s
      task_timeout: 30m
      lease_duration: 30s
  media:
    concurrency:
      enabled: true
      limit: 2
      poll_interval: 1s
      task_timeout: 30m
      lease_duration: 30s
  prerender:
    concurrency:
      enabled: true
      limit: 1
      poll_interval: 1s
      task_timeout: 30m
      lease_duration: 30s
  publication:
    concurrency:
      enabled: true
      limit: 2
      poll_interval: 1s
      task_timeout: 30m
      lease_duration: 30s
```

`enabled: false` на пуле или per-route/per-operation группе полностью исключает её из runtime path; метрики остаются видимыми с нулевыми счётчиками. Код отказа фиксирован контрактом (`503`) и не является настраиваемым полем: это защитная реакция системы, а не бизнес-политика ответа.

## 12. Observability

Добавляются в [SPEC-observability.md](../observability/SPEC-observability.md):

| Метрика | Тип | Метки |
| --- | --- | --- |
| `zubrium_concurrency_in_flight` | gauge | `scope=HTTP\|BUS\|STORAGE\|LOADER` |
| `zubrium_concurrency_rejected_total` | counter | `scope=HTTP\|BUS\|STORAGE\|LOADER`, `reason=GLOBAL\|PER_ROUTE\|PER_OPERATION\|DB_RESERVE` |
| `zubrium_cache_coalesced_total` | counter | `cache`, `result=JOINED\|LEADER\|FAILED` |
| `zubrium_cache_invalidation_batch_total` | counter | `result=APPLIED\|OVERFLOW\|CLEARED` |
| `zubrium_worker_pool_active` | gauge | `pool=ARCHIVE\|MEDIA\|PRERENDER\|PUBLICATION` |
| `zubrium_worker_pool_tasks_total` | counter | `pool`, `outcome=OK\|ERROR` |
| `zubrium_db_pool_usage` | gauge | без дополнительных меток |

Запрещены метки с route ID, operation ID, file ID, principal ID, URL, query, locale, User-Agent и ключами кэша. `/metrics` остаётся `no-store`.

## 13. Acceptance scenarios

Идентификаторы добавляются в `tests/ACCEPTANCE-MATRIX.md`; результаты не объявляются пройденными до реального прогона.

| ID | Сценарий |
| --- | --- |
| `CONC-001` | Нагрузка выше `router.concurrency.http.max_in_flight`: избыточные запросы получают `503` сразу, p99 активных запросов не растёт скачкообразно, метрика `rejected` увеличивается. |
| `CONC-002` | Per-route override пытается превысить global лимит: конфиг отвергается до старта; валидный override уменьшает конкуренцию только для статического route ID. |
| `CONC-003` | `bus.concurrency.max_in_flight` исчерпан: `ErrOverloaded` и `503`, без асинхронной очереди; при shutdown активные dispatch завершаются или отменяются по deadline. |
| `CONC-004` | Конфиг с `core.max_connections` меньше требуемого бюджета: fail-fast до открытия API; сообщение не содержит секретов. |
| `CONC-005` | Параллельные read-запросы к одному холодному ключу: метрика coalescing показывает одного leader и JOINED waiters, источник прочитан один раз. |
| `CONC-006` | `-race` и benchmark zero-copy path: аллокации на hit не растут линейно от размера объекта; мутация `cache.View` во внутреннем тесте детектируется как дефект. |
| `CONC-007` | Поток инвалидации превышает `batch_size`: пачка применяется в рамках `batch_window`, overflow вызывает полный clear, stale запись недоступна. |
| `CONC-008` | Насыщен пул `media`; задачи `prerender` и `publication` продолжают выполняться в пределах своих лимитов; суммарное число соединений не превышает бюджет. |
| `CONC-009` | Loader получает больше `max_concurrent_commits` и session lookups: избыточные операции получают `503`, состояние сессии и Core не меняется. |
| `CONC-010` | `/metrics` содержит все метрики раздела 12 только с разрешёнными низкокардинальными метками; `/metrics` отдаёт `no-store`. |
| `CONC-011` | Shutdown под нагрузкой: admission закрывается, часть активных операций завершается, часть отменяется по deadline, повторный `End`/release не создаёт дубликаты метрик. |
| `CONC-012` | Один Router process на branch сохраняется; proxy не создаёт дополнительные инстансы той же branch; новых публичных HTTP-путей и версионных префиксов нет. |

## 14. Связанные документы

- [SPEC-caching.md](SPEC-caching.md) — кэши, HTTP cache policy и прогрев до Ready.
- [SPEC-core-architecture.md](../core/SPEC-core-architecture.md) — границы Core и владение PostgreSQL/S3/cache.
- [SPEC-router-engine.md](../engines/router/SPEC-router-engine.md) — маршрутизация, один процесс на branch и proxy.
- [SPEC-module-data.md](../core/SPEC-module-data.md) — CAS revision как основа инвалидации.
- [SPEC-observability.md](../observability/SPEC-observability.md) — метрики и `/metrics`.
- [router.yaml](../presentation/openapi/router.yaml) — HTTP-контракт публичного Router.
