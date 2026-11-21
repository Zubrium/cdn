# План междвижкового E2E Website–Router

> **Для исполнителей-агентов:** обязательно используй поднавык `superpowers:subagent-driven-development` (рекомендуется) или `superpowers:executing-plans`. Шаги отмечаются чекбоксами (`- [ ]`).

**Цель:** проверить реальный админский/API-to-public-browser workflow Website и Router на изолированных disposable данных, включая отказ, retry и сохранение прежней выдачи.

**Архитектура:** Playwright управляет реальными Admin и public HTTP entrypoints именно этого checkout. Тест проверяет состояние durable workflow через поддерживаемый API, валидирует immutable `static-page-artifact/v1`, затем отдельным Router CAS binding публикует его и проверяет результат в браузере. Движки не вызывают друг друга напрямую. Набор имеет отдельную red-команду и строгую проверку prerequisites.

**Стек:** существующий Admin React/TypeScript workspace; Playwright Test только если в workspace нет подходящего browser runner; реальная Go HTTP composition; disposable PostgreSQL и object storage.

**Спецификации:** `docs/superpowers/specs/2026-11-21-website-router-test-coverage-design.md`; Website `SPEC-site-editor-workspace.md`, `SPEC-static-site-artifact.md`, `SPEC-public-server-rendering.md`, `SPEC-branches-snapshots.md`; `spec/engines/router/SPEC-router-engine.md`; `tests/ACCEPTANCE-MATRIX.md`.

## Глобальные ограничения

- Не отправлять мутации в live API/database/bucket или общий Vitasil stack. Runner запускает только test instances/resources, созданные для данного прогона, и удаляет только их.
- Требовать явные test-only DB/storage inputs и уникальный run namespace; запрещены implicit network targets, credentials, fallback services и массовая очистка.
- Перед любым write workflow доказать, что Admin API принадлежит тестовой composition из текущего checkout; при несовпадении origin/build identity остановиться.
- Все межкомпонентные runtime calls идут через process-local bus в `main` с static profile allowlist и общим JSON envelope; durable work остаётся в Core Coordinator.
- Website создаёт и валидирует artifact; только Router управляет route mapping/activation и public delivery. Успешный Website build сам по себе не делает artifact публичным.
- Browser scenarios не считаются пройденными по typecheck, unit tests или HTTP-only checks. Отсутствие production factories/runtime — блокер, а не успех тестовой заглушки.
- До фиксации полного исходного red-baseline по обоим движкам и E2E не начинать изменения product code; сначала собрать все suites и классифицировать результаты.

## Основные риски для проверки

- Несовпадающий API target или недоступный runtime: остановиться до первого write и назвать prerequisite — Task 1.
- Медленный/retried durable worker и обновление browser: ожидать workflow state, не полагаться на фиксированные задержки — Task 2.
- Ошибка Website build, stale output или частичный отказ worker: прежний public route не меняется — Tasks 2–3.
- Router CAS conflict после build: не выдавать unbound/stale artifact — Task 3.
- Browser reload, locale preference или прямой asset request: выдавать только явно привязанные artifact/locale, без межлокального fallback — Task 4.

---

## Файлы и ответственность

- `tests/e2e/playwright.config.ts` — browser projects, lifecycle локальных webServer, явные timeouts и failure artifact retention.
- `tests/e2e/compose.yaml` и `tests/e2e/postgres-init.sh` — собственные disposable PostgreSQL и S3-compatible services под уникальным Compose project данного прогона.
- `tests/e2e/support/fixtures.ts` — уникальный run identity, проверка API target, setup/teardown и учёт владения ресурсами.
- `tests/e2e/support/feature-fixtures.ts` — детерминированное создание валидных и повреждённых ZIP fixtures.
- `tests/e2e/scenarios.json` — `E2E-*` IDs, spec/Acceptance-Matrix источник, inputs, ожидаемые result/state, test symbol, отдельные coverage/execution statuses и blocker.
- `tests/e2e/specs/website-router-publication.spec.ts` — Admin/API/worker/artifact/Router/public success и failure workflows.
- `tests/e2e/specs/router-locale-delivery.spec.ts` — locale preference, cookie и browser/public delivery.
- `tests/qa/run-website-router-e2e.sh` — отдельная команда и prerequisite checks.
- `frontend/package.json` — E2E script/Playwright Test dependency только если подходящего runner ещё нет.
- Runtime composition в `main/` — менять только если baseline докажет отсутствие нормативного server entrypoint/factory; соблюдать существующую static composition root, не создавать альтернативную production topology.
- `docs/QA_REPORT.md` — baseline, версии browser/runtime, точная команда, результаты по ID и ограничения.

## Интерфейсы

- Зависит от `WEB-*` и `ROUTER-*` ledgers и отдельных red selectors из `2026-11-21-website-test-coverage.md` и `2026-11-21-router-test-coverage.md`.
- Каждая запись E2E ledger использует общую схему `scenario-ledger.py`: `id` (строка `E2E-*`), `source` (спецификация/раздел или matrix clause), `level` (`e2e`), `preconditions` (массив строк), `input`, `action`, `expected_result`, `expected_state` (строки-описания), `test_file`, `test_symbol`, `coverage_status` (`covered|blocked|gap`), `execution_status` (`not_run|pass|fail|blocked`), `blocker` (строка или `null`). Общий checker принимает enum уровней `unit|contract|integration|e2e`; E2E records используют `e2e`.
- Каждый browser test проверяет API response, durable workflow terminal state, artifact/route state и фактически видимый результат в browser, где это применимо. Coverage и последний execution result раздельны; blockers/gaps остаются в знаменателе и не считаются покрытыми.
- У write suite нет внешнего `BASE_URL` обходного пути: запускается только server, чей API target/build identity доказан как принадлежащий этому checkout. Если composition невозможно запустить, записать точный blocker и не отправлять write request.

### Task 1: Создать изолированный browser harness и safety gate

**Файлы:**
- Создать: `tests/e2e/playwright.config.ts`
- Создать: `tests/e2e/compose.yaml`
- Создать: `tests/e2e/postgres-init.sh`
- Создать: `tests/e2e/support/fixtures.ts`
- Создать: `tests/e2e/support/feature-fixtures.ts`
- Создать: `tests/e2e/scenarios.json`
- Изменить: `frontend/package.json` только если browser test dependency/script отсутствует

- [ ] Проверить существующие frontend browser-test dependencies и server entrypoint. Переиспользовать найденный runner; если его нет — добавить `@playwright/test` как development-only dependency и команду `pnpm --dir frontend exec playwright test --config ../tests/e2e/playwright.config.ts`.
- [ ] Создать отдельный `tests/e2e/compose.yaml` с PostgreSQL и S3-compatible disposable service; использовать уникальный Compose project на прогон, loopback-only ports, ephemeral volume names и credentials, сгенерированные в runtime. `postgres-init.sh` создаёт только нужные QA roles/database. Не использовать и не менять пользовательский root `compose.yaml`.
- [ ] В runner запускать только этот Compose project и завершать только его exact project name; прерывание также удаляет лишь созданные им volumes. Если Docker/Compose отсутствует — явный prerequisite blocker. Применить runtime config variables, прочитанные из текущих composition specs, без скрытых defaults.
- [ ] В Playwright `webServer` указывать только фактические команды запуска Admin и product HTTP composition из репозитория. Не принимать shell command из environment и не выполнять произвольную команду пользователя. Если Website/Router composition нельзя запустить, добавить конкретные blocker IDs и прекратить этот сценарий до любого write request.
- [ ] Добавить `feature-fixtures.ts` для создания в памяти детерминированных валидного и повреждённого ZIP. Не хранить реальные архивы с недоверенным содержимым и не скачивать fixtures из сети.
- [ ] Создавать UUID namespace на каждый прогон и вести ownership list только созданных fixture ресурсов. Teardown удаляет только перечисленное; при failure сохранять trace/screenshot/logs без секретов.
- [ ] До UI действий выполнить read-only health/identity probe и проверить, что API сообщает о test composition данного checkout. Несовпадение немедленно прекращает прогон до write-запроса.
- [ ] В prerequisite reporting различать отсутствие browser/server/DB/storage и assertion failure. Подтвердить, что отсутствующий prerequisite даёт явный `BLOCKED` и non-zero, а не pass или молчаливый skip.

### Task 2: Проверить создание страницы, импорт feature и durable build workflow

**Файлы:**
- Создать: `tests/e2e/specs/website-router-publication.spec.ts`
- Изменить: `tests/e2e/scenarios.json`

- [ ] Добавить `E2E-WEB-001` для авторизованного создания страницы в Admin: проверить server-generated page UUID, один root snapshot и UI loading/empty/success states; group level `0/1` не может выполнить write.
- [ ] Добавить `E2E-WEB-002` для валидного feature ZIP: сначала загрузить bytes через File API/Loader и получить `source_file_id`, затем вызвать Website import только с этим ID и idempotency key. Проверить `worker.archive` receipt и durable task state, дождаться terminal success, затем проверить feature record и отсутствие import secrets в public context.
- [ ] Добавить `E2E-WEB-003` для invalid ZIP/manifest: workflow завершается безопасной ошибкой, partial feature record отсутствует, Admin UI сохраняет прежнее состояние страницы.
- [ ] Добавить `E2E-WEB-004` для locale-bound prerender: ожидать переходы workflow state; проверить artifact metadata и `static-page-artifact/v1` document/assets через поддерживаемый API/read path.
- [ ] Проверить для workflow start/retry прямой group level `2` на каждом действии, безопасную summary visibility для level `1`, недоступность payload/result/diagnostics и запрет retry для control/join tasks. Отсутствующий UI/API записать blocker-ом с точным spec clause.

### Task 3: Проверить явный Router binding, CAS и сбои публикации

**Файлы:**
- Изменить: `tests/e2e/specs/website-router-publication.spec.ts`, `tests/e2e/scenarios.json`

- [ ] Добавить `E2E-PUB-001`: после успешного Website artifact build подтвердить, что он ещё не public; отдельно выполнить поддерживаемую Router settings operation с revision/CAS и проверить route response.
- [ ] Добавить `E2E-PUB-002`: устроить stale Router revision race; устаревший binding получает conflict, active mapping остаётся прежним, public response не меняется.
- [ ] Добавить `E2E-PUB-003`: после активации прежнего artifact вызвать Website worker/build failure; неуспешный output не активируется, прежняя route response сохраняется. После retry только свежий успешный результат доступен для явного activation.
- [ ] Добавить `E2E-PUB-004`: настроить несколько route/locale targets и один отказ; успешные CAS activation сохраняются, failed targets оставляют прежние artifacts, retry идемпотентен, stale results отбрасываются.
- [ ] Добавить `E2E-RETIRE-001`: retire страницы durably отзывает все зарегистрированные route targets; после retry публичная выдача отсутствует, но snapshots, Content drafts/publications/history, Forms и Core files сохраняются.
- [ ] Для каждого случая сохранить API responses, durable task state, mapping revision и public body/header evidence. Не вызывать Website из Router или наоборот напрямую и не добавлять неявный binding в fixture.

### Task 4: Проверить browser delivery, locale preference и public security

**Файлы:**
- Создать: `tests/e2e/specs/router-locale-delivery.spec.ts`
- Изменить: `tests/e2e/scenarios.json`

- [ ] Добавить `E2E-ROUTER-001`: открыть exact configured host/path; проверить server-rendered document, hydration-safe markup, asset, headers и 404 неизвестного route без implicit fallback.
- [ ] Добавить `E2E-ROUTER-002`: через реальный browser проверить поддерживаемую locale cookie, `Accept-Language` quality/order и RFC 4647 Lookup. Invalid/unsupported cookie игнорируется, wildcard выбирает только default, locale не берётся из path/query, содержимое другой locale не появляется.
- [ ] Добавить `E2E-ROUTER-003`: проверить, что preference endpoint принимает только same-origin return path, устанавливает host-only cookie с настроенными lifetime/Secure policy и отвергает open-redirect input без изменения route state.
- [ ] Добавить `E2E-ROUTER-004`: запросить exact configured public file и artifact asset методами GET/HEAD; проверить body/header parity, отказ unknown/visitor-selected IDs, запрещённые MIME/HTML assets и отсутствие доступа на основании FileSet membership.
- [ ] Проверить в browser только UI workflows, описанные Site Editor/Router contracts: keyboard navigation, видимый focus, responsive controls, destructive confirmation и unsaved-change protection. Отдельно привязать к E2E IDs; отсутствующий UI не считать pass.

### Task 5: Зафиксировать E2E red baseline и устранить блокеры/провалы

**Файлы:**
- Изменить: E2E fixtures/specs; runtime composition files только при наличии нормативных blockers
- Изменить: `tests/e2e/scenarios.json`, `tests/ACCEPTANCE-MATRIX.md`, `docs/QA_REPORT.md`

- [ ] После сборки обоих модульных red-наборов запустить весь E2E suite и записать статусы каждого `E2E-*` до правок product code. Зафиксировать воспроизводимые blockers вместо отложения red baseline из-за незавершённых модулей.
- [ ] Если blocker — отсутствие production factory/entrypoint, реализовать только composition, требуемую уже утверждённой архитектурой `main/` и имеющимися module presentation APIs; не делать test-only product handler и не обходить runtime bus. Если контракт composition не задан спецификацией — остановиться и запросить решение, не угадывать.
- [ ] Исправлять поведение по утверждённым спецификациям, сохраняя красные assertions. После каждого fix повторять browser case и затронутые Website/Router unit, contract и integration selectors.
- [ ] Выполнить `pnpm --dir frontend exec playwright test --config ../tests/e2e/playwright.config.ts` только против запущенного suite disposable stack; затем запустить Admin typecheck/test/build и проверки изменённых Go modules.
- [ ] Обновить Acceptance Matrix стабильными `E2E-*` IDs и QA report фактическими browser/runtime versions, командами, результатами, failure artifacts и остаточными blockers. Не заявлять browser/UI coverage по одним Go tests.
