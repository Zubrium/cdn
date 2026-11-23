# План закрытия release-готовности (2026-11-23)

Рабочая ветка: `readiness-20261123`. База: `ee3878e7d81410b6efaa388f1c4815cf01d81901`.

Источник истины — `spec/`. Исторические отчёты (`output/test-report-release-final-20261123-c*.json`,
E2E `e2e-20261123T160114Z-82a196`) учитываются только как evidence содержимого рабочего дерева,
из которого сделан `ee3878e`; новое доказательство должно быть получено на чистом checkout итогового коммита.

## Восстановление доказуемой базы

- [x] Git status/HEAD: `ee3878e`, проектные файлы чистые; локальные `skills-lock.json`, `.claude/`, `.agents/` не трогаются.
- [x] Чистый checkout: detached worktree `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-clean-ee3878e`.
- [x] На чистом checkout: `make lint-core-boundary` → `Architecture boundaries OK ... across 20 repository-local Go modules`; `make build` → exit 0; `make test` → exit 0.
- [ ] Сверить код с историческими отчётами и SPEC (OBS/CACHE/CONC, Feature Catalog).

## Блокеры и приоритеты

| Приоритет | Область | Статус на старте |
| --- | --- | --- |
| P0 | Feature Catalog `GET /api/admin/features` → 503 | требуется root cause |
| P0 | Архитектурные инварианты (DataBus, Core isolation) | зелёный boundary lint на чистом checkout |
| P1 | OBS-001..010 | нет executable ledger |
| P1 | CACHE-001..012 | нет executable ledger, целевая YAML-схема не внедрена |
| P1 | CONC-001..012 | нет executable ledger |
| P1 | Отдельные ledger'ы Content/Forms/Loader | нет |
| P2 | Остаточные пункты Website/Router/Workers/Admin | по активному TODO |
| P3 | Документация и финальный release sweep | после реализации |

## Правила выполнения

- Каждое изменение: SPEC → воспроизведение дефекта → regression test (RED) → минимальное исправление (GREEN) → полный набор проверок → ledger → коммит.
- Ни один сценарий не переводится в `pass` без реального вывода команды в этом сеансе.
- `blocked`/`not_run`/`skipped` остаются таковыми в матрице и отчёте.
- Категории результата: implemented / verified / blocked / not implemented.

## Журнал решений

- `Core changes`: пользователь разрешил минимальные внутренние изменения замороженного Core (access_epoch, `cache.View`, шардирование/батчинг инвалидации, class-scoped Claim, storage-семaphore) с regression-тестами. Публичный credential-bound session контракт и внешние HTTP-пути остаются неизменными. Цена ошибки: при нарушении контракта потребуется откат внутренних изменений Core.
- `Feature Catalog 503`: наблюдение 2026-11-22 относится к старому stub в `engines/website/application/unavailable.go`; на `ee3878e` каталог реализован. Статус `UI-POL-011` меняется только после реального browser-прогона на текущем коде.
- `OBS-006/008`: изолированный стек OTel Collector + OpenObserve поднимается локально (проверено: OTLP/HTTP и OpenObserve отвечают, Collector принимает все три сигнала), но исполняемый контракт подтестов использует один неаутентифицированный GET-поиск с `{signal}`, а OpenObserve v1.1.2 требует аутентифицированный POST `/api/default/_search` с `query{sql,start_time,end_time}` и отдельными stream'ами на каждую метрику. PASS без адаптации test harness был бы фикцией; сценарии остаются `blocked` с уточнённой причиной. Стек остановлен без удаления disposable-volume.
- `SPEC-concurrency §11`: пример `core.max_connections: 20` противоречил формуле §7 (для показанных лимитов минимум 27). Пример исправлен на `32` с явным расчётом; конфигурации продукта уже использовали `32`.
