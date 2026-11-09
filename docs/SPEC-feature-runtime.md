# Универсальный ABI статических фич

Статус: контракт реализован; ручной screen-reader и полный responsive/accessibility audit остаются открытыми. Фича поставляется как готовый статический ZIP вместе с `manifest.json`; серверный код и сборка исходников движком не допускаются. Загруженные сборки считаются доверенным исполняемым кодом сайта. HTML-фича — статический фрагмент без шаблонной подстановки. Существующие legacy React-сборки остаются совместимыми; исходники Vitasil переведены на `static-module-v1`, а React и остальные клиентские зависимости входят в ZIP.

Реализованы runtime-контракты `legacy-react-v1`, `static-module-v1` и `static-html-v1`, immutable asset URLs и browser MIME types, локальные ESM/CSS/HTML-проверки, placement lifecycle, collection/form SDK, server fallback для `static-html-v1` и browser prerender. Содержимое остальных ABI присутствует в ответе без JavaScript только после успешного Chromium prerender; если worker выключен или завершился ошибкой, в shell не выполненные JS-фичи не имеют серверной разметки. Это полный текущий набор ABI: Go document rendering не добавляет и не меняет ABI. Manifest format 2 — отдельный контракт. Ранее проверено unit-тестами, disposable PostgreSQL, Chromium, девятью ZIP Vitasil, Vue SFC-фичей формы и vanilla ESM build. Эти результаты не подтверждают текущую deferred acceptance. Svelte/Angular сборки отдельно не запускались; рабочий Vitasil API/БД не использовались. См. [руководство автора фич](FEATURE-RUNTIME-AUTHORING.md).

## Цель и границы

Один manifest-контракт должен описывать фичу независимо от языка и UI-фреймворка, использованных автором. Движок принимает только уже собранные браузерные артефакты, проверяет их, хранит неизменяемо и выдаёт с того же origin. TypeScript, JSX, Vue, Svelte, Angular, vanilla JS и другие инструменты допустимы, если сборка выдаёт один из ABI ниже.

Фреймворк не запускается на сервере и не является зависимостью Website Engine. Не принимаются исходный проект, `package.json` для выполнения, серверный entrypoint, функции, SSR-сервер, Node.js API, нативные бинарники и команды сборки. Сервер не компилирует ZIP.

Manifest format 2 keeps feature metadata, the `/site` argument schema, typed `dataInputs`, runtime contract and strict public `form.submissionSchema`. The snapshot `dataModel` owns CMS entity/field schemas and `/content` generates record editors from it. Format 1 and manifest `content.*` schemas are rejected; there is no conversion path. `engineApi` denotes the SDK contract and runtime ABI below describes rendering. The model and bindings are specified in [SPEC-data-model.md](SPEC-data-model.md); removing CMS schemas does not change the runtime ABI because the Engine projects published records into the existing `content` channel.

## Model-backed runtime projection

Before passing a placement to any feature ABI, the Engine resolves its declared typed `dataInputs` and snapshot `dataBindings` into the existing runtime `content` channel. Collection output preserves editorial order or the explicit ordered record selection. Record output uses either the `/content` selection or the unique route lookup. Scalar, record and collection projections follow only the validated stable field/relation IDs; their shape must match the manifest's typed output paths and array cardinality. CMS record fields remain optional. Only published model records, relations and placement selections are public; draft workspaces, publication history and submissions are never returned by the public runtime endpoint or embedded into prerendered HTML. Media references used by a published record must be public and ready before publication. Manifest argument values with `format: "media"` are checked against ready public files before hydration and projection; a private or unavailable file reference fails closed and is not serialized to the public runtime or HTML.

On detail pages, an optional `detailSEO` mapping on the unique record input assigns stable IDs from that same bound entity to `title`, `description`, `ogTitle`, `ogDescription` and `ogImage`. Text roles accept model `text`/`longText`; the image role accepts `media`. Runtime uses the selected published record for both feature `content` and document head metadata. Empty mapped values inherit the page/site metadata, with derived Open Graph text following dynamic title/description unless an explicit static value or separate field mapping exists. An unresolved unique route lookup is HTTP 404. The runtime JSON composition and document renderer consume the same resolved `RuntimeComposition.Metadata`; this does not add feature ABI members or change format 2 manifests.

Document cache identity covers branch, assigned snapshot, structural digest, published model-content revision, resolved route and path arguments, final head metadata, theme/system assets, and projected placements. Any request query that may affect output bypasses HTML caching unless a separately reviewed safe identity is introduced. Snapshot metadata and structural mappings change the snapshot digest; publish/restore advances content revision; draft-only saves do not alter public output or sitemap identity. Public sitemap detail routes are enumerated from published records only and pass the normal route resolver before inclusion.

## Semantic composition DOM and lifecycle roots — agreed 9 November 2026

The runtime keeps the existing `legacy-react-v1`, `static-module-v1`, and `static-html-v1` contracts. A v1 placement owns one neutral `<div>` lifecycle root, required for mount/update/unmount, isolated styles, readiness, and placement-local failure handling. That root carries `data-engine-feature`, `data-engine-placement`, and `data-engine-slot`; Vitasil or the engine must not add another generic wrapper inside it just to identify the placement. Feature-authored content keeps its semantic structure (`<article>`, `<section>`, lists and forms); no nested `<main>` is allowed.

The interactive runtime renders placements in composition order as direct children of the application mount root, except `main` placements, which are direct children of the one page-level `<main>`. The engine does not create a page wrapper or shared `background`, `header`, or `footer` slot containers. The application mount node `#root` carries page classes and snapshot/content revision attributes; it is infrastructure, not a content landmark. A resolved page always contains exactly one `<main>`, including chrome-only pages; the empty main has no wrapper around it. Placement slot positioning/scoping is applied to the ABI lifecycle root via `.runtime-placement--<slot>`.

The Go shell uses one `#root` with non-main placement roots as direct children and main placements inside exactly one `<main>`; verified `static-html-v1` fragments are inserted into their existing placement roots. There is no parallel `<noscript>` subtree or hidden duplicate mount tree. After successful prerender, the populated document remains the live `#root` for both no-JavaScript output and the browser ABI lifecycle. React-backed feature helpers hydrate existing markup; other modules keep their ABI-defined `mount` behavior. Already-rendered `static-html-v1` content is reused on the initial prerendered route. The worker waits for the runtime to finish mounting visible placements, traverses the remaining roots, and removes its readiness marker before serialization. The ABI lifecycle `<div>` stays for all three current ABI kinds, including static HTML, to preserve one placement scope and existing manifests. There is no new ABI or implicit manifest root-tag extension. Feature CSS remains scoped under `[data-engine-feature="<key>"]`.

For a successfully prerendered page, offscreen placement roots remain deferred unless a visible descendant is `position: fixed` and intersects the current viewport. In that case the Engine initializes the placement immediately, even when its lifecycle root is below the fold. This keeps visible overlays (such as a cookie-consent panel owned by a footer feature) interactive without eagerly mounting unrelated offscreen content.

The engine adds no meaningful landmarks around feature content. Its error and loading messages use a paragraph/status-compatible element rather than an extra generic block. Background is decorative and marked by its feature; header/footer landmarks are supplied by their feature roots. Error UI remains isolated to one placement.

Acceptance: browser DOM assertions verify direct placement roots in slot order, no page or non-main slot wrappers, exactly one `<main>` even on chrome-only pages, no placement ancestor/descendant relationship, and continued isolation of mount/style errors. Static fallback tests verify equivalent parentage, an empty main on chrome-only pages, semantic feature content, immutable asset rewriting, and no-JavaScript HTML both after successful prerender and when the worker is unavailable. Lighthouse runs on isolated local Vitasil/engine output; the QA report records profile, category scores, notable audits, and environment limitations separately from structural code acceptance. Raw Lighthouse JSON/HTML is retained only when needed for follow-up analysis. Existing Vitasil feature manifest ABI is unchanged; feature-specific semantics are versioned normally.

## Статический ZIP

Новый ZIP содержит `manifest.json` в корне и только готовые файлы: JavaScript ESM-модули, CSS, HTML-фрагмент, изображения, шрифты и другие статические ресурсы, на которые ссылается сборка. Пути в манифесте относительные к корню ZIP, нормализованные и не могут выходить за него. Все импортируемые модули и обязательные ресурсы должны находиться в том же архиве; движок не скачивает npm-пакеты и не разрешает bare imports, CDN-скрипты или серверные модули.

Реестр продолжает хранить ZIP как неизменяемую сборку с digest. Обновление требует новой версии фичи. Движок проверяет ZIP, manifest, совместимость ABI и существование перечисленных файлов до регистрации. Запрещены traversal-пути, дублирующиеся нормализованные имена, symlink-записи и archive bombs согласно существующим ограничениям загрузки.

## Версионирование сборок и выбор в снапшоте

Логическая идентичность фичи — `manifest.key`; в реестре существует одна запись фичи на ключ. Каждая сборка идентифицируется парой `key + manifest.version`, версия обязательна и проверяется как SemVer. Загрузка той же пары с тем же digest идемпотентна; другой digest для уже зарегистрированной пары отклоняется. Сборки остаются неизменяемыми, старые версии сохраняются для уже созданных снапшотов и отката.

Снапшот фиксирует ровно одну точную версию для каждого `featureKey`. Все размещения, источники коллекций и связи содержимого с этой фичей используют закреплённую версию. Несколько версий одного `key` могут использоваться в разных снапшотах, но не смешиваются внутри одного снапшота. Изменение выбранной версии обновляет ссылки на неё в рабочей конфигурации; явное сохранение создаёт дочерний снапшот. Загрузка новой сборки сама по себе не меняет снапшоты и назначения веток.

В глобальном реестре показывается одна запись на логическую фичу с раскрываемым списком сборок. В редакторе состава снапшота также показывается одна запись на фичу и выбирается конкретная версия; для новой фичи предварительно выбрана версия с наибольшим SemVer-приоритетом, но в снапшот она попадёт только после явного действия пользователя и сохранения. При одинаковом SemVer-приоритете (например, версии различаются только build metadata) выше показывается сборка, зарегистрированная позже. Существующая версия отображается явно; загрузка более новой версии не переводит на неё существующие снапшоты.

В редакторе страницы выбор сборки группирует версии под одним логическим ключом. Если ключ уже включён в снапшот, доступна только его закреплённая версия; для ещё не включённой фичи версии упорядочены по SemVer, последняя помечена как рекомендуемая. Так редактор размещений не может незаметно создать два ABI/schema-варианта одной фичи в одном снапшоте.

При выборе несовместимой схемы сохраняется существующая проверка совместимости контента: несовместимости показываются до создания дочернего снапшота, исходные данные не перезаписываются и не удаляются автоматически. API, схема БД, формат ZIP и контракт manifest для этой возможности не меняются. Новые права доступа не добавляются; действующие route guards и ограничения редактирования снапшота сохраняются.

Совместимость сборок также учитывает `runtime.sharedStyles`: если разные выбранные версии объявляют один путь общей таблицы стилей с разными SHA-256, конфигурация снапшота отклоняется. Для обновления таких Vitasil-сборок выбирайте версии, собранные с одинаковым общим stylesheet; при этом каждый `manifest.key` всё равно закрепляет ровно одну версию. Ошибка возвращает путь stylesheet и требует согласовать версии явно.

### Критерии приёмки версионирования

1. Глобальный реестр визуально содержит одну строку на `key`; история версий доступна из этой строки и показывает точную версию, digest и manifest.
2. В составе снапшота каждая фича показана один раз. При добавлении предложена последняя SemVer-версия; доступен ручной выбор предыдущих версий.
3. Смена версии меняет все placement/source/binding ссылки на тот же ключ в рабочем снапшоте, но не родителя и не назначения веток; сохранение создаёт потомка и запускает существующую проверку совместимости контента.
4. В редакторе страницы версия закреплённой фичи не расходится между её размещениями; список версий сгруппирован под логической фичей.
5. Регистрация ZIP под уже существующей версией остаётся идемпотентной для того же digest и завершается конфликтом для другого digest. Новая сборка не обновляет ни один существующий снапшот автоматически.
6. Тесты покрывают SemVer-порядок, группировку сборок, выбор одной версии на ключ, сохранение родителя, точную регистрацию и неизменность назначения веток. Не добавляются API и миграции.

### Результат реализации версии редактора

- Глобальный реестр и состав снапшота группируют сборки по `manifest.key`; история раскрывается внутри логической фичи.
- Новая фича предварительно показывает старшую SemVer-сборку. Добавление или смена версии требуют явного действия; существующие версии не обновляются автоматически.
- Редактор страницы группирует сборки по ключу и ограничивает уже включённую фичу её закреплённой версией. `selectFeature()` переносит выбранную версию на ссылки размещений, коллекций и content bindings в новой рабочей конфигурации; исходный объект остаётся неизменным.
- Версия манифеста проверяется как SemVer, включая корректные prerelease/build identifiers. Backend отклоняет две версии одного ключа в одном снапшоте; никакие API или миграции не добавлены.
- Проверки: `pnpm typecheck`, `pnpm lint`, `pnpm test` (12 файлов, 58 тестов), `pnpm build` под Node 20; `go test ./features/sitebuilder`. Тесты покрывают SemVer-порядок, группировку, выбор старшей версии, ссылки placement/source/binding, неизменность родительской конфигурации, запрет смешения версий и валидные/невалидные SemVer identifiers.
- Проверка реальных артефактов: `sitebuilder.VerifyArchive` через read-only Go runner успешно провалидировал все 24 ZIP в `/Users/docup/Projects/vitasil/frontend/dist/feature-builds`; для каждого сохранена исходная пара `key/version`. Рабочие API, БД, хранилище и файлы Vitasil не менялись.
- Browser QA на disposable API: реальная Vitasil-сборка `catalog@1.0.9` появилась в одной раскрываемой записи вместе с `1.0.5`; до выбора снимок остался закреплён на `1.0.5`. Первая попытка обновить только `catalog` была отклонена, потому что его `core.css` отличается от `main-data@1.0.5`; UI/API показали ошибку общего stylesheet без создания снапшота. После выбора согласованной пары `catalog@1.0.9` + `main-data@1.0.9` редактор показал предупреждение о копировании одной опубликованной записи каждой фичи без преобразования и создал дочерний снапшот `#5`. В disposable PostgreSQL подтверждены закреплённые версии, неизменные parent `#1` (`1.0.5`), хеши опубликованного контента в родителе и ребёнке и неизменное назначение ветки на родитель (revision `2`). Рабочие Vitasil API, БД, хранилище и файлы не менялись.

## Единый manifest-контракт

Сборки объявляют один из существующих вариантов в `runtime`. Добавление новых ABI и изменение существующих runtime-контрактов не входит в эту спецификацию. Пример модуля:

```json
{
  "runtime": {
    "abi": "static-module-v1",
    "entry": "dist/feature.js",
    "styles": ["dist/feature.css"],
    "sharedStyles": ["dist/core.css"]
  }
}
```

Пример HTML-фрагмента:

```json
{
  "runtime": {
    "abi": "static-html-v1",
    "entry": "dist/feature.html",
    "styles": ["dist/feature.css"]
  }
}
```

Это варианты одного `manifest.json`, а не отдельные каталоги регистрации. Общие поля manifest и правила версионирования одинаковы. Для `static-module-v1` `entry` указывает на ESM ABI модуля; для `static-html-v1` — на HTML fragment. `styles` может быть пустым или отсутствовать. Engine API version и runtime ABI проверяются раздельно; неизвестная версия отклоняется при импорте, а не приводит к попытке угадать контракт.

Для runtime-вариантов нельзя одновременно задавать legacy `entry`, `styles` или `sharedStyles` в корне: это исключает неоднозначный ABI. `runtime.styles` содержит feature-scoped стили размещения. Необязательный `runtime.sharedStyles` перечисляет локальные CSS-ресурсы общей основы, одинаковые для версий выбранных сборок и подключаемые один раз; эта общая основа может задавать базовые глобальные стили. JavaScript entries имеют расширение `.js` или `.mjs`, HTML fragment — `.html` или `.htm`; статический HTML entry ограничен 2 MiB.

JavaScript assets разбираются ECMAScript parser перед проверкой import-графа. Статические `import` и `export … from`, а также dynamic `import()` ровно с одной строковой константой проходят проверку относительного локального пути, наличия файла и JS-расширения. Bare, внешние, отсутствующие и вычисляемые импорты отклоняются. Лексические конструкции JavaScript, включая regex literals с экранированным `/` и операторы деления, не должны ошибочно классифицироваться как import-пути.

Сборки без `runtime` используют существующий `legacy-react-v1` contract. Значение ABI не угадывается по содержимому архива и не переписывается при регистрации. Публичный Go document renderer оставляет feature assets и lifecycle текущему runtime.

## `static-module-v1`

Модуль реализует один framework-neutral lifecycle. На каждое размещение движок создаёт отдельный корневой DOM-элемент и вызывает экспорт `mount`:

```ts
export type FeatureContext = {
  placementId: string;
  featureKey: string;
  version: string;
  arguments: Record<string, unknown>;
  content: Record<string, unknown>;
  route: { path: string; params: Record<string, string>; search: string };
  systemAssets: { logoUrl?: string };
  collectionEndpoint?: string;
  detailLink?: { routeId: string; field: string; pathTemplate: string };
  sdk: FeatureSDK;
  signal: AbortSignal;
};

export type FeatureInstance = {
  ready: Promise<void>;
  update(next: FeatureContext): void | Promise<void>;
  unmount(): void | Promise<void>;
};

export function mount(root: HTMLElement, context: FeatureContext):
  FeatureInstance | Promise<FeatureInstance>;
```

`mount` создаёт UI в переданном root и возвращает handle. `ready` разрешается после первоначального содержимого и допустимых чтений, нужных для первого отображения; reject считается ошибкой фичи. На смену маршрута или контекста движок вызывает `update`; при удалении или замене размещения вызывает `unmount`, отменяет старый `signal` и освобождает подписки, timers и framework roots. Ошибка импорта, mount, update или ready изолируется границей одного размещения и не скрывает остальную страницу.

`FeatureContext` не содержит секретов, прав администратора, выбора ветки, произвольного доступа к серверной БД или приватным файлам. Все разрешённые операции с данными идут через версионируемый `FeatureSDK` из `engineApi`. Формы используют существующий SDK и публичный endpoint форм; обязательность полей и проверка на сервере сохраняются.

В текущем browser SDK доступны `listCollection(endpoint, { field?, value?, offset?, limit? })` и `submitForm(featureKey, data)`. Composition передаёт `collectionEndpoint` и `detailLink`, если они настроены. Это существующий SDK, а не новый серверный API.

Автор может использовать framework, если конечный ESM entry самой фичи реализует этот lifecycle. Framework и его клиентские зависимости включаются в ZIP; движок не предоставляет обязательную framework global-зависимость. Это правило описывает имеющийся `static-module-v1` контракт и не вводит дополнительный ABI или Engine-side адаптер.

## `static-html-v1`

`entry` — фрагмент разметки для одного placement, а не полный `<html>/<head>/<body>` документ. Фрагмент вставляется в root страницы движка без шаблонной подстановки. Он может содержать доступную семантическую разметку, обычные ссылки и локальные пути к включённым ресурсам.

HTML-вариант не исполняет inline script, event-handler атрибуты (`on*`), `javascript:` URL, загруженные скрипты или внешние executable зависимости. Он не принимает редакционные content values, не выполняет data binding и не реализует отправку формы. Если нужны данные по manifest schema, интерактивность или формы, используется `static-module-v1`. Полный HTML-документ, меняющий head, canonical, robots или оболочку сайта, не является фичей placement и остаётся ответственностью движка.

Go document rendering помещает fragment непосредственно в соответствующий ABI placement root, переписывает локальные `src`, `poster` и `srcset` на immutable asset URLs и подключает styles placement и темы. После успешного Chromium prerender runtime обновляет тот же placement root; отдельная дублирующая fallback-ветка не создаётся.

## CSS и ресурсы

Engine создаёт стабильную границу placement с `data-engine-feature` и `data-engine-placement`. CSS сборки должен быть ограничен этой границей и не менять `html`, `body`, соседние фичи или админку. Дизайн-токены движка доступны через наследуемые CSS custom properties; имена/значения токенов в админке по-прежнему не вводятся кодом.

Runtime-shell fallback для `html`/`body` (минимальная геометрия, системный шрифт, нейтральные цвет и фон) находится в низкоприоритетном CSS-слое `runtime-base`. `runtime.sharedStyles` подключаются после shell и могут задавать общую тему сайта, включая глобальные `html`/`body` цвета и фон; они должны перекрывать fallback при одинаковой специфичности. Сгенерированные движком токены загружаются после shared styles и остаются источником выбранных значений `--base-*`/`--feature-*`. Feature-scoped `runtime.styles` по-прежнему ограничены placement boundary и не могут изменять оболочку.

Проверка 7 ноября на реальном ZIP Vitasil `not-found@1.0.7` подтвердила cascade: Engine fallback допускает тёмный фон и цвет страницы из общей CSS фичи; цвет кнопки `text-primary-foreground` не блокируется общим reset-селектором ссылки. Предыдущий CSS содержал unlayered fallback оболочки и unlayered `a { color: inherit }`, из-за которых текущий 404 был нечитаемым.

Для новых runtime-вариантов загрузчик проверяет CSS selectors в `runtime.styles` на `data-engine-feature="<featureKey>"` либо `:where(...)` с этой границей. Имена `@keyframes`, layers, counter styles, registered properties и `@font-face` должны иметь префикс feature key. `@import` и внешние CSS resources не допускаются. `runtime.sharedStyles` предназначены только для явно разделяемой основы сборок и входят в digest/проверку архива, но не считаются feature-scoped стилями.

Ресурсы загружаются по same-origin URL, адресуемому digest сборки и относительному пути; cache policy остаётся immutable. Относительные CSS `url()` и ESM imports разрешаются внутри той же сборки. Внешние CDN зависимости и runtime-компиляция запрещены в ABI v1. HTML ссылки на внешние сайты допускаются, но встроенные ресурсы HTML/CSS должны находиться в ZIP или обращаться к разрешённым публичным файлам движка.

## Доверие и ограничения

Загрузка ZIP разрешена только существующей роли site manager. Регистрация сборки означает разрешение доверенному автору исполнять JavaScript в origin публичного сайта; static build не является sandbox. Такой код потенциально имеет доступ к DOM и публичным same-origin API, поэтому секреты, административные API и закрытые данные в feature context не передаются. Изоляция недоверенных сборок в iframe — отдельный продуктовый контракт и в ABI v1 не входит.

Сборка не должна выполнять побочные действия при `mount` или начальном рендере. В режиме пререндеринга worker изолирован Chromium-контекстом, разрешает только согласованные GET/HEAD публичных ресурсов и блокирует POST/PUT/PATCH/DELETE. `sdk.submitForm` в worker не отправляет заявку. В обычном браузере разрешённые действия выполняются через SDK с действующей серверной валидацией.

## Пререндеринг и SEO

Engine остаётся владельцем маршрутизации, статуса HTTP, head metadata, canonical, robots и sitemap. `static-html-v1` сразу даёт содержимую HTML-разметку. Для `static-module-v1` существующий browser worker открывает страницу в Chromium, загружает статическую сборку, ждёт `FeatureInstance.ready`, окончания публичных GET и quiet interval и сохраняет rendered HTML. Это browser prerender, не запуск server framework и не использование User-Agent для выбора содержимого.

Engine также владеет семантической оболочкой страницы: все placements со слотом `main` находятся внутри одного `<main>` после header и перед footer; placements `background`, `header` и `footer` остаются вне него. Фичи в `main` являются фрагментами содержимого и не должны создавать вложенный `<main>`; используйте `<article>`, `<section>` и заголовки подходящего уровня. Оболочка одинаково попадает в browser prerender и интерактивную страницу; manifest и ABI не меняются.

После загрузки страницы клиентский runtime запускает module ABI, пока prerendered копия остаётся видимой; после успешной готовности она заменяется интерактивным размещением. Пререндер с ошибкой сборки, несовпавшей ревизией или timeout не кешируется и не объявляется успешным. Браузерный JS должен дать тот же основной контент, что и HTML-фрагмент пререндера; формам сохраняется защита от повторной отправки.

Placement, содержащий намеренно отмеченный feature-элемент `[data-engine-runtime-overlay]`, монтируется сразу, даже если корень placement расположен ниже viewport. Это явная runtime-граница для viewport UI: она не зависит от того, успела ли таблица стилей применить `position: fixed` к моменту проверки геометрии. До завершения клиентской инициализации shell скрывает такие элементы только при `@media (scripting: enabled)`; без JavaScript пререндер остаётся видимым. Runtime снимает gate после завершения mount/update всех eager placements, поэтому сохранённое в браузере предпочтение успевает убрать пререндерный overlay до его показа.

## Права, ошибки и совместимость

Импорт ZIP защищён существующей авторизацией. Build immutable; snapshot pins точную версию/digest. Замена runtime или manifest требует новой feature version и проходит существующий compatibility preflight для контента. Повреждённый/небезопасный архив, отсутствующий entry/style/import, неизвестный ABI, нарушенный HTML fragment contract и превышение лимита отклоняются без частичной регистрации. Публичная ошибка одной фичи показывает fallback только её placement; worker сообщает безопасную категорию сбоя и не записывает содержимое или контекст пользователя.

Не вводить отдельный API, таблицу, редактор сборок или schema management. Сохраняются существующие `/api/v2/admin/feature-builds`, digest-хранилище, manifest registration/versioning, snapshot composition и `FeatureSDK`; меняется только поддерживаемая декларация и runtime adapter согласно этой спецификации.

## Критерии приёмки

1. Реальные Vitasil ZIP продолжают регистрироваться; актуальные версии (`background@1.0.6`, `catalog@1.0.9`, `catalog-detail@1.0.8`, `footer@1.0.4`, `form@1.0.9`, `header@1.0.6`, `hero@1.0.9`, `main-data@1.0.9`, `not-found@1.0.6`) прошли disposable integration. `hero@1.0.9` пререндерится через module ABI, `catalog-detail@1.0.8` проходит проверку landmark/H1 в Chromium, а `form@1.0.9` из Vue SFC выполняет реальную отправку через Engine SDK в Chromium. Рабочая Vitasil API/БД не изменяется.
2. Engine не содержит ветвлений по framework; `static-module-v1` проверен независимым vanilla ESM build. Любая framework-сборка может включить свой runtime в локальные ZIP-модули и реализовать тот же adapter.
3. `static-html-v1` фрагмент загружается, виден без JavaScript, не может менять head документа и отклоняет запрещённые inline handlers/scripts/unsafe URLs.
4. Невалидные ZIP: traversal, дубликаты, symlinks, отсутствующий entry/asset, bare или внешний import, неизвестный ABI и нес scoped CSS отклоняются с понятной ошибкой до регистрации.
5. Проверены два placements одного build, lifecycle `update`/`unmount`, смена маршрута, content, `listCollection`, валидная `submitForm` после keyboard activation и запрет отправки формы из prerender worker.
6. CSS upload принимается только с feature scope; Chromium проверяет локальный asset, вычисленные стили двух feature scopes с одинаковым классом и отсутствие меж-placement утечки. Ошибки `mount` и загрузки stylesheet показывают локальный `role=alert`, сохраняя работающий соседний placement.
7. Браузерная проверка текущего ZIP с `runtime.sharedStyles` подтверждает, что его глобальная тема переопределяет низкоприоритетные shell fallback цвета/фон; без shared theme сохраняется нейтральный fallback.
7. Chromium prerender содержит основной текст HTML/module фичи, не отправляет форму и отражает актуальные branch/snapshot/content revisions; worker fail не оставляет stale HTML.
8. Проверены клавиатурная отправка формы, семантические заголовки и alt-текст изображения в статическом HTML без JavaScript, локальные immutable URLs и отдельные runtime/resource errors. Ручная проверка скринридером и полная responsive/accessibility матрица реальных страниц Vitasil остаются отдельным QA сценарием.

## Осталось проверить

- Выполнить ручной screen-reader и responsive/accessibility audit публичных страниц на полном изолированном экземпляре Vitasil. Автоматизированный Chromium сценарий проверяет keyboard activation, accessible names/alt, no-JS rendering, CSS scope и локальную изоляцию runtime/resource errors.

## Результаты проверки

- После адаптации Vitasil на Node 20 прошли `npm --prefix frontend run typecheck`, `npm --prefix frontend run lint` и `FEATURE_BUILD_OUTPUT_DIR=/tmp/vitasil-static-feature-builds.ApgFsM npm --prefix frontend run build`. Девять актуальных ZIP прошли полный Go integration и Chromium для реальных hero/catalog/main-data; все ZIP скопированы в стандартный `frontend/dist/feature-builds/`, предыдущие ZIP сохранены. Предыдущий `hero-1.0.6.zip` отдельно прошёл Chromium с legacy ABI.
- `TEST_DATABASE_URL=… TEST_FEATURE_DIR=… go test -count=1 ./...` прошёл на disposable PostgreSQL со всеми DB-backed тестами и ZIP Vitasil; браузерные тесты, требующие Playwright environment variables, запускались отдельно. `go vet ./...` также прошёл.
- Из `frontend/` на Node 20 прошли `pnpm typecheck`, `pnpm lint`, `pnpm test` (55 тестов) и `pnpm build`.
- Отдельная Chromium-команда `go test ./cmd/integration -run '^(TestStatic.*|TestExternalVitasil.*|TestBranchBrowserPrerenderUpdatesAfterContentAndAssignment)$' -count=1 -v` прошла на disposable PostgreSQL. Она включает два одинаковых класса под разными CSS scopes, no-JS fallback, локальную изоляцию ошибки `mount` и отказа stylesheet, форму с keyboard activation, коллекционный SDK, route lifecycle, актуализацию prerender, регистрацию девяти ZIP Vitasil и prerender настоящего legacy hero.
- `go test ./pkg/runtime -run '^TestPrerenderWorkerRendersAndRejectsSideEffects$' -count=1 -v` прошёл с Chromium; fixture использует отдельный cache identity для query-зависимого небезопасного сценария, поэтому гарантированно запускает worker.
- `TEST_FEATURE_DIR=/Users/docup/Projects/vitasil/frontend/dist/feature-builds go test ./cmd/integration -run '^TestExternalVitasil' -count=1 -v` прошёл. Это проверяло ZIP и поведение в disposable test server, не в рабочем Vitasil.
- **Историческая запись, Vitasil до перехода на model-backed manifest v2.** `form@1.0.9` тогда использовала один SFC `ContactForm.vue`, а CMS-схема формы находилась в manifest. Этот контракт снят: актуальная `form@2.0.0` хранит копирайт в snapshot DataModel и отдельно объявляет обязательную схему отправки через `form.submissionSchema`. Старые результаты сборки и E2E относятся к прежнему контракту и не подтверждают новую модель; актуальное состояние Vitasil зафиксировано в [его спецификации dataInputs](../../vitasil/docs/SPEC-feature-data-inputs.md) и [QA отчёте движка](QA_REPORT.md).
- Повторная проверка текущей версии движка 6 ноября 2026 выявила, что lexer-based import scanning ошибочно отклонял regex literals из новых ZIP Vitasil. Проверка переведена на AST traversal существующего parser; regression покрывает `/\/+/g`, `\d`-класс, division, static import/export и запрещённые bare/external/dynamic вычисляемые пути. На актуальной временной сборке девяти ZIP прошли `go test -count=1 ./...`, `go vet ./...`, Vitasil `npm run lint`, `npm run typecheck` и `npm run build`. Browser prerender реального `hero@1.0.9` включён в полный Go-прогон. Рабочие API/БД/storage Vitasil не использовались.
- Дополнительный focused Chromium прогон 6 ноября на disposable PostgreSQL прошёл с актуальными hero и catalog-detail ZIP. Проверена содержательная prerendered разметка, landmark/H1 и повторный ответ из runtime cache. Наблюдаемое время одного cold/warm HTTP запроса: hero — 1.497 s / 10 ms при 3,245 bytes HTML, catalog-detail — 830 ms / 9 ms при 4,010 bytes HTML. Это единичные локальные замеры, cold включает запуск Chromium; они не заменяют Lighthouse/Core Web Vitals или сравнительный mobile/desktop профайл.

### Повторная проверка сборки текущих исходников Vitasil — 7 ноября 2026

Изменённые исходники Vitasil собраны read-only в production режиме. Девять source manifests совпали с каталогами и содержат `static-module-v1`: `background@1.0.7`, `catalog@1.0.11`, `catalog-detail@1.0.10`, `footer@1.0.5`, `form@1.0.10`, `header@1.0.8`, `hero@1.0.12`, `main-data@1.0.11`, `not-found@1.0.7`. `npm run typecheck`, `npm run lint` и production build завершились без warning/error. В output находилось ровно девять ZIP, по одному на каждый текущий manifest key/version.

Стандартный `sitebuilder.VerifyArchive`-based тест `TestExternalBackendBindingsVitasilArchiveCompatibility` прошёл 9/9: ZIP filename identity совпала с `manifest.json`, архивы прошли runtime/manifest/archive checks. Тест не создаёт Engine environment и не требует БД. ZIP взяты из новой временной output директории; стандартный `frontend/dist`, Vitasil API, БД, storage, volumes и исходники не менялись. После валидации каталог `/tmp/website-engine-vitasil-features-build-20261107-mBco0y` удалён.

Команды (Node `v20.19.4`, из Vitasil `frontend/` для npm-команд):

```sh
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' npm run typecheck
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' npm run lint
PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' FEATURE_BUILD_OUTPUT_DIR='/tmp/website-engine-vitasil-features-build-20261107-mBco0y' npm run build
```

```sh
TEST_FEATURE_DIR='/tmp/website-engine-vitasil-features-build-20261107-mBco0y' go test -count=1 -v ./cmd/integration -run '^TestExternalBackendBindingsVitasilArchiveCompatibility$'
```
