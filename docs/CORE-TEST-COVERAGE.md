# Приёмочный набор Core v2

> **Исторический QA документ.** Пункты и результаты ниже относятся к single-schema Core до переноса в DDD и до Core-owned Coordinator. Они не подтверждают новую схему, новые пакеты или текущую реализацию. Актуальные критерии — [SPEC-core-architecture.md](../spec/SPEC-core-architecture.md), [SPEC-coordinator-platform.md](../spec/SPEC-coordinator-platform.md) и [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md). Цель текущего прохода — покрыть тестами не менее 60% согласованных наборов входных данных для каждого модуля; текущий статус фиксируется только в acceptance matrix после проверки.

Нормативные требования старого Core описаны в [SPEC-core-v2.md](../spec/SPEC-core-v2.md) и прежней версии [SPEC-module-data.md](../spec/SPEC-module-data.md). Нижеследующие команды, цифры и результаты сохраняются только как исторические свидетельства; их нельзя переносить на новую baseline без повторного прогона.

## Покрываемые сценарии

Module-data page size ограничивается одновременно настроенным числом записей и byte-budget: `max_page_bytes` учитывает потенциальную JSONB-экспансию payload/index, поля записи и число строк. Валидатор сохраняет произвольные конечные JSON numbers без перевода payload в `float64`, учитывает расширение экспонент и ограничивает namespace 255 байтами. Регрессионные проверки добавлены; актуальный verification pass должен подтвердить оценки размера, целостность страниц и монотонность cursor.

`presentation/fileapi` предоставляет HTTP CRUD наборов через Loader contract. Набор не выдаёт доступ к файлам; добавление файла требует уровня `2` в управляющей группе набора и хотя бы одной активной группе файла, а Core фильтрует файлы по текущим правам. Loader state machine, SHA-256 частей/файла, streaming/buffer policy, JSON persistence в `core.module_data`, scoped Core API и composition runtime реализованы; их acceptance-команды и последние изменения восстановления должны быть подтверждены актуальным QA прогоном.

- **Доступ:** уровни `0/1/2`, чтение через любую активную группу файла, неактивные группы, запрет повышения уровня без права управления администраторами и уровня `2`, отсутствие обхода группового доступа правом управления администраторами, запрет продуктовых действий в Core.
- **Идентичность и группы:** хеши ключей, bootstrap, новые учётные записи без групповых назначений, прямые назначения и запрет частичного изменения, сохранение назначений при изменениях YAML, закрытие отсутствующей группы и возврат прежней группы, защита последнего управляющего пользователя и ключа, конкурентный bootstrap/deactivate/revoke.
- **Файлы и наборы:** один пользовательский тип, отсутствие владельцев/visibility/версий, неизменяемые байты и новый ID для нового содержимого, повтор идемпотентного запроса, конфликт ключа, лимиты размера/метаданных, ревизии метаданных, несколько групп, запрет последней группы, чтение диапазона, окончательное удаление; наборы хранятся в Core, HTTP CRUD идёт через Loader→Core с обязательной управляющей группой, но membership не даёт прав на файлы. Проверяются уровни `1/2`, добавление файла только при уровне `2` в управляющей группе набора и хотя бы одной группе файла, фильтрация закрытых файлов до cursor/пагинации, отсутствие утечек счётчиков/имён и возврат только тех групп файла, где у субъекта есть право чтения.
- **Кеш и ошибки:** копирование байтов, TTL, ограничение количества и объёма, LRU, конкурентный доступ; актуальная проверка разрешения для каждой выдачи, отзыв права на второй реплике и отказ при недоступной БД.
- **S3 boundary:** небезопасная конфигурация, запрет не-UUID ключей до сетевого запроса, отказ при неполном public-access-block, публичном ACL/policy и невозможности проверить приватность, изолированный roundtrip и несоответствие длины потока.
- **Core staging:** привязка ID к инициатору, уровень `2` на каждой целевой группе, проверка лимита и SHA-256, потоковое чтение с актуальной проверкой доступа, отказ после отзыва назначения, безопасное удаление и отсутствие TTL-очистки готовых staging-объектов.
- **Контракт и схема Core:** сигнатуры публичного `Runtime` и module-data API, отсутствие старых API/полей, чистая начальная Core Atlas migration history; runtime-only DML grants и единственная таблица `core.module_data` для engine JSON.

## Приёмка module-data API

Новая физическая модель реализована в текущей миграции и Go API. Следующие проверки остаются обязательными до закрытия acceptance:

- Один JSONB-документ на ключ `(module_id, namespace, scope_type, scope_id, record_key)`; одинаковые ключи разных engines/scopes изолированы. Capability всегда привязана Core к зарегистрированному module ID, попытки чтения/записи чужого ID закрыты.
- Payload version/upcaster остаётся у engine; запись payload и `index_data` атомарна. Bootstrap defaults не перезаписывают существующие пользовательские значения.
- Typed selectors принимают только зарегистрированные поля/операторы и используют индекс; arbitrary SQL/JSONPath и full-table unbounded scan невозможны. Проверить планы запросов на disposable PostgreSQL.
- `expires_at` не даёт читать истёкшее значение до фонового удаления; TTL worker безопасен при нескольких экземплярах.
- CAS create/update/delete проверяет revision. Atomic batch CAS применяет все операции одной capability одним Core transaction: при любой конфликтной revision или ошибке не остаётся частичных изменений.
- Проверить предел версии payload для SQL integer, несовместимые с JSONB NUL/surrogate escapes, суммарный нормализованный размер batch, общий бюджет сериализованных selectors, ограничения размера JSON, пагинацию, индексацию, race между экземплярами, payload upcast, TTL очистку и Loader session recovery после перезапуска.

Прежние проверки ролей, владельцев, публикации файла и его версий удалены. `core/platform/runtime_integration_test.go` удалён как дублирующий тест, который совместно с новым сценарием менял одну БД и делал bootstrap зависимым от порядка тестов. Старый storage unit suite на `Grant` заменён тестом целевого интерфейса; сквозные сценарии доступа закреплены в Core v2 acceptance.

## Приёмка Loader Engine на общем хранилище

Эти контракты реализованы: загрузочные сессии, idempotency records, части, expiry и cleanup сохраняются JSON-записями namespace `loader` в `core.module_data`; отдельной Loader schema/БД нет. PostgreSQL через Core API является источником истины, а временные bytes проходят только через Core staging API. Проверки ниже остаются критериями приёмки, включая последние исправления recovery и ротации durable cleanup.

- Unit tests Loader: state machine, bind session к инициатору, идемпотентность, per-part и итоговый SHA-256, checksum mismatch, неполные/повторные части, commit/abort/expiry, race на одну часть, cache hit/miss/eviction и invalidation.
- Streaming/buffered policy: выбранный режим соответствует конфигурационному порогу и memory budget; oversized uploads и исчерпание бюджета остаются streaming, параллельная буферизация не превышает общий лимит.
- Integration tests: потеря процесса/переподключение/два экземпляра, resume из `core.module_data`, fail/retry при Core staging/finalize, очистка только точных staging refs, отсутствие видимого файла до commit.
- File API: fake Loader contract и маршруты CRUD наборов; проверка статичной управляющей группы набора, уровня `2` на группе файла, конфликта повторного имени набора, отказа на NUL в поисковом запросе, фильтрации недоступных файлов и fail-closed поведения.
- Storage boundary: один Core Atlas migration set и одна DB schema; engines не имеют DB credentials/SQL и не могут получить чужую module capability; никаких engine-specific DBs, schemas или migration histories.

## Изолированные integration-переменные

Каждая PostgreSQL integration-переменная указывает на отдельную, только что созданную пустую disposable БД с префиксом `zubrium_core_qa_*`; все модули в тесте работают через единую Core-схему. Тесты проверяют имя БД и отсутствие посторонних подключений, никогда не очищают или не удаляют неизвестные БД. База, уже использованная предыдущим запуском, повторно не подходит.

| Переменные | Назначение |
| --- | --- |
| `TEST_MODULE_DATA_DATABASE_URL` | пустая disposable БД для namespace isolation, selector/index, TTL, CAS/batch и Loader restart/resume через Core API |
| `TEST_CORE_DATABASE_URL` | identity/credential lifecycle |
| `TEST_CORE_GROUPS_DATABASE_URL` | прямые уровни, YAML reconciliation, last-manager guard |
| `TEST_CORE_SCHEMA_DATABASE_URL` | применение миграции и проверка её реального результата |
| `TEST_CORE_BOOTSTRAP_DATABASE_URL` | конкурентный первый запуск |
| `TEST_CORE_MANAGER_RACE_DATABASE_URL` | конкурентное отключение управляющих пользователей |
| `TEST_CORE_CREDENTIAL_RACE_DATABASE_URL` | конкурентный отзыв ключей управляющих пользователей |
| `TEST_CORE_PLATFORM_DATABASE_URL`, `TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL`, `TEST_CORE_PLATFORM_ADMIN_DATABASE_URL` | полный сценарий Core на двух и более экземплярах; runtime, мигратор и тестовый admin подключаются разными логинами к одному disposable PostgreSQL host/port/database. Admin нужен только чтобы адресно отключить backend listener в тесте reconnect |
| `TEST_CORE_RUNTIME_ROLE_DATABASE_URL`, `TEST_CORE_RUNTIME_ROLE_MIGRATION_DATABASE_URL` | отдельная свежая БД для проверки, что runtime с дополнительным `CREATEDB`-role membership отклоняется; эти логины не используются для остальных сценариев |
| `TEST_CORE_PLATFORM_S3_ENDPOINT`, `TEST_CORE_PLATFORM_S3_BUCKET`, `TEST_CORE_PLATFORM_S3_ACCESS_KEY`, `TEST_CORE_PLATFORM_S3_SECRET_KEY` | необязательный внешний S3 для Core acceptance; при отсутствии всех значений используется изолированный локальный S3-протокол mock |
| `TEST_CORE_S3_ENDPOINT`, `TEST_CORE_S3_BUCKET`, `TEST_CORE_S3_ACCESS_KEY`, `TEST_CORE_S3_SECRET_KEY` | необязательный внешний S3 roundtrip; при отсутствии всех значений используется изолированный локальный S3-протокол mock |

Внешний S3 bucket должен иметь префикс `zubrium-core-qa-`. Тесты удаляют только UUID-объекты и файлы, созданные конкретным сценарием. Удаление произвольных bucket объектов не выполняется. Передача тестовых ключей по HTTP разрешена только к loopback; для иных endpoint обязателен HTTPS. Локальный mock проверяет поток запросов AWS SDK и ответы на проверки приватности, но не подтверждает настройки реального S3 endpoint или его долговременность.

## Проверки и текущее состояние

- `go test -race -count=1 ./...` из `core/` — PASS на свежем наборе PostgreSQL 16; прошли unit-, migration-, concurrency- и Core acceptance-тесты.
- `make coverage` — PASS; общий statement coverage Core и нового scaffold — **72,2%** (порог 70% выполнен). Coverage workflow выполняет полный набор `go test -tags=full -count=1` на отдельных пустых disposable БД.
- `make test` — PASS на свежем наборе изолированных БД. После последнего небольшого рефакторинга дополнительно прошли `go test -count=1 ./...` для `presentation/fileapi` и `engines/loader`.
- `make vet`, `make build`, `make build-full` — PASS. `staticcheck ./...` — PASS отдельно во всех 10 Go modules. `git diff --check` — PASS.
- Regression `TestCoreGoCodeDoesNotExecuteSchemaDDL` прошёл в составе integration/coverage run. Производственные `.go`-файлы не содержат schema DDL; схемы создают только версионированные Atlas SQL migration files.
- `ListAccessGroups` должен отказать субъекту без права управления администраторами, чтобы не раскрывать перечень внутренних групп; управляющий администратор видит настроенные группы и собственные уровни. Этот критерий добавлен в текущую приёмку и требует повторного прогона.
- БД: PostgreSQL 16 в изолированном task-owned контейнере; Atlas CLI и библиотека миграций — `v1.3.0`. S3 acceptance использует loopback-only protocol mock. Реальный внешний S3, production PostgreSQL, внешний gateway и Vitasil не использовались.
- Все PostgreSQL integration-переменные указывают на отдельные пустые БД с префиксом `zubrium_core_qa_`; runtime, migration и listener-admin connections в platform acceptance используют разные login. Перечень созданных disposable БД и удаление task-owned QA окружения отражены в [QA_REPORT.md](QA_REPORT.md).
- `govulncheck` на машине не установлен; специализированная независимая security-приёмка не проводилась. Прошли source-level security checks, permission/fail-closed integration cases и Staticcheck, но это не является формальным полным security audit.

## Оставшиеся acceptance проверки

Core acceptance прошёл контролируемые S3 upload/delete failures, PostgreSQL finalization failure, повторную очистку, recovery-конкуренцию с активными file/staging uploads и восстановление cache-invalidation listener после потери соединения. Реальный внешний S3/provider и production DB не проверялись. Публичный маршрут относится к Router acceptance: Core публичную выдачу запрещает. Loader и File API реализованы; Router, Admin API/UI и перенос Website/Content/Forms остаются открытыми в TODO. Последние изменения Loader после этого QA прогона пока не проверены.
