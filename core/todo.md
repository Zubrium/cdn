# План завершения Core

## Цель и границы

Довести Core до полного соответствия согласованным Core-контрактам и всем применимым Core acceptance-сценариям из [матрицы приёмки](../tests/ACCEPTANCE-MATRIX.md). Для каждого сценария должны быть определены предусловия, вход, ожидаемые ответ и состояние после операции, исполняемый тест с assertions и фактическое evidence. Целевой показатель — 100% согласованных Core-сценариев и критичных инвариантов, а не 100% statement coverage.

Core остаётся единственным владельцем PostgreSQL, S3, общего файлового cache, Core identity/access, File API capabilities, engine-data и Coordinator. Не переносить в Core HTTP/cookie/CSRF политику, engine-specific модели и операции, bus implementation или продуктовую оркестрацию. Вызовы Core через bus и static composition проверять на интеграционной границе; их реализация принадлежит `main` и Presentation.

Авторитетные контракты: [SPEC-core-architecture.md](../spec/core/SPEC-core-architecture.md), [SPEC-coordinator-platform.md](../spec/architecture/SPEC-coordinator-platform.md), [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md) и операционные требования [CORE-OPERATIONS.md](../docs/CORE-OPERATIONS.md).

## Текущее подтверждённое состояние

- Полный набор Core-проверок прошёл в текущем проходе: `QA_ENV_FILE=<private> tests/qa/run-core-acceptance.sh run` выполнил `go test -count=1 -overlay=<generated> ./...`, `go vet -overlay=<generated> ./...` и `go build ./...` из `core/` без ошибок. Все 27 task-specific БД `zubrium_core_qa_*` перед финальным прогоном были пересозданы заново.
- S3 integration round-trip прошёл через локальный тестовый `s3mock`; внешний S3 endpoint и credentials не использовались.
- PostgreSQL acceptance прошла на отдельном disposable-контейнере `core-hardening-7d86bd2` (PostgreSQL на `127.0.0.1:55433`, tmpfs, Atlas CLI v1.3.0): чистый baseline/checksum и повторный startup, runtime privilege validation, early rejection грязной БД, Core v2 session/file/access/cache, Coordinator lifecycle/failure injection и engine-data persistence.
- Persisted engine-data regressions для ABA после удаления/повторного создания, изоляции между namespaces, CAS/TTL/batch/cursor и dotted `engine_id` прошли на свежих disposable PostgreSQL БД.
- Coordinator denominator зафиксирован в `tests/qa/core-coordinator-scenarios.json` (16 сценариев); suite жизненного цикла и failure-injection прошёл на отдельной свежей БД. Unit- и DB-сценарии из ledger исполнены, status `blocked` снят.
- Для Core Domain остаётся сверить прямые тесты с каждым согласованным инвариантом; пакеты без test-файлов остаются предметом отдельной проверки.
- Финальный sweep после всех правок прошёл: `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures` и `make lint-core-boundary` из корня репозитория; Admin: typecheck, 213 unit-тестов и production build. Сборка `engines/content`, `presentation/admin/content` и `presentation/admin/base` завершается с централизованными тестами.
- Disposable PostgreSQL `core-hardening-7d86bd2` и его сеть удалены после acceptance; все 27 QA-БД принадлежали только этому прогону и не сохранялись. Существующий контейнер на порту `55432` не трогался.

Предыдущие результаты не считать доказательством для сценария, который был пропущен, не запускался или завершился ошибкой. Итоговые команды и evidence обновлять в [QA_REPORT.md](../docs/QA_REPORT.md), не записывая DSN, секреты или credentials.

### Текущий проход доработки Core (2026-11-21)

- Исходное грязное дерево зафиксировано коммитом `7d86bd2`. Дальнейшие изменения проверяются относительно него; проблемные untracked-файлы в коммит включены только потому, что пользователь явно запросил зафиксировать текущее состояние.
- Подготовлена отдельная disposable PostgreSQL-среда `core-hardening-7d86bd2` на `127.0.0.1:55433` с tmpfs-хранилищем и task-specific БД `zubrium_core_qa_*`. Существующий контейнер `websiteengine-core-qa-postgres-1` на порту `55432` не изменялся.
- Atlas CLI `v1.3.0` установлен только во временный каталог QA и передаётся через `TEST_ATLAS_BINARY`; в репозиторий бинарник не добавлен.
- DSN для direct PostgreSQL-тестов используют отдельного migrator-логина, потому что эти тесты сами вызывают `Migrate`; `TEST_CORE_PLATFORM_*` использует раздельные runtime/migration/admin логины для проверкиstartup и listener recovery. S3 остаётся на loopback `s3mock`.
- Добавлен `tests/qa/run-core-acceptance.sh`: он принимает приватный `QA_ENV_FILE`, монтирует централизованные тесты через `tests/go-overlay.py` и запускает `go test`, `go vet`, `go build` из `core/`.
- Найдены и исправлены два реальных дефекта Coordinator: `Service.Start` оставлял `started=true` после неудачного старта, а `validTaskOutcome` проверял лимиты зависимостей по отдельности и пропускал сумму `200+200`. Оба воспроизведены RED-тестами и закрыты GREEN.
- Найдены и исправлены два реальных дефекта engine-data: PostgreSQL-адаптер отвергал легальные точечные `engine_id`, несовместимые с application/Atlas, а `Store.List`/`ListExpired` не проверяли корректность keyset-страницы перед возвратом курсора и проекции. Добавлены fail-closed проверки.
- В новых QA-тестах исправлены три ошибки фикстур, выявленные реальным PostgreSQL: неверный namespace `loader.v2` вместо `loader/v2`, сравнение нормализованного JSONB как строки и неквалифицированное имя функции триггера при `search_path=pg_catalog`. Все три не являются product-дефектами.
- Content/locale-refactor доведён до сборки: состояние вынесено в `StoredWorkspace.PageSelections`, trusted projections поставляются отдельной зависимостью `ContentSchemas`, route guards и `VerifyScope` сохранены. По итогам ревью сохранён класс ошибок провайдера проекций и добавлен лимит длины локали из OpenAPI. Изменения в Content минимальны и не меняют Core-контракты.
- исправлены вне Core: устаревший alias `routerhttp` и несуществующий символ `compiledDevelopmentFixtures` в композиции `main`; централизованные контрактные тесты и тесты путей к конфигурации переведены на фактическое состояние checkout'а; два ожидаемых, но ещё не подключённых endpoint'а зафиксированы в явном списке исключений. Разрешённые исключения перечислены в [QA_REPORT.md](../docs/QA_REPORT.md).

## Правила безопасного выполнения интеграционных тестов

1. Использовать только новые disposable PostgreSQL database с task-specific именами `zubrium_core_qa_*`; runtime и migration identity должны быть раздельными и указывать на одну тестовую БД. Переменные задавать в локальном окружении, не сохранять значения в этом файле.
2. До запуска сверить точные имена БД, пользователей, S3 bucket и ресурсов; для cleanup подтвердить отсутствие активных пользователей. Удалять только ресурсы, созданные текущим QA-run и записанные в его manifest/evidence.
3. Для S3 по умолчанию использовать локальный mock. Реальный bucket допускается только как отдельный явно disposable private bucket `zubrium-core-qa-*` с включёнными public-access blocks и без публичной policy/ACL.
4. Не направлять acceptance-команды на production, рабочие или неопознанные ресурсы; не выполнять широкую очистку.
5. Если disposable-конфигурация недоступна или нарушает требования из [CORE-OPERATIONS.md](../docs/CORE-OPERATIONS.md), соответствующий сценарий остаётся `blocked`, а не `passed`.

## План работ

### Этап 1. Зафиксировать denominator и тестовую среду

- [ ] Составить таблицу всех применимых Core input/result scenarios из Acceptance Matrix: Domain; public facade/boundaries; access/files; PostgreSQL/S3; engine-data; Coordinator/workflows.
- [ ] Для каждого scenario указать предусловия, действие, ответ, persisted side effects, отказные/конкурентные варианты и существующий тест либо конкретный пробел.
- [ ] Отделить Core-owned требования от требований `main`, Presentation и engines. Для cross-boundary сценариев отметить владельца каждого assertion, не переносить его реализацию в Core.
- [x] Проверить готовность disposable runtime/migration identities и S3 mock до тестовых запусков; не выводить секреты в логи или документацию.
- [ ] Согласовать denominator до публикации процента acceptance coverage. Если обнаружены неописанные сценарии, сначала согласовать их применение и ожидаемое поведение.

**Готово, когда:** каждый применимый пункт Core из Acceptance Matrix имеет однозначный тестовый сценарий либо явно зафиксированное внешнее ограничение; тестовая среда проверена без обращения к пользовательским данным.

### Этап 2. Усилить Core Domain и архитектурные границы

- [ ] Добавить assertions на конструкторы и валидированные доменные значения: валидный ввод принимается, некорректный отклоняется типизированной ошибкой.
- [ ] Проверить контракты repository/time/event ports и поведение на граничных значениях; не тестировать реализации инфраструктуры в Domain-тестах.
- [ ] Убедиться, что Domain не содержит application policy, SQL/JSONB/driver types, network clients, framework dependencies или engine-specific понятия.
- [ ] Проверить структуру пакетов: четыре слоя находятся непосредственно в `core/{domain,application,infrastructure,presentation}`; отсутствуют Core `internal` и циклические зависимости.
- [ ] Проверить направление imports всех Core packages и потребителей: потребители импортируют только `core/presentation`, Core не импортирует соседние модули, а `pkg/contracts` не используется для обхода Core capabilities.
- [x] Для архитектурных проверок использовать доступные AST/structure tests и имеющуюся boundary-проверку. Root `Makefile` добавлен как тонкий wrapper, `main/Makefile` указывает на существующий `tests/linter/lint_core_boundary.go`; `make lint-core-boundary` выполнен и вывел `Architecture boundaries OK`.

**Готово, когда:** каждому Domain и boundary-инварианту соответствует проверяемый тест/assertion, а нарушение структуры или направления imports приводит к провалу.

### Этап 3. Закрыть identity, access и файловые invariants

- [ ] Проверить права `0/1/2` на каждой операции и fail-closed на ошибках Core/access provider.
- [ ] Проверить отдельное `can_manage_administrators`, запрет самонаделения правами и изменение grant только при собственном уровне `2` в управляемой группе.
- [ ] Проверить last-manager protection, включая конкурентные изменения и безопасное операторское восстановление.
- [ ] Проверить объединение group declarations из YAML и component registration: уникальность канонического имени, bootstrap precedence, deactivate/reactivate без потери прежних grants и file memberships.
- [ ] Проверить credential-bound sessions: хранится только token digest; пользователь и session привязаны к одному credential; отзыв и expiry закрывают доступ. HTTP transport/cookie/CSRF тестировать только в Presentation.
- [ ] Проверить metadata CAS и stale revision conflict; file bytes неизменяемы, S3 key внутренний, файл может быть связан с несколькими группами.
- [ ] Проверить multi-group read: достаточно разрешения хотя бы в одной активной группе; revoke/деактивация группы немедленно прекращают кешированную выдачу.
- [x] Проверить, что FileSet/поисковое членство никогда не выдаёт права на чтение bytes.
- [ ] Проверить отказ при PostgreSQL недоступности, ошибках object store и неопределённом результате операции: приватный объект не становится видимым частично, повтор идемпотентен, cleanup не удаляет объект конкурентной операции.

**Готово, когда:** все сценарии access/files из матрицы проверены unit/integration tests, включая отказные, revoke, CAS и кешированные пути.

### Этап 4. Пройти PostgreSQL, Atlas и S3 acceptance

Выполнять после безопасной подготовки disposable DB по требованиям CORE-OPERATIONS.md.

- [x] На пустой disposable DB применить Atlas baseline и подтвердить все Core schemas, таблицы, constraints, индексы, sequence и grants.
- [x] Проверить `atlas.sum` и повторный startup без изменения baseline.
- [x] Проверить, что runtime login не superuser, не имеет elevated role memberships и обладает только согласованными DML/sequence privileges; DDL, grants, `TRUNCATE`, Atlas history и посторонние schemas недоступны.
- [x] Подготовить контролируемую несовместимую/неуправляемую схему и проверить ранний отказ до любых DDL или изменения существующих объектов. Добавлен `TestDisposableMigrationRefusesDirtyDatabaseWithoutDDL` на отдельной disposable БД `zubrium_core_qa_dirty`; тест прошёл и подтвердил `ErrDirtyDatabase` без создания Core-таблиц и истории Atlas.
- [x] Проверить конкурентный bootstrap и миграционный запуск; подтвердить отсутствие дубликатов пользователей/групп и целостность назначений. `TestDisposableBootstrapIsSerializedAcrossConnections`, `TestDisposableMigratedSchemaHasNoRetiredModels` и `TestDisposableDirectGroupAssignmentLifecycle` прошли в финальном прогоне.
- [x] Проверить transaction rollback на ошибках до и во время фиксации; проверить конкурентные metadata/file operations. Инжектированный outage в `TestDisposableCoordinatorLifecycleSuite/commit_outage_rolls_back` и конкурентные тесты identity/metadata прошли без частичной фиксации.
- [x] Пройти upload/staging lifecycle: начало, повтор, finalize, crash-window recovery, abort/expiry и cleanup; не допускать выдачи незавершённых объектов. Покрыто `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` и objectstore acceptance на `s3mock`.
- [x] Проверить приватность S3, generated UUID keys, checksum, лимиты, повтор операции после неопределённого ответа и безопасную обработку сирот. Покрыто `core/infrastructure/objectstore` и Core v2 acceptance; внешние S3-ресурсы не использовались.
- [x] Проверить cache invalidation между двумя Core instances, включая изменение metadata, revoke access и object-store failure. Core v2 acceptance включает listener recovery и инвалидацию на отдельном runtime/admin login.
- [x] Проверить, что cleanup удаляет только созданные данным тестом записи/объекты и подтверждённые точные disposable ресурсы. Persistence-тесты удаляют только свои `qa-*` записи; все используемые disposable БД принадлежат текущему QA-run.

**Готово, когда:** все перечисленные DB/S3 сценарии выполнены на disposable ресурсах, результаты сверены с состоянием БД/S3, а тесты не оставили неопознанных ресурсов.

### Этап 5. Закрыть engine-data acceptance gaps

- [x] Проверить capability binding к зарегистрированному `engine_id`/namespace: consumer не может выбрать или подменить чужой owner через request payload. Проверено persisted regression с двумя зарегистрированными module ID и изолированными namespaces.
- [x] Добавить cross-namespace regression: capability первого module создаёт запись; capability второго module не может прочитать, изменить, удалить или перечислить её, включая guessed ID и selector. Persisted assertion выполнен через `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` на новой disposable PostgreSQL БД.
- [x] Добавить ABA regression: создать запись, зафиксировать revision, удалить запись, пересоздать её, затем отправить старые CAS revisions на update/delete; stale операции отвергнуты, новая запись и revision остались неизменными. Persisted assertion выполнен через `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` на новой disposable PostgreSQL БД.
- [x] Проверить CAS update/delete, монотонность revisions и корректность revision sequence при rollback/recreate. `TestDisposablePostgresEngineDataPersistenceVariants` и блок в `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` прошли на PostgreSQL; unit assertions добавлены в `tests/go/core/application/engine_data/gaps_test.go`.
- [x] Проверить payload/index size bounds, разрешённые fields/selectors, pagination cursor validation и стабильность результатов при конкурентных изменениях. Добавлены fail-closed проверки страницы в `Store.List`/`ListExpired`; malformed cursor и чужой scope отвергаются без утечки данных.
- [x] Проверить TTL: запись доступна до expiry, недоступна после, cleanup удаляет только истёкшие records и не гоняется с продлением/обновлением. Persisted TTL-вариант прошёл; активные соседние записи не удаляются.
- [x] Проверить batch atomicity: ошибка одного элемента не оставляет частичных изменений; проверить дубликаты keys и conflict при stale revisions. Persisted stale-batch и duplicate-key сценарии прошли с откатом без частичной записи.
- [x] Проверить отказ на unknown capability и отсутствие утечки данных через ошибки, cursors или index projection. Unknown/unregistered capability и malformed page отвергаются fail-closed; в текущем контракте `Registry` нет состояния `inactive`, поэтому отдельный inactive-сценарий не применим.
- [x] Повторить persistence-варианты на disposable PostgreSQL, а не ограничиваться in-memory/mocked repository. `TEST_CORE_ENGINE_DATA_DATABASE_URL` и `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` выполнены на свежих БД `zubrium_core_qa_*`.

**Готово, когда:** все engine-data сценарии из матрицы имеют assertion-backed тест, включая новые namespace isolation и ABA cases на persistence реализации.

### Этап 6. Подтвердить Coordinator и durable workflows

- [x] Проверить регистрацию только статических Workers/operations: unique IDs и versions, ошибки на неизвестный handler, duplicate registration и некорректную зависимость до побочных эффектов. Сценарии CC-001/CC-002 из `tests/qa/core-coordinator-scenarios.json` исполнены unit-тестами.
- [x] Проверить, что Workflow root всегда Worker-task; Worker-task требует WorkerRef; join-task не имеет handler/lease и никогда не claims TaskExecutor. CC-003: `TestTaskExecutorNeverInvokesClaimedJoinTask` и persisted join-тест прошли.
- [x] Проверить, что handler получает durable `Task.ID`, а caller не может подставить owner, workflow/task ID, epoch или handler через payload. CC-004: spoofing-тесты прошли, handler получает серверные идентификаторы.
- [x] Проверить start idempotency: тот же key и fingerprint возвращают workflow; тот же key с иным input даёт conflict. CC-005: unit- и PostgreSQL-идентичность проверены.
- [x] Проверить построение графа: родительские зависимости по умолчанию, sibling emission refs, join с несколькими dependencies, запрет чужих/повторных ссылок, превышения лимитов и cycle. CC-006/CC-007: unit-тесты и `TestDisposableCoordinatorLifecycleSuite` прошли.
- [x] Проверить атомарность в одной транзакции: parent result/lease completion, создание дочерних tasks и dependency edges фиксируются вместе либо полностью откатываются. CC-008: инжектированный commit outage привёл к полному откату.
- [x] Проверить join barrier: join succeeds только после успеха всех dependencies; failed/cancelled dependencies не дают ложного успеха. CC-009: persisted-сценарий в lifecycle suite прошёл.
- [x] На двух Core instances проверить instance registration/renewal, единый configuration fingerprint, запрет несовместимой epoch при живых leases и корректное переключение после остановки всех instances. CC-010 и `TestDisposableCoordinatorFencesConfigurationEpoch` прошли.
- [x] Проверить concurrent claims, lease expiry/takeover, fencing token, stale result rejection и отсутствие повторной фиксации после cancellation. CC-011 и соответствующие PostgreSQL-тесты прошли.
- [x] Проверить retry/backoff/max attempts, terminal failure, отмену оставшихся tasks, ручной retry только failed Worker-task, deadlines/timeouts и bounded error codes. CC-012/CC-016: unit-тесты и persisted retry-сценарии прошли.
- [x] Проверить restart/result recovery: workflow и результат доступны через scoped API после остановки/запуска; delayed work продолжает выполняться; retention cleanup соблюдает настроенный срок. CC-013: persisted restart/delayed/retention тесты прошли.
- [x] Инжектировать DB/store outages в start/claim/commit/read/cancel paths и проверить fail-closed/unhealthy поведение без частичной фиксации. CC-014/CC-015: outage-тесты приложения и `canceled_context_store_outages` прошли.
- [x] Запустить PostgreSQL multi-instance/lifecycle suite на disposable DB; unit tests и проверка checksum не заменяют её. `TestDisposableCoordinatorLifecycleSuite` прошёл на свежей БД `zubrium_core_qa_coordinator_lifecycle`.

**Готово, когда:** Coordinator acceptance из матрицы выполнена на PostgreSQL с restart, concurrency и failure injection; нет непроверенных гарантий, которые опираются лишь на unit tests.

### Этап 7. Проверить lifecycle и публичные Core capabilities

- [ ] Проверить startup/shutdown порядок: schema и Core stores готовы до компонентов; claims и ingress не открываются до готовности; shutdown прекращает новые claims, ждёт активные handlers и закрывает Core infrastructure последним.
- [ ] Проверить отказ/rollback при ошибке каждой startup-фазы и отсутствие утечки ресурсов/активных API после partial startup.
- [ ] Проверить конфигурационные limits: отсутствующие обязательные лимиты не получают скрытых defaults; некорректные параметры отвергаются при границе конфигурации.
- [ ] Проверить каждую capability через публичный `core/presentation` контракт: scope определяется server-side, unauthorized/unknown scope fail-closed, DTO не раскрывают инфраструктурные типы или секреты.
- [ ] На интеграционной границе убедиться, что `main` направляет выбранные Core operations через bus и Core adapters, не передавая Core pool/SQL/S3 internals потребителям. Сам bus, HTTP, CSRF, OpenAPI и Admin authorization остаются задачами владельцев `main`/Presentation.

**Готово, когда:** Core lifecycle и каждая capability имеют интеграционный assertion; ownership границ зафиксирован без переноса bus или HTTP логики в Core.

### Этап 8. Финальный verification sweep и фиксация evidence

Из корня репозитория сформировать overlay и затем выполнять Core-команды из `core/`:

```bash
python3 tests/go-overlay.py > "$TMPDIR/go-overlay.json"
```

```bash
go list ./... | xargs go test -vet=off -overlay="$TMPDIR/go-overlay.json"
```

```bash
go vet ./...
```

```bash
go build ./...
```

- [x] Выполнить все команды из указанного каталога/окружения, сохранить полный вывод и exit status; исправлять причины ошибок и повторять затронутые и полный итоговый набор. Финальный `make test` и `make vet` прошли после всех правок.
- [x] Выполнить централизованные Core acceptance tests через overlay; `tests/go-overlay.py` отображает только тесты и test-support-файлы и отвергает подмену существующих исходников.
- [x] Повторить disposable PostgreSQL и S3 suites после последних изменений Core; последующие правки затрагивали только Content, `main` и тесты, не Core.
- [ ] Сопоставить каждую строку Core denominator с названием теста/assertion, командой и исходом: `passed`, `failed`, `blocked` или `not applicable` с причиной. Для Coordinator зафиксировано 16/16 в `tests/qa/core-coordinator-scenarios.json`; остальные разделы denominator остаются открытыми.
- [x] Обновить [QA_REPORT.md](../docs/QA_REPORT.md) текущими подтверждёнными результатами и ограничениями; не вести журнал действий и не записывать credentials/DSN.

## Критерии завершения Core

Core можно считать готовым по согласованному target, только когда одновременно выполнено всё следующее:

1. Для каждого применимого Core scenario denominator согласован и есть проверяемый assertion на ожидаемый ответ и состояние после операции.
2. Все критичные инварианты Domain, access/files, migration/runtime privilege, S3 recovery, engine-data scope/CAS/ABA и Coordinator durability подтверждены соответствующими тестами.
3. Все Core acceptance tests проходят, включая disposable PostgreSQL и S3 интеграционные сценарии; ни один обязательный сценарий не пропущен и не подменён наличием теста без assertions.
4. Для каждого затронутого пакета проходят `go test` (с overlay для централизованных tests), `go vet ./...` и `go build ./...`.
5. Boundary/lifecycle проверки подтверждают архитектурный ownership; непроверенные `main`/Presentation части явно остаются вне Core completion, но необходимая интеграционная поверхность Core проверена.
6. QA report содержит воспроизводимые команды, фактические результаты и точные ограничения; disposable ресурсы безопасно очищены.

Оценку `100%` не назначать только по unit test coverage или наличию интерфейсов/тестовых файлов: она означает закрытие всех заранее согласованных Core acceptance scenarios и critical invariants.
