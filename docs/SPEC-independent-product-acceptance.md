# Независимая приёмка Website Engine

Статус: критерии независимой приёмки обновлены 15 ноября 2026. Проверка не использует Vitasil ZIP, пути к Vitasil, API, базы, object storage или fixtures. Цель — подтвердить, что Engine и `/admin` поддерживают общий flat-model контракт для сторонних feature builds.

## Синтетические feature builds

Тесты сами создают минимальные ZIP format 2 с `manifest.json`, `static-module-v1`, JS и CSS. Сборки не загружаются из реестра Vitasil, не используют npm-пакеты, внешний CDN или server-side renderer.

- `qa-catalog@1.0.0`: collection-вход для Product; аргументы строки, числа, boolean, enum, массива простых значений и media; отображает каталог.
- `qa-product-detail@1.0.0`: record-вход Product, typed path-аргумент `slug` и lookup по уникальному model field; отображает тот же Product.
- `qa-featured-products@1.0.0`: collection-вход Product и редакционный selection/order; отображает выбранное подмножество без копирования полей записи.

Общая модель содержит независимые Product и, при необходимости, другие flat entities. Binding fixtures должны включать direct scalar, record и collection mappings, аргументы разных типов и media argument. Варианты с неверным типом, отсутствующей моделью/полем, неверным `relationIds` и несовместимым legacy relation payload отклоняются API до сохранения; UI показывает ошибку рядом с соответствующим binding.

## Сквозной Engine сценарий

На уникальной disposable PostgreSQL и disposable object storage проверяются:

1. Пустая БД, baseline migration, регистрация трёх ZIP и создание корневого снапшота.
2. В браузере `/site` открывается список моделей и отдельная страница модели; создаются модели и поля, порядок управляется мышью и клавиатурой; аргументы и все типизированные inputs связываются напрямую с model/field IDs. Relation/canvas controls и пустые relation placeholders отсутствуют.
3. Структура сохраняется только дочерним снапшотом. Родительский payload и назначения веток не меняются.
4. `/content` создаёт Product и другие flat records, редактирует только собственные поля, выбирает и сортирует featured Products, сохраняет draft и явно публикует его.
5. Media upload выполняется через `/media`; argument и model media field выбирают существующий файл. В редакторах нет upload control.
6. Каталог и detail используют одну Product, detail разрешается по уникальному slug, виджет отображает выбранный порядок. Публичная выдача/prerender использует опубликованные данные, а publish, snapshot assignment и rollback обновляют HTML/ETag без утечки draft.
7. Fork копирует published/draft model state и selections независимо; история restore создаёт новый draft и не публикует его автоматически. Sitepack import/export проверяется на независимой установке.

Проверки должны использовать синтетическую ESM-реализацию, создавать минимум две ветки и проверять назначения только через `/snapshots/branches`. Все роли, conflicts, browser/keyboard flows и media picker проходят на disposable окружении.

## Матрица ролей

| Действие | admin | moderator | staff | user |
| --- | --- | --- | --- | --- |
| Читать, менять структуру сайта и модель; менять feature builds; назначать ветки | Да | Да | Нет | Нет |
| Читать, редактировать, сохранять, публиковать и восстанавливать content history | Да | Да | Да | Да |
| Читать и экспортировать заявки | Да | Да | Да | Да |
| Менять статус и архивировать заявки | Да | Да | Нет | Нет |
| Удалять заявки | Да | Нет | Нет | Нет |
| Читать, загружать и переименовывать media; выбирать в редакторе | Да | Да | Да | Да |
| Безвозвратно удалять media | Да | Нет | Нет | Нет |

Неавторизованный вызов возвращает 401; запрещённая роль — 403; ни одна из операций с запретом не оставляет частично изменённую структуру, ревизию, файл или заявку. `staff` — поддерживаемая legacy content role, она должна быть описана и в серверной role registry, и в клиентских `can*` helpers.

## Конкурентные и восстановительные сценарии

- Устаревшая ревизия draft save, publish, restore, структурного fork/binding и branch assignment отклоняется без перезаписи серверного состояния.
- Вкладки с одинаковым исходным revision сохраняют локальный ввод; UI сообщает локальную и серверную ревизии и предлагает явно загрузить/сравнить состояние.
- Network failure до отправки не меняет сервер; потеря ответа после commit допускает повтор запроса без дублирования записей, publication или снапшота.
- Browser reload и уход со страницы сохраняют локальные несохранённые значения либо требуют явного решения; никакая ошибка не публикует content draft.
- Restore создаёт новый revision draft; published revision и публичный HTML меняются только после отдельной publish операции.
- При отказе роли API и UI не применяют часть multi-record или binding изменений.

## Текущий статус

Vitasil acceptance не используется как доказательство независимого контракта. Ранее прошли synthetic runtime/prerender/SEO, S3 media и sitepack сценарии; role/revision API и browser acceptance подтвердили серверную и браузерную матрицы ролей и revision guards. После удаления relation-модели текущие frontend component tests и Go compatibility tests заново проверяют flat model, direct bindings и отказ непустых legacy relation-полей. Старые синтетические ER relation acceptance tests удалены как проверки снятой возможности; новый независимый full-stack flat-model run остаётся открытым.

Критерии пока не закрыты целиком. Role/revision API и browser acceptance проходят на disposable окружении, включая сохранение локального черновика при конфликте, повтор после неопределённого ответа, восстановление отдельным черновиком и последующую явную публикацию. Текущий flat-model list/page проверен component tests и Chromium QA на desktop и 390 px; direct bindings и legacy rejection проверены Go tests и HTTP lifecycle test. Полный synthetic flat-model browser run с media/S3/prerender, ручная screen reader/WCAG оценка и расширенная keyboard matrix остаются открытыми.
