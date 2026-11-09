# API

Большинство административных endpoints, включая управление заявками и медиа, используют `/api/v1`; публичная отправка форм, API сборок фич, веток, снапшотов, DataModel, файлов и runtime используют `/api/v2`. Все тела запросов и ответов — JSON UTF-8, кроме загрузки медиа
(`multipart/form-data`) и CSV-экспорта. Время — RFC 3339 в UTC. Идентификаторы — UUID.

Авторизация административных маршрутов:

```http
Authorization: Bearer ct_live_...
```

Единый формат ошибки:

```json
{
  "error": {
    "code": "schema_validation_failed",
    "message": "Данные не соответствуют схеме",
    "details": [
      {"path": "title", "code": "required", "message": "missing properties: 'title'"}
    ],
    "request_id": "f2f8cc9e-52bb-4c1c-a0a8-7a5146a62a44"
  }
}
```

Списки используют `limit` и курсор `cursor`. Ответ списка:

```json
{"items": [], "next_cursor": null}
```

`next_cursor` — непрозрачная строка. Ограничения страниц: 200 элементов для
административных списков, 50 для публичных коллекций. Формы: 200 для списка заявок,
10 000 строк для CSV.

Публичные маршруты каждой ветки применяют её список CORS origins из `BRANCHES`.
Wildcard origin разрешает любой источник; используйте его только если это намеренно.
Административные маршруты не отдают CORS-заголовки.

## Служебные маршруты

| Метод | Маршрут | Доступ | Ответ |
| --- | --- | --- | --- |
| GET | `/health/live` | публичный | `{"status":"ok"}` |
| GET | `/health/ready` | публичный, без секретов | `{"status":"ready"}` или 503 |
| GET | `/media/{id}` | публичный | 302 на публичный S3 URL |
| GET | `/admin/` | публичный | React-админка |

`/health/ready` проверяет PostgreSQL и доступность S3 bucket.

## Сводка для админки

`GET /admin/overview` (admin, moderator, staff, user) возвращает реальные агрегаты для главной страницы:

```json
{
  "counts": {
    "features_active": 4,
    "features_archived": 0,
    "draft_model_snapshots": 1,
    "draft_model_records": 2,
    "new_submissions": 3,
    "media_ready": 8,
    "failed_media": 0
  },
  "attention": {
    "draft_model_snapshots": [{"snapshot_id":"...","name":"Основной сайт","revision":3,"updated_at":"..."}],
    "new_submissions": [{"id":"...","feature_key":"contacts","status":"new","created_at":"...","data_preview":"Иван"}],
    "failed_media": []
  },
  "recent_activity": [
    {"kind":"entry","title":"Запись в «Услуги»","feature_key":"services","occurred_at":"..."}
  ]
}
```

`attention` содержит не более пяти элементов каждого типа, `recent_activity` — не более десяти.

## Токены

| Метод | Маршрут | Доступ |
| --- | --- | --- |
| GET | `/admin/me` | любой действующий токен |
| GET | `/admin/tokens` | admin; moderator — только созданные им |
| POST | `/admin/tokens` | admin — любые роли; moderator — только `user` |
| GET | `/admin/tokens/{id}` | admin; moderator — только созданный им |
| DELETE | `/admin/tokens/{id}` | admin; moderator — только созданный им |

Создание токена:

```json
{"name": "CI deployment", "role": "user", "expires_at": "2027-01-01T00:00:00Z"}
```

`expires_at` можно не передавать или передать `null` для бессрочного токена.

Ответ `201 Created`:

```json
{
  "id": "e091e5a0-a274-4e67-9cc1-9bd2221a2f01",
  "name": "CI deployment",
  "token": "ct_live_<random-secret>",
  "role": "user",
  "expires_at": "2027-01-01T00:00:00Z",
  "created_at": "2026-01-01T00:00:00Z"
}
```

Секрет возвращается один раз. В БД хранится только SHA-256 хеш. `DELETE` отзывает токен
(204), физического удаления нет.

## Feature ZIP и модель сайта

Feature ZIP регистрируется как неизменяемая сборка и закрепляется в снапшоте только после явной структурной настройки. Манифест format 2 содержит аргументы фичи для /site, типизированные dataInputs, runtime metadata и строгую form.submissionSchema, если фича принимает публичные отправки. Манифест не содержит CMS-схемы записей. Format 1 и старые content.* поля отклоняются; конвертации нет.

ER-модель и bindings dataInputs входят в структурную конфигурацию снапшота. Аргументы и bindings редактируются в /site; записи и значения модели — в /content; файлы загружаются только через /media. Структурное сохранение создаёт дочерний снапшот. API старого глобального CRUD схем фич и per-feature записей не зарегистрированы.

Для действующих manifest, ZIP, runtime ABI и editor API см. SPEC-feature-runtime.md, SPEC-data-model.md и FEATURE_SNAPSHOTS.md.

## Модельные записи

Контент хранится в модели выбранного снапшота. Один запрос читает или сохраняет черновик записей и выборок размещений с проверкой ревизии; публикация выполняется отдельно в том же снапшоте. Формы строятся по полям ER-модели, все редакционные поля необязательны, а типы, ограничения, связи и media ownership проверяются сервером. История/restore создают проверяемую новую черновую ревизию. Draft не используется публичным runtime.

Точные маршруты и требования к роли приведены ниже в разделе «Сайт: статические ветки и снапшоты».

## Публичная композиция

GET /api/v2/runtime?path=... возвращает композицию для ветки и назначенного ей снапшота. Runtime получает опубликованные модельные данные в прежнем feature content ABI. Черновики не выдаются. Публичные формы продолжают проходить строгую валидацию по закреплённому manifest form.submissionSchema; см. раздел «Формы».

## Формы

Публичная отправка:

```http
POST /api/v2/forms/{key}/submissions
Content-Type: application/json

{"name":"Иван","email":"ivan@example.com","message":"Хочу обсудить проект"}
```

Тело — сам объект данных, без обёртки. Ответ `201`:

```json
{"id": "uuid", "status": "accepted"}
```

Сервис в одной транзакции читает опубликованную схему, валидирует данные и записывает
заявку с `schema_version_id`. Публичный API не раскрывает содержимое заявки. Действует
лимит частоты по IP, окно и предел одновременно сохранённых IP-счётчиков задаются
`FORM_RATE_LIMIT`, `FORM_RATE_WINDOW` и `FORM_RATE_MAX_CLIENTS`; частота очистки счётчиков
задаётся `FORM_RATE_SWEEP_INTERVAL`. Если таблица счётчиков заполнена активными адресами,
запросы с новых IP временно отклоняются до освобождения места. Скрытых значений по умолчанию
нет. Также действует лимит размера JSON.

Административные маршруты:

| Метод | Маршрут | Доступ |
| --- | --- | --- |
| GET | `/admin/forms/{key}/submissions?branchId={uuid}` | admin, moderator, staff, user | заявки формы с фильтрацией по ветке и параметрам списка ниже |
| GET | `/admin/forms/{key}/submissions/export` | admin, moderator, staff, user |
| GET | `/admin/submissions/{id}` | admin, moderator, staff, user |
| PATCH | `/admin/submissions/{id}/status` | admin, moderator |
| POST | `/admin/submissions/{id}/archive` | admin, moderator |
| DELETE | `/admin/submissions/{id}` | только admin |

Параметры списка:

- `status` — `new`, `in_progress`, `processed`, `rejected`;
- `created_from`, `created_to` — RFC 3339;
- `q` — поиск, `field` — разрешённое поле из схемы формы (иначе поиск идёт по скалярным
  полям схемы, максимум 10);
- `sort` — `asc`/`desc` по дате;
- `include_archived=true`;
- `limit`, `cursor`.

Статус меняется телом `{"status":"in_progress"}`. `DELETE` — мягкое удаление: запись
остаётся tombstone в базе, но исключается из списка, экспорта и прямого `GET /submissions/{id}`
для всех ролей, включая admin. Модератор может только архивировать.

CSV-экспорт учитывает те же фильтры, значения с потенциальными формулами Excel
экранируются. Максимум 10 000 строк за экспорт.

## Сайт: статические ветки и снапшоты

Ветки задаются оператором в конфигурации запуска и получают отдельные listeners. Порты, CORS и политика индексации принадлежат веткам; внешний URL и TLS назначает gateway, canonical-домен хранится в настройках сайта снапшота. Набор веток фиксирован конфигурацией. Назначение выполняется только в /snapshots/branches.

Все маршруты ниже имеют префикс /api/v2/admin.

| Метод | Маршрут | Доступ | Описание |
| --- | --- | --- | --- |
| GET / POST | /feature-builds | site manager | Список неизменяемых ZIP и регистрация новой сборки |
| GET | /branches | site manager | Настроенные ветки и их текущие назначения |
| PUT | /branches/{id}/snapshot | site manager | Назначить снапшот ветке с проверкой ревизии |
| GET | /site/snapshots?limit=&beforeSequence= | site manager | Cursor-страницы дерева снапшотов и веток |
| POST | /site/snapshots | site manager | Создать независимый корень или потомка |
| GET / DELETE | /site/snapshots/{id} | site manager | Прочитать или удалить разрешённый узел дерева |
| GET / POST | /site/snapshots/{id}/design-system | site manager | Прочитать тему или создать потомка с её изменениями |
| GET | /site/snapshot-events?branchId=&limit=&before= | site manager | История назначений указанной ветки |
| GET | /content/snapshots | admin, moderator, staff, user | Снапшоты, доступные редактору контента |
| GET / PUT | /content/snapshots/{id}/model | admin, moderator, staff, user | Прочитать или сохранить черновик записей и выборок с проверкой ревизии |
| POST | /content/snapshots/{id}/model/publish | admin, moderator, staff, user | Опубликовать записи и выборки в том же снапшоте |
| GET | /content/snapshots/{id}/model/history | admin, moderator, staff, user | История публикаций модельного контента |
| POST | /content/snapshots/{id}/model/history/{publicationId}/restore | admin, moderator, staff, user | Восстановить публикацию в черновик с проверкой ревизии |
| GET | /sitepack/limits | site manager | Настроенные ограничения импорта и экспорта |
| GET / POST | /site/snapshots/{id}/export, /site/snapshots/import | site manager | Экспорт или импорт снапшота и модельного контента |

Структурная конфигурация, модель и bindings сохраняются новым снапшотом; контентные черновик и публикация имеют отдельные ревизии. Fork копирует опубликованные записи, draft, выборки и историю в независимое состояние ребёнка. Назначения веток не меняются автоматически. Снапшот, назначенный ветке, и его непосредственный родитель защищены от удаления.

Старые dev/prod release endpoints, per-feature CMS routes, contentRelations и legacy manifest schemas не входят в текущий контракт и не преобразуются.

## Настройки

`GET /admin/settings` (admin, moderator) возвращает санитизированную конфигурацию runtime без
секретов: лимиты запросов и страниц, параметры публичного API, допустимые типы файлов и
матрицу ролей. Значения берутся из реальной конфигурации процесса, поэтому раздел
«Настройки» в админке показывает фактические ограничения, а не заглушки.

```json
{
  "limits": {
    "max_json_bytes": 1048576,
    "max_upload_bytes": 20971520,
    "form_rate_limit": 20,
    "form_rate_window_seconds": 60,
    "admin_page_size": 200,
    "public_page_size": 50,
    "submission_export_max_rows": 10000
  },
  "public_api": {
    "runtime_path": "/api/v2/runtime?path={path}",
    "forms_path": "/api/v2/forms/{key}/submissions",
    "admin_path": "/api/v1/admin",
    "media_path": "/media/{id}",
    "cors_enabled": true,
    "cors_origins": ["https://site.example"]
  },
  "media": {
    "accepted_types": ["image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml", "application/pdf", "video/mp4"],
    "max_upload_bytes": 20971520,
    "image_variants": ["original", "thumbnail", "medium", "large"]
  },
  "roles": [
    {"id":"admin","label":"Администратор"},
    {"id":"moderator","label":"Модератор"},
    {"id":"staff","label":"Сотрудник"},
    {"id":"user","label":"Пользователь"}
  ]
}
```

Пример сокращён: реальные объекты ролей также содержат `summary`, `permissions` и `notes`.
Матрица ролей отражает согласованные права и серверные guards. Администратор и модератор
управляют структурой сайта; все четыре роли редактируют и публикуют контент, читают и
экспортируют заявки и работают с медиа; только администратор безвозвратно удаляет медиа и
заявки. Секреты (S3, `DATABASE_URL`,
`BOOTSTRAP_ADMIN_TOKEN`) в ответ не попадают.

Публичный runtime получает связанные коллекции внутри композиции `GET /api/v2/runtime`; отдельного
публичного маршрута коллекций нет. Поэтому настройки показывают только действующие адреса API.

## Медиа

| Метод | Маршрут | Доступ |
| --- | --- | --- |
| POST | `/admin/media` | admin, moderator, staff, user; `multipart/form-data`, поле `file` |
| GET | `/admin/media?status=&type=image|video|pdf&q=&created_from=&created_to=&min_size=&max_size=&sort=&limit=&cursor=` | admin, moderator, staff, user |
| GET | `/admin/media/{id}` | admin, moderator, staff, user |
| PATCH | `/admin/media/{id}` | admin, moderator, staff, user; `{"filename":"new-name.png"}` — меняет только отображаемое имя |
| POST | `/admin/media/{id}/retry` | admin, moderator, staff, user |
| DELETE | `/admin/media/{id}` | только admin |
| GET | `/media/{id}?variant=thumbnail` | публичный, 302 |
| POST | `/api/v2/admin/media/{id}/renditions/prepare` | user, поставить профиль фичи в очередь |
| GET | `/api/v2/admin/media/{id}/renditions/state?snapshotId=&featureKey=&buildVersion=&schemaKind=&fieldPath=&buildDigest=` | user, состояние обработки поля |

Допустимые типы: JPEG, PNG, GIF, WebP, безопасный SVG, PDF, MP4. Лимит по умолчанию 20 MiB
(`MAX_UPLOAD_BYTES`). Новая загрузка сохраняет оригинал и сразу показывает его в медиатеке.
Варианты для runtime ABI 2 создаются отдельно по профилям `x-media` в manifest; для
статичных JPEG/PNG/WebP движок создаёт WebP, для видео — H.264 MP4 и WebP poster.
GIF, SVG, PDF и анимированные WebP остаются оригиналами. Общие варианты старых загрузок
и URL с `?variant=` сохраняются для совместимости со снапшотами ABI 1.

`GET /media/{id}` ищет объект в БД, проверяет статус `ready` и отвечает `302 Found` с
`Location` на публичный URL S3. URL строится только из ключей, сохранённых в БД,
поэтому открытый редирект невозможен. Переменная `variant` ограничена ключами
`variants.json`; по умолчанию используется `original`. Рендишены ABI 2 выдаются по
`/media/{id}?profile=<sha256>&variant=<name>`; публичный URL строится из ключа в БД.

`POST /api/v2/admin/media/{id}/renditions/prepare` принимает `snapshotId`, `featureKey`,
`buildVersion`, `schemaKind` (`arguments` или `dataInputs`), JSON Pointer `fieldPath`
и `buildDigest`. GET state принимает те же параметры в query string. Движок находит профиль
только в манифесте сборки, закреплённой выбранным снапшотом; ветка не обязана быть назначена.
Клиент не передаёт и не редактирует параметры обработки. Отсутствующий снапшот или сборка
возвращают 404, устаревшие version/digest — 409, неверный путь/профиль — 422. Ответ содержит
`status: pending|processing|ready|failed`; задачи хранятся в PostgreSQL, повторяются после
истечения lease и могут быть запущены повторно после ошибки. Оригинал можно сохранить в
черновик во время обработки, публикация ждёт готовности профиля.

Медиа в контенте хранится как UUID (`format: "media"`), а не как URL.

Параметры списка: `status` — `pending|ready|failed`; `type` — `image|video|pdf`;
`q` ищет по отображаемому имени; `created_from`/`created_to` принимают `YYYY-MM-DD`
или RFC3339, причём `created_to` включает весь указанный день; `min_size`/`max_size`
задаются в байтах; `sort` — `new|old|name|size_desc|size_asc`. `limit` по умолчанию 50,
максимум 100. `cursor` непрозрачен и привязан к выбранной сортировке: смена `sort`
требует начать пагинацию заново.

## Коды ошибок

| HTTP | Код | Причина |
| --- | --- | --- |
| 400 | `invalid_json`, `validation_failed`, `invalid_query` | тело/параметры |
| 401 | `unauthorized` | токен отсутствует, истёк или отозван |
| 403 | `forbidden` | роль не даёт прав |
| 404 | `not_found` | объект не найден |
| 409 | `conflict` | revision, уникальность, состояние |
| 413 | `request_too_large`, `file_too_large` | лимит размера |
| 415 | `unsupported_media_type` | тип файла |
| 422 | `schema_validation_failed`, `media_processing_failed` | данные не соответствуют схеме |
| 429 | `rate_limited` | лимит публичных отправок |
| 500 | `internal_error` | необработанная ошибка |
| 503 | `auth_unavailable` | временная недоступность БД |

Внутренние SQL-ошибки, имена таблиц, пути и секреты клиенту не возвращаются.
