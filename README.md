# Zubrium

Движок для сайтов, собираемых из независимо версионируемых frontend-фич. Один экземпляр может обслуживать несколько сайтов: каждый назначенный статической ветке снапшот задаёт отдельную структуру сайта и порт. Движок хранит сборки фич и данные, отдаёт встроенный публичный runtime и админку, управляет медиа, заявками и CORS. Движок принимает HTTP-трафик; HTTPS, если он нужен, завершается на внешнем сетевом уровне.

## Быстрый старт

```bash
cp .env.example .env
# Set ENGINE_IMAGE to the engine's GitHub Container Registry package.
docker compose pull server
docker compose up -d --no-build
```

- Админка и API: <http://localhost:8080/admin/>
- Публичная ветка `preview`: <http://localhost:8081/>
- Публичная ветка `website`: <http://localhost:8082/>
- MinIO console: <http://localhost:9001>; учётные данные задаются конфигурацией.

Имена, порты, CORS и политика индексации веток заданы в `BRANCHES` из `.env.example`.
Настройте значения под окружение; публичные домены и TLS назначает внешний gateway.

`ENGINE_IMAGE` указывает на опубликованный в GHCR образ. Compose не собирает движок на этой машине и всегда проверяет registry при запуске. Для приватного пакета выполните `docker login ghcr.io` с токеном, имеющим `read:packages`. Перед запуском замените тестовые токены и пароли из `.env.example`.

## Сборка и выпуск образа

Workflow [engine-image.yml](.github/workflows/engine-image.yml) запускает проверки на pull request и push в `main`. Публикация в GHCR запускается при push SemVer-тега (`v1.2.3`) или публикации GitHub Release. Workflow публикует версии `1.2.3`, `1.2` и `latest` для стабильного релиза. Имя образа формируется из `GITHUB_REPOSITORY`: `ghcr.io/<owner>/<repository>`.

Чтобы выпустить версию, создайте SemVer-тег (`git tag v1.2.3 && git push origin v1.2.3`) либо опубликуйте GitHub Release с таким тегом. Чтобы обновить локальный движок до конкретного релиза, укажите его тег в `ENGINE_IMAGE` в `.env`, затем выполните `make compose-up`. Команда сначала получает образ из registry и запускает Compose без локальной сборки. Чтобы разрешить скачивание без входа в GHCR, после первой публикации сделайте package публичным в настройках GitHub Packages.

## Как собирается сайт

```text
репозиторий сайта                         Zubrium
┌────────────────────────┐                ┌───────────────────────────┐
│ core/                  │  feature ZIPs  │ admin + public runtime    │
│ features/<key>/        ├───────────────►│ feature build registry    │
│   manifest.json        │                │ visual page/route editor  │
│   MVC + SCSS           │                │ snapshot tree + branches │
└────────────────────────┘                └───────────────────────────┘
```

В репозитории сайта исходниками остаются `core` и независимые `features`; в CI каждая фича собирается в отдельный статический ZIP. Архив содержит `manifest.json` и готовые browser assets. Движок проверяет архив и digest, хранит его неизменяемо и не компилирует загруженный код. Помимо совместимого legacy React ABI поддерживаются `static-module-v1` для framework-neutral ESM lifecycle и `static-html-v1` для статических HTML-фрагментов, видимых без JavaScript. Загрузки доверенные и исполняются с origin сайта. Контракт, ограничения и руководство по упаковке описаны в [SPEC-feature-runtime.md](docs/SPEC-feature-runtime.md) и [FEATURE-RUNTIME-AUTHORING.md](docs/FEATURE-RUNTIME-AUTHORING.md); реализация отслеживается в TODO.

Manifest format 2 — контракт разработчика: runtime ABI, schema аргументов для редактора сайта, typed `dataInputs`, form submission schema, слоты и feature tokens. CMS-модель и формы записей принадлежат снапшоту и настраиваются графически в `/site`; значения сущностей редактируются в `/content`. Для CMS-схемы в манифесте нет обратной совместимости с format 1.

Структурные изменения создают новый снапшот. Снапшоты образуют дерево; копия получает независимые модель, записи, черновики и выборки. У движка нет фиксированных сред dev/prod: имена и порты веток задаются в конфигурации, а назначения снапшотов изменяются только в `/snapshots/branches`. В каждой ветке публикация контента обновляет только записи назначенного снапшота.

Схема записей задаётся графически в ER-редакторе `/site/data-model`. Все настройки фич, включая аргументы и сопоставления `dataInputs`, задаются в `/site`; редакционные значения, черновики, публикация и выбор записей для размещений — в `/content`. Медиа загружаются в `/media`, а редакторы могут только выбирать уже загруженные файлы. Формы заявок валидируются отдельной строгой схемой manifest.

## Чистый запуск без переноса старых данных

Для перехода используйте пустую базу с единственной начальной миграцией, затем загрузите ZIP-фичи, соберите модель данных и структуру сайта и назначьте снапшот нужной статической ветке. Старые per-feature CMS-данные, manifest format 1 и relation envelopes не переносятся и не читаются новой системой. Проверки сброса выполняйте только на явно disposable QA-ресурсах; рабочие базы, storage и данные Vitasil не сбрасываются и не мигрируются.

## Архитектура движка

- `features/sitebuilder` — manifests, проверка ZIP, реестр сборок, модель сайта, маршруты и снапшоты.
- `features/forms`, `features/media`, `features/files` — заявки, медиагалерея и серверное файловое API.
- `presentation/http` — API, middleware и публичные маршрутизаторы.
- `pkg/runtime` — публичные Go HTML-шаблоны, ABI lifecycle scripts и независимые публичные стили/assets.
- `frontend/admin/src/pages/site` — структура сайта, ER-модель, аргументы, bindings и дизайн-система; управление ветками вынесено в `/snapshots/branches`.
- `frontend/admin/src/pages/content` — записи snapshot-scoped модели, черновики, публикация и выборки размещений.
- `frontend/admin/src/domains/content-schema` — общие поля и проверки редакционных значений; формы записей строятся из ER-модели выбранного снапшота.
- `frontend/admin` и `frontend/ui` содержат админку и её стили. Публичные HTML и маршруты полностью формируются Go из route tree снапшота; scripts жизненного цикла ABI и отдельные публичные стили находятся в `pkg/runtime`. Подробности и команды — в [архитектуре frontend](docs/FRONTEND_ARCHITECTURE.md).

Ограничения зависимостей: доменная логика находится в `features/<name>`, `pkg` не зависит от `features`, HTTP-слой не обращается к БД напрямую. Go формирует начальный HTML и bootstrap напрямую из snapshot composition; `/api/v2/runtime` используется внешними API-потребителями и браузерным ABI-runtime при последующих переходах назад/вперёд.

## Локальная разработка

```bash
docker compose up -d db minio
export DATABASE_URL='postgres://engine:engine@localhost:5432/engine?sslmode=disable'
export BOOTSTRAP_ADMIN_TOKEN='replace-with-a-local-admin-secret'
export S3_ENDPOINT='http://localhost:9000'
export S3_PUBLIC_BASE_URL='http://localhost:9000'
export S3_ENSURE_BUCKET=true
```

Админка и публичные runtime scripts встраиваются в Go-бинарь. Dockerfile собирает admin UI и ABI scripts до сборки backend; публичный HTML отдельным frontend bundle не является:

```bash
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
go run ./cmd/api
```

Для локальной разработки и сборки каждого пакета отдельно:

```bash
pnpm --dir frontend dev:admin
pnpm --dir frontend build:admin
pnpm --dir frontend build:runtime
```

## Проверки

```bash
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
go test ./...
go vet ./...
pnpm --dir frontend lint
pnpm --dir frontend typecheck
```

Интеграционные тесты требуют отдельную тестовую PostgreSQL базу. Они очищают таблицы, поэтому не направляйте на неё рабочие данные:

```bash
docker compose up -d db
pnpm --dir frontend build
TEST_DATABASE_URL='postgres://engine:engine@localhost:5432/engine?sslmode=disable' go test ./cmd/integration/ -count=1
```

Контракты и последовательность миграции описаны в [docs/FEATURE_SNAPSHOTS.md](docs/FEATURE_SNAPSHOTS.md), HTTP API — в [docs/API.md](docs/API.md).
