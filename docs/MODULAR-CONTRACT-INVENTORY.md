# Инвентаризация контрактов для модульного перехода

Статус: начальная source-инвентаризация, не полный контрактный набор и не свидетельство проверки. Эталон — `c710fae` на `dev_v2`. Дальнейший перенос должен добавить точные примеры запросов и ответов и исполняемые сравнения.

Это инвентаризация внешнего поведения `legacy/`, а не требования к внутренней модели нового Core. Целевая модель Core — [SPEC-core-v2.md](../spec/SPEC-core-v2.md): она исключает роли, владельцев и версии файлов, индивидуальные файловые права, публичное чтение Core и Core-реестр ссылок. По решению пользователя поле `role` и старая матрица ролей удаляются из нового API и админки: это исключение из совместимости, а не требование воспроизвести роли в адаптере. Остальные исторические контракты, Sitepack, публичную выдачу и редакционные сценарии нужно воспроизвести в engines/presentation. Паритет пока не подтверждён.

| Граница | Текущий источник истины | Что необходимо сохранить |
| --- | --- | --- |
| Admin и public HTTP | `presentation/http/routes.go` и handlers, включая отдельную регистрацию файловых маршрутов | Методы, пути, ответы и ошибки, лимиты, CORS, публичные и административные listeners; исключение — удаляемые `role` и старая матрица ролей |
| Идентичности и ключи | `features/auth`, `presentation/http/middleware`, `frontend/admin/src/lib/permissions.ts` | Отказ до изменения данных и безопасный жизненный цикл ключей; legacy `role` и матрица четырёх ролей явно исключены из нового контракта |
| Снапшоты и ветки | `spec/SPEC-branches-snapshots.md`, `spec/SPEC-route-targets.md` | Дерево, назначение на настроенные ветки, публикация, откат и независимые маршруты |
| Контент | `spec/SPEC-data-model.md` | Snapshot-scoped flat models, many/singleton, drafts, публикация, история и revisions |
| Импорт и экспорт | `spec/SPEC-sitepack.md` | Текущий поддерживаемый Sitepack с моделью, содержимым, сборками и необходимыми media; пользователи, токены, заявки и назначения веток в Sitepack не входят |
| Публичные фичи | `spec/SPEC-feature-runtime.md`, `spec/SPEC-public-server-rendering.md` | Manifest, `legacy-react-v1`, `static-module-v1`, `static-html-v1`, markup и lifecycle |
| Файлы и медиа | `spec/SPEC-files.md` и файловые handlers | Владелец, public/private, доступ сервисов, потоковые операции, Range и безопасное удаление |
| Админка | `frontend/admin/src/app/router.tsx`, `docs/FRONTEND_ARCHITECTURE.md` | Маршруты, workflow, защиту несохранённых данных и destructive confirmations |
| Конфигурация | `spec/SPEC-configuration.md`, `.env.example` | Явные настройки, секреты и политики; в новой реализации YAML loader является package приложения, а не отдельным модулем |
| Application / Composition | `go.work`, `cmd/zubrium` | Статическую регистрацию модулей и запуск единственного Core Atlas migration set; YAML только настраивает скомпонованные модули |
| Presentation | `presentation/http/routes.go`, engine handlers и `docs/API.md` | Router, File API, Admin API и Admin UI; File API вызывает Loader Engine contract, остальные адаптеры — типизированные контракты соответствующих engines |
| Данные engines | `core.module_data`, [SPEC-module-data.md](../spec/SPEC-module-data.md) | Все persistent данные движков в JSONB; namespace/scope, typed selectors/indexes, TTL, CAS и атомарный batch через scoped Core API |



Целевая инфраструктурная граница: одна PostgreSQL-база и схема `core`; только Core владеет DSN и подключением. Engine не имеет собственной DB, схемы, SQL-доступа или Atlas migration set. Все persistent данные engines хранятся в одной таблице `core.module_data` как JSONB-документы; модуль получает доступ только к собственному `module_id` через scoped typed Go API. Loader хранит upload-сессии/части/idempotency/cleanup entries в своём namespace этой таблицы, а байты staging передаёт через Core API. Core-модель файлов, групп, identities и наборов остаётся нормализованной Core-таблицами; File API вызывает Loader, который делегирует Core.

Карта относится к существующей системе и целевому переходу. YAML-конфигурация остаётся отдельной ответственностью в коде composition root; отдельные `config/*` Go modules удалены. Presentation владеет всеми точками входа и Admin UI. Действующий Sitepack не переносит пользователей или сервисные credentials; их перенос нельзя считать выполненным через импорт Sitepack.
