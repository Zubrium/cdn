# План реализации сценарного покрытия Website Engine

> **Для исполнителей-агентов:** обязательно используй поднавык `superpowers:subagent-driven-development` (рекомендуется) или `superpowers:executing-plans`. Шаги отмечаются чекбоксами (`- [ ]`).

**Цель:** создать полный сценарный red-набор Website по нормативным требованиям спецификаций, затем довести реализацию до прохождения проверок.

**Архитектура:** каждому независимому сценарию назначается стабильный ID; ledger связывает его с исполняемым Go/Vitest тестом либо точным техническим блокером. Go-тесты накладываются на пакеты существующим overlay. Сначала формируется весь набор и фиксируются исходные провалы/блокеры, затем устраняются только доказанные нарушения контракта; Core, Coordinator, Loader и workers проверяются лишь на границах Website.

**Стек:** Go 1.26.4 workspace, `tests/go-overlay.py`, Go standard library, существующий Vitest Admin test runner, disposable PostgreSQL/object storage только для integration.

**Спецификации:** `docs/superpowers/specs/2026-11-21-website-router-test-coverage-design.md`; `spec/engines/website/SPEC-website-engine.md`, `SPEC-site-editor-workspace.md`, `SPEC-data-model.md`, `SPEC-branches-snapshots.md`, `features/SPEC-feature-runtime.md`, `SPEC-static-site-artifact.md`, `SPEC-public-server-rendering.md`, `SPEC-seo.md`, `SPEC-system-pages.md`, `SPEC-design-tokens.md`; `tests/ACCEPTANCE-MATRIX.md`.

## Глобальные ограничения

- Website владеет структурой страниц, деревьями snapshot, импортом features, шаблонами и детерминированным созданием статического page artifact; URL/route binding и публикация принадлежат Router.
- Feature import, media derivative и prerender выполняются как durable Coordinator Worker tasks (`worker.archive`, `worker.media`, `worker.prerender`); workers не вызывают engines.
- Persistence/storage доступны только через Core presentation capabilities; не добавлять Website-owned SQL schemas, migrations, DSN или прямые runtime-вызовы Router/Content/Forms.
- Контракт артефакта — `static-page-artifact/v1`; Router binding/activation выполняется отдельно.
- Acceptance-red tests запускаются отдельными командами, не входящими в обычный CI, пока целевое поведение намеренно красное. Блокер остаётся в знаменателе и не покрыт; содержательный failing test считается покрытым сценарием и отдельно проваленным. Отчёт показывает denominator и долю покрытия относительно порога не менее 60% для каждого модуля; statement coverage — отдельная метрика.
- Использовать только уникальные disposable resources; не отправлять write-запросы в live API/database/bucket. Удалять только ресурсы, созданные данным прогоном.

## Основные риски для проверки

- Конкурентные изменения шаблона/snapshot: проигравший CAS не оставляет побочных эффектов — Task 3.
- ZIP с traversal, включая Windows paths, symlinks, дубликатами, повреждёнными данными и граничными лимитами: отказ до появления частичного import — Task 2.
- Ошибка/cancellation media или prerender: прежнее field/artifact сохраняется, частичный output не публикуется — Task 4.
- Изменение locale/projection input после начала работы: stale output не заменяет текущий artifact — Tasks 3–4.
- Structural replacement/retirement: родительский snapshot, Content values/history и Router binding сохраняются, кроме отдельно заданного retirement workflow — Tasks 2 и 4.

---

## Файлы и ответственность

- `tests/qa/website-scenarios.json` — реестр Website: стабильный ID, источник, уровень, предусловия, вход/действие, результат/состояние, тест и отдельные coverage/execution statuses.
- `tests/go/engines/website/**/acceptance_test.go` — tagged Go scenarios по реальным domain/application/presentation контрактам, накладываемые `tests/go-overlay.py`.
- `tests/qa/run-website-red.sh` — отдельный Website red runner и явный вывод prerequisites.
- Реализация Website — только минимальные исправления после фиксации baseline.
- `docs/QA_REPORT.md` — сводное evidence по Website, без хронологического журнала.
- `docs/TODO-admin-snapshots-content-files.md` — только актуальные незакрытые пункты после подтверждения evidence.

## Порядок выполнения и зависимости

- Этот план создаёт общий `tests/qa/scenario-ledger.py`, который затем использует Router- и E2E-планы.
- Website и Router test suites формируются независимо после создания ledger checker; общий E2E зависит от обоих наборов.
- Сначала собрать и запустить все red-наборы Website, Router и E2E; product-code fixes начинать только после фиксации их полных исходных baseline.

## Интерфейсы

- Overlay: `python3 tests/go-overlay.py` создаёт JSON mapping из `tests/go/**` в package paths; target-файл overlay должен отсутствовать, package directory — существовать.
- Схема каждой записи ledger фиксирована: `id` (строка `WEB-###`), `source` (спецификация/раздел или matrix clause), `level` (`unit|contract|integration|e2e`; Website uses the first three), `preconditions` (массив строк), `input` (описание), `action` (описание), `expected_result` (описание), `expected_state` (описание), `test_file`, `test_symbol`, `coverage_status` (`covered|blocked|gap`), `execution_status` (`not_run|pass|fail|blocked`), `blocker` (строка или `null`).
- Каждый исполняемый случай имеет `WEB-###` subtest, конкретные input/response/state assertions и запись в ledger. Ledger — источник знаменателя и counts.
- `tests/qa/run-website-red.sh` принимает `unit`, `contract`, `integration` или `all`; создаёт overlay в `$TMPDIR`, запускает только acceptance build-tag и имена `^TestWebsiteScenario`, сохраняет настоящий exit status. Для integration нужны явные disposable DB/storage inputs. Никаких неявных endpoints, credentials или очистки ресурсов за границами уникального прогона.
- Ledger различает `covered`, `blocked`, `gap`; блокеры и gaps входят в denominator, но не в числитель покрытия. `skip`, TODO, fixture и тест без содержательных assertions покрытием не являются.

### Task 1: Создать Website ledger и отдельный red runner

**Файлы:**

- Создать: `tests/qa/website-scenarios.json`
- Создать: `tests/qa/run-website-red.sh`
- Создать: `tests/qa/scenario-ledger.py`
- Создать: `tests/go/engines/website/application/acceptance_ledger_test.go`
- Изменить: `tests/ACCEPTANCE-MATRIX.md`

- [ ] Извлечь независимые input/result scenarios из указанных Website спецификаций и строки Website в Acceptance Matrix. Назначить стабильные `WEB-###`; для каждой записи указать конкретный spec clause/matrix criterion.
- [ ] В каждой записи указать уровень, предусловия, input/action, HTTP/domain result, persist/visible aftermath, тестовый файл и symbol, coverage status (`covered`, `blocked`, `gap`), execution status (`not_run`, `pass`, `fail`, `blocked`) и точную причину blocker. Blockers/gaps входят в denominator, но не в coverage numerator; исполняемые failing tests входят в numerator, но не в pass count.
- [ ] Реализовать `scenario-ledger.py` на стандартной библиотеке Python: проверить уникальность ID, обязательные поля и допустимые статусы; проверить наличие test symbol/ID в указанном исходнике; вывести denominator, покрытые сценарии, gaps и blockers отдельно. Covered без реального теста — ошибка.
- [ ] Добавить `TestWebsiteScenarioLedger` с build tag, проверяющий согласованность ledger и тестовых subtest IDs. Все acceptance-файлы, включая validator test, должны быть исключены из стандартного прогона без этого tag.
- [ ] Реализовать unit/contract/integration/all selectors в runner. Сгенерировать overlay в `$TMPDIR`, вызвать `go test -tags=website_acceptance -overlay=... -run '^TestWebsiteScenario'`; для UI использовать штатную Vitest-команду с отдельным file selector. Для integration требовать явно заданные disposable DB/storage inputs и возвращать non-zero при отсутствующем prerequisite, не подменяя blocker на pass.
- [ ] Запустить overlay validation, ledger checker и unit red selector; отличить ожидаемые failing assertions от syntax/overlay errors. Product code пока не менять.

### Task 2: Сначала добавить Website unit и Admin UI scenarios

**Файлы:**

- Создать: `tests/go/engines/website/application/acceptance_rules_test.go`
- Создать: `tests/go/engines/website/repository/acceptance_keys_test.go`
- Изменить: `tests/qa/website-scenarios.json`

- [ ] Добавить `TestWebsiteScenarioPageRootCreation`: server-generated UUID, один root snapshot при создании page, независимые page trees и отказ caller-supplied identity там, где контракт запрещает.
- [ ] Добавить `TestWebsiteScenarioSnapshotCAS`: structural edits, stale CAS conflict, отсутствие мутации при conflict, сохранение parent snapshot и отсутствие неявных изменений route/Content values.
- [ ] Добавить `TestWebsiteScenarioSiteEditorBindings`: typed structural bindings, допустимые arguments/data inputs, invalid/unknown binding и точная state после отказа.
- [ ] Добавить `TestWebsiteScenarioPageDeletion`: удаление/retirement через нормативный workflow удаляет только route delivery targets, сохраняет snapshots и связанные Content/Forms/Core resources и историю; retry безопасен.
- [ ] Добавить `TestWebsiteScenarioLocaleProjection`: trusted projection ограничена entity/locale и catalog revision; неверная или устаревшая projection отклоняется без fallback на другую locale.
- [ ] Добавить `TestWebsiteScenarioDataModelCardinality`: `many`/`singleton`, одна пустая draft-запись нового singleton без auto-publication, очистка с сохранением record ID и отказ cardinality change, теряющего records.
- [ ] Добавить `TestWebsiteScenarioFeatureRuntime`: только manifest-declared HTTPS origins и typed contracts, отсутствие секретов в public context, cancellation/disposal запросов и ограничения CSP/CORS/network из runtime spec.
- [ ] Добавить `TestWebsiteScenarioTemplateMaterialization`: immutable materialization, revision CAS, invalid template и сохранение выбранного template при ошибке.
- [ ] Добавить `TestWebsiteScenarioFeatureImportPreflight`: malformed ZIP, traversal (включая Windows paths), symlink/duplicate paths, archive limits/digest, invalid manifest/ABI и atomic visibility; после отказа нет частичных feature records.
- [ ] Добавить `TestWebsiteScenarioArtifactValidation`: `static-page-artifact/v1`, deterministic output, document metadata, asset path/MIME constraints, unknown/missing asset и отсутствие visitor-selected identifiers.
- [ ] Добавить `TestWebsiteScenarioRenderAndSEO`: server-rendered/hydratable markup, system pages, locale-specific SEO fields, canonical/hreflang/sitemap output, safe rendering и точное empty/missing-data поведение из спецификаций.
- [ ] Добавить `TestWebsiteScenarioDesignTokens`: корректные light/dark values; invalid/incomplete values не сохраняют частичные изменения.
- [ ] Связать каждый тест с IDs в ledger, запустить Website unit selector и зафиксировать failures/blockers, не исправляя product code.

### Task 3: Добавить Website contract и persistence integration scenarios

**Файлы:**

- Создать: `tests/go/engines/website/presentation/acceptance_contract_test.go`
- Создать: `tests/go/engines/website/repository/acceptance_integration_test.go`
- Изменить: `tests/qa/website-scenarios.json`, `tests/ACCEPTANCE-MATRIX.md`, `docs/QA_REPORT.md`

- [ ] Добавить `TestWebsiteScenarioHTTPContract`: OpenAPI 3.1 parity, mounted `/api/...` handlers, success/error schema, strict bounded decode, access/group-level checks, required revision/idempotency headers и отсутствие route fields в page/snapshot contract.
- [ ] Добавить disposable persistence scenarios через Core capabilities: page/snapshot isolation, immutable feature/template/artifact records, CAS race, retry/idempotency, access revocation, atomic feature-import recovery и artifact cleanup. Проверить namespace isolation и отсутствие прямой engine-to-engine dependency.
- [ ] Недоступную production factory/capability/disposable object storage записать как конкретный blocker с prerequisite; fake-only test не выдавать за integration pass.
- [ ] Добавить `WEB-*` IDs в Website row Acceptance Matrix; не добавлять независимые Core/Coordinator scenarios в Website denominator. Обновить ledger точными test names и результатами.
- [ ] Запустить contract selector и integration selector только при наличии явно подтверждённых disposable ресурсов; сохранить команды/evidence в QA report, не удаляя существующий текст.

### Task 4: Проверить durable Worker workflows и границы Website

**Файлы:**

- Создать: `tests/go/engines/website/application/acceptance_workflows_test.go`
- Создать: `tests/go/workers/archive/acceptance_website_test.go`
- Создать: `tests/go/workers/media/acceptance_website_test.go`
- Создать: `tests/go/workers/prerender/acceptance_website_test.go`
- Изменить: `tests/qa/website-scenarios.json`

- [ ] Добавить `TestWebsiteScenarioFeatureImportWorkflow`: typed `worker.archive` receipt, corrupt/unsafe archive, retry/idempotency, cancellation и отсутствие partial Core output.
- [ ] Добавить `TestWebsiteScenarioMediaWorkflow`: manifest-pinned `x-media` profile/version, source fingerprint/idempotency, MIME/size/checksum validation, supervised process/resource/network/time limits, group/FileSet inheritance, original retention, commit только после readiness и сохранение старого field при failure/cancellation.
- [ ] Добавить `TestWebsiteScenarioPrerenderWorkflow`: strict `render-plan/v1`, locale/input digest, validation/readiness, network allowlist и отказ loopback/private/link-local/metadata адресов, sandbox/resource limits, cancellation/restart, stale-result rejection, prior artifact preservation и Core-only writes.
- [ ] Добавить `TestWebsiteScenarioFeatureDependencies`: resolver/dependency lock, неизвестная или несовместимая dependency и восстановление после import failure; повторный запуск не создаёт дубликатов и не раскрывает частичный результат.
- [ ] Добавить `TestWebsiteScenarioPageRetirement`: durable withdrawal targets, повторяемость retry, успешные withdrawals остаются, прежние snapshot/Content/Forms/Core file данные сохраняются, file cleanup не выполняется автоматически.
- [ ] Проверить worker registration/readiness и безопасный task summary только по существующим typed contracts; не расширять suite до независимой Coordinator acceptance-программы.
- [ ] Убедиться, что overlay target directories существуют; запускать tagged Worker checks отдельно. Fake-only coverage не считать persistence integration.

### Task 5: Зафиксировать полный red baseline, затем исправить Website gaps

**Файлы:**

- Изменить: только Website implementation files, на которые указывают проваленные scenarios
- Изменить: `tests/qa/website-scenarios.json`, `docs/QA_REPORT.md`, `docs/TODO-admin-snapshots-content-files.md`

- [ ] До первого product-code изменения запустить все Website unit/contract/integration red selectors и сохранить результаты по каждому ID, failures и infrastructure blockers.
- [ ] Отдельно проверить известные текущие сбои `TestSnapshotListRejectsUnknownSiteAndMalformedCursor` и `TestCreateSnapshotRejectsStaleOrProvisioningParent` из `docs/QA_REPORT.md`; сопоставить их с ledger IDs, не считать заново подтверждёнными, пока они не прогнаны текущей командой.
- [ ] Сгруппировать failures по контрактным областям и исправлять минимальное поведение Website application/presentation/repository. Сохранять assertions; не ослаблять ожидаемый результат под текущую реализацию.
- [ ] После каждой группы прогонять её selector и соседние regressions. Помечать ID пройденным только после успешных input/result/state assertions; blocker остаётся uncovered в denominator до выполнения проверки.
- [ ] Для изменённых Go-файлов выполнить `gofmt`; затем из `engines/website/` запустить `go test ./...`, `go vet ./...`, `go build ./...`; для изменённых worker modules отдельно выполнить те же команды. Отдельно прогнать tagged overlay suite.
- [ ] Обновить QA report реальными commands, fixed failures, blockers и scenario coverage/pass counts; TODO оставить только активные работы. Не объявлять E2E или заблокированные инфраструктурой сценарии завершёнными.
