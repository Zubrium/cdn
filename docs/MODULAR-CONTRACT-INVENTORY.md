# Инвентаризация контрактов для модульного перехода

Статус: начальная source-инвентаризация, не полный контрактный набор и не свидетельство проверки. Эталон — `c710fae` на `dev_v2`. Дальнейший перенос должен добавить точные примеры запросов и ответов и исполняемые сравнения.

Это инвентаризация внешнего поведения `legacy/`, а не требования к внутренней модели нового Core. Целевая модель Core — [SPEC-core-v2.md](SPEC-core-v2.md): она исключает роли, владельцев и версии файлов, индивидуальные файловые права, публичное чтение Core и Core-реестр ссылок. По решению пользователя поле `role` и старая матрица ролей удаляются из нового API и админки: это исключение из совместимости, а не требование воспроизвести роли в адаптере. Остальные исторические контракты, Sitepack, публичную выдачу и редакционные сценарии нужно воспроизвести в engines/presentation. Паритет пока не подтверждён.

| Граница | Текущий источник истины | Что необходимо сохранить |
| --- | --- | --- |
| Admin и public HTTP | `presentation/http/routes.go` и handlers, включая отдельную регистрацию файловых маршрутов | Методы, пути, ответы и ошибки, лимиты, CORS, публичные и административные listeners; исключение — удаляемые `role` и старая матрица ролей |
| Идентичности и ключи | `features/auth`, `presentation/http/middleware`, `frontend/admin/src/lib/permissions.ts` | Отказ до изменения данных и безопасный жизненный цикл ключей; legacy `role` и матрица четырёх ролей явно исключены из нового контракта |
| Снапшоты и ветки | `docs/SPEC-branches-snapshots.md`, `docs/SPEC-route-targets.md` | Дерево, назначение на настроенные ветки, публикация, откат и независимые маршруты |
| Контент | `docs/SPEC-data-model.md` | Snapshot-scoped flat models, many/singleton, drafts, публикация, история и revisions |
| Импорт и экспорт | `docs/SPEC-sitepack.md` | Текущий поддерживаемый Sitepack с моделью, содержимым, сборками и необходимыми media; пользователи, токены, заявки и назначения веток в Sitepack не входят |
| Публичные фичи | `docs/SPEC-feature-runtime.md`, `docs/SPEC-public-server-rendering.md` | Manifest, `legacy-react-v1`, `static-module-v1`, `static-html-v1`, markup и lifecycle |
| Файлы и медиа | `docs/SPEC-files.md` и файловые handlers | Владелец, public/private, доступ сервисов, потоковые операции, Range и безопасное удаление |
| Админка | `frontend/admin/src/app/router.tsx`, `docs/FRONTEND_ARCHITECTURE.md` | Маршруты, workflow, защиту несохранённых данных и destructive confirmations |
| Конфигурация | `docs/SPEC-configuration.md`, `.env.example` | Явные настройки, секреты и политики; в новой реализации YAML loader является package приложения, а не отдельным модулем |
| Application / Composition | `go.work`, `cmd/zubrium` | Статическую регистрацию модулей и последовательный запуск их изолированных Atlas migrations; YAML только настраивает скомпонованные модули |
| Presentation | `presentation/http/routes.go`, engine handlers и `docs/API.md` | Router, File API, Admin API и Admin UI; File API вызывает Loader Engine contract, остальные адаптеры — типизированные контракты соответствующих engines |



Целевая инфраструктурная граница: Core владеет только PostgreSQL-схемой `core`; каждый engine владеет только схемой со стабильным именем модуля. Межсхемный SQL и foreign keys запрещены, совместные данные доступны только через Go-контракты. Для каждой схемы есть отдельные Atlas migrations/config/checksum/history; runtime database role имеет права только на схему своего модуля, migration role отделена. Loader хранит upload-сессии в своей схеме `loader`, а Core предоставляет ему разрешённый файловый/staging API. Файловые наборы и их управляющие статичные группы остаются данными Core; File API получает доступ к ним только через Loader contract.

Карта относится к существующей системе и целевому переходу. YAML-конфигурация остаётся отдельной ответственностью в коде composition root; отдельные `config/*` Go modules удалены. Presentation владеет всеми точками входа и Admin UI. Действующий Sitepack не переносит пользователей или сервисные credentials; их перенос нельзя считать выполненным через импорт Sitepack.
