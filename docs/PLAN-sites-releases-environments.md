# Архивный план: сайты, релизы и среды dev/prod

> **Архив, не текущая спецификация и не трекер работ.** Этот документ фиксирует
> прежнее предложение с dev/prod, продвижением релизов и per-feature указателями.
> Его решения, схемы БД, API, примеры и отмеченные исторические чекбоксы заменены
> статическими конфигурационными ветками, деревом снапшотов и snapshot DataModel.
> Текущие требования и статус реализации находятся в
> [TODO](TODO-admin-snapshots-content-files.md), [ветках и снапшотах](SPEC-branches-snapshots.md)
> и [модели данных](SPEC-data-model.md). Не используйте архив как описание
> действующих endpoints или миграций.

Документ фиксирует целевую архитектуру, порядок работ и критерии готовности для
хостинга статического сайта из Git-релизов и трёхсостоятельного контента
`draft → dev → prod`.

## Статус

- [x] Архитектура и решения согласованы
- [x] Определены модель сред, права и источники релизов
- [x] Реализация начата

## Принятые решения

- Один движок и одна БД обслуживают один сайт.
- Сайт — статический SPA/SSG. Движок не собирает код сайта, а хранит и раздаёт
  immutable-артефакты.
- Окружения: `dev` и `prod`.
- Один общий черновик. Публикация идёт в `dev`, затем версия продвигается в `prod`.
- У схем Feature и записей контента независимые указатели `dev`/`prod`.
- `prod` не может опережать `dev`.
- Один и тот же релизный артефакт используется в обеих средах.
- Runtime-конфиг сайта инжектится движком; сборка сайта не зависит от среды.
- Публичный API определяет среду по listener/host/port.
- CORS, домены и TLS настраиваются per environment в БД и админке.
- Артефакты хранятся в S3/MinIO и локальном кеше.
- Заявки форм тегируются средой; медиа общие.
- Preview-URL для релизов не делаем.
- TLS — ACME/Let's Encrypt; локально HTTP/self-signed.
- Секреты Git-провайдеров шифруются AES-GCM и не возвращаются через API.

## Целевая модель

```
draft ──publish──> dev ──promote──> prod
```

### Инварианты контента и схем

- `draft` один и общий для обеих сред.
- `prod`-указатель допустим только при наличии `dev`-указателя.
- Номер версии `dev` не меньше номера версии `prod`.
- Публикация схемы в `dev` проверяет совместимость всех активных dev-записей.
- Продвижение схемы в `prod` проверяет совместимость всех активных prod-записей.
- Публикация записи в `dev` валидирует данные по dev-схеме.
- Продвижение записи в `prod` валидирует данные по prod-схеме.
- `rollback` переключает указатель среды, не меняя immutable-версии.
- `unpublish` из `dev` запрещён, пока сущность опубликована в `prod`.
- `archive` Feature/записи скрывает её в обеих средах.
- Публичный API проецирует данные по схеме активной среды, а не по схеме,
  с которой была создана версия записи. Это защищает `private`-поля при
  расхождении схем.
- Публикация и продвижение контента в `prod` — только moderator/admin.
- Импорт релизов и активация `dev` — staff; активация/rollback `prod` — admin.

### Права

| Возможность | user | moderator | admin |
|---|---:|---:|---:|
| Контент: draft/create/edit | да | да | да |
| Публикация контента в dev | да | да | да |
| Снятие с dev | да | да | да |
| Продвижение/снятие контента в prod | нет | да | да |
| Rollback контента в dev | нет | да | да |
| Rollback контента в prod | нет | нет | да |
| Схемы и настройки Feature | нет | да | да |
| Архив Feature/записей | нет | да | да |
| Медиа: upload/view/rename | да | да | да |
| Медиа: delete | нет | нет | да |
| Заявки: view/export | да | да | да |
| Заявки: статусы/архив | нет | да | да |
| Импорт релизов и активация dev | нет | да | да |
| Активация/rollback prod | нет | нет | да |
| Доступы, токены, настройки | нет | нет | да |

Стартовая страница `user` — дашборд.

---

## TODO

### 1. Миграции БД

- [x] Создать `migrations/002_sites_releases_environments.sql`
- [x] Добавить `features.dev_version_id`
- [x] Переименовать `features.published_version_id` → `prod_version_id`
- [x] Добавить `feature_entries.dev_version_id`
- [x] Переименовать `feature_entries.published_version_id` → `prod_version_id`
- [x] Перенести существующие published-указатели в `dev` и `prod`
- [x] Заменить constraint `draft <> published` на проверки dev/prod
- [x] Добавить триггеры: `prod IS NULL OR dev IS NOT NULL`
- [x] Добавить триггеры: версия `dev >= prod`
- [x] Адаптировать триггеры immutable-версий и cross-table проверки
- [x] Создать `content_publication_events`
- [x] Создать `site_settings`
- [x] Создать `site_environments` и seed `dev`/`prod`
- [x] Создать `release_sources`
- [x] Создать `site_releases`
- [x] Создать `release_events`
- [x] Добавить `form_submissions.environment`
- [x] Добавить индексы по средам и релизам

### 2. Backend: среды и контент

- [x] Добавить пакет `pkg/environment`
- [x] Добавить resolver среды по listener/host/port
- [x] Пробросить среду через context middleware
- [x] Перестроить `features.Repository` под `dev`/`prod`
- [x] Перестроить `content.Repository` под `dev`/`prod`
- [x] `features.Publish` → публикация draft-схемы в `dev`
- [x] `features.Promote` → продвижение dev-схемы в `prod`
- [x] `features.Rollback(environment)`
- [x] `content.Publish` → публикация draft-записи в `dev`
- [x] `content.Promote` → продвижение dev-версии в `prod`
- [x] `content.Unpublish(environment)`
- [x] `content.Rollback(environment)`
- [x] Валидация записей при публикации/продвижении схемы
- [x] `authoringSchema`: draft → dev → prod
- [x] Публичный API: выбор указателей по среде
- [x] Публичный API: проекция по схеме среды
- [x] `forms.LookupFormByKey(environment)`
- [x] `forms.Submit` сохраняет среду
- [x] Админский список/экспорт заявок фильтрует по среде
- [x] `Detail` возвращает `draft`, `dev`, `prod`
- [x] Записывать `content_publication_events` при publish/promote/rollback/unpublish

### 3. Backend: права

- [x] Добавить `lib`-независимый capability-слой на backend
- [x] `user`: контент, dev-публикация, медиа, просмотр/экспорт заявок
- [x] `user`: запрет схем, настроек, архива, rollback, prod-публикаций
- [x] `moderator`: схемы, feature-настройки, архив, prod-контент, dev-rollback
- [x] `moderator`: импорт релизов и активация dev
- [x] `admin`: полный доступ, prod-релизы и prod-rollback
- [x] Обновить матрицу в `features/settings/handler.go`
- [x] Закрыть админские маршруты на публичных listener'ах

### 4. Backend: релизы и артефакты

- [x] Добавить пакет `pkg/secrets` (AES-GCM)
- [x] Добавить пакет `features/releases`
- [x] Интерфейс Git-провайдера
- [x] GitHub provider: список релизов и скачивание asset
- [x] Ручная загрузка zip
- [x] GitLab provider
- [x] Gitea provider
- [x] Валидация zip: `index.html`, traversal, symlink, zip-bomb, лимиты
- [x] Нормализация корня артефакта (`dist/`, вложенная папка, корень)
- [x] Загрузка артефакта в S3 с SHA-256
- [x] Локальный кеш релиза с атомарной распаковкой
- [x] Активация релиза в среду
- [x] Rollback релиза
- [x] Снимок версий Feature в `release_events.metadata`
- [x] Статическая раздача с SPA fallback
- [x] Заголовки кеширования: `index.html` no-store, assets immutable
- [x] Инъекция `window.__SITE_CONFIG__` в `index.html`
- [x] Публичный `GET /api/v1/site`
- [x] Защита от SSRF при работе с repo URL

### 5. Listener'ы, TLS, домены, CORS

- [x] Разделить admin/router и public site router
- [x] Admin listener `:8080`
- [x] Dev site listener `:8081`
- [x] Prod site listener `:8082`
- [x] HTTPS `:443` с SNI-роутингом по Host
- [x] HTTP `:80` для ACME HTTP-01 и редиректа
- [x] `autocert.Manager` с доменами из БД
- [x] Кеш сертификатов в volume
- [x] ACME env: email, staging, cache dir
- [x] CORS per environment из БД
- [x] `PUBLIC_CORS_ORIGINS` только как bootstrap
- [x] UI/API настроек доменов, CORS, TLS, public base URL
- [x] Показывать фактические порты с пометкой о необходимости рестарта
- [x] Обновить `compose.yaml`: порты, volumes, env

### 6. Frontend

- [x] Убрать блокировку `user` в `LoginPage.tsx`
- [x] Убрать блокировку `user` в `RequireAuth` (`router.tsx`)
- [x] Добавить `frontend/src/lib/permissions.ts`
- [x] Обновить `types.ts`: `draft/dev/prod`, environments/releases/sources/events
- [x] Обновить `api.ts`: publish/promote/unpublish/rollback, site/releases
- [x] Трёхсостоятельные бейджи в Feature
- [x] Действия publish dev / promote prod / unpublish / rollback
- [x] Предупреждение о расхождении dev/prod-схем
- [x] Показывать схему, по которой валидируется draft
- [x] Content editor: статусы версий по средам
- [x] Content editor: скрыть prod-действия и rollback для `user`
- [x] Новый раздел «Сайт» (`/deploy`)
- [x] Обзор сред: активный релиз, версии контента, домены, TLS, CORS, health
- [x] Список релизов и детали
- [x] Импорт из источника
- [x] Ручная загрузка zip
- [x] Активация dev/prod и rollback
- [x] Настройки источников: write-only токен
- [x] Настройки сред: домены, CORS, TLS, base URL
- [x] Заявки: колонка и фильтр среды, экспорт с `environment`
- [x] Дашборд: активные релизы и состояние сред
- [x] Скрыть разделы навигации по роли
- [x] Role guards для схемы, настроек, доступов, релизов

### 7. Тесты

- [x] Миграции и инварианты указателей
- [x] Publish dev / promote prod / rollback / unpublish схем
- [x] Publish dev / promote prod / rollback / unpublish записей
- [x] Запрет `prod > dev`
- [x] Запрет dev-unpublish при активном prod
- [x] Валидация контента при смене схемы
- [x] Изоляция публичного API по средам
- [x] Private-поля при расхождении схем
- [x] Формы: схема и заявки по средам
- [x] CORS per environment
- [x] Матрица прав user/moderator/admin
- [x] Импорт артефакта: корректный zip
- [x] Импорт артефакта: нет `index.html`
- [x] Импорт артефакта: path traversal
- [x] Импорт артефакта: symlink
- [x] Импорт артефакта: zip-bomb и лимиты
- [x] Активация/rollback и снимок версий в событии
- [x] Шифрование/расшифровка токенов провайдеров
- [x] Fake Git-провайдер через `httptest`
- [x] Frontend `pnpm typecheck`
- [x] Frontend `pnpm build`

### 8. E2E и документация

- [x] Собрать тестовый сайт `onix-fun/website`
- [x] Упаковать `frontend/dist` в zip
- [x] Загрузить артефакт вручную
- [x] Активировать dev и проверить статику + API
- [x] Продвинуть артефакт в prod
- [x] Проверить изоляцию контента и форм
- [x] Rollback на предыдущий релиз
- [x] Обновить `README.md`
- [x] Обновить `docs/API.md`
- [x] Добавить `docs/RELEASES.md`
- [x] Обновить `.env.example` / compose-документацию
- [x] Полный прогон Go-проверок

---

## План по этапам

### Этап 1. Контент dev/prod и права

Цель: получить рабочую трёхсостоятельную модель контента и схем без релизов сайта.

Состав:

- миграция 002;
- environment-контекст и resolver;
- перестройка features/content/forms;
- публичный API по средам;
- права и capability-слой;
- UI трёх состояний;
- интеграционные тесты.

Критерии готовности:

- схемы и записи независимо публикуются в dev и продвигаются в prod;
- публичный API отдаёт разные данные для dev/prod;
- prod не может опережать dev;
- `user` работает по новой матрице прав;
- все Go-тесты и frontend typecheck/build зелёные.

### Этап 2. Релизы

Цель: импортировать и активировать статический артефакт.

Состав:

- site/environments/release sources;
- шифрование секретов;
- GitHub + manual import;
- S3 + локальный кеш;
- статическая раздача;
- активация/rollback + аудит;
- UI раздела «Сайт»;
- затем GitLab/Gitea.

Критерии готовности:

- zip импортируется, валидируется и хранится immutable;
- релиз активируется в dev;
- тот же артефакт продвигается в prod;
- rollback работает без удаления истории;
- событие активации содержит снимок версий Feature;
- статика и SPA fallback работают.

### Этап 3. Сети и TLS

Цель: production-домены, HTTPS и per-environment CORS.

Состав:

- listener'ы `:8080`, `:8081`, `:8082`;
- `:80`/`:443`, SNI и ACME;
- кеш сертификатов;
- домены/CORS в БД и UI;
- compose/volumes.

Критерии готовности:

- dev и prod доступны на отдельных портах локально;
- в production домены маршрутизируются по SNI;
- сертификаты выпускаются и кешируются;
- CORS работает отдельно для каждой среды.

### Этап 4. Финализация

Цель: закрыть документацию и e2e.

Состав:

- дашборд;
- README/API/RELEASES;
- e2e с тестовым сайтом;
- полный прогон проверок.

Критерии готовности:

- документация описывает настройку, роли и деплой;
- e2e-сценарий воспроизводим;
- нет известных блокирующих дефектов.

---

## API

### Админские маршруты

```text
GET/PUT  /api/v1/admin/site
GET/PUT  /api/v1/admin/site/sources
POST     /api/v1/admin/site/sources/{id}/sync
GET      /api/v1/admin/releases
POST     /api/v1/admin/releases/import
POST     /api/v1/admin/releases/upload
GET      /api/v1/admin/releases/{id}
POST     /api/v1/admin/releases/{id}/activate
POST     /api/v1/admin/environments/{key}/rollback
GET      /api/v1/admin/environments
PATCH    /api/v1/admin/environments/{key}
GET      /api/v1/admin/publication-events
```

### Контент и схемы

```text
POST /api/v1/admin/features/{id}/publish      -> dev
POST /api/v1/admin/features/{id}/promote      -> prod
POST /api/v1/admin/features/{id}/rollback     { version, environment }

POST /api/v1/admin/entries/{id}/publish       -> dev
POST /api/v1/admin/entries/{id}/promote       -> prod
POST /api/v1/admin/entries/{id}/unpublish     { environment }
POST /api/v1/admin/entries/{id}/rollback      { version, environment }
```

### Публичные маршруты

```text
GET  /api/v1/site
GET  /api/v1/content
GET  /api/v1/content/{key}
GET  /api/v1/content/{key}/entries
POST /api/v1/forms/{key}/submissions
GET  /media/{key}
```

Среда определяется listener/host/port. Админский listener `:8080` для публичных
маршрутов по умолчанию использует `prod` для обратной совместимости. Сайтовые
listener'ы монтируют только публичные маршруты и статику.

---

## Схема БД

### `features` / `feature_entries`

```text
draft_version_id  UUID NULL
dev_version_id    UUID NULL
prod_version_id   UUID NULL
revision          BIGINT
```

Инварианты:

```text
prod IS NULL OR dev IS NOT NULL
version(dev) >= version(prod)
draft <> dev
draft <> prod
```

### `content_publication_events`

```text
id, entity_kind, feature_id, entry_id,
version_id, environment, action,
actor_token_id, created_at, metadata
```

### `site_environments`

```text
key               dev|prod PK
name
public_base_url
domains           TEXT[]
http_port
tls_enabled
cors_origins      TEXT[]
active_release_id
revision
updated_at
```

### `release_sources`

```text
provider          github|gitlab|gitea|manual
repo_url
asset_pattern
auth_ciphertext
auth_nonce
created_by
is_enabled
```

### `site_releases`

```text
version, source_id, external_id, tag, commit_sha,
artifact_object_key, artifact_sha256, artifact_size,
manifest, imported_by, created_at
```

### `release_events`

```text
release_id, environment, action,
actor_token_id, metadata, created_at
```

### `form_submissions`

```text
environment  dev|prod NOT NULL DEFAULT 'prod'
```

---

## Runtime-конфиг сайта

Движок инжектит в `index.html`:

```js
window.__SITE_CONFIG__ = {
  environment: "dev" | "prod",
  releaseId: "uuid",
  releaseVersion: "front_v1.2.3",
  apiBase: "/api/v1"
};
```

Требования:

- штатная работа через same-origin;
- `index.html` не кешируется;
- хешированные ассеты кешируются как immutable;
- SPA fallback на `index.html`;
- защита от path traversal.

---

## Тестовый сайт

Репозиторий: `https://github.com/onix-fun/website.git`

- Vite SPA в `frontend/`;
- GitHub Actions `frontend.yml`;
- теги `front_v*.*.*`;
- asset `frontend-dist.zip`;
- внутри архива `dist/`;
- сборка: `npm ci && npm run build`;
- build output: `frontend/dist`;
- релизов пока 0, поэтому e2e начинается с ручной загрузки собранного артефакта.

---

## Риски и меры

| Риск | Мера |
|---|---|
| Расхождение dev/prod-схем ломает данные | Валидация при publish/promote, блокировка при несовместимости |
| Утечка `private`-полей | Проекция по схеме среды, тесты на расхождение схем |
| Zip-slip / zip-bomb | Нормализация путей, запрет symlink, лимиты размера и файлов |
| SSRF через repo URL | Только https, allowlist провайдеров, запрет private IP |
| Утечка Git-токенов | AES-GCM, write-only API, запрет логирования |
| Prod опережает dev | Триггеры и сервисные проверки |
| Случайное снятие dev при активном prod | Запрет до снятия prod |
| Изменение портов на лету | Порты только через конфиг/рестарт, в UI read-only |
| Смешивание заявок сред | Обязательное поле `environment` и фильтры |

---

## Открытые вопросы

Нет. Все решения, влияющие на модель данных и права, согласованы.

## 5. Ограничение прав: user редактирует только контент существующих фич

### Цель
Сделать роль `user` максимально ограниченной: он не может создавать фичи, не работает со схемой, только наполняет контент существующих фич (и для форм — только чтение заявок).

### Backend
- [x] Добавить `auth.CanCreateFeature(role string) bool` (только `admin`, `moderator`).
- [x] `POST /api/v1/admin/features` перевести с `jsonStaff` на проверку `CanCreateFeature` (только admin/moderator).
- [x] `features.Handler.Create` проверяет права, возвращает `403` для `user`.
- [x] Эндпоинты схемы (`GET/PUT /draft`, `POST /publish`, `POST /promote`, `POST /rollback`, `GET /versions`) по-прежнему доступны только staff/admin согласно текущим правила, доступ для `user` блокирован.
- [x] Формы: `user` разрешён просмотр списка/детали/экспорт заявок; изменение статуса/архив/удаление только staff/admin.

### Frontend
- [x] `frontend/src/lib/permissions.ts` — добавить `canCreateFeature`.
- [x] `/features` — скрыть кнопку «Создать фичу» для `user`.
- [x] Вкладка «Схема» скрыта для `user` в `FeatureLayout`. Редирект `/features/:id/schema` -> `/features/:id/content`.
- [x] Страница `FeatureSchemaPage` — для `user` редиректит на контент, визуальный конструктор недоступен.
- [x] `FeatureContentPage` / `ContentEditorPage` — форма ввода строится автоматически по схеме фичи, без выбора типа полей, без доступа к настройкам схемы.
- [x] `SubmissionsPage` — для `user` скрыты действия изменения статуса, архивирования, удаления; доступны только просмотр, фильтр по среде, CSV-экспорт.
- [x] `ReleasesPage` — доступ только staff/admin (уже реализовано).

### Результат
- `user` строго редактирует контент на существующих фичах.
- `user` не может создавать фичи и не имеет доступа к экрану «Схема».
- Конструктор схемы недоступен для `user`, ввод контента формируется автоматически по готовой схеме.
