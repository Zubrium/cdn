# План завершения Core

## Цель и границы

Довести Core до полного соответствия согласованным Core-контрактам и всем применимым Core acceptance-сценариям из [матрицы приёмки](../tests/ACCEPTANCE-MATRIX.md). Для каждого сценария должны быть определены предусловия, вход, ожидаемые ответ и состояние после операции, исполняемый тест с assertions и фактическое evidence. Целевой показатель — 100% согласованных Core-сценариев и критичных инвариантов, а не 100% statement coverage.

Core остаётся единственным владельцем PostgreSQL, S3, общего файлового cache, Core identity/access, File API capabilities, engine-data и Coordinator. Не переносить в Core HTTP/cookie/CSRF политику, engine-specific модели и операции, bus implementation или продуктовую оркестрацию. Вызовы Core через bus и static composition проверять на интеграционной границе; их реализация принадлежит `main` и Presentation.

Авторитетные контракты: [SPEC-core-architecture.md](../spec/core/SPEC-core-architecture.md), [SPEC-coordinator-platform.md](../spec/architecture/SPEC-coordinator-platform.md), [ACCEPTANCE-MATRIX.md](../tests/ACCEPTANCE-MATRIX.md) и операционные требования [CORE-OPERATIONS.md](../docs/CORE-OPERATIONS.md).

## Текущее подтверждённое состояние

- Централизованные Core Go-тесты прошли по физическим пакетам через `tests/go-overlay.py` с `-vet=off`; PostgreSQL acceptance запускался в ARM64 Linux-контейнере с Go 1.26.4 и Atlas v1.3.0. Отдельные `go vet ./...` и `go build ./...` из `core/` прошли.
- S3 integration round-trip прошёл через локальный тестовый `s3mock`; внешний S3 endpoint и credentials не использовались.
- Существующие PostgreSQL acceptance tests прошли на отдельных `zubrium_core_qa_*` БД в tmpfs-контейнере, включая чистый Atlas baseline/checksum и повторный startup, runtime privilege validation, Core v2 session/file/access и Coordinator lifecycle suite.
- Persisted engine-data regressions для ABA после удаления/повторного создания и изоляции между двумя зарегистрированными namespaces добавлены и прошли на свежей disposable PostgreSQL БД.
- Нужно сопоставить каждый Core acceptance scenario с assertion/evidence и определить оставшиеся gaps Coordinator failure-injection и lifecycle; успешный прогон имеющегося Coordinator suite сам по себе не доказывает полноту denominator.
- Для Core Domain требуется сверить прямые тесты с каждым согласованным инвариантом; пакеты без test-файлов остаются предметом этой проверки.

Предыдущие результаты не считать доказательством для сценария, который был пропущен, не запускался или завершился ошибкой. Итоговые команды и evidence обновлять в [QA_REPORT.md](../docs/QA_REPORT.md), не записывая DSN, секреты или credentials.

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
- [ ] Для архитектурных проверок использовать доступные AST/structure tests и имеющуюся boundary-проверку; если целевая `make lint-core-boundary`/Makefile отсутствует в checkout, не считать такую проверку выполненной и зафиксировать фактический runnable equivalent либо blocker.

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
- [ ] Подготовить контролируемую несовместимую/неуправляемую схему и проверить ранний отказ до любых DDL или изменения существующих объектов.
- [ ] Проверить конкурентный bootstrap и миграционный запуск; подтвердить отсутствие дубликатов пользователей/групп и целостность назначений.
- [ ] Проверить transaction rollback на ошибках до и во время фиксации; проверить конкурентные metadata/file operations.
- [ ] Пройти upload/staging lifecycle: начало, повтор, finalize, crash-window recovery, abort/expiry и cleanup; не допускать выдачи незавершённых объектов.
- [ ] Проверить приватность S3, generated UUID keys, checksum, лимиты, повтор операции после неопределённого ответа и безопасную обработку сирот.
- [ ] Проверить cache invalidation между двумя Core instances, включая изменение metadata, revoke access и object-store failure.
- [ ] Проверить, что cleanup удаляет только созданные данным тестом записи/объекты и подтверждённые точные disposable ресурсы.

**Готово, когда:** все перечисленные DB/S3 сценарии выполнены на disposable ресурсах, результаты сверены с состоянием БД/S3, а тесты не оставили неопознанных ресурсов.

### Этап 5. Закрыть engine-data acceptance gaps

- [x] Проверить capability binding к зарегистрированному `engine_id`/namespace: consumer не может выбрать или подменить чужой owner через request payload. Проверено persisted regression с двумя зарегистрированными module ID и изолированными namespaces.
- [x] Добавить cross-namespace regression: capability первого module создаёт запись; capability второго module не может прочитать, изменить, удалить или перечислить её, включая guessed ID и selector. Persisted assertion выполнен через `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` на новой disposable PostgreSQL БД.
- [x] Добавить ABA regression: создать запись, зафиксировать revision, удалить запись, пересоздать её, затем отправить старые CAS revisions на update/delete; stale операции отвергнуты, новая запись и revision остались неизменными. Persisted assertion выполнен через `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` на новой disposable PostgreSQL БД.
- [ ] Проверить CAS update/delete, монотонность revisions и корректность revision sequence при rollback/recreate.
- [ ] Проверить payload/index size bounds, разрешённые fields/selectors, pagination cursor validation и стабильность результатов при конкурентных изменениях.
- [ ] Проверить TTL: запись доступна до expiry, недоступна после, cleanup удаляет только истёкшие records и не гоняется с продлением/обновлением.
- [ ] Проверить batch atomicity: ошибка одного элемента не оставляет частичных изменений; проверить дубликаты keys и conflict при stale revisions.
- [ ] Проверить отказ на unknown/inactive capability и отсутствие утечки данных через ошибки, cursors или index projection.
- [ ] Повторить persistence-варианты на disposable PostgreSQL, а не ограничиваться in-memory/mocked repository.

**Готово, когда:** все engine-data сценарии из матрицы имеют assertion-backed тест, включая новые namespace isolation и ABA cases на persistence реализации.

### Этап 6. Подтвердить Coordinator и durable workflows

- [ ] Проверить регистрацию только статических Workers/operations: unique IDs и versions, ошибки на неизвестный handler, duplicate registration и некорректную зависимость до побочных эффектов.
- [ ] Проверить, что Workflow root всегда Worker-task; Worker-task требует WorkerRef; join-task не имеет handler/lease и никогда не claims TaskExecutor.
- [ ] Проверить, что handler получает durable `Task.ID`, а caller не может подставить owner, workflow/task ID, epoch или handler через payload.
- [ ] Проверить start idempotency: тот же key и fingerprint возвращают workflow; тот же key с иным input даёт conflict.
- [ ] Проверить построение графа: родительские зависимости по умолчанию, sibling emission refs, join с несколькими dependencies, запрет чужих/повторных ссылок, превышения лимитов и cycle.
- [ ] Проверить атомарность в одной транзакции: parent result/lease completion, создание дочерних tasks и dependency edges фиксируются вместе либо полностью откатываются.
- [ ] Проверить join barrier: join succeeds только после успеха всех dependencies; failed/cancelled dependencies не дают ложного успеха.
- [ ] На двух Core instances проверить instance registration/renewal, единый configuration fingerprint, запрет несовместимой epoch при живых leases и корректное переключение после остановки всех instances.
- [ ] Проверить concurrent claims, lease expiry/takeover, fencing token, stale result rejection и отсутствие повторной фиксации после cancellation.
- [ ] Проверить retry/backoff/max attempts, terminal failure, отмену оставшихся tasks, ручной retry только failed Worker-task, deadlines/timeouts и bounded error codes.
- [ ] Проверить restart/result recovery: workflow и результат доступны через scoped API после остановки/запуска; delayed work продолжает выполняться; retention cleanup соблюдает настроенный срок.
- [ ] Инжектировать DB/store outages в start/claim/commit/read/cancel paths и проверить fail-closed/unhealthy поведение без частичной фиксации.
- [ ] Запустить PostgreSQL multi-instance/lifecycle suite на disposable DB; unit tests и проверка checksum не заменяют её.

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

- [ ] Выполнить все команды из указанного каталога/окружения, сохранить полный вывод и exit status; исправлять причины ошибок и повторять затронутые и полный итоговый набор.
- [ ] Выполнить централизованные Core acceptance tests через overlay; проверить, что overlay отображает только нужные тесты/helpers и не подменяет существующие исходники.
- [ ] Повторить disposable PostgreSQL и S3 suites после последних изменений; не считать прежний успешный run доказательством после изменения соответствующего кода.
- [ ] Сопоставить каждую строку Core denominator с названием теста/assertion, командой и исходом: `passed`, `failed`, `blocked` или `not applicable` с причиной.
- [ ] Обновить [QA_REPORT.md](../docs/QA_REPORT.md) текущими подтверждёнными результатами и ограничениями; не вести журнал действий и не записывать credentials/DSN.

## Критерии завершения Core

Core можно считать готовым по согласованному target, только когда одновременно выполнено всё следующее:

1. Для каждого применимого Core scenario denominator согласован и есть проверяемый assertion на ожидаемый ответ и состояние после операции.
2. Все критичные инварианты Domain, access/files, migration/runtime privilege, S3 recovery, engine-data scope/CAS/ABA и Coordinator durability подтверждены соответствующими тестами.
3. Все Core acceptance tests проходят, включая disposable PostgreSQL и S3 интеграционные сценарии; ни один обязательный сценарий не пропущен и не подменён наличием теста без assertions.
4. Для каждого затронутого пакета проходят `go test` (с overlay для централизованных tests), `go vet ./...` и `go build ./...`.
5. Boundary/lifecycle проверки подтверждают архитектурный ownership; непроверенные `main`/Presentation части явно остаются вне Core completion, но необходимая интеграционная поверхность Core проверена.
6. QA report содержит воспроизводимые команды, фактические результаты и точные ограничения; disposable ресурсы безопасно очищены.

Оценку `100%` не назначать только по unit test coverage или наличию интерфейсов/тестовых файлов: она означает закрытие всех заранее согласованных Core acceptance scenarios и critical invariants.
