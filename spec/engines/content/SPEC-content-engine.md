# Content Engine: целевая спецификация

Статус: согласованная граница Content для новой архитектуры. HTTP-контракт и реализация могут уточняться только вместе с этой спецификацией и приёмочными сценариями.

## Владение данными и границы

Website owns the system-wide `dataModel` and each page's immutable structure separately. System model changes are shared across all pages and do not create or modify a page snapshot. Page snapshot owns one page's placements and structural bindings; it contains no route. Content does not create or edit either Website-owned contract.

Content owns system-scoped records, drafts, published records and publication history. Every record belongs to one configured locale and has its own server-generated record ID; translated records are separate records linked by a stable `translation_id`. Draft, publication pointer and history are independent per locale. Pages share the same locale-specific records. Placement selections/order are scoped to `(page_snapshot_id, locale)` and reference records in that locale. A page fork may copy page-scoped selections/order for each locale, but never forks system-wide records or publication history.

Content Engine does not call Website Engine and receives no Website facade/resolver. Trusted outer Presentation/composition obtains separate immutable projections through Website capability: a system-scoped `ContentModelProjection` (model revision/digest and validated `dataModel`) and a page-scoped `PagePlacementProjection` (page snapshot ID/revision/digest and placements/bindings). These typed DTOs are never decoded from client-controlled request bodies. Content validates digest, page scope, and revision on each operation. Durable copy workflows carry bounded versioned projections as opaque Coordinator task data; Content does not call Website to validate state. Core is the only persistence gateway for `engine_data.records`; byte access remains through Core.

In `engine_data.records`, system-wide workspace/current publication/history are stored in the canonical global scope. Page-scoped selections use page snapshot scope and their own revision/CAS. Namespaces and payload budgets remain configured through Core. This split prevents a child page snapshot from duplicating or diverging shared records while preserving independent page editing.

В v1 одна рабочая область является одним bounded JSON aggregate: объём draft и опубликованного содержимого не превышает активный `engine_data.MaxPayloadBytes` Core (верхняя граница Core — 16 MiB). Сохранение сверх лимита отклоняется полностью с `413`; часть записей не применяется. Список истории публикуется курсорно, а записи одного workspace загружаются одним запросом, чтобы сохранять draft и selections атомарно. Если фактический контент регулярно достигает лимита, следующая версия должна перейти на staging revisions/chunks с атомарной сменой указателя, не увеличивая скрыто лимит Core.

Все люди и сервисные интеграции аутентифицируются как один тип пользователя. Чтение Content требует уровень `1` в настроенной группе `content`; изменение черновика, порядка записей, выборов размещений и удаление записи требуют уровень `2`. Публикация, восстановление и просмотр истории используют те же индивидуальные групповые права: read требует `1`, запись — `2`. Сервер повторно проверяет права на каждый запрос.

## Locale configuration and translated records

Content owns the system locale catalog: the configured supported locale codes, one required `default_locale`, and a revision used for CAS. Operators manage this catalog through the Admin Content API. Locale codes are canonical BCP 47 language tags; duplicates after canonicalization are rejected, and the default must be in the supported list. Removing a locale is rejected while it has draft or published records, page selections, or a Router target; operators must explicitly unpublish and remove those references first.

Each record has one `locale` and one stable `translation_id`; its server-generated `id` remains unique to that locale's record. A translation group contains at most one record per locale and is constrained to one entity. Creating a translated record uses the existing translation ID; creating a new group generates it server-side. Record validation, uniqueness, singleton cardinality, draft revision, publication pointer, and history are evaluated independently per locale. Locale is required on workspace reads/writes, publication, history, and restore. A publish affects only that locale.

A placement selection is also locale-scoped. Collection order and selected record IDs reference records of the same locale; the page snapshot may therefore have different selections/order per locale. At render time, only the selected locale's published records and selections are projected. A missing translation is omitted or yields the documented empty input for that port; values from another locale are never substituted.

## Рабочая область контента

Admin selects a page snapshot and locale separately. External Content Presentation supplies the trusted Website projections plus the locale catalog/revision to Content: `ContentModelProjection` defines the shared model; `PagePlacementProjection` defines the selected page's placement bindings. Workspace returns only records and publication state for the requested locale plus selections/order for the `(page_snapshot_id, locale)` scope. Neither projection is client-authored, and Content does not persist Website structure as authoritative state.

`PUT workspace` saves the complete draft and selections for exactly one locale with `expected_revision` CAS and increments that locale's revision. New records carry a request-local `client_key`; translated records may include the existing server-generated `translation_id`, while new groups omit it and receive an ID from Content. The response maps client keys to generated record IDs and translation IDs. Reusing a translation ID requires the same entity and an unused locale. Uniqueness and singleton constraints are enforced within the locale. Repeating `Idempotency-Key` with the same payload before the next successful mutation returns the prior response; a changed payload conflicts. The latest receipt is stored atomically with the workspace, and stale revisions are rejected by CAS. Deleting a locale-specific record atomically removes only selections referencing that record in the same locale; it does not affect its translations or other page snapshots.

Черновые значения необязательны, включая вложенные поля; обязательность проверяется для публичных отправок Forms, а не для редакционного контента. Content проверяет тип каждого переданного значения, enum/options, адреса файлов и ссылки на существующие поля. Значения неизвестных или удалённых полей сохраняются в черновике, помечаются редактором как orphaned и не могут быть опубликованы, пока их не очистят либо не восстановят схему. Это позволяет безопасно адаптировать данные при изменении Website `dataModel`.

A `many` entity allows zero or more system-wide records in each locale. For `singleton`, Content creates exactly one empty draft record per configured locale when that locale's workspace is first initialized for the current system model revision; initialization is idempotent. Clearing a singleton preserves its locale-specific record ID. A singleton is not published until explicit publication for that locale.

Выборы размещений задаются `page_snapshot_id + locale + placement_id + input_key` и содержат режим `all` либо упорядоченный список `record_ids`. Schema/placement подтверждаются соответствующими Website projection; все выбранные IDs должны принадлежать связанной сущности и запрошенной locale. Порядок записей и выбор размещения хранятся независимо для каждой locale и page snapshot. Удаление записи атомарно очищает только затронутые selections этой locale. Публичный runtime читает только опубликованные записи выбранной locale.

## Публикация и история

`PUT workspace` saves the draft for one locale and increments only that locale's revision. `POST publish` atomically fixes that locale's records and page selections as an immutable publication, advances its published revision and appends locale-scoped history. Incomplete editorial fields are allowed; invalid links, incompatible values, orphaned entity/field and invalid selections reject publication without partial changes.

История содержит опубликованные снимки записей и выборов, пригодные для точного воспроизведения. Восстановление истории создаёт новый черновик с текущим expected revision; оно не меняет текущую публичную публикацию. Для публикации восстановленного черновика требуется обычная отдельная команда `publish`. Удаление истории и автоматическое истечение срока хранения не поддерживаются.

Каждая мутация использует revision/idempotency, согласованные с общими ограничениями Core `engine_data`. Сохранение, удаление, reorder, публикация и восстановление либо фиксируются полностью, либо не изменяют рабочую область. Устаревшая revision возвращает conflict и свежие revision; автоматическое слияние конфликтующих черновиков запрещено.

## Автоматическая публикация страниц

Content publication commits one locale's records/selections and then starts a durable Coordinator fan-out for published page snapshots using that locale and all locale-aware Router route slots. For each affected `(page_snapshot, locale)` pair, the trusted Website projection, published locale revision, and page-scoped locale selections form the immutable build input. Website builds the corresponding artifact; orchestration independently CAS-switches the locale target of each associated Router route.

Content publication itself succeeds when its atomic Content commit succeeds; route activation is an asynchronous durable workflow with per-route/per-locale results. A build or CAS failure does not roll back Content publication or remove prior artifacts. Successful independent route targets may switch while failed targets remain unchanged; the workflow reports each outcome and supports safe retry. Duplicate work is idempotent by publication revision, page, route, and locale. A stale build is rejected when a newer publication for that locale supersedes it. Draft-only saves do not start rebuilds.

The same atomic Content commit must persist a Content-owned, versioned delivery
intent for the fan-out. It pins the locale, committed Content publication
revision, trusted system-model revision and a server-derived stable identity;
it does not contain credentials, Router operations or client-selected targets.
The intent and publication history are distinct: delivery state can advance
without mutating immutable history. Commit failure leaves neither a new
publication nor its intent. A successful commit is returned as success even
if a later dispatch is unavailable.

The system has one Router listener and one route registry. Each locale
publication has a single delivery acknowledgement. The workflow still
tracks activation separately for each affected route and locale.

Trusted composition recovers pending intents after restart and submits the
registered workflow through the typed process-local bus. Recovery is bounded
and uses explicitly configured operational limits; duplicate submit, lost
acknowledgement, concurrent recovery and process termination cannot lose an
intent or create duplicate logical fan-out. Completion of delivery means the
durable workflow start is confirmed, not that route activation succeeded.
Outcomes of the latter remain in the publication workflow. A newer publication
must prevent a delayed older build from replacing its route target. Existing
uncompleted publications and receipts are preserved and reconciled before any
format migration; they must not be silently deleted or assumed delivered.

## Создание дочернего page snapshot

Page snapshot fork выполняется durable workflow, запущенным внешним Presentation через Coordinator:

1. Website создаёт child в `provisioning`, внутри того же `page_id` tree, и выдаёт immutable `PagePlacementProjection` для source/target.
2. Coordinator passes bounded opaque projections to the registered Content operation. Content copies only page-scoped selections/order for each configured locale and checks the expected selection revision. System-wide locale records, drafts, publications, and history remain shared and are never copied.
3. После подтверждённого результата Website finalizes child как `ready`.

Схема/placements входа версионированы, связаны с Website revision/digest и проверяются Content. Coordinator хранит только opaque payloads и результаты; движки не вызывают друг друга. Повтор шага безопасен. Пока snapshot `provisioning` или fork завершился ошибкой, page build запрещён; прежние Router routes и опубликованные system records остаются неизменными. Оператор может повторить workflow либо удалить только незавершённый child и его page-scoped selection state.


## Публичная выдача, формы и файлы

Content не принимает публичные отправки и вложения форм: это область Forms и Loader. Content не публикует произвольные HTTP endpoint или внешние origins. Схема публичной формы и её обязательные поля проверяются Forms; редакционные записи Content остаются частично заполненными.

Ссылки на медиа в значениях содержат Core file ID. При авторизованном изменении Content проверяет существование и доступность файла через Core capability; Content никогда не возвращает байты и не делает файл публичным. Публичную выдачу создаёт только явно настроенный Router route; фактическое чтение файла повторно авторизуется через Core. Идентификатор в опубликованном содержимом сам по себе не раскрывает приватный файл. Производные медиа, если они потребуются, являются отдельными Core-файлами и генерируются отдельным workflow; оригинал не заменяется автоматически.

## Приёмочные сценарии

- Website `dataModel` и structural bindings отображаются read-only в Content, а mutation Content API не меняет Website config.
- Locale catalog CAS, canonical BCP 47 validation, default membership, locale removal guards, per-locale workspace/revision/publication/history, one record per locale in a translation group, and same-locale selection validation.
- Publishing one locale starts only that locale's page rebuilds; independent route/locale CAS results include partial failures and preserve each failed prior target.
- Crash after Content commit but before fan-out submission, lost start acknowledgement, restart and concurrent recovery preserve the intent and produce one logical workflow; Content response follows the commit result. Test failed Content commit, dispatcher outage, duplicate recovery, newer-publication stale protection, and existing in-flight data on disposable PostgreSQL.
- Создание, изменение, сортировка и удаление записей проверяют типы, orphaned данные, CAS-конфликты, idempotency и group levels `0/1/2`.
- Пустая singleton-сущность получает одну пустую запись, повторная инициализация не создаёт вторую, очистка сохраняет ID.
- Выборы `all` и `ids` проверяют наличие placement/input, связанную сущность, порядок ID и удаление записи с очисткой ссылок.
- Публикация атомарно фиксирует записи и выборы; история воспроизводит публикацию; restore создаёт новый черновик и не меняет публичную ревизию.
- Fork copies page-scoped selections/order per locale only; system-wide records, drafts, publications, and history remain shared. Test source mutation before copy, Content-step failure, worker retry, and finalization failure without exposing a `provisioning` snapshot.
- Root snapshot и forked snapshot становятся `ready` только после нужной инициализации Content.
- Проверить пределы JSON и engine-data, malformed/duplicate IDs, параллельные записи, отмену context и безопасное отсутствие Core/Website storage обхода.
