# SPEC: Observability (логи, метрики, трейсы)

> Статус: проектный контракт. Текущая реализация создаётся отдельно; наличие пакета,
> конфиг-ключа или примера compose не доказывает, что сценарии из
> [acceptance matrix](../../tests/ACCEPTANCE-MATRIX.md) исполнены. Результаты реальных
> прогонов фиксируются в [QA report](../../docs/QA_REPORT.md), незавершённые работы — в
> [TODO](../../docs/TODO-observability.md).

## 1. Цели и границы

Observability даёт единую, безопасную для продакшена трассировку запросов, фоновых
задач и вызовов компонентов поверх уже существующих границ Modules/Bus/HTTP. Система
должна отвечать на вопросы: какой запрос или какая задача была выполнена, через какие компоненты прошла,
какой operation вызвал ошибку, какова задержка и каков исход.

Сигналы:

- **логи** — структурированные события с уровнем `debug|info|warn|error`;
- **метрики** — счётчики, гистограммы и gauge с низкой кардинальностью меток;
- **трейсы** — span'ы, связанные общим `trace_id` по W3C Trace Context.

Границы ответственности:

1. `main` (composition root) один раз задаёт базовую identity процесса:
   `service.name`, `service.version`, `service.instance.id` и `deployment.profile`.
   Бэкенд задаёт `observability.mode`.
2. На границах **HTTP ingress**, **bus dispatch**, **worker task** и
   **component lifecycle** доверенный адаптер обогащает identity полями
   `module.kind`, `module.id`, `component.id`, `operation.id` и связывает сигнал с
   активным `trace_id`/`span_id`. Дочерние вызовы наследуют identity и trace через
   `context`, если явно не заменены на другой доверенный компонент.
3. Бизнес-код модулей (`core/`, `engines/`, `workers/`, `presentation/`) **не
   instruments** в смысле самостоятельного выбора бэкенда, формата экспорта или
   базовой identity. Он может только передавать типизированные, безопасные атрибуты
   через контракт наблюдателя, когда это часть согласованного контракта границы.
4. Observability не является источником бизнес-решений: отказ или переполнение
   телеметрии не должен менять HTTP-статус, результат операции или durable-состояние.
5. Observability не заменяет аудит доступа, Forms audit, durable workflow records и
   OpenAPI-контракты; это отдельные механизмы с отдельными гарантиями.

Допустимые значения `module.kind`: `core`, `engine`, `worker`, `presentation`,
`composition`, `bus`. Значение должно быть фиксированным идентификатором из
зарегистрированного build profile, а не произвольной строкой запроса.

## 2. Бэкенды и выбор режима

Режим выбирается единственным ключом `observability.mode` в конфиге приложения:

| Режим   | Поведение                                                                                                                                                                   |
| ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `noop`  | Сигналы не экспортируются; контракт остаётся доступным, trace context для корреляции может создаваться локально. Используется по умолчанию и для тестов без telemetry sink. |
| `file`  | Только stdlib. Три JSONL-файла в `observability.file.directory`: `logs.jsonl`, `spans.jsonl`, `metrics.jsonl`. Синхронная запись под mutex, ротация по размеру и числу файлов. |
| `otel`  | OTLP/HTTP в OTel Collector; Collector экспортирует в OpenObserve. Экспорт асинхронный и не блокирует обработчик запроса. Локальный `/metrics` сохраняется для отладки.        |

### 2.1 `file`

- Каждая строка — один JSON-объект с `timestamp` (UTC, RFC3339Nano), `signal`
  (`log`, `span`, `metric`) и обязательного набора атрибутов из раздела 3.
- Файлы создаются с режимом `0600`, каталог — `0700`. Не записывать в каталог,
  доступный публичному runtime или другим пользователям контейнера.
- Ротация: при превышении `max_file_bytes` активный файл переименовывается в
  `<name>.jsonl.1`, существующие индексы сдвигаются до `max_files`; записи старше
  `max_files` удаляются. Диапазон `max_file_bytes`: 1024..1073741824, `max_files`: 1..16.
- Ошибка открытия каталога/файла на старте — fail-fast: конфигурация считается
  невалидной, процесс не поднимается. Ошибка отдельной записи во время работы не
  должна ронять процесс и не должна изменять бизнес-ответ; факт потери сигнала
  должен быть observable через внутренний счётчик/статус, если он реализован.
- Порядок строк внутри файла соответствует порядку записи; между тремя файлами
  корреляция идёт по `trace_id`, а не по позиции.

### 2.2 `otel` и OpenObserve

- Приложение отправляет OTLP/HTTP на `observability.otlp.endpoint` (в compose —
  `http://otel-collector:4318`). `insecure: true` допустим только внутри изолированной
  docker-сети; наружный трафик требует TLS.
- Заголовки экспорта задаются только через `observability.otlp.headers_env`: переменная
  окружения содержит список `key=value` через запятую. Секреты (Basic auth и т.п.)
  существуют только в окружении и никогда не попадают в YAML, логи и ответы API.
- OTel Collector в `deploy/observability/otel-collector.yaml` принимает OTLP gRPC
  (`4317`) и HTTP (`4318`), применяет `memory_limiter` и `batch`, дублирует сигналы в
  `debug` exporter и отправляет в OpenObserve через `otlphttp` с заголовками
  `Authorization: Basic <base64>` и `X-OQ-ORG-ID: default`.
- Collector — буфер, не надёжная очередь: при его недоступности SDK должен продолжать
  работу, отбрасывать переполненные батчи и не блокировать приложение. Потеря
  телеметрии фиксируется в логах/внутренних метриках Collector, но не меняет код
  ответа продукта.
- OpenObserve хранит данные в named volume; БД Core и S3 objects этого стека
  одноразовые (tmpfs) и не являются долгосрочным хранилищем телеметрии.

## 3. Контракт атрибутов

### 3.1 Обязательные поля

Каждая запись лога, span и каждая метрическая выборка должна нести после санитизации:

| Атрибут              | Тип    | Источник                                                                |
| -------------------- | ------ | --------------------------------------------------------------------------- |
| `service.name`       | string | composition root (`zubrium` или зарегистрированное имя процесса)            |
| `service.version`    | string | build identity / `observability.service_version`                             |
| `service.instance.id`| string | уникальный instance ID процесса (`observability.instance_id`)                |
| `deployment.profile` | string | выбранный build profile (`full`, `router`, `development-fixtures`, ...)      |
| `module.kind`        | string | одно из шести фиксированных значений раздела 1                              |
| `module.id`          | string | зарегистрированный модуль, например `engine.forms`, `worker.media`           |
| `component.id`       | string | зарегистрированный компонент, например `composition.retirement`             |
| `operation.id`       | string | стабильный operation ID из static allowlist, например `engine.website/build-page/v1` |
| `trace_id`           | string | 32 hex-символа W3C trace ID                                                  |
| `span_id`            | string | 16 hex-символов W3C span ID текущего span                                    |

Для метрик `trace_id`/`span_id` — exemplar конкретной выборки: в `file` режиме они
записываются в ту же строку `metrics.jsonl`, в `otel` режиме передаются через
active span context и экспортируются OTel SDK. Они **не** становятся метками
метрической серии. Локальный Prometheus text format остаётся отладочным срезом
без exemplars.

Дополнительно для HTTP: `http.method`, `http.route` (шаблон маршрута, **не** raw URL и
не query string), `http.status_code`; для ошибок: `outcome=error`, `error.type`,
`error.message` (только безопасная, санитизированная строка). Высококардинальные
метки (ID пользователя, ID формы, токены, URL с параметрами) в метрики не попадают.

### 3.2 Typed attributes и redaction

- Используются только типизированные конструкторы: строковый, целочисленный,
  вещественный, булевый. Сырые `map[string]any` из HTTP/bus в телеметрию не принимаются.
- Ключи нормализуются ( нижний регистр, `.`, `_`, `-`), обрезаются до 96 символов;
  строковые значения обрезаются до 256 символов и очищаются от управляющих символов.
- Ключи, содержащие `authorization`, `cookie`, `token`, `password`, `secret`,
  `credential`, `session`, `body`, `payload`, `values`, `query`, `form`, `response`,
  `apikey` или `privatekey` (включая производные формы вроде `user.token` и
  `request-body`), отклоняются целиком. Значения дополнительно проверяются по
  шаблонам Bearer/Basic, JWT и `key=value` пар credential/cookie: даже под
  безопасным ключом нельзя передать токен, cookie, credential, тело формы, PII или
  произвольный raw request/response body. На основании
  [SPEC-caching.md](../architecture/SPEC-caching.md) дополнительно отклоняются
  производные ключи `cache.key`, `cache_key`, `file.id`, `principal.id`, `url`,
  `uri`, `locale`, `user.agent`, `user.email` и `username`: ключи кэша, file ID,
  principal ID, URL, query, locale и пользовательские данные не попадают ни в
  логи, ни в метрические метки.
- `error.message` не должен содержать PII: бизнес-код обязан возвращать типизированную
  ошибку с безопасным классом, а не текст с данными посетителя.
- trace_id/span_id — корреляционные идентификаторы, а не секреты; они могут
  возвращаться клиенту в заголовке ответа и печататься в логах.

## 4. Метрики

Имена фиксированы и не версионируются в URL:

| Метрика                                  | Тип      | Назначение и базовые метки                                                     |
| ---------------------------------------- | -------- | -------------------------------------------------------------------------------- |
| `zubrium_http_server_requests_total`     | counter  | HTTP-запросы; `http.method`, `http.route`, `http.status_code`                    |
| `zubrium_http_server_duration_seconds`   | histogram| Длительность HTTP; те же метки; bucket-ы 0.001..30 s и `+Inf`                    |
| `zubrium_operation_total`                | counter  | Завершённые `Observer.Operation`; `module.*`, `component.id`, `operation.id`, `outcome` |
| `zubrium_operation_duration_seconds`     | histogram| Длительность operation; те же метки                                             |
| `zubrium_bus_dispatch_total`             | counter  | Диспетчеризация через process-local bus; `module.kind=bus`, `operation.id`, `outcome` |
| `zubrium_bus_dispatch_duration_seconds`  | histogram| Длительность bus dispatch                                                       |
| `zubrium_worker_task_total`              | counter  | Завершённые worker task; `module.kind=worker`, `module.id`, `component.id`, `operation.id`, `outcome` |
| `zubrium_worker_task_duration_seconds`   | histogram| Длительность worker task                                                        |
| `zubrium_cache_operations_total`         | counter  | Операции кэша; `cache`, `result=HIT\|MISS\|EVICTION\|INVALIDATED`                 |
| `zubrium_cache_entries`                  | gauge    | Число записей; `cache`                                                          |
| `zubrium_cache_bytes`                    | gauge    | Занятые байты; `cache`                                                          |
| `zubrium_cache_warmup_keys_total`        | counter  | Ключи прогрева; `cache`, `tier=REQUIRED\|OPTIONAL`, `result=WARMED\|FAILED\|SKIPPED` |
| `zubrium_cache_warmup_duration_seconds`  | histogram| Длительность прогрева; `cache`                                                 |
| `zubrium_cache_invalidation_lag_seconds` | gauge    | Задержка инвалидации; `cache`                                                  |
| `zubrium_concurrency_in_flight`          | gauge    | Активные операции; `scope=HTTP\|BUS\|STORAGE\|LOADER`                            |
| `zubrium_concurrency_rejected_total`     | counter  | Отказы перегрузки; `scope`, `reason=GLOBAL\|PER_ROUTE\|PER_OPERATION\|DB_RESERVE` |
| `zubrium_cache_coalesced_total`          | counter  | Coalescing cold misses; `cache`, `result=JOINED\|LEADER\|FAILED`                  |
| `zubrium_cache_invalidation_batch_total` | counter  | Батчи инвалидации; `result=APPLIED\|OVERFLOW\|CLEARED`                           |
| `zubrium_worker_pool_active`             | gauge    | Активные задачи пула; `pool=ARCHIVE\|MEDIA\|PRERENDER\|PUBLICATION`              |
| `zubrium_worker_pool_tasks_total`        | counter  | Завершённые задачи пула; `pool`, `outcome=OK\|ERROR`                             |
| `zubrium_db_pool_usage`                  | gauge    | Использование пула PostgreSQL; без дополнительных меток                         |

Метрики конкуренции, multipool и coalescing определены в [SPEC-concurrency.md](../architecture/SPEC-concurrency.md)
и перечислены здесь как обязательный контракт будущей волны. Метки `scope`, `pool`,
`result` и `reason` низкокардинальны; метрики не содержат route ID, operation ID,
file ID, principal ID, URL, query, locale и User-Agent.

Метрики кэша и прогрева определены в [SPEC-caching.md](../architecture/SPEC-caching.md)
и перечислены здесь как обязательный контракт будущей волны. Наблюдатель поддерживает
тип gauge и принимает `SetGauge`, но кэши ещё не испускают эти серии: реализация
кэшей, конфигурация warm-up и сценарии `CACHE-001..CACHE-012` выполняются отдельно.
Наличие поддержки gauge не означает, что метрики кэша прошли acceptance.
- Метки метрик фиксированы и низкокардинальны: identity, `outcome` и базовые поля
  из таблицы. Идентификаторы task/workflow/record, file ID, principal ID, URL, query,
  locale и значения ответов остаются только в span/log-записи и не попадают в метрические серии.
- Gauge хранит последнее записанное значение для набора меток и не накапливается;
  сброс/пересчёт кэша обновляет ту же серию, не создавая новую.
- Локальный endpoint `/metrics` отдаёт Prometheus text format
  (`text/plain; version=0.0.4; charset=utf-8`, `Cache-Control: no-store`) в обоих
  активных режимах; это отладочный snapshot процесса, а не замена долгосрочного push.
- В `otel` режиме метрики дополнительно push-ятся в OpenObserve через Collector.
- Health-check'и (`/healthz`, `/readyz`) учитываются в метриках, но не создают
  «http request completed» лог, чтобы не засорять поток; trace/span для них
  создаваться могут. Это поведение проверяется `OBS-001`.

## 5. Трассировка

- На HTTP ingress принимается только валидный `traceparent` формата W3C `00`:
  32-hex `trace-id`, 16-hex `span-id`, 2-hex flags, ненулевые ID. Невалидный или
  отсутствующий заголовок не является ошибкой запроса: создаётся новый root trace,
  запрос обрабатывается обычным порядком (`OBS-007`).
- Span'ы: `http.server` (на границе HTTP), `bus.dispatch` (на вызове через bus),
  `worker.task` (на execution boundaries воркера) и `component.lifecycle` (создание
  компонента композицией; полный lifecycle `Compose` отдельно фиксируется тем же
  именем в composition root). Имена стабильны; параметры передаются атрибутами.
- Внутренние вызовы получают дочерний span через `context` с тем же `trace_id`;
  `parent_span_id` фиксирует иерархию. Correlation id логов и метрик — `trace_id`.
- Клиенту возвращаются `X-Request-ID` (текущий `span_id`) и `X-Trace-ID`
  (`trace_id`) для корреляции (`OBS-003`). Оба значения — не секреты.
- Span закрывается ровно один раз; `End(err)` пишет `span.status=ok|error`, а также
  `error.type`/`error.message` при ошибке. Несовпадение `expectedRevision` даёт
  HTTP 409: в span — `http.status_code=409`, `span.status=error`, `outcome=error`,
  в логе — `level=warn` или выше; тела запроса и значения полей формы в атрибуты не попадают
  (`OBS-002`).

## 6. Отказные и конкурентные случаи

| Случай | Требуемое поведение |
| ------ | ------------------- |
| Collector недоступен / таймаут экспорта | Приложение продолжает работать, HTTP/bus/worker не падают из-за telemetry; экспорт асинхронный с ограниченным timeout и bounded queue. |
| Переполнение файлов | Ротация не теряет активную запись; при исчерпании `max_files` самые старые файлы удаляются, процесс продолжает работу. Ошибка ротации не меняет бизнес-ответ. |
| Невалидный `traceparent` | Игнорируется, создаётся новый root trace; 400 не возвращается. |
| `409 expectedRevision` | Видно как `http.status_code=409`, `outcome=error`, `span.status=error`; без утечки bodies/credentials. |
| Параллельные запросы | Observer потокобезопасен; запись в JSONL сериализована; метрики агрегируются без гонок; `trace_id` разных запросов не смешиваются. |
| Сброс/завершение компонента | При `Close` rollback/commit telemetry не меняет бизнес-результат; открытые span'ы закрываются, буферы сбрасываются в рамках shutdown timeout; повторный `End` не создаёт дубликат. |
| Двойной старт/duplicate operation ID | Композиция отклоняет неоднозначную identity; duplicates не сливаются в одну серию метрик. |
| Ошибка конфигурации | Неизвестный `mode`, пустой directory/endpoint, некорректный `headers_env`, невалидный `min_level` → fail-fast до старта приложения. |

Отказные случаи перегрузки, backpressure и shutdown под нагрузкой описаны в [SPEC-concurrency.md](../architecture/SPEC-concurrency.md); observer должен фиксировать их через метрики `zubrium_concurrency_*` и `zubrium_worker_pool_*`, не раскрывая идентификаторы запросов и principal.

## 7. Конфигурация

Пример блока (секреты — только имена переменных окружения; значения не приводятся):

```yaml
observability:
  mode: otel                 # noop | file | otel
  min_level: info            # debug | info | warn | error
  service_version: dev       # из build identity
  instance_id: zubrium-obs-1 # уникален для процесса
  file:
    directory: /var/lib/zubrium/observability
    max_file_bytes: 16777216
    max_files: 4
  otlp:
    endpoint: http://otel-collector:4318
    insecure: true           # только внутри docker network
    headers_env: ZUBRIUM_OBS_OTLP_HEADERS
    timeout: 5s
```

`deployment.profile` не настраивается в YAML: его задаёт composition root из
фактически скомпилированного build profile (`full`, `core`, `router`,
`development-fixtures`). Это исключает расхождение между конфигом и бинарником.
Рабочий пример для compose — [`main/config.observability.yaml.example`](../../main/config.observability.yaml.example).

## 8. Покрытие acceptance-сценариев

Исполняемые сценарии `OBS-001..OBS-010` определены в
[`tests/ACCEPTANCE-MATRIX.md`](../../tests/ACCEPTANCE-MATRIX.md). До реального прогона
все они имеют статус «планируется/ожидает проверки». Минимальный набор доказательств:

- конфиг проходит `--check-config` и поднимается в isolate-стеке;
- файловый режим создаёт три JSONL-файла с обязательными полями;
- OTLP-режим даёт записи в OpenObserve в трёх signal pipelines;
- один и тот же `trace_id` найден в ответе, логе и span'е;
- 409 conflict и невалидный traceparent воспроизведены как отдельные input/result
  сценарии;
- отказ Collector не меняет доступность приложения;
- проверки записаны в QA report с точными командами и ограничениями.
