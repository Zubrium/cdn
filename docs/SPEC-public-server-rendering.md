# Публичный Website Engine: Go document renderer и DOM structure

Статус: переход продолжается; acceptance и Lighthouse ещё не закрыты. Публичная HTML-оболочка и начальный bootstrap принадлежат Go `html/template`; feature ABI и админский React SPA сохраняются.

## Цель и границы

Go — единственный владелец публичной маршрутизации, snapshot resolution, HTTP status, route parameters, published data projection, canonical/robots/OG metadata и каждого начального HTML document. Go `html/template` выдаёт весь документ, head, placement roots и inert bootstrap. В `pkg/runtime/scripts` остаётся только browser-код, необходимый для исполнения сохранённых feature ABI и последующих `popstate` обновлений; Vite не генерирует публичный HTML shell и на публичных маршрутах нет SPA fallback.

Сохраняются все существующие feature ABI и контракты lifecycle: `mount/update/unmount`, context, ready/error handling, assets, route/data/media. Не добавляются ABI, ABI aliases, поля manifest для выбора HTML root или скрытые изменения контрактов. Внутренний ленивый host для уже существующего `legacy-react-v1` разрешён только если нужен для исполнения этого ABI; он не является новым ABI и не участвует на страницах без таких placements. Go server rendering заменяет только формирование публичного HTML document и не изменяет контракты текущих Vitasil ZIP.

Сохранять существующую Vitasil feature-разметку, UI и поведение. Не выполнять массовую перепись фич в native DOM и не менять их контракты ради упрощения Engine shell. Если исходник требуется адаптировать, ограничиться build/export boundary для уже поддерживаемого ABI (`static-module-v1`); не добавлять новую ABI-версию и не менять feature arguments, `dataInputs`, маршруты, media, forms или lifecycle без доказанной блокирующей ошибки. Не трогать рабочие production DB и Vitasil data.

DOM contract: `#root` не получает дополнительных page/slot containers. Размещения основного слота находятся внутри ровно одного page-level `<main>`. Неосновные placements — непосредственные дети `#root`, без shared slot wrappers для header/footer/background. Каждый placement сохраняет ровно один нейтральный `<div>` lifecycle mount root по существующему ABI; roots не вкладываются друг в друга. Внутренняя семантическая разметка определяется смыслом feature content. В публичном документе ровно один page-level `<main>`; feature markup не добавляет вложенный `<main>`.

`/api/v2/runtime` сохраняется для поддерживаемых API-потребителей и тестового harness, но основной HTML path получает composition и публикуемые данные напрямую из `SnapshotService.PageDocument`; JSON API не запрашивается браузером повторно. После успешного Chromium prerender клиент сохраняет эту разметку до одной возможности отрисовки браузером, затем монтирует существующий feature ABI для интерактивности. Сигнатуры `mount/update/unmount`, context, ready/error handling и данные фич не меняются.

## Document template и escaping

`html/template` получает строго типизированный готовый view model. В шаблон не передаются пользовательские HTML/JS как исполняемые значения. Title, descriptions, canonical, robots, OG, корневые атрибуты и placement attributes проходят contextual escaping. Bootstrap JSON строится из уже разрешённой публичной композиции, повторно кодируется `encoding/json` с HTML escaping и помещается только в inert `script type="application/json"`; ни feature аргументы, ни контент не конкатенируются в исполняемый script. Начальный runtime читает этот bootstrap и не делает повторный запрос `/api/v2/runtime`; сам API остаётся доступен для остальных потребителей и последующих route changes.

HTML-разметка и template actions находятся в `pkg/runtime/templates/*.tmpl` и встраиваются через `embed.FS`. Go-код содержит только типы данных и логику именованных шаблонов; не добавлять HTML-шаблоны или `{{...}}` actions строковыми литералами в `.go` и не собирать их конкатенацией. Доверенные структурные фрагменты создаются только из Go templates; динамические метаданные и значения placements остаются типизированными значениями с contextual escaping. Публичные стили находятся отдельно от admin CSS в `pkg/runtime/styles/`; маленький embedded stylesheet можно inline включить через named template при соблюдении лимита и безопасного контекста `<style>`.

Обычная страница содержит корректный `<head>`, одну `<main>`, семантические landmarks в feature markup, placement roots в согласованном порядке, content-addressed runtime assets и bootstrap data. CSS и JSON ограничиваются существующими лимитами. Опубликованные значения — единственный источник runtime data; drafts, submissions, credentials и private media не включаются. Dynamic module errors остаются изолированы в собственном placement и не ломают соседние модули. Пустые ветки, 404, 403 и системные ошибки рендерятся серверным системным документом с правильным HTTP status и `noindex`.

Ссылки сайта используют обычную same-origin навигацию браузера. Go повторно разрешает каждый URL; Back/Forward работают через стандартную историю. Client-side router не создаётся. Формы сохраняют существующие submission endpoints и валидацию; media и backend SDK не меняют контракт.

## Chromium prerender и HTML cache

`DocumentHandler` вызывает resolver `PageDocument`; при назначенном snapshot handler синхронно вызывает `Prerenderer.Render` до ответа. `Render` сначала проверяет in-memory TTL/LRU cache по hash resolved `Document.CacheKey + requestPath`; concurrent cold requests одного key coalesce через pending promise/channel. При miss выполняется ограниченный синхронный Chromium worker, который открывает тот же публичный URL с internal nonce, ждёт module readiness/quiet interval и сериализует готовый HTML. Успех помещается в cache; ошибка не кешируется, ответом остаётся Go-rendered document с `X-Engine-Prerender: unavailable` и `Cache-Control: no-store`. Холодный промах блокирует HTTP ответ. Проверки должны подтвердить неизменность cache semantics после самостоятельной сборки content-addressed runtime scripts.

`PageDocument` identity учитывает branch ID, snapshot ID, content revision, structural digest, resolved route/path/status, head metadata, route args, theme/system assets и уже разрешённые placement outputs. Cache сохраняет только status 200, indexable, query-free pages без внешнего backend. Query variants и external-backend страницы обходят общий HTML cache; external backend также пропускает Chromium. Publish/restore, snapshot assignment/structure и metadata дают новую identity; stale entries вытесняются TTL/LRU, отдельная массовая invalidation не требуется. Response `ETag` зависит от cache identity и HTML; HTTP validators применяются только cacheable документам. `X-Engine-Prerender: hit` сейчас означает успешное наличие prerendered HTML, включая только что завершившийся miss; cache hit/miss отдельно видны в worker structured logs. Новая реализация сохраняет эти semantics, cache key, in-flight coalescing, TTL/LRU, fallback и blocking miss; background warm queue не добавляется.

До рефакторинга baseline уже снят на одинаковых pre-DOM Vitasil ZIP и disposable конфигурации. На 9 ноября пять warm Lighthouse прогонов дали median 100 для пяти комбинаций route/device и 99 для mobile catalog; холодный первый ответ проверен отдельно. Каталог mobile — открытая оптимизация до целевой медианы 100. После отказа от массовой переписи source для повторной приёмки должны использоваться ZIP из предшествующей сборки либо эквивалентные сборки восстановленных feature sources; рабочая Vitasil не используется.

## Порядок реализации

1. Go template document contract, типизированный bootstrap, placements/styles/static HTML и system documents.
2. Сохранить текущий client runtime/lifecycle; Go document renderer должен выдавать правильные placement roots и bootstrap assets, не меняя host/runtime контракты.
3. Проверить и точечно упростить только engine-owned wrappers; проверить CSS, landmarks, lifecycle, no-JS prerender и динамические обновления перед уплощением.
4. Переместить ABI sources, публичные styles и assets внутрь `pkg/runtime`, убрать frontend-пакет публичной страницы, Vite `index.html`, SPA fallback и неиспользуемое snapshot-shell storage; обновить build/Docker/embed paths без ABI/manifest changes, сохранить `/api/v2/runtime` и существующие API/status/SEO/cache contracts.
5. Disposable API + browser tests: escaped/untrusted fields, every current ABI, system pages, form submit, media, back/forward, JS disabled/enabled, route/status/SEO, readiness/failure, cache identity/miss/hit/invalidation и запрет утечки private/draft data.
6. После реализации снять пять Lighthouse warm-cache Performance runs на mobile и desktop для `/`, catalog и detail; median всех шести комбинаций должна быть 100. Cold miss измерить отдельно, не объединять с warm target.

## Acceptance и ограничения

- Go `html/template` формирует полный исходный HTML документ, head, status, route output и семантическую структуру; нет публичного Vite HTML shell, SPA fallback, route table или повторного composition waterfall.
- В исходной схеме нет устаревшей таблицы предварительно сохранённых HTML shells; каждый документ строится из актуального состояния и существующей HTTP/prerender cache identity.
- Feature ABI и manifests остаются неизменными; текущие Vitasil ZIP проходят текущую manifest validation и работают в публичном runtime. Legacy React host загружается только если выбранный placement требует существующий `legacy-react-v1`.
- В DOM выполнены placement wrapper invariants; `/`, catalog, detail params, 404, unconfigured branch, form, media, all slots, legal documents, back/forward работают.
- JavaScript-enabled HTML, no-JS HTML, escaping, media sizing, canonical/robots/OG, cache miss/hit, публикация/invalidation проверяются Chromium и HTTP acceptance.
- Lighthouse: медиана 100 для Performance в каждом из шести warm-cache mobile/desktop/route случаев по пяти независимым прогонам; отчёты включают scores, LCP/CLS/TBT, traces, screenshots и JSON.
- Disposable database/storage, bucket/container, port и temp paths записываются до теста; рабочий Vitasil не трогается. Production gateway/CDN и field Core Web Vitals не выводятся из локальной лаборатории.

### Фактические cache/runtime observations

До изменения кода проверены `pkg/runtime/document.go`, `pkg/runtime/prerender.go`, `features/sitebuilder/seo.go`, `presentation/http/routes.go` и `scripts/prerender-worker.mjs`: handler/resolver/worker путь и правила перечислены выше. Lighthouse baseline и измеренные miss/hit добавляются сюда до первой реализации.

### Lighthouse baseline — 9 ноября 2026

На текущих ZIP Vitasil зафиксировано шесть комбинаций маршрута/устройства, каждая с пятью warm runs и одним отдельным cold run. Warm медиа́ны Performance: home mobile 100, home desktop 100, catalog mobile 99, catalog desktop 100, detail mobile 100, detail desktop 100. Warm median LCP/CLS/TBT: home mobile 1582.7 ms / 0 / 0 ms; home desktop 453.6 ms / 0 / 0 ms; catalog mobile 1832.2 ms / 0.0270 / 0 ms; catalog desktop 513.6 ms / 0.0149 / 0 ms; detail mobile 1682.9 ms / 0 / 0 ms; detail desktop 474.2 ms / 0 / 0 ms. Cold Performance: 99, 99, 99, 100, 99 и 100 соответственно; каждый cold run логировал 0 cache hits, все 30 warm runs — по 1 hit. Lighthouse mobile catalog теряет баллы на FCP/LCP (1.4/1.8 s), не на CLS; его причину нужно снизить после runtime/bootstrap изменений.
