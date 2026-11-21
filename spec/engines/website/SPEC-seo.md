# SEO, browser prerender и системные страницы

## Metadata и индексация

Системные и страничные metadata хранятся в payload snapshot. Поддерживаются canonical origin, language, `noIndex`, title, description, Open Graph media и page-level overrides. Canonical принимает только HTTP(S) origin без path, query или userinfo и не строится из `Host` запроса. Пустой canonical разрешён до настройки; sitemap в этом случае пустой.

Назначенный snapshot — источник metadata публичного runtime для статической branch. Индексация разрешена только при согласии branch policy и metadata сайта/страницы. Preview не является контролем доступа: приватный preview закрывается gateway/auth.

`robots.txt` позволяет crawler прочитать публичные страницы и запрещает административные URL. Он не блокирует необходимые публичные GET runtime/data/theme/assets endpoints. Публичные JSON runtime/data ответы помечаются `X-Robots-Tag: noindex`; это не препятствует их загрузке как данных страницы. Sitemap указывается только для индексируемой branch с canonical origin. Неиндексируемая публичная branch выдаёт `noindex,nofollow` и пустой sitemap; `robots.txt` не используется вместо контроля доступа или как способ удалить URL из индекса.

Query-варианты индексируемой страницы получают `noindex,follow`. Preview, query-варианты, ошибки и core fallback не должны становиться индексируемым или shared-cache содержимым.

## Sitemap и detail metadata

Sitemap содержит только индексируемые и реально разрешаемые статические routes и detail URLs опубликованных записей. Он исключает дубли, query-варианты, отсутствующие записи и страницы, назначенные в `systemPages`. Произвольные regex-маршруты не угадываются; для неперечисляемых маршрутов возвращается диагностика. Каждый URL повторно проверяется runtime router.

Detail route lookup использует уникальное поле из `dataInputs` binding. Для `record` input можно задать один `detailSEO` mapping: `titleFieldId`, `descriptionFieldId`, `ogTitleFieldId`, `ogDescriptionFieldId` и `ogImageFieldId`. Текстовые значения используют поля `text` или `longText`, изображение — `media`. Mapping настраивается в `/site`; значения редактируются в `/content`.

Runtime строит detail metadata только из опубликованных значений выбранной записи. Пустые или отсутствующие значения наследуют page/system SEO. Если отдельные OG title/description не заданы, они следуют за динамическими title/description. OG image публикуется только при доступности публичного файла и выводится относительно canonical origin. Устаревший `fieldId` блокирует structural save; неизвестный detail lookup возвращает 404. Draft values, submissions и private media не попадают в public HTML или cache.

Browser prerender и `/api/runtime` используют одинаковую итоговую `RuntimeComposition.Metadata`. Metadata и разрешённое содержимое placements входят в document cache identity. Draft save не меняет публичное представление; публикация и restore обновляют `contentRevision`.

Sitemap index делит результат, если XML превышает 50 000 URL или 50 MiB; каждая часть остаётся в пределах обоих лимитов. URL list может кешироваться в памяти по branch, active snapshot, content revision, structural digest, canonical origin и branch/page indexing policy. Draft save не инвалидирует опубликованный результат. XML ETag вычисляется по байтам представления; GET и HEAD поддерживают conditional 304.

## HTTP cache и статусы документов

Индексируемый ответ со статусом 200 и полностью разрешённым `CacheKey` может использовать `Cache-Control: public, max-age=0, must-revalidate` и strong ETag. Origin повторно разрешает runtime composition перед обработкой validators; 304 не должен скрывать публикацию, смену snapshot assignment, metadata или feature build. GET/HEAD учитывают списки validators, `*` и weak comparison согласно HTTP.

Query-варианты, noindex, ошибки, неготовый сайт и failed prerender fallback выдают `Cache-Control: no-store` без ETag. Shared cache не получает TTL, способный скрыть изменение между origin revalidations. Дополнительная CDN-политика находится вне контракта приложения и задаётся оператором.

Неизвестный URL или detail lookup возвращает 404; отсутствие назначенного snapshot — встроенную страницу 503 с `noindex`; ошибка runtime — 500. `catchAll` сам по себе возвращает 200. Если его page назначена в `systemPages["404"]`, совпавший запрос сохраняет содержимое override, но получает 404 и `noindex,nofollow`. Имя страницы и feature не меняют HTTP status. System pages исключены из sitemap.

## Browser prerender worker

Worker запускает отдельный Node/Playwright процесс без shell и использует непостоянный browser context без cookies и credentials. Пути Node, Playwright и browser executable, concurrency, queue, timeouts, retries, viewport, HTML limit, cache limits и JS heap задаются конфигурацией; скрытые runtime defaults не добавляются.

Worker разрешает только GET/HEAD к текущему внутреннему listener и явно разрешённым публичным runtime/data/theme/assets/media endpoints. Внешняя сеть, admin endpoints, service workers, WebSocket и изменяющие методы блокируются. Рекурсивный prerender запрещён внутренней непредсказуемой process-меткой, которая не раскрывается клиенту. Chromium работает в sandbox; если платформа не поддерживает sandbox, worker сообщает ошибку и возвращает server shell без заявления об успешном prerender.

Каждый ответ одинаков для всех User-Agent. При успешной генерации HTML содержит inert prerendered copy и гидратируемый target runtime без дополнительных page/slot wrappers или вложенных `<main>`. При отключённом или отказавшем worker возвращается актуальная server shell; частичный HTML не кешируется, ошибка не считается успешным prerender и не кешируется.

HTML cache локален для процесса, ограничен конфигурацией и использует resolved identity: branch, snapshot, content revision, structural digest, metadata, normalized path и эффективный результат route/argument/data resolution. Сырая query string не является cache key: неиспользуемые параметры и порядок независимых query keys не создают отдельные entries; порядок повторных значений array arguments сохраняется. Cache misses с одинаковой identity объединяются; вытеснение использует LRU. Ошибки не кешируются. Каждая генерация повторно сверяет snapshot/content identity до сохранения результата и отклоняет изменившееся состояние.

Worker failure diagnostics ограничены безопасными кодами стадий и ошибок; URL, query, cache keys, HTML, stderr и данные посетителей не записываются. Общий timeout завершает группу worker-процессов. Повтор разрешён только для сбоя до создания browser context, в пределах настроенного общего timeout и исходного queue slot; ошибки страницы и запреты запросов автоматически не повторяются.

## Системные страницы

Администратор выбирает system pages из страниц хотя бы с одним static route. Назначение хранится в snapshot `config.systemPages` и сохраняется обычным structural save. Перед удалением назначенной страницы назначение снимается. Если выбранная страница удалена или стала динамической, редактор показывает предупреждение, а runtime использует встроенный fallback.

Для branch без назначенного snapshot всегда используется встроенная 503-страница; её нельзя назначить через startup config или snapshot. Отдельное публичное состояние 403 не заявляется, пока runtime не определяет согласованный источник такого запрета.

## Acceptance criteria

- Проверить canonical, language, title/description/OG escaping, отсутствие дублирующихся head tags и согласованность metadata между HTML и `/api/runtime`.
- Проверить robots/sitemap для индексируемой и неиндексируемой branch, detail records, system pages, неизвестных routes, query variants и branch без snapshot.
- Подтвердить, что drafts не меняют public HTML, ETag или sitemap, а publish/restore обновляют корректные представления.
- Проверить HTTP validators, HEAD, 304, ошибки/no-store и отсутствие validators у failed prerender fallback.
- Проверить worker sandbox, сетевые и method ограничения, queue/timeouts, отказ/восстановление, coalescing и отсутствие чувствительных данных в diagnostics.
- На disposable среде проверить актуальные feature ZIP, prerender HTML, гидратацию, no-JS содержимое и отсутствие browser errors для главной, каталога, detail, формы и system pages.
- Проверки запускать только на изолированных disposable ресурсах. Результаты, ограничения и незакрытые сценарии фиксировать в [QA_REPORT.md](../../../docs/QA_REPORT.md) и [TODO](../../../docs/TODO-admin-snapshots-content-files.md); этот документ сам по себе не подтверждает прохождение acceptance.
