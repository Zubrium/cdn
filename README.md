# Zubrium Website Engine

Модульная платформа для управления сайтами и их публичной выдачи. Единственная product composition root и исполняемое приложение находятся в `main/`; Core, engines, workers и Presentation остаются независимо собираемыми Go-модулями.

## Структура

- `core/` — идентичности, доступ, файлы, scoped engine data и durable Coordinator.
- `engines/` — Website, Loader, Content, Forms и Router workflows.
- `workers/` — фоновые обработчики.
- `presentation/` — HTTP adapters и внешние API.
- `main/` — конфигурация, profile и композиция приложения.
- `frontend/admin/` — React 18 + TypeScript Admin SPA.
- `spec/` — архитектурные и модульные контракты; OpenAPI 3.1 находится в `spec/presentation/openapi/`.
- `tests/` — Go/frontend tests и acceptance matrix.

Внешние HTTP-маршруты имеют вид `/api/...` без версионного префикса. OpenAPI `info.version` остаётся метаданными документа и не задаёт URL.

## Разработка

Из корня репозитория:

```bash
make test
make vet
make build
make build-full
```

Из `frontend/`:

```bash
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

Интеграционные проверки требуют отдельных disposable PostgreSQL/S3 ресурсов. Не направляйте их на рабочие данные. Публичный Router listener должен быть размещён за внешним proxy; конфигурация и admission policy proxy принадлежат deployer.

Активный статус и критерии реализации: [TODO](docs/TODO-admin-snapshots-content-files.md). Контракты проверки: [acceptance matrix](tests/ACCEPTANCE-MATRIX.md). Результаты QA и ограничения: [QA report](docs/QA_REPORT.md).
