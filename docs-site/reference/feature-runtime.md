<!-- Generated from docs/FEATURE-RUNTIME-AUTHORING.md. Do not edit here; update the source document. -->

# Руководство автора статической фичи

Фича загружается как ZIP готовой browser-сборки. Website Engine не запускает npm, не компилирует исходники и не добавляет framework за автора.

## ZIP и manifest

В корне ZIP находятся `manifest.json` и только готовые файлы, на которые он ссылается:

```text
manifest.json
dist/entry.js
dist/feature.css
dist/images/card.webp
```

Общие поля `manifest.json` остаются прежними. Для browser-модуля добавляется:

```json
"runtime": {
  "abi": "static-module-v1",
  "entry": "dist/entry.js",
  "styles": ["dist/feature.css"],
  "sharedStyles": ["dist/core.css"]
}
```

Для статической разметки:

```json
"runtime": {
  "abi": "static-html-v1",
  "entry": "dist/card.html",
  "styles": ["dist/feature.css"]
}
```

Не задавайте одновременно legacy `entry/styles/sharedStyles` в корне и новый `runtime`. `runtime.styles` — scoped стили фичи; необязательный `runtime.sharedStyles` — общая база для нескольких сборок одного сайта, одинаковая по digest и подключаемая движком один раз. Paths должны быть относительными к ZIP. Все ESM imports, CSS resources и HTML media files должны быть локальными файлами того же ZIP; bare imports и CDN runtime-зависимости отклоняются. Изменение runtime ABI требует новой версии feature build.

## Module ABI

Движок создаёт отдельный `HTMLElement` для каждого placement. Entry экспортирует `mount` и возвращает полный lifecycle:

```js
export function mount(root, context) {
  const heading = document.createElement("h1");
  root.appendChild(heading);

  const render = (next) => {
    heading.textContent = String(next.content.title ?? "");
  };
  render(context);

  return {
    ready: Promise.resolve(),
    update(next) {
      render(next);
    },
    unmount() {
      root.replaceChildren();
    },
  };
}
```

`ready` завершается после первой готовой разметки и чтений данных для отображения. `update` вызывается при изменении arguments, content или route. `unmount` должен удалить обработчики, подписки и framework root. Учитывайте `context.signal`: он отменяется при смене контекста и размонтировании.

Engine размещает все фичи слота `main` внутри одного page-level `<main>` landmark. Не создавайте в feature view второй `<main>`; используйте `<article>`, `<section>` и заголовки подходящего уровня. `background`, `header` и `footer` остаются вне этого контейнера.

`context` содержит `placementId`, `featureKey`, `version`, `arguments`, `content`, `route`, `systemAssets`, опциональные `collectionEndpoint`/`detailLink`, `sdk` и `signal`. Он не содержит секретов, прав администратора, ветки или приватных файлов.

В `sdk` доступны:

- `listCollection(endpoint, { field?, value?, offset?, limit? })` — публичный список записей; результат содержит `items`, `total` и `nextOffset`.
- `submitForm(featureKey, data)` — существующая публичная отправка формы с серверной проверкой schema.

Для React, Vue, Svelte, Angular или другого framework создайте адаптер, который вызывает его `mount/render`, обновляет framework state при `update` и уничтожает root в `unmount`. Упакуйте клиентские зависимости в локальные ESM-файлы. У движка нет специальных веток для отдельных framework и нет обязательной React global-зависимости для `static-module-v1`.

CSS должен быть scoped под `[data-engine-feature="<featureKey>"]` или `:where(...)` с такой границей. Уникально именуйте keyframes, layers, counter styles, registered properties и `@font-face` families. CSS `@import`, внешние `url()` и глобальные селекторы не проходят регистрацию. Используйте CSS custom properties темы движка вместо жёстких значений, где они подходят.

## HTML ABI

`static-html-v1` принимает один небольшой HTML fragment, не целый документ. Inline scripts, `on*` handlers, `style` attributes, формы, шаблонные выражения и опасные URLs запрещены. Редакционные значения схемы в HTML не подставляются; для динамического содержимого выбирайте `static-module-v1`.

Разрешённые локальные `src`, `poster`, `srcset` переписываются на immutable URLs сборки. Обычная server response содержит fragment в `noscript` fallback. При выключенном JavaScript он остаётся доступным, а после успешного browser prerender fallback убирается вместе с дублирующей разметкой.

## Сборка и загрузка

1. Соберите framework в browser ESM и обычные static assets; серверный entrypoint или команда сборки в ZIP не нужны.
2. Запишите правильный runtime ABI в `manifest.json`.
3. Проверьте, что все imports и ресурсы находятся внутри архива, а CSS ограничен feature boundary.
4. Упакуйте manifest и build output в ZIP и загрузите его через существующий реестр сборок Website Engine.

Загруженный JavaScript является доверенным и исполняется с origin сайта; это не sandbox. Не помещайте в feature bundle секреты и не используйте административные API.
