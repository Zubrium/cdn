# Durable build workflow Website — согласованный контракт

Статус: контракт согласован пользователем 2026-11-22; реализация durable build подключена в `main`-композицию по TDD. Документ фиксирует согласованное поведение и фактическую границу: root handler и reconciliation работают, реальный prerender sandbox и Router-активация по-прежнему не подключены.

## Контекст и доказательство нарушения

- `spec/workers/SPEC-workers.md` требует, чтобы Website build оставался fail-closed, пока владелец workflow не может безопасно получить результат дочерней задачи.
- `WorkflowClient.Get` возвращает только `Workflow.Result` root task. `WorkflowControl` (Admin) намеренно скрывает task payload/result.
- Публикация `static-page-artifact/v1` создаётся дочерним `worker.prerender/render-page/v1`; без owner-scoped доступа к её result Website не может зафиксировать `BuildJob.ArtifactManifestFileID` и перевести job в `ready`.
- Пользователь разрешил минимальное изменение замороженного Core для устранения этого нарушения с обязательным regression-тестом.

## Решения

### 1. Core capability

- Добавляется owner-scoped метод чтения результата одной task в рамках workflow, принадлежащего вызвавшему компоненту: `WorkflowClient.GetTaskResult(ctx, workflowID, taskID)`.
- Владелец (owner engine ID, instance owner, config epoch) передаётся только из сконструированного `WorkflowClient`, никогда из запроса Admin или посетителя.
- Новый repository port узкий: возвращает нейтральный DTO (`TaskID`, `WorkflowID`, `Worker`, `State`, `Result`, `ErrorCode`) без engine-specific полей.
- Результат ограничен существующим лимитом task result; превышение — typed error. Admin `WorkflowControl` не получает доступа к результатам.
- Regression-тесты покрывают: success, чужой engine/instance/epoch, task из другого workflow, отсутствующий task, oversized result, отменённый context.

### 2. Persistence Website

- Job хранится в уже объявленном namespace `website/builds`, scope type `job`.
- Ключи: `build/<build_id>` и `idempotency/<sha256(snapshot_id + locale + pinned_input_digest + caller_idempotency_key)>`.
- Payload version отдельная от snapshot/feature record; index содержит `record_type`, `state`, `snapshot_id`.
- Переходы: `queued → resolving → prerendering → packaging → verifying → ready`; любая стадия может перейти в `failed` с machine-readable `error_code`.
- Каждый переход — CAS по точной revision. Concurrent stale transition — `ErrConflict`, без перезаписи состояния.
- Повтор того же idempotency key с тем же нормализованным input возвращает тот же job; другой input — conflict.

### 3. Workflow graph

- Root: зарегистрированная static-операция `engine.website/build-page/v1`; handler живёт в `engines/website/workflow` и регистрируется только `main`-композицией full-профиля. Operation ID и версию задаёт trusted `websiteapp.Config`, не HTTP-запрос.
- Root payload содержит immutable pin'нутые ревизии/диджесты и locale. Handler проверяет strict JSON, identity задачи и контракт, после чего детерминированно строит `render-plan/v1` с `input_digest`. Запрещённые поля (route, URL, branch, record values, команды, path, secret) в DTO отсутствуют.
- Child: `worker.prerender/render-page/v1` с payload `{idempotency_key, plan, plan_digest, input_digest}`. Core создаёт `Task.ID` только при коммите root result, поэтому root публикует стабильный `prerender_emission_key = prerender:<build_id>`, а Website разрешает его через task graph доверенного `WorkflowClient` и затем закрепляет task ID в записи build. Результат child читается через owner-scoped `GetTaskResult`; root result не содержит engine-specific данных.
- Prerender остаётся изолированным worker; JavaScript `static-lifecycle` исполняется только там. Website не запускает браузер и не держит credential.

### 4. Входные данные build

- `snapshot_id`, `locale`, `locale_catalog_revision`, content publication revision, dependency lock digest, snapshot revision, вычисленный `input_digest`.
- Запрещены: route, URL, branch, visitor-selected target, record values, произвольные command/executable, S3 paths, секреты.
- Stale input (любая из закреплённых ревизий изменилась) отклоняется до запуска prerender и не активирует результат.

### 5. Artifact и активация

- Артефакт — `static-page-artifact/v1` с одним документом и проверенными assets; валидация до перехода в `ready`.
- `ArtifactManifestFileID` фиксируется в job только после успешной проверки checksum/content type/size.
- Router activation — отдельный outer orchestration с per-target `(route, locale)` CAS; Website самой активации не выполняет.
- Ошибка, timeout, cancellation или несовпадение digest сохраняют предыдущий активный artifact и не оставляют частично готовый job.

### 6. Права и fail-closed

- `BuildSnapshot`: website write grant + coordinator write grant; `GetBuild`: website read grant.
- Service credentials и session создаются trusted composition, не из request context.
- Если workflow/files/capabilities не инъектированы профилем, `BuildSnapshot`/`GetBuild` остаются fail-closed с typed `ErrUnavailable`.
- Вне full-профиля (`router`, `devfixtures`, Profile Core) root handler не регистрируется, `BuildWorkflow` не инъектируется, а prerender остаётся fail-closed.

### 7. Фактическая композиция

- `main/internal/composition/website_component.go` создаёт `Runtime.NewWorkflowClient("engine.website")`, owner-scoped reader и `NewWorkflowControl`, затем инъектирует их в `websiteapp.Service` вместе с `BuildOperationID = build-page` и `BuildContractVersion = 1`.
- `main/internal/composition/worker_handlers_full.go` регистрирует `engine.website/build-page/v1` и fail-closed handlers для `worker.archive`, `worker.media`, `worker.prerender`.
- Locale catalog resolver получает только доверенный service context Content через `contentEngineComponent.Activate`; контекст посетителя не передаётся. Для locale вне зарегистрированного каталога build fail-closed.
- `websiteBuildEnabled` гарантирует, что не-full сборки не получают durable workflow capability.

## Acceptance-связь

- `WEB-020` — locale-bound render-plan, sandbox/network policy, stale result, artifact verification (disposable sandbox).
- `WEB-021` — rebuild fan-out, независимые Router target CAS, retry и stale protection (disposable orchestration).
- `WEB-014`, `WEB-026` — валидация artifact manifest и trusted file metadata.
- `WEB-010` — отсутствие route/URL/branch/record values в structural/build DTO.
- Новый Core regression test — владелец видит result child task, посторонний components/networks — нет.

## Ограничения, которые не снимаются этим решением

- Реальный OS-supervised Chromium/network sandbox должен быть предоставлен static composition; в текущем checkout `worker.prerender/render-page/v1` остаётся fail-closed, поэтому сквозной build не доходит до готового artifact.
- Disposable PostgreSQL/S3 обязательны для integration-приёмки; unit/contract checks их не заменяют. Текущие `WEB-020`/`WEB-021` остаются `blocked` до появления изолированного sandbox и outer-orchestration Router CAS.
- Core API расширяется только в описанной owner-scoped части; общий доступ к task payload/results не открывается. SQL-путь `GetTaskResult` проверен static/unit, но не реальным integration-прогоном в этом проходе.
