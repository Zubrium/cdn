# Файловый сервис

## Адаптивные изображения и sitepack rendition

Manifest media profile является источником размеров и `fit` для производных изображений. Runtime ABI остаётся прежним. Обычное изображение рендерится как `<picture>`: WebP `srcset` с фактическими ширинами служит источником выбора, а исходный файл остаётся fallback в `<img>`. Для art direction manifest может объявить отдельные rendition-профили и соответствующие `<source media>`; это применяется к mobile/desktop композициям hero. `<img>` сохраняет `alt`, `loading`, `fetchpriority`, `decoding`, `sizes` и доступные intrinsic width/height.

В Vitasil Product использует один профиль `small` 480×360, `medium` 800×600, `large` 1200×900 во всех трёх потребителях (каталог, detail и слайдер), чтобы один файл не кодировался повторно с эквивалентными профилями. Каталог использует lazy loading для следующих карточек; первый товар и detail image загружаются eager/high. Декоративные изображения остаются без alt-текста и вне семантического содержимого.

Engine строит `srcset` по фактическим dimensions результата, сортирует ширины и убирает дубли, которые возникают при уменьшенном оригинале. Импорт sitepack восстанавливает упакованные готовые renditions и для нового файла, и при дедупликации уже существующего оригинала; уже готовые target outputs сохраняются, недостающие дополняются, а `processing` не перезаписывается. Cache policy импорта следует visibility: public immutable год, private — `private, no-store`. Декодер изображений проверяет лимит 40 млн пикселей после чтения image config и до обработки.

Предел: браузерная acceptance проверяет выбор реальных URL-кандидатов и размеры, но disposable browser test использует in-memory storage и не имитирует production CDN. Экспорт/импорт проверен через fake storage и отдельную PostgreSQL; production S3/CDN остаются отдельной инфраструктурной приёмкой.

## Ограничение частоты

Файловый HTTP API ограничивает частоту запросов независимо для каждого подтверждённого владельца; ротация ключей одного сервиса не обходит лимит. Публичные и подписанные запросы без ключа ограничиваются по сетевому адресу клиента; доверие proxy определяется общей явной настройкой сервера. Неверные ключи также ограничиваются по адресу, а сами ключи не сохраняются в счётчиках или логах. Число запросов, длительность окна и максимальное число одновременно отслеживаемых клиентов задаются оператором; при исчерпании таблицы новые клиенты получают 429 до освобождения места. Лимитер локален процессу: при нескольких экземплярах gateway должен применять общий лимит. Поток уже начатого ответа не прерывается из-за последующих запросов; повтор частей загрузки учитывается как отдельный запрос.

Статус: реализация, 4–6 ноября 2026. Основание — [TODO](TODO-admin-snapshots-content-files.md) и [спецификация снапшотов](SPEC-branches-snapshots.md). Спецификация описывает целевой контракт; проверенная часть и оставшиеся критерии перечислены ниже.

## Контракт

Реестр `media_objects` общий для медиа и произвольных файлов. Неизменяемые байты идентифицируются UUID и SHA-256. Изменение байтов создаёт новый UUID. Имя — только метаданные, физические ключи строит сервер. Владелец `file_owners` имеет тип admin/service; один admin-владелец библиотеки и отдельные service-владельцы. Сервисы автоматически регистрируются по имени из конфигурации. Отключение сервиса отзывает ключи после перезапуска; запись ID сохраняется. Команда RenameOwner сохраняет ID; оператор затем обновляет имя конфигурации.

Операторский порядок безопасного переименования файлового сервиса и ветки описан в [OPERATIONS.md](OPERATIONS.md).

API сервиса `/api/v2/files` принимает Bearer-ключ; API админки `/api/v2/admin/files` получает пользователя через прежнюю auth middleware. Права сервиса: upload, read, list, delete, link. Сервисы видят и меняют только свои объекты. Админская библиотека по умолчанию содержит только admin-owned файлы. Ключи не записываются в БД и логи; имена секретных переменных либо файлов задаются в конфигурации, сопоставление выполняется по SHA-256 с constant-time compare.

Разрешённый сервису scope `upload` включает выбор `visibility=public` или `private` для каждого объекта. Public-файл намеренно читается анонимно через публичный content endpoint; private-файл требует ключа владельца или действующей ограниченной по времени ссылки. Ключи выдаются только доверенным сервисам, которые уполномочены принимать это решение.

- POST `/uploads`: `{name,size,sha256,visibility}` → сессия, fileId, partSize, expiresAt. `visibility` обязателен (`public`/`private`); файл не виден до commit.
- PUT `/uploads/{id}/parts/{number}`: байты с точным Content-Length и заголовком X-Content-SHA256. Нумерация с 1; все части кроме последней имеют настроенный partSize. Повтор той же части идемпотентен; другие байты для существующей части дают 409.
- GET `/uploads/{id}`: состояния и принятые номера частей; возобновление после рестарта.
- POST `/uploads/{id}/commit`: проверяет полноту, размер, SHA-256; создаёт готовый файл и возвращает его. Повтор возвращает тот же fileId. Неверная checksum не создаёт ready-объект.
- DELETE `/uploads/{id}` отменяет сессию; отменённый/истёкший upload не продолжается.
- GET `/`: свои файлы с поиском, стабильным ID-курсором и настроенным лимитом.
- GET `/objects/{id}`: метаданные; GET/HEAD `/objects/{id}/content`: поток байтов.
- POST `/objects/{id}/links`: `{ttlSeconds}` → ограниченная по времени ссылка. Серверный максимум обязателен, срок запроса обязателен и не превышает максимум.
- GET `/objects/{id}/references`: известные ссылки в данных Engine и digest `revision` их списка; внешние зависимости не обнаруживаются.
- DELETE `/objects/{id}?confirm=true&referencesRevision=...`: явное подтверждение ранее просмотренного списка. При известных ссылках обязательна актуальная revision из GET; изменившийся список даёт 409 и требует повторного просмотра. Без confirm при известных ссылках — 409 с зависимостями; при отсутствии зависимостей удаление разрешено. Чтение списка, проверка revision и очистка связей выполняются в транзакции под общей блокировкой публикации/удаления, включая при единственном соединении БД. Физическая очистка ставится в очередь. Отсутствующий файл больше не выдаётся даже до завершения S3 cleanup.

## Передача и восстановление

Загрузка файлов из админки выполняется в отдельной медиатеке `/media`. Контекстный медиапикер только ищет и выбирает готовые публичные объекты, сохраняет ссылку по ID и даёт открыть медиатеку в новой вкладке. Он не загружает файлы непосредственно из полей фич, SEO или дизайн-системы. Для приватных объектов picker не создаёт публичную ссылку.

Части передаются потоково во временный файл ограниченного размера для проверки checksum до S3 Put. Часть резервируется в БД до S3 записи, поэтому crash оставляет обнаруживаемый объект. Commit потоково проверяет части и общий hash; для совместимого S3 собирает их серверным multipart copy, в остальных хранилищах использует временный дисковый файл. Память не зависит от размера файла. Он сериализуется блокировкой session-row, а повтор после отказа S3/БД повторяет запись того же object key и возвращает тот же UUID. Неподтверждённые объекты не выдаются. TTL, лимиты сессий и квоты владельца заданы конфигурацией. Уборка повторяема и удаляет все зарегистрированные ключи частей, отменённые/истёкшие сессии и очередь удалённых объектов. Используется приватный bucket; Engine не перенаправляет на публичный S3.

S3 Put не буферизует целый поток в памяти. AWS SDK рассчитывает дополнительную транспортную checksum только когда её требует протокол; API сам проверяет SHA-256 файла перед commit и хранит его в реестре. AWS SigV4 для HTTP S3-compatible endpoint может перечитать body, поэтому callers передают seekable временный файл. File API уже проверяет и записывает части во временные файлы; sitepack import по одному извлекает медиа в `SITEPACK_TEMP_DIR`, проверяет точный размер, передаёт seekable file в S3 и удаляет staging-файл. Верхние пределы импорта/распаковки из конфигурации ограничивают объём дискового staging; весь ZIP или отдельный файл не загружается в память.

Админка рассчитывает SHA-256 потоком в изолированном browser worker и отправляет части по тому же API. При разрыве она предлагает повторить загрузку выбранного файла и пропускает уже подтверждённые части по идентификатору сессии и контрольной сумме. Для продолжения после закрытия браузера пользователь повторно выбирает исходный файл; без доступа к его байтам загрузку завершить невозможно. Явная отмена отменяет сессию, а автоматически прерванная загрузка очищается по TTL.

Производные изображения и видео обрабатываются FFmpeg через временные файлы: ни оригинал, ни результат не читаются целиком в память. Временная директория и таймаут обработки задаются оператором. Недостаток диска и ошибка обработки приводят к состоянию failed с возможностью повторить обработку; оригинал и его скачивание сохраняются. Контент-тип определяется по первым байтам. SVG размером не больше настроенного размера части полностью проверяется по допустимому подмножеству; небезопасный или превышающий предел SVG сохраняется как произвольное вложение и не допускается как изображение. WOFF2 определяется по сигнатуре, а не по имени файла. Классификация не изменяет байты и контрольную сумму.

Потоковое чтение поддерживает один HTTP byte Range, If-None-Match, HEAD и nosniff. Для S3 используется ranged GetObject; fallback ObjectStore пропускает префикс без накопления в памяти. Часть диапазона проверяется относительно размера. Неизвестные и активные типы (HTML/SVG/JS/SH) возвращаются attachment и CSP sandbox; разрешённые raster/video/font типы могут отображаться inline. Имя сохраняется как метаданные, но перед использованием в `Content-Disposition` очищается до basename и удаляются все Unicode control characters; обычные международные имена сохраняются. Частные ответы private,no-store; подписанные ссылки не становятся бессрочными из-за кеша. Существование файла, visibility и подпись проверяются на каждом запросе. Удаление файла отзывает все его ссылки.

`If-None-Match` использует слабое сравнение тегов, поддерживает списки и `*`; разделители списка распознаются только вне кавычек, поскольку запятая допустима внутри opaque-tag. Сначала проверяются существование файла и доступ, затем сравнивается ETag: приватный объект без доступа не выдаёт 304 и не раскрывает ETag. Публичный ответ использует `public, no-cache` с повторной проверкой; приватный и подписанный — `private, no-store`. Если `If-Range` не совпадает с ETag, сервер возвращает полный ответ вместо частичного.

## Конкурентность и права

Файловое удаление и структурные/content публикации используют общий transaction advisory lock `websiteengine_file_references`, взятый до snapshot/media row locks. Чистка ссылок выполняется callback `RemoveReferences(ctx, tx, id)` в той же БД-транзакции. Контрольные суммы структуры пересчитываются только при согласованном удалении файла; ревизии затронутых drafts/content повышаются, чтобы старые формы не возвращали удалённые ссылки. Ошибки storage не маскируются под успех загрузки. Контентные публикации не должны принимать отсутствующий/закрытый файл для публичной выдачи без правил доступа.

## Обязательная конфигурация

`FILES_MAX_BYTES` (до 5368709120), `FILES_PART_BYTES`, `FILES_UPLOAD_TTL`, `FILES_CLEANUP_INTERVAL`, `FILES_PROCESS_TIMEOUT`, `FILES_OWNER_QUOTA_BYTES`, `FILES_MAX_SESSIONS`, `FILES_LIST_LIMIT`, `FILES_LINK_MAX_TTL`, `FILES_PUBLIC_BASE_URL`, `FILES_TEMP_DIR`, `FILES_SIGNING_KEY_ENV` или `FILES_SIGNING_KEY_FILE`, `FILE_SERVICES` (массив, может быть пустым). Секрет подписания минимум 32 байта. Сервис: name, permissions, keys (env/file). В приложении нет скрытых operational defaults.

## Приёмка

Изолированная PostgreSQL/S3: сервисы A/B и admin не обходят ownership; отсутствующий/отозванный ключ, недостаточное разрешение, приватный public URL и S3 не доступны. Upload: разрыв/повтор части/несовпадающие байты/неполный commit/checksum/повтор commit/рестарт/TTL/abort/квота/5 ГБ без чтения целиком в RAM. Чтение: HEAD/range/suffix/416/ETag/expiry/attachment и удалённый файл. Delete: предупреждение, подтверждение, очистка ссылок и повторная storage cleanup. Визуальный и E2E прогон админки выполняется в disposable среде. Этот документ не является свидетельством прохождения приёмки.

### Очистка ссылок при подтверждённом удалении

В конфигурации снапшота очищаются только явные media-поля metadata, дизайн-системы и параметры размещений с `format: media` в закреплённом манифесте. В опубликованном контенте, черновиках, истории публикаций, сохранённых версиях и заявках используется схема соответствующей версии; строка, совпадающая с UUID файла вне media-поля, сохраняется. Очищенное значение становится пустой строкой; после изменения обновляются ревизии и digest снапшота. Изменения выполняются в одной транзакции с удалением, под блокировкой ссылок. Триггер разрешает в payload только замену удаляемого идентификатора пустой строкой.

Исторические настройки без надёжной привязки к схеме пока проверяются консервативно: при совпадении идентификатора операция отклоняется во избежание потери данных. Предупреждения для современных данных также используют закреплённые схемы; обычный текст с совпадающим UUID в них не попадает.

## Проверка реализации, 6 ноября 2026

На disposable PostgreSQL `files_qa` и отдельном MinIO `127.0.0.1:39000` прошла команда:

```sh
TEST_FILES_DATABASE_URL='postgres://plan_runner:…@127.0.0.1:35432/files_qa?sslmode=disable' \
TEST_FILES_S3_ENDPOINT='http://127.0.0.1:39000' \
TEST_FILES_LARGE=1 \
TEST_VITASIL_ASSETS_DIR='/Users/docup/Projects/vitasil/backend/assets' \
go test -count=1 ./features/files
```

Проверены реальные S3 private ACL и ranged read; multipart composition 32 MiB с повторным commit; HTTP API round trip с upload, metadata, проверкой владельца, Range, ограниченной ссылкой, delete и отзывом ссылки; потоковый 5 GiB service stream через bounded-memory виртуальное хранилище; типизация реальных Vitasil SVG и WOFF2. `TestActualS3HTTPFiveGiBTransfer` передал физические 5 GiB по HTTP в disposable MinIO 80 частями по 64 MiB, проверил checksum и server-side composition, размер объекта, хвостовой Range, полное потоковое скачивание с проверкой объёма и SHA-256, затем удаление итогового объекта и всех частей. HTTP tests используют зарегистрированные service routes в `httptest.Server`, не браузер или внешний gateway.

Эти проверки не подтверждают полный production запуск, браузерную галерею, отказ/рестарт всего процесса, ротацию ключа на работающем сервере, мультирепличный rate limit, CDN/gateway, либо все роли и зависимости в UI. Поэтому раздел «Приёмка» остаётся открытым.

### Дополнительный focused audit, 6 ноября 2026

На выделенных disposable `files_qa` и MinIO `127.0.0.1:39000` повторно прошли `go test -count=1 -v ./features/files ./presentation/http/handlers/files`, тот же набор с `-race` и `go vet ./features/files ./presentation/http/handlers/files`. Проверены изоляция владельцев, повтор и возобновление загрузки после перезапуска сервиса, ротация и отзыв старого ключа при повторной инициализации, лимиты/TTL/abort/cleanup, закрытая выдача по Range и HEAD, диапазон 416, срок подписанной ссылки и отказ после удаления, S3 multipart composition и HTTP API round trip, ограничения роли админского удаления и конкурентное удаление. Добавлен regression test строгого разбора `FILE_SERVICES`: принимается один JSON-массив; `null`, неверный тип и завершающие JSON-значения отклоняются. Условный повторный запрос через `If-None-Match` тогда отдельно не проверялся; покрытие добавлено в проверке ниже.

В этом повторном запуске не задавались `TEST_FILES_LARGE`, `TEST_FILES_5GIB` и `TEST_VITASIL_ASSETS_DIR`; поэтому bounded-memory 5 GiB service stream, физическая 5 GiB HTTP/S3 передача и классификация реальных SVG/WOFF2 Vitasil были пропущены. Их прежние успешные прогоны остаются отдельными доказательствами выше и в [QA отчёте](QA_REPORT.md); текущий запуск их не повторяет и не опровергает. Совместимость через production gateway и production S3/CDN, браузерный workflow и полный restart всего процесса по-прежнему не проверены. Приёмка файлового API остаётся открытой.


### Условное чтение и cache revalidation, 6 ноября 2026

Условные запросы проверены на disposable PostgreSQL `files_qa`; suite также использовал disposable MinIO `127.0.0.1:39000`. Unit regression `TestConditionalMatchParsesEntityTagList` проверяет weak validator, список entity-tags, wildcard и opaque-tag с запятыми. `TestHTTPConditionalRequestsRespectVisibilityAndCachePolicy` проверяет публичные 200/304 и `public, no-cache`, stale/matching `If-Range`, авторизованный private 304 с `private, no-store`, отказ анонимному запросу с совпадающим ETag до условной обработки и недоступность удалённого файла при старом ETag. Реальный MinIO HTTP roundtrip дополнительно проверяет private ETag, Range и 304 для owner key и подписанной ссылки.

Прошли `go test -count=1 -v ./features/files ./presentation/http/handlers/files` с `TEST_FILES_LARGE=1` и `TEST_VITASIL_ASSETS_DIR`, `go test -race -count=1 ./features/files ./presentation/http/handlers/files` и `go vet ./features/files ./presentation/http/handlers/files`. HTTP API тестируется через `httptest.Server`, storage — реальный disposable MinIO; production gateway, общий reverse-proxy cache и CDN этим срезом не проверяются. Частное условное чтение покрыто, но общее acceptance по CDN/gateway остаётся открытым.

### Админская медиатека и media picker, 6 ноября 2026

Дополнительно пройдена браузерная приёмка `cmd/integration/files_media_browser_test.go` на выделенных disposable PostgreSQL `website_engine_files_media_qa_20261106_01` и MinIO bucket `files-media-qa-20261106-01` (локальные порты 39831/39832). Интеграционный harness создаёт отдельный API с новым `files.Service`, связывает его с прежней admin-медиатекой и не использует Vitasil API, БД или storage.

В реальном браузере прошли поиск и фильтр по типу, превью публичного изображения и известной ссылки на снапшот, скрытие service-owned файлов, отсутствие кнопки удаления для роли user, выбор только публичных admin-owned файлов в picker, отсутствие file input в `/site` и picker, ссылка на `/media` в новой вкладке, resumable upload из `/media`, предупреждение перед удалением, отмена без удаления, подтверждённое удаление и очистка ссылки с изменением snapshot digest. После удаления публичный URL с устаревшим `If-None-Match` вернул 404, а не 304.

Отдельный API сценарий с `-race` подтвердил изоляцию admin и service owners A/B, запрет удаления для read-only ключа, отказ в выдаче private файла публичным Engine URL и анонимному запросу в MinIO, авторизованное private чтение, 409 при устаревшей revision ссылок без удаления, а также замену файла новым immutable UUID/SHA-256 с сохранением предыдущего объекта.

Для Go-пакета прошли `go test -count=1 -v ./features/files ./presentation/http/handlers/files`, `go test -race -count=1 ./features/files ./presentation/http/handlers/files` и `go vet ./features/files ./presentation/http/handlers/files` на той же disposable PostgreSQL с тестовым fake storage. Эти сценарии проверили поиск/фильтры owner-scoped медиатеки, отказ и повтор загрузки, восстановление сессии после перезапуска сервиса, TTL/abort/cleanup, права, удаление, Range, private cache validators и rate limits. S3-dependent тесты и тесты, требующие реальных Vitasil assets, в этих пакетных командах были пропущены. Fixture теперь принимает только уникальный префикс DB `website_engine_files_media_qa_*`, отклоняет старые `files_qa`/`engine_files_goal`, а при включённом S3 требует loopback endpoint и уникальный маркер bucket `files-media-qa-*`.

Дополнительно прошли `go test -race -count=1 -v ./cmd/integration -run '^TestAdminMediaGallerySharesAdminFileOwnerAndServiceKeysStayScoped$'` и `go test ./cmd/integration -run '^TestAdminMediaGalleryAndPickerBrowserAcceptance$' -count=1 -v`. Frontend после изменений прошёл Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (60 тестов) и `pnpm build`.

Физическую передачу 5 GiB в этом прогоне не повторяли: её результаты указаны в предыдущих разделах и остаются отдельным свидетельством. Production gateway/CDN, многоэкземплярный rate limit и production storage не проверялись; сквозной прогон Vitasil остаётся ответственностью общей приёмки. Общий статус файлового API остаётся открытым до проверки этих границ.

### Удаление сервисного файла и восстановление после перезапуска, 7 ноября 2026

На уникальных disposable PostgreSQL `website_engine_files_media_qa_20261107_82e1` (`127.0.0.1:35441`) и MinIO bucket `files-media-qa-20261107-82e1` (`127.0.0.1:39041`) прошли:

- `go test -count=1 ./features/files` — прошёл с loopback MinIO; `TEST_FILES_5GIB` и путь к реальным Vitasil assets не задавались, поэтому физическая 5 GiB передача и Vitasil классификация в этом прогоне были пропущены.
- `go test -count=1 -run '^TestServiceOwnerDeleteClearsConfirmedSnapshotContentAndSubmissionReferences$' -v ./cmd/integration` — service owner получил известные ссылки на файл, удаление без подтверждения и с устаревшей revision вернуло конфликт; подтверждение актуальной revision удалило объект, очистило только медиа-поля в snapshot config/content draft/published/content history и form submission, сохранило текст, запись заявки и назначение ветки. Подписанная ссылка после удаления вернула 404; обработка очереди физически удалила объект из disposable MinIO.
- `go test -count=1 -run '^TestFilesHTTPResumesPrivateUploadAfterAPIRestartWithS3$' -v ./cmd/integration` — незавершённая multipart-сессия пережила остановку HTTP server, создание нового `files.Service` и нового S3-клиента; после повторного commit сохранился file ID и SHA-256. Закрытый файл читался через авторизованный API и краткоживущую ссылку, а публичный Engine URL оставался закрытым.
- `go test -run '^$' ./cmd/integration` — compile-only проверка integration package; `gofmt` и `git diff --check` для добавленного теста прошли.

Для каждого destructive integration test проверялись точная `current_database()` с ожидаемым префиксом и ноль других активных сессий непосредственно перед `newTestEnv`; порты были привязаны только к созданным для теста loopback контейнерам. Живой Vitasil и его API/БД/storage не использовались. Перезапуск смоделирован восстановлением file service, S3 client и HTTP router внутри теста; рестарт полного `cmd/api` контейнера не проверялся. Отказ настоящего S3, production gateway, production S3/CDN и общий multi-instance rate limit остаются непроверенными; покрытие через CDN можно считать закрытым только после проверки конкретного gateway/CDN, которого сейчас нет в isolated окружении.

### Нормализация фильтров галереи, 7 ноября 2026

`/media` нормализует принадлежащие галерее query-параметры до поддерживаемых значений и не удаляет посторонние query-параметры. Фильтры даты принимают реальные календарные даты `YYYY-MM-DD`, а размер в МБ — конечное неотрицательное десятичное значение, безопасно преобразуемое в байты. Неверные enum/date/size query значения не отправляются в API. При обратном диапазоне дат или размера выдача не запрашивается; интерфейс показывает ошибку диапазона до загрузки списка.

Разрешение удаления в двух представлениях списка использует общий gallery helper: только admin видит destructive action; проверяется для всех ролей admin/moderator/staff/user. В desktop-таблице у кнопки открытия файла с клавиатуры теперь видимый focus ring. Загрузка имеет доступное status-сообщение. Если ошибка пагинации/обновления возникает после получения файлов, уже показанные файлы остаются видимыми и рядом появляется явное действие повтора; ошибка первоначальной загрузки остаётся отдельным состоянием.

Unit coverage: `gallery-filters.test.ts` проверяет разбор, безопасную нормализацию, сохранение посторонних параметров, значения по умолчанию, MB-конверсию, обе ошибки диапазона и отдельные состояния загрузки/ошибки/пустого/имеющихся результатов; `gallery-access.test.ts` проверяет роли. Проверки: Node 20 `pnpm typecheck`, `pnpm lint` и `pnpm test` (15 файлов, 67 тестов) завершились успешно. Этот срез не является новым browser-регресс-прогоном; ранее описанные disposable browser проверки owner-scoped галереи и picker остаются отдельным свидетельством.

### Vitasil content media через disposable S3, 7 ноября 2026

`TestVitasilContentMediaS3Acceptance` прошёл в Chromium на отдельной PostgreSQL `website_engine_vitasil_media_s3_qa_20261107_01` и одноразовом приватном MinIO `website-engine-vitasil-media-s3-qa-20261107-01` (`127.0.0.1:39083`), bucket `files-media-qa-vitasil-s3-20261107-01`. Контейнер использовал только loopback port mapping и `/data` tmpfs размером 512 MiB, без persistent mounts. Перед тестом проверены точное имя БД, loopback endpoint и отсутствие других сессий.

Из read-only каталога текущих Vitasil сборок в изолированный Engine API зарегистрированы ZIP `catalog@1.0.10`, `main-data@1.0.10` и `background@1.0.7`. Через `/admin/media` загружены два публичных изображения; их разные ID, admin ownership и фактические байты в MinIO проверены. Контентный picker показал публичный admin-owned файл и исключил private-файл и публичный файл service owner. Picker не содержит file input; в `/site` нет загрузки/назначения ветки, а content-managed поле изображения текущего Vitasil background не выводится. Замена значения `catalog.imageSrc` сохранила новый immutable ID и не удалила предыдущий объект; draft сохранился и опубликовался.

Публичный collection API вернул hydrated Engine URL `/media/{id}?profile=…&variant=…`; body не содержит S3 endpoint или bucket. Превью успешно декодировалось и полная передача, а публичный image decode и запрос `Range: bytes=0-7` прошли через Engine `/media` с HTTP 206. Анонимное чтение публичного и private object по прямому S3 URL вернуло 403; публичный Engine URL private-файла вернул 404. Удаление referenced объекта показало использование в каталоге: отмена сохранила draft и файл, подтверждение очистило ссылку из draft, публикации и публичной выдачи, а deferred cleanup удалил bytes из MinIO.

Проверки завершились успешно:

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:35474/website_engine_vitasil_media_s3_qa_20261107_01?sslmode=disable' \
TEST_FILES_S3_ENDPOINT='http://127.0.0.1:39083' \
TEST_FILES_S3_BUCKET='files-media-qa-vitasil-s3-20261107-01' \
TEST_FILES_S3_ACCESS_KEY='<disposable>' TEST_FILES_S3_SECRET_KEY='<disposable>' \
TEST_FEATURE_DIR='/Users/docup/Projects/vitasil/frontend/dist/feature-builds' \
PRERENDER_TEST_NODE='/Users/docup/.nvm/versions/node/v20.19.4/bin/node' \
PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' \
PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' \
go test -count=1 -v ./cmd/integration -run '^TestVitasilContentMediaS3Acceptance$'

go vet ./cmd/integration
gofmt -w cmd/integration/vitasil_content_media_s3_acceptance_test.go
git diff --check
```

Для валидных WebP вариантов harness использует установленный `cwebp` acceptance processor, потому что локальный FFmpeg собран без `libwebp`; производственная FFmpeg обработка не считается проверенной этим тестом. Другие Vitasil runtime assets, все media роли и production gateway/CDN в этом узком сценарии не проверялись. Рабочая Vitasil установка, её API, БД, S3 и volumes не использовались.

После теста: ноль активных подключений в указанной БД; удалены только эта disposable БД, её bucket/objects вместе с tmpfs MinIO контейнером и compile-only файл `/tmp/website-engine-vitasil-media-s3-qa-20261107-01.test`. Персистентных mounts и оставшихся test products у этого acceptance нет.
