# Приёмочный набор Core v2

Нормативные требования — в [SPEC-core-v2.md](SPEC-core-v2.md). Контрактные проверки находятся в `core/access`, `core/internal/storage`, `core/internal/postgres` и test-local `coreV2RuntimeContract` в `core/platform`. Ниже зафиксирован результат локальной приёмки 16 ноября 2026 года. Он подтверждает проверенные Core-критерии, но не означает готовность всей модульной платформы, проверку реального S3-провайдера или завершение независимого security audit.

## Покрываемые сценарии

`presentation/fileapi` проверяет HTTP-границу через fake Loader contract: bearer-аутентификацию, отказ до вызова Loader, streaming upload-session routes, строгий JSON и лимиты, закрытие устаревших полей `public`/`role`, ETag/If-Match, диапазонную выдачу с `no-store` и отсутствие generic HTTP-маршрутов для динамических наборов. Наборы создаются и меняются движками через внутренний Core-контракт; их membership не выдаёт доступ, а поиск фильтрует файлы по актуальным группам. Реальная загрузочная state machine, SHA-256 частей и файла, streaming/buffer policy, долговременная схема Loader, интеграция с Core staging и composition runtime ещё требуют отдельных тестов и реализации. Core staging API и его roundtrip/checksum сценарий добавлены в Core integration acceptance; это пока не является пройденным доказательством.

- **Доступ:** уровни `0/1/2`, чтение через любую активную группу файла, неактивные группы, запрет повышения уровня без права управления администраторами и уровня `2`, отсутствие обхода группового доступа правом управления администраторами, запрет продуктовых действий в Core.
- **Идентичность и группы:** хеши ключей, bootstrap, новые учётные записи без групповых назначений, прямые назначения и запрет частичного изменения, сохранение назначений при изменениях YAML, закрытие отсутствующей группы и возврат прежней группы, защита последнего управляющего пользователя и ключа, конкурентный bootstrap/deactivate/revoke.
- **Файлы и наборы:** один пользовательский тип, отсутствие владельцев/visibility/версий, неизменяемые байты и новый ID для нового содержимого, повтор идемпотентного запроса, конфликт ключа, лимиты размера/метаданных, ревизии метаданных, несколько групп, запрет последней группы, чтение диапазона, окончательное удаление; наборы хранятся в Core, создаются/меняются движками через внутренний контракт и не дают прав на файлы. Проверяются ограничение изменений учётной записью-создателем, фильтрация недоступных файлов до cursor/пагинации и отсутствие generic HTTP CRUD наборов.
- **Кеш и ошибки:** копирование байтов, TTL, ограничение количества и объёма, LRU, конкурентный доступ; актуальная проверка разрешения для каждой выдачи, отзыв права на второй реплике и отказ при недоступной БД.
- **S3 boundary:** небезопасная конфигурация, запрет не-UUID ключей до сетевого запроса, отказ при неполном public-access-block, публичном ACL/policy и невозможности проверить приватность, изолированный roundtrip и несоответствие длины потока.
- **Core staging:** привязка ID к инициатору, уровень `2` на каждой целевой группе, проверка лимита и SHA-256, потоковое чтение с актуальной проверкой доступа, отказ после отзыва назначения, безопасное удаление и отсутствие TTL-очистки готовых staging-объектов.
- **Контракт и схема Core:** сигнатуры публичного `Runtime` и storage-service, отсутствие старых API/полей, чистая начальная Atlas-схема `core` с прямыми группами и наборами; интеграционная проверка обеих Atlas histories, grants runtime-пользователя и запрет межсхемного доступа.

Прежние проверки ролей, владельцев, публикации файла и его версий удалены. `core/platform/runtime_integration_test.go` удалён как дублирующий тест, который совместно с новым сценарием менял одну БД и делал bootstrap зависимым от порядка тестов. Старый storage unit suite на `Grant` заменён тестом целевого интерфейса; сквозные сценарии доступа закреплены в Core v2 acceptance.

## Приёмка Loader Engine и изоляции схем

Эти критерии ещё не реализованы. Загрузочные сессии, idempotency records, части, expiry и их локальный cache принадлежат Loader и только его схеме `loader`; PostgreSQL является источником истины, поэтому несколько экземпляров и рестарт продолжают сессию без чтения Core tables. Временные bytes проходят только через Core staging API; в Core schema нет таблиц upload-сессий.

- Unit tests Loader: state machine, bind session к инициатору, идемпотентность, per-part и итоговый SHA-256, checksum mismatch, неполные/повторные части, commit/abort/expiry, race на одну часть, cache hit/miss/eviction и invalidation.
- Streaming/buffered policy: выбранный режим соответствует конфигурационному порогу и memory budget; oversized uploads и исчерпание бюджета остаются streaming, параллельная буферизация не превышает общий лимит.
- Integration tests: потеря процесса/переподключение/два экземпляра, resume через Loader schema, fail/retry при Core staging/finalize, очистка только точных staging refs, отсутствие видимого файла до commit.
- File API: fake Loader contract; generic API не публикует маршруты для наборов, прав доступа или администрирования; динамические наборы остаются в engine workflows и Core in-process API.
- Schema boundary: каждая Atlas migration меняет только свою схему; отдельные checksum и migration history; runtime DB roles не имеют прав на чужие схемы; cross-schema query/FK запрещены и отрицательно проверяются.

## Изолированные integration-переменные

Каждая PostgreSQL integration-переменная должна указывать на отдельную, только что созданную пустую disposable БД: Core — с префиксом `zubrium_core_qa_*`, Loader — `zubrium_loader_qa_*`. В каждом окружении тесты проверяют имя, отсутствие посторонних подключений и отсутствие целевой схемы или Atlas history; они никогда не очищают или не удаляют БД. База, уже использованная предыдущим запуском, повторно не подходит.

| Переменные | Назначение |
| --- | --- |
| `TEST_LOADER_DATABASE_URL` | пустая disposable БД/схема для Loader migrations, session concurrency и restart/resume |
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
- БД: PostgreSQL 16 в изолированном task-owned контейнере; Atlas CLI и библиотека миграций — `v1.3.0`. S3 acceptance использует loopback-only protocol mock. Реальный внешний S3, production PostgreSQL, внешний gateway и Vitasil не использовались.
- Все PostgreSQL integration-переменные указывают на отдельные пустые БД с префиксом `zubrium_core_qa_`; runtime, migration и listener-admin connections в platform acceptance используют разные login. Перечень созданных disposable БД и удаление task-owned QA окружения отражены в [QA_REPORT.md](QA_REPORT.md).
- `govulncheck` на машине не установлен; специализированная независимая security-приёмка не проводилась. Прошли source-level security checks, permission/fail-closed integration cases и Staticcheck, но это не является формальным полным security audit.

## Оставшиеся acceptance проверки

Core acceptance прошёл контролируемые S3 upload/delete failures, PostgreSQL finalization failure, повторную очистку, recovery-конкуренцию с активными file/staging uploads и восстановление cache-invalidation listener после потери соединения. Реальный внешний S3/provider и production DB не проверялись. Публичный маршрут относится к Router acceptance: Core публичную выдачу запрещает. Полная интеграция Loader Engine, Router, Admin API/UI и модулей Website/Content/Forms ещё не завершена и остаётся открытой в TODO.
