# Универсальный ABI статических фич

## Целевой ABI

Target ABI называется `static-lifecycle` и указывается в `manifest.json` как `runtime.abi`. `runtime.entry` указывает на локальный ESM entry; `runtime.styles` и `runtime.sharedStyles` задают CSS-ресурсы сборки. Runtime принимает только этот ABI.

Entry экспортирует `render`, `hydrate`, `update` и `dispose`:

```ts
export type FeatureContext = Readonly<{
    placementId: string;
    featureKey: string;
    version: string;
    arguments: Readonly<Record<string, unknown>>;
    content: Readonly<Record<string, unknown>>;
    route: Readonly<{ path: string; params: Readonly<Record<string, string>>; search: string }>;
    systemAssets: Readonly<{ logoUrl?: string }>;
    collectionEndpoint?: string;
    detailLink?: Readonly<{ routeId: string; field: string; pathTemplate: string }>;
    sdk: FeatureSDK;
    signal: AbortSignal;
}>;

export type FeatureModule = {
    render(root: HTMLElement, context: FeatureContext): void | Promise<void>;
    hydrate(root: HTMLElement, context: FeatureContext): void | Promise<void>;
    update(root: HTMLElement, context: FeatureContext): void | Promise<void>;
    dispose(root: HTMLElement): void | Promise<void>;
};
```

`render` получает пустой lifecycle root и создаёт начальную разметку. `hydrate` получает уже сформированную Website/Prerender разметку и подключает интерактивное состояние, не заменяя root. `update` применяет новый context к тому же placement; `dispose` освобождает принадлежащие feature ресурсы. Завершение Promise `render`/`hydrate` означает готовность первоначального содержимого и необходимых для первого отображения разрешённых чтений; отказ изолируется в placement.

На одном root выполняется не более одной lifecycle-операции одновременно. При новом `update` runtime отменяет signal прежнего контекста, ждёт завершения текущей операции и применяет только самый новый ожидающий context. Замена placement отменяет текущий context, ожидает текущую операцию в пределах настроенного lifecycle timeout, вызывает `dispose` и только затем удаляет root. Ошибка или timeout одного placement не блокирует соседние. Feature прекращает работу по отменённому signal и не мутирует root после завершения операции.

Context содержит только публичные проекции выбранной страницы и system-wide published Content, плюс scoped SDK; в нём нет секретов, credentials, приватных Core file data, произвольных callbacks или выбора engine/module namespace. Страница не задаёт route: runtime получает текущие `path` и `params` от Router как request projection.

Единый аргумент-маршрут для контента, форм и внешних бэкендов объявляется в manifest как обычный строковый аргумент с `format: "route"`; backend-валидация форматов аргументов не отвергает это значение. Admin разрешает выбранный маршрут в итоговую строку до публикации snapshot, а runtime получает только эту строку: объект binding во внешний контракт фичи не передаётся.

Feature dependencies разрешаются Website по manifest SemVer ranges до готовности page snapshot. Dependency graph конечен, не содержит циклов и конфликтующих диапазонов; точные provider version, package digest и требуемая версия public runtime API фиксируются в immutable lock. Feature импортирует только exports объявленного versioned public API.

Feature manifest объявляет сетевые возможности: точные `https` origins и типизированные контракты запросов/ответов. Runtime строит browser `connect-src` только из same-origin и exact origins manifest-ов закреплённых feature builds в dependency lock; wildcard, credentials и произвольные origins запрещены. Значение path может поступать из опубликованной модели Content только как относительный путь под одним из объявленных origins; вводимые значения не могут менять origin.

Запросы и декодирование ответов выполняются browser-кодом фичи. Они могут использовать объявленные typed inputs, включая значения опубликованных Content records; feature code отвечает за сериализацию, проверку ответа и UI-состояния. `/content` редактирует значения записей; `/site` связывает поля модели с объявленными `dataInputs`, но не регистрирует origins и не настраивает path/query/body/header bindings. Manifest описывает контракт запроса и его модель данных; код фичи выполняет его.

Фича может получать сетевые функции через versioned exports явно объявленной provider-фичи, например `api`; обращения к произвольным фичам, их внутренним файлам, ambient globals или незафиксированному общему store запрещены. Provider может инкапсулировать запросы и framework-specific state, но runtime не требует Pinia, Vue или общего mutable store. Публичные методы provider принимают `AbortSignal` вызывающего placement и не возвращают результаты после отмены.

В manifest, аргументах и опубликованном Content запрещены secrets и credentials. Внешний вызов выполняется напрямую из браузера, зависит от CORS backend и не проходит через Website. Публичный runtime и prerender worker никогда не получают credentials. `context.signal` распространяется на запросы placement и вызванные им provider methods; отменённый запрос не должен менять DOM/state или блокировать соседние placements. При невалидном ответе feature показывает контролируемое состояние; внешний ответ не сохраняется как редакционный Content.

Prerender worker не имеет внешней сети и не выполняет browser-only API requests. Его HTML и cache identity не зависят от ответов, получаемых только после browser hydration. Значения Content для начальной серверной разметки учитываются обычными model/content revisions. Включение внешнего API-ответа в prerender требует отдельного target-контракта.

Design tokens — часть feature-provider manifest, namespaced по `feature_id`; только provider объявляет token schema и value. Consumers и Admin не переопределяют их. Общей базовой дизайн-системы, системных tokens и пользовательских overrides нет.

System-wide `dataModel` and records are shared across the single logical system; translated Content records are separate locale-specific records linked by `translation_id`. Page snapshots contain placements and typed bindings to that model. Prerender receives the selected locale, locale-catalog revision, immutable trusted projections and includes all revisions/digests in build identity. A publication rebuilds only affected locale targets according to [SPEC-website-engine.md](../SPEC-website-engine.md) and [SPEC-router-engine.md](../../router/SPEC-router-engine.md).

## Media declarations

A media field declares `x-media` in its pinned feature manifest. Supported profiles are `accept: image | raster-image | video | file`; optional `renditions` are allowed only for `raster-image`, with unique names, bounded positive `width`/`height` and `fit: contain | cover`. A `video` profile instead declares bounded `maxWidth`/`maxHeight` and a `poster` rendition. Unknown properties, invalid combinations and unbounded rendition counts are rejected before the feature version becomes ready. Profiles contain no command, executable, path, URL, filter expression or resource limit.

The editor submits the original Core file ID and its manifest-bound placement/field identity; Website resolves profile from the snapshot's exact feature version and rejects client-supplied profile overrides. The editor waits until every declared output is ready before replacing the field's previous value. On processing error or cancellation the previous value remains unchanged. The original file remains the stored Content value and is never replaced by a derivative; prerender resolves only outputs matching canonical profile digest and processor version. Derivatives are immutable Core files in the original's FileSet memberships and inherit its current access groups; membership never grants access. See [SPEC-workers.md](../../../workers/SPEC-workers.md) for worker retry and resource-isolation guarantees.

ZIP проверяются на build/export boundary. Импортёр не угадывает и не подменяет ABI. Website build создаёт HTML для initial paint; browser runtime вызывает `hydrate` поверх готовой разметки. UI-kit — обычная feature-provider, используемая через публичный runtime API и dependency lock; Admin fallback остаётся отдельной встроенной возможностью и не участвует в provider rollout страниц.

## Проверяемые требования

- Проверить, что неизвестные ABI и некорректные/неполные lifecycle exports отклоняются до импорта сборки.
- Проверить `render` и `hydrate` на initial paint, сериализацию публичного context и изоляцию ошибок одного placement.
- Проверить конкурентные `update`, cancellation, timeout, dispose и замену placement без мутации DOM после отмены.
- Проверить фиксирование dependency version/digest/API version и отказ на циклах, неразрешённых диапазонах и конфликтующих provider versions.
- Проверить строгую проверку manifest origins, CSP `connect-src`, typed request/response contract и отсутствие credentials в публичных payloads.
- Проверить, что prerender не имеет внешней сети, а публикация system-wide Content инициирует rebuild с сохранением предыдущего готового artifact при сбое.
- Проверять поведение на применимых реальных features в disposable среде; не направлять QA-запросы на рабочие API или хранилища.
