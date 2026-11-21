# План реализации сценарного покрытия Router Engine

> **Для исполнителей-агентов:** обязательно используй поднавык `superpowers:subagent-driven-development` (рекомендуется) или `superpowers:executing-plans`. Шаги отмечаются чекбоксами (`- [ ]`).

**Цель:** создать полный сценарный red-набор Router по спецификации и устранить подтверждённые провалы реализации.

**Архитектура:** сценарии разделены по route domain/application, HTTP settings/delivery и disposable integration. Стабильные `ROUTER-*` IDs связывают независимые input/result scenarios с tagged Go-тестами или точным техническим блокером. Router остаётся владельцем route topology, mapping, locale resolution и public delivery.

**Стек:** Go 1.26.4 workspace, `tests/go-overlay.py`, Go standard library, disposable PostgreSQL/object storage только для integration.

**Спецификации:** `docs/superpowers/specs/2026-11-21-website-router-test-coverage-design.md`; `spec/engines/router/SPEC-router-engine.md`; Router row в `tests/ACCEPTANCE-MATRIX.md`.

## Глобальные ограничения

- Внешние пути остаются `/api/...` без `/vN`; OpenAPI сохраняет 3.1.0 и строгую parity с HTTP.
- Router единолично владеет сконфигурированными branches, host/path routes и route-to-artifact mappings. Один process обслуживает только настроенные branches; изменение topology требует конфигурации и restart.
- Delivery принимает только зарегистрированные visitor operations и настроенные file/artifact identifiers; не принимать от посетителя Core, group, FileSet, artifact или operation IDs.
- Locale precedence: поддерживаемая валидная preference cookie, затем `Accept-Language` quality/order с RFC 4647 Lookup, затем настроенный default. Locale не выбирается из path/query; fallback между locale content запрещён.
- Core/Loader доступны через Core presentation capabilities и зарегистрированные operations; не добавлять прямые Website/Router вызовы, Router-owned SQL или rate limiting.
- Acceptance-red tests изолированы от обычного CI до устранения целевых провалов. Blockers остаются в denominator и не считаются покрытыми; отчёт показывает процент покрытия относительно порога не менее 60% на модуль, отдельно от statement coverage.
- Не начинать product-code fixes до полного исходного red-baseline по обоим движкам и междвижковому E2E; сначала собрать и запустить все три набора.
- Использовать только уникальные disposable resources; запрещены writes в live API/database/bucket.

## Основные риски для проверки

- Encoded separators, dot segments, duplicate slashes и регистр: сохраняется точная route семантика и защита `/api` — Tasks 2–3.
- Конкурентные mapping updates/stale revisions: проигравший CAS не меняет active delivery — Tasks 2 и 4.
- Invalid/unsupported locale, wildcard и равные `Accept-Language` quality: точный заданный результат, без утечки другого locale — Task 3.
- Неизвестные artifact assets, неожиданные MIME и visitor-selected IDs: bytes не выдаются — Task 3.
- Anonymous upload с неверным MIME/size/idempotency или отозванным доступом: файл/staging residue не становится видимым — Task 4.

---

## Файлы и ответственность

- `tests/qa/router-scenarios.json` — стабильный Router scenario ledger и привязка к источнику, test или blocker; coverage и последний execution result хранятся отдельно.
- `tests/go/engines/router/**/acceptance_test.go`, `tests/go/presentation/router/acceptance_test.go` — tagged Go tests по Router contracts, накладываемые на packages.
- `tests/qa/run-router-red.sh` — отдельный module/level selector с явными disposable prerequisites.
- `tests/qa/scenario-ledger.py` — общий checker/count reporter, созданный планом Website test coverage.
- Реализация Router — минимальные изменения только после фиксации полного red baseline.
- `docs/QA_REPORT.md` и `tests/ACCEPTANCE-MATRIX.md` — evidence и scenario IDs без переписывания посторонних результатов.

## Зависимости и порядок выполнения

- Сначала выполнить Задачу 1 Website-плана, которая создаёт общий `tests/qa/scenario-ledger.py`; затем Router red-набор не зависит от Website test implementation и может готовиться отдельно.
- E2E-plan зависит от обоих scenario ledgers и red selectors. Во всех трёх планах до первого product-code изменения должны быть собраны и запущены полные unit/contract/integration/E2E red-наборы.

- Overlay `python3 tests/go-overlay.py` создаёт `-overlay` JSON для `tests/go/`; новые target-файлы не должны существовать, package directories должны быть.
- Каждый случай — tagged subtest со стабильным `ROUTER-###` ID и конкретными assertions по входу, ответу и состоянию. Статус не выводится из существования файла.
- Запись Router ledger использует ту же схему, что Website: `id` (строка `ROUTER-###`), `source` (спецификация/раздел или matrix clause), `level` (`unit|contract|integration`), `preconditions` (массив строк), `input`, `action`, `expected_result`, `expected_state` (строки-описания), `test_file`, `test_symbol`, `coverage_status` (`covered|blocked|gap`), `execution_status` (`not_run|pass|fail|blocked`), `blocker` (строка или `null`). Общий checker принимает только соответствующую схему и проверяет source/symbol для покрытых тестом сценариев.
- Blockers/gaps входят в знаменатель, но не в coverage numerator; failing executable tests входят в coverage, но не в pass count.
- Runner принимает `unit`, `contract`, `integration` или `all`; запускает `go test -tags=router_acceptance -overlay=... -run '^TestRouterScenario'`, передаёт настоящий exit status. Integration требует явной disposable конфигурации и выводит незаполненные prerequisites как blockers/non-zero, а не pass или silent skip. До устранения целевых провалов не подключать набор к стандартному CI.

### Task 1: Зарегистрировать Router scenarios и изолированный runner

**Файлы:**

- Создать: `tests/qa/router-scenarios.json`
- Создать: `tests/qa/run-router-red.sh`
- Создать: `tests/go/engines/router/features/routes/application/acceptance_ledger_test.go`
- Изменить: `tests/ACCEPTANCE-MATRIX.md`

- [ ] Извлечь независимые input/result scenarios из `SPEC-router-engine.md` и Router row Acceptance Matrix. Назначить `ROUTER-###`; в каждой записи указать источник, level, precondition, action/input, ожидаемые response и persist/visible aftermath, test name/file, статус (`covered`, `blocked`, `gap`).
- [ ] Добавить `TestRouterScenarioLedger` с build tag и тестированием согласованности ledger и subtest IDs; acceptance-файлы исключены из обычного тестового прогона без этого tag.
- [ ] Добавить runner selectors unit/contract/integration/all. Создать overlay в `$TMPDIR`, запускать только tagged Router acceptance tests; integration требует caller-указанную disposable DB/storage конфигурацию. Не угадывать endpoints и не удалять ресурсы вне уникального namespace.
- [ ] Запустить общий ledger checker и overlay generation. Проверить, что красный assertion возвращает scenario failure, отсутствующий prerequisite — blocker; ни один статус не считается pass.

### Task 2: Проверить route model/settings contracts

**Файлы:**

- Создать: `tests/go/engines/router/features/routes/model/acceptance_test.go`
- Создать: `tests/go/engines/router/features/routes/application/acceptance_routes_test.go`
- Создать: `tests/go/engines/router/presentation/acceptance_settings_test.go`
- Создать: `tests/go/presentation/admin/router/acceptance_handler_test.go`
- Изменить: `tests/qa/router-scenarios.json`

- [ ] Добавить `TestRouterScenarioExactRouteMatch`: exact host/path, method, normalized/encoded paths, duplicate/trailing separators, query semantics по spec, precedence и только explicit fallback. Проверять точный route/artifact result либо typed not-found/conflict.
- [ ] Добавить `TestRouterScenarioBranchIsolation`: границы configured branch, изоляция route listings, отказ выбора чужой branch из request; topology меняется только после config/restart.
- [ ] Добавить `TestRouterScenarioRouteCAS`: create/update/delete revision checks, stale CAS rejection, отсутствие side effects при conflict и сохранение прежнего route-to-artifact mapping.
- [ ] Добавить `TestRouterScenarioAdminRouteAuthorization`: direct group levels `0/1/2`, write authorization для каждой мутации, строгая query/body validation и fail-closed на ошибке identity/capability.
- [ ] Добавить `TestRouterScenarioSettingsContract`: OpenAPI 3.1 и mounted `/api/...` parity, общие errors, bounded JSON, обязательные revision/idempotency headers и отсутствие внешних `/api/vN` aliases.
- [ ] Добавить `TestRouterScenarioReadiness`: not-ready/ready transitions, наличие registered operations и готовность configured branches; missing dependency не даёт ложный ready.
- [ ] Связать assertions с ledger IDs и запустить Router unit/contract selectors. Не менять implementation до завершения всего red набора.

### Task 3: Проверить locale и public artifact/file delivery

**Файлы:**

- Создать: `tests/go/presentation/router/acceptance_delivery_test.go`
- Создать: `tests/go/engines/router/presentation/acceptance_delivery_test.go`
- Изменить: `tests/qa/router-scenarios.json`

- [ ] Добавить `TestRouterScenarioLocaleResolution`: supported valid preference cookie, invalid/unsupported cookie, `Accept-Language` quality/order, RFC 4647 Lookup, wildcard только к default, configured default, отказ выбирать locale из path/query. Проверить отсутствие cross-locale content fallback.
- [ ] Добавить `TestRouterScenarioLocaleConfiguration`: разрешённые locale map/default, отказ для unsupported/duplicate locales и некорректного default; route activation использует только настроенный locale.
- [ ] Добавить `TestRouterScenarioLocalePreferenceEndpoint`: exact-Origin/return-path validation, host-only cookie, configured lifetime/Secure policy, отказ open redirect и отсутствие сохранения rejected locale.
- [ ] Добавить `TestRouterScenarioArtifactDelivery`: точный configured artifact binding, валидный `static-page-artifact/v1` document/asset, отказ unknown asset/ID, MIME allowlist и запрет HTML asset response. Проверить cache/no-store policy из спецификации.
- [ ] Добавить `TestRouterScenarioLocaleTargetCAS`: для независимых `(route, locale)` targets проверить отдельный CAS, отказ stale activation без изменения старой выдачи и повторное применение только текущего результата.
- [ ] Добавить `TestRouterScenarioFileDelivery`: exact configured file ID/path, GET/HEAD parity, корректные content headers, отказ unknown/visitor-selected file, запрет публичного доступа через FileSet membership.
- [ ] Добавить `TestRouterScenarioRouteSecurity`: encoded `/api` path protection, traversal/encoded separators, 404 для неизвестного маршрута без explicit fallback и отсутствие неявной route/URL dispatch.
- [ ] Связать тесты с ledger rows и запустить Router delivery/contract selectors. Проверить, что public HTTP ingress не принимает request-supplied operation ID.

### Task 4: Проверить registered operations, upload и persistence

**Файлы:**

- Создать: `tests/go/presentation/router/acceptance_operations_test.go`
- Создать: `tests/go/presentation/router/publicupload/acceptance_test.go`
- Создать: `tests/go/engines/router/features/routes/repository/acceptance_integration_test.go`
- Изменить: `tests/qa/router-scenarios.json`

- [ ] Добавить `TestRouterScenarioRegisteredOperations`: static visitor operation registry, строгая input schema и size limits, typed owner mapping, отказ unknown/duplicate operation и caller-selected operation/target IDs.
- [ ] Добавить `TestRouterScenarioLoaderBackedUpload`: server-configured destination, MIME/size limits, idempotency, staging isolation и completion через registered Loader-backed operation; запрет arbitrary destination/Core IDs.
- [ ] Добавить negative upload subtests для invalid content type, oversized body, bad idempotency/replay, cancellation, Loader failure и access revocation. Проверить отсутствие частично видимого Core file, staging leak и unauthorized membership.
- [ ] Добавить disposable Core-capability persistence tests для route/artifact mapping CAS race, restart persistence, branch isolation, file access revocation и cleanup. FileSet остаётся только index и не даёт доступ к bytes.
- [ ] Недоступные production factories/object storage записать отдельными blockers с prerequisites. Fake-only test не считать integration pass. Запускать integration selector только на явно подтверждённых disposable resources.

### Task 5: Зафиксировать red baseline и исправить Router gaps

**Файлы:**

- Изменить: только Router implementation files, на которые указывают red scenarios
- Изменить: `tests/qa/router-scenarios.json`, `tests/ACCEPTANCE-MATRIX.md`, `docs/QA_REPORT.md`

- [ ] До первого product-code изменения запустить все Router unit/contract/integration selectors и сохранить status каждого ID, failures, blockers и точные команды.
- [ ] Исправлять только нарушения `SPEC-router-engine.md`; сохранить assertions и не добавлять route aliases, скрытые config defaults или authorization fallbacks.
- [ ] После каждого fix group повторить его selector и затронутые соседние проверки. Отмечать scenario как passed только после успешных response/post-state assertions; blocker остаётся uncovered в denominator.
- [ ] Выполнить `gofmt`; из каждого изменённого Go module запустить `go test ./...`, `go vet ./...`, `go build ./...`. Отдельно запускать tagged Router overlay suite. Сверить выбранные проверки с существующими `main/Makefile` командами `test-router` без замены ими acceptance ledger.
- [ ] Обновить QA report и Acceptance Matrix фактическими evidence/counts. Не объявлять междвижковое browser E2E завершённым: оно описано в отдельном плане Website–Router E2E.
