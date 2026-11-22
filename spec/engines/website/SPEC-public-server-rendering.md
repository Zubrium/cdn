# Публичный Website Engine: document rendering и DOM contract

## Границы владения

Go `html/template` формирует полный публичный HTML document: `head`, status, route output, metadata, placement roots и inert runtime bootstrap. Go владеет публичной маршрутизацией, snapshot resolution, published data projection, canonical/robots/OG metadata и начальным HTML. Публичные маршруты не используют Vite shell или SPA fallback. В `pkg/runtime/scripts` остаётся только browser runtime для target feature ABI и последующих navigation events.

`/api/runtime` остаётся отдельным JSON API-контрактом. Начальный document получает разрешённую composition и опубликованные данные через `PageDocument`; browser runtime не обязан повторно загружать ту же composition при первоначальной загрузке. Последующие запросы и навигация используют только явно разрешённые API-контракты.

Feature runtime использует только `static-lifecycle`, описанный в [SPEC-feature-runtime.md](features/SPEC-feature-runtime.md). Неизвестный или неподдерживаемый ABI отклоняется до исполнения. Feature modules не получают credentials или секреты.

## HTML template, escaping и DOM

Шаблон получает строго типизированный готовый view model. Пользовательские данные не преобразуются в `template.HTML`, `template.JS` или иные trusted types. Metadata, attributes и bootstrap проходят contextual escaping. Bootstrap сериализуется `encoding/json` с HTML escaping и размещается только в inert `script type="application/json"`.

Полный документ содержит корректный `<head>`, ровно один page-level `<main>`, упорядоченные placement roots и content-addressed runtime assets. `#root` не получает общих page/slot wrappers. Основные placements находятся внутри единственного `<main>`; остальные располагаются без shared slot wrappers. Каждый placement имеет один нейтральный lifecycle root, roots не вкладываются друг в друга, а feature markup не создаёт вложенный `<main>`.

Начальный HTML, передаваемый `hydrate`, соответствует target runtime и не заменяется параллельным скрытым tree. `render` создаёт начальную разметку в пустом root, `hydrate` связывает готовую разметку с интерактивным состоянием, `update` применяет новый context, `dispose` освобождает ресурсы. Ошибка placement изолируется и не меняет соседние placements. Формы в статическом HTML inert; отправка требует JavaScript.

## HTTP и cache

Каждый URL повторно разрешается Go router; обычная same-origin navigation и browser history обеспечивают Back/Forward без второго client-side router. Неизвестный URL/detail возвращает 404, отсутствие назначенного snapshot — встроенный 503, runtime error — 500. Страницы ошибок получают `noindex`; ошибки и fallback без успешного prerender выдают `no-store` и не имеют ETag.

Успешная индексируемая страница со status 200 использует `Cache-Control: no-store` и strong ETag только при полной resolved document identity. `public, max-age=<uint>[, immutable]` допустим только для representation с полностью стабильной identity и по правилам [SPEC-caching.md](../../architecture/SPEC-caching.md). Identity учитывает branch, snapshot, published content revision, structural digest, route/path/status, metadata, route arguments, theme/system assets и resolved placement outputs. Query raw string не включается в key; учитывается эффективный результат разрешения arguments и данных. Drafts, submissions, credentials и private media не включаются в публичный документ.

Перед сохранением нового HTML renderer повторно проверяет snapshot/content identity. Если состояние изменилось, результат отклоняется и не кешируется. Совпадающие cold requests могут объединяться в одну генерацию; любой такой кэш остаётся process-local, bounded по записям и байтам и подчиняется [SPEC-caching.md](../../architecture/SPEC-caching.md). Ошибки и частичные ответы не кешируются, межпроцессный shared cache не предполагается.

Сжатие HTTP-представлений, `Vary: Accept-Encoding`, locale-политика и поведение внешнего CDN/proxy определяются [SPEC-caching.md](../../architecture/SPEC-caching.md). Range и бинарные media не перекодируются; deployer не меняет origin semantics.

## Prerender worker

Chromium worker запускается отдельным Node/Playwright процессом без shell и использует непостоянный browser context без cookies и credentials. Node, Playwright, browser executable, concurrency, queue, timeout, retries, viewport, HTML limit, cache bounds и JS heap задаются конфигурацией.

Worker выполняет только GET/HEAD к внутреннему listener и разрешённым публичным runtime/assets/media/collection endpoints. Внешняя сеть, admin endpoints, service workers, WebSocket и изменяющие методы запрещены. Рекурсивный prerender блокируется внутренней непредсказуемой меткой, не публикуемой клиенту. Chromium работает в sandbox; если платформа не поддерживает его, worker сообщает отказ и renderer не заявляет успешный prerender.

Результат сериализуется только после runtime readiness, разрешения необходимых публичных данных и проверки актуальности snapshot/content revision. Частичный HTML не кешируется. При worker failure Go возвращает безопасный server shell с корректными metadata/status; ошибки страницы и сетевые запреты не повторяются автоматически. Повтор допустим только для сбоя до создания browser context, в пределах общего timeout и исходного queue slot.

Diagnostics ограничены allowlisted стадиями и безопасными кодами ошибок. URL, query, cache keys, HTML, stderr, snapshot identifiers и пользовательские данные не записываются.

## Acceptance criteria

- Проверить полный server-rendered document, escaping, один `<main>`, placement order и отсутствие дополнительных wrappers.
- Проверить target ABI `render`/`hydrate`/`update`/`dispose`, initial paint, cancellation, timeout, placement isolation и отсутствие DOM mutation после отмены.
- Проверить no-JS document, успешный prerender, worker disabled/failure и безопасный fallback без заявления о prerender success.
- Проверить route/status/SEO, 404/503/500, system-page overrides, publication/restore и отсутствие drafts/private data в public HTML.
- Проверить cache identity, validators, query semantics, concurrency coalescing, publish/assignment invalidation и отказ от кеширования частичного/ошибочного результата согласно [SPEC-caching.md](../../architecture/SPEC-caching.md).
- Проверить Chromium sandbox, запросы worker, retries и приватность diagnostics на изолированной disposable среде.
- Все browser/API acceptance выполнять только с disposable DB/storage. Фактические результаты, ограничения и незакрытые сценарии записывать в [QA_REPORT.md](../../../docs/QA_REPORT.md); критерии здесь сами по себе не подтверждают прохождение.
