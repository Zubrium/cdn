# Активный план Website Engine

## Состояние

Внешний HTTP API использует пути `/api/...` без версионного префикса. OpenAPI остаётся версии 3.1; `info.version` — метаданные документа. Внутренние версии данных, схем, feature builds, component contracts, snapshots и миграций сохраняются.

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
- [ ] Завершить worker isolation, resource limits, cancellation/retry, cleanup и регистрацию в production profiles для Archive, Media и Prerender; никакие worker-to-engine вызовы не допускаются.
- [ ] Выполнить согласованные acceptance-сценарии каждого модуля и определить denominator до расчёта целевого функционального покрытия. Описания сценариев и statement coverage не заменяют исполняемую acceptance-проверку. Хост-side контракты `static-lifecycle` закрыты WEB-023..029; browser acceptance и сценарии WEB-015/WEB-016 остаются открытыми.
- [ ] После implementation sweep выполнить Go/frontend checks, disposable PostgreSQL/S3 tests, OpenAPI parity и применимую browser QA. Записать команды, результаты и ограничения; не менять рабочие данные.

## Порядок проверки

Сначала завершается применимая реализация и документация. Затем выполняются `make test`, `make vet`, `make build`, `make build-full` и из `frontend/` — `pnpm typecheck`, `pnpm lint`, `pnpm test`, `pnpm build`. Интеграционные проверки выполняются только на одноразовых ресурсах с точным учётом созданных идентификаторов и безопасной очисткой.
