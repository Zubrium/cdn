# Публичные Go-контракты Website, Content и Forms

Статус: проект границ для приёмочных тестов; наличие описания не означает реализации. Приоритет общих правил и неразрешённые вопросы — [SPEC-coordinator-platform.md](SPEC-coordinator-platform.md). Каждый engine предоставляет типизированный facade из своего `presentation` package и не импортирует другие engines. Coordinator и Presentation передают им только явно выданные полномочия и зарегистрированные операции.

## Общие гарантии

- Входы содержат контекст отмены, сгенерированные системой идентификаторы, ожидаемые ревизии для изменяемых записей и ключ идемпотентности для повторяемых операций. Ни один endpoint не принимает DSN, S3 key, namespace или identity модуля от внешнего клиента.
- Ошибки позволяют различить недействительный ввод, отсутствие авторизации, отказ в доступе, недоступный или несуществующий ресурс, конфликт ревизии/идемпотентности и временную недоступность. Публичный слой не раскрывает наличие закрытых файлов или внутренние ошибки хранилища.
- После вызова авторизация проверяется Core на актуальном состоянии. Сбои между Core файлами и данными модуля обрабатываются durable workflow и повтором; общая распределённая транзакция не предполагается.
- Для каждого приёмочного случая фиксируются входные данные, предварительное состояние, действие, ожидаемый результат и постусловие. Табличные варианты считаются отдельно, но список вариантов заранее фиксируется и включает отказ, конкуренцию и восстановление; добавление множества одинаковых успешных вариантов не должно искусственно увеличивать показатель.

## Website

Facade принимает существующий `source_file_id`, idempotency key и необязательный ожидаемый digest. Клиент не задаёт целевой FileSet. Создание job проверяет полномочия пользователя и возвращает durable ID и состояние. При совпадении ключа и fingerprint возвращается та же job; другой fingerprint даёт конфликт. Worker через Core и настроенную capability проверяет ZIP, создаёт FileSet и файлы с группами исходника. Только после проверки результата и окончательного удаления архива версия становится `ready`; отказ очистки оставляет задачу в состоянии повтора.

Чтение каталога и jobs учитывает разрешения. Snapshot фиксирует идентичности и версии; изменение создаёт потомка. Задание сборки фиксирует snapshot, locale, locale catalog и точные build inputs, возвращая ID результата только после готовности. Website не вызывает Router и не переключает маршрут. Feature ZIP использует согласованный lifecycle `render/hydrate/update/dispose` и manifest-bound `x-media` profile; форма декларации и обработка описаны в [SPEC-feature-runtime.md](../engines/website/features/SPEC-feature-runtime.md).

## Content

Content publication and history are isolated per configured locale; translated records have separate record IDs linked by a server-issued `translation_id`. Locale catalog/default management belongs to Content Admin. Router alone resolves visitor locale, using its preference cookie, `Accept-Language` and the configured default; Content does not implement locale fallback. A missing translation is not replaced by another locale. Content validates values against trusted Website projections and publishes a locale-scoped immutable revision; conflict or invalid values do not mutate the publication.

Feature-owned browser requests are declared by each feature manifest and executed by feature code; external origins and typed request/response contracts follow [SPEC-feature-runtime.md](../engines/website/features/SPEC-feature-runtime.md). Request paths or parameters that vary by published record enter through the feature's typed `dataInputs` mapping. The system-level backend registry and placement request bindings are not part of the target contract. Routes support named parameters and Go RE2.

Website build accepts trusted immutable projections for one configured locale and includes the locale catalog revision, locale-specific publication revision and page selections in its build digest. The matching Router target is activated per `(route, locale)` with independent CAS. Page retirement withdraws each registered route through Router and retains page snapshots, Content history and Forms submissions. A media job resolves `x-media` from the exact manifest version pinned by the page snapshot; the editor waits for every output before changing the stored field value, while failure leaves the previous value unchanged. Media profiles never arrive from clients as preset IDs or processing arguments.

## Forms

Административный facade управляет конфигурацией и ревизией глобальной схемы, читает и удаляет результаты с актуальными Core group permissions. Публичный facade создаёт ограниченную по времени сессию формы, возвращает непрозрачное полномочие для этой сессии и подтверждает отправку только с файлами, связанными с ней через сервер. Presentation координирует Loader и Forms; движки друг друга не вызывают.

Схемы global: метаданные состоят только из `key + fields`, ключ глобально уникален, записи живут в scope `global/global` и защищены version/CAS без `snapshot_id`. Исторические snapshot-scoped payload version 1 не читаются и не мигрируются. Submission при приёмке фиксирует `form_key` в хранимом payload и индексе; список и экспорт фильтруются по обязательному `form_id` и необязательному `key`. Router-привязки pinned на версию схемы и меняются только явным действием оператора.

Сессия позволяет возобновить части после перезагрузки до истечения срока. Истечение, изменение схемы или снятие публикации запрещает отправку и инициирует точечную очистку временных и загруженных файлов. Повтор подтверждения с тем же idempotency key и fingerprint возвращает тот же результат; изменённый fingerprint конфликтует. Готовые ответы неизменяемы, хранятся до удаления и не становятся публичными от факта отправки. Авторизованное удаление окончательно удаляет ответ и его вложения с durable повтором после частичной ошибки.

Форма транспортного token и cookie/CSRF boundary принадлежат Router Presentation; Forms получает только typed session token и не задаёт HTTP cookie semantics. Группы `forms_schemas` и `forms_submissions` независимы и проверяются отдельно на каждом соответствующем Admin operation.

## Статус тестирования

Текущие Go interfaces реализованы частично: Forms предоставляет глобальные схемы, публичные сессии, upload и submit, но статусы, CSV-экспорт, expiry и cleanup остаются в работе; Content/Website production workflows и media worker integration не завершены. Router locale resolution и locale-specific target delivery остаются целевым контрактом до production acceptance. До появления production adapters невозможно честно выполнять большинство поведенческих acceptance-сценариев. Тесты не должны проверять поведение собственной заглушки или выдавать такую проверку за покрытие engine.
