# Content Engine: целевая спецификация

Статус: согласованная граница Content для новой архитектуры. HTTP-контракт и реализация могут уточняться только вместе с этой спецификацией и приёмочными сценариями.

## Владение данными и границы

Website owns the site-wide `dataModel` and each page's immutable structure separately. Site model changes are shared across all pages and do not create or modify a page snapshot. Page snapshot owns one page's placements and structural bindings; it contains no route. Content does not create or edit either Website-owned contract.

Content owns site-scoped records, drafts, published records and publication history. Record identity and values are shared by all pages of the site. Placement selections/order are scoped to a page snapshot and reference those shared record IDs. A page fork may copy page-scoped selections, but never forks site-wide records or publication history.

Content Engine does not call Website Engine and receives no Website facade/resolver. Trusted outer Presentation/composition obtains separate immutable projections through Website capability: a site-scoped `SiteContentSchema` (site ID, model revision/digest and validated `dataModel`) and a page-scoped `PagePlacementProjection` (page snapshot ID/revision/digest and placements/bindings). These typed DTOs are never decoded from client-controlled request bodies. Content validates digest, site/page scope, and revision on each operation. Durable copy workflows carry bounded versioned projections as opaque Coordinator task data; Content does not call Website to validate state. Core is the only persistence gateway for `engine_data.records`; byte access remains through Core.

In `engine_data.records`, site-wide workspace/current publication/history are stored in a site scope. Page-scoped selections use page snapshot scope and their own revision/CAS. Namespaces and payload budgets remain configured through Core. This split prevents a child page snapshot from duplicating or diverging shared records while preserving independent page editing.

В v1 одна рабочая область является одним bounded JSON aggregate: объём draft и опубликованного содержимого не превышает активный `engine_data.MaxPayloadBytes` Core (верхняя граница Core — 16 MiB). Сохранение сверх лимита отклоняется полностью с `413`; часть записей не применяется. Список истории публикуется курсорно, а записи одного workspace загружаются одним запросом, чтобы сохранять draft и selections атомарно. Если фактический контент регулярно достигает лимита, следующая версия должна перейти на staging revisions/chunks с атомарной сменой указателя, не увеличивая скрыто лимит Core.

Все люди и сервисные интеграции аутентифицируются как один тип пользователя. Чтение Content требует уровень `1` в настроенной группе `content`; изменение черновика, порядка записей, выборов размещений и удаление записи требуют уровень `2`. Публикация, восстановление и просмотр истории используют те же индивидуальные групповые права: read требует `1`, запись — `2`. Сервер повторно проверяет права на каждый запрос.

## Рабочая область контента

Admin selects a site and page snapshot separately. External Content Presentation supplies both trusted projections to Content: `SiteContentSchema` defines the shared model; `PagePlacementProjection` defines the selected page's placement bindings. Workspace returns site-wide records and publication revisions plus page-scoped selections/order. Neither projection is client-authored, and Content does not persist Website structure as authoritative state.

Черновик сохраняется целиком с `expected_revision` через CAS. Запись запроса может ссылаться на существующий серверный ID или иметь request-local `client_key`; Core ID создаёт только сервер. При сохранении Content возвращает соответствие `client_key` созданным ID, чтобы выборы размещений ссылались на устойчивые записи. Повтор `Idempotency-Key` с тем же payload до следующей успешной мутации возвращает прежний ответ; повтор того же ключа с другим payload конфликтует. Receipt последней принятой мутации хранится вместе с рабочей областью и атомарно заменяется следующей мутацией; более старый запрос после продвижения revision отклоняется через CAS. Удаление записи фиксируется отсутствием в полном черновике, очищает выборы этой записи атомарно и не затрагивает другие снапшоты.

Черновые значения необязательны, включая вложенные поля; обязательность проверяется для публичных отправок Forms, а не для редакционного контента. Content проверяет тип каждого переданного значения, enum/options, адреса файлов и ссылки на существующие поля. Значения неизвестных или удалённых полей сохраняются в черновике, помечаются редактором как orphaned и не могут быть опубликованы, пока их не очистят либо не восстановят схему. Это позволяет безопасно адаптировать данные при изменении Website `dataModel`.

Сущность с cardinality `many` допускает ноль или больше site-wide записей. Для `singleton` Content создаёт ровно одну пустую черновую запись при первой инициализации site-wide model revision; повтор идемпотентен. Её очистка сохраняет серверный ID. Опубликованная singleton-запись не появляется автоматически до явной публикации.

Выборы размещений задаются парой `page_snapshot_id` + `placement_id` + `input_key` и содержат режим `all` либо упорядоченный список site-wide `record_ids`. Schema/placement подтверждаются соответствующими Website projection; все выбранные IDs должны относиться к связанной сущности и существовать в общем site workspace. Порядок записей хранится site-wide и участвует в публикации; порядок/selection размещения хранится на странице. Изменение или удаление record атомарно очищает затронутые selections всех страниц. Публичный runtime читает только опубликованные records.

## Публикация и история

`PUT workspace` сохраняет черновик и увеличивает его revision. `POST publish` фиксирует одним атомарным действием текущие записи и выборы как неизменяемую публикацию, обновляет указатель опубликованной revision и создаёт элемент истории. Неполные поля в контенте разрешены; повреждённые ссылки, значения неподходящего типа, orphaned entity/field и некорректные выборы отклоняют публикацию без частичных изменений.

История содержит опубликованные снимки записей и выборов, пригодные для точного воспроизведения. Восстановление истории создаёт новый черновик с текущим expected revision; оно не меняет текущую публичную публикацию. Для публикации восстановленного черновика требуется обычная отдельная команда `publish`. Удаление истории и автоматическое истечение срока хранения не поддерживаются.

Каждая мутация использует revision/idempotency, согласованные с общими ограничениями Core `engine_data`. Сохранение, удаление, reorder, публикация и восстановление либо фиксируются полностью, либо не изменяют рабочую область. Устаревшая revision возвращает conflict и свежие revision; автоматическое слияние конфликтующих черновиков запрещено.

## Автоматическая публикация страниц

Публикация общей модели или records фиксирует новую site-wide revision. После commit Content сообщает typed наружной Presentation operation идентификатор сайта и revision; она запускает durable Coordinator fan-out по затронутым опубликованным page snapshots и Router route slots. Для каждого page snapshot Website projection, новая модель/публикация и page selections образуют воспроизводимый build input. Новые page artifacts активируются только после успешной проверки через CAS каждого Router route.

Ошибки не откатывают site-wide Content publication и не удаляют прежние артефакты: удачные routes могут переключиться, а неудачные остаются на прежнем target и входят в возвращаемый частичный результат с безопасной причиной/возможностью повтора. Fan-out идемпотентен по publication revision и page/route; устаревший build не активируется, если пока завершался более новый publish. Draft-only saves пересборку не запускают.

## Создание дочернего page snapshot

Page snapshot fork выполняется durable workflow, запущенным внешним Presentation через Coordinator:

1. Website создаёт child в `provisioning`, внутри того же `(site_id, page_id)` tree, и выдаёт immutable `PagePlacementProjection` для source/target.
2. Coordinator передаёт bounded opaque projection в зарегистрированную Content operation, которая при необходимости копирует только page-scoped selections и проверяет ожидаемую revision selection. Общие site records, модель и publication history не копируются.
3. После подтверждённого результата Website finalizes child как `ready`.

Схема/placements входа версионированы, связаны с Website revision/digest и проверяются Content. Coordinator хранит только opaque payloads и результаты; движки не вызывают друг друга. Повтор шага безопасен. Пока snapshot `provisioning` или fork завершился ошибкой, page build запрещён; прежние Router routes и опубликованные site records остаются неизменными. Оператор может повторить workflow либо удалить только незавершённый child и его page-scoped selection state.


## Публичная выдача, формы и файлы

Content не принимает публичные отправки и вложения форм: это область Forms и Loader. Content не публикует произвольные HTTP endpoint или внешние origins. Схема публичной формы и её обязательные поля проверяются Forms; редакционные записи Content остаются частично заполненными.

Ссылки на медиа в значениях содержат Core file ID. При авторизованном изменении Content проверяет существование и доступность файла через Core capability; Content никогда не возвращает байты и не делает файл публичным. Публичную выдачу создаёт только явно настроенный Router route; фактическое чтение файла повторно авторизуется через Core. Идентификатор в опубликованном содержимом сам по себе не раскрывает приватный файл. Производные медиа, если они потребуются, являются отдельными Core-файлами и генерируются отдельным workflow; оригинал не заменяется автоматически.

## Приёмочные сценарии

- Website `dataModel` и structural bindings отображаются read-only в Content, а mutation Content API не меняет Website config.
- Создание, изменение, сортировка и удаление записей проверяют типы, orphaned данные, CAS-конфликты, idempotency и group levels `0/1/2`.
- Пустая singleton-сущность получает одну пустую запись, повторная инициализация не создаёт вторую, очистка сохраняет ID.
- Выборы `all` и `ids` проверяют наличие placement/input, связанную сущность, порядок ID и удаление записи с очисткой ссылок.
- Публикация атомарно фиксирует записи и выборы; история воспроизводит публикацию; restore создаёт новый черновик и не меняет публичную ревизию.
- Fork копирует черновик, публикацию, историю и выборы один раз; изменение родителя перед копированием, падение Content-шага, повтор worker и падение финализации не раскрывают и не назначают `provisioning` snapshot.
- Root snapshot и forked snapshot становятся `ready` только после нужной инициализации Content.
- Проверить пределы JSON и engine-data, malformed/duplicate IDs, параллельные записи, отмену context и безопасное отсутствие Core/Website storage обхода.
