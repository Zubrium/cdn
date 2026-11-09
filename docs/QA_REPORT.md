# QA-отчёт

Дата: 2026-11-02. Проверка охватывает React-админку и затронутые backend-сценарии снапшотов. Это отчёт о выполненных проверках, не подтверждение полной production-readiness или WCAG-сертификации.

## Актуализация публичного runtime — 9 ноября 2026

После исторической проверки ниже начат перенос публичного runtime из frontend-пакета в `pkg/runtime`: Go templates формируют документ, ABI-скрипты расположены в `pkg/runtime/scripts/`, публичные стили — отдельно от админки в `pkg/runtime/styles/`, source assets — в `pkg/runtime/assets/`. Старые отчёты о frontend-пакете `base` и его `index.html` описывают предыдущую сборку и не являются подтверждением нового состояния. 9 ноября текущий `cmd/api` был собран из рабочего дерева и поднят на отдельном QA-стеке с сохранёнными QA-томами; targeted HTTP-запросы главной и каталога вернули 200, оба ответа содержали `X-Engine-Prerender: hit`. Полный переход и его acceptance остаются открытыми.

Изменения исходников Vitasil и Engine здесь проверяются отдельно. Основной рабочий стек Vitasil, его API, БД, storage и volumes не использовались. В этом QA-проекте разрешён только собственный disposable стек `vitasil-current-20261109`; его контейнеры были удалены и затем восстановлены из сохранённых project volumes. Первичный запуск по исходному QA-конфигу сообщил `database migrations applied`; последующий запуск обновлённого app image использовал `MIGRATE_ON_START=false`. Не переносить этот результат на основной стек Vitasil.

### Точечное восстановление публичного renderer — 9 ноября 2026

После восстановления QA-проекта старый app image продолжил отвечать 500 на `/` и `/catalog`, тогда как `/api/v2/runtime` возвращал 200. Read-only вызов текущего `SnapshotService.PageDocument` на том же активном snapshot также вернул 200. Для проверки реального HTTP пути собран новый Linux/arm64 `cmd/api` из текущего исходного дерева и добавлен поверх сохранённого app image как `website-engine-vitasil-current-20261109-ssrfix:local`; `runtime-engine-override.yaml` закрепляет этот image и отключает миграции для последующих запусков. Сохранённый базовый image оставлен как rollback.

После замены только app-контейнера, без удаления и пересоздания именованных volumes, `GET /` вернул 200 (146521 bytes), `GET /catalog` — 200 (124152 bytes); оба ответа получили `X-Engine-Prerender: hit`, title `ВитаСил`, `Cache-Control: no-store`. Healthcheck контейнера прошёл. Подтверждена корректная отдача двух маршрутов после исправления renderer image; это не acceptance и не проверка браузерной интерактивности. В полном сценарии проверка не запускалась по указанию пользователя. Первичный полный `docker buildx build` не прошёл из-за Docker Hub authorization EOF; для точечной проверки использован локальный Go build и слой поверх уже имеющегося image.

QA-конфигурация и запуск: `/tmp/vitasil-current-20261109-run/compose.yaml`, `/tmp/vitasil-current-20261109-run/runtime.env` и `/tmp/vitasil-current-20261109-run/runtime-engine-override.yaml`; `docker compose -p vitasil-current-20261109 --env-file /tmp/vitasil-current-20261109-run/runtime.env -f /tmp/vitasil-current-20261109-run/compose.yaml -f /tmp/vitasil-current-20261109-run/runtime-engine-override.yaml up -d --pull never`. Порты: admin 28120, preview 28121, website 28122, MinIO 29100/29101. Данные в отдельных QA volumes сохранены.

### Актуальные ZIP Vitasil в QA-снимке — 9 ноября 2026

В тот же disposable проект зарегистрированы ZIP `catalog@2.0.26`, `hero@2.0.15`, `main-data@2.0.16` и `not-found@2.0.5` (HTTP 201); неизменившиеся `background@2.0.5`, `footer@2.0.14`, `form@2.0.8`, `header@2.0.11` и `legal-documents@1.0.7` уже присутствовали в реестре. Создан дочерний snapshot `ed782da9-cfca-41df-a7c0-d4d85d523d17`, родитель `36abe8ee-53ed-4da1-9e6c-b47c71484efa`; в нём 9 выбранных feature builds, 7 страниц, без страницы `product`, маршрута `/catalog/:slug`, `catalog-detail` и `detailLink`. Для `catalog@2.0.26` удалено только сопоставление `products.slug`, которого больше нет в typed input контракта каталога. Данные модели и значения 21 записи сохранены.

При создании ребёнка API предупредил о шести устаревших selection entries для удалённых placements страницы товара (`header-product`, `footer-product`, `legal-footer-product`). Удалены только эти placement-scoped выборки; значения записей и родительский snapshot не менялись. Черновик ребёнка сохранён и опубликован (`revision=7`, `contentRevision=2`); повторный GET snapshot вернул пустой `warnings`. Ветка `website` назначена ребёнку отдельным явным вызовом branch-assignment API с revision check; новое назначение имеет revision 3.

Точечный HTTP smoke после назначения: `/` — 200, `X-Engine-Prerender: hit`; `/catalog` — 200, `X-Engine-Prerender: hit`; HTML parser увидел один `<picture>`, ноль breadcrumb элементов и ноль ссылок вида `/catalog/...`; `/catalog/probif` — 404, `noindex,nofollow`, `X-Engine-Prerender: hit`. Все ответы имеют `Cache-Control: no-store`, поскольку metadata этого QA-снимка не индексируемая. Это проверка трёх изменённых маршрутов и SSR/prerender, не полный browser acceptance. Админский UI Feature Lab, формы, history/back-forward, Lighthouse и остальные маршруты в этом запуске не проверялись по просьбе пользователя.

## Текущий implementation-first статус — 8 ноября 2026

Согласованная кардинальность `many`/`singleton` реализована и проверена: singleton создаёт одну пустую draft-запись, очистка сохраняет ID, сервер соблюдает кратность, typed `record`/`scalar` разрешается автоматически, `collection` остаётся массивом 0–1; история many сохраняется, несовместимый restore отклоняется. Фокусные API/browser, ER keyboard, model-backed content, роли/ревизии, текущие Vitasil ZIP, files/S3, Sitepack, design tokens и SEO/prerender acceptance прошли на disposable средах. Рабочий Vitasil API/БД/storage не использовался.

Финальные статические проверки завершились успешно: полный `go test -count=1 ./...` без DB env, `go vet ./...`, Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (27 файлов, 140 тестов), `pnpm build`; отдельный полный `cmd/integration` и namespace-specific интеграции прошли на изолированных БД/MinIO. Холодный `PageIndex` для 50 000 Vitasil Product измерен как 24,935 мс, warm cache — 1 мс. Текущие browser results, cleanup и внешние/ручные ограничения собраны в разделах ниже; рабочие продуктовые данные не затрагивались.

### Выполненная часть общего QA-прогона — 8 ноября 2026

После implementation sweep собран актуальный frontend bundle командой `PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm build`; TypeScript-проверки, production admin build и public runtime build прошли. Это обновило только сборочные артефакты `pkg/static/dist` и `pkg/runtime/dist`, нужные встроенному integration-серверу. Затем `pnpm test` прошёл: 27 файлов, 140 тестов; `pnpm lint` и `pnpm typecheck` прошли отдельно. `env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_FEATURE_ZIP -u TEST_S3_ENDPOINT -u TEST_S3_BUCKET go test -count=1 ./...` и `go vet ./...` прошли.

На disposable PostgreSQL-контейнере `website-engine-plan-qa-pg-20261108-r1` (loopback `127.0.0.1:35611`) и отдельном MinIO `website-engine-plan-qa-minio-20261108-r1` (API `127.0.0.1:35612`, console `127.0.0.1:35613`) завершились успешно:

- `TestModelContentSingletonAPIAcceptance` и `TestModelContentSingletonBrowserAcceptance` на `website_engine_model_content_qa_20261108_r1`; browser проверил дочерний snapshot, независимое назначение ветки, автосоздание одной пустой черновой записи, autosave, подтверждённую очистку с сохранением ID, отдельную публикацию и runtime без утечки draft.
- `TestDataModelEditorKeyboardRenameReorderBrowserAcceptance` на `website_engine_data_model_keyboard_qa_20261108_r1`; проверены стабильные ID при переименовании, порядок полей, связи, видимый фокус после перемещения/добавления/удаления и ширины 360/390/768/1280 px.
- `TestVitasilAllCurrentPlacementsBrowserAcceptance` на `website_engine_vitasil_all_browser_qa_20261108_r1` с текущими девятью Vitasil v2 ZIP; Chromium открыл главную, каталог, товар detail и not-found, проверил каждый placement/slot, отсутствие browser errors и file upload/branch assignment controls в `/site` и `/content`.
- `TestVitasilSiteArgumentsV2BrowserAcceptance` на `website_engine_vitasil_site_arguments_browser_qa_20261108_r1`; проверены структурные аргументы каталога, вложенные значения и массивы аргументов, типизированное сопоставление полей Product, выбор существующего файла в media picker, отсутствие загрузки из `/site`, отображение значения отдельно в `/content` и неизменность назначения ветки.
- `TestModelSitepackCrossInstallAcceptance` на отдельных БД `website_engine_sitepack_cross_install_qa_src_20261108_r1` и `website_engine_sitepack_cross_install_qa_dst_20261108_r1`, bucket `sitepack-cross-install-qa-20261108-r1`; перенос модели, записей/истории, типизированных media/ACL, side-effect-free preflight и version conflict прошёл.

Устаревшие browser-fixture ожидания исправлены: собирать embedded frontend перед браузерными проверками обязательно; доступные имена required arguments включают метку «обязательно»; all-placement fixture теперь связывает `footer.contacts` из актуального v2 manifest и не отправляет retired поле `contactNote`. Локальные тесты и browser acceptance выше повторно прошли после этих исправлений. Текущие ZIP собраны из Vitasil в `/tmp/website-engine-vitasil-v2-qa-20261108-r1`; рабочие Vitasil API, DB, storage и volumes не использовались.

Полный `go test -count=1 ./cmd/integration` на `website_engine_plan_qa_20261108_r1` завершился успешно после повторного запуска. В первом запуске вывод не успел появиться до обновления инструкций; этот запуск не учитывается. Singleton API acceptance вместе с историей/sitepack/запретом несовместимого restore прошёл на `website_engine_model_content_qa_20261108_r1`. Schema checksum также проверяется тестами `./migrations`. После проверки нулевых подключений удалены task-owned контейнеры `website-engine-plan-qa-pg-20261108-r1` (порт 35611) и `website-engine-plan-qa-minio-20261108-r1` (порты 35612–35613), а также временная директория `/tmp/website-engine-vitasil-v2-qa-20261108-r1`; у них не было привязанных volumes.

Read-only Docker-проверка целевого `vitasil-admin-latest-20261107` обнаружила старый image `website-engine-vitasil-latest-20261107:local` в restart loop (133 перезапуска); его PostgreSQL и MinIO остановлены. Legacy DB volume `vitasil-admin-latest-20261107_postgres-data` и media/release data не изменялись. Новая clean-baseline схема не подключалась к legacy БД. Переключение на новую сборку заблокировано отсутствием безопасной актуальной БД/S3 и непроверенным переносом содержимого; экземпляр оставлен как есть. Отдельный сломанный task-owned QA stack `vitasil-current-qa-20261108` с собственными остановленными PostgreSQL/MinIO и restart-loop application container очищен: удалены три его контейнера, точные volumes `vitasil-current-qa-20261108_engine-data`, `vitasil-current-qa-20261108_minio-data`, `vitasil-current-qa-20261108_postgres-data`, каталог `/tmp/vitasil-current-qa-20261108` и неиспользуемый образ `website-engine-vitasil-content-fix-20261108:local`. Основной product stack и его volumes сохранены.

Рабочий экземпляр и отдельные старые QA/production listeners не изменялись. Полная доступность/screen-reader, field CWV/Search Console, production gateway/CDN/S3 и большой sitemap остаются внешними/ручными ограничениями, указанными в TODO.

## Автоматические проверки

Из `frontend/`:

- `pnpm typecheck` — TypeScript приложения и конфигурации Vite, успешно.
- `pnpm lint` — ESLint с `--max-warnings=0`, успешно.
- `pnpm test` — 2 файла, 10 тестов, успешно.
- `pnpm build` — обе TypeScript-проверки, production build админки и отдельного public runtime, успешно.

Из корня репозитория:

- `go test ./...` — успешно на отдельной проверочной PostgreSQL; интеграционные тесты снапшотов и содержимого прошли.
- `go vet ./...` — успешно.
- `git diff --check` — успешно.

## Браузерная проверка

Использовалась собранная админка на отдельном локальном Go backend с disposable тестовой базой. Основной локальный preview также обновлён. По интерфейсу выполнялись только безопасные переходы и чтения; тест незаписанных изменений ограничивался локальным состоянием формы. Снапшоты, сохранения, публикации, развёртывания, удаления, импорты, экспорты и отзыв токенов из UI не запускались. Для отсутствующих submission/feature ID ожидаемые GET-ответы 404 проверяли экран восстановления.

| Область | Проверка и результат |
| --- | --- |
| Responsive | 24 адреса/состояния × 7 ширин = 168 проверок в тёмной теме: 360, 390, 768, 1024, 1280, 1440 и 1920 CSS px. Во всех случаях `scrollWidth` не превышал клиентскую ширину. |
| Справка | Единый Tooltip проверен наведением, нажатием и клавиатурным фокусом; Escape закрывает подсказку. На 320/360/390 px блок остаётся в границах окна с 8 px боковым отступом и не перекрывает меню, заголовок или вкладки дизайн-системы. Эмуляция физического сенсорного устройства не проводилась. |
| Редактор маршрутов | Мобильная подсказка проверена на 320×568 px; содержимое и document width остаются в пределах viewport. Сохранение маршрутов не выполнялось. |
| Свёрнутая навигация | Tooltip пункта «Снапшоты» при 1024×768 px находится внутри viewport. Активный пункт сохраняет квадратную форму, разделители категорий видны. |
| Дизайн-система | Light/dark, переключение категорий, редактируемые поля и отсутствие iframe предпросмотра. Изменения токенов не сохранялись. |
| Снапшоты | Активные dev/production версии, история, создание и независимые действия окружений проверены по интерфейсу. Создание и deploy не выполнялись. Backend-семантика выбора любого снимка, immutable content и прямого deployment покрыта Go integration tests. |
| Несохранённые данные | Метаданные и редакторы предлагают остаться или явно покинуть экран; незаписанный тестовый текст оставался локальным и серверу не отправлялся. |
| Оболочка и маршрутизация | Один знак/бренд в sidebar, одна кнопка выхода без профиля, отсутствует пункт «Модели данных», `/admin/features` ведёт к контенту/реестру по существующим правилам. |

## Скриншоты

Viewport-скриншоты финальной сборки сохранены в `artifacts/ui-audit-final/`:

- `design-system-desktop.png`, `design-system-desktop-tooltip-dark.png`;
- `design-system-mobile.png`, `design-system-mobile-tooltip.png`;
- `route-canvas-mobile-tooltip.png`;
- `snapshots-desktop.png`, `snapshots-mobile.png`.

## Остаточные ограничения

- Реальные физические сенсорные устройства, screen-reader сценарии и полная проверка всех контрастных пар не использовались.
- Полная автоматическая оценка WCAG 2.2 AA не выполнялась.
- Заполненные стресс-коллекции на всех списковых экранах и нагрузочное тестирование не проводились.
- Admin entry остаётся около 521 KB raw / 164 KB gzip; дальнейший code-splitting можно оценить отдельно.
# Проверка изменений админки, 3 ноября 2026

Дополнение: в изолированной среде с PostgreSQL, S3-совместимым хранилищем и Go-сервером этой рабочей копии собраны девять ZIP Vitasil. Фича `header` загружена из ZIP; её поля и массив навигации отредактированы через админку, автосохранение подтверждено запросом к БД. Через UI выполнено назначение снапшота в production с подтверждением; проверены оба указателя в БД. На странице снапшотов проверена ширина 320 px и отсутствие ошибок консоли в этом сценарии. Остальные фичи собраны, но их пользовательские сценарии не проверены. Повторный полный прогон frontend и Go включает интеграционный тест с реальным ZIP `header`.

Дополнение к браузерной проверке: загружены ZIP `header`, `hero`, `catalog` и `main-data`. Для `hero` через UI загружен файл в изолированное S3 и сохранён его ID во вложенном поле; потеря сети, повторное ручное сохранение и конфликт ревизии проверены через UI. Обработка производных файлов не прошла: локальный FFmpeg не содержит encoder `libwebp`. После исправлений axe не сообщил нарушений на списке контента, форме `hero` и открытом мобильном меню; проверены также закрытие меню и возврат фокуса. Для формы `hero` сняты скриншоты и проверена ширина документа на 320, 360, 390, 768, 1024, 1280, 1440 и 1920 px. Это не заменяет визуальную регрессию остальных экранов. Интеграционный тест `TestExternalFeatureContentWorkspace` с реальным ZIP проверяет сохранение, конфликт, параллельные запросы, две дочерние версии, независимые назначения и откаты; для запуска нужны `TEST_DATABASE_URL` и `TEST_FEATURE_ZIP`.

Дополнительно в собранной админке проверен диалог при уходе с несохранённой правкой: действие «Остаться» сохраняет локальное значение. Роль `user` не получает доступ к прямому URL снапшотов, API отвечает 403. После исправления контраста в тёмной теме axe не обнаружил нарушений на открытой форме `hero`. Проверки не охватывают все роли, экраны и состояния.

Обработка медиа повторена в Docker-образе, собранном из текущей рабочей копии: содержащийся в нём FFmpeg поддерживает `libwebp`, повтор обработки завершился состоянием «Медиа подготовлено». Ошибка выше относится к локальному FFmpeg на хосте.

Для страницы снапшотов проверены восемь заданных ширин в обеих темах, отсутствие переполнения документа и переключение вкладок стрелкой. Найденный axe пропуск уровня заголовков исправлен; повторный axe в обеих темах не сообщил нарушений. Это не полный визуальный аудит или автоматизированный E2E всех требуемых состояний.

Продолжение: история каждой среды загружается страницами по стабильному курсору. Браузерная проверка на 55 тестовых событиях показала 50 записей на первой странице и все 55 после загрузки следующей; Production при этом показал собственную пустую историю. Сценарии настоящих ZIP `catalog` и `main-data` проверены через API и частично через UI: вложенные настройки каталога и массив с enum сохранены в БД. Вложенные объект и массив внутри строки другого массива дополнительно проверены на синтетической фиче через UI, БД и повторное открытие.

Открытые проверки и работы:

- [x] Добавить постраничный просмотр истории назначений независимо для каждой среды. Проверены 55 событий в UI, стабильный курсор при новой операции, изоляция сред и запрет для роли `user` в интеграционных тестах.
- [ ] Автоматизировать полный браузерный сценарий A/B/C, независимых откатов и повторного открытия контента с проверками UI, API и БД.
- [ ] Проверить все роли и legacy-маршруты, мобильное меню и свёрнутую панель во всех необходимых состояниях. Для `catalog` и `main-data` частично проверены формы и сохранение вложенных данных в UI и интеграционными тестами.
- [ ] Выполнить визуальную регрессию всех затронутых экранов и полный accessibility-аудит, включая ошибки, диалоги и обе темы.
- [ ] Повторить полный регрессионный набор после завершения оставшихся изменений; не считать частичные проверки подтверждением production readiness.

- Пункт «Файлы» перемещён рядом с «Главной». Требуется браузерная проверка мобильного и свёрнутого меню и ролей.
- На странице снапшотов добавлены переключение среды, история последних 200 назначений и отдельные действия развёртывания и отката. Реестр версий общий. Интеграционный тест проверяет историю и независимое назначение; постраничная история операций ещё не реализована.
- Редактор сохраняет локальные изменения, появившиеся во время запроса, и не повторяет сетевой запрос автоматически после ошибки. Неподдерживаемые конструкции схемы блокируют редактирование с сообщением. Проверка гонок и конфликтов в двух браузерных сессиях ещё необходима.
- Прошли frontend typecheck, lint, unit-тесты и сборка; `go test ./...`, `go vet ./...` и интеграционный набор на изолированном PostgreSQL. Интеграционные fixtures синтетические.
- E2E на реальных фичах Vitasil, S3, browser console/network, accessibility, темы и responsive-матрица в этой проверке не выполнялись. Готовность к production не подтверждена.

## Проверка реальной формы и фильтров заявок, 6 ноября 2026

На disposable-контейнерах `website-engine-vitasil-current-clean-qa-v2` (admin `38410`, preview `38411`, public `38412`) через UI добавлена Vitasil `form@1.0.4` в дочерний снапшот #5. Снапшот назначен только ветке `vitasil preview`; ветка `vitasil public` осталась ненастроенной. Заполнены и опубликованы тексты формы. Черновой заголовок пережил перезагрузку редактора; публичная форма сохраняла опубликованный заголовок, затем черновик был возвращён к опубликованному тексту.

Публичная форма приняла три тестовые заявки и показала текст успеха. В админке проверены фильтры по форме, ветке, статусу, имени/строке, дате и сортировке, изменение статуса, пустой результат, экспорт CSV с теми же тремя строками и ожидаемым порядком. Рабочий Vitasil API и его хранилища не использовались. Во время навигации публичная страница сообщила один console error: отсутствующий `/favicon.ico` (404); это не ошибка загрузки фич или формы и требует отдельной проверки runtime-документа.

Остаются непроверенными: отказ пустой/невалидной заявки как browser assertion, права разных ролей, архивирование и удаление, пагинация на нескольких страницах, полные сетевые ошибки, полная мобильная/accessibility-матрица и submission flow двух веток с разными назначенными снапшотами. Не считать этот частичный E2E полным подтверждением критерия заявок или production readiness.

Дополнительные проверки этого прохода: `pnpm typecheck`, `pnpm lint`, `pnpm test` (9 файлов, 40 тестов), `pnpm build`, `go test -count=1 ./...` (с `TEST_DATABASE_URL`, направленным на новую пустую disposable БД `website_engine_plan_run_20261106`, `TEST_FEATURE_DIR` с девятью ZIP Vitasil и `TEST_FEATURE_ZIP` для теста `header`) и `go vet ./...` завершились успешно. Интеграционный запуск мигрировал чистую схему и выполнил проверки реальных Vitasil-фич. Atlas CLI не установлен; актуальность `migrations/atlas.sum` проверена методом `migrate.Validate` библиотеки Atlas v1.3.0, совпадение подтверждено. `git diff --check` также прошёл. Эти результаты не закрывают перечисленные выше ручные и security-сценарии.

## Проверка внутреннего файлового API, 6 ноября 2026

На отдельной disposable PostgreSQL `files_qa` и MinIO `127.0.0.1:39000` прошли полный пакет `features/files` с `TEST_FILES_LARGE=1` и тестовыми Vitasil assets. Помимо unit/integration проверок пакет включал HTTP round trip через новый `/api/v2/files` и реальный S3: загрузка/подтверждение, metadata, изоляция владельцев, private read, Range, короткоживущая ссылка и её отзыв при удалении. Проверен также 32 MiB multipart S3 object, retry commit, private bucket и synthetic 5 GiB bounded-memory stream. Сам HTTP round trip сначала выявил несоответствие размера тестового chunk; тест исправлен на корректный размер, после чего отдельный тест и весь пакет прошли. Физический 5 GiB HTTP/S3 transfer этим общим запуском не выполнялся; он отдельно проверен тестом `TestActualS3HTTPFiveGiBTransfer`, описанным ниже. Браузерная и production приёмка остаются открытыми; актуальные пробелы перечислены в `SPEC-files.md` и TODO.

## Favicon публичного runtime, 6 ноября 2026

Добавлены исходная ссылка на `/favicon.svg` и runtime fallback на неё, когда настройки сайта не содержат собственного favicon. `pnpm build` и `go test -count=1 ./pkg/runtime` прошли. Playwright открыл собранный runtime bundle: HTML, JS, CSS и `/favicon.svg` вернули 200; запрос `/favicon.ico` отсутствовал. Smoke запускался на статическом Python HTTP сервере, поэтому ожидаемый запрос `/api/v2/runtime` там получил 404; backend и контентный runtime этим сценарием не проверялись. Ранее замеченный запрос за отсутствующим favicon устранён в текущей исходной сборке.

## Гонка удаления снапшота и назначения ветке, 6 ноября 2026

Интеграционный `TestSnapshotAssignmentRacesDeletion` выполнен на disposable PostgreSQL обычным и `-race` запусками. В восьми одновременных парах проверяется, что назначение первым приводит к `409`-совместимому конфликту удаления, а удаление первым приводит к `not found` при назначении без изменения предыдущего snapshot ID и ревизии ветки. Дополнительно проходят `TestConfiguredBranchesPublishForkAndProtectTree` и `TestSnapshotDeletionReparentsCopiesWithoutChangingPayload`; браузерное подтверждение удаления и визуальное отображение перепривязанных узлов остаются непроверенными.

После усиления `TestSnapshotDeletionReparentsCopiesWithoutChangingPayload` повторно прошёл интеграционный сценарий на disposable PostgreSQL. Он создаёт опубликованный контент и отдельный черновик, копирует оба состояния в дочерний снапшот, затем меняет и публикует контент дочернего снапшота, оставляя более новый несохранённый в публикацию черновик. После перепривязки через удаление среднего узла и затем корня опубликованное значение, черновое значение, revision и publishedRevision потомка остаются прежними. Команда: `TEST_DATABASE_URL='postgres://…/website_engine_plan_run_20261106?sslmode=disable' go test -count=1 ./cmd/integration -run '^TestSnapshotDeletionReparentsCopiesWithoutChangingPayload$' -v`. Проверка UI удаления и показа дерева не входит в этот тест.

## Повторный полный прогон и контейнер пререндеринга, 6 ноября 2026

На disposable PostgreSQL `website_engine_plan_run_20261106` повторно прошёл `go test -count=1 ./...` с `TEST_FEATURE_DIR` девяти ZIP Vitasil и `TEST_FEATURE_ZIP=header-1.0.5.zip`; `go vet ./...` также прошёл. Пакет file service отдельно прошёл с disposable PostgreSQL `files_qa`, MinIO, `TEST_FILES_LARGE=1` и тестовыми Vitasil assets. Frontend: `pnpm typecheck`, `pnpm lint`, `pnpm test` (9 файлов/40 тестов), `pnpm build`; обе сборки admin/runtime завершились. Для сборки Docker-образа и пререндер-smoke использован OrbStack: `docker build -t website-engine-prerender-goal:local .` и `docker run --rm --network none --security-opt seccomp=deploy/seccomp-prerender.json --entrypoint node -v "$(pwd)/scripts/prerender-container-smoke.mjs:/tmp/smoke.mjs:ro" website-engine-prerender-goal:local /tmp/smoke.mjs`; worker сформировал содержательный HTML с отключённой формой, Chromium sandbox включён. Это подтверждает проверки выше, но не закрывает все открытые браузерные, производительные и рабочие Vitasil сценарии.

## Физическая передача и потоковое скачивание 5 ГиБ через файловый HTTP API, 6 ноября 2026

`TestActualS3HTTPFiveGiBTransfer` прошёл на disposable PostgreSQL `files_qa` и выделенном MinIO `127.0.0.1:39000`. Тест передал 5 ГиБ нулевого потока по локальному HTTP в 80 частях по 64 МиБ, подтвердил SHA-256, server-side S3 composition и размер 5 ГиБ, прочитал последние 32 байта через Range, полностью скачал 5 ГиБ потоково и проверил его размер и SHA-256, удалил файл через API и подтвердил отсутствие итогового объекта и всех временных частей. Повторная передача, полное скачивание и проверки заняли 75 секунд; тест не создавал локальный файл на 5 ГиБ. Команда: `TEST_FILES_5GIB=1 TEST_FILES_DATABASE_URL='postgres://plan_runner:engine-plan-runner-qa-only@127.0.0.1:35432/files_qa?sslmode=disable' TEST_FILES_S3_ENDPOINT='http://127.0.0.1:39000' go test -count=1 ./features/files -run '^TestActualS3HTTPFiveGiBTransfer$' -v`. Это подтверждает серверный HTTP/S3 путь на локальном disposable storage; gateway, production S3 и browser UI требуют отдельной проверки.

После этого теста на том же disposable MinIO прошли полный `features/files` пакет с `TEST_FILES_LARGE=1` и тестовыми Vitasil SVG/WOFF2, его `-race` вариант и `go vet ./features/files`. На отдельной disposable БД `website_engine_plan_run_20261106` со свежими ZIP Vitasil повторно прошёл `go test -count=1 ./...`; `go vet ./...`, `gofmt -d features/files/service_test.go` и `git diff --check` завершились без замечаний. Рабочие контейнеры и данные Vitasil не затрагивались.

Только disposable `website-engine-vitasil-current-clean-qa-v2` перезапущен для проверки listeners. После старта admin readiness на 38410 вернул 200, назначенная preview-ветка на 38411 — 200, неназначенная public-ветка на 38412 — системный 503. Старые endpoints `/api/v2/admin/site/draft` и `/api/v1/admin/site/environments` в production branch mode вернули 404, а `/api/v2/admin/branches` без авторизации — 401. `TestConfiguredBranchesPublishForkAndProtectTree` проверяет отключение ветки при удалении из конфига и восстановление прежних ID и назначения; `TestBranchManagementHTTPRequiresManagerAndRevision` проверяет 401 для анонимного запроса, 403 для обычного пользователя, 409 для устаревшей ревизии без изменения состояния и успешное назначение manager-ом. После этих изменений прошли полный `go test -count=1 ./...` и `go vet ./...`. Рабочий Vitasil не запускался; `vitasil-db-1` остаётся остановленной. Ошибка при занятом listener-порту не проверялась.

## Дополнительный focused audit файлового API, 6 ноября 2026

Повторно прошли `go test -count=1 -v ./features/files ./presentation/http/handlers/files` и `go test -race -count=1 ./features/files ./presentation/http/handlers/files` на disposable PostgreSQL `files_qa` и MinIO `127.0.0.1:39000`; `go vet ./features/files ./presentation/http/handlers/files` тоже прошёл. Оба пакета проверили владельцев и сервисные разрешения, restart/resume и повтор commit, ротацию ключа с отзывом старого при повторной инициализации, quota/TTL/abort/cleanup, signed private Range/HEAD и отклонение удалённого файла, S3 composition/API round trip, роль админа, конкурентное удаление и новый строгий разбор `FILE_SERVICES`. ETag/условный `If-None-Match` отдельно не проверялись в том прогоне.

Следующий focused прогон закрыл этот пробел и исправил найденный parser bug: comma-separated entity-tags разбираются по запятым вне кавычек, поэтому валидный opaque-tag с собственной запятой совпадает. На отдельной temporary role/schema в disposable `files_qa` прошли `go test -count=1 -v ./features/files ./presentation/http/handlers/files` с `TEST_FILES_LARGE=1` и реальными Vitasil SVG/WOFF2, `go test -race -count=1 ./features/files ./presentation/http/handlers/files` и `go vet ./features/files ./presentation/http/handlers/files`. `TestHTTPConditionalRequestsRespectVisibilityAndCachePolicy` проверил публичный ETag/304/`public, no-cache`, weak/list/wildcard validators, stale и matching `If-Range`, приватный 304 только после проверки владельца с `private, no-store`, удалённый объект при старом ETag. `TestActualS3ServiceHTTPAPIRoundTrip` проверяет private owner Range/ETag, owner 304, отсутствие анонимного 304 и 304 через ограниченную ссылку на disposable MinIO. Синтетическая bounded-memory передача 5 GiB прошла; физический 5 GiB HTTP/S3 тест в этом прогоне пропущен, его отдельный pass выше не меняется. Test role удалена; рабочие Vitasil API, БД и storage не использовались.

В этой повторной команде не задавались `TEST_FILES_LARGE=1`, `TEST_FILES_5GIB=1` и `TEST_VITASIL_ASSETS_DIR`: bounded-memory 5 GiB service test, физическая 5 GiB HTTP/S3 передача и тесты реальных Vitasil SVG/WOFF2 были пропущены. Ранее прошедшие отдельные тесты этих сценариев описаны в разделах выше; текущий запуск — дополнительный focused прогон и не заменяет их. Production gateway/S3/CDN, полный process restart, ротация ключей на работающем production-процессе и браузерная галерея этим аудитом не проверялись. TODO файлового API остаётся частично выполненным.

## Повторная сборка Vitasil и состояние рабочей установки, 6 ноября 2026

В репозитории Vitasil из актуальных исходников прошли `npm run typecheck`, `npm run lint` и `npm run build` из `frontend/`. Построены девять ZIP `background@1.0.5`, `catalog@1.0.5`, `catalog-detail@1.0.4`, `footer@1.0.3`, `form@1.0.4`, `header@1.0.5`, `hero@1.0.6`, `main-data@1.0.4`, `not-found@1.0.3`; каждый ZIP прошёл `unzip -t`. После пересборки свежий набор прошёл `go test -count=1 ./cmd/integration` на disposable PostgreSQL с `TEST_FEATURE_DIR` и `header-1.0.5.zip` как fixture.

Рабочую установку Vitasil не изменяли. Последний read-only `docker inspect` подтвердил, что `vitasil-vitasil-1` находится в restart loop (`restartCount=928`, `exit=1`, `unhealthy`), а `vitasil-db-1` остановлен (`exit=0`). Более ранний просмотр логов показывал, что приложение не разрешает hostname `db`. Контейнеры используют сохранённые volumes `vitasil_postgres-data`, `vitasil_minio-data` и `vitasil_release-cache`; содержимое не читалось и не изменялось. Текущий Vitasil `.env` не содержит `ENGINE_PREVIEW_PORT`, обязательный для нового `compose.yaml`; полный `.env` и значения секретов не читались. `docker compose --env-file .env.example -f compose.yaml config --quiet` прошёл. Сервисы не запускались, так как запуск приложения может автоматически применить чистые миграции к неизвестной рабочей БД. Безопасное продолжение требует сначала подтвердить назначение и состояние `vitasil_postgres-data`, проверить backup или сделать копию, затем заполнить текущие параметры Compose и только после этого отдельно согласовать запуск на клоне. Изолированные тестовые сервисы Engine остаются healthy; у публичной ветки без назначения снапшота `GET /` ожидаемо возвращает 503.
## OrbStack: Docker образ и конфликты портов, 6 ноября 2026


В OrbStack заново собран `website-engine-prerender-goal:local`; сборка успешно включила Node, Playwright и Chromium. Размер образа — около 1.61 GB. Контейнерный prerender smoke прошёл с `--network none` и seccomp профилем: Chromium сформировал статический HTML, а форму оставил inert. `TestBindHTTPAddressesReleasesPartialBindingsOnConflict` прошёл и подтвердил, что при занятом listener-адресе startup останавливается с указанием адреса и освобождает ранее открытые listeners. Отдельно запущенный контейнер занял случайный host-port; второй одноразовый контейнер получил `port is already allocated`, после чего первый был остановлен и автоматически удалён. Compose прошёл структурную проверку с `.env.example` и `config --no-env-resolution`; полное разрешение service `env_file` требует подготовленного локального `.env`, которого в репозитории нет. Рабочие Vitasil containers и volumes в этих проверках не использовались.

## Реальный браузерный сценарий назначения снапшота, 6 ноября 2026

На свежем контейнере `website-engine-prerender-goal:local` с отдельной disposable PostgreSQL/MinIO прошли вход bootstrap-admin токеном, создание пустого корня, отказ от его назначения и создание потомка. В диалоге выбрана ветка `website`; назначение выполнено только после нажатия «Подтвердить назначение». После перезагрузки UI сохранил связь потомка и ветки. Публичный listener `website` ответил 200, неназначенный `preview` — системным 503; DevTools не зарегистрировал ошибок. Данные были созданы только в Compose-проекте `website-engine-prerender-qa-20261106`; в репозитории Vitasil ничего не менялось.

## Проверка удаления и перепривязки в UI, 6 ноября 2026

На свежем disposable Compose сервере созданы корень, потомок и внук; `website` назначена внуку. UI показал предупреждение перед удалением корня, перепривязал потомка в новый корень, сохранил связь внука и назначение ветки, а прежний непосредственный родитель остался защищённым от удаления. После операции API показал корень #1 отсутствующим, #2 без parent, #3 с parent=#2; `website` listener отвечал 200. `TestSnapshotDeletionReparentsCopiesWithoutChangingPayload` и `TestSnapshotAssignmentRacesDeletion` прошли на отдельной disposable PostgreSQL. Ошибок в консоли браузера не было.

## Повторная сверка целевой начальной схемы БД, 6 ноября 2026

Первичная заметка аудита выше по рабочей истории ошибочно прочитала структуру старой модели и устарела. Повторная проверка актуальной `migrations/0001_init.sql` подтвердила отсутствие `site_draft_config`, singleton state таблиц метаданных/дизайн-системы и `draft/dev/prod_version_id` колонок у `features`/`feature_entries`. `site_environments` остаётся таблицей для статически конфигурируемых веток, а не набором фиксированных dev/prod. `atlas.sum` проверяется в ходе `postgres.Migrate`.

На новой disposable БД `website_engine_branch_review_qa_20261106` прошли `TestStaticBranchRuntimeDoesNotSeedLegacyDrafts`, `TestBranchModeHidesLegacyGlobalFeatureAndContentAPIs` и `TestBranchManagementHTTPRequiresManagerAndRevision`; все три теста прошли после применения начальной миграции, затем тестовая БД была удалена. Рабочие базы Vitasil не изменялись.

## Проверка подтверждения назначения ветки, 6 ноября 2026

В админке текущий frontend checkout проверен через `/admin/snapshots/branches`, проксируя только к отдельному UI QA API и disposable PostgreSQL `engine_content_ui_qa_20261106`. Диалог проверен на ширине 360 px и широком desktop viewport: он показывает имя ветки/снапшота, проверенную content revision и то, что черновики не видны runtime. Для проверки предупреждения в snapshot #4 временно изменено опубликованное значение `main-data.info.title` с ожидаемой строки на число; UI показал точный feature/path warning и кнопку «Назначить с предупреждениями». Диалог закрыт через «Отмена»; ветка осталась назначенной #1. Тестовое значение восстановлено из backup-строки, backup-таблица удалена.

Браузер использовал UI QA серверную сборку, созданную до добавления серверной проверки `contentRevision`; в браузере выполнялись только чтения и отмена диалога. Новая проверка на стороне backend отдельно прошла через `TestBranchManagementHTTPRequiresManagerAndRevision`: отсутствие просмотренной ревизии отклоняется, устаревшая content revision даёт 409 без назначения, актуальная ревизия назначает снапшот. API назначения не отправлялся из браузера.

## Индексируемая Vitasil ветка: HTML и системный catch-all, 6 ноября 2026

Read-only запросы к disposable QA ветке `127.0.0.1:38192` подтвердили, что текущий worker отдаёт готовое содержимое главной, каталога, товара и формы (`X-Engine-Prerender: hit`); опубликованные записи каталога присутствовали в HTML. Главная выдала canonical `https://vitasil.example/` и `robots=index,follow`. `robots.txt` разрешил обход и указал sitemap; sitemap содержал `/`, `/catalog`, три опубликованных detail URL и `/form`. Соседняя preview ветка `38191` имела `noindex,nofollow`, `Disallow: /` и пустой sitemap.

Неизвестный `/does-not-exist` возвращается с 200, потому что payload текущего снимка задаёт обычный catch-all маршрут `^/(?<path>.+)$` на страницу `not-found`; `/api/v2/runtime` подтвердил совпавшие route ID/page ID и `X-Engine-Page-Status: 200`. Это конфигурация снимка, а не вывод из имени Vitasil-фичи. Отдельный интеграционный тест с реальным ZIP `not-found` уже проверяет системное назначение `systemPages[404]`, fallback для неизвестного маршрута и код 404. UI QA остановлена до записи: открытый редактор показывал локальное состояние «Изменено», поэтому его несохранённые данные сохранены и не тронуты. Полный Vitasil system-page E2E требует чистого отдельного снапшота с удалённым catch-all и явным `systemPages[404]`; задача остаётся открытой. Рабочая установка и данные Vitasil не изменялись.

## Закрытие legacy API в режиме статических веток, 6 ноября 2026

По коду текущего frontend оставлены только два нужных странице заявок read-only legacy вызова: список типов форм (`GET /api/v1/admin/features`) и версии схемы исторической заявки (`GET /api/v1/admin/features/{id}/versions`). В `UseBranches=true` сняты старые endpoint-ы глобального CRUD/публикации фич и записей, настройки и порядка записей, whole-site release import/activate/rollback и release sources. Новые snapshot/branch/content endpoints доступны. `TestBranchModeHidesLegacyGlobalFeatureAndContentAPIs` прошёл на disposable PostgreSQL и проверил эти маршруты; повторно прошёл полный `go test ./...`, `go vet ./...` и `git diff --check`. Рабочие данные Vitasil не использовались.

## Операторское переименование веток и файловых сервисов, 6 ноября 2026

Команды `go run ./cmd/api rename-branch 'branch api test' 'renamed cli test'` и `go run ./cmd/api rename-service 'rename cli test' 'renamed cli service test'` запускались с `DATABASE_URL`, направленным только на disposable `website_engine_plan_run_20261106`. SQL-проверка после rename подтвердила, что UUID ветки и service owner остались прежними. `TestConfiguredBranchesPublishForkAndProtectTree` повторно прошёл после усиления: сравнивает сохранение ID и snapshot assignment ветки после обновления имени конфигурации и повторной регистрации. `TestResumableIsolationCommitRetryRestartAndQuota` прошёл на отдельной disposable schema в `files_qa`, включая service rename, повторную инициализацию с обновлённым конфигом, сохранение owner ID и отзыв старого ключа. Команды проверены только на тестовых данных.

## Повторная полная проверка Go и Vitasil branch runtime, 6 ноября 2026

После перевода `TestExternalVitasilNotFoundFeatureOverridesSystemPage` с dev/prod fixtures на сгенерированную статическую ветку повторно прошла команда `TEST_DATABASE_URL='postgres://…/website_engine_plan_run_20261106?sslmode=disable' TEST_FEATURE_DIR='/Users/docup/Projects/vitasil/frontend/dist/feature-builds' TEST_FEATURE_ZIP='/Users/docup/Projects/vitasil/frontend/dist/feature-builds/header-1.0.5.zip' go test -count=1 ./...`; затем прошли `go vet ./...` и `git diff --check`. Сценарий регистрирует ZIP `not-found`, создаёт и назначает snapshot branch, публикует содержимое фичи и проверяет публичный HTTP 404/noindex ответ и composition. Все проверки используют disposable PostgreSQL. Текущие Vitasil browser/prerender проверка именно страницы 404 и очистка legacy миграционной схемы остаются открытыми.

После этого изменения OrbStack пересобрал актуальный образ командой `docker build -t website-engine-prerender-goal:local .`. Повторно прошёл контейнерный prerender smoke под `--network none` и seccomp: Chromium sandbox был включён, worker вернул содержательный статический HTML, а форма осталась inert. Рабочий Vitasil и его volumes не использовались.

## Vitasil system 404 с Chromium prerender, 6 ноября 2026

Тест `TestExternalVitasilNotFoundFeatureOverridesSystemPage` запущен на disposable PostgreSQL и реальном ZIP Vitasil `not-found` внутри OrbStack-образа. Он создал snapshot без catch-all, назначил его двум тестовым статическим веткам (indexable и preview), опубликовал текст ошибки, затем проверил через Chromium worker статический HTML, `X-Engine-Prerender: hit`, HTTP 404, `noindex,nofollow` и исключение `/missing`/страницы 404 из sitemap на обеих ветках. Тест прошёл. Первая попытка корректно вернула shell с `X-Engine-Prerender: unavailable`: Chromium runtime попытался загрузить same-origin `/favicon.svg`, отсутствовавший в allowlist. Путь разрешён только как статический ресурс; после пересборки `docker build -t website-engine-prerender-goal:local .` smoke снова прошёл без сети, с `deploy/seccomp-prerender.json` и активной sandbox: `Worker produced static HTML and an inert form with Chromium sandbox enabled`. Затем полный `go test -count=1 ./...` прошёл с disposable PostgreSQL и ZIP-фичами Vitasil, `go vet ./...` и `git diff --check` тоже завершились успешно. Полная визуальная браузерная матрица остаётся открытой. Рабочая БД и контейнеры Vitasil не использовались.

В bounded HTML cache заменено случайное вытеснение map-записей на LRU. Тесты проверяют сохранение часто используемой страницы, истечение TTL и объединение одновременных cache miss одного ключа; `go test -race -count=1 ./pkg/runtime` прошёл. Новый ключ использует эффективную собранную композицию, поэтому tracking query/order не создают отдельные варианты, а связанные аргументы, dynamic path и token URL темы остаются изолированы. `TestBranchSEOEffectiveRenderCacheIdentity` прошёл на disposable `website_engine_branch_cleanup_20261106`; он также проверил `index,follow` без query и `noindex,follow` с query. Перезапуск и межпроцессный кеш остаются открытыми. Полный Go-набор после этих изменений повторно прошёл 6 ноября на disposable PostgreSQL; новый запуск и Chromium сценарии записаны ниже. Остальные ограничения и contract details приведены в [SPEC-seo.md](SPEC-seo.md).

Опциональный Chromium-тест `TestPrerenderWorkerRendersAndRejectsSideEffects` выполнен внутри OrbStack production image с `--network none` и seccomp: same-origin `/favicon.svg` разрешён, POST и admin endpoint по-прежнему блокируются, итоговый HTML содержит опубликованный текст и inert-форму. Тест прошёл (16.34 s для трёх отдельных генераций); внешняя сеть и рабочий Vitasil не использовались.

## Конфликт локального черновика между вкладками, 6 ноября 2026

Для браузерной проверки собранный текущий admin bundle обслуживал `go run ./cmd/api` на отдельной PostgreSQL в tmpfs и disposable MinIO bucket `browser-qa-20261106`. Две вкладки открыли один workspace и snapshot key. Изменение этого ключа через вторую вкладку вызвало видимый alert в редакторе первой. Затем поле страницы изменили в первой вкладке; проверка показала, что конкурирующее значение в localStorage осталось нетронутым. Нажатие серверного сохранения не выполнялось. После проверки очищены localStorage и cookies Playwright-сессии, закрыты сессии и остановлена временная PostgreSQL. Рабочие API, данные и volumes Vitasil не использовались.

## Регистрация Vitasil ZIP без глобальной активации, 6 ноября 2026

`TestExternalVitasilFeatureBuildsRegister` прошёл с девятью текущими ZIP из `frontend/dist/feature-builds`. Для каждого зарегистрированного manifest ключ/версия совпали; SQL-проверка подтвердила, что `draft_version_id`, `dev_version_id` и `prod_version_id` остались пустыми. Регистрация только добавляет сборки в общий immutable registry; назначение версии остаётся частью payload снапшота. После перевода `TestCollectionRuntimeFiltersAndResolvesDetailRoutes` на snapshot-scoped публикацию повторно прошли полный `go test -count=1 ./...` с актуальными ZIP Vitasil и `go vet ./...`. Промежуточный полный прогон корректно выявил зависимость старого теста от `dev_version_id`; она устранена переходом тестового сценария на ветку и снапшот, не возвратом глобальной активации.

## Доступ к контенту закреплённых фич, 6 ноября 2026

Добавлены disposable integration-сценарии для контентного каталога и API. Сначала негативная проверка выявила, что пустая редакционная схема скрывалась в каталоге, но прямой GET workspace всё ещё возвращал 200. Серверная проверка теперь едина для каталога, чтения и сохранения: фичи без редакционных полей, настроек коллекции или form-контракта отсутствуют в каталоге, а прямые GET/PUT возвращают 404; формы и коллекционные настройки сохраняют поддержку по manifest. `TestSnapshotContentCatalogOmitsBuildWithoutEditorialSchema` проверяет чтение и запись для пустой схемы. `TestSnapshotContentRejectsUnselectedBuildAndKeepsParentContent` регистрирует версии 1.0.0/2.0.0, подтверждает использование закреплённой 1.0.0, отсутствие дочерних данных при исключении фичи, неизменность родительской публикации и 404 на чтение/запись дочернего workspace. Оба теста и полный `go test -count=1 ./...` прошли на disposable PostgreSQL `website_engine_plan_run_20261106` с ZIP Vitasil; `go vet ./...` и `git diff --check` прошли. Рабочие Vitasil API и данные не использовались.

## Фильтрация, экспорт и права заявок, 6 ноября 2026

Закрыл пробелы приёмки точечными integration assertions в `TestBranchSubmissionFiltersHistoryAndExport` и `TestAllFormsListSearchAndExportShareFilters`. Проверены форма и branch ID, независимые версии manifest для одной формы на двух ветках, общий список нескольких форм, дата с границей `[from,to)`, статус, поиск по полю из прежней схемы отключённой ветки, буквальные `%`/`_`, архив и паритет CSV со списком. Для сортировки asc/desc проверены одинаковые timestamps и ID tie-break; во время asc-пагинации между первой и второй страницей добавлена новая запись, не сместившая уже ожидаемые строки. Проверены одинаковые фильтры и полный лимитированный экспорт без усечения. Обычная роль `user` может читать/экспортировать, менять статус и архивировать, но не hard-delete; анонимное чтение отклоняется, moderator не может hard-delete, admin может.

Команды `TEST_DATABASE_URL=<fresh disposable PostgreSQL> go test -count=1 -run 'TestBranchSubmissionFiltersHistoryAndExport|TestAllFormsListSearchAndExportShareFilters|TestFormsLifecycleAndCSV' ./cmd/integration`, тот же запуск с `-race` и `go vet ./cmd/integration` прошли. Использован отдельный временный PostgreSQL-контейнер `website-engine-submissions-qa-20261106`; его новая пустая БД была удалена вместе с контейнером после проверки. Production code/API contract и рабочие Vitasil или общие QA данные не менялись.

## Повторные проверки Engine и Vitasil после исправления media publication, 6 ноября 2026

На disposable PostgreSQL `website_engine_site_draft_qa_20261106` прошёл полный `go test -count=1 ./...` с девятью текущими Vitasil ZIP (`TEST_FEATURE_DIR`) и `header-1.0.5.zip` (`TEST_FEATURE_ZIP`). `go vet ./...` прошёл. В `features/sitebuilder` сохранена типизированная ошибка `ErrRenditionsPending`; публикация возвращает 409, пока обязательные варианты изображения обрабатываются, и остаётся повторяемой после готовности. `TestContentDraftSaveWithSingleDatabaseConnection` и `TestMediaRenditionsQueueRetryRestartPublishAndRuntime` покрывают очередь, retry, перезапуск worker, публикацию и выдачу runtime. Для полного suite также исправлен helper публичного integration-router: он теперь получает media handler, который обязателен production-маршрутам.

OrbStack пересобрал `website-engine-prerender-goal:local`. Production smoke прошёл с `--network none`, `deploy/seccomp-prerender.json` и включённой Chromium sandbox. Linux/arm64 integration binary запускался внутри этого образа против disposable PostgreSQL и read-only mount девяти ZIP Vitasil; `TestBranchBrowserPrerenderUpdatesAfterContentAndAssignment` и `TestExternalVitasilNotFoundFeatureOverridesSystemPage` прошли в Chromium. Для реального ZIP `not-found` подтверждены содержательный HTML, HTTP 404, `noindex,nofollow`, `X-Engine-Prerender: hit` и исключение из sitemap на indexable и preview ветках.

Vitasil frontend проверен через Node 20: `npm run typecheck`, `npm run lint`, `npm run build`; собрано 9 архивов. Engine frontend через Node 20: `pnpm typecheck`, `pnpm lint`, `pnpm test` (9 файлов, 41 тест), `pnpm build`. Для admin login выполнен read-only Playwright QA на disposable `website-engine-vitasil-current-clean-qa-v2`: API admin-порт 38410 без токена вернул 401 `unauthorized`, веточные listeners 38411/38412 отвечают соответственно назначенным сайтом и системной 503; отдельно осмотрены desktop 1440px, mobile 390px, light/dark и console без ошибок. Это проверяет login shell и публичные listeners, но не заменяет UI E2E админских редакторов и остальные responsive/accessibility состояния.

Рабочий `vitasil-vitasil-1` не запрашивался и не менялся. На момент проверки контейнер был в restart loop: его логи показывали `hostname resolving error: lookup db on 127.0.0.11:53: no such host`. Его БД/volumes и API не трогались; для полного browser E2E рабочий stack нужно восстановить отдельно либо продолжать на disposable QA-контейнерах.

## Проверка текущего образа после запуска OrbStack, 6 ноября 2026

Повторно прошёл container smoke образа `website-engine-prerender-goal:local`: `--network none`, `deploy/seccomp-prerender.json`, включённая Chromium sandbox; результат — содержательный статический HTML и inert-форма.

Из текущего checkout собран Linux/arm64 integration binary и запущен внутри текущего образа с read-only mount репозиториев Vitasil/Engine и disposable PostgreSQL `website_engine_site_draft_qa_20261106`. Прошли 8 выбранных групп: `TestBranchSEODocumentsAndPublishedDynamicURLs`, `TestBranchBrowserPrerenderUpdatesAfterContentAndAssignment`, `TestExternalVitasilFeatureBuildsRegister` (9 ZIP), `TestExternalVitasilFormKeepsSubmissionFieldsRequired`, `TestExternalVitasilDesignTokensFollowSnapshotBuildSelection`, `TestExternalVitasilNotFoundFeatureOverridesSystemPage`, `TestExternalNestedFeatureWorkspaces` (catalog/main-data) и `TestExternalPlacementScopedBackgroundContent`. Chromium проверил invalidation HTML после публикации/назначения, а реальный ZIP `not-found` — 404, noindex и sitemap exclusion на indexable/preview ветках. Тестовый helper очищает только указанную disposable БД; перед запуском проверено отсутствие активных подключений приложения к ней. Все тесты завершились `PASS`.

Дополнительные read-only GET к давно запущенному `website-engine-vitasil-debian-goal-v2` подтвердили содержательный пререндер главной, каталога, карточек и формы. Этот контейнер использует более ранний образ, не является проверкой текущего checkout. Его неизвестный URL попал в сохранённый catch-all `not-found` и вернул HTTP 200; данные/БД/volumes и API не менялись. В актуальном коде отдельный системный override `systemPages.404` без catch-all возвращает 404, что покрывает текущий интеграционный тест. Реальную конфигурацию сайта Vitasil ещё нужно перенести в новый disposable snapshot с явной системной страницей и проверить на актуальном образе. Рабочий Vitasil по-прежнему не запускался и не менялся.

В том же старом disposable QA-контейнере выполнен read-only Chromium-прогон на `/`, `/catalog`, `/catalog/1`–`/catalog/3`, `/form` и неизвестном URL при ширинах 360, 390, 768, 1280 и 1920 px. Все запросы были GET/HEAD; API-записей не было. Навигации вернули `data-engine-ready=true`, горизонтального overflow не обнаружено, browser CLS составил 0 для каждого измеренного перехода, ошибок консоли не было. Клик по товару открыл его страницу, а пустая форма имела две обязательные поля и `checkValidity() === false`. Эти UI-результаты относятся к старому Engine image и не закрывают current-image E2E.


## Разделение редактора сайта, веток и загрузки файлов — 6 ноября 2026

По уточнению пользователя все назначения веток выполняются только в разделе «Ветки». Удалены post-save диалог из SiteWorkspace, назначение после создания и shortcut «Назначить» из SnapshotsPage; assignBranch() остался только в BranchesPage. В изолированном браузере /admin/site/design-system сохранил структурное изменение и создал снапшот #4 без выбора ветки. На /admin/snapshots/branches показан единственный контрол назначения; /admin/snapshots/tree не показывает кнопку, селектор или диалог назначения.

Загрузка файла отделена от редактирования поля: медиапикер изображения в реальном content workspace catalog@1.0.5 показывает только поиск/выбор готовых публичных файлов и ссылку «Открыть медиатеку» (/admin/media); прямого file input или загрузки в поле фичи нет. background.imageSrc остаётся content-managed по manifest и редактируется в /content.

Проверки после правок (Node 20): pnpm typecheck, pnpm lint, pnpm test — 11 файлов, 48 тестов; admin и runtime Vite builds прошли в отдельные каталоги /tmp. Disposable Vite/API использовали отдельную PostgreSQL engine_content_ui_qa_20261106 и bucket engine-content-ui-qa-20261106. Рабочий API, база и storage Vitasil не менялись.

Повторная read-only проверка 6 ноября в том же изолированном UI: на канвасе доступен только выбор рабочего снапшота; управление назначением находится в отдельной странице веток. В редакторе страницы показаны размещения, сборка, слот, структурная ссылка на detail-маршрут и поля, но нет формы записи редакционного контента. DOM страницы не содержал `input[type=file]`; media upload не запускался. Сохранение и назначение не нажимались. Оставление content source/field mapping в инспекторе подтверждено кодом редактора и этой спецификацией; изменения ветки, контента и файлов не выполнялись.

## Проверка fixture Vitasil с сохранёнными версиями — 6 ноября 2026

Первый полный Go-прогон на рабочем каталоге ZIP выявил, что тестовый helper ожидал ровно один архив для каждого feature key. В каталоге сохранены неизменяемые `main-data@1.0.4` и актуальный `main-data@1.0.5`; старую сборку оставили. Fixtures теперь сравнивают SemVer и выбирают актуальную версию для snapshot сценариев, а тест токенов использует только одну закреплённую версию каждого ключа, сохраняя проверку контрактов всех архивов.

После исправления прошли `go test -count=1 ./...` с `TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds` и `TEST_FEATURE_ZIP=.../header-1.0.5.zip`, `go vet ./...` и `go test -race -count=1 -run 'TestBranchManagementHTTPRequiresManagerAndRevision|TestSnapshotAssignmentRacesDeletion' ./cmd/integration`. Использовалась созданная для этого прогона disposable PostgreSQL на `35432`; проверено отсутствие подключений, затем удалены только тестовая БД и временная роль. Все девять текущих ZIP совпали по manifest key/version с исходниками, хеши совпали с QA-сборкой; исторический ZIP `main-data@1.0.4` сохранён. Рабочие API, БД, storage и volumes Vitasil не затрагивались.

Связанный контент проверен тем же полным Go-прогоном. Unit покрывают проверку schema pointers/типов и сохранение overlay за стабильным `EntryID`; реальный integration test `TestExternalVitasilContentRelationsProjectPublishedCatalogIntoPlacements` использует Vitasil `catalog` и `main-data`: один опубликованный источник проецируется в два размещения, draft источника скрыт, публикация обновляет общие поля, локальные overlays и media renditions остаются собственными. `TestSitepackRoundtripRemapsPublishedAndDraftRelationSidecars` проверяет перенос ссылок. Frontend `content-relations.test.ts` входит в ранее прошедшие 48 тестов. Browser E2E редактора сопоставления, сохранения overlay при сортировке/дублировании записей, unlink/delete preview и полного inheritance остаётся открытым; эти проверки не отмечены в TODO как завершённые.

## Регрессия responsive CSS дизайн-токенов, 6 ноября 2026

Добавлен `TestStructuredThemeCSSKeepsResponsiveCascadeAndBaseLinks` в `features/sitebuilder/design_system_structured_test.go`. `go test -count=1 ./features/sitebuilder` прошёл. Тест проверяет точный выходной CSS при несортированном вводе: media thresholds сериализуются по возрастанию; breakpoint без override не эмитит сброс значения; feature custom property остаётся связанной через `var(--base-…)`, а responsive base overrides меняют значение ссылки. Проверка доказывает stylesheet contract, но не запуск браузерного CSS cascade и не проверяет UI удаление breakpoint/предупреждение о потере значений.

## Идентичность вложенных записей редактора контента, 6 ноября 2026

Разбор manifest schema сохраняет annotation `x-engine-record-id`. Редактор скрывает такое поле от ввода; новые вложенные object-array строки получают UUID, дублирование генерирует новый UUID, а при загрузке/импорте пустые и повторные значения нормализуются, сохраняя первый стабильный ID. Первая card-layout проверка обнаружила, что ID использовался как заголовок строки; заголовок теперь выбирает только редакционные поля. Regression tests проверяют сохранение ID, генерацию при создании/дублировании, восстановление старых данных и отсутствие ID как элемента или заголовка в табличной и карточной разметке. Проверки после исправления: `pnpm typecheck`, `pnpm test` (11 файлов, 52 теста), `pnpm lint`, `pnpm build` (admin и public runtime), `go test -count=1 ./features/sitebuilder`, `gofmt -d` и `git diff --check` прошли. Это покрытие не включает браузерное создание/дублирование строки и сквозную проверку сохранения overlays в реальном Vitasil workspace.

## Связи контента в редакторе сайта, 6 ноября 2026

Уточнение пользователя подтверждено: источник и сопоставление полей остаются структурными настройками инспектора `/site`; редакционные значения остаются в `/content`. Read-only браузерная проверка disposable Vitasil показала только выбор рабочего снапшота в `/site`, без выбора/назначения ветки, без формы редакционной записи и без `input[type=file]` в редакторе страницы. Реальные public file поля используют выбор готового файла; загрузка доступна в `/media`. На выбранном QA-снапшоте была только пустая главная без связанного placement, поэтому данный браузерный прогон не проверял интерактивное сопоставление или сохранение overlays.

Code review выявил, что выбор новой версии сборки обновлял `collectionSource`, но оставлял старую версию источника в `contentBindings`; удаление сборки также не считало такую ссылку используемой. Исправлены синхронизация version references и защита от удаления. После этого прошли `pnpm typecheck`, `pnpm lint`, `pnpm test` (11 файлов, 54 теста), `pnpm build` (admin и public runtime) и scoped `git diff --check`. Обе браузерные базы были disposable; рабочие Vitasil API, БД и storage не использовались. Сквозное E2E выбора mapping и записи overlay позднее прошло; оставшиеся сценарии перечислены в следующем разделе.

## Безопасный fallback системных страниц, 6 ноября 2026

Добавлен `cmd/integration/system_pages_test.go`. На отдельной disposable PostgreSQL прошли `go test ./cmd/integration -run 'TestSystemPage404FallsBackWhenOverrideIsMissingOrBroken|TestSystemPage500OverrideAndFallbackWhenOverrideIsBroken' -count=1 -v` и `git diff --check -- cmd/integration/system_pages_test.go`. Проверены fallback 404 без настроенной error-страницы и при ошибке 404 override, успешный выбор страницы `systemPages.500` с HTTP 500, а также core fallback 500 при ошибке самой страницы override. Ошибка моделируется synthetic feature с required query argument, отсутствующим при рендере системного статического маршрута; код статуса и `noindex,nofollow` сохраняются. Использованная БД удалена после проверки нулевых активных сессий. Изменения рабочей установки Vitasil не выполнялись. Реальный JS exception в Vitasil error-фиче, клиентская ErrorBoundary и браузерная матрица не проверены; отдельное публичное состояние 403 пока не формируется runtime.

## Проверки продолжения: config, полный Go/frontend suite и Vitasil builds — 6 ноября 2026

Закрыт скрытый интервал и предел памяти публичного rate limiter: `FORM_RATE_MAX_CLIENTS` ограничивает число IP-счётчиков, `FORM_RATE_SWEEP_INTERVAL` управляет периодической очисткой, заполнение освобождается от просроченных записей и безопасно отклоняет новые IP при полном лимите. Параметры добавлены в `.env.example`, config spec и API docs. Удалены дубли `MEDIA_RENDITION_LEASE` и `MEDIA_WORKER_POLL_INTERVAL` в примере конфигурации.

Проверки:

- `go test -count=1 ./...` с `TEST_DATABASE_URL`, `TEST_FEATURE_DIR` и `TEST_FEATURE_ZIP` — все пакеты прошли, включая интеграционные тесты с чистой отдельной disposable PostgreSQL и ZIP-фичами Vitasil.
- `go vet ./...` и `go test -race -count=1 ./presentation/http/middleware` — прошли.
- Node 20: `pnpm typecheck`, `pnpm lint`, `pnpm test` (11 файлов, 54 теста), `pnpm build` — прошли; собраны admin SPA и public runtime.
- В исходном репозитории Vitasil без записи: `npm run lint` прошёл. Временная копия Vitasil: `npm run build` прошёл для девяти ZIP (`background@1.0.5`, `catalog@1.0.7`, `catalog-detail@1.0.6`, `footer@1.0.3`, `form@1.0.4`, `header@1.0.5`, `hero@1.0.8`, `main-data@1.0.7`, `not-found@1.0.5`).
- `docker compose config --quiet` с `.env.example` прошёл через временную подстановку Compose `env_file`; конфигурация с пустым interpolation environment отклонена. `env -u DATABASE_URL go run ./cmd/api` завершился с `DATABASE_URL is required` до обращения к БД/S3. `.env` в рабочем дереве не создавался.
- `git diff --check` прошёл до этого прохода; повторная финальная проверка после документирования выполняется отдельно.

Эта QA-запись не закрывает полную браузерную матрицу продуктовых сценариев из TODO. Рабочий Vitasil API/БД/storage не использовались; его runtime-контейнер оставался в состоянии `Restarting`, а database-контейнер остановлен. Для полного runtime E2E нужно восстановить Vitasil или поднять его конфигурацию и объекты в disposable-контейнерах; автоматические миграции к рабочему Vitasil не выполнялись.

## Актуальные ZIP Vitasil и проверка ESM parser — 6 ноября 2026

Актуальные исходники Vitasil собраны в изолированной временной копии `frontend/`; в исходный `dist/` ничего не записывалось. Сборка выпустила девять ZIP с версиями `background@1.0.6`, `catalog@1.0.9`, `catalog-detail@1.0.7`, `footer@1.0.4`, `form@1.0.5`, `header@1.0.6`, `hero@1.0.9`, `main-data@1.0.9` и `not-found@1.0.6`. Engine первоначально отклонял минифицированный JS на `/` внутри regex literals: lexer использовался напрямую и не переклассифицировал `DivToken` в `RegExpToken`. Валидатор теперь разбирает asset существующим ECMAScript parser и проверяет import-граф через AST; bare/external/missing paths и dynamic imports без единственной строковой константы остаются отклонёнными. Добавлен unit regression на escaped slash, `\d`, division, static import/export; прежние проверки некорректных импортов прошли.

Проверки с актуальными ZIP и отдельной disposable PostgreSQL:

- `go test -count=1 ./cmd/integration -run '^TestExternal'` — прошёл, включая регистрацию всех девяти ZIP, partial nested content/prerender для `catalog` и `main-data`, обязательные поля реальной формы, design tokens, `not-found`, relations и placement-scoped background/media.
- `go test -count=1 ./...` и `go vet ./...` — прошли; Chromium был включён в интеграционные тесты пререндеринга.
- Engine frontend на Node 20: `pnpm typecheck`, `pnpm lint`, `pnpm test` (55 тестов), `pnpm build` — прошли. Vitasil frontend: `npm run lint`, `npm run typecheck`; `npm run build` — прошёл в временной копии.
- `gofmt -d` и scoped `git diff --check` для кода — прошли.

Read-only admin QA на disposable `http://127.0.0.1:39280/admin/site/pages` проверила редактор страницы: source и mapping остаются структурными настройками; здесь нет назначения ветки, редакционной записи или upload control. Назначения находятся в `/snapshots/branches`, а медиаполя выбирают уже загруженные файлы по предыдущей browser QA-записи. Этот просмотр не сохранял данные. Полный browser workflow source/mapping и overlays, реальный upload через `/media`, screen-reader и публичная accessibility-матрица остаются открытыми. Рабочий `vitasil-vitasil-1` наблюдался в restart loop; его API, БД, storage и volumes не менялись.

## Public runtime: landmarks и responsive smoke — 6 ноября 2026

Read-only Playwright проверил опубликованные `/`, `/catalog`, `/catalog/1` и `/form` в disposable Vitasil v2 (`127.0.0.1:38291`) при ширине 360, 768, 1280 и 1920 px. Горизонтального переполнения не было; на форме браузер обнаружил два обязательных поля, отправка не выполнялась. Исходный DOM имел один H1 на главной/каталоге/форме, но не на detail; у трёх страниц не было `main` landmark. Detail показывал название через `div`-компонент `CardTitle`, поэтому браузер не распознавал его как заголовок.

Engine теперь группирует все placements слота `main` в одном `<main>`; header/footer остаются соседями вне него. Vitasil detail использует `<article>` и семантический `<h1>`; manifest bump — `catalog-detail@1.0.8`, schema и feature ABI не менялись.

Текущий реестр Vitasil на момент проверки: `background@1.0.6`, `catalog@1.0.9`, `catalog-detail@1.0.8`, `footer@1.0.4`, `form@1.0.9`, `header@1.0.6`, `hero@1.0.9`, `main-data@1.0.9`, `not-found@1.0.6`. Все девять сборок прошли lint, typecheck и build. ZIP-файлы текущих версий проверены; `catalog-detail-1.0.8.zip` добавлен в каталог сборок, исторические ZIP сохранены. Повторная сборка в отдельный временный каталог совпала с текущими артефактами.

Проверки Engine: `pnpm typecheck`, `pnpm lint`, `pnpm test` (55 тестов), `pnpm build`, `go test -count=1 ./...`, `go vet ./...` — прошли. Четыре Chromium-сценария прошли: `TestStaticModuleBrowserLifecycleAndPrerender`, `TestExternalVitasilFeatureRuntimeBrowserPrerender`, `TestExternalVitasilCatalogDetailBrowserSemantics`, `TestExternalVitasilCatalogAndMainDataAllowPartialEditorialContentPrerender`. Полный Go suite использовал отдельную PostgreSQL `engine_current_qa_1793987096`; после проверки нулевого числа подключений disposable БД удалена. В одном первом запуске detail-сценария тестовая фикстура передала строку вместо `ArgumentBinding`; фикстура исправлена, повторный focused run и полный suite прошли.

Опубликованные страницы disposable Vitasil проверялись только GET-запросами; рабочие API, БД, storage и volumes Vitasil не менялись. Screen-reader/VoiceOver, контрастность и полная публичная accessibility-матрица остаются открытыми. Сквозной browser E2E source mapping, выбор/порядок записей, перезагрузка контентного редактора и публикация локального overlay описаны ниже; duplicate/sort/slug вложенных строк, conflicts/unlink/restore, public HTML связей, полный media/upload и остальные незакрытые пункты TODO остаются открытыми.

## Сквозной E2E связей контента на Vitasil — 6 ноября 2026

Добавлен browser subtest `TestExternalVitasilContentRelationsProjectPublishedCatalogIntoPlacements/editor_browser_preserves_snapshot_boundaries`. Он использует реальные `catalog@1.0.9` и `main-data@1.0.9`, встроенную админку checkout, Chromium и отдельные disposable PostgreSQL базы.

Playwright открыл `/site/pages`: UI оставляет настройку источника и сопоставление полей, но не показывает назначение ветке или `input[type=file]`. Для реального `background@1.0.6` инспектор не показывает поле `Изображение фона`, кнопку выбора файла или загрузку. Mapping `catalog.slug` → `main-data.slider.products.name` сохранился в дочерний снапшот без изменения назначений веток. В `/content` на ребёнке проверены выбор Yogurt, перемещение выше Milk, отсутствие редактирования mapped `Название`, автосохранение локальной «Категории», перезагрузка страницы и восстановление выбранного порядка и overlay до публикации. Затем контент отдельно опубликован.

Дочерний снапшот назначен production-ветке в UI `/snapshots/branches`; runtime выдал `yogurt, milk` в заданном порядке, `Browser dairy` только для Milk; второе размещение осталось `Milk`/`Featured`, родитель — `Milk`/`Dairy`. Chromium-пререндер публичной главной подтвердил эти значения и порядок внутри конкретных DOM-размещений. Последующая публикация и конфликт удаления источника не переписали данные ребёнка. Рабочий Vitasil API, БД, storage и volumes не использовались.

Focused Chromium E2E прошёл. На последней disposable PostgreSQL прошли полный `go test -count=1 ./...` и `go vet ./...`; frontend проверки прошли: `pnpm typecheck`, `pnpm lint`, `pnpm test` (58 тестов), `pnpm build` (admin и runtime). Временные БД запускались с нулевыми активными подключениями перед очисткой.

Открыты UI duplicate/sort/slug вложенных строк и сохранение overlay на том же `EntryID`, конфликты двух редакторов, unlink/delete preview и права, полный inheritance/restore, инвалидация prerender-кеша после публикаций/назначений, detail routes, browser media/sitepack roundtrip и тесты с disposable S3.

## Дизайн-токены: computed CSS в Chromium — 6 ноября 2026

Добавлены браузерные assertions в `TestBranchDesignTokensRemainSnapshotScoped` и `TestExternalVitasilDesignTokensFollowSnapshotBuildSelection`. Runtime получает опубликованную тему через обычную ветку; назначение дочернего снапшота выполнялось явно в тестовом setup. На synthetic feature проверены base-linked color и responsive cascade на ширинах 500, 800, 1280 и 1600 px. На актуальном ZIP Vitasil `header` проверена декларация `color-focus`, ссылка на базовый цвет и CSS-вычисление на 500 и 1280 px.

Обе команды прошли на disposable PostgreSQL `website-engine-design-token-qa`, Chromium через закреплённый Playwright 1.62.1:

- `go test -count=1 -v ./cmd/integration -run '^TestBranchDesignTokensRemainSnapshotScoped$'`
- `go test -count=1 -v ./cmd/integration -run '^TestExternalVitasilDesignTokensFollowSnapshotBuildSelection$'` с `TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds`.

В тесте Vitasil применены `header@1.0.6` и остальные последние доступные ZIP из каталога сборок. Проверка обслуживалась отдельным router/test database; рабочие Vitasil API, БД, storage и volumes не затрагивались. Графическая настройка ссылки и breakpoint через админку, предупреждение перед удалением порога, конфликт сохранения, наследование fallback и реальная загрузка WOFF2 в опубликованной странице остаются непроверенными.

### Дополнительная приёмка provenance и дизайн-системы

Для снапшота добавлено неизменяемое резюме происхождения в существующий `payload`: корень/потомок/content fork/sitepack import, изменённые области и версии фич, а также количества опубликованных записей и черновиков по feature key. Резюме не включает значения контента или ID оператора и исключено из `SnapshotDigest`; доступное имя токена разрешается только в административных ответах и скрывается после revoke. Публичная runtime composition не возвращает автора или provenance.

На disposable PostgreSQL `website-engine-snapshot-provenance-qa-pg` (35437) прошли `go test -count=1 ./features/sitebuilder` и focused integration: `TestSnapshotProvenanceIsStoredShownAndNotExposedByRuntime`, `TestConfiguredBranchesPublishForkAndProtectTree`, `TestSnapshotScopedContentDraftIsIsolatedAndForksImmutably`, `TestSitepackRoundtripIsAtomicAndIncludesOnlyReferencedFiles`. Chromium `TestSnapshotProvenanceEditorBrowser` проверил автора, дату, родителя и резюме в дереве и редакторе. Первый запуск выявил устаревший встроенный frontend bundle без родителя; после `pnpm build` сценарий повторно прошёл.

Полный Chromium сценарий дизайна на disposable PostgreSQL 35436 и ZIP Vitasil проверил создание token, связь `header/color-focus` с базовым, адаптивные пороги, наследование/reset, duplicate rejection, удаление с отменой и подтверждением, сохранение правок после 409 и неизменность назначения ветки. Вычисленный CSS проверен при 500/800/1280/1600 px; при 360 px переполнения нет. Frontend `pnpm typecheck`, `pnpm lint`, `pnpm test` (60 тестов) и `pnpm build` после интеграции прошли. Рабочие API, БД, storage и volumes Vitasil не использовались.

## Startup config и отчёт sitepack — 7 ноября 2026

API теперь валидирует `Load`, `LoadFiles`, `LoadPagination`, `LoadSitepack` и `LoadPrerender` до bind listeners, открытия PostgreSQL и S3. На URL заведомо недоступной тестовой БД проверки `TestRunValidatesAllConfigGroupsBeforeDatabaseConnections` вернули имена отсутствующих `DATABASE_URL`, `FILES_MAX_BYTES`, `PUBLIC_PAGE_SIZE`, `SITEPACK_MAX_BYTES` и `PRERENDER_ENABLED` без попытки connect; CLI аргументы переименования проверяются до чтения DB-конфига. `go test -count=1 ./cmd/api ./pkg/config`, `go vet ./cmd/api ./pkg/config` и `git diff --check` прошли. Полный аудит всех числовых guardrails и всех CLI сообщений остаётся открытым.

Отчёт импорта sitepack дополнен числами опубликованных записей, draft items и переназначенных ID записей; UI показывает их вместе с новыми/переиспользованными feature builds и файлами. На уникальной disposable PostgreSQL `website_engine_sitepack_qa_20261106_b2c6` прошли `TEST_DATABASE_URL='<unique disposable PostgreSQL URL>' go test -count=1 -v ./cmd/integration -run '^TestSitepack'`, `go test -count=1 ./features/sitebuilder` и `go vet ./features/sitebuilder ./cmd/integration`. Интеграционный отчёт подтвердил одну опубликованную запись, один draft item и один переназначенный ID.

7 ноября добавлен `TestSitepackImportBrowserAcceptance`. На отдельной disposable PostgreSQL `website_engine_sitepack_ui_qa_20261107_7d3c` и Chromium с ZIP `catalog@1.0.9`/`hero@1.0.10` пройдены экспорт через UI, отказ повреждённого ZIP без создания снапшота, очищение ошибки при повторном выборе и два успешных импорта как разных корневых снапшотов без назначений веткам. UI отчёт показал 0 новых/2 переиспользованных сборок, 0 новых/1 переиспользованный файл, 1 опубликованную запись, 1 draft item и 1 переназначенный EntryID; обе ссылки вели на один и тот же импортированный snapshot. Playwright подтвердил видимый клавиатурный фокус на кнопке выбора архива и отсутствие фокуса на визуально скрытом file input; тот открывается клавишей Enter через trigger. При 390 px горизонтального переполнения нет. Прошли `go test -count=1 -run '^TestSitepackImportBrowserAcceptance$' -v ./cmd/integration`, `go vet ./cmd/integration`, Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (60) и `pnpm build`. Для этого UI теста storage был отдельным in-memory FakeStore; browser acceptance с MinIO и перенос между разными инсталляциями не входят в результат. Рабочие Vitasil API, БД, storage и volumes не использовались.

При продолжении аудита config удалены скрытые page defaults/caps публичной коллекции и списка публикаций. Unit suite проверил fail-closed при отсутствующей конфигурации, configured limit 250 и SQL `LIMIT 251`; на той же уникальной disposable PostgreSQL прошёл `TestCollectionRuntimeFiltersAndResolvesDetailRoutes`. Рабочие Vitasil API, БД и storage не затрагивались.

## Повторная проверка frontend и конфигурации — 7 ноября 2026

После текущих изменений повторно прошли `go test -count=1 ./cmd/api ./pkg/config`, `go vet ./cmd/api ./pkg/config` и `docker compose --env-file .env.example config --no-env-resolution`. Compose разобрал пример конфигурации без запуска сервисов. В ней теперь явно заданы `HTTP_MAX_HEADER_BYTES` и независимый `SITEPACK_MAX_SVG_BYTES`; конфигурация также проверяет синтаксис `DATABASE_URL` до подключения и ограничивает временем запрос первоначального `EnsureBucket`.

С Node `v20.19.4` из `frontend/` прошли `pnpm typecheck`, `pnpm lint`, `pnpm test` (13 файлов, 60 тестов) и `pnpm build` для админки и runtime. Сборка обновила встроенные `pkg/static/dist` ассеты. Эти проверки не запускали production Compose, не мигрировали рабочую базу и не обращались к Vitasil.

## Fork редакционных данных формы — 7 ноября 2026

На отдельной PostgreSQL `website_engine_form_fork_qa_20261107_01` прошёл `go test -count=1 -run '^TestSnapshotForkCopiesFormContentWithoutCopyingSubmissions$' -v ./cmd/integration`. Тест создал published ревизию текста формы и более новый draft, отправил заявку в исходной ветке, создал дочерний снапшот и проверил наследование обеих ревизий, выдачу runtime только published текста, неизменность родителя/назначений и отсутствие копии заявки. Тест перед `newTestEnv` подтвердил уникальный локальный DB prefix и отсутствие других сессий; после завершения подключений снова было 0. Production Vitasil не использовался.

## Приёмка Sitepack между инсталляциями и built-in 503 — 7 ноября 2026

`TestSitepackCrossInstallationAcceptance` прошёл на двух disposable PostgreSQL: source `website_engine_sitepack_transfer_qa_20261107_4b1733_source` и target `website_engine_sitepack_transfer_qa_20261107_4b1733_target` (host ports 35450/35451), с двумя отдельными loopback MinIO endpoint на 39050/39051 и уникальными buckets `sitepack-transfer-qa-*`. Перед сбросом source/target helper подтвердил точные имена БД и отсутствие других сессий; после теста cleanup подтвердил ноль сессий. Команда: `go test -count=1 -v ./cmd/integration -run '^TestSitepackCrossInstallationAcceptance$'`.

Тест перенёс актуальные fixture archives между независимыми Engine services, проверил content/relation remap, checksum файла, reuse builds/files, отсутствие service-private файла, заявок и credentials, а также отсутствие автоматического назначения. Fault injection записал частичный namespaced S3 объект и вернул ошибку: snapshot/build/file/submission rows откатились, ключ попал в cleanup queue, повторный `Delete` после временного отказа убрал объект и queue row. Первая попытка focused test встретила три очереди от предыдущих прогонов, оставленные вне общего `TRUNCATE`; integration reset теперь очищает `file_object_deletions`. После этого повторный прогон завершился `PASS`. Только созданные root disposable-контейнеры теста; рабочий Vitasil и другие БД/storage не использовались.

`TestUnassignedBranchUsesBuiltInNotConfigured503` прошёл на disposable `website_engine_form_fork_qa_20261107_01` (35439) через `go test -count=1 -v ./cmd/integration -run '^TestUnassignedBranchUsesBuiltInNotConfigured503$'`. Проверены repository и реальный public HTTP GET `/`: HTTP 503 с текстом «Сайт ещё не настроен», `noindex,nofollow`, отсутствие cache/snapshot identity. Перед тестом и после него в базе было ноль других подключений. Согласно ответу пользователя отдельная startup-config страница для неназначенной ветки не добавляется.

Тот же disposable DB использовался для `TestVitasilContentEditorEndToEnd` с актуальными ZIP `catalog@1.0.9`, `catalog-detail@1.0.8`, `main-data@1.0.9`, `form@1.0.9`, `hero@1.0.11` и `background@1.0.6`; Chromium сценарий проверил заполнение detail полей, автосохранение и публикацию частичного form content без создания снапшота и серверное отклонение/принятие form submission по обязательным полям. Перед тестом и после него подтверждено `other_sessions=0`. Первая попытка остановилась на ошибке тестового fixture (`arguments.slug` был сериализован строкой вместо `ArgumentBinding`); fixture исправлен, повторный прогон завершился `PASS` за 22.19 секунд. Рабочий Vitasil не использовался. Команды: `go test -count=1 -v ./cmd/integration -run '^TestVitasilContentEditorEndToEnd$'` с Vitasil `TEST_FEATURE_DIR` и Node 20/Playwright/Chromium env.

## Стабильная identity снапшотов и контента — 7 ноября 2026

На выделенной disposable PostgreSQL `website_engine_snapshot_identity_qa_20261107_47d195f6` (контейнер с loopback mapping `127.0.0.1:35460` и tmpfs) прошли:

- `TestSnapshotChildPreservesPlacementRouteAndCollectionEntryIDs` — сервер выдал разные UUID новым collection records; IDs и порядок сохранились через draft reorder, publication и child fork. Структурное изменение сохранило page, route и placement IDs.
- `TestRemovingFeatureFromChildPreservesParentPublishedAndDraftContent` — исключение feature оставило published и draft родителя неизменными; child не получил feature в каталоге редактора, workspace или таблицах опубликованных/черновых записей.

Команда: `go test -count=1 -v ./cmd/integration -run '^(TestSnapshotChildPreservesPlacementRouteAndCollectionEntryIDs|TestRemovingFeatureFromChildPreservesParentPublishedAndDraftContent)$'`. Перед запуском и после него подтверждены точная `current_database()` и `other_sessions=0`. Фронтенд генерации page/route/placement IDs подтверждён исходниками `crypto.randomUUID`; отдельный browser assertion создания этих сущностей не выполнялся. Рабочий Vitasil не использовался.

## Сравнение дизайн-токенов с родителем — 7 ноября 2026

На новой disposable PostgreSQL `website_engine_design_parent_qa_20261107_a832` (контейнер `website-engine-design-parent-qa-20261107-a832-pg`, только loopback `127.0.0.1:35462`, tmpfs) прошёл `go test -count=1 -v ./cmd/integration -run '^TestVitasilDesignSystemGraphicalWorkflowCreatesIndependentSnapshot$'` с актуальными ZIP девяти Vitasil-фич, Node 20, Playwright и Chrome. Браузер подтвердил сравнение новых и изменённых базовых/feature-токенов, источник значения, manifest default закреплённой версии, responsive inheritance и ссылку feature→base. Сброс убирает отметку отличия; ответ 503 для чтения родителя можно повторить и продолжить редактирование. Также повторно прошли проверки независимости child/grandchild, responsive-порогов, конфликтного сохранения, отмены/подтверждения удаления и итогового CSS runtime. До и после теста подтверждены точное имя БД и ноль других подключений. Рабочая установка Vitasil не использовалась.

## Публичный SEO/prerender Vitasil и embedded favicon — 7 ноября 2026

На свежей disposable PostgreSQL `website_engine_vitasil_archive_qa_20261107_8d92` (контейнер `website-engine-seo-current-qa-20261107-8d92-pg`, loopback `127.0.0.1:35461`, tmpfs) прошёл `TestSEOCurrentVitasilCatalogPublicationRestoreAcceptance` с актуальными ZIP `header@1.0.7`, `hero@1.0.11`, `catalog@1.0.9`, `catalog-detail@1.0.8` и `not-found@1.0.6`. Проверены HTML с JS и без JS, каталог/detail, sitemap, canonical/robots, CTA и карточка товара, черновик и явная публикация/восстановление slug, ETag/HTTP cache identity и системная 404. Public collection helper получил настроенный размер 50, соответствующий текущему Vitasil catalog. Chromium не зарегистрировал `pageerror`, `console.error` или неожиданные ошибки same-origin ресурсов; favicon вернул 200 `image/svg+xml`.

Прогон выявил дефект: в route-aware document handler путь `/favicon.svg` разрешался как страница и возвращал 404, несмотря на embedded favicon в runtime `index.html`. `pkg/runtime/document.go` теперь обслуживает embedded `/favicon.svg` как статический ресурс до route resolution; `TestDocumentHandlerServesEmbeddedFaviconWithoutSiteRoute` покрывает GET/HEAD, MIME, cache header и отсутствие обращения к route resolver. Прошёл `go test -count=1 ./pkg/runtime`; полная браузерная приёмка прошла за 12.18 s (package 12.544 s). До/после выполнена проверка подключения; после прогона только сама psql-проверка соединялась с disposable DB. Рабочий Vitasil и его API/БД/storage/volumes не использовались.

Один desktop run 1280×800 записал TTFB/DCL/load/LCP (home 1049.5/1067.4/1068.6/1088 ms; catalog 5.1/22.2/22.2/32 ms; detail 4.6/15.1/19.6/56 ms; 404 9.9/19.8/20.6/72 ms). Это только локальная единичная диагностика: CLS недоступен, нет INP/TBT/FCP, mobile/form и повторов, поэтому Core Web Vitals и performance пункт TODO не закрываются. См. [SPEC-seo.md](SPEC-seo.md).

## Защита имени файла и ревизии ссылок — 7 ноября 2026

Backend файлового сервиса удаляет Unicode control characters из имени перед помещением в `Content-Disposition`, сохраняя корректные международные имена. Добавлен unit regression для path traversal/CRLF/control chars и порядка UTF-8 имени; fingerprint списка ссылок детерминирован по порядку и меняется при изменении ссылки. Прошли `go test -count=1 ./features/files ./presentation/http/handlers/files` и `go vet ./features/files ./presentation/http/handlers/files`. Полные MinIO/API/browser сценарии, 5 GiB передача и галерея проверяются отдельно; этот unit результат не закрывает широкие файловые пункты TODO.

## Системные страницы и исключение из sitemap — 7 ноября 2026

На disposable PostgreSQL `website_engine_system_pages_qa_20261107_1b4c` (контейнер `website-engine-system-pages-qa-20261107-1b4c-pg`, loopback `127.0.0.1:35464`, tmpfs) одним последовательным прогоном прошли `TestSystemPage404FallsBackWhenOverrideIsMissingOrBroken`, `TestSystemPage500OverrideAndFallbackWhenOverrideIsBroken`, `TestUnassignedBranchUsesBuiltInNotConfigured503` и новый `TestConfiguredSystemPagesAreExcludedFromSitemapWithoutNoindexOverride`. Проверены 404/500 override и безопасный fallback с `noindex`/`no-store`, встроенная 503 для не назначенной ветки и отсутствие явно назначенной системной страницы в sitemap даже без собственного `noindex`. Команда: `go test -count=1 -v ./cmd/integration -run '^(TestConfiguredSystemPagesAreExcludedFromSitemapWithoutNoindexOverride|TestSystemPage404FallsBackWhenOverrideIsMissingOrBroken|TestUnassignedBranchUsesBuiltInNotConfigured503|TestSystemPage500OverrideAndFallbackWhenOverrideIsBroken)$'`. Preflight/postflight подтвердили точную disposable БД и ноль других клиентских подключений. Публичный route-level 403 остаётся ответственностью gateway; browser/accessibility матрица системных страниц остаётся открыта. Спецификация: [SPEC-system-pages.md](SPEC-system-pages.md).

## Чистая схема и контроль миграции — 7 ноября 2026

В текущей чистой миграции остался один файл `migrations/0001_init.sql`, содержащий финальные таблицы веток, дерева снапшотов, опубликованных и черновых контентных ревизий, файловых владельцев и multipart-сессий. Исторические миграции удалены согласно согласованному reset-плану; рабочие БД не удалялись и миграции к Vitasil не применялись. Atlas `migrate.Validate` через уже имеющийся модуль `ariga.io/atlas/sql/migrate` сообщил успешную проверку `migrations/atlas.sum` и списка migration-файлов. Валидатор запускался как временная программа в `/tmp`, без изменения зависимостей или файлов репозитория. Интеграционные acceptance-проверки на новой disposable PostgreSQL применяют эту чистую миграцию.

## Configured asset cache TTL и migration CLI — 7 ноября 2026

`RUNTIME_ASSET_CACHE_TTL` теперь проходит из явной конфигурации API через HTTP dependencies в route-aware и fallback public runtime. Он применяется к embedded favicon и unhashed runtime assets; `assets/*` с hash digest сохраняют `public, max-age=31536000, immutable`. Legacy media redirect получает обязательный `MEDIA_REDIRECT_CACHE_TTL` через setter. Constructors отвергают отсутствующий/дробный/нулевой TTL вместо скрытой подстановки. Проверено: `go test -count=1 ./pkg/config ./presentation/http/handlers/media ./pkg/runtime`, `go vet ./pkg/config ./presentation/http/handlers/media ./pkg/runtime`; тест runtime проверяет настроенные 7200 секунд, immutable hash asset и невалидные durations. Под Node 20 прошли `node --test scripts/migrate-legacy-site.test.mjs` (3/3), включая отсутствие фиксированного page limit и обязательные bounded retry flags; `docker compose --env-file .env.example config --no-env-resolution` успешно разобрал пример. README синхронизирован с `TARGET_BRANCH_NAME` и обязательными флагами. Пока не выполнялся интеграционный запуск полного API после этой wiring-правки; он остаётся в очереди с другими тестами `cmd/integration`.

## Удаление контентного файла и backend bindings — 7 ноября 2026

На disposable PostgreSQL `website_engine_files_media_qa_20261107_b77d` (loopback `127.0.0.1:35432`) прошёл `TestFileDeletionAfterSnapshotForkInvalidatesBothBranches`. Перед тестом проверены владелец loopback-порта и отсутствие других подключений; после теста `other_sessions=0`. Подтверждённое удаление в родителе и потомке очищает распознанные manifest file fields в опубликованных данных и черновиках, сохраняет обычное текстовое поле с тем же UUID, повышает content/draft revisions и меняет runtime identity назначенных веток. Устаревшая ссылка подтверждения получает конфликт; устаревшие открытые сохранение и публикация также получают `ErrConflict`, а не ошибку «недопустимый файл». Исправление ранней проверки ревизии внесено в `features/sitebuilder/content_workspace.go`. Прошли `go test -count=1 ./features/sitebuilder ./features/files`, соответствующий `go vet` и `git diff --check`. Реальный сервисный владелец, история контента и заявки дополнительно покрыты `TestServiceOwnerDeleteClearsConfirmedSnapshotContentAndSubmissionReferences`; gateway/CDN не проверялись.

На disposable PostgreSQL `website_engine_external_backend_qa_20261107_01` (`127.0.0.1:35442`) прошёл `TestExternalBackendBindingsDisposableAcceptance` с Chromium/Playwright; helper подтвердил уникальную БД и нулевые прочие сессии, postflight также вернул 0. Проверены same-origin и перехваченные запросы на зарегистрированный внешний origin, path/query bindings с повторяющимися значениями, 404/500 и отсутствующий источник, зависимые placements, snapshot inheritance и отказ от HTML cache/prerender для внешнего binding. Playwright test не ходил к production API: реальная CORS политика, сетевые timeout и поведение поставщика требуют отдельного доступного test endpoint. `TestExternalBackendBindingsVitasilArchiveCompatibility` и `TestExternalFeatureDesignArchives` прошли read-only на девяти актуальных ZIP Vitasil (`background@1.0.6`, `catalog@1.0.9`, `catalog-detail@1.0.8`, `footer@1.0.4`, `form@1.0.9`, `header@1.0.7`, `hero@1.0.11`, `main-data@1.0.9`, `not-found@1.0.6`); манифесты не объявляют request slots, ABI фич не менялся.

## Повторная browser-проверка Vitasil и медиагалереи — 7 ноября 2026

`TestAdminMediaGalleryAndPickerBrowserAcceptance` прошёл на disposable PostgreSQL/MinIO `website_engine_files_media_qa_20261107_c055` (`35465`/`39044`), preflight и postflight показали ноль других DB-сессий. Chromium проверил `/media`, владельцев и правила видимости, выбор существующего файла в picker; новый файл из редактора фичи загрузить нельзя. `TestSitepackImportBrowserAcceptance` и `TestSitepackCrossInstallationAcceptance` прошли на отдельных disposable PostgreSQL/MinIO; сведённые per-item результаты, повреждённый ZIP, atomic failure cleanup, remapping и повторный импорт проверены в UI/backend.

Актуальные исходники Vitasil прошли Node 20 `npm run typecheck` и сборку всех девяти ZIP в `/tmp/website-engine-vitasil-qa.PZKKPk/feature-builds`; каталоги сборки рабочего Vitasil не перезаписывались. На отдельной disposable PostgreSQL `website_engine_vitasil_archive_qa_20261107_8d92` последовательно прошли `TestSEOCurrentVitasilCatalogPublicationRestoreAcceptance`, `TestVitasilContentEditorEndToEnd`, `TestVitasilCurrentHeaderAndHeroResponsiveRuntime` и `TestVitasilDesignSystemGraphicalWorkflowCreatesIndependentSnapshot` с Chromium. Они проверили текущий контент Vitasil, вложенные/массивные поля, автосохранение и публикацию, обязательность серверной формы, responsive header/hero, token cascade, sitemap/canonical/robots и публикацию/restore cache identity. В тесте SEO ожидаемая несуществующая страница вернула 404 и оставила обычную запись Chrome `Failed to load resource: 404`; assertion пройден. Рабочие API/БД/storage/volumes Vitasil не использовались. Ручная VoiceOver проверка, production gateway/CDN и повторные field Core Web Vitals по-прежнему не подтверждены.

Проверка правил интерфейса по актуальному Web Interface Guidelines выявила нес semantic upload drop area в `/media`, `transition-all` на upload progress и отсутствующую подпись колонки действий в таблице. Drop area заменён на нативную keyboard-operable кнопку; прогресс теперь имеет `role="progressbar"` и ограниченный transition по ширине; добавлена подпись колонки. Полный Node 20 frontend run после integration правок ещё требует повторной фиксации после последних CSS/test изменений.

## CSS cascade runtime и браузерная приёмка системных страниц — 7 ноября 2026

В Chromium screenshot текущей Vitasil `not-found@1.0.6` выявил два связанных visual defect: Engine shell fallback цвета/фона перекрывал site-wide theme из ZIP, а Vitasil core unlayered `a { color: inherit }` скрывал `text-primary-foreground` CTA. Первый дефект исправлен универсально: fallback геометрия и цвета публичного runtime помещены в ранний CSS layer `runtime-base`; контракт добавлен в `SPEC-feature-runtime.md`. Второй исправлен в Vitasil shared core удалением дублирующего reset, уже выполняемого Tailwind Preflight. ABI не менялся; версии всех девяти feature manifests повышены на patch и ZIP пересобраны с единым core.css digest.

В Vitasil прошли Node 20 `npm run typecheck`, `npm run lint`, `FEATURE_BUILD_OUTPUT_DIR=/tmp/website-engine-vitasil-shared-css-qa-if2P88/feature-builds npm run build` (9 ZIP: `background@1.0.7`, `catalog@1.0.10`, `catalog-detail@1.0.9`, `footer@1.0.5`, `form@1.0.10`, `header@1.0.8`, `hero@1.0.12`, `main-data@1.0.10`, `not-found@1.0.7`). На disposable tmpfs PostgreSQL `website_engine_system_pages_browser_qa_20261107_02` Chromium `TestSystemPagesBrowserAccessibilityAcceptance` прошёл no-JS system page cases: Vitasil 404 override, core 500 and user-approved built-in 503 for unassigned branch. Каждая страница была проверена на 360/390/1280 px: HTTP status, `noindex,nofollow`, `no-store`, одна `main`, один `h1`, видимый keyboard focus, отсутствие admin assets и horizontal overflow. Для 404 computed root фон `rgb(70, 88, 63)`, текст `rgb(255, 250, 240)`, CTA label `rgb(0, 85, 148)`. Screenshot вручную просмотрен: `/tmp/website-engine-system-page-404-360.png`. Команда: `go test -count=1 -v ./cmd/integration -run '^TestSystemPagesBrowserAccessibilityAcceptance$'` с Node 20/Playwright/Chromium и свежими ZIP. Disposable контейнер был tmpfs и после финальных проверок будет остановлен; рабочий Vitasil API/БД/storage/volumes не использовались. Остаются route-level 403, screen-reader, production gateway/CDN и широкая accessibility/performance matrix.

## Проверка конфигурации и переноса — 7 ноября 2026

Повторно прошли `go test -count=1 ./pkg/config ./cmd/api`, `go vet ./pkg/config ./cmd/api`, `docker compose --env-file .env.example config --no-env-resolution` и Node 20 `node --test scripts/migrate-legacy-site.test.mjs` (3/3). Сверены обязательные operator settings, `.env.example`, fail-fast API/CLI и worker stdin contract; Atlas `atlas.sum` entry/root hash для единственного `migrations/0001_init.sql` независимо совпали с алгоритмом `ariga.io/atlas/sql/migrate` из уже имеющегося Go module cache. CLI `atlas` в shell не установлен; миграционный файл и `atlas.sum` не менялись этим hash check.

## HTTP-контракт отвязки content relation — 7 ноября 2026

Добавлены role-gated preview и unlink маршруты, а также user-scoped preview воздействия удаления source record; контракт внесён в `docs/API.md`, детали состояния и приватности остаются в [SPEC-content-relations.md](SPEC-content-relations.md). `TestContentRelationsUnlinkHTTPAcceptance` прошёл с синтетическими pinned builds на отдельной БД `website_engine_relation_http_qa_20261107_root05` в disposable PostgreSQL контейнере `website-engine-system-pages-css-qa-20261107-02` (`127.0.0.1:35474`). Проверены 401 без токена, 403 content-user для site-manager endpoints, доступ user к source-entry impact preview, `no-store`, отсутствие content/storage-key значений, успешный admin discard и сохранение parent binding при создании независимого child. До/после подтверждены точная БД и 0 других сессий; после теста удалена только эта БД. Прошли `go test -count=1 -v ./cmd/integration -run '^TestContentRelationsUnlinkHTTPAcceptance$'`, `go test -count=1 ./features/sitebuilder`, `go test ./presentation/http/handlers/sitebuilder ./presentation/http` и `git diff --check`. Это покрывает HTTP contract на синтетической схеме; текущий Vitasil browser unlink/source-delete flow ещё выполняется в отдельном пакете.

Новый Chromium `TestSiteEditorTransferReviewBrowserAcceptance` прошёл на текущем Vitasil `hero@1.0.12` и in-memory review fixture `hero@1.0.13` на отдельной disposable БД. Проверены явный отчёт о переносе, legacy/invalid/default findings, отмена обоих review без создания снапшота и подтверждение с `expectedParentContentRevision`; опубликованный и черновой JSON в ребёнке совпали с источником, manifest default не применялся. БД удалена после нулевого session check. `go vet ./cmd/integration` и `git diff --check` прошли.

Отдельно на новой disposable БД `website_engine_snapshot_content_compat_qa_20261107_rootcheck` прошёл `go test -count=1 -v ./cmd/integration -run '^TestSnapshotCreationRejectsStaleParentContentReview$'`: устаревшая content revision вернула HTTP 409 и не создала потомка; актуальная revision создала ребёнка. До прогона подтверждены точное имя базы и 0 других подключений, после теста проверено 0 сессий и удалена только эта БД. Schema defaults в существующие значения не подставляются — compatibility preview явно сообщает об этом, включая legacy и невалидные значения.

Исправлена ошибка прежнего relation integration harness: без Playwright дочерний subtest пропускается, и связанные с ним проверки production-ветки теперь выполняются только если браузер действительно создал и назначил child. `TestExternalVitasilContentRelationsProjectPublishedCatalogIntoPlacements` прошёл дважды на отдельных disposable PostgreSQL: без браузера и с Chromium. Браузерный прогон с текущими Vitasil `catalog@1.0.10`, `main-data@1.0.10` и `background@1.0.7` подтвердил структурный mapping в `/site`, редактирование и порядок связанных записей в `/content`, публикацию и независимость snapshot/branch, а также HTML prerender relations; тестовое ограничение сводной HTML assertion уточнено до placement-specific проверки. Оба disposable DB пока сохранены для общего прогона и точечной очистки.

Vitasil повторно прошёл `npm run typecheck`, `npm run lint` и `npm run build` под Node 20; проверка ZIP распаковки и `manifest.json` прошла для всех девяти текущих архивов: `background@1.0.7`, `catalog@1.0.10`, `catalog-detail@1.0.9`, `footer@1.0.5`, `form@1.0.10`, `header@1.0.8`, `hero@1.0.12`, `main-data@1.0.10`, `not-found@1.0.7`. Эта повторная сборка записала игнорируемые generated ZIP в стандартный `frontend/dist/feature-builds`; исходники, рабочие API, БД, S3 и volumes Vitasil не менялись. ZIP оставлены как вход текущих изолированных E2E до окончания тестовой волны.

## Инвалидация SEO-кеша после структурных и файловых изменений — 7 ноября 2026

`TestSEOCacheInvalidationVitasilAcceptance` прошёл с актуальными read-only ZIP `header@1.0.8`, `catalog@1.0.10`, `catalog-detail@1.0.9`. На disposable PostgreSQL проверены исходный HTML/ETag и conditional 304; создание дочернего снапшота с изменёнными маршрутом каталога, canonical, metadata и design token сформировало новое HTML/ETag; откат ветки восстановил родительский ответ. Удаление используемого публичного файла очистило ссылки в родителе и потомке и обновило оба ответа/ETag; закрытый и удалённый файл возвращали `404`, `private, no-store`, без ETag и без раскрытия UUID частного файла. Затронуты только отдельная test DB и in-memory storage; тест подтвердил ноль прочих сессий до и после, затем удалена только точная disposable DB `website_engine_seo_cache_invalidation_vitasil_qa_20261107_a2`. Рабочий Vitasil не менялся.

Команды успешной проверки:

```sh
go test -count=1 -v ./cmd/integration -run '^TestSEOCacheInvalidationVitasilAcceptance$'
go test -count=1 ./pkg/runtime ./features/sitebuilder
go vet ./cmd/integration ./pkg/runtime ./features/sitebuilder
git diff --check
```

Это подтверждает локальную origin/cache invalidation на реальных ZIP, но не production gateway/CDN, S3 и согласованность между несколькими экземплярами.

## Права и автосохранение редактора контента на Vitasil — 7 ноября 2026

> Историческая проверка старой модели доступа. Её вывод о запрете публикации для `staff`/`user` заменён согласованной матрицей от 8 ноября: все четыре роли могут публиковать model content. Не используйте результат ниже как текущий контракт; актуальная браузерная и API проверка зафиксирована в записи от 8 ноября.

`TestVitasilContentEditorRolesAcceptance` прошёл в Chromium на текущих read-only ZIP `main-data@1.0.10`, `background@1.0.7`, `catalog-detail@1.0.9`. Проверены роли `admin`, `moderator`, `staff`, `user`: site-manager роли читают конфигурацию и публикуют; `staff`/`user` читают и сохраняют черновики, но получают 403 на полный snapshot GET и публикацию. Отказ публикации не меняет published revision или публичные данные. UI проверил загрузку/ошибку/пустое состояние, retry, draft autosave и восстановление после reload при 360/390/1280 px.

Content API projection показал у `background-home` только marker-managed `imageSrc` (`x-editor-managed-by-content`); обычный `catalog-detail.slug` и private sentinel не раскрыты. Начальный staff draft GET вернул пустой workspace, далее UI отправил PUT revision 0 с `placementId=background-home` и legacy media ID; PUT вернул 200, следующий GET — revision 1, publishedRevision 0 и то же значение. Опубликованный background content не изменялся. Первоначальное падение теста оказалось гонкой ожидания перед асинхронным autosave; после ожидания фактического PUT acceptance прошёл.

Frontend проверки в disposable checkout без записи в tracked `pkg/static/dist` и `pkg/runtime/dist`: Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 файлов/85 тестов) и `pnpm build`. Также прошли focused integration, `go vet ./cmd/integration`, `gofmt` и `git diff --check`. Тест использовал отдельную disposable БД `website_engine_vitasil_content_roles_qa_20261107_02`, PostgreSQL container `website-engine-vitasil-content-roles-qa2-20261107` на 35493 и временную копию `/tmp/website-engine-content-role-qa-20261107-01`. После нулевого session check удалены точные БД, контейнер, его anonymous volume и temp copy; postflight подтвердил отсутствие ресурса и listener. Рабочий Vitasil API, БД, storage и volumes не затрагивались.

Точная браузерная команда: `go test -count=1 -v ./cmd/integration -run '^TestVitasilContentEditorRolesAcceptance$'`. Расширенная спецификация и точные frontend команды записаны в [SPEC-content-editor-acceptance.md](SPEC-content-editor-acceptance.md). Полная ручная screen-reader и accessibility проверка остаётся открытой в TODO 438.

## Cross-install Sitepack и S3 media для связанного контента Vitasil — 7 ноября 2026

`TestContentRelationsSitepackMediaVitasilBrowserAcceptance` прошёл в Chromium на актуальных read-only ZIP `catalog@1.0.10` и `main-data@1.0.10`, с двумя независимыми disposable Engine и раздельным MinIO storage. Проверены source/target placements, опубликованные и черновые relation selections/overlays с порядком, экспорт/импорт через `/snapshots/tree`, remap source EntryID и media ID, перенос только принадлежащего экспорту публичного объекта; private admin и другого service-owner файлы не попали в archive, picker или destination DB. Импорт не назначил снапшот: явное назначение выполнено отдельно через `/snapshots/branches`.

После импорта relation editor выбрал уже имеющийся публичный файл через media picker. Vitasil prerender отдал связанный контент с Engine URL `/media/{id}`; браузер загрузил и декодировал изображение при отключённом JavaScript, Range через Engine media handler вернул 206. Ответы не раскрыли S3 endpoint или прямой storage URL. Первый Chromium запуск остановился на встроенном `showSaveFilePicker` headless-поведения; acceptance применил test-only stub по образцу существующего sitepack test, без изменения production UI.

Команды успешной проверки:

```sh
go test -count=1 -v ./cmd/integration -run '^TestContentRelationsSitepackMediaVitasilBrowserAcceptance$'
go vet ./cmd/integration ./features/sitebuilder ./features/files
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm typecheck
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm lint
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm test
gofmt -d cmd/integration/content_relations_sitepack_media_vitasil_browser_acceptance_test.go
git diff --check
```

Тест использовал точные disposable БД `website_engine_sitepack_transfer_qa_relm_1107_a3d9_src` и `website_engine_sitepack_transfer_qa_relm_1107_a3d9_dst`, а также отдельный MinIO container `website-engine-content-relations-sitepack-media-minio-20261107-a3d9c6` на 39127. Перед/после проверялись нулевые посторонние DB sessions; после теста удалены обе БД и точный контейнер с tmpfs `/data` 512 MiB. Postflight подтвердил отсутствие БД, контейнера и listener. Общий disposable PG сохранён другим пакетам; рабочий Vitasil API, DB, S3 и volumes не использовались.

## Производный состав фич и зависимости редактора сайта — 7 ноября 2026

`TestFeatureCompositionBrowserAcceptance` прошёл в Chromium на disposable Engine и read-only ZIP Vitasil `catalog@1.0.10`, `catalog-detail@1.0.9`, `header@1.0.8`, `main-data@1.0.10`. Для проверки смены версии использовалась отдельная in-memory ZIP-копия `catalog@1.0.11`, исходный Vitasil архив не изменялся. Проверены только отображение реально размещённых сборок, отсутствие ручного управления составом/upload/content API/branch assignment в `/site`, наследование composition при создании потомка, блокировка orphaned `collectionSource`, обновление версии всех placements и source, блокировка удаления последнего источника до снятия зависимости, удаление сборки только из потомка и сохранность parent structure, опубликованного/чернового контента, ветки и глобального build registry. `feature-selection.test.ts` дополнительно покрывает состав по `contentBindings`.

Успешная проверка: Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 файлов/85 тестов), `pnpm build` в disposable source copy; `go test -count=1 -v ./cmd/integration -run '^TestFeatureCompositionBrowserAcceptance$'`; `go vet ./cmd/integration`; `gofmt` и `git diff --check`. Ошибки первых browser запусков относились к фикстуре/assertions (удалена только одна из двух placements, затем нарушался тип detail-страницы); тест исправлен и финальный запуск прошёл.

Использована только disposable PostgreSQL `website_engine_feature_composition_qa_20261107_a94cc376` на loopback `127.0.0.1:35474` с in-memory storage. Preflight/postflight подтвердили точную БД и отсутствие других сессий; БД удалена. Временная копия `/tmp/website-engine-feature-composition-20261107.Ed12Ud` отсутствует. Общий PostgreSQL-контейнер `website-engine-system-pages-css-qa-20261107-02` на 35474 оставлен, пока он нужен следующему изолированному acceptance-пакету. Рабочий Vitasil API, БД, storage и volumes не использовались.
## Фактическая CORS-проверка внешнего запроса в Chromium — 7 ноября 2026

`TestExternalBackendActualCORSAndRetryBrowserAcceptance` прошёл против контролируемых HTTP origins на отдельных loopback listeners. Chromium получил успешный cross-origin GET только при точном `Access-Control-Allow-Origin`; отсутствующий и чужой CORS origin завершились browser network error, после Retry запрос восстановился. Проверены читаемый HTTP 503, AbortController timeout и восстановление, CSP-блокировка незарегистрированного origin до его listener-а, отсутствие prerender и общий cache для страниц с backend binding. Код test-фичи использует обычный публичный backend contract; Engine API/ABI и Vitasil не менялись.

Проверки: `go test -count=1 -v ./cmd/integration -run '^TestExternalBackendActualCORSAndRetryBrowserAcceptance$'`, focused `go test ./features/sitebuilder -run 'Test(ValidatePublicBackendBaseURL|ManifestValidatesRequestSlot|SiteConfigValidatesBackendReferences|ResolveBackendRequest|ResolveRequestPath|PublicBackendOrigins)' -count=1`, `go vet ./features/sitebuilder ./cmd/integration`, `git diff --check` — PASS, по зафиксированному результату в [SPEC-external-backend-bindings.md](SPEC-external-backend-bindings.md).

Приёмка использовала disposable PostgreSQL `website_engine_external_backend_browser_qa_20261107_01` и контейнер `website-engine-external-cors-qa-20261107-01` на 35488 (tmpfs, без volume); оба task-owned ресурса удалены после проверки сессий. Текущий production provider и его CORS policy не тестировались, поскольку endpoint не предоставлен. Рабочие Vitasil API, БД, storage и volumes не использовались.

## Сквозные проверки редактора, системных страниц и prerender — 7 ноября 2026

### Nested content relations на Vitasil

`TestContentRelationsNestedRecordsBrowserAcceptance` — PASS за 8.52 s в Chromium на read-only `catalog@1.0.10` и `main-data@1.0.10`. Проверены создание/дублирование nested rows с уникальными manifest IDs, порядок и значения при сортировке, autosave/reload, ручное сохранение overlay, сохранение source EntryID и overlays при смене slug, draft/publication separation и неизменность родителя и двух назначений веток. Пройдены `go test -run '^$' ./cmd/integration`, `go vet ./cmd/integration`, frontend Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 suites/85 tests), `gofmt -d` и scoped whitespace checks.

Использована disposable DB `website_engine_content_nested_qa_20261107_e3a1f485` на `127.0.0.1:35474`; pre/postflight показали 0 посторонних sessions, DB удалена. Object store был in-memory fake. Рабочие API, DB, storage и volumes Vitasil не использовались.

### Межинстанционная согласованность cache

`TestVitasilPrerenderCacheMultiInstanceAcceptance` — PASS за 7.87 s. Два отдельных public router/prerender worker с собственными in-memory caches на общем branch ID и disposable DB после публикации Vitasil content, назначения child snapshot и rollback вернули одинаково актуальные HTML и ETag. После создания worker с холодным cache текущие состояние и назначение разрешились заново из БД. Проверены `catalog@1.0.10` и `catalog-detail@1.0.9`.

Команда: `go test -timeout 10m -count=1 -v ./cmd/integration -run '^TestVitasilPrerenderCacheMultiInstanceAcceptance$'`. Disposable DB `website_engine_seo_multi_instance_vitasil_qa_20261107_root01` на `127.0.0.1:35474` имела 0 посторонних sessions до и после; удалена. Storage был in-memory. Это не проверяет отдельный process restart, distributed cache, gateway/CDN или production traffic.

### Локальная performance matrix

`TestSEOPrerenderVitasilPerformanceMatrixAcceptance` — PASS за 111.60 s: 120 browser navigations по шести реальным Vitasil маршрутам, двум viewports (desktop `1280×800/DPR1`, mobile `390×844/DPR2`), пяти повторам и cold/warm Go cache. По 60 cold и 60 warm образцам медианы TTFB/FCP/LCP — `884.0/912/918 ms` и `7.8/32/40 ms`; CLS равен 0 во всех samples. Worker miss duration median `878.545 ms`, queue wait практически нулевой; cache-hit render median `0.001 ms`. По маршрутам записаны HTTP status, LCP/FCP, JS/CSS bytes, requests, DOM mutations и безопасная worker telemetry. ZIP: `header@1.0.8`, `hero@1.0.12`, `catalog@1.0.10`, `catalog-detail@1.0.9`, `form@1.0.10`, `not-found@1.0.7`.

Команда: `go test -timeout 20m -count=1 -v ./cmd/integration -run '^TestSEOPrerenderVitasilPerformanceMatrixAcceptance$'`. DB `website_engine_seo_performance_qa_20261107_root02` на `127.0.0.1:35474` проверена до/после (0 чужих sessions) и удалена; storage in-memory. Это unthrottled loopback lab measurement, не Lighthouse, INP/field CWV, production, нагрузочный тест или before/after оптимизации. Worker duration не декомпозирован по стадиям; mutation timing не изолирует hydration. Точная таблица — в [SPEC-seo.md](SPEC-seo.md).

### Worker startup/timeout fault injection

Повторно пройден `TestPrerenderWorkerVitasilFaultAcceptance` (PASS, 4.61 s) на read-only `header@1.0.8`, `hero@1.0.12`, `not-found@1.0.7`. Startup failure и timeout сохраняли 200/404, выдавали `no-store` без ETag/304 и не кэшировали ошибку. Безопасные structured diagnostics не содержали secret markers; успешный Node в том же worker восстановил prerender, ETag и conditional 304. Timeout завершил дочерний process.

Команда: `go test -timeout 5m -count=1 -v ./cmd/integration -run '^TestPrerenderWorkerVitasilFaultAcceptance$'` с Node 20, текущими ZIP Vitasil и Chromium из `SPEC-seo.md`. Disposable DB `website_engine_prerender_worker_fault_qa_20261107_root03` на 35474 имела 0 посторонних sessions до/после и удалена. Общий принадлежащий QA PostgreSQL container использовали только как хост disposable DB; его сохранили, поскольку он нужен следующим тестам. Рабочие Vitasil API/DB/storage не использовались.

### Публичные системные страницы и доступность редактора

В Chromium прошёл `TestSystemPagesBrowserAccessibilityAcceptance` на read-only `not-found@1.0.7`: no-JS 404 override, core 500 fallback и встроенная 503 проверены на ширинах 360/390/1280 px. Проверены HTTP status, `no-store`, роли/имена и main landmark accessibility snapshot, title/H1, keyboard focus, reduced-motion idle state, базовые contrast pairs и overflow. Исправлен пустой `<title>` у error response без metadata title; явные заголовки и успешные страницы не меняются. Также прошёл `TestSystemPageDocumentTitleFallback`.

Команда: `go test -count=1 ./cmd/integration -run '^(TestSystemPagesBrowserAccessibilityAcceptance|TestSystemPageDocumentTitleFallback)$'`; дополнительно `go test -run '^$' ./cmd/integration`, `go vet ./cmd/integration ./pkg/runtime`, Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 files/85 tests), `gofmt -d` и scoped whitespace checks. Disposable DB `website_engine_system_pages_browser_qa_20261107_8d1f`, container `system-pages-a11y-qa-20261107-8d1f`, port 35484 и volume `2989e8a9246fbbdc6ddba40a540300abcc15b4117af4448a86c7734c5d0913ea` удалены после zero-session check. Manual VoiceOver, actual speech announcements, interaction-triggered motion, all themes and a public 403 origin remain unverified.

`TestVitasilContentEditorKeyboardAndResponsiveAccessibility` — PASS на read-only `main-data@1.0.10` и `hero@1.0.12`. Проверены accessible control names, keyboard reorder/focus retention, nested editing, manual save, publication, 503 retry, revision conflict/overwrite modal lifecycle, scroll/focus restoration после закрытия диалога, существующий media picker и responsive layout 360/390/1280 без file upload в редакторе.

Прошли Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (20 files/85 tests), `go vet ./cmd/integration`, `gofmt -d` и scoped whitespace checks. Disposable DB `website_engine_content_editor_accessibility_qa_20261107_02` на 35475 прошла pre/post zero-session checks; удалены только DB, container `website-engine-content-editor-accessibility-qa-pg-20261107-02`, его volume `1d93b847eb917268014dcf46bab9d6f1b39e3205a853ed7ea4c1250f900394be` и временный binary. Ручной screen reader не запускался; рабочие Vitasil API/DB/storage/volumes не использовались.

## Радикальный переход `/site` + ER-модель + `/content` — acceptance 7 ноября 2026

Проверены актуальные read-only исходники Vitasil и временная сборка всех девяти фич в `/tmp/website-engine-model-content-radical-vitasil-feature-builds-20261107-r1`; пользовательский `frontend/dist/feature-builds` не перезаписывался. Vitasil `pnpm typecheck`, `pnpm lint` и `FEATURE_BUILD_OUTPUT_DIR=... pnpm build:features` прошли. Все ZIP (`background@2.0.0`, `catalog@2.0.0`, `catalog-detail@2.0.0`, `footer@2.0.0`, `form@2.0.0`, `header@2.0.0`, `hero@2.0.0`, `main-data@2.0.1`, `not-found@2.0.0`) приняты Engine как format 2, `static-module-v1`, с явным `dataInputs`. ABI фичей не менялся.

Focused API и browser acceptance прошёл на disposable PostgreSQL `website_engine_model_content_qa_radical_side_20261107_r1` в контейнере `website-engine-model-content-radical-sideqa-20261107-r1`, loopback port `35517`, с task-owned label `codex.qa`. Перед каждым test setup подтверждались точная база и ноль других сессий; helper повторял ту же проверку перед `TRUNCATE`. Контейнер использовал tmpfs и не имел volume. Storage тестового Engine был in-memory; S3 bucket/object не создавались. Рабочие Vitasil API, БД, storage и volumes не использовались.

Точный focused запуск:

```sh
TEST_DATABASE_URL='<loopback DSN собран внутри команды из env task-owned QA PostgreSQL>' \
TEST_FEATURE_DIR='/tmp/website-engine-model-content-radical-vitasil-feature-builds-20261107-r1' \
PRERENDER_TEST_NODE='/Users/docup/.nvm/versions/node/v20.19.4/bin/node' \
PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' \
PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' \
ADMIN_UI_TEST_OUTPUT_DIR='/tmp/website-engine-model-content-radical-admin-side-20261107-r13' \
go test -count=1 -run 'TestModelContentAPIWorkspaceLifecycle|TestVitasilAllCurrentDataModelBuildsAccepted|TestVitasilSharedProductModelContentAcceptance|TestMediaRenditionsResolveSelectedSnapshotWithoutActiveBranchAssignment|TestModelMediaRenditionsRetryRestartPublishAndRuntime' -v \
  cmd/integration/api_test.go cmd/integration/rendition_processor_test.go \
  cmd/integration/model_content_acceptance_helpers_test.go \
  cmd/integration/model_content_api_acceptance_test.go \
  cmd/integration/vitasil_model_content_acceptance_test.go \
  cmd/integration/media_renditions_test.go
```

Все пять tests прошли. API покрыли optional fields, type/enum/unique/media/relation validation, revision conflict, draft/publication/history/restore, fork copy and isolation, selections, branch assignment isolation, media reference cleanup and media rendition retry/restart. Chromium на актуальных `catalog`, `catalog-detail`, `main-data` создал сущность и relation в ER-editor, сохранил их дочерним снапшотом, создал category record, связал Product, вручную сохранил и отдельно опубликовал. API подтвердил опубликованные values/relation и неизменную привязку ветки к parent. Документная ширина не превышала 360/390/768/1280 CSS px; `/site` и `/content` не содержали branch controls; в `/content` не обнаружена схема CMS из manifest; browser page errors отсутствовали.

При этой проверке исправлена подпись записи в `/content`: если первым полем сущности идёт технический slug, список теперь предпочитает поле, чьё имя похоже на название/заголовок. Unit-test проверяет приоритет русскоязычного названия над slug.

Дополнительные успешные проверки: root `go test ./features/... ./pkg/... ./presentation/http/... ./cmd/api`, `go vet ./features/... ./pkg/... ./presentation/http/... ./cmd/api`, Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (21 файл, 94 теста), `pnpm build`, Vitasil `pnpm typecheck`, `pnpm lint`, `pnpm typecheck:feature-lab`, Feature Lab 4 contract tests, `npm run docs:check-generated`, `npm run docs:build`, `gofmt` и `git diff --check`. `migrations/atlas.sum` was independently recomputed with Atlas v1.3.0's `HashDir` algorithm and matches the clean single-migration directory.

**Историческая запись, заменена разделами ниже от 8 ноября:** на тот момент полная Go-сборка блокировалась компиляцией `cmd/integration`. Тогда 22 integration-файла содержали 278 diagnostics по снятому per-feature контракту, в том числе 276 ссылок на `SaveContentWorkspace`, `CollectionSource`, старые relation envelopes и поля Sitepack. Эти fixtures затем заменены; текущее состояние и открытые критерии указаны в двух более поздних разделах ниже.

Для этой приёмки после подтверждения нулевых DB sessions и завершения тестовых процессов удалены disposable container `website-engine-model-content-radical-sideqa-20261107-r1` без Docker volume, temporary feature ZIP output, compile log и скриншоты `...-r1`–`...-r13`. Postflight подтвердил отсутствие контейнера и освобождение loopback port 35517. Storage был in-memory; S3 buckets/objects не создавались. Рабочие Vitasil API, БД, storage и volumes не использовались.

## Явная очистка модельных данных после изменения структуры — 8 ноября 2026

Исправлен блокирующий переходный сценарий: `CreateSnapshot` раньше отказывался создавать потомка, если новая модель переставала содержать поле, связь или сущность унаследованного контента. Это не давало редактору `/content` показать данные для согласованной очистки. Теперь потомок сохраняет их без потерь, получает предупреждение несовместимости, а API draft save/publication отклоняют их до исправления. `/content` показывает отсутствующие поля/связи и записи удалённых сущностей; очистка значений, связей и удаление orphan-record требуют явного подтверждения. Родительский snapshot неизменен. Полное удаление модели при существующем content workspace по-прежнему блокируется, поскольку model-less snapshot не имел бы editor для восстановления.

`TestModelContentForkRequiresExplicitCleanupWhenStructureDropsField` прошёл на disposable PostgreSQL `website_engine_model_content_qa_orphan_20261108_01`, контейнер `website-engine-model-content-orphan-qa-20261108-01`, loopback port `35591`, task label `codex.qa=website-engine-model-content-orphan-qa-20261108-01`, tmpfs 512 MiB, без Docker volume и S3. Helper подтвердил точную БД, префикс disposable DB и ноль других подключений перед `TRUNCATE`. Проверены snapshot warning, сохранение старого значения в потомке, отказ несовместимого draft, явное удаление поля из draft, успешная публикация очищенного потомка и сохранность исходного поля/значения родителя.

Команда прошла:

```sh
TEST_DATABASE_URL='<loopback DSN для website_engine_model_content_qa_orphan_20261108_01>' \
go test -count=1 -run '^TestModelContentForkRequiresExplicitCleanupWhenStructureDropsField$' -v \
  cmd/integration/api_test.go cmd/integration/rendition_processor_test.go \
  cmd/integration/model_content_acceptance_helpers_test.go \
  cmd/integration/model_content_api_acceptance_test.go \
  cmd/integration/vitasil_model_content_acceptance_test.go \
  cmd/integration/media_renditions_test.go
```

Frontend checks: Node 20 `pnpm typecheck`, `pnpm lint`, focused Vitest on `model-content-workspace.test.ts` plus `DataModelPage.test.ts` — PASS (2 suites, 21 tests), full `pnpm test` — PASS (21 suites, 96 tests), and `pnpm build` — PASS for admin and runtime. `go test ./features/... ./pkg/... ./presentation/http/... ./cmd/api` and matching `go vet` — PASS. Docs `npm run docs:check-generated` and `npm run docs:build` — PASS. New unit coverage confirms explicit cleanup preserves valid fields/relations and record deletion removes dangling record and selection references. The disposable container was removed after postflight confirmed the task label, no mounts, zero database sessions and no running integration process; postflight confirmed the container is absent and port 35591 is free. There was no S3 storage or host temp directory for this test. The working Vitasil API, database, object storage and volumes were not accessed.

## Повторная сборка Vitasil и model-content browser acceptance — 8 ноября 2026

Vitasil `pnpm typecheck`, `pnpm lint` и `FEATURE_BUILD_OUTPUT_DIR=/tmp/website-engine-vitasil-feature-builds-model-cutover-20261108-02 pnpm build:features` прошли с Node 20. Собраны девять текущих ZIP: `background@2.0.0`, `catalog@2.0.0`, `catalog-detail@2.0.0`, `footer@2.0.0`, `form@2.0.0`, `header@2.0.0`, `hero@2.0.0`, `main-data@2.0.1`, `not-found@2.0.0`; `unzip -t` прошёл для каждого. Manifest contract tests прошли 7/7 через абсолютный Node 20. `pnpm typecheck:feature-lab` прошёл. Первый запуск `node --test` после pnpm script подобрал сломанный Homebrew Node (`libllhttp.9.4.dylib` отсутствует); повторный запуск `/Users/docup/.nvm/versions/node/v20.19.4/bin/node --test scripts/feature-manifest-contract.test.mjs` прошёл 7/7.

На disposable PostgreSQL прошли `TestModelContentAPIWorkspaceLifecycle`, `TestModelContentForkRequiresExplicitCleanupWhenStructureDropsField`, `TestVitasilAllCurrentDataModelBuildsAccepted`, `TestVitasilSharedProductModelContentAcceptance`, `TestMediaRenditionsResolveSelectedSnapshotWithoutActiveBranchAssignment` и `TestModelMediaRenditionsRetryRestartPublishAndRuntime`. Все девять ZIP зарегистрированы в Engine; Chromium проверил model editor `/site`, content editor `/content` и назначения в `/snapshots/branches`. Для трех текущих Vitasil features `catalog`, `catalog-detail` и `main-data` проверены общий Product, выбранный и упорядоченный subset слайдера, создание категории и связи, ручное draft save, отдельная публикация, отсутствие изменения branch assignment, отсутствие старой feature schema/branch UI в редакторах, отсутствие page errors и overflow при 360/390/768/1280 px. Дополнительно проверен UI autosave: draft изменился с опубликованной ревизии 5 на ревизию 6, пережил reload и не был опубликован автоматически. Browser test явно ждёт закрытия publish confirmation перед скриншотом; повторные screenshots подтвердили чистое состояние без модального overlay.

Команда API/browser suite:

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:35593/website_engine_model_content_qa_final_20261108_02?sslmode=disable' \
TEST_FEATURE_DIR='/tmp/website-engine-vitasil-feature-builds-model-cutover-20261108-02' \
PRERENDER_TEST_NODE='/Users/docup/.nvm/versions/node/v20.19.4/bin/node' \
PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' \
PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' \
ADMIN_UI_TEST_OUTPUT_DIR='/tmp/website-engine-model-cutover-browser-qa-20261108-03' \
go test -timeout 10m -count=1 -run '^(TestModelContentAPIWorkspaceLifecycle|TestModelContentForkRequiresExplicitCleanupWhenStructureDropsField|TestVitasilAllCurrentDataModelBuildsAccepted|TestVitasilSharedProductModelContentAcceptance|TestMediaRenditionsResolveSelectedSnapshotWithoutActiveBranchAssignment|TestModelMediaRenditionsRetryRestartPublishAndRuntime)$' -v \
  cmd/integration/api_test.go cmd/integration/rendition_processor_test.go \
  cmd/integration/model_content_acceptance_helpers_test.go \
  cmd/integration/model_content_api_acceptance_test.go \
  cmd/integration/vitasil_model_content_acceptance_test.go \
  cmd/integration/media_renditions_test.go
```

Disposable resources: PostgreSQL container `website-engine-model-content-final-qa-20261108-02`, label `codex.qa=website-engine-model-content-final-qa-20261108-02`, DB `website_engine_model_content_qa_final_20261108_02`, loopback port `35593`, tmpfs `/var/lib/postgresql/data` (no volume), and temporary build/screenshot paths named above. Before the test helper truncated the DB it verified the exact local DB prefix and zero other sessions; before cleanup, the container label/mounts and zero sessions were checked again. Container stopped and removed by exact name, port released, and only the three task-owned temporary directories removed. Test storage was in-memory; no S3 bucket/object was created. The working Vitasil API, DB, storage and volumes were not accessed.

**Historical checkpoint; superseded by the following integration-fixture and harness sections.** At that point `go test ./...` still failed to compile `cmd/integration`, and cross-install model Sitepack had not yet run. Both facts were subsequently resolved; remaining acceptance gates are listed in the latest section below.

## Замена legacy integration fixtures и повторная сквозная отладка — 8 ноября 2026

Предыдущая запись выше была актуальна до миграции integration fixtures и заменена этой проверкой: ссылки на удалённый per-feature CMS API в тестах устранены; сценарии переписаны на model API, snapshot/branch, текущие files/forms, runtime и format-2 Vitasil manifests. Компиляция всего integration package больше не имеет ошибок. Исправлены проверки веток, которые ошибочно полагались на порядок результата; fixture формы теперь явно выбирает модельную запись в placement; Vitasil responsive header/hero и media-picker fixtures выбирают модельные записи и публикуют нужный workspace; provenance снапшота возвращается в metadata, не попадая в runtime payload; sitemap test проверяет действующий публичный ETag/revalidation contract. Устаревший orphan helper `vitasil_content_editor_e2e_test.go` удалён.

Пройдены проверки:

```sh
go test -gcflags=all=-e -run '^$' ./cmd/integration
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./features/sitebuilder ./features/files ./pkg/runtime
```

Полный integration suite прошёл с актуальными архивами Vitasil на выделенной disposable PostgreSQL; два сценария, требующие независимой рестартуемой S3/browser среды, были исключены из общего процесса и каждый прошёл отдельным запуском:

```sh
TEST_DATABASE_URL='postgres://postgres:local-test-only@127.0.0.1:35513/website_engine_model_content_qa_20261108_01?sslmode=disable' \
TEST_FEATURE_DIR='/Users/docup/Projects/vitasil/frontend/dist/feature-builds' EXPECT_STATIC_FEATURES=1 \
go test -count=1 -parallel=1 \
  -skip '^(TestFilesHTTPResumesPrivateUploadAfterAPIRestartWithS3|TestSubmissionsBrowserAcceptanceWithVitasilForm)$' \
  ./cmd/integration
```

Отдельно прошли `TestFilesHTTPResumesPrivateUploadAfterAPIRestartWithS3`, `TestSubmissionsBrowserAcceptanceWithVitasilForm`, `TestAdminMediaGallerySharesAdminFileOwnerAndServiceKeysStayScoped`, `TestAdminMediaGalleryAndPickerBrowserAcceptance`, `TestVitasilDesignMediaPickerPersistsPublicAssets`, `TestExternalBackendBindingsDisposableAcceptance` и `TestExternalBackendActualCORSAndRetryBrowserAcceptance`. Browser acceptance прогнана на актуальных ZIP Vitasil для model-backed catalog/detail/main-data, form submissions, header/hero responsive runtime, дизайн-токенов и медиапикера. Disposable Engine/Vitasil использовался; рабочие Vitasil API, БД, storage и volumes не использовались.

Frontend и контроль артефактов прошли: Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test` (21 suites, 96 tests), `pnpm build`; повторная сборка Vitasil девяти ZIP, `unzip -t` для каждого, manifest contract tests (7/7), Feature Lab typecheck, `git diff --check`. Атлас-сумма соответствует текущей чистой директории единственной начальной миграции.

Для этого прогона использовались только точные disposable ресурсы: PostgreSQL `website-engine-model-content-test-20261108-01` на `127.0.0.1:35513` с БД `website_engine_model_content_qa_20261108_01`, `website_engine_vitasil_archive_qa_20261108_01`, `website_engine_submissions_ui_qa_20261108_01`, `website_engine_files_media_qa_20261108_01`, `website_engine_files_media_qa_20261108_design_01`, `website_engine_external_backend_qa_20261108_01`, `website_engine_external_backend_browser_qa_20261108_01`; MinIO `website-engine-model-content-files-qa-20261108-01` на `127.0.0.1:35514–35515` с buckets `files-media-qa-20261108-01` и `files-media-qa-design-20261108-01`. У PostgreSQL data tmpfs; MinIO использовал tmpfs 1 GiB; Docker volumes не создавались. Все DB/bucket/container идентификаторы выше были записаны до запуска тестов.

На момент этого прохода оставались открытыми cross-install model-content Sitepack, all-nine Vitasil browser acceptance, полный UI matrix revision/autosave/conflict/permissions, графический ER editor keyboard/accessibility и ручной screen-reader сценарии. Последующие записи ниже закрыли cross-install Sitepack, UI revision recovery и all-nine Vitasil browser acceptance; ER keyboard/accessibility и screen-reader остаются открытыми. Полные отметки TODO не выставлены.

## Актуализация integration harness и clean migration — 8 ноября 2026

Эта запись supersedes более ранние строки выше, где `go test ./...` ошибочно описан как некомпилируемый и model Sitepack cross-install указан как непроверенный. Старые fixtures удалённого per-feature CMS контракта переведены на snapshot model API, ветки, текущие files/forms/runtime и manifest format 2. Исправлены ещё два дефекта harness: сценарии для отдельной БД теперь пропускаются, если общий запуск направлен в другую QA namespace (ранее они завершались ложной ошибкой); `TestSitepackRejectsConflictingFeatureVersionBeforeStorageWrites` закрывает источник и его DB pool перед повторным `newTestEnv`, чтобы не сбрасывать БД при активном соединении.

`newTestEnv` — общий destructive integration fixture: перед миграцией и `TRUNCATE` он теперь требует loopback host и имя `website_engine_*_qa_*`, подтверждает фактическую подключённую БД и отсутствие остальных DB sessions. Запуск по не-loopback DSN останавливается, несовпадающий локальный QA namespace пропускается. Это защищает API integration tests от случайного запуска против рабочей БД и не затрагивает прочие процессы.

Чистая схема закреплена в единственной начальной миграции `migrations/0001_init.sql`: legacy per-feature CMS/relation tables удалены, SQL `ALTER` в baseline отсутствуют, constraints/defaults задаются при `CREATE TABLE`. На disposable raw PostgreSQL применение дало 18 таблиц и 232 constraints; старые таблицы `site_snapshot_content`, `feature_entries` и `content_relations` отсутствуют. `postgres.Migrate` применил ту же схему к новой disposable БД после проверки `migrations/atlas.sum`. `TestFreshInitialSchemaHasNoLegacyTablesOrAlterStatements` и `go test -count=1 ./migrations` прошли. `migrations/atlas.sum` сверена с Atlas v1.3.0 `HashDir` алгоритмом.

Проверки текущего кода:

```sh
go test -count=1 ./migrations
env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_SITEPACK_SOURCE_DATABASE_URL \
  -u TEST_SITEPACK_TARGET_DATABASE_URL -u TEST_SITEPACK_S3_ENDPOINT \
  -u TEST_SITEPACK_S3_ACCESS_KEY -u TEST_SITEPACK_S3_SECRET_KEY \
  -u TEST_SITEPACK_S3_BUCKET go test -count=1 ./...
go vet ./...
go test -count=1 -parallel=1 -timeout=40m -v ./cmd/integration
git diff --check
```

Все перечисленные команды прошли. Полный `cmd/integration` выполнялся на свежей БД `website_engine_vitasil_archive_qa_20261108_root04` в task-owned disposable контейнере `website-engine-model-cutover-db-qa-20261108-root01`, loopback `127.0.0.1:35492`; он включал branch/snapshot races, draft/publish, public routing, responsive design-token и Vitasil header/hero Chromium tests, system pages, sitemap/cache identity, snapshot history, registrations всех девяти Vitasil ZIP и model/runtime guards. Продолжительность: 12.282s. Он ожидаемо пропустил проверки, требующие отдельные namespace DB/S3 или `ADMIN_UI_TEST_*`: model-content, files/media browser, submissions browser, external-backend CORS и design media. `TestAdminShellTouchTargetsBrowserAcceptance` отдельно прошёл с явно заданным built admin output и Chromium.

Пропущенные в агрегированном запуске группы отдельно прошли на чистой схеме: model-content lifecycle/fork, shared Product catalog/detail/slider, all-nine v2 ZIP registration and renditions; files API restart/resume, admin media gallery/picker, Vitasil design media picker; Vitasil form submissions browser; external backend binding/CORS/retry browser. `TestModelSitepackCrossInstallAcceptance` также прошёл с двумя disposable БД `website_engine_sitepack_cross_install_qa_src` и `website_engine_sitepack_cross_install_qa_dst`, объектным bucket `sitepack-cross-install-qa-20261108-c42c4eed` и текущими ZIP `catalog@2.0.0`, `catalog-detail@2.0.0`, `main-data@2.0.1`. Проверены UUID remap, draft/published/history/selection import, private/public media ACL, повторный импорт, preflight без записи и отказ при конфликте immutable build. Точные результаты специализированных acceptance приведены в отчётах выше; ни один тест не писал в рабочий Vitasil.

Task-owned QA ресурсы этого повторного прохода: PostgreSQL container `website-engine-model-cutover-db-qa-20261108-root01` (порт 35492, tmpfs, без volume), MinIO container `website-engine-model-cutover-minio-qa-20261108-root01` (порты 39213/39214, tmpfs), перечисленные в записях выше disposable DBs и task buckets. После завершения тестов и проверки нулевых DB sessions удалены только эти два точных контейнера; проверены освобождение их портов и отсутствие временного ZIP-каталога и schema dump файлов. Перекрёстный Sitepack пакет ранее удалил свои точные БД/MinIO контейнеры, buckets, volumes и staging directory после zero-session проверки; порты 35493/35494/39211/39212 освобождены. Рабочий Vitasil stack, DB, S3, volumes и данные не использовались.

На момент этого полного integration прогона полная задача оставалась открыта: не все девять Vitasil placements проходили browser acceptance и расширенная UI/accessibility матрица была неполной. Focused all-nine browser acceptance приведена ниже; оставшиеся UI/accessibility критерии по-прежнему требуют выполнения.

## Независимая приёмка ER/editor, ролей и revisions — 8 ноября 2026

Повторно сверены актуальные `AGENTS.md`, TODO, TOPLAN, `SPEC-data-model.md`, `SPEC-site-editor-workspace.md`, `SPEC-content-editor-acceptance.md`, synthetic acceptance spec и оба git-дерева. Engine commit: `04d16823449e54958808ccc4e0b3ac93c87cfaa5` плюс текущая dirty worktree; Vitasil worktree уже содержала широкие пользовательские изменения (539 путей в `git status --short` на момент сверки). В этом проходе Vitasil source, API, DB, S3 и volumes не изменялись и не использовались.

### Успешные независимые проверки

- `TestIndependentProductEditorBrowserAcceptance` — PASS в resource-limited Docker runner с Chromium sandbox. Synthetic format-2 feature ZIP проверили графическое создание сущностей, поля scalar/enum/media, отношения 1:1/1:N/N:M, keyboard-операцию, typed binding включая nested relation, отклонение несовместимых input paths, сохранение структуры дочерним snapshot и неизменность родителя/назначения ветки. Ширины Chromium: 360/390/768/1280 CSS px; горизонтального overflow и page errors нет. До повторного прохода исправлены только тестовые ошибки locator и JS result-key names в принадлежащем QA acceptance-файле.
- `TestIndependentProductSyntheticAcceptance` — PASS в том же строгом sandbox с fakeStore. Публичная выдача синтетических фич и отдельный prerender browser subcase проверили общую запись каталога/detail/collection, опубликованную проекцию и SEO HTML.
- `TestIndependentProductSyntheticAcceptanceWithS3` — PASS на disposable PostgreSQL и private MinIO. Проверены уникальная БД без посторонних sessions, приватный bucket, media bytes/metadata roundtrip и несовместимые bindings. Prerender variables были намеренно не заданы: тест не проверяет S3-backed media и prerender в одном browser-сеансе.
- `TestIndependentProductSitepackCrossInstallAcceptance` — PASS: три synthetic builds, published/draft records, nested relations, два порядка selections, remapping model/record/media IDs, private MinIO byte/digest check и явное branch assignment после импорта. Source/target были разными disposable установками.
- `TestRoleRevisionMatrixAcceptance` — PASS на disposable PostgreSQL. Все `admin`, `moderator`, `staff`, `user` прочитали/сохранили/опубликовали/восстановили content; staff/user получили 403 на структуру/build/branch assignment без побочных изменений; admin/moderator меняют статус/архивируют submissions, только admin удаляет; все четыре роли используют media, безвозвратное удаление только admin. Также пройдены stale save/publish/restore/structural-fork checks и отсутствие частичной записи.
- Скопированные в sandbox integration fixtures прошли compile-only `go test -mod=readonly -run '^$' ./cmd/integration` и `go vet -mod=readonly ./cmd/integration`.
- Текущие frontend sources прошли в isolated Node 20 container: `node /pnpm-cli/bin/pnpm.cjs typecheck` и `node /pnpm-cli/bin/pnpm.cjs lint`. Runner использовал Node `v20.20.2`, `--network none`, read-only source/tool mounts, scratch-only writes, 2 GiB RAM, 2 CPU, 128 processes, 1 GiB file limit и 600 s timeout.

### Незавершённые проверки и точное ограничение

`TestRoleRevisionMatrixBrowserAcceptance` запущен с отдельной disposable PostgreSQL и строгой browser sandbox, но упал через 29.46 s на ожидании текста `Локальная ревизия: 5 · серверная ревизия: 7`. Текущий React source в `frontend/admin/src/pages/content/model-content-editor.tsx` этот конфликтный блок содержит; `pkg/static/dist` и зафиксированный output в source copy — старые и текста не содержат. Поэтому embedded UI не соответствует source, и этот прогон нельзя считать pass.

Обновить embedded frontend в подходящем runner сейчас не удалось: Linux runner видит только установленные Darwin ARM64 native пакеты в `frontend/node_modules`; Linux build требует platform-specific optional packages, которых нет в проектной `node_modules`. Package manager установку не выполняли и внешнюю сеть не включали. На macOS `sandbox-exec` не предоставляет требуемую память-лимитирующую возможность; target build вне sandbox не запускали. После предоставления/подготовки заранее установленных Linux-compatible build dependencies в ограниченном runner следующий шаг — `pnpm test`, `pnpm build`, затем повторить этот browser test на свежем `pkg/static/dist`.

Ролевой контракт при этом не меняли: текущая спецификация соответствует решению пользователя — все роли публикуют; `user`/`staff` не меняют структуру и branch assignments; submissions меняют `admin`/`moderator`, удаляет только `admin`. Старый Vitasil browser test от 7 ноября, который запрещал `staff`/`user` публикацию, помечен как исторический и не является текущим доказательством.

### Изоляция и disposable resources

Проверки выполнялись из read-only source copy `/Users/docup/Library/Caches/we-engine-acceptance-sandbox-20261108-root01/mount/source-final` на APFS sparse image `/Users/docup/Library/Caches/we-engine-acceptance-sandbox-20261108-root01/we-engine-qa.sparseimage`, смонтированном в `/Users/docup/Library/Caches/we-engine-acceptance-sandbox-20261108-root01/mount` (лимит образа 3 GiB). Resource runner: `we-engine-acceptance-runner:20261108-root01`; browser image: `we-engine-node20-checks-20261108-root01:latest`. В browser контейнере использовались `--network` только для task-owned internal QA сети, `--read-only`, uid 10001, `cap-drop ALL`, только нужный Chromium `SYS_CHROOT`, `no-new-privileges`, `deploy/seccomp-prerender.json`, pids 256, память/своп 4 GiB, 2 CPU, nofile 1024, fsize 1 GiB, отдельные `/tmp` и `/dev/shm`; target source и toolchain были read-only, writable mount — только QA scratch. В Node20 check runner — сеть полностью отключена.

Task-owned resources, оставленные до конца текущей координации для postflight/повторного запуска:

| Purpose | Exact resources |
| --- | --- |
| Synthetic S3/API | `website-engine-independent-product-s3-qa-20261108-root01-pg` (127.0.0.1:35569), DB `website_engine_independent_product_qa_20261108_s3root01`; `website-engine-independent-product-s3-qa-20261108-root01-minio` (35570/35571), final bucket `independent-product-qa-20261108-root01-final01`; failed-attempt buckets `independent-product-qa-20261108-root01-retry01`, `independent-product-qa-20261108-root01-retry02`, `independent-product-qa-20261108-root01-retry03` and original `independent-product-qa-20261108-root01`. |
| Cross-install Sitepack | `website-engine-independent-product-sitepack-qa-pg-20261108-root01` (35572), DBs `website_engine_ind_sitepack_qa_20261108_root01_src`/`_dst`; `website-engine-independent-product-sitepack-qa-minio-20261108-root01` (35573/35574), bucket `independent-product-qa-sitepack-20261108-root01`. |
| Editor browser | `website-engine-independent-product-ui-qa-pg-20261108-root01` (35563), DB `website_engine_independent_product_ui_qa_20261108_root01`. |
| Role API/browser | `website-engine-role-ui-qa-pg-20261108-root01` (35564), DBs `website_engine_role_revision_qa_20261108_root01` and `website_engine_role_revision_ui_qa_20261108_root01`; API database was confirmed zero-session after the pass; UI database after the failed browser run also had zero sessions. |
| Sandbox scratch | `source-final`, `frontend-work`, `frontend-output`, `go-work`, `tools`, and `node20-base-context` below the recorded APFS mount; no product data. |

До удаления перечисленных ресурсов нужно завершить любые повторные browser checks и проверить exact owner labels/mounts, ноль DB sessions и свободные процессы/порты. Неопознанные соседние контейнеры и volumes не удалять.

## Повторная S3/prerender и role/revision browser проверка — 8 ноября 2026

Предыдущие S3+prerender прогоны (`a03`–`a07`) изолированно установили, что media bytes roundtrip проходит, но public `/feature-assets` возвращает 400 до запуска браузера. Причиной был дефект интеграционного helper `seoPublicServerWithWorker`: builds регистрировались в injected `e.objectStore`, а helper создавал public `BuildService` с `e.store` (fakeStore). В `cmd/integration/seo_branch_test.go` helper теперь использует ту же object store, куда тест загрузил ZIP.

После исправления прошли оба прогона на уникальной disposable PostgreSQL и приватном MinIO:

- `go test -v -count=1 -run '^TestIndependentProductSyntheticAcceptanceWithS3$' ./cmd/integration` без `PRERENDER_TEST_*`: PASS. Проверены S3 media roundtrip, runtime composition, прямые GET для каждого module/CSS feature asset с непустыми ответами 200 и binding rejection cases.
- Та же команда с `PRERENDER_TEST_NODE`, `PRERENDER_TEST_MODULE` (Playwright 1.62.1) и Chromium 1252: PASS за 2.29 s. Выполнен S3-backed браузерный prerender; публичная HTML-страница содержит данные Product и возвращает cacheable prerender response.
- `go test -v -count=1 -run '^TestRoleRevisionMatrixBrowserAcceptance$' ./cmd/integration`: PASS за 8.16 s на свежей embedded admin сборке и отдельной disposable БД. Chromium подтвердил role UI для `user`, `staff`, `moderator`, `admin`, восстановление истории только в draft до отдельной публикации, сетевой сбой до commit с retry, commit с потерей ответа, безопасный 409 stale retry, сохранение/скачивание локального ввода и подтверждённую загрузку новой серверной ревизии.
- После исправления: `go test -gcflags=all=-e -run '^$' ./cmd/integration`, `go test -count=1 ./pkg/runtime`, `go vet ./pkg/runtime` и `git diff --check` — PASS.

S3 прогон использовал только `website-engine-independent-product-s3-qa-20261108-root01-pg` (`127.0.0.1:35569`), БД `website_engine_independent_product_qa_s3root08`, и `website-engine-independent-product-s3-qa-20261108-root01-minio` (`127.0.0.1:35570–35571`), bucket `independent-product-qa-s3-prerender-20261108-root08`. До создания bucket его отсутствие проверено точным S3 `HeadBucket`; тест потребовал bucket private. После обеих проверок подтверждено ноль DB sessions, удалена только эта БД и этот bucket с 11 объектами; точный одноразовый helper `/tmp/website-engine-s3-qa-helper-a08.go` удалён. QA containers остались доступны для других записанных проверок.

Role browser прогон использовал только `website-engine-role-ui-qa-pg-20261108-root01` (`127.0.0.1:35564`) и новую БД `website_engine_role_revision_ui_qa_20261108_root02`; перед тестом БД отсутствовала и не имела сессий. После PASS подтверждено ноль sessions и удалена только эта БД. После заключительного `pnpm build` этот тест повторён ещё раз на БД `website_engine_role_revision_ui_qa_20261108_root03`: PASS за 8.36 s; ноль sessions проверен, удалена только эта БД. Role QA PostgreSQL и S3 QA PostgreSQL/MinIO контейнеры остались запущенными для оставшейся координации; на рабочий Vitasil API, БД, storage и volumes записи не выполнялись.

На момент этой записи browser acceptance всех девяти Vitasil placements и ручная screen-reader оценка оставались открытыми; all-nine acceptance прошла в последующем разделе ниже. Общий TODO E2E остаётся открытым из-за оставшихся accessibility критериев.

## UI role/revision recovery acceptance — 8 ноября 2026

`TestRoleRevisionMatrixBrowserAcceptance` дополнен проверкой предупреждений при потере локального ввода. В Chromium подтверждены: отмена in-app ухода оставляет страницу и локальное значение; реальная перезагрузка вызывает `beforeunload`; после её подтверждения session recovery возвращает черновик с конфликтом локальной revision 5 против серверной 7; черновик не отправляется автоматически; серверная revision 7 и published revision 4 остаются неизменными; публичный runtime сохраняет опубликованное значение. Затем отдельное явное подтверждение загружает серверную версию. Это дополняет сценарии role UI, stale retry, competing tab, отказа до commit и потери ответа после commit.

Проверки: `gofmt -w cmd/integration/role_revision_matrix_browser_test.go`, `go test -run '^$' ./cmd/integration` и `git diff --check -- cmd/integration/role_revision_matrix_browser_test.go` — PASS. Chromium: `go test -timeout 10m -count=1 -v ./cmd/integration -run '^TestRoleRevisionMatrixBrowserAcceptance$'` — PASS за 9.68 s. Использовались Node 20 `/Users/docup/.nvm/versions/node/v20.19.4/bin/node`, Playwright `/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs`, Chromium 1252 `/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing` и только БД `website_engine_role_revision_ui_qa_20261108_root04` в контейнере `website-engine-role-ui-qa-pg-20261108-root01` (`127.0.0.1:35564`, task-owned label `codex.qa`). До создания проверены владелец порта, точная метка контейнера, отсутствие БД и ноль сессий по префиксу; `newTestEnv` повторно подтвердил ноль посторонних подключений. Пароль подключения был извлечён из окружения только этого disposable контейнера и подставлен в память процесса, не напечатан и не записан в документацию. После PASS подтверждён ноль активных сессий, удалена только эта БД; QA PostgreSQL контейнер сохранён для текущей проверки. Рабочий Vitasil не использовался.

На момент этой записи browser acceptance всех девяти Vitasil placements ещё не прошла; более поздний результат приведён в следующем разделе. Ручная screen-reader/accessibility оценка и полная responsive/WCAG матрица остаются открытыми. TODO не закрывался.

## All nine current Vitasil placements — 8 ноября 2026

`TestVitasilAllCurrentPlacementsBrowserAcceptance` прошёл на актуальных ZIP, прочитанных только из `/Users/docup/Projects/vitasil/frontend/dist/feature-builds`: `background@2.0.0`, `catalog@2.0.0`, `catalog-detail@2.0.0`, `footer@2.0.0`, `form@2.0.0`, `header@2.0.0`, `hero@2.0.0`, `main-data@2.0.1`, `not-found@2.0.0`. Команда: `go test -timeout 10m -count=1 -run '^TestVitasilAllCurrentPlacementsBrowserAcceptance$' -v ./cmd/integration`; compile-only проверка нового теста также прошла через `go test -run '^$' ./cmd/integration`; `gofmt -d cmd/integration/vitasil_all_placements_browser_test.go` вывел пустой diff; `git diff --check` для изменённых markdown-файлов прошёл. Запуск использовал Go `/usr/local/go/bin/go`, Node 20 `/Users/docup/.nvm/versions/node/v20.19.4/bin/node`, Playwright 1.62.1 из `prerender/node_modules/playwright/index.mjs` и установленный Chromium 1252.

До миграции создана только disposable БД `website_engine_vitasil_all_browser_qa_20261108_root03` в task-owned контейнере `website-engine-role-ui-qa-pg-20261108-root01` (loopback `127.0.0.1:35564`, label `codex.qa`, без mounted volumes); точное отсутствие БД и ноль подключений в семействе `website_engine_vitasil_all_browser_qa_*` проверены заранее. Integration helper повторно проверил ноль посторонних соединений. После теста подтверждён ноль подключений и удалена только эта БД. Один предварительный запуск wrapper-а до теста завершился из-за отсутствия Go в заданном `PATH`; созданная им пустая disposable БД `website_engine_vitasil_all_browser_qa_20261108_root02` была проверена на ноль сессий и удалена до любых миграций.

Chromium проверил главную, каталог, `/catalog/rosehip` и пользовательский 404: девять placements попали в назначенные слоты, карточка каталога ведёт на detail, тексты общей сущности `Product` и остальные sentinel-значения присутствуют; каждый документ имеет один `main`, runtime-feature ошибки и неожиданные сетевые/page errors отсутствуют. В `/admin/site/data-model` и `/admin/content` не найдено `input[type=file]`, выбора/назначения ветки; назначение изолированной тестовой ветки до и после браузерного сценария совпало. Рабочий Vitasil не запускался и не изменялся; его ZIP служили только read-only входом для теста.

## Model-backed SEO для Vitasil Detail — 8 ноября 2026

Добавлен `TestVitasilDetailSEOPublishedProductFieldsAcceptance` в `cmd/integration/vitasil_detail_seo_acceptance_test.go`. Он использует неизменённый архив `catalog-detail@2.0.0` из disposable build-каталога Vitasil и отдельную PostgreSQL `website_engine_model_content_qa_20261108_root01` в task-owned контейнере `website-engine-qa-main-20261108-root01-pg` (`127.0.0.1:35650`). Перед запуском helper подтвердил уникальный disposable DB и отсутствие посторонних сессий; тест проверил это повторно. Image rendition обработан через интеграционный media worker.

Команда `go test -count=1 -v ./cmd/integration -run '^TestVitasilDetailSEOPublishedProductFieldsAcceptance$'` завершилась PASS. Python wrapper передал `TEST_DATABASE_URL` из env только task-owned disposable PostgreSQL процесса; пароль не выводился и не сохранялся. `TEST_FEATURE_DIR=/tmp/vitasil-feature-build-qa-20261108-root01` указывал на отдельные ZIP-сборки текущих Vitasil исходников. Тест подтвердил, что черновая Product-запись не раскрывает SEO поля и изображение, первая публикация ждёт готовой rendition, опубликованный title/description/OG metadata присутствуют в серверном HTML, черновая правка сохраняет прежний ETag и public content, а явная публикация обновляет HTML и ETag. Перевод тестовой ветки в indexable policy выполнен через `RegisterBranches`; до и после сохранены те же branch IDs и snapshot assignments.

Первый запуск показал две ошибки в новом acceptance fixture: policy индексации была изменена только в HTTP-зависимости, но оставалась `false` в DB; кроме того, обновление контента теста адресовало display name вместо сгенерированного model field key. Fixture исправлена штатной `RegisterBranches` и stable `fieldKeys`; production-код при этом не менялся. После тестов в контейнере осталась только служебная `postgres` session. После проверки owner labels и единственных volume mounts удалены только контейнеры `website-engine-qa-main-20261108-root01-pg`/`website-engine-qa-main-20261108-root01-minio` с точными anonymous volumes, их disposable DBs и bucket `files-media-qa-20261108-root01`, а также `/tmp/vitasil-feature-build-qa-20261108-root01`. Порты 35650–35652 и оба volume ID освобождены; Vitasil stack/data не затрагивались.

Также прошли `env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_FEATURE_ZIP -u TEST_S3_ENDPOINT -u TEST_S3_BUCKET go test -count=1 ./...` и соответствующий `go vet ./...` без DB env. Они подтверждают unit/compile coverage, но не заменяют namespace-specific integration runs. После focused test в disposable PostgreSQL не осталось посторонних сессий. Рабочая установка Vitasil, её API/БД/S3/volumes не использовались. Результат подтверждает model-backed SEO для этого format-2 detail feature; production gateway/CDN, Search Console, field CWV и ручная screen-reader проверка остаются внешними/ручными критериями.

## Финальная проверочная волна текущей реализации — 8 ноября 2026

На актуальных Vitasil ZIP прошёл opt-in `TestVitasilPageIndexLargeCatalogAcceptance`: 50 000 опубликованных записей `Product`, 50 001 полный отсортированный URL (`/catalog` и все detail routes). Cold `PageIndex` занял 24,935 мс, warm cached вызов — 1 мс. ZIP: `catalog@2.0.1`, `catalog-detail@2.0.0`. Результат является локальным базовым замером, не production SLA и не замером sitemap XML delivery.

Дополнительные Chromium проверки прошли: `TestRoleRevisionMatrixBrowserAcceptance` на `website_engine_role_revision_ui_qa_20261108_r2`; `TestVitasilCurrentHeaderAndHeroResponsiveRuntime` на `website_engine_vitasil_archive_qa_20261108_r2`; `TestAdminShellTouchTargetsBrowserAcceptance` на собранном `pkg/static/dist`. Responsive acceptance проверила текущую фиксированную поверх hero шапку, мобильные и desktop размеры, видимость заголовка и CTA, отсутствие горизонтального overflow, landmark/labels, клавиатурный фокус и доступность hero без JS. Первоначальный критерий теста ошибочно требовал `sticky` и подменял короткий фактический брендовый заголовок длинным текстом; критерий исправлен, Vitasil CSS/ABI не менялись. Первая попытка touch-target теста указывала на несуществующий `frontend/dist`; повторный запуск с фактической Vite-сборкой `pkg/static/dist` прошёл.

Финальные проверки репозитория после этих изменений:

```sh
env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_FEATURE_ZIP -u TEST_S3_ENDPOINT -u TEST_S3_BUCKET go test -count=1 ./...
env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_FEATURE_ZIP -u TEST_S3_ENDPOINT -u TEST_S3_BUCKET go vet ./...
cd frontend && PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm typecheck
cd frontend && PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm lint
cd frontend && PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm test
cd frontend && PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' pnpm build
```

Все перечисленные команды прошли; frontend: 27 файлов, 140 тестов. Полный `cmd/integration` suite на disposable PostgreSQL ранее прошёл; namespace-specific S3, Sitepack, browser и крупный каталог были отдельно выполнены на изолированных БД/MinIO. Рабочие API, БД, storage и volumes Vitasil не использовались.

Новый крупный каталог использовал только `website_engine_model_content_qa_20261108_r2`, контейнер `website-engine-plan-fullqa-pg-20261108-r2` (`127.0.0.1:35520`) и текущие ZIP из `/Users/docup/Projects/vitasil/frontend/dist/feature-builds`. Role/revision и responsive browser acceptance также использовали только disposable DB в том же task-owned PostgreSQL. Touch-target acceptance не использовал БД. После проверок запросом к `pg_stat_activity` подтверждён ноль подключений; удалены только 14 перечисленных выше task-owned DB, контейнеры `website-engine-plan-fullqa-pg-20261108-r2` и `website-engine-plan-fullqa-minio-20261108-r2`, disposable MinIO buckets вместе с одноразовым MinIO контейнером и единственный анонимный volume `c17915791ac15a1eb83aacc4866b3b2406f64b473e73763ed20dbcdee90bbb99`, смонтированный только в указанную PostgreSQL. Проверены отсутствие контейнеров/volume и освобождение портов `35520`/`35521`; активных integration процессов не было. Неизвестные/активные Vitasil viewer instances и рабочие сервисы сохранены.

Остаются неподтверждёнными внешние/ручные критерии: Search Console/URL Inspection, production gateway/auth/CDN и production S3, полевые Core Web Vitals, полный ручной screen-reader/WCAG review. Они требуют production домена/учётной записи или ручной проверки и поэтому не отмечены в TODO выполненными. PageIndex для 50k измерен, но 24,935 мс cold build требует дальнейшего профилирования и сравнения с согласованным целевым порогом перед закрытием performance acceptance.

Postflight cleanup по предыдущим зафиксированным disposable прогонам: после проверки, что следующие восемь контейнеров остановлены и не имеют подключённых задач, удалены только `website-engine-role-ui-qa-pg-20261108-root01`, `website-engine-independent-product-sitepack-qa-minio-20261108-root01`, `website-engine-independent-product-sitepack-qa-pg-20261108-root01`, `website-engine-independent-product-ui-qa-pg-20261108-root01`, `website-engine-model-content-files-qa-20261108-01`, `website-engine-model-content-test-20261108-01`, `website-engine-model-content-qa-radical-20261107-r1` и `website-engine-model-content-qa-20261107-root01`. Единственный volume `e457d6cf7d31509af5a97c781638c9b66d1cd3f0c8b9b37991e48863f5268f2c` был смонтирован только в последний остановленный test PostgreSQL; после удаления контейнера подтверждено отсутствие ссылки и удалён этот volume. Текущие `website-engine-vitasil-viewer-*` контейнеры имеют явную метку `codex.qa.project=website-engine-vitasil-viewer-20261108`, но оставлены работающими и здоровыми, поскольку это отдельный доступный viewer и его пользовательская необходимость не была однозначно исключена. Рабочие `website-engine-local-20261108-server-1` и `vitasil-admin-latest-20261107-vitasil-1` наблюдались в состоянии restart loop; их конфигурацию, данные и volumes не трогали. Контейнеры `website-engine-latest-local-20261107-pg`/`minio` также сохранены.

## Семантическая структура placements и Vitasil — 9 ноября 2026

Уточнён и реализован DOM-контракт: независимые placements — прямые sibling roots в одном host своего slot; ABI сохраняет ровно один lifecycle mount-root на placement. На каждой разрешённой странице есть ровно один page-level `<main>`, даже если в композиции нет placement со slot `main`. Статический fallback воспроизводит тот же порядок слотов и добавляет пустой main-host для страниц, содержащих только chrome placements. Подробности в [SPEC-feature-runtime.md](SPEC-feature-runtime.md).

Проверки Engine прошли: `frontend` Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm test -- --run` (27 файлов/140 тестов), `pnpm build`; `env -u TEST_DATABASE_URL -u TEST_FEATURE_DIR -u TEST_FEATURE_ZIP -u TEST_S3_ENDPOINT -u TEST_S3_BUCKET go test -count=1 ./...`; `go vet ./...`; `git diff --check`; `go test -count=1 ./pkg/runtime ./cmd/integration`. Текущий `pkg/runtime/dist` собран вместе с админским frontend.

Витасил собран read-only из исходников Node `v20.19.4`: `npm run typecheck`, `npm run lint`, `node --test scripts/feature-manifest-contract.test.mjs` (7/7), `FEATURE_BUILD_OUTPUT_DIR=/tmp/website-engine-dom-sibling-vitasil-qa-20261109-r1 npm run build`. `unzip -t` прошёл для десяти ZIP: `background@2.0.1`, `catalog@2.0.13`, `catalog-detail@2.0.3`, `footer@2.0.10`, `form@2.0.6`, `header@2.0.6`, `hero@2.0.5`, `legal-documents@1.0.7`, `main-data@2.0.3`, `not-found@2.0.2`.

Три изолированных Chromium acceptance прошли:

- `TestVitasilAllCurrentPlacementsBrowserAcceptance` — `/`, `/catalog`, `/catalog/rosehip`, not-found; актуальные ZIP зарегистрированы, все девять core placements отрисованы; кроме siblings/hosts и одного `<main>`, проверены семантические структуры hero/main-data/form/footer/catalog/detail/not-found, detail URL, отсутствие ошибок, overflow на 360/390/768/1280 px и отсутствие upload/branch controls в `/site` и `/content`.
- `TestVitasilLegalDocumentsBrowserAcceptance` — legal singleton, `/`, `/privacy`, `/personal-data-consent`, `/cookies`, один main даже на footer-only странице, ссылки документов, клавиатурное сохранение выбора cookie, только ожидаемый localStorage key, ноль browser cookie и network side effects.
- `TestVitasilSiteArgumentsV2BrowserAcceptance` — актуальные `catalog@2.0.13` и `background@2.0.1`, вложенный `action`, массивы, typed field mapping, выбор существующего media, запись независимого child snapshot и `/content` без uploads/branch assignment.
- `TestSubmissionsBrowserAcceptanceWithVitasilForm` — текущий `form@2.0.6`, 43 строки списка и роли admin/moderator/user.

Общие текущие сбои были найдены и устранены в тестах и продукте: старая all-placement фикстура передавала несуществующий `catalog.breadcrumb`; legal-documents фикстура передавала удалённые `consentVersion`/`optionalFunctionalCategory`; site-arguments E2E также ожидал удалённый `breadcrumb`. Фикстуры теперь соответствуют manifest v2. В браузере catalog product cards также выявили отсутствие штатного `detailLink` в текущем Vitasil view; title-link восстановлен через существующий runtime contract и `catalog` поднят до `2.0.13`. Legal-only route выявил отсутствие main landmark; shared main-host теперь создаётся даже без main placements, а Go и browser checks покрывают этот случай.

Для всех acceptance использовались только созданные для этого прогона PostgreSQL ресурсы: контейнер `website-engine-dom-sibling-pg-20261109-r1` на `127.0.0.1:35891` и БД `website_engine_vitasil_all_browser_qa_20261109_r1`, `website_engine_legal_docs_qa_20261109_r1`, `website_engine_submissions_ui_qa_20261109_r1`, `website_engine_vitasil_site_arguments_browser_qa_20261109_r1`; ZIP были в `/tmp/website-engine-dom-sibling-vitasil-qa-20261109-r1`. Рабочие Vitasil API, БД, S3 и volumes не использовались. Очистка этих точных ресурсов выполнена после проверки нулевых сессий; использованные выходные файлы удалены.

Ручной screen-reader/WCAG аудит и production gateway/CDN/Search Console остаются отдельными открытыми критериями и этим UI/runtime refactor не закрываются.

### Lighthouse и текущий viewer — 9 ноября 2026

Lighthouse 12.8.2, мобильный профиль, был запущен read-only против существующего viewer `http://127.0.0.1:28082` по `/` и `/catalog/biogard`. Результаты главной: Performance 42, Accessibility 96, Best Practices 96, SEO 100; FCP 1.7 s, LCP 16.9 s, CLS 1.063, TTI 16.9 s, transferred payload 2,797 KiB. Detail: Performance 56, Accessibility 100, Best Practices 96, SEO 100; FCP 1.7 s, LCP 12.9 s, CLS 0.386, TTI 12.9 s, payload 2,155 KiB. Аудит также обнаружил отсутствие текстовой компрессии (оценённая экономия 1,734 KiB/1,373 KiB), большой объём неиспользованного JS (1,116 KiB/951 KiB) и запрос отсутствующего `/favicon.ico` на главной.

Это диагностический замер работающего контейнера `website-engine-vitasil-viewer-app-r2-20261108`; конфигурация была прочитана без изменения: `PRERENDER_ENABLED=false`, хотя пути Node/Playwright/Chromium заданы. HTTP передавал только shell HTML (943 bytes), без содержимого feature placements. Поэтому SEO score Lighthouse отражает браузерное выполнение JS и не доказывает indexable HTML без JS; текущий viewer не является проверкой нового изолированного build. Ни API, ни БД, ни storage, ни volumes не изменялись. Lighthouse JSON и временные HTML использованы для выписки этих результатов в `/tmp`, затем удалены.

Изолированный Lighthouse на свежей сборке Engine и текущих ZIP Vitasil ранее дал mobile Performance 44 / Accessibility 100 / Best Practices 100 / SEO 100 и desktop 62 / 100 / 100 / 100. Эти прогоны шли через тестовый HTTP harness без production prerender worker и gateway compression; CLS был 1.0 из-за позднего появления form placement. Это фиксирует baseline, не подтверждает целевой production CWV. Встроенный Lighthouse hook и браузерные acceptance прошли, но полноценный Lighthouse прогон через Docker-образ с `PRERENDER_ENABLED=true` и production-подобным gzip gateway остаётся следующим изолированным шагом.

Команды Lighthouse для read-only viewer:

```sh
/Users/docup/.npm/_npx/0f94ee7615faf582/node_modules/.bin/lighthouse http://127.0.0.1:28082/ --chrome-path='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' --chrome-flags='--headless --no-sandbox' --output=json --only-categories=performance,accessibility,best-practices,seo --quiet
/Users/docup/.npm/_npx/0f94ee7615faf582/node_modules/.bin/lighthouse http://127.0.0.1:28082/catalog/biogard --chrome-path='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' --chrome-flags='--headless --no-sandbox' --output=json --only-categories=performance,accessibility,best-practices,seo --quiet
```

### Изолированная Lighthouse-матрица после runtime CSS/gzip оптимизации — 9 ноября 2026

Исторический прогон проверял порог Performance не ниже 96 на mobile и desktop для `/`, `/catalog`, `/catalog/rosehip`. Пользователь позднее повысил целевой результат до медианы 100/100 для каждой страницы и профиля. После CSS inlining integration hook отдельно проверяет cold miss и warm hit внутреннего prerender HTML cache; browser cache Lighthouse пустой при каждом запуске. Выполнено пять повторов всех 12 route/profile/cache-state комбинаций (60 Lighthouse запусков). Среда: Lighthouse 12.8.2, Chromium 1252, Node 20.19.4; ZIP `background@2.0.2`, `catalog@2.0.18`, `catalog-detail@2.0.3`, `footer@2.0.11`, `form@2.0.6`, `header@2.0.8`, `hero@2.0.7`, `main-data@2.0.8`, `not-found@2.0.2`; public branch indexable, test feature runtime ABI v1. Ветка не обращалась к рабочему Vitasil. Записи и текстовые медиа для теста синтетические; это проверка актуальных сборок и движка, не замер реальных production assets/CDN.

| Маршрут | Профиль | Engine HTML cache | Performance min / median / max | Median FCP / LCP | Median TBT / CLS |
|---|---|---|---:|---:|---:|
| `/` | mobile | cold | 99 / 100 / 100 | 1.36 / 1.66 s | 0 ms / 0 |
| `/` | mobile | warm | 100 / 100 / 100 | 1.36 / 1.66 s | 0 ms / 0 |
| `/` | desktop | cold | 100 / 100 / 100 | 0.37 / 0.41 s | 0 ms / 0 |
| `/` | desktop | warm | 100 / 100 / 100 | 0.37 / 0.42 s | 0 ms / 0 |
| `/catalog` | mobile | cold | 99 / 100 / 100 | 1.35 / 1.58 s | 0 ms / 0 |
| `/catalog` | mobile | warm | 100 / 100 / 100 | 1.35 / 1.58 s | 0 ms / 0 |
| `/catalog` | desktop | cold | 100 / 100 / 100 | 0.36 / 0.36 s | 0 ms / 0 |
| `/catalog` | desktop | warm | 100 / 100 / 100 | 0.36 / 0.36 s | 0 ms / 0 |
| `/catalog/rosehip` | mobile | cold | 100 / 100 / 100 | 1.20 / 1.20 s | 0 ms / 0 |
| `/catalog/rosehip` | mobile | warm | 100 / 100 / 100 | 1.20 / 1.20 s | 0 ms / 0 |
| `/catalog/rosehip` | desktop | cold | 100 / 100 / 100 | 0.32 / 0.37 s | 0 ms / 0 |
| `/catalog/rosehip` | desktop | warm | 100 / 100 / 100 | 0.32 / 0.37 s | 0 ms / 0 |

До оптимизации gzip уже работал, но prerendered HTML сохранял внешние feature stylesheet links. Сравнимый single-run mobile baseline был: главная Performance 75 / FCP 3.2 s / LCP 5.0 s; каталог 100 / 1.4 s / 1.7 s; detail 98 / 1.6 s / 2.1 s. После inlining home mobile получил 100 в пяти повторах warm и 99–100 в cold; оптимизация устранила слабый маршрут при нулевых TBT/CLS. Accessibility на HTML-cache прогоне: home 96, catalog 95, detail 100; Best Practices и SEO — 100 во всех прогонах. Доступность оценивалась отдельно от заданного Performance порога.

Изменение: `scripts/prerender-worker.mjs` встраивает в сериализованный документ только успешно загруженные same-origin stylesheet без `url(...)`, `@import`/небезопасного `</style` и при общем лимите 96 KiB. CSS со внешними зависимостями и превышением лимита остаётся `<link>`. `RuntimeApp.ensureStyles` считает загруженные inline style готовыми; feature ABI и DOM placement contract не менялись. `TestPrerenderWorkerHandlesFallbackStylesheetReplacement` на Chromium проверил inline для безопасного CSS и сохранение ссылки с внешним font URL. Lighthouse reports для пяти повторов сохранялись в disposable `/tmp/website-engine-performance-qa-20261109-r5` до агрегации.

`TestVitasilAllCurrentPlacementsBrowserAcceptance` прошёл на всех девяти актуальных ZIP: одна `<main>`, 360/390/768/1280 px без overflow, без JS errors; дополнительно gzip на реальных endpoints подтверждён для runtime composition JSON, feature JavaScript/CSS и HTML. Маленькая test sitemap (меньше 512 B) осталась несжатой с `Vary: Accept-Encoding`; middleware tests покрывают gzip decoding, ETag weak comparison, HEAD/304, Range, q-values и binary passthrough. Команды: `pnpm typecheck`, `pnpm build:runtime`, `pnpm lint`, `go test ./presentation/http/middleware ./pkg/runtime -count=1`, `go vet ./presentation/http/middleware ./pkg/runtime ./cmd/integration`, Chromium stylesheet test и 60-run disposable Lighthouse integration acceptance прошли. Полный Docker Compose production-equivalent запуск, production gateway/CDN, реальные пользовательские данные, INP/field CWV и Search Console не входят в эти локальные лабораторные метрики.

Повторяемая команда интеграционного прогона (создать новую пустую DB с указанным тестовым префиксом и каталог результатов перед запуском):

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:35942/website_engine_vitasil_all_browser_qa_perf_20261109_r5?sslmode=disable' \
TEST_FEATURE_DIR='/Users/docup/Projects/vitasil/frontend/dist/feature-builds' \
PRERENDER_TEST_NODE='/Users/docup/.nvm/versions/node/v20.19.4/bin/node' \
PRERENDER_TEST_MODULE='/Users/docup/Projects/website engine/prerender/node_modules/playwright/index.mjs' \
PRERENDER_TEST_BROWSER='/Users/docup/Library/Caches/ms-playwright/chromium-1252/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' \
LIGHTHOUSE_BIN='/Users/docup/.npm/_npx/0f94ee7615faf582/node_modules/.bin/lighthouse' \
LIGHTHOUSE_OUTPUT_PATH='/tmp/website-engine-performance-qa-20261109-r5/vitasil.json' \
LIGHTHOUSE_REPETITIONS=5 \
go test -timeout 30m -count=1 -run '^TestVitasilAllCurrentPlacementsBrowserAcceptance$' -v ./cmd/integration
```

Disposable ресурсы для 60-run acceptance: task-owned PostgreSQL container `website-engine-performance-qa-pg-20261109-r1` на `127.0.0.1:35942`, база `website_engine_vitasil_all_browser_qa_perf_20261109_r5`; ранее использовались и затем удалены одноразовые `..._r2`, `..._r3`, `..._r4`, `..._r6`, `..._r7` и промежуточная пустая `website_engine_perf_qa_r2_20261109`. Vitasil production/viewer resources не использовались. Postflight cleanup: перед удалением базы проверяется `pg_stat_activity`; затем удаляются только указанные QA DB, контейнер и временные отчёты/каталоги. Текущий Vitasil оставлен без API/DB/S3/volume записей.

### Контролируемый A/B prerender и повтор текущего viewer — 9 ноября 2026

Пользователь указал URL `http://127.0.0.1:28082/` и Lighthouse Performance 84. В установленном Lighthouse 12.8.2 этот score не воспроизвёлся: read-only повтор дал home Performance 68 mobile / 59 desktop. Accessibility 96, Best Practices 96, SEO 100. Это viewer `website-engine-vitasil-viewer-app-r2-20261108`; проверено без изменения его API/DB/S3/volumes: image старый, `PRERENDER_ENABLED=false`. HTML был shell-only; hero LCP image появляется после client runtime и не находится в initial HTML, несмотря на `fetchpriority=high`. Lighthouse оценил текстовую компрессию примерно в 1,734 KiB и unused JS около 1,116 KiB. Это объясняет направления диагностики, но не устанавливает причину расхождения 84 vs 68/59: профиль или состояние viewer могли отличаться.

Для controlled A/B использованы текущая сборка Engine и девять актуальных ZIP Vitasil, уникальные disposable базы `website_engine_vitasil_all_browser_qa_prerender_off_20261109_r1` и `website_engine_vitasil_all_browser_qa_prerender_on_20261109_r1`. Хранилище — синтетическое/in-memory; крупного реального hero asset не было. Единственная переменная — `LIGHTHOUSE_PRERENDER=0/1`. Lighthouse 12.8.2 запускался трижды на mobile и desktop; для worker выполнены cold/warm Engine HTML-cache варианты. Итоги: без worker mobile Performance 82/58/57 (median 58), LCP 4.882 s, CLS 1; desktop 76/75/74 (median 75), LCP 0.945 s, CLS 1. С worker mobile cold/warm scores 99–100 (median 100), LCP 1.508 s, CLS 0; desktop 99–100 (median 100), LCP около 0.42 s, CLS 0. Off-повторы заметно нестабильны, поэтому вывод не делается по одному запуску. Изолированная страница показывает, что серверный HTML и стабильная геометрия дают крупное улучшение; это не гарантирует production score и само по себе не повышает рейтинг Google/Яндекса.

Дополнительно browser acceptance прошёл на девяти актуальных feature ZIP: routes/layout, responsive viewport, одна `<main>`, без overflow/JS errors; тестовые базы не были рабочей Vitasil БД. Тестовая среда: PostgreSQL container `website-engine-prerender-ab-qa-pg-20261109-r1`, port `35990`, tmpfs data dir; Lighthouse outputs: `/tmp/website-engine-prerender-ab-20261109-off` и `/tmp/website-engine-prerender-ab-20261109-on`. Postflight cleanup завершён: перед удалением подтверждены нулевые подключения к обеим базам; удалены только эти две disposable БД, принадлежащий тесту контейнер и четыре перечисленных временных Lighthouse output. Контейнер и listener на порту 35990 после удаления отсутствуют. Полная команда: `go test -timeout 30m -count=1 -run '^TestVitasilAllCurrentPlacementsBrowserAcceptance$' -v ./cmd/integration` с `LIGHTHOUSE_PRERENDER=0|1`, `LIGHTHOUSE_REPETITIONS=3`, disposable `TEST_DATABASE_URL`, текущим `TEST_FEATURE_DIR` и Lighthouse/Chromium/Node путями из секции A/B. Результаты и ограничения: [SPEC-seo.md](SPEC-seo.md).

### Transitional frontend workspace split (superseded) — 9 November 2026

Это был переходный эксперимент package split. Публичный Vite base с `frontend/base/src/runtime/RuntimeApp.ts` и его `index.html` позднее удалены; его сборка и browser routing больше не являются целевым состоянием. Текущая архитектура описана в [FRONTEND_ARCHITECTURE.md](FRONTEND_ARCHITECTURE.md): Go `html/template` формирует каждый публичный документ, а `pkg/runtime/scripts` содержит только browser feature ABI lifecycle.

Исторические проверки этого split подтверждают только тот временный пакетный layout и не доказывают текущий Go renderer. До замены base на Go Docker build создавал `admin.html` и public `index.html`; это больше не описывает текущий Docker build.

### Responsive image delivery, rendition processing и Sitepack — 9 ноября 2026

Vitasil feature source теперь использует общий `ResponsivePicture`: WebP `srcset` и `<source>` для адаптивной загрузки, исходный файл как `<img>` fallback, `sizes`, async decoding, loading/fetch priority и intrinsic размеры по крупнейшей готовой rendition. Hero использует media-specific mobile/desktop источники для art direction. В трёх потребителях общей сущности Product профили сведены к одному набору `small` 480×360, `medium` 800×600, `large` 1200×900. Обновлены и пересобраны ZIP: `background@2.0.3`, `catalog@2.0.19`, `catalog-detail@2.0.4`, `hero@2.0.8`, `main-data@2.0.10`; остальные актуальные сборки также были собраны. Node 20 `pnpm typecheck`, `pnpm lint`, `pnpm build` прошли до Engine integration прогона.

В Engine фактические output dimensions сортируются и дедуплицируются перед созданием `srcset`; декодер отклоняет изображение больше 40 млн пикселей до кодирования. Sitepack импорт восстанавливает rendition при новом файле и при совпадении оригинала с уже имеющимся target; готовые target outputs не затираются, отсутствующие достраиваются, processing row пропускается. Cache policy при импорте следует visibility: public immutable, private `no-store`.

`TestVitasilAllCurrentPlacementsBrowserAcceptance` прошёл на актуальных девяти ZIP. Disposable Product image 1600×1200 создала кандидаты `small/medium/large`; Chromium проверил `<source type="image/webp">`, три width descriptors, intrinsic 1200×900, mobile выбор `small` при 360 px и desktop `medium` при 1280 px. Все маршруты, placements и прежняя responsive/layout проверка также прошли. `TestSitepackReusedFileRestoresPackagedRenditions` прошёл: после удаления rendition state/object у совпадающего оригинала экспортированный sitepack восстановил готовый вариант, сохранил оригинал и применил public immutable cache policy.

Проверки Engine: `go test -count=1 ./features/media ./features/sitebuilder`; `go vet ./features/media ./features/sitebuilder ./cmd/integration`; `go test -count=1 -run '^TestSitepackReusedFileRestoresPackagedRenditions$' ./cmd/integration`; `go test -timeout 15m -count=1 -run '^TestVitasilAllCurrentPlacementsBrowserAcceptance$' -v ./cmd/integration`; `gofmt` и `git diff --check` прошли. `features/media` processor tests включают реальное FFmpeg WebP уменьшение без upscale и отказ источника с превышенным pixel limit. Browser/service integration для rendition metadata и sitepack использует disposable PostgreSQL и in-memory storage/processor; production S3/CDN fetch/cache не проверялся в этой итерации.

QA PostgreSQL: container `website-engine-media-sitepack-qa-pg-20261109-r1`, loopback port `36071`; базы `website_engine_media_sitepack_qa_20261109_r1` и `website_engine_vitasil_all_browser_qa_media_20261109_r1`. Перед удалением проверено отсутствие активных подключений; удалены только эти task-owned базы и контейнер с tmpfs PGDATA. После удаления контейнер и listener на 36071 отсутствуют. S3/MinIO не создавался. Рабочие Vitasil API, database, S3 и volumes не использовались.

### Public Go document / DOM refactor baseline — 9 ноября 2026

До внедрения Go-rendered placement roots и composition bootstrap повторно снят baseline на исходниках и ZIP Vitasil. Успешно прошёл `TestVitasilAllCurrentPlacementsBrowserAcceptance` (381.75 s): девять текущих placements/features, home/catalog/detail and route error paths, media candidates, form and admin-state assertions; worker remained enabled and verified actual cache behavior. Every cold Lighthouse run logged 0 Engine HTML-cache hits; all 30 warm runs logged one hit each. Lighthouse 12.8.2 results (five warm runs per route/device, plus one separate cold run):

| Route | Device | Warm Performance scores | Median | Median LCP / CLS / TBT | Cold Performance / LCP |
|---|---|---:|---:|---:|---:|
| Home | Mobile | 100, 99, 100, 100, 100 | 100 | 1582.7 ms / 0 / 0 ms | 99 / 1638.1 ms |
| Home | Desktop | 100, 100, 100, 100, 100 | 100 | 453.6 ms / 0 / 0 ms | 99 / 452.6 ms |
| Catalog | Mobile | 99, 99, 99, 99, 99 | **99** | 1832.2 ms / 0.0270 / 0 ms | 99 / 1833.3 ms |
| Catalog | Desktop | 100, 100, 100, 100, 100 | 100 | 513.6 ms / 0.0149 / 0 ms | 100 / 512.7 ms |
| Detail | Mobile | 100, 100, 100, 100, 100 | 100 | 1682.9 ms / 0 / 0 ms | 99 / 1683.5 ms |
| Detail | Desktop | 100, 100, 100, 100, 100 | 100 | 474.2 ms / 0 / 0 ms | 100 / 473.3 ms |

The mobile catalog median is below the target because Lighthouse scores FCP/LCP at 0.98 (FCP 1.4 s, LCP 1.8 s); CLS and TBT are not the cause. The six-way 100 target is therefore still open. These are laboratory measurements on this local disposable environment, not field Core Web Vitals or a search ranking guarantee.

Run: `go test -timeout 45m -count=1 -run '^TestVitasilAllCurrentPlacementsBrowserAcceptance$' -v ./cmd/integration` with `LIGHTHOUSE_REPETITIONS=5`, `LIGHTHOUSE_COLD_REPETITIONS=1`, `LIGHTHOUSE_PRERENDER=1`, temporary `TEST_FEATURE_DIR=/tmp/website-engine-ssr-vitasil-baseline-20261109-r1`, and separate disposable PostgreSQL `website_engine_vitasil_all_browser_qa_ssrbaseline_20261109_r1` on loopback `127.0.0.1:36130` in task-owned container `website-engine-ssr-baseline-pg-20261109-r1` (tmpfs data). Reports, HTML, 36 traces and 36 DevTools logs are retained in `/tmp/website-engine-ssr-baseline-20261109-r2`; Lighthouse JSON includes final and filmstrip screenshots. S3/MinIO was not used. The database/container and reports remain allocated for the pending implementation/verification cycle; they must be checked for active sessions and removed only after final QA and artifact handoff.

### Implementation follow-up — template files and `/site` configurability

Inline Go template source in `pkg/runtime/metadata.go` and `pkg/runtime/bootstrap.go` was moved into embedded `pkg/runtime/templates/*.tmpl`. Go now builds typed template data and executes named templates; static public-build shell fragments are the only trusted HTML values. Also removed the hard-coded Zubrium `theme-color` from the reusable public shell so it cannot silently brand every configured site with one fixed color. No checks have run for these edits yet.

The `/site` editor now hides unsupported query bindings for nested objects and arrays of objects, explains stale route/query/media bindings, distinguishes build-registry loading from an empty registry, and provides a recovery path when a pinned build is missing. A targeted frontend test file was added but has not been run. Four Vitasil feature files without JSX were renamed from `.tsx` to `.ts`; React/Vue shared authoring helpers, UI exports, and Feature Lab plugins remain because they are still supported authoring/preview APIs. Required checks remain deferred: Go tests/vet, frontend typecheck/lint/tests/build, Vitasil typecheck/lint/build/ZIP inspection, and disposable browser acceptance for argument and runtime paths.

#### Follow-up: restore Vitasil authoring sources and remove CSS render-blocking request — 9 November 2026

The bulk native-DOM rewrite of Vitasil features was rolled back. Nine tracked feature component/view trees were restored from Git and given the existing `static-module-v1` mount wrapper; format-2 manifests and `dataInputs` were retained at the exact versions in the previous pre-rewrite ZIPs. Existing ZIP archives in `frontend/dist/feature-builds` were preserved as the behavioral checkpoint. The original uncommitted source used to create those archives was not available in Git, so the restored React component source is a Git baseline adaptation, not a byte-for-byte source recovery. `legal-documents` was untracked before the rewrite and its original Vue source is unavailable; the current implementation and the previous `legal-documents@1.0.7` ZIP were retained. A temporary copy of the superseded DOM rewrite is at `/tmp/vitasil-before-restore-20261109-r1` for recovery if needed. Working Vitasil API, DB, S3 and volumes were not accessed.

The public renderer now emits its system error page from `pkg/runtime/templates/public-system.tmpl`. It also inlines the small stylesheet from the embedded public build through a named `.tmpl` only when the asset is under `/assets/`, at most 8 KiB, and contains no closing style tag; this targets Lighthouse's previously reported blocking request for the 1.5 KiB base CSS. The Lighthouse integration gate now checks median Performance `1.00` separately for each route, viewport and cache state. Removed the unused CSS rule for the prior `<noscript>` tree. These Engine changes are not yet verified.

The preceding implementation QA wave found and fixed these failures, but no rerun has established a pass yet: Engine frontend typecheck reported unused `value` in `FeatureArgumentsEditor.test.tsx`; lint also reported an unused `EMPTY_STYLE_URLS` and a syntax error in `legacy-react.tsx`; Vitasil typecheck/lint reported anchor element typing and unused imports; the Vitasil feature build rejected `catalog-detail`'s stale source entry. Exact remaining final commands include Engine `pnpm typecheck`, `pnpm lint`, `pnpm test`, both frontend builds, Go tests/vet, Vitasil typecheck/lint/manifest tests/build/ZIP checks, and disposable Chromium plus Lighthouse acceptance.

#### Vitasil product correction: no product detail page — 9 November 2026

Per the latest product decision, Vitasil no longer has a `catalog-detail` source feature or `/catalog/:slug` route. The catalog does not render breadcrumbs or link product cards to detail pages. `catalog@2.0.26` drops its unused slug input; the other feature versions remain recorded in the Vitasil [feature-data-input specification](../../vitasil/docs/SPEC-feature-data-inputs.md). Vitasil source typecheck, lint, manifest contract tests (7/7), isolated nine-ZIP build/integrity check, and read-only Feature Lab browser preview passed. Engine integration fixtures were updated to use the current no-detail feature set and `/form` as the third public route; `go test -run '^$' ./cmd/integration` passed. As requested, isolated Engine/browser acceptance and Lighthouse were not run for this correction. The running Vitasil snapshot was not changed, so it can continue to serve the old detail route until its page/placement and route are removed in `/site`, then the updated snapshot is assigned to the intended branch.

#### Current public QA branch and hero responsive rendition — 9 November 2026

The task-owned Vitasil QA branch `website` remains available at `http://127.0.0.1:28122/` with admin at `http://127.0.0.1:28120/admin/`. Its disposable DB/storage are not the working Vitasil resources. Snapshot sequence 6 (`a3cf5b7c-8575-4d31-8aa6-fde4c738fe14`) is assigned to that branch and uses the current nine-feature set with `hero@2.0.18`; sequence 5 is retained as its protected parent. The preview branch remains unassigned and returns the configured 503 system page. Current HTTP checks returned 200 for `/`, `/catalog`, and `/form`, and 404 for the removed `/catalog/probif` route.

The current source keeps asynchronous image decoding and adjusts only the hero mobile rendition from 960×1200 to 768×960. A nine-feature build was emitted into `/tmp/vitasil-hero-2018-qa-20261109`; `hero-2.0.18.zip` passed `unzip -t` and was registered only in the disposable QA Engine. Its 768w WebP is 36,222 bytes versus 51,982 bytes for the prior 960w rendition (about 30% smaller); visual comparison showed no apparent composition change at the 412px mobile viewport. The attempted `decoding="sync"` experiment was rejected: build `hero@2.0.17` produced poor mobile Lighthouse observations and was superseded by `hero@2.0.18` with `decoding="async"` restored.

Focused Lighthouse reruns are currently inconclusive and do not close the 100-point gate. The earlier five-run `hero@2.0.16`/768w report in `lighthouse-hero768-mobile` scored 99 in every run (median 99). Re-running through an explicit CLI invocation changed host UA/mode and gave a different result; even after forcing `HeadlessChrome/157`, one control run on the same earlier snapshot scored 90 (LCP 3.51s). With the new `hero@2.0.18` snapshot, five explicit-CLI runs scored 77, 78, 86, 78, 78; the control on `hero@2.0.16` scored 77, 93, 78 using that same invocation. Network evidence showed the hero candidate discoverable, high priority, eager, and HTTP 200; TBT and CLS were zero. Because equivalent route/snapshot runs are not reproducing the previous measurements consistently, these runs are diagnostic only, not a valid replacement matrix or proof of regression/improvement. Keep the Lighthouse TODO open and repeat with a pinned/reproducible Chrome launch command before making further visual trade-offs.

Exact artifacts remain in `/tmp/vitasil-current-20261109-run/lighthouse-hero768-mobile`, `lighthouse-hero2017-mobile`, `lighthouse-hero2018-mobile`, `lighthouse-hero768-samecli`, and `lighthouse-headless-explicit-control.json`; ZIP outputs are in `/tmp/vitasil-hero-2017-qa-20261109` and `/tmp/vitasil-hero-2018-qa-20261109`. The QA branch was restored to sequence 6 after the control run. Do not remove these artifacts or the QA stack until the reproducible Lighthouse retest and documentation handoff are complete.

#### Cookie-consent contrast and interaction fix — 9 November 2026

The Vitasil footer's fixed consent banner inherited the footer's white text color onto a white background, making its copy invisible. Its footer placement was below the viewport, so the public runtime deferred mounting it even though the fixed banner itself was on screen; the visible buttons consequently had no event handlers. `RuntimeApp.ts` now eagerly mounts an offscreen placement when it contains a visible fixed descendant intersecting the viewport, while continuing to defer ordinary offscreen placements. The Vitasil `legal-documents` feature now renders a high-contrast responsive panel with a heading, concise explanation, link to `/cookies`, and clear accept/reject actions. The `/cookies` route suppresses the redundant fixed banner and keeps its own choice controls.

The choice is stored as a versioned accepted/rejected record in browser `localStorage`, the banner disappears immediately, and a choice on `/cookies` receives a visible live-region confirmation. Existing version `1` choices remain compatible. No optional third-party scripts or analytics are enabled by these buttons; current Vitasil cookie documentation says none are used. If browser storage is unavailable, the module retains the selection in memory for the page session; that fallback was not separately exercised in this browser run.

Checks passed: Vitasil `pnpm typecheck`, `pnpm lint`, and `pnpm build:features`; Engine `pnpm typecheck`, `pnpm build:runtime`, and Linux ARM64 API binary build. Browser QA on the disposable QA Vitasil branch verified readable text color `rgb(63, 77, 67)` on `rgb(255, 254, 250)`, visible fixed banner at 1200×732 and 390×844 without horizontal overflow, immediate placement initialization while the footer root was at y=2387, accept and reject saving, dismissal, and persistence after reload. `/cookies` showed no duplicate fixed banner, retained its own controls, stored rejection, and displayed its status text. Playwright console reported zero errors/warnings. Routes `/`, `/catalog`, `/cookies`, and `/form` returned HTTP 200. The mobile screenshot is retained at `../../vitasil/.playwright-cli/page-2026-11-09T20-37-18-493Z.png`.

The updated ZIP `legal-documents@1.0.11` was registered in the task-owned disposable Engine, copied into a child snapshot, and tested on the `website` branch. To preserve the shared-style digest pinned by the other features in that snapshot, this isolated feature ZIP reused the existing registered `core.css`; the feature's own stylesheet and entry were freshly built. Browser testing was performed against the child snapshot, and later QA snapshots created on top of it retained `legal-documents@1.0.11`. The current branch is assigned sequence 12 (`Vitasil footer layout 2026-11-09`); its parent chain includes this change. The task-owned containers `vitasil-current-20261109-vitasil-1`, `...-db-1`, and `...-minio-1` remain running for the user at `http://127.0.0.1:28122/` and `http://127.0.0.1:28120/admin/`. No working Vitasil service or data was accessed.

#### Cookie notice flash on reload — 10 November 2026

The saved accepted choice was present in browser storage, but a prerendered cookie notice still appeared after reload. The legal footer is below the viewport; the runtime tried to infer that its child was fixed before the feature stylesheet had necessarily been applied. It therefore deferred the placement and left the prerendered notice in place. `RuntimeApp.ts` now eagerly mounts any placement containing the explicit `[data-engine-runtime-overlay]` marker, independent of computed CSS positioning. The public shell hides marked overlays only while browser scripting is enabled and the runtime has not completed initialization. The server-rendered notice remains visible when scripting is disabled. The feature mount then reads the saved choice and removes the prerendered notice before the runtime releases the visibility gate.

Verified in the task-owned Vitasil QA instance at `http://127.0.0.1:28122/`, snapshot sequence 13 (`f8ee78e7-ec06-414e-af37-08b9cfdaac66`), with `legal-documents@1.0.12`. In the browser, reloading with the existing `accepted` version-1 preference left no cookie notice after initialization; the legal placement was ready and contained only its links. A controlled reload with the runtime bundle intercepted showed the prerendered notice as `visibility:hidden` before initialization, confirming it cannot flash during the consent lookup. The intercepted bundle intentionally returned 503 for that gate test and was un-routed immediately afterward. `/` returned 200 after replacing the task-owned QA container's Engine binary with the rebuilt Linux/ARM64 binary. No working Vitasil API, database, storage, or volume was used.

Checks for this fix: Engine `pnpm typecheck`, `pnpm build:runtime`, `pnpm lint`, `go test -count=1 ./pkg/runtime`, `go vet ./pkg/runtime`, and Linux/ARM64 `go build ./cmd/api` passed.

#### Internal feature origin blocked by public CSP — 10 November 2026

The browser error was real on the previously open page: its older prerendered header emitted the absolute internal URL `http://127.0.0.1:8082/feature-assets/69227cd5e89c496f01f5127b3455bb974ffa8def4daa69128e05b46fd3812a12/assets/cow-BZyfs3AS.svg`. That Engine branch intentionally permits same-origin images, not the internal branch port. The current Vitasil `header` source now takes only the asset URL's pathname, query, and fragment when deriving the default logo; the internal prerender origin is not serialized into the page. No CSP exception was added.

After reloading the task-owned QA site, the browser moved from the old tab snapshot to active snapshot sequence 15 (`c512d8f3-8bd0-4eab-9991-d39cbf42a98b`, `header@2.0.16`). The logo URL became `/feature-assets/803a9f9124cc0958cbed7c328e02006afee758ddb620c7d7672d4b477a84e4b4/assets/cow-BZyfs3AS.svg`; it returned HTTP 200 and decoded to 80×80 pixels. The fresh page reported zero console errors or warnings. The earlier CSP message was from the stale page/snapshot and is no longer present after reload. No API writes or changes to Vitasil database/storage were made in this check.
