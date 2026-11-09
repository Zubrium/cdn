# Публичные backend-запросы из фич

Статус: реализация и scoped QA проверены на синтетической фиче; внешняя CORS-политика реального backend и использование bindings существующими Vitasil-фичами не заявляются проверенными. Спецификация не добавляет движку собственный HTTP-клиент или модель ответа: сетевой вызов и преобразование ответа остаются кодом фичи.

## Цель и границы

Администратор настраивает публичные адреса backend в общих настройках сайта, а в инспекторе конкретного размещения выбирает backend, путь и связи входящих данных с аргументами запроса. Фича получает разрешённую конфигурацию через runtime и сама выполняет `fetch`/другой запрос. Ожидаемая модель ответа и обработка результата остаются в исходниках и manifest фичи.

Функция относится к структурным настройкам сайта и сохраняется в снапшоте. Она не управляет назначением снапшотов веткам, редакционными записями, контентными черновиками, публикацией или загрузкой файлов. Связи источников контента между размещениями остаются в инспекторе сайта и не являются HTTP-backend настройками.

## Модель настроек

- В общих настройках сайта (`/site/settings`) администратор регистрирует именованные публичные backend. ID каждой записи генерируется движком; вручную задаётся только отображаемое имя и публичный base URL.
- Реестр backend принадлежит выбранному снапшоту. Изменение реестра является структурным изменением и при сохранении создаёт потомка согласно модели снапшотов. Назначения веток не меняются.
- В инспекторе фичи (`/site/pages/...`) для каждого объявленного манифестом запроса доступны зарегистрированные backend и вариант «Текущий сайт».
- «Текущий сайт» использует относительный путь от origin страницы. Регистрация домена не требуется; запрос может идти на другой путь того же origin, например `/api/profiles/{username}`.
- Backend URL и аргументы являются публичной конфигурацией снапшота: их нельзя использовать для ключей, паролей, bearer-токенов или иных секретов.
- Удаление используемого backend должно показать все зависящие размещения и требовать сначала перепривязать их либо явно удалить конфигурацию запроса. Ссылка не должна незаметно превратиться в запрос к другому backend.

## Манифест и редактор

Манифест ZIP остаётся источником схемы и метаданных фичи. Он объявляет стабильные ключи конфигурации запроса, заголовки полей, типы аргументов и допустимые источники/назначения. Значения выбранного backend, пути и связей сохраняются в конфигурации размещения выбранного снапшота. Не создаётся отдельный редактор схем или API для регистрации манифестов.

Для совместимости запрос объявляется как `manifest.requests[]` с ключом, подписью, ключом существующего объектного `arguments`-поля и списком допустимых назначений. Это поле аргументов помечается `x-editor-managed-by-site`; его значения формирует редактор/движок, а пользователь не редактирует объект вручную. Размещённая фича читает объект из того же `arguments` map, что и остальные настройки. Сборки без `requests` остаются неизменными. Разрешённая runtime-конфигурация имеет ограниченную форму: `baseUrl` опущен для текущего origin, `path` содержит уже безопасно закодированные сегменты, `query` содержит значения/массивы для повторяющихся ключей, `body` содержит типизированные поля и `headers` только для объявленных несекретных заголовков. Метод, сериализация тела в HTTP, вызов `fetch`, ответ и его ошибки остаются в коде фичи.

В каждом request slot manifest задаёт destinations (`path`, `query`, `body`, `header`), тип значения и разрешённые виды источников. Ключ destination стабилен и не вводится произвольным пользовательским выражением. Для `path` destination является именованным placeholder в endpoint path (`{name}`); движок заменяет его единичным URL-encoded сегментом. Пользователь может выбирать только тип источника и его ключ среди route captures, входных query keys, объявленных feature arguments и публичных констант выбранного снапшота.

Публичные константы снапшота представлены простыми типизированными значениями, а не исполняемым кодом или произвольным JSON. Секретные имена констант и заголовков (например, authorization, cookie, token и password) запрещены. Текущая схема поддерживает простые значения и массивы строк; объекты и null не являются источниками для запросов.

Манифест задаёт поля интерфейса и аргументы, которые ожидает фича; движок не исполняет описанный в манифесте запрос и не выводит из ответа новую схему контента. Метод запроса, сериализация, типизация/проверка ответа, загрузка, ошибка и fallback остаются в самой фиче. Путь может быть настраиваемым значением инспектора, если манифест фичи разрешает такую настройку.

Полная матрица передачи аргументов:

- Источники: именованный параметр пути текущего маршрута; входной query-параметр текущего URL; значение аргумента фичи; константное публичное значение снапшота.
- Назначения, которые фича может объявить в своём контракте: сегмент/плейсхолдер пути backend, outgoing query-параметр, поле JSON body и несекретный HTTP-заголовок.
- Значения query должны сохранять повторяющиеся ключи и массивы, когда это поддерживает аргумент фичи; пустые, отсутствующие и некорректные значения должны различаться.
- Связь редактируется контролами без кода: пользователь выбирает источник и ключ, а затем допустимое назначение из контракта фичи. Админка не принимает JavaScript, выражения или произвольную регулярку.
- Для значений пути используется существующее именованное извлечение параметров маршрута. Произвольный regex в интерфейсе не нужен; манифест может объявить поддерживаемые аргументы, а редактор выбирает уже разобранный параметр маршрута.

## Runtime и HTTP

Runtime передаёт фиче настройки выбранного backend и разрешённые значения связи через существующий контракт аргументов и lifecycle. Новые ABI и скрытое изменение ABI не вводятся; фича объявляет request slots в своём текущем manifest contract, а старые фичи без request metadata продолжают работать без изменений.

Запрос выполняется браузером посетителя из кода фичи. Для абсолютного origin внешний backend обязан разрешать реальный origin сайта через собственную CORS-политику. CSP страницы добавляет только точный connect-src origin зарегистрированного backend, который фактически привязан к размещению текущей страницы. Origin повторно проверяется сервером; wildcard, credentials, path, query и fragment не разрешены. Same-origin bindings не добавляют внешние источники. Значение base URL хранится в объекте аргументов, который помечен x-editor-managed-by-site; UI не позволяет редактировать его напрямую, а сервер отвергает попытки задать этот аргумент обычной привязкой. Для относительного пути используется origin текущей ветки/сайта, обычно через gateway. Engine не становится прокси, не добавляет серверный доступ к произвольному host и не передаёт секретные заголовки.

Валидация принимает только `http`/`https` для зарегистрированных адресов и относительные HTTP-пути для «Текущий сайт»; URL с user-info, скриптовыми схемами и фрагментами отклоняются. В продакшн-развёртывании публичные backend должны использовать HTTPS. Ответы внешнего backend не сохраняются движком в контент или базу.

Пререндеринг никогда не получает право обращения к origin backend. Существующий worker остаётся ограничен origin движка и разрешёнными публичными same-origin путями. CSP-разрешение точного внешнего origin применяется только к документу, отдаваемому посетителю; сетевые ограничения Chromium worker не меняются. Страницы с хотя бы одним включённым request slot, привязанным к зарегистрированному внешнему backend, пропускают Chromium prerender и HTML cache и отвечаются с Cache-Control: no-store; это исключает SSRF через приватный адрес и устаревшие данные стороннего API. Относительные same-origin запросы продолжают работать в существующем worker allowlist и могут пререндериться по обычной cache-политике. Расширение worker до внешних origins в этом объёме не выполняется.

## Ошибки, доступ и конкурентность

- Снапшот с неизвестным backend ID, отсутствующим route/query значением или несовместимым назначением не должен молча подменять значение; runtime показывает контролируемую ошибку/состояние, предусмотренное фичей.
- Сайт-менеджер может редактировать backend registry и связи; остальные роли сохраняют существующие ограничения маршрутов и `can*` checks.
- Конкурирующее структурное сохранение создаёт независимые дочерние снапшоты от выбранного родителя, как прочие изменения сайта. Удаление backend с зависимыми размещениями блокируется или требует их явного перепривязывания.
- URL, query-значения и параметры пользователя не должны попадать в чувствительные серверные логи; feature-разработчик отвечает за безопасное отображение и обработку недоверенных ответов.

## Критерии приёмки

1. В настройках сайта создать два backend с автогенерируемыми ID; сохранить потомок, перезагрузить страницу и подтвердить восстановление реестра без изменения назначения веток.
2. В инспекторе фичи выбрать зарегистрированный backend, указать путь, связать route capture с path-параметром и входной query с outgoing query; проверить arrays, повторные query keys, пустое и отсутствующее значение.
3. Проверить режим «Текущий сайт» без зарегистрированного домена: relative path идёт на origin страницы с сохранённым query/body mapping.
4. На реальной фиче убедиться, что запрос написан в коде фичи, ответ преобразован в её собственную модель и Engine не сохраняет сторонний response как контент.
5. Проверить типовые 4xx/5xx, CORS, timeout, недоступный backend, неизвестную конфигурацию и повторное открытие сохранённого снапшота.
6. Удаление используемого backend показывает зависимые размещения; выбор backend и изменение пути не затрагивают родителя и другие снапшоты.
7. Проверить, что UI и runtime не публикуют секреты и отклоняют небезопасные URL; для внешнего backend проверить действующую CORS-политику.
8. Проверить пререндер на same-origin запросе и на зарегистрированном внешнем origin, ограничения worker, актуальность/TTL кеша и интерактивность после загрузки JS.
9. Собрать все Vitasil ZIP без изменения их текущих схем ответа; отдельная тестовая feature демонстрирует path + query bindings. Рабочие Vitasil API, БД и хранилище в QA не используются.
10. Проверить, что браузерный CSP разрешает только точные публичные origins, реально используемые размещениями страницы; same-origin bindings не расширяют connect-src, а Chromium worker не получает эти origins.

## Контракт сериализации

- `SiteConfig.backends` содержит `{id, name, baseUrl}`. `id` — UUID, создаётся автоматически интерфейсом Website Engine и не редактируется пользователем; `baseUrl` — origin без path (кроме `/`), user-info, query и fragment.
- `SiteConfig.publicConstants` содержит список `{key, label, type, value}`; `type` — `string`, `number`, `integer`, `boolean` или `string-array`. Ключи валидируются и уникальны.
- `Placement.backendRequests` — объект по ключу `manifest.requests[].key`; каждая запись содержит `backendId` (пустой для текущего сайта), относительный `path` и список `{destination: {kind, key}, source: {kind, key}}`.
- Unknown backend ID, request slot, source, destination, placeholder и несовместимый тип отклоняются при проверке snapshot; связь с отсутствующим runtime query/route value возвращает фиче контролируемый код ошибки в request argument, не подменяя её пустой строкой.
- Manifest request slot ссылается на заранее объявленное объектное поле `arguments`, помеченное `x-editor-managed-by-site: true`. Разрешённая конфигурация помещается внутрь этого поля; новый `FeatureContext` key и ABI не добавляются.
- HTTP заголовки и аргументы остаются публичными. Для абсолютного backend требуются HTTPS и корректный CORS со стороны API в production.

## Оставшиеся проверки

- Проверить CORS и сетевые ошибки с API целевого внешнего провайдера в отдельной контролируемой среде. Браузерная acceptance с настоящим loopback HTTP listener теперь проверяет CORS браузера, но не политику конкретного провайдера.
- Существующие Vitasil manifest и ABI не требуют изменений; все девять актуальных ZIP проверены read-only.

### Acceptance для фактического браузерного CORS

Дополнительная изолированная браузерная проверка использует два локальных `httptest` listener-а на случайных loopback-портах. В конфигурации snapshot им соответствуют отдельные public-looking DNS origins, которые Chromium явно разрешает на `127.0.0.1` через `--host-resolver-rules`; запросы не выходят в Интернет, а production-код по-прежнему отклоняет literal loopback/private IP. Разрешён только первый точный origin из snapshot binding.

Chromium с загруженной feature должен получить публичный runtime document с `connect-src 'self' <точный origin>`, успешно выполнить cross-origin GET при точном `Access-Control-Allow-Origin` Engine origin, получить browser network error при отсутствии совпадающего CORS origin, и восстановить запрос после retry. Дополнительный fetch к контролируемому, но не зарегистрированному origin блокируется CSP до обращения к listener-у. Отдельные режимы backend проверяют HTTP 503 и `AbortController` timeout; ответ сервера после отмены запроса не должен менять runtime state. Это проверяет настоящие браузерные CORS/CSP/network границы на контролируемом endpoint, но не политику внешнего провайдера.

Критерии: `TestExternalBackendActualCORSAndRetryBrowserAcceptance` должен пройти на новой disposable PostgreSQL с именем `website_engine_external_backend_browser_qa_*`, Chromium и локальным Playwright; счётчик listener-а не зарегистрированного origin остаётся нулевым. Тест не меняет API/ABI, Vitasil и пользовательские данные. После проверки контейнер и временные outputs, созданные именно для этого прогона, удаляются после проверки отсутствия активных подключений.


## Проверка реализации

Проверено 7 ноября 2026 года. В disposable PostgreSQL `website_engine_external_backend_qa_20261107_01` на loopback-порту 35442 до каждого запуска подтверждено отсутствие других сессий. Объектное хранилище было in-memory fake из integration harness. Рабочая Vitasil, её API, БД, storage и volumes не использовались для записей.

- Go unit tests подтверждают проверку публичных URL (credentials, localhost/private/special IP и malformed origin отклоняются), request manifest/schema, source/destination type mapping, повторяющихся query values, пустых/отсутствующих значений и экранирования path segments.
- Integration/runtime test создал root snapshot и независимый child, отверг удаление используемого backend, сохранил назначение ветки, проверил stale revision conflict и подтвердил HTTP 404/noindex/no-store для catch-all, явно назначенного в systemPages[404].
- Браузерная проверка с Chromium/Playwright проверила same-origin runtime GET и три мокированных GET на точный зарегистрированный public origin (200, 404, 500); при отсутствующем query source feature показала контролируемую ошибку без внешнего GET. Для external backend HTML получил no-store без ETag и не запускал Chromium worker. CSP разрешил только используемый origin.
- В админке добавление backend сохранилось отдельным дочерним snapshot; инспектор восстановил source bindings маршрута и query, изменение backend/path создало следующий child, родительский snapshot и branch assignment остались прежними.
- Без изменений ZIP/manifest/ABI проверены все актуальные архивы Vitasil через VerifyArchive: background 1.0.6, catalog 1.0.9, catalog-detail 1.0.8, footer 1.0.4, form 1.0.9, header 1.0.7, hero 1.0.10, main-data 1.0.9 и not-found 1.0.6.

Команды scoped checks:

```sh
go test -count=1 ./features/sitebuilder ./pkg/runtime
go vet ./features/sitebuilder ./pkg/runtime ./cmd/integration
TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds go test -count=1 ./cmd/integration -run '^TestExternalBackendBindingsVitasilArchiveCompatibility$' -v
TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds go test -count=1 ./features/sitebuilder -run '^TestExternalFeatureDesignArchives$' -v
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:35442/website_engine_external_backend_qa_20261107_01?sslmode=disable' TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds PRERENDER_TEST_NODE=/Users/docup/.nvm/versions/node/v20.19.4/bin/node PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' go test -count=1 ./cmd/integration -run '^TestExternalBackendBindingsDisposableAcceptance$' -v
```

The browser acceptance command used Chromium 1252 and the Playwright module under `prerender/node_modules`; the synthetic external origin was intercepted by Playwright and no real external API or CORS policy was contacted.

Two test-fixture corrections were required: the first route fixture mapped parameter paths with different captures to one page, so the 404 override was moved to a separate static page; a browser locator initially matched the route card and two action buttons, so keyboard navigation now targets the first accessible route button. The initial failed fixture run is not a pass; the corrected targeted integration and browser run passed.

Limits: the target provider's CORS policy and API behavior remain untested; Vitasil ZIPs do not currently declare external backend bindings. Stale branch assignment conflict was checked through the service CAS, not a browser-driven conflict dialog. The catch-all override test asserts HTTP status, noindex and no-store for an empty not-found page; a feature-rendered custom 404 body was not separately asserted.

### Фактический CORS браузера на контролируемом backend — 7 ноября 2026

На disposable PostgreSQL `website_engine_external_backend_browser_qa_20261107_01` в одноразовом контейнере `website-engine-external-cors-qa-20261107-01` (loopback port 35488, tmpfs data, без volume) Chromium прошёл `TestExternalBackendActualCORSAndRetryBrowserAcceptance`. Перед интеграционным запуском проверялись `current_database()` и отсутствие других подключений; тестовый service был создан в Engine `httptest` harness. Рабочий Vitasil, его API, БД, storage и volumes не использовались.

Два public-looking origins с разными случайными loopback listener-портами маршрутизировались Chromium на `127.0.0.1` только флагом `--host-resolver-rules`. Зарегистрированный exact origin был доступен через CSP, а второй, не зарегистрированный в snapshot, блокировался browser `connect-src` ещё до попадания в свой listener (0 запросов). Для зарегистрированного origin браузер получил успешный GET только когда контролируемый backend возвращал точный `Access-Control-Allow-Origin` origin движка. Отсутствующий и чужой CORS origin дали browser `network-error`; retry после каждого отказа завершился успехом. Отдельно проверены читаемый HTTP 503, `AbortController` timeout и retry после timeout. Документ с внешней привязкой остался `no-store`, без ETag и без запуска prerender worker. Контролируемый backend подтверждает поведение браузера и сервера в изоляции, но не реальную политику третьей стороны и не использование bindings конкретными ZIP Vitasil.

Проверки:

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:35488/website_engine_external_backend_browser_qa_20261107_01?sslmode=disable' PRERENDER_TEST_NODE='/Users/docup/.nvm/versions/node/v20.19.4/bin/node' PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' go test -count=1 -v ./cmd/integration -run '^TestExternalBackendActualCORSAndRetryBrowserAcceptance$'
go test ./features/sitebuilder -run 'Test(ValidatePublicBackendBaseURL|ManifestValidatesRequestSlot|SiteConfigValidatesBackendReferences|ResolveBackendRequest|ResolveRequestPath|PublicBackendOrigins)' -count=1
go vet ./features/sitebuilder ./cmd/integration
git diff --check -- cmd/integration/external_backend_bindings_browser_acceptance_test.go docs/SPEC-external-backend-bindings.md
```

Результат: браузерный acceptance, focused backend-binding tests, `go vet` и whitespace check завершились с кодом 0. Вызовы к внешним Интернет-API не выполнялись.
