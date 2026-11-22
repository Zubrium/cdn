# Активный план Website Engine

## Состояние

Внешний HTTP API использует пути `/api/...` без версионного префикса. OpenAPI остаётся версии 3.1; `info.version` — метаданные документа. Внутренние версии данных, схем, feature builds, component contracts, snapshots и миграций сохраняются.

Композиция `main` собирает компоненты профиля только через запечатанный runtime bus со статическим allowlist; typed workflow-операции, чтение опубликованного Content для публичного runtime и scoped streams на каждый вызов регистрируются только в нём. Full-профиль содержит живой Content engine с доверенным провайдером проекций и зарегистрированный `public.content-runtime`; worker handlers компилируются и выбираются профилем. Durable Website build подключён в full-профиле: root `engine.website/build-page/v1` эмитит `worker.prerender/render-page/v1`, а результат child читается owner-scoped Core capability; реальный prerender sandbox и Router activation остаются fail-closed/неподключёнными. Manifest-pinned media derivative подключён в full-профиле: root `engine.website/media-derivative/v1` эмитит `worker.media/derivative/v2`, а изолированный runner использует Docker/Linux kernel enforcement только при trusted startup-конфигурации. Линтер границ в `tests/linter` проверяет импорты, согласованность профилей и сгенерированных файлов, а также обход шины.

Integration-прогон на disposable PostgreSQL/S3 прошёл: `CORE-001`, `WEB-018` и `WEB-019` исполнены реально, а не через `t.Skip`. Browser QA Admin выполнен в отдельном checkout с изолированным full-профилем, disposable PostgreSQL и `moto`; проверены 12 экранов, responsive 360–1920 px, темы, клавиатурный фокус, диалоги и публичный listener. Найденные дефекты — пустой фильтр `ListSnapshots`, глобальная Core revision в Website-декодерах, пустой metadata новой страницы, mobile overflow и SSE same-origin — исправлены с regression-тестами. Ограничения и точные команды зафиксированы в [QA report](QA_REPORT.md); оставшиеся blocked-сценарии не трактуются как pass.

Целевой контракт описан в модульных спецификациях и OpenAPI. Ни этот план, ни наличие интерфейсов/заглушек не доказывают готовность реализации. Чекбоксы закрываются только после выполнения критериев и записи результатов в [QA report](QA_REPORT.md).

## Незавершённые работы

- [ ] Перевести все runtime-вызовы между зарегистрированными компонентами на bus `main`: статический operation allowlist профиля, общий JSON envelope, typed owner adapters, Core capability operations, HTTP ingress и Router public operations; файловые потоки оставить scoped stream capability. Сохранить Coordinator как durable execution и добавить boundary/acceptance tests.
- [ ] Завершить production composition Website, Content и Forms, включая принадлежащие им workers и API; профиль отказывает при выборе отсутствующей реализации.
- [ ] Довести Website page/template/editor workflows до [Website API](../spec/engines/website/SPEC-website-engine.md) и [Site Editor contract](../spec/engines/website/SPEC-site-editor-workspace.md): system-scoped pages с UUID, готовый root snapshot, независимые page snapshot trees, template CAS/materialization, feature import, typed bindings и fail-closed validation.
- [ ] Реализовать Content contract: system-wide model records/drafts/publication/history, page-snapshot selections/order, атомарные операции в пределах разрешённой Core scope и безопасные конфликты/cleanup.
- [ ] Реализовать публикацию публичных артефактов: deterministic `static-page-artifact/v1`, durable fan-out при изменении общих данных, stale-result discard, per-route CAS, retry и сохранение предыдущей публикации при частичном отказе.
- [ ] Завершить Forms workflow: schema revisions, visitor sessions, upload attachments, immutable submissions, status policy, CSV export limits, отмена/expiry и подтверждённое удаление с очисткой файлов.
- [ ] Завершить Router/Loader production acceptance: exact route binding и artifact delivery, public upload staging/idempotency/authorization, permission revocation, process-per-branch и readiness. Публичный listener доступен только через внешний proxy, который настраивает deployer.
- [ ] Завершить Admin workflows по актуальным группам и контрактам: Website, Content, Forms, Router, access management, notifications и recovery. Сохранить подтверждения destructive actions, revision checks и защиту несохранённых изменений.
- [ ] Завершить worker isolation, resource limits, cancellation/retry, cleanup и регистрацию в production profiles для Archive и Prerender; media derivative (`WEB-019`) исполнен в Docker/Linux sandbox с kernel-enforced ограничениями. Никакие worker-to-engine вызовы не допускаются.
- [ ] Выполнить согласованные acceptance-сценарии каждого модуля и определить denominator до расчёта целевого функционального покрытия. Описания сценариев и statement coverage не заменяют исполняемую acceptance-проверку. Хост-side контракты `static-lifecycle` закрыты WEB-023..029, `WEB-012`, `WEB-018`, `WEB-019` и `CORE-001` исполнены на disposable-инфраструктуре; `WEB-020`, `WEB-021`, `WEB-022` остаются blocked.
- [ ] После implementation sweep выполнить Go/frontend checks, disposable PostgreSQL/S3 tests, OpenAPI parity и применимую browser QA. Go/frontend/disposable/browser checks текущего прохода пройдены и записаны в [QA report](QA_REPORT.md); полный live E2E publication/retirement и release checks остаются открытыми.

## Порядок проверки

Сначала завершается применимая реализация и документация. Затем выполняются `make test`, `make vet`, `make build`, `make build-full` и из `frontend/` — `pnpm typecheck`, `pnpm lint`, `pnpm test`, `pnpm build`. Интеграционные проверки выполняются только на одноразовых ресурсах с точным учётом созданных идентификаторов и безопасной очисткой.
