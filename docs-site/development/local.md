# Локальная среда и проверки

Для разработки нужны Go версии из `go.mod`, Node.js 20 для фронтенда, PostgreSQL и S3-совместимое объектное хранилище. Все operational values задаются в `.env`; используйте [пример конфигурации](../reference/configuration) и не коммитьте секреты.

## Запустить backend

```sh
cp .env.example .env
# замените placeholders и секреты в .env
go run ./cmd/api
```

Для стандартной контейнерной среды используйте `compose.yaml`; сервис должен видеть PostgreSQL, S3-хранилище и каталог временных файлов. Проверяйте `GET /health/live` и `GET /health/ready` перед браузерной работой.

## Запустить админку для разработки

```sh
cd frontend
pnpm install --frozen-lockfile
pnpm dev
```

Путь раздачи собранной админки в продукте — `/admin/`. Проверяйте, что dev proxy или API target указывает на нужный checkout, а интеграционные сценарии используют только disposable DB и object store.

## Проверки

Из корня Engine:

```sh
go test ./...
go vet ./...
```

Из `frontend/`:

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

Интеграционные тесты запускайте только с явно заданным `TEST_DATABASE_URL`, указывающим на одноразовую базу: тестовый helper может очищать её содержимое.
