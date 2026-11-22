# Единый контракт кэширования

Статус: целевой контракт. Этот документ — центральный источник правил кэширования для Core, engines, workers, Presentation и deploy-окружения. Локальные спецификации не должны определять собственную политику кэша и обязаны ссылаться на этот документ. При расхождении приоритет у `SPEC-caching.md`.

Механизмы потокобезопасности, шардирования, zero-copy, coalescing cold misses, батчинга инвалидации и ограничения параллелизма перечислены в [SPEC-concurrency.md](SPEC-concurrency.md). Настоящий документ задаёт инварианты кэша и HTTP-политику; `SPEC-concurrency.md` задаёт способ их выполнения под нагрузкой. При расхождении в механизмах приоритет у `SPEC-concurrency.md`, при расхождении в инвариантах доступа — у настоящего документа.

Реализация описанных возможностей — отдельная волна работ. До её завершения текущий `main/config.example.yaml` остаётся конфигурацией текущего кода, а этот документ задаёт целевую схему, которую нужно синхронно внедрить в код, пример конфига, OpenAPI, тесты и acceptance-матрицу. Старый evidence из `docs/QA_REPORT.md` не подтверждает сценарии этого документа.

## 1. Область и владение

- Core — единственный владелец PostgreSQL, S3 и процессного файлового кэша. Core владеет кэшами `core.bytes`, `core.metadata` и `core.authorization`.
- Router владеет кэшами `router.manifests` и `router.routes`. Router не кэширует файловые байты Core и не принимает Core ID от посетителя.
- Loader владеет process-local LRU upload-сессий. Он остаётся подсказкой и проверяет Core revision и fingerprint при каждом попадании.
- Admin SPA использует TanStack Query в памяти браузера. Не разрешены `persistQueryClient`, service worker, Cache Storage и хранение credentials/session tokens.
- Внешний caching proxy/CDN принадлежит deployer. Router и Core не выбирают и не настраивают прокси, но прокси обязан подчиняться заголовкам origin из этого контракта.
- Документ не вводит новых публичных HTTP-путей, версионных префиксов и compatibility aliases. Публичный Core контракт, включая credential-bound session contract, остаётся замороженным; изменения внутренние для Core, Router и composition.

## 2. Инварианты

1. Кэш — подсказка. Авторитетный источник — PostgreSQL/Core metadata, revision, CAS и file bytes. Кэш никогда не является источником истины для прав или содержимого.
2. Авторизация выполняется до использования кэша. Отсутствие свежей проверки прав закрывает доступ, даже при наличии кэшированных байт или метаданных.
3. Кэш не хранит решение доступа как самостоятельный факт. Запись `core.authorization` содержит revision файла и `access_epoch`; она используется только если оба значения совпадают с актуальным состоянием БД.
4. Группы фильтруются из того же снимка `Authorize`, что и факт доступа. Разрешения одного principal не попадают в ответ другому.
5. Устаревший кэш никогда не используется с fail-open: при расхождении revision, epoch или authorization выполняется перечитывание из источника либо доступ закрывается.
6. Ошибки, частичные ответы, fallback, draft/submission/private media и авторизационные ответы не кэшируются.
7. Все границы заданы явно в YAML: размер в байтах, число записей, TTL, parallelism, timeout, retries, source toggles и target toggles. Скрытых runtime defaults нет.
8. Инвалидация транзакционна: `pg_notify` выполняется в той же транзакции, что и изменение; на reconnect кэш очищается целиком, чтобы не использовать пропущенные уведомления.
9. Ключи кэша, file ID, principal ID, URL, query, locale и пользовательские данные не пишутся в логи и метрики. Метрики имеют низкую кардинальность.
10. Публичные ответы по умолчанию `no-store`. Shared cache может получить TTL только для representation с полностью определённой identity.
11. Сжатие не меняет ETag identity и не конфликтует с Range: range-запросы отдаются в identity-представлении.
12. Прогрев не меняет семантику авторизации: он только заранее заполняет существующий bounded cache через уже разрешённые вызовы.

## 3. Реестр кэшей

| Кэш | Владелец | Ключ | Bounds / TTL | Инвалидация | Потокобезопасность |
| --- | --- | --- | --- | --- | --- |
| `core.bytes` | Core | `object.ID` | `core.cache.max_bytes`, `core.cache.max_object_bytes`, `core.cache.ttl` | `pg_notify('core_cache_invalidation', <id>)`, `'*'` или полная очистка на reconnect | `sync.Mutex`, LRU, копия `[]byte` на `Get`/`Put` |
| `core.metadata` | Core | `core-metadata:<object.ID>` | те же лимиты | тот же канал; плюс проверка `Revision`, `Ready`, `ID` на попадании | тот же bounded LRU |
| `core.authorization` | Core | `(principal.ID, object.ID, action)` | `core.cache.access_cache.max_entries`, `.max_bytes`, `.ttl` | `pg_notify('*')`; проверка `revision + access_epoch` на каждом hit; ошибка epoch = miss | `sync.RWMutex` или shardable LRU |
| `loader.sessions` | Loader | upload session ID | `loader.session_cache_entries`, `.session_cache_max_bytes`, `.session_cache_ttl` | mutation/recovery удаляет точный ключ; hit проверяет principal, fingerprint и revision | `sync.Mutex`, LRU |
| `router.manifests` | Router | artifact `fileID + digest` | `router.cache.manifests.*` | Core file revision/CAS; при расхождении — miss и fail-closed | `sync.RWMutex`, bounded LRU |
| `router.routes` | Router | `(branch, host, normalizedPath, method)` | `router.cache.routes.*` | revision route record; публикация и смена assignment меняют revision | `sync.RWMutex`, bounded LRU |
| `admin.query` | Admin SPA | `queryKeys.*` | `staleTime`, память вкладки | `invalidateQueries` по мутациям и SSE | React/TanStack Query |
| `admin.static` | Admin UI + browser | URL ассета | hashed-ассеты `public, max-age=<uint>, immutable`, `index.html` `no-store` | смена имени файла при сборке | HTTP-заголовки origin |
| `deploy.proxy_cdn` | deployer | origin URL + `Vary` keys | политика deployer | строго по origin `Cache-Control`/`ETag` | внешняя инфраструктура |

`core.cache.max_object_bytes` остаётся жёстким потолком: объект больше лимита не кэшируется, а читается потоково. `core.cache.max_bytes` ограничивает суммарный размер всех записей одного LRU, включая длину ключа. Значения в колонке «Потокобезопасность» описывают текущий код; целевое состояние — шардированные LRU, `RWMutex`, immutable `cache.View` и coalescing согласно [SPEC-concurrency.md](SPEC-concurrency.md). Инварианты авторизации, revision и fail-closed от этого не меняются.

## 4. HTTP cache policy

### 4.1. Разрешённые `Cache-Control`

Origin и Router обязаны использовать только:

- `no-store`;
- `public, max-age=<uint>` при `<uint> > 0`;
- `public, max-age=<uint>, immutable` при `<uint> > 0`.

Запрещены `no-cache`, `must-revalidate`, `max-age=0`, `private` в публичных ответах и произвольные директивы. Динамические индексируемые страницы используют `no-store` и strong ETag: conditional GET сохраняется без TTL shared cache. Политика engine/formatter проверяется этим allowlist до issuance; invalidation identity не может быть подменена visitor input.

### 4.2. Validators

- Strong ETag — quoted, вычисляется по стабильной identity representation: `ContentRevision`, checksum artifact или hash representation. Weak `W/` и `*` обрабатываются через weak comparison для `If-None-Match`.
- 304 возвращает только заголовки, без тела, и сохраняет те же `Cache-Control` и `ETag`, что и 200.
- Origin повторно разрешает route, publication/assignment и авторизацию до обработки conditional request. 304 не должен скрыть публикацию, смену snapshot, metadata или feature build.
- `Last-Modified`/`If-Modified-Since`, `Range` и `Accept-Ranges` остаются частью соответствующего Core/file контракта. Router включает сжатие только вне range-запроса.

### 4.3. `Vary`, locale и сжатие

- Если представление выбрано по cookie `zubrium_locale`, ответ получает `no-store`. `Vary: Cookie` не используется: он сделал бы shared cache бесполезным из-за любых cookie и опасен для приватных данных.
- Если locale выбрана из `Accept-Language`, ответ получает `Vary: Accept-Language`.
- Сжатие включается конфигурацией `router.cache.http.compression`; минимально поддерживается `gzip`. `br` допускается только если runtime действительно реализует алгоритм. Сжимаются типы из явного списка и только при размере не меньше порога.
- Сжатый ответ получает `Vary: Accept-Encoding`. ETag считается по несжатой representation и не меняется при сжатии. `Content-Length` описывает переданное закодированное тело. Range-запрос или несовместимый `Content-Type` отключает сжатие.
- Deploy proxy/CDN обязан уважать `Vary`, `no-store` и `ETag`; он не может «улучшить» политику приватного ответа.

## 5. Режим прогрева кэшей Core

### 5.1. Цель и границы

Цель — устранить холодные промахи по заранее известным горячим файлам и published artifacts до момента, когда компонент объявит себя Ready. Прогрев не меняет авторизацию, не ищет популярные файлы скрытым SQL-сканом и не создаёт новые публичные операции.

Не разрешено:

- кэшировать секреты, credentials, session tokens или пользовательские данные;
- прогревать ошибки, fallback, draft/submission/private media;
- запускать неограниченный обход всех файлов;
- использовать один кэш несколькими процессами через общую память;
- делать прогрев частью HTTP-запроса посетителя.

### 5.2. Активация и режимы

| `mode` | Поведение |
| --- | --- |
| `strict` | Обязательные ключи должны быть прогреты до Ready. Любая обязательная ошибка или таймаут означает: компонент не Ready, failure stream останавливает запуск. |
| `best_effort` | Прогрев выполняется до Ready с теми же лимитами, но ошибки только логируются и увеличивают метрики; Ready сохраняется. |
| `off` | Прогрев не запускается. `enabled: false` эквивалентен `off`. |

`strict` — рекомендуемый режим для full/router профилей с известными hot keys. `refresh_interval: 0s` означает отсутствие фонового повтора; положительное значение запускает повтор с тем же набором и лимитами без перекрытия предыдущего цикла.

Цель `authorization` требует внутреннего монотонного `access_epoch` в Core. Публичный Core контракт и credential-bound session contract от этого не меняются, но само изменение замороженного Core допускается только после отдельного согласования и с regression test. Пока согласование не получено, `targets.authorization` остаётся `false`, а права проверяются существующим `Authorize` без сохранения результата в кэш; остальные цели прогрева работают независимо.

### 5.3. Источники ключей

Разрешены два источника, включаемые независимо:

1. `sources.inline: true` — явный список `core.cache.warmup.keys` из startup YAML.
2. `sources.published_artifacts: true` — детерминированное получение file ID из уже сконфигурированных published Router artifacts, а не из пользовательского ввода.

Объединение дедуплицируется по `fileID`, сортируется детерминированно и обрезается до `max_keys`. Каждая запись имеет `tier: required | optional`. Удаление ключа из конфига удаляет его из набора прогрева, но не инвалидирует уже загруженные записи — их TTL и обычная инвалидация продолжают работать.

### 5.4. Цели прогрева

| Цель | Что заполняется | Порядок |
| --- | --- | --- |
| `authorization` | `core.authorization` для пар `(principal, file, action)` из записи ключа | 1 |
| `metadata` | `core.metadata` с проверкой `revision` из авторизационного снимка | 2 |
| `bytes` | `core.bytes`, только если размер не больше `max_object_bytes` | 3 |
| `router_manifests` | `router.manifests` для artifact file ID | 4 |
| `router_routes` | `router.routes` для сконфигурированных branches/routes | 5 |

Порядок значим: authorization возвращает revision, которым валидируется metadata; metadata возвращает размер и state, которые ограничивают byte cache. Router-цели выполняются после Core-целей и используют те же hot keys. Проверка `Authorize` выполняется для каждого ключа независимо от того, включено ли сохранение результата в `core.authorization`: при `targets.authorization: false` она остаётся обязательной валидацией, но warmed запись не создаётся.

### 5.5. Алгоритм

1. Валидировать конфиг до старта всех компонентов: режим, таймауты, `max_keys > 0` при включённом прогреве, `concurrency > 0`, `retry.attempts ≥ 1`, известные target/action, корректные UUID и tier.
2. Разрешить ключи из разрешённых источников, дедуплицировать, отсортировать, применить `max_keys`.
3. Для каждого ключа выполнить существующий авторизованный Core-вызов, а затем — включённые цели в порядке из раздела 5.4; каждый вызов использует `context.WithTimeout(per_key_timeout)` в рамках общего `timeout`.
4. Ограничить параллелизм `concurrency`; общий timeout и `context` остаются родительскими. Отмена при shutdown прерывает все операции.
5. Повторять только transient errors (downstream deadline, unavailable storage) не более `retry.attempts` с `retry.backoff`. Ошибки валидации, авторизации, not found и revision conflict не считаются transient и сразу дают результат key failed.
6. Для `tier: required` ошибка даёт failure результата. Для `tier: optional` ошибка даёт метрику `FAILED` и продолжение.
7. Заполнять кэш только после успешной проверки identity. При расхождении revision, epoch или state запись не сохраняется.
8. Объявить компонент Ready только после завершения всех обязательных операций в режиме `strict`.
9. При `refresh_interval > 0` повторять тот же bounded pipeline в фоне; ошибки повтора не меняют Ready, но попадают в метрики и логи.

### 5.6. Инварианты во время прогрева

- При включённой цели `core.authorization` запись содержит `revision` и `access_epoch`. `access_epoch` увеличивается в той же Core транзакции, что и изменение grants, groups или credentials; обычный hit сравнивает epoch с актуальным значением из PostgreSQL. При недоступности проверки epoch запись не используется.
- Прогрев `bytes` не обходит `max_object_bytes` и не буферизует весь объект, если он больше лимита.
- Прогрев `metadata` не сохраняет groups без фильтрации по `visibleGroups` из того же `Authorize`.
- Router manifest cache принимает запись только после сверки `fileID`, digest, size, `Content-Type` и Core file revision.
- Router route cache принимает запись только после проверки route record revision и активной branch.
- В логах и метриках отсутствуют file ID, principal ID, URL, query, locale и значения ключей.

### 5.7. Отказные и конкурентные случаи

- Разные процессы/branch с этим YAML прогревают свои локальные кэши независимо; между ними нет общей памяти. Внутри одной branch сохраняется ровно один Router process согласно [SPEC-concurrency.md](SPEC-concurrency.md). Инвалидация приходит через PostgreSQL `pg_notify`.
- Если во время прогрева изменился grant, `access_epoch` делает warmed запись непригодной; следующий запрос выполнит полную проверку. При отключённой цели `authorization` гарантию обеспечивает сам обязательный вызов `Authorize` на каждом ключе.
- Если во время прогрева изменился объект, revision mismatch забрасывает запись и вызывает перечитывание.
- Если LISTEN-слушатель был отключён, высокий `zubrium_cache_invalidation_lag_seconds` не даёт права использовать старую authorization entry.
- Превышение `timeout`, `max_keys` или `concurrency` завершает прогрев с метрикой и не увеличивает лимиты автоматически.
- Shutdown отменяет контекст прогрева; незавершённые записи не сохраняются частично.

## 6. Улучшения вне Core warm-up

### 6.1. Router manifest и route cache

- Кэш декодированного artifact manifest устраняет повторный JSON parse и повторное чтение Core file на каждом запросе. Ключ содержит `fileID + digest`; hit требует сверки с Core file metadata. При любой рассогласованности — miss и fail-closed.
- Кэш route match хранит результат разрешения `(branch, host, normalizedPath, method)` до artifact. Ключ включает revision route record и назначения snapshot. Публикация, смена assignment и отзыв группы меняют revision и инвалидируют запись.
- Оба кэша bounded по записям и байтам, имеют TTL и метрики. Межпроцессная инвалидация использует тот же принцип, что в Router spec: после commit; недоступность проверки revision закрывает выдачу.

### 6.2. Admin static

Hashed Vite-ассеты получают `public, max-age=31536000, immutable`; `index.html` и mutable ассеты — `no-store`. SPA не кэширует чувствительные данные в persistent storage.

### 6.3. Deploy proxy/CDN

Deployer может включить caching proxy/CDN перед публичным listener. Обязательные условия:

- `no-store` origin никогда не переопределяется;
- учитываются `Vary: Accept-Language` и `Vary: Accept-Encoding`;
- locale из cookie остаётся `no-store`;
- приватные, admin, SSE и form-session ответы не кэшируются;
- proxy описан в `deploy/` как часть topology, но не выбирается профилем и не меняет router semantics.

## 7. Конфигурация

Ниже — целевая схема. Все значения обязательны в `config.example.yaml`; приложение не подменяет пропуски скрытыми defaults. Переход с плоских `core.cache_max_bytes`, `core.cache_ttl`, `core.max_cached_object_bytes` на вложенную схему делается одной волной с кодом; compatibility aliases не добавляются.

```yaml
core:
  cache:
    max_bytes: 67108864
    ttl: 10m
    max_object_bytes: 2097152
    access_cache:
      max_entries: 10000
      max_bytes: 8388608
      ttl: 1m
    warmup:
      enabled: true
      mode: strict                # strict | best_effort | off
      timeout: 30s
      per_key_timeout: 5s
      concurrency: 4
      max_keys: 200
      retry:
        attempts: 2
        backoff: 200ms
      refresh_interval: 0s        # 0s означает отсутствие фонового повтора
      sources:
        inline: true
        published_artifacts: true
      targets:
        authorization: false         # true только после согласования access_epoch в замороженном Core
        metadata: true
        bytes: true
        router_manifests: true
        router_routes: true
      keys:
        - file_id: "00000000-0000-0000-0000-000000000000"  # пример формата; реальные ID задаёт оператор
          tier: required          # required | optional
          authorization:
            principals: []        # principal ID; пустой список отключает цель authorization для ключа
            actions: [read]

router:
  cache:
    manifests:
      enabled: true
      max_entries: 512
      max_bytes: 33554432
      ttl: 5m
    routes:
      enabled: true
      max_entries: 4096
      max_bytes: 8388608
      ttl: 30s
    http:
      compression:
        enabled: true
        algorithms: [gzip]
        min_bytes: 1024
        media_types:
          - text/html
          - text/css
          - application/javascript
          - application/json
          - image/svg+xml
      vary:
        accept_encoding: true
        locale_from_cookie: no_store
```

`enabled: false` на отдельной цели или кэше полностью исключает её из warm-up и runtime path; соответствующие метрики остаются видимыми с нулевыми счётчиками.

## 8. Observability

Имена метрик фиксированы и добавляются в `SPEC-observability.md`:

| Метрика | Тип | Метки |
| --- | --- | --- |
| `zubrium_cache_operations_total` | counter | `cache`, `result=HIT\|MISS\|EVICTION\|INVALIDATED` |
| `zubrium_cache_entries` | gauge | `cache` |
| `zubrium_cache_bytes` | gauge | `cache` |
| `zubrium_cache_warmup_keys_total` | counter | `cache`, `tier=REQUIRED\|OPTIONAL`, `result=WARMED\|FAILED\|SKIPPED` |
| `zubrium_cache_warmup_duration_seconds` | histogram | `cache` |
| `zubrium_cache_invalidation_lag_seconds` | gauge | `cache` |

Допустимые значения метки `cache`: `core.bytes`, `core.metadata`, `core.authorization`, `loader.sessions`, `router.manifests`, `router.routes`, `admin.query`. Запрещены метки с file ID, principal ID, URL, query, locale и User-Agent. `/metrics` остаётся `no-store`.

## 9. Acceptance scenarios

Идентификаторы добавляются в `tests/ACCEPTANCE-MATRIX.md`; результаты не объявляются пройденными до реального прогона.

| ID | Сценарий |
| --- | --- |
| `CACHE-001` | `strict` и обязательные ключи: все включённые цели прогреты до Ready; `/metrics` содержит hit/warmup метрики. |
| `CACHE-002` | Обязательный ключ завершается ошибкой или таймаутом: компонент не Ready, startup останавливается; optional-key даёт Ready и метрику `FAILED`. |
| `CACHE-003` | `timeout`, `per_key_timeout`, `concurrency`, `max_keys` соблюдены; скрытые defaults не подставляются. |
| `CACHE-004` | Отзыв grant между прогревом и запросом делает authorization entry непригодной; доступ закрыт или перепроверен. |
| `CACHE-005` | Изменение и удаление файла, а также reconnect LISTEN инвалидируют bytes и metadata; stale bytes не отдаются. |
| `CACHE-006` | Смена published artifact/route по CAS инвалидирует `router.manifests` и `router.routes`; stale match закрыт. |
| `CACHE-007` | Cookie locale даёт `no-store`; `Accept-Language` даёт `Vary: Accept-Language`; `Vary: Cookie` отсутствует. |
| `CACHE-008` | gzip с `Vary: Accept-Encoding`, стабильный ETag, Range отдаётся без сжатия. |
| `CACHE-009` | Deploy proxy/CDN не переопределяет `no-store`; SSE, form и admin остаются приватными. |
| `CACHE-010` | Метрики кэша присутствуют и имеют только разрешённые низкокардинальные метки. |
| `CACHE-011` | `refresh_interval > 0` повторяет warm-up без перекрытия циклов и без сверхлинейного роста записей. |
| `CACHE-012` | Логи и diagnostics не содержат ключей, file ID, principal ID, URL, query или пользовательских данных. |

## 10. Связанные документы

- [SPEC-concurrency.md](SPEC-concurrency.md) — потокобезопасность, backpressure, coalescing и worker-пулы.
- [SPEC-core-architecture.md](../core/SPEC-core-architecture.md) — границы Core и владение PostgreSQL, S3, cache.
- [SPEC-module-data.md](../core/SPEC-module-data.md) — CAS revision как основа инвалидации.
- [SPEC-router-engine.md](../engines/router/SPEC-router-engine.md) — маршрутизация, proxy и fail-closed правила.
- [SPEC-static-site-artifact.md](../engines/website/SPEC-static-site-artifact.md) — `cache_class` и manifest identity.
- [SPEC-public-server-rendering.md](../engines/website/SPEC-public-server-rendering.md) и [SPEC-seo.md](../engines/website/SPEC-seo.md) — публичные document/SEO ответы.
- [SPEC-observability.md](../observability/SPEC-observability.md) — метрики и `/metrics`.
- [router.yaml](../presentation/openapi/router.yaml) — HTTP-контракт публичного Router.
