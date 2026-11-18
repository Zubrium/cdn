# Worker контракты и безопасность исполнения

Статус: нейтральный worker contract согласован; production обработчики и изоляция тяжёлого кода реализуются по отдельным acceptance пакетам. Coordinator и общие гарантии определены в [SPEC-coordinator-platform.md](SPEC-coordinator-platform.md).

## Worker и Core

Worker — статически включённый обработчик зарегистрированной component operation/version. Он не является engine и не обращается к PostgreSQL, S3 или cache напрямую. Вход и результат — ограниченные нейтральные DTO; file IDs, engine record IDs и иные ссылки остаются непрозрачными для Coordinator.

Handler получает context с отменой, deadline и данные одной задачи. Он возвращает ограниченный JSON result, безопасный machine error code или список дочерних операций. Регистрация передаётся из статической composition root; профиль не может указать произвольный импорт, команду или executable.

Внешние эффекты могут выполняться более одного раза: гарантия Coordinator — at-least-once. Каждый handler обязан использовать стабильный idempotency key либо reconciliation до повторного внешнего действия. Секреты передаются через Core capability/secret provider или opaque references; они не помещаются в task payload, result, logs или error code.

## Retry, timeout и граница процесса

Core ограничивает concurrency, task timeout, attempt count, lease, polling, exponential backoff, payload/result размер и число динамических дочерних задач. Ошибка хранения, отказ renewal или потеря instance lease делает Core unhealthy и останавливает claims. Потеря lease отдельной задачи ожидаема: устаревший worker не может обновить задачу или породить детей после смены fencing token.

Отмена context — кооперативная. Go не может безопасно завершить зависшую goroutine. CPU/memory-heavy, network-facing или недоверенная обработка (в том числе Chromium и декодеры медиа) должна исполняться в отдельно supervised процессе/контейнере с явными CPU, memory, filesystem, network и wall-clock ограничениями. In-process timeout не является sandbox.

Ошибки handler нормализуются к bounded machine code. Диагностический текст, stack trace, payload и результат не возвращаются в административную сводку. Повтор разрешается отдельной авторизованной операцией; уже совершённый внешний side effect не отменяется Core.

## Специфичные worker сценарии

### Archive

Archive импортирует ZIP как обычный Core file reference, валидирует central directory, manifest и каждый entry до записи результата. Запрещены absolute/traversal/ambiguous paths, symlinks, дубликаты, чрезмерные размеры, число записей и compression ratio. Для распаковки проверяются хэши нераспакованных bytes; вывод создаётся как immutable Core files в наборе, созданном Website по configuration. Частичный output не помечается готовым. Повторяемые шаги используют стабильные keys; исходный ZIP удаляется только после durable фиксации и проверки результата.

Сейчас существует ZIP inspection в `workers/archive/inspect.go`; полноценный Core-backed import handler и его runtime factory ещё не подключены.

### Media

Media принимает immutable Core file ID и allowlisted preset с версией. Output — новый immutable file с согласованными metadata и правами. Fingerprint включает checksum источника и preset version. Декодеры ограничиваются по времени, памяти, размеру, frame count и output count. Произвольные shell-команды и network fetch запрещены.

Сейчас есть контракт `workers/media/contract.go`; processor и task handler не реализованы.

### Prerender

Prerender получает версионированный opaque render plan, а не предметные типы в Core. Каждый результат сохраняется через ограниченную Core file capability. Результат одной стадии не публикуется до полной проверки. Сбои оставляют текущее опубликованное состояние движка без изменений; публикация и Router activation являются product-owned workflow, зарегистрированным как обычная component operation.

Сейчас есть контракт `workers/prerender/contract.go`; Chromium runner, handler и изоляция процесса не реализованы.

## Приёмочные сценарии

Каждый отдельный набор входных данных считается отдельным сценарием согласно [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md). Для общего WorkerPool нужны реальные проверки: дубликатная регистрация, неверная версия, payload/result bounds, cancellation, timeout, panic, store failures, sibling cancellation, retry/backoff, delayed tasks, atomic child creation, concurrent claim, lease takeover, stale fencing и shutdown. Unit проверки с fake полезны, но не заменяют disposable PostgreSQL.

Для Archive нужны реальные повреждённые ZIP, path traversal (включая Windows drive paths), symlink, duplicate entries, max-size/count/ratio и digest сценарии; затем — end-to-end сохранение в disposable Core/S3. Для Media и Prerender знаменатель acceptance сначала задаётся их публичными контрактами и ресурсными лимитами. Ни contract-only пакет, ни описание теста не считаются покрытой функциональностью.
