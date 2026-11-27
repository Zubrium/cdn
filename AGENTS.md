# Website Engine — инструкции агентам

## Архитектура и приоритеты

`go.work` объединяет Core (`core/`), engines (`engines/`), Presentation (`presentation/`), workers (`workers/`), независимый модуль контрактов (`pkg/`) и единственную композицию продукта (`main/`). Только `main/` содержит product executable и статическую composition root. Engines, workers и Presentation остаются отдельно собираемыми Go-модулями. Источник целевой архитектуры: `spec/core/SPEC-core-architecture.md`, `spec/architecture/SPEC-modular-platform.md`, затем спецификации соответствующих модулей и OpenAPI.

Соблюдай существующие границы и не переноси слой/ответственность без требования задачи или согласованной спецификации. Все runtime-вызовы между зарегистрированными компонентами проходят через process-local bus в `main` с общим JSON envelope и статическим operation allowlist выбранного profile; HTTP остаётся ingress и не принимает operation ID от клиента. Большие file bodies передаются только через scoped stream capability. Вызовы пакетов и слоёв внутри одного модуля остаются прямыми; durable workflows остаются в Core Coordinator. Не добавляй заглушки, непроверенные fallback-механизмы, зависимости или обратную совместимость без конкретного требования. Сохраняй пользовательские изменения и данные.

## Core и persistence

- Одна PostgreSQL database соответствует одной логической системе. Core — единственный владелец PostgreSQL, S3 и общего файлового cache; потребители используют только `core/presentation` capabilities.
- Core содержит `domain`, `application`, `infrastructure`, `presentation` непосредственно в `core/`; не создавай `core/internal`. Политики и use cases относятся к application, SQL/S3/cache адаптеры — к infrastructure.
- Только Core владеет Atlas migrations. Engines хранят versioned JSON records в scoped `engine_data.records`, не имеют DSN, SQL, собственных схем или миграций. Module identity задаёт composition, не запрос; доступ между namespaces запрещён.
- Пользователь — единая сущность без `kind` и роли. Уровни доступа к статическим YAML-группам: `0` deny, `1` read, `2` read/write. Управление пользователями и credentials требует отдельного `can_manage_administrators`; изменение grant также требует уровня `2` в соответствующей группе.
- Файлы Core неизменяемы и не имеют владельцев или публичного флага. FileSets — поисковые индексы и не дают доступа к bytes. Loader владеет upload workflow; File API вызывает Loader, а Core выполняет авторизацию и операции хранения.
- Core sessions привязаны к одному credential и хранят digest токена. Cookies, CSRF, Origin и browser transport принадлежат HTTP Presentation. Браузер использует HttpOnly cookie и отдельный CSRF header; отклоняй неоднозначную cookie-plus-Bearer авторизацию. Не сохраняй credentials/session tokens в `localStorage` и не возвращай их в обычных ответах API.
- Core считается замороженным после согласованного credential-bound session контракта. Меняй его только для исправления доказанного нарушения этого контракта и добавляй regression test.
- Внутренние версии payload/schema, upcasters, feature builds, component contracts, artifacts, snapshots, CAS/revisions и Atlas history являются частью системы. Не удаляй и не переименовывай их при чистке внешних HTTP-путей.

## HTTP и безопасность

- Внешние продуктовые маршруты не имеют версионного префикса: `/api/...`. Не добавляй версионные маршруты или compatibility aliases.
- OpenAPI сохраняет `openapi: 3.1.0` и обязательный `info.version`; `info.version` не задаёт версию URL.
- Каждый внешний Presentation-модуль владеет OpenAPI-контрактом в `spec/presentation/openapi/` и своим HTTP adapter. Изменения синхронно отражаются в маршрутах, OpenAPI, Admin TypeScript-клиенте, тестах и acceptance matrix.
- Выровняй схемы ошибок, валидацию, ограничения размера, idempotency/revision headers и security requirements между контрактом и реализацией. Не ослабляй проверки авторизации и route guards.
- Router публикует только явно настроенные маршруты. Публичные операции должны быть скомпилированы в профиль и зарегистрированы типизированным контрактом. Не принимай от посетителя произвольные handlers, URLs, Core IDs, group IDs или FileSet IDs.
- Публичный file GET привязан к одному настроенному `file_id` и exact path. Публичная загрузка использует только зарегистрированную Loader-backed operation, серверные destination/limits и отдельный путь staging; FileSet не делает файл публичным. Router не реализует rate limiting; публичный listener должен быть закрыт за внешним proxy, настройки которого относятся к deployer.
- Cookie policy, CSRF, точный Origin, Secure/SameSite/HttpOnly и очистка cookie реализуются только в Presentation. Публичные ответы по умолчанию `no-store`; отсутствующий маршрут даёт 404, fallback должен быть явно сконфигурирован.
- Секреты передаются через environment variables или secret files, никогда не помещаются в примеры конфигураций как реальные значения. Операционные лимиты задаются конфигурацией; не добавляй скрытые runtime defaults.

## Продуктовые контракты

- Router обслуживает один публичный listener по адресу `public_listen_address` из startup-конфигурации. Все настроенные hosts используют один набор маршрутов. Изменение listener и host policy требует YAML и полного restart.
- Website snapshots образуют независимые деревья по страницам. Структурные изменения создают child snapshot; route-to-artifact mapping принадлежит Router. Защищай корень и непосредственные ссылки дерева согласно Website snapshot contract.
- `/site` управляет страницами, маршрутами, размещениями, аргументами фич, flat data model и структурными bindings; `/site` не редактирует record values. `/content` владеет drafts/publication и редакционным выбором/порядком записей. Загрузка файлов остаётся в `/media`.
- Model entities имеют cardinality `many` или `singleton`. Новый singleton получает одну пустую draft-запись и не публикуется автоматически. Очистка сохраняет record ID; сервер отклоняет изменения cardinality, теряющие records. Typed singleton `record`/`scalar` разрешается автоматически, `collection` остаётся массивом из нуля или одной записи.
- Feature ZIP содержит `manifest.json`, описывающий metadata, site-editor arguments, typed `dataInputs`, form submission contract, design tokens и runtime metadata. Используй только согласованный target lifecycle ABI из `spec/engines/website/features/SPEC-feature-runtime.md`; не угадывай discriminator/signatures и не запускай несовместимый ABI.
- Сохраняй семантическую разметку, поведение и доступность feature UI. Не добавляй общие page/slot wrappers, вложенные placements или вложенный `<main>` без target-контракта. Server-rendered markup должен быть hydratable целевым runtime.
- Forms submissions после принятия неизменяемы; удаление подтверждается и очищает все attachments/memberships согласно Forms contract. Публичные form sessions, schema changes, expiry и cleanup следуют `SPEC-forms-engine.md`.
- Design tokens редактируются графически и с доступными light/dark значениями. Не требуй от оператора CSS, JSON, имён переменных или выражений; не встраивай live site preview в design editor.

## Frontend и шаблоны

- Admin SPA: React 18 + TypeScript в `frontend/admin/src`; маршруты — `app/router.tsx`, общий shell — `app/layouts/AppShell.tsx`, API — `api.ts`, типы — `types.ts`. Используй текущие React Router, TanStack Query, React Hook Form, Zod, Radix, Tailwind и Lucide patterns.
- Проверки доступа используют direct group levels и отдельное право управления администраторами; не используй `principal.role`. Сохраняй destructive confirmations и защиту несохранённых изменений.
- Используй семантические HSL tokens `frontend/ui/styles/admin.css`, соответствующие light/dark значения, клавиатурную доступность, видимый focus, reduced motion и удобные mobile targets. Не разделяй стили Admin и публичного runtime.
- Запросы и состояние API держи в `api.ts`/domain hooks, а не дублируй fetch в страницах. Для пользовательских workflows показывай loading, empty, error и saving states.
- HTML и `html/template` actions хранятся в `.tmpl` под пакетом-владельцем. Динамические значения проходят contextual escaping; пользовательские данные нельзя приводить к `template.HTML`, `template.JS` или аналогам.

## Тестирование и QA

- Веди активные задачи в `docs/TODO-admin-snapshots-content-files.md`, acceptance-сценарии — в `tests/ACCEPTANCE-MATRIX.md`, результаты проверок — в `docs/QA_REPORT.md`. Не храни в этих файлах хронологический журнал завершённых проходов.
- Для существенных изменений сначала согласуй поведение, инварианты, API/data contracts, permissions, отказные/конкурентные случаи и acceptance tests. Для недоопределённого публичного контракта сначала уточни контракт.
- Не отмечай TODO выполненным до прохождения применимых проверок и фиксации evidence. Не заявляй pass для отложенной, не запущенной или ограниченной проверкой.
- Для end-to-end Website implementation sweep выполняй реализацию и документацию прежде финального набора typecheck/lint/build/unit/integration/browser/E2E/release checks; затем выполняй запланированную проверку, исправляй ошибки и повторяй только нужные проверки. Существующие результаты сохраняй для неизменённого кода.
- Не отправляй write-запросы в работающий API при visual QA, если это не изолированная среда с disposable data. До destructive QA фиксируй точные имена/порты/БД/buckets/volumes/paths и проверяй отсутствие активных пользователей. Удаляй только ресурсы, созданные текущим QA; не используй широкие очистки и не трогай неопределённые данные или рабочий Vitasil stack.
- Go-модули проверяй отдельно командами `go test ./...`, `go vet ./...` и `go build ./...` из каталога каждого модуля workspace; в корне нет Makefile. Admin проверяй из `frontend/` командами `pnpm --filter @website-engine/admin typecheck`, `pnpm --filter @website-engine/admin test`, `pnpm --filter @website-engine/admin build` и ESLint для `admin/`. Интеграционные acceptance с PostgreSQL/S3 требуют отдельных disposable ресурсов. Acceptance threshold по исполняемым input/result scenarios не подменяется statement coverage.
- Browser QA для Admin проверяет, что API target — этот checkout, и не трактует отказ тестовой среды как успешную проверку UI. Для feature manifests, content, snapshots, tokens и runtime проверяй применимые реальные features в изолированной среде.

## Git и данные

- Перед любыми командами, которые могут затронуть пользовательские изменения, проверяй `git status`. Не запускай `reset --hard`, `clean`, массовый restore/checkout и не удаляй существующие данные.
- Самостоятельно фиксируй завершённые проверенные результаты отдельными коммитами и пушами в текущую ветку. При коммите проверяй состав staged changes, не включай секреты и не смешивай несвязанные волны.
- Не перемещай и не удаляй `.env`, dependency caches и другие пользовательские данные при реорганизации исходников.
