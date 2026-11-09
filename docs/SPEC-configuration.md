# Спецификация эксплуатационной конфигурации

## Цель и границы

Все значения, которыми управляет оператор и которые влияют на запуск, лимиты, время ожидания, пропускную способность, порты, кеширование и доступ к хранилищу, задаются явно. Приложение завершается с понятной ошибкой, если обязательное значение отсутствует, имеет неверный формат или не проходит проверку диапазона. Значения примера находятся в `.env.example`; они не превращаются в fallback при запуске. Фиксированные требования протокола и алгоритма перечисляются отдельно и не маскируются под операторские настройки.

Протокольные и алгоритмические константы остаются в коде, если оператор не управляет этим поведением. Manifest defaults относятся к контракту фичи и не являются runtime-конфигурацией движка.

## Источники и секреты

- Обычные параметры читаются из окружения процесса или `env_file`, секреты — из окружения либо файлов секретов.
- Секрет нельзя встраивать в образ, исходники или пример как действующее значение. Значения `replace-with-*` в примере требуют замены оператором.
- Пустой список `FILE_SERVICES=[]` является явной настройкой «нет внешних сервисов»; отсутствие переменной — ошибка.
- Ветки и сервисы файлов регистрируются по настроенному имени; внутренний ID генерируется и сохраняется. Переименование выполняется операторскими командами из [OPERATIONS.md](OPERATIONS.md).

## Обязательные группы

`LoadDatabase` требует `DATABASE_URL`, размеры пула PostgreSQL, время жизни соединений, период проверки соединений, таймаут подключения и таймаут миграционного lock. `DATABASE_MIN_CONNECTIONS` может быть нулём и не должен превышать максимум.

`Load` требует адрес админского HTTP-сервера, массив `BRANCHES`, предел HTTP headers, режим миграции, уровень логов, доверие proxy-заголовкам, лимиты JSON/загрузки/ZIP фич, таймаут регистрации сборки, лимит и окно заявок, предел числа IP-счётчиков и интервал их очистки, все HTTP/shutdown/readiness таймауты, интервалы ожидания S3, срок жизни предпросмотра дизайн-токенов, аренду и polling media worker, `MEDIA_REDIRECT_CACHE_TTL`, `RUNTIME_ASSET_CACHE_TTL` и полные S3-параметры. Два cache TTL — положительные целые секунды, минимум 1s; они задаются явно, без fallback. Таблица ограничителя заявок никогда не превышает `FORM_RATE_MAX_CLIENTS`; заполнение таблицы временно отклоняет запросы с новых IP до истечения счётчиков. Периодическая очистка задаётся `FORM_RATE_SWEEP_INTERVAL`, а при заполнении expired-записи удаляются немедленно. Bootstrap token опционален, но если задан, валидируется.

`LoadPagination` требует `ADMIN_PAGE_SIZE`, `PUBLIC_PAGE_SIZE` и `SUBMISSION_EXPORT_MAX_ROWS`. `PUBLIC_PAGE_SIZE` задаёт размер по умолчанию для пагинируемых API-списков и предел публичной выдачи коллекций; `ADMIN_PAGE_SIZE` задаёт верхний предел списков и объём списка токенов. Журнал публикаций использует эти же заданные значения, не локальные ограничения в коде. Значение по умолчанию не может превышать верхний предел. Экспорт заявок ограничен `SUBMISSION_EXPORT_MAX_ROWS`.

`LoadFiles` требует лимит объекта и части, TTL загрузки, интервал очистки, таймаут обработки, квоту владельца, число сессий, размер списка, сетевые лимиты, срок подписанной ссылки, публичный base URL, temp directory, ключ подписи и `FILE_SERVICES`. Ошибки отношений параметров называют затронутые переменные. Максимум объекта ограничен 5 GiB; часть не превышает объект; квота владельца не меньше максимума объекта; размер списка — от 1 до 1000; ключ подписи содержит минимум 32 байта, ключ сервиса — минимум 24 байта. Каталог должен быть доступен для создания и удаления временного файла.

`LoadPrerender` требует явный `PRERENDER_ENABLED`. При `false` остальные browser-параметры не применяются. При `true` Node и browser должны быть существующими исполняемыми файлами, worker и Playwright module — существующими обычными файлами. Все ограничения ресурсов, concurrency/queue/cache и интервалы/таймауты обязательны; invalid range errors указывают имя настройки. Attempt timeout не превышает общий timeout, quiet interval короче attempt timeout.

`LoadSitepack` требует temp directory, максимумы архива/распаковки/индекса/записей, отдельный лимит SVG и предел browser fallback. Все byte limits не меньше 1 KiB; архив содержит не более 64 MiB SVG; распакованный предел не меньше предела архива; индекс не превышает распакованный предел; browser fallback не превышает лимит архива. Ошибки называют каждую конфликтующую переменную. Каталог должен быть доступен для создания и удаления временного файла.

API должен загрузить и проверить все группы конфигурации (`Load`, `LoadFiles`, `LoadPagination`, `LoadSitepack` и `LoadPrerender`) до открытия listeners, PostgreSQL, миграций или S3. Отсутствующая настройка должна завершать запуск без изменений базы и хранилища. Операторские команды переименования сначала проверяют аргументы и затем все используемые ими параметры БД до подключения; они не запускают остальные API-сервисы и потому не читают их конфигурационные группы.

## Реестр параметров

`.env.example` — полный пример для движка и Compose. Ниже перечислены переменные, читаемые loaders, и их роль; отсутствующее обязательное значение не заменяется примерным.

| Группа | Параметры | Назначение |
| --- | --- | --- |
| PostgreSQL | `DATABASE_URL`, `DATABASE_MAX_CONNECTIONS`, `DATABASE_MIN_CONNECTIONS`, `DATABASE_MAX_CONNECTION_LIFETIME`, `DATABASE_MAX_CONNECTION_IDLE_TIME`, `DATABASE_HEALTH_CHECK_INTERVAL`, `DATABASE_CONNECT_TIMEOUT`, `MIGRATION_LOCK_TIMEOUT` | Подключение, размер и обслуживание пула, timeout подключения и блокировки миграций. URL может содержать секрет; ошибки конфигурации не выводят его. |
| Identity и запуск | `BOOTSTRAP_ADMIN_TOKEN` (необязательно), `MIGRATE_ON_START`, `LOG_LEVEL`, `TRUST_PROXY_HEADERS` | Создание стартового администратора, миграции, журналирование и доверие forwarded headers. |
| Сеть и branches | `HTTP_ADDR`, `BRANCHES`, `HTTP_MAX_HEADER_BYTES` | Админский listener, статический JSON branches с отдельными портами/CORS/index policy и явный предел заголовка каждого HTTP listener. Host-port mapping Compose должен совпадать с настроенными адресами. |
| HTTP и readiness | `READ_HEADER_TIMEOUT`, `HTTP_READ_TIMEOUT`, `HTTP_WRITE_TIMEOUT`, `HTTP_IDLE_TIMEOUT`, `SHUTDOWN_TIMEOUT`, `HEALTH_CHECK_TIMEOUT` | Ограничения заголовка/запроса/ответа/keep-alive, graceful shutdown и readiness. |
| JSON и редакционные операции | `MAX_JSON_BYTES`, `MAX_UPLOAD_BYTES`, `MAX_FEATURE_BUILD_BYTES`, `FEATURE_BUILD_REGISTRATION_TIMEOUT`, `DESIGN_PREVIEW_TTL` | Размеры запросов, загрузок и ZIP сборок; регистрация сборки и предпросмотр design tokens. |
| Формы | `FORM_RATE_LIMIT`, `FORM_RATE_WINDOW`, `FORM_RATE_MAX_CLIENTS`, `FORM_RATE_SWEEP_INTERVAL`, `ADMIN_PAGE_SIZE`, `SUBMISSION_EXPORT_MAX_ROWS` | Публичный rate limit, жёсткая ёмкость и очистка счётчиков, административные списки и экспорт. |
| S3 | `S3_ENDPOINT`, `S3_PUBLIC_BASE_URL`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_FORCE_PATH_STYLE`, `S3_ENSURE_BUCKET`, `S3_STARTUP_TIMEOUT`, `S3_RETRY_INTERVAL` | Адреса и учётные данные storage, path-style, проверка приватности и ограниченный retry готовности bucket. Keys передаются через env; реальные значения не включаются в образ или пример. |
| Media | `MAX_UPLOAD_BYTES`, `MEDIA_RENDITION_LEASE`, `MEDIA_WORKER_POLL_INTERVAL`, `MEDIA_REDIRECT_CACHE_TTL`, `FILES_PROCESS_TIMEOUT` | Приём оригиналов, аренда и частота опроса обработки, TTL legacy public redirect и ограничение времени FFmpeg/image processor. |
| Runtime assets | `RUNTIME_ASSET_CACHE_TTL` | TTL для незахэшированных embedded public runtime assets и favicon. Content-hashed immutable assets сохраняют контрактный годовой TTL. |
| Публичные/API списки | `PUBLIC_PAGE_SIZE` | Default и верхний предел публичной выдачи, а также настроенный default для admin lists. Он не должен превышать `ADMIN_PAGE_SIZE`. |
| Внешние файлы | `FILES_MAX_BYTES`, `FILES_PART_BYTES`, `FILES_UPLOAD_TTL`, `FILES_CLEANUP_INTERVAL`, `FILES_PROCESS_TIMEOUT`, `FILES_OWNER_QUOTA_BYTES`, `FILES_MAX_SESSIONS`, `FILES_LIST_LIMIT`, `FILES_RATE_LIMIT`, `FILES_RATE_WINDOW`, `FILES_RATE_MAX_CLIENTS`, `FILES_LINK_MAX_TTL`, `FILES_PUBLIC_BASE_URL`, `FILES_TEMP_DIR` | Максимумы объекта/части/квоты/сессий/выдачи и ограниченные rate counters; TTL, cleanup/processing intervals, краткоживущие links, origin и временные файлы. |
| Внешние файлы: секреты | `FILES_SIGNING_KEY_ENV` либо `FILES_SIGNING_KEY_FILE`, `FILE_SERVICES` с `keys[].env` либо `keys[].file` | Ровно один источник секрета подписи; scoped service owners, операции и один или несколько одновременно действующих ключей. Отсутствие сервисов задаётся явно как `FILE_SERVICES=[]`. |
| Sitepack | `SITEPACK_TEMP_DIR`, `SITEPACK_MAX_BYTES`, `SITEPACK_MAX_EXPANDED_BYTES`, `SITEPACK_MAX_ENTRIES`, `SITEPACK_MAX_INDEX_BYTES`, `SITEPACK_MAX_SVG_BYTES`, `SITEPACK_BROWSER_FALLBACK_BYTES` | Временный каталог и пределы ZIP, распакованных entries/index, проверки SVG и клиентского fallback. SVG-лимит является отдельной настройкой и не наследуется от multipart-размера файлов. |
| Prerender | `PRERENDER_ENABLED`, `PRERENDER_NODE_PATH`, `PRERENDER_WORKER_PATH`, `PRERENDER_PLAYWRIGHT_MODULE`, `PRERENDER_BROWSER_PATH`, `PRERENDER_CONCURRENCY`, `PRERENDER_QUEUE_SIZE`, `PRERENDER_CACHE_ENTRIES`, `PRERENDER_MAX_HTML_BYTES`, `PRERENDER_HEAP_MB`, `PRERENDER_VIEWPORT_WIDTH`, `PRERENDER_VIEWPORT_HEIGHT`, `PRERENDER_MAX_REQUESTS`, `PRERENDER_MAX_NETWORK_BYTES`, `PRERENDER_TIMEOUT`, `PRERENDER_ATTEMPT_TIMEOUT`, `PRERENDER_MAX_ATTEMPTS`, `PRERENDER_QUIET_INTERVAL`, `PRERENDER_CACHE_TTL` | Явное включение; пути к уже установленным исполняемым файлам; ресурсные пределы worker, bounded queue/cache, viewport и общий/попытка/settle/cache TTL. При `false` browser-пути и лимиты игнорируются. |
| Compose | `ENGINE_IMAGE`, `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Image и начальная база Postgres. `POSTGRES_PASSWORD` — только операторский secret из `.env`/environment; `replace-with-*` в примере не является credential. |

`HTTP_MAX_HEADER_BYTES` находится в диапазоне 1 KiB–16 MiB; `MAX_JSON_BYTES` — 1 KiB–64 MiB; `MAX_UPLOAD_BYTES` — 1 KiB–5 GiB; `MAX_FEATURE_BUILD_BYTES` — 1 MiB–2 GiB. `SITEPACK_MAX_SVG_BYTES` находится в диапазоне 1 KiB–64 MiB и не превышает `SITEPACK_MAX_BYTES`. Оба cache TTL принимают целые seconds не меньше одной секунды; duration без целой секунды отклоняется, поскольку HTTP `max-age` кодируется целым числом секунд. Остальные resource ranges файлов, пагинации, sitepack и prerender перечислены выше и в loader-валидации; положительные лимиты не получают fallback. Отдельные protocol/algorithmic ceilings не являются настройками по умолчанию.

### Фиксированные протокольные и алгоритмические ограничения

- Go/HTTP header cap нельзя оставлять library default: `HTTP_MAX_HEADER_BYTES` обязательно задаёт его оператор.
- multipart S3 части, MIME/path validation, версии ABI, hash/digest lengths и секундная точность подписанной ссылки являются требованиями протокола.
- Декодер изображения фиксирует 40 млн пикселей для ограничения памяти при распаковке; валидатор feature ZIP ограничивает распакованный объём, число entries и размер одной entry; статический HTML ABI ограничен 2 MiB; URL matcher ограничивает работу одного route match. Это жёсткие safety/algorithm caps в коде, не значения конфигурации.
- Форматные размеры и число элементов схемы/manifest либо snapshot payload валидируются по контракту конкретного формата; они не заменяются конфигурационными лимитами.
- Числа внутри versioned feature manifests (включая размеры token defaults) принадлежат контракту фичи, а не startup configuration.
- AWS SDK сохраняет свой протокольный retry/backoff policy для отдельных S3 операций; `S3_RETRY_INTERVAL` относится только к стартовой проверке bucket. Отдельный S3 SDK retry override не задаётся Engine в этом контракте.

## Валидация и ошибки

- Boolean, integer и duration парсятся строго; ошибка не заменяется значением по умолчанию. PostgreSQL connection string синтаксически проверяется без соединения и не попадает в сообщение ошибки.
- Положительные лимиты должны быть больше нуля, а связанные ограничения согласованы: page size, quota ≥ максимального файла, part size ≤ file size, max expanded archive ≥ archive size, SVG limit ≤ sitepack archive limit, cache/resource limits проходят собственные проверки. Для prerender минимумы — concurrency/queue/cache/attempts/requests ≥ 1, HTML/network ≥ 1024 bytes, heap ≥ 32 MiB, viewport ≥ 320×240; attempt timeout ≤ общий timeout, quiet interval < attempt timeout. Node и browser должны быть executable files.
- Branches обязателен, содержит минимум одну запись, уникальные имена и порты, отличные от admin-порта; `indexable` и `corsOrigins` обязательны.
- Списки фич, сборок, медиа, заявок, снапшотов, истории публикаций и токенов используют настроенные пределы; публичная выдача коллекций использует явно заданный размер страницы. Handler без настроенных лимитов отвечает configuration error, а service без лимита не выполняет запрос.
- Публичная коллекция не подставляет собственные default/max; сервис использует только установленный `PUBLIC_PAGE_SIZE` и отклоняет отсутствующий или превышающий его лимит до чтения runtime/БД. Журнал публикаций получает default/max от pagination handler и не имеет второго скрытого cap/default.
- Отсутствие или ошибка параметра возвращает имя переменной и ссылку на `.env.example`, не включая секретное значение. Cross-limit errors для file/Sitepack/prerender называют оба конфликтующих параметра; имена secret env могут быть перечислены, сами значения запрещено включать в ошибку. URL S3 не принимает userinfo.

## Проверки приёмки

1. Табличные unit tests покрывают отсутствующие и неверные значения каждого loader, включая rate-limit capacity/sweep, required settings files, все resource ranges enabled prerender и все cross-limit связи files/sitepack. Каждая ошибка указывает имя затронутой настройки.
2. Загрузка валидного `.env.example` fixture создаёт только настроенные ветки, настраивает пул БД и требует отдельные file/prerender/sitepack параметры без скрытого runtime fallback.
3. API pagination handlers получают конфигурируемые default/max; изменение значений влияет на фактические ответы и не меняет cursor/permissions.
4. Docker Compose `config` проходит с заполненным примером и понятно отказывает при отсутствующих обязательных переменных.
5. Тесты запускаются без подключения к рабочей БД и без доступа к production storage.
6. API сообщает об отсутствующих параметрах sitepack или prerender до bind портов, подключения к PostgreSQL и S3; такой же порядок действует для всех обязательных групп.
7. `newServer` получает `MaxHeaderBytes` из обязательного параметра, а Sitepack получает самостоятельный `SITEPACK_MAX_SVG_BYTES`; оба валидируются до bind.
8. Настроенный `S3_STARTUP_TIMEOUT` ограничивает также зависший отдельный вызов storage, а не только число повторов.

## Проверка порядка запуска — 6 ноября 2026

`cmd/api` загружает `Load`, `LoadFiles`, `LoadPagination`, `LoadSitepack` и `LoadPrerender` до bind listeners, PostgreSQL и object storage. `TestRunValidatesAllConfigGroupsBeforeDatabaseConnections` проверяет ранний отказ при отсутствии `DATABASE_URL`, `FILES_MAX_BYTES`, `PUBLIC_PAGE_SIZE`, `SITEPACK_MAX_BYTES` и `PRERENDER_ENABLED`, указывая имя параметра вместо ошибки соединения. `TestRenameCommandsValidateArgumentsBeforeDatabaseConfig` подтверждает проверку аргументов CLI до чтения конфигурации БД. На 6 ноября полный перебор числовых guardrails и сообщений для всех CLI путей ещё не был выполнен; полная сверка и классификация завершена 7 ноября ниже.

## Пределы списков

Публичная выдача коллекций использует `PUBLIC_PAGE_SIZE` как default и maximum; admin аудит событий публикации использует `PUBLIC_PAGE_SIZE` как default и `ADMIN_PAGE_SIZE` как maximum через handler. Ни `SnapshotService.Collection`, ни `publication.ListEvents` не выбирают скрытый лимит при отсутствующей настройке. Проверяются fail-closed unit cases и конфигурируемый предел выше прежних 50/200.

Проверка 7 ноября: `go test -count=1 ./features/sitebuilder ./pkg/publication ./presentation/http/handlers/sitebuilder ./presentation/http/handlers/publication ./cmd/api ./pkg/config`, соответствующий `go vet` и `TestCollectionRuntimeFiltersAndResolvesDetailRoutes` на уникальной disposable PostgreSQL прошли. Unit tests подтвердили отказ сервиса коллекций без `PUBLIC_PAGE_SIZE`, принятие 250 при настроенном лимите 250 и передачу `LIMIT 251` для журнала с запрошенными 250 элементами; предыдущие скрытые cap/default 50/20 и 200/50 удалены.

## Полная проверка loader guardrails — 7 ноября 2026

В `LoadFiles`, `LoadPrerender` и `LoadSitepack` cross-limit и range ошибки теперь называют точные env variables, включая обе стороны несовместимого предела. Prerender проверяет каждую числовую нижнюю границу, положительные таймауты и связи `PRERENDER_ATTEMPT_TIMEOUT ≤ PRERENDER_TIMEOUT` и `PRERENDER_QUIET_INTERVAL < PRERENDER_ATTEMPT_TIMEOUT`; Node/Chromium paths должны указывать на существующие исполняемые файлы, worker/module — на существующие обычные файлы. S3 URLs не принимают credentials в userinfo.

File loader сообщает отдельно об ограничении `FILES_MAX_BYTES ≤ 5 GiB`, `FILES_PART_BYTES ≤ FILES_MAX_BYTES`, `FILES_OWNER_QUOTA_BYTES ≥ FILES_MAX_BYTES`, `FILES_LIST_LIMIT ∈ [1,1000]`, положительных сессиях/rate limits/TTL и длинах secret keys. Sitepack error names cover минимальный размер, SVG archive/hard cap, expanded/archive, index/expanded и browser-fallback/archive. Перечисленные hard caps остаются защитными верхними границами; все рабочие значения по-прежнему задаются оператором без runtime fallback.

Unit suite `go test -count=1 ./pkg/config` включает табличные invalid/missing/range cases для database pool, prerender resources/paths/timeouts, files ownership/quotas/secrets и Sitepack relations. Установленные значения примера остаются в `.env.example`; изменения относятся к валидации и тестам и не требуют изменения Compose. Полный production gateway/CDN audit не относится к этим loader tests и остаётся отдельным ограничением общего плана.

## Полная сверка CLI, public и worker startup — 7 ноября 2026

`cmd/api.run` по-прежнему сначала загружает все пять групп конфигурации, затем резервирует listeners и только после этого открывает PostgreSQL и S3. Загрузка `FILES_TEMP_DIR` и `SITEPACK_TEMP_DIR` теперь также создаёт каталог при необходимости и проверяет создание/удаление временного файла; невалидный или недоступный путь завершается с именем переменной до bind/DB/S3. Это важно, потому что те же каталоги используются позже файловым/media сервисом и импортом sitepack. CLI `rename-branch` и `rename-service` проверяют аргументы, затем валидируют `LoadDatabase` и только потом открывают пул; им не нужны группы, используемые только HTTP API. Media/file workers создаются после полной проверки, а их интервалы/аренда берутся из конфигурации. Prerender worker не читает `process.env`: API передаёт все лимиты, пути, nonce и timeout через stdin JSON; при `PRERENDER_ENABLED=false` процесс worker не создаётся.

Ошибка `BRANCHES` для некорректного `HTTP_ADDR` и CORS origin не включает пользовательский URL: сетевой парсер мог включить `userinfo` из входной строки в ошибку. Ошибки конфигурации файлов/путей также называют env-переменную и не выводят путь или секретное значение. Для `DATABASE_MIN_CONNECTIONS=0` сохранена допустимость, а сообщение об invalid range теперь явно говорит «неотрицательное» значение.

### Классификация числовых ограничений

| Класс | Значения и владелец | Решение |
| --- | --- | --- |
| Операторские runtime-настройки | Все переменные loader групп из таблицы выше: размеры пула, адреса/порты веток, HTTP-пределы и таймауты, лимиты/интервалы файлов, пагинация/экспорт, sitepack, prerender, S3 и media worker. | Не имеют runtime fallback; конкретные значения задаются в `.env.example` и проверяются loader до внешних подключений. Основные minima/cross-limit связи перечислены в разделах обязательных групп и валидации. Положительные durations и настройки вместимости без специальной верхней границы остаются явной ответственностью оператора, а не скрытым пределом в приложении. |
| Протокольные ограничения | S3 multipart: часть не меньше 5 MiB, кроме единственной части; подпись ссылок сериализует expiry с секундной точностью; размеры hash и версии ABI закреплены контрактом. | Не менять через operator config: изменение может нарушить совместимость формата или storage API. |
| Алгоритмические/safety caps | Image decode: 40 млн пикселей. Feature ZIP: 600 entries, 80 MiB на entry, 300 MiB суммарно после распаковки и compression ratio не больше 1000. Static HTML ABI: 2 MiB. Route matcher: pattern не длиннее 500 символов, request path не длиннее 4096 символов, match timeout 25 ms. Manifest/schema validator ограничивает глубину обхода 32. Media profile dimension — не более 8192 px. Media settings JSON ограничен 32 KiB; multipart framing получает 1 MiB сверх настроенного media upload limit. Prerender settle loop проверяет состояние с фиксированным 50 ms polling tick. | Эти значения ограничивают работу одного алгоритма/входного формата и остаются защитными границами в коде; настраиваемые archive upload, network, HTML, file, viewport и прочие budgets при этом идут через loaders. Не добавлять второй операторский параметр для каждой внутренней защитной проверки без отдельного требования. |
| Cache policy | `RUNTIME_ASSET_CACHE_TTL` задаёт mutable embedded runtime asset/favicon TTL; `MEDIA_REDIRECT_CACHE_TTL` задаёт legacy media redirect TTL. | Оба TTL проходят loader и передаются в response handlers; отсутствие или неверная настройка останавливает startup. Canonical file-service public response остаётся `public, no-cache`; hash/immutable assets сохраняют `max-age=31536000`; public HTML/SEO остаются `max-age=0, must-revalidate` либо `no-store` для корректной инвалидации и не получают отдельный TTL. |
| Deployment/test harness, вне процесса движка | `compose.yaml` закрепляет host ports `8080–8082`, Postgres healthcheck `5s/3s/20 retries`, MinIO console `9001`; `Dockerfile` объявляет `EXPOSE 8080`. `scripts/prerender-container-smoke.mjs` выбирает локальный test port `39091`. | Compose ports соответствуют только образцовым `BRANCHES` из `.env.example`; при других статических ветках оператор синхронизирует port mappings вручную. Эти числа не являются fallback Go-loader. |

Проверки после этой сверки: `go test -count=1 ./pkg/config` покрывает недоступные temp directories и безопасные branch errors; `go test ./cmd/api` проверяет compile/startup test suite; `go vet ./pkg/config ./cmd/api`; Compose `config` без запуска сервисов; `git diff --check`. Полные DB/browser интеграции не входят в этот пакет и не запускались.

## Cache TTL и режим чистой инициализации — 7 ноября 2026

`RUNTIME_ASSET_CACHE_TTL` и `MEDIA_REDIRECT_CACHE_TTL` заданы как обязательные engine settings и приведены в `.env.example` (`1h` и `5m`). Оба принимают только целое положительное число секунд; меньше 1s, дробное число секунд или невалидный duration останавливают `Load` с точным env name. `main.go` применяет TTL к media redirect и передаёт runtime asset TTL через HTTP dependencies в route-aware и fallback runtime. Конструкторы runtime отвергают невалидный TTL, а embedded favicon и unhashed assets используют настроенный `max-age`; content-hashed assets сохраняют immutable годовой TTL, HTML — прежнюю revalidation/no-store политику. Unit tests покрывают маршруты и формат header.

Старый одноразовый CLI переноса удалён после решения отказаться от преобразования данных. Новые установки проходят только чистую начальную схему; reset разрешён для task-owned disposable QA БД и storage. Существующие рабочие данные, включая Vitasil, не читаются и не изменяются в ходе перехода.

Проверено командами `go test -count=1 ./pkg/config ./presentation/http/handlers/media ./features/sitebuilder ./presentation/http/handlers/sitebuilder`, `go vet` для тех же пакетов и `docker compose --env-file .env.example config --no-env-resolution`. Эти проверки подтверждают настройки/cache behavior, но не говорят о браузерной миграции: такой миграции в целевом контракте нет.
