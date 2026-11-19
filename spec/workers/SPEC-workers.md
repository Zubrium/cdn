# Worker контракты и безопасность исполнения

Статус: контракты и частичные handlers реализованы, но Archive/Media/Prerender не подключены к production composition и не прошли приёмку. Website API для import/build/media остаётся fail-closed. Coordinator и общие гарантии определены в [SPEC-coordinator-platform.md](../architecture/SPEC-coordinator-platform.md).

## Worker и Core

Worker — статически включённый обработчик зарегистрированной component operation/version. Он не является engine и не обращается к PostgreSQL, S3 или cache напрямую. Вход и результат — ограниченные нейтральные DTO; file IDs, engine record IDs и иные ссылки остаются непрозрачными для Coordinator.

Target operation identities неизменны: компонент `worker.archive` предоставляет `feature-import` версии `1`, `worker.media` — `derivative` версии `1`, `worker.prerender` — `render-page` версии `1`. Owner задаётся только статической composition; он не входит в request payload. Движок вызывает зарегистрированную операцию через `WorkflowClient`, а Core принимает только generic operation/version и не импортирует worker-пакеты. Изменение DTO несовместимым образом требует новой `ContractVersion`, а не неявного переиспользования версии `1`.

Handler получает context с отменой, deadline и данные одной задачи. Он возвращает ограниченный JSON result, безопасный machine error code или список дочерних операций. Регистрация передаётся из статической composition root; профиль не может указать произвольный импорт, команду или executable.

Внешние эффекты могут выполняться более одного раза: гарантия Coordinator — at-least-once. Каждый handler обязан использовать стабильный idempotency key либо reconciliation до повторного внешнего действия. Секреты передаются через Core capability/secret provider или opaque references; они не помещаются в task payload, result, logs или error code.

## Retry, timeout и граница процесса

Core ограничивает concurrency, task timeout, attempt count, lease, polling, exponential backoff, payload/result размер и число динамических дочерних задач. Ошибка хранения, отказ renewal или потеря instance lease делает Core unhealthy и останавливает claims. Потеря lease отдельной задачи ожидаема: устаревший worker не может обновить задачу или породить детей после смены fencing token.

Отмена context — кооперативная. Go не может безопасно завершить зависшую goroutine. CPU/memory-heavy, network-facing или недоверенная обработка (в том числе Chromium и декодеры медиа) должна исполняться в отдельно supervised процессе/контейнере с явными CPU, memory, filesystem, network и wall-clock ограничениями. In-process timeout не является sandbox.

Ошибки handler нормализуются к bounded machine code. Диагностический текст, stack trace, payload и результат не возвращаются в административную сводку. Повтор разрешается отдельной авторизованной операцией; уже совершённый внешний side effect не отменяется Core.

## Специфичные worker сценарии

### Archive

Archive импортирует ZIP как обычный Core file reference, валидирует central directory, manifest и каждый entry до записи результата. Запрещены absolute/traversal/ambiguous paths, symlinks, дубликаты, чрезмерные размеры, число записей и compression ratio. Для распаковки проверяются хэши нераспакованных bytes; вывод создаётся как immutable Core files в наборе, созданном Website по configuration. Частичный output не помечается готовым. Повторяемые шаги используют стабильные keys; исходный ZIP удаляется только после durable фиксации и проверки результата.

`workers/archive/handler.go` реализует ограниченный Core-backed импорт: source spooling, ZIP/manifest validation до output writes, scoped CAS progress, idempotent immutable file creation и FileSet membership reconciliation. `Handler.TaskHandler` и descriptors адаптируют его к generic Coordinator identity, но handler не зарегистрирован в production composition; source cleanup и сквозная приёмка отсутствуют.

### Media

Media принимает immutable Core file ID и идентификатор/версию preset из серверного allowlist. Запрос фиксирует `job_id`, `source_file_id`, `profile`, `profile_version` и idempotency key; произвольные параметры FFmpeg, пути и команды клиент не передаёт. Fingerprint включает Core checksum источника и точную версию preset. Повтор идентичного fingerprint возвращает прежнюю задачу/результат.

Обработчик запускает фиксированный FFmpeg executable без shell в отдельно ограниченном процессе с закрытым окружением, приватной временной директорией и отключёнными сетевыми protocols. Он проверяет входные stream types и фактические output MIME/container, применяет явные CPU/memory/time/input/output/frame/stream/concurrency limits, а preset registry и его версии задаются конфигурацией. Output — новый immutable Core file с checksum, content type и размером; он наследует актуальные access groups источника и только согласованные FileSet memberships. Оригинал не изменяется и не удаляется. Частичный результат не регистрируется как готовый файл; failure/cancel очищает принадлежащие job временные данные и оставляет прежние outputs без изменений.

`workers/media/handler.go` содержит versioned worker/task handler, durable scoped job state, preset fingerprint, Core-backed immutable derivative и source-group propagation. `workers/media/ffmpeg.go` ограничивает staged bytes, protocol arguments, временной каталог и wall-clock, но явно не является OS sandbox; `NewHandler` отклоняет такой runner, пока composition не предоставит внешний supervisor. Перенос FileSet memberships также пока недоступен через текущую Core file capability. Production factory, supervisor и сквозная приёмка отсутствуют.

### Prerender

Prerender получает версионированный render plan (`render-plan/v1`) как ограниченный JSON payload, подготовленный Website/внешней composition. Plan включает digest всех входов, точные snapshot/build revisions, маршруты/placements, публичную content projection и references на feature assets; он не содержит credentials, secrets, S3 paths, пользовательские executable paths или произвольные команды. Coordinator хранит plan как opaque payload; Core не парсит его. Worker не вызывает Website, Content или Router API и не получает их storage access.

Для исполнения worker запускает Chromium в отдельно supervised процессе/контейнере с явными CPU, memory, filesystem, process и wall-clock limits. Сеть отключена по умолчанию; включить можно только явно описанный безопасный allowlist публичных GET/HEAD endpoints, с блокировкой loopback, private, link-local и metadata адресов на каждом redirect/DNS resolution. Worker исполняет `static-lifecycle-v2` согласно [SPEC-feature-runtime.md](../engines/website/features/SPEC-feature-runtime.md), ждёт завершения ограниченных render/readiness стадий и записывает HTML/assets только через ограниченную Core file capability. Output содержит проверяемый artifact manifest file ID и input digest; результат становится `ready` только после проверки всех файлов и ссылок. Ошибка, cancellation, timeout или несовпадение digest не заменяет предыдущий активный artifact. Router activation остаётся отдельным явным клиентским/API действием.

`workers/prerender/contract.go` и `validation.go` задают/проверяют ограниченный envelope и artifact result metadata; `FailClosedWorker` прекращает обработку с `ErrRenderUnavailable` и не создаёт output. Payload остаётся семантически opaque; typed Website/page/runtime projection, Chromium renderer, OS supervisor, Core persistence и production factory отсутствуют. Дополнительно текущий Coordinator не предоставляет owner-scoped чтение результата дочерней task: `WorkflowClient.Get` возвращает workflow result root task, а Admin JobControl скрывает task results. Website не может безопасно получить artifact IDs дочернего worker через существующие capabilities. Core остаётся заморожен; Website API поэтому fail-closed, пока generic Coordinator contract не разрешит эту композицию отдельным согласованным изменением.

## Приёмочные сценарии

Каждый отдельный набор входных данных считается отдельным сценарием согласно [ACCEPTANCE-MATRIX.md](../../tests/ACCEPTANCE-MATRIX.md). Для общего WorkerPool нужны реальные проверки: дубликатная регистрация, неверная версия, payload/result bounds, cancellation, timeout, panic, store failures, sibling cancellation, retry/backoff, delayed tasks, atomic child creation, concurrent claim, lease takeover, stale fencing и shutdown.

Для Archive нужны реальные повреждённые ZIP, path traversal (включая Windows drive paths), symlink, duplicate entries, max-size/count/ratio и digest сценарии; затем — end-to-end сохранение в disposable Core/S3. Для Media — реальные и некорректные форматы, preset/version allowlist, детерминированный fingerprint, quota/timeout/cancel/restart/retry, выходные метаданные/checksum, Core authorization/revocation, cleanup и неизменность оригинала. Для Prerender — plan version/digest/input mismatch, валидный HTML/assets, readiness и runtime errors, network denial/allowlist redirect/DNS, process limits, cancellation/timeout/restart, безопасное отсутствие частичного output и сохранение прежнего artifact. Unit проверки с fake полезны, но не заменяют disposable PostgreSQL/S3 и OS-enforced process isolation.
