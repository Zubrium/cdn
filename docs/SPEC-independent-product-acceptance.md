# Независимая приёмка Website Engine

Статус: критерии независимой приёмки согласованы 8 ноября 2026. Проверка не использует Vitasil ZIP, пути к Vitasil, API, базы, object storage или fixtures. Цель — подтвердить, что Engine и `/admin` поддерживают общий контракт для сторонних feature builds.

## Синтетические feature builds

Тесты сами создают минимальные ZIP format 2 с `manifest.json`, `static-module-v1`, JS и CSS. Сборки не загружаются из реестра Vitasil, не используют npm-пакеты, внешний CDN или server-side renderer.

- `qa-catalog@1.0.0`: collection-вход для Product; аргументы строки, числа, boolean, enum, массива простых значений и media; отображает каталог и имя связанной Category.
- `qa-product-detail@1.0.0`: record-вход Product, typed path-аргумент `slug` и lookup по уникальному model field; отображает тот же Product и вложенную Category.
- `qa-featured-products@1.0.0`: collection-вход Product и редакционный selection/order; отображает выбранное подмножество без копирования полей записи.

Общая модель содержит Product, Category и relation. Binding fixtures должны включать scalar, record, collection, однонаправленный вложенный relation, relation с wildcard для множества записей, аргументы разных типов и media argument. Варианты с неверным типом, отсутствующим полем/связью, обратным направлением и неверной wildcard/cardinality отклоняются API до сохранения; UI показывает ошибку рядом с соответствующим binding.

## Сквозной Engine сценарий

На уникальной disposable PostgreSQL и disposable object storage проверяются:

1. Пустая БД, baseline migration, регистрация трёх ZIP и создание корневого снапшота.
2. В браузере `/site` создаются сущности, поля и связи; граф доступен мышью и клавиатурной структурой; аргументы и все типизированные inputs связываются с model ID.
3. Структура сохраняется только дочерним снапшотом. Родительский payload и назначения веток не меняются.
4. `/content` создаёт Product/Category, редактирует nested relation, выбирает и сортирует featured Products, сохраняет draft и явно публикует его.
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

Vitasil acceptance не используется как доказательство независимого контракта. 8 ноября 2026 прошли отдельные synthetic сценарии: `TestIndependentProductEditorBrowserAcceptance` (ER/browser/bindings), `TestIndependentProductSyntheticAcceptance` (runtime/prerender/SEO без внешнего storage), `TestIndependentProductSyntheticAcceptanceWithS3` (S3 media) и `TestIndependentProductSitepackCrossInstallAcceptance` (межинсталляционный перенос с MinIO). Повторный прогон `TestIndependentProductSyntheticAcceptanceWithS3` прошёл с S3-backed выдачей feature assets и Chromium prerender. Причина прежнего сбоя находилась в тестовом helper: public server читал сборки из `fakeStore`, хотя ZIP загружались в injected S3 object store; helper исправлен. `TestRoleRevisionMatrixAcceptance` и `TestRoleRevisionMatrixBrowserAcceptance` подтвердили серверную и браузерную матрицы ролей и revision guards. Браузерный role/revision тест также проверил конкурирующую вкладку, сбой до commit и потерю ответа после commit, сохранение локального черновика при конфликте, предупреждение при уходе и `beforeunload`, а также восстановление черновика после перезагрузки без автосохранения поверх серверной ревизии. Подробные команды, exact disposable resources и ограничения указаны в [QA_REPORT.md](QA_REPORT.md).

Критерии пока не закрыты целиком. Role/revision API и browser acceptance проходят на disposable окружении, включая сохранение локального черновика при конфликте, повтор после неопределённого ответа, восстановление отдельным черновиком и последующую явную публикацию. ER editor прошёл synthetic browser acceptance для клавиатуры, фокуса, relation validation и четырёх ширин экрана. Совместный сценарий media с S3 и prerender прошёл на disposable MinIO и Chromium. 8 ноября повторно прошла browser acceptance всех девяти актуальных Vitasil placements на главной, каталоге, detail и 404; проверены контент `Product`, data bindings, feature slots, отсутствие runtime/network ошибок и отсутствие branch/file-upload controls в `/site` и `/content`. Ручная оценка screen reader/WCAG и полный keyboard/accessibility набор ER editor остаются открытыми. TODO остаётся открытым, пока эти и остальные критерии не подтверждены.
