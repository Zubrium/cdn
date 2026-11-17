# Workers: task contract and implementation status

Статус: общий durable task loop реализован в `coordinator/`; production
обработчики Archive, Media и Prerender и их runtime factories ещё не реализованы.
Архитектура модулей и автопубликация описаны в
[SPEC-coordinator-platform.md](SPEC-coordinator-platform.md), а транзакционные
гарантии — в [SPEC-coordinator-implementation.md](SPEC-coordinator-implementation.md).

## Общий контракт исполнения

Worker — статически подключаемый task handler, не Engine. Coordinator выбирает
работу по `job_type`; worker получает только DTO задачи и ограниченный typed
Core capability, не получает DSN, SQL, S3 credentials или произвольные команды.
Модули не вызывают друг друга напрямую. Фабрика выбранного worker должна
зарегистрировать только поддерживаемые версии handler до `Ready`; отсутствующая
реализация должна завершить startup ошибкой, не объявляя worker доступным.

Core хранит durable job ID, system, type, idempotency key, object payload,
state, attempt counters, database-clock availability/lease timestamps, owner,
configuration epoch, fencing token, result и безопасный error code. Claim
атомарен и конкурентно-безопасен; takeover истёкшей lease получает новый
fencing token. Completion/failure/extension отклоняются для старого owner,
epoch, token или истёкшей lease. Внешние эффекты at-least-once и обязаны иметь
стабильные idempotency keys и reconciliation. Секреты и диагностические
сообщения не сохраняются в payload/error/result.

`WorkerPool` ограничивает число параллельных задач, lease duration, task
timeout, polling, retry delay и результат. Ошибка durable store делает pool
unready и останавливает его sibling workers. Обработчик обязан кооперативно
завершаться при отмене context: Go не может безопасно прервать зависшую
goroutine. Недоверенный или тяжёлый код должен исполняться в отдельно
supervised процессе/контейнере с OS CPU, memory, filesystem, network и
wall-clock limits. In-process timeout не является sandbox.

Временные ошибки получают ограниченный exponential retry со стабильным jitter;
постоянные ошибки переводят задачу в `failed`; после исчерпания попыток
оператор должен иметь авторизованный способ проверить состояние и явно
повторить задание. В Coordinator persistence есть internal `GetJob`/`Retry`, но
admin job-list/retry API и UI пока не подключены.

## Archive Worker

Принимает Core file ID ZIP, целевой FileSet и явные лимиты. Перед распаковкой
проверяет central directory, manifest и каждый entry; запрещает traversal,
абсолютные/неоднозначные пути, symlinks, дубликаты, чрезмерные размеры и
compression ratio. Выходные immutable files и FileSet membership создаёт через
Core с наследованием разрешённых групп; повторяемые шаги адресуются стабильными
ID/idempotency keys. Неполный результат не регистрируется Website как готовая
feature version. Исходный ZIP удаляется только после durable фиксации и
проверки результата. Сейчас есть только ZIP inspection; сохранение файлов,
durable retry/recovery и handler registration отсутствуют.

## Media Worker

Принимает исходный Core file ID и allowlisted preset с версией. Выход — новый
immutable Core file с согласованными группами и метаданными; исходник
сохраняется. Fingerprint включает hash источника и preset version. Декодеры
должны иметь лимиты времени, памяти, размеров, frames и числа outputs; нельзя
строить shell-команду из пользовательских параметров или разрешать произвольный
network fetch. Сейчас пакет содержит контракт, но не processor/handler.

## Prerender Worker

Принимает immutable render plan с точными snapshot/feature/provider/content
revisions и Core file IDs. Первая стадия создаёт site base, вторая — artifact
для опубликованной ревизии контента. Результаты сохраняются через Core как
immutable files. Каждая стадия повторяема по стабильному job key; ошибка второй
стадии оставляет текущую Router target активной. Chromium/runtime изолируется
от секретов и Core credentials; ограничения сети и ресурсов задаются явно.
Сейчас пакет содержит контракт, но не Chromium runner/handler и не OS sandbox.

## Приёмочные сценарии

Исполняемые tests для generic worker loop находятся в
`tests/go/coordinator/workers_test.go`, а transaction/fencing tests — в
`tests/go/core/internal/postgres/coordinator_test.go`. Они покрывают safe error
codes, результат, panic/timeout, sibling cancellation, idempotent enqueue,
concurrent reclaim, fencing token, epoch changes и атомарную route activation.
Эти tests не подтверждают extraction/processing handlers, process isolation,
реальный S3, retry после каждого внешнего эффекта, authenticated operator API
или достижение общего целевого покрытия 60% для каждого worker.

Будущие probe/OCR/transcription/export/sitemap/link-check workers допускаются
только после отдельного input/output, permission, resource-limit и retry
контракта. Loader session cleanup остаётся обязанностью Loader/Core, пока не
согласован отдельный перенос.
