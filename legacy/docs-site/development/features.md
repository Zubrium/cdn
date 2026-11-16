# Разработка фич и manifest

Фича — готовый статический browser build с `manifest.json` в корне ZIP. Website Engine хранит сборки неизменяемо и не запускает авторский toolchain, серверный код или npm. Manifest format 2 объявляет идентичность сборки, аргументы размещения, типизированные `dataInputs`, публичную схему отправки формы, токены и runtime ABI. CMS-записи и их формы принадлежат snapshot-модели, не manifest.

## Рабочий процесс

1. Выберите стабильный `key` и SemVer `version` для новой сборки.
2. Определите слоты, аргументы фичи для `/site` и ожидаемые model data inputs. CMS-поля и подписи создаются администратором в модели снапшота.
3. Реализуйте адаптер одного из поддерживаемых runtime ABI.
4. Соберите браузерные файлы локально и проверьте, что все imports, стили и ресурсы находятся внутри build output.
5. Заполните `manifest.json` по образцу ниже.
6. Упакуйте ZIP так, чтобы `manifest.json` был в корне, и загрузите его в **Снапшоты → Реестр фичей**.
7. Проверьте фичу в изолированном снапшоте: размещение, настройки, контент, медиа и публичный runtime.
8. Для изменения уже зарегистрированной версии увеличьте `version`; существующая пара `key + version` с другим digest не заменяется.

## Минимальный manifest

Следующий пример — одна фича с размещением `main`, настройкой сайта, одним model-backed входом и одним токеном:

```json
{
  "formatVersion": 2,
  "key": "page-intro",
  "name": "Вступительный блок",
  "category": "Содержимое",
  "version": "1.0.0",
  "engineApi": "2",
  "runtime": {
    "abi": "static-module-v1",
    "entry": "dist/entry.js",
    "styles": ["dist/feature.css"]
  },
  "slots": ["main"],
  "arguments": {
    "type": "object",
    "properties": {
      "alignment": {
        "type": "string",
        "title": "Выравнивание",
        "enum": ["left", "center"],
        "default": "left"
      }
    },
    "additionalProperties": false
  },
  "dataInputs": [
    {
      "key": "intro",
      "kind": "record",
      "fields": [
        {"path": "/title", "type": "text"},
        {"path": "/body", "type": "longText"}
      ]
    }
  ],
  "designTokens": [
    {
      "key": "color-title",
      "label": "Цвет заголовка",
      "group": "Цвета",
      "kind": "color",
      "default": {"color": {"hex": "#274C59", "opacity": 100}}
    }
  ]
}
```

## Поля верхнего уровня

| Поле | Назначение и ограничения |
| --- | --- |
| `formatVersion` | Сейчас `2`. Формат 1 больше не принимается. |
| `key` | Стабильный идентификатор: строчная латиница, цифры и дефисы; начинается с буквы. Максимум 100 символов. |
| `name` | Имя для реестра и редактора, от 1 до 200 символов. |
| `category` | Необязательная группа в интерфейсе, максимум 100 символов. |
| `version` | SemVer. Одинаковая пара `key + version` должна всегда означать один и тот же ZIP digest. |
| `engineApi` | Версия Feature SDK/runtime API, которую использует фича. Текущий код принимает `1` и `2`; актуальное значение — `2`. |
| `runtime` | Новый вариант ABI: `static-module-v1` или `static-html-v1`. Если его нет, используется legacy React ABI с корневыми `entry`, `styles`, `sharedStyles`. |
| `slots` | Один или несколько уникальных слотов: `background`, `header`, `main`, `footer`. |
| `arguments` | Обязательная JSON Schema корневого типа `object`; настройки экземпляра/размещения, доступные редактору сайта. |
| `dataInputs` | Обязательный массив. Объявляет типизированные ports, которые фича читает из `context.content`; это технический runtime contract, не CMS-форма. Используйте `[]`, если модельные записи не нужны. |
| `form` | Необязательный объект. `submissionSchema` строго проверяет публичные отправки; он не описывает CMS-контент формы. |
| `designTokens` | Необязательные графические настройки этой фичи для дизайн-системы снапшота. |

Ключи JSON Schema properties должны начинаться с латинской буквы и содержать латинские буквы, цифры и `_`; максимум 64 символа. Схемы должны быть самодостаточны: внешние `$ref` не подходят для загруженного ZIP.

Сводка JSON-полей, Go-типов, typed inputs и полей модели генерируется в [справочнике manifest](../reference/manifest).

## Аргументы и модельные данные — разные входы

`arguments` задаёт значения конфигурации фичи: варианты поведения, ссылки, выравнивание и другие параметры размещения. Все они редактируются только в `/site`. Media-аргумент выбирает уже загруженный файл из медиатеки; загрузка выполняется в `/media`.

`dataInputs` — список ports в `context.content`. Port имеет `key` и `kind`: `scalar`, `record` или `collection`. Scalar объявляет один `type`; record и collection объявляют `fields` с JSON Pointer `path` и типом. Это типизированная форма runtime-входа, а не схема редактора записей. В `/site` администратор связывает input с ER-моделью снапшота и настраивает все аргументы фичи. В `/content` редактируются значения общих записей и порядок записей, выбранных для размещения. Подписи, перечисления и формы записи принадлежат модели снапшота. Все CMS-значения необязательны.

Пример коллекционного входа:

```json
{"key":"products","kind":"collection","fields":[{"path":"/title","type":"text"},{"path":"/image","type":"media"}]}
```

Изменение формата или shape model inputs требует `formatVersion: 2` и новой major-версии feature build. Формат 1 не поддерживается.

`form.submissionSchema` остаётся отдельной JSON Schema пользовательского ввода. Её `required` и серверные ограничения относятся к отправке формы, а не к редактированию CMS-записей.

Для media-поля можно указать typed profile обработки прямо на input или поле `dataInputs`:

```json
{
  "key": "products",
  "kind": "collection",
  "fields": [
    {"path": "/title", "type": "text"},
    {
      "path": "/image",
      "type": "media",
      "profile": {
    "accept": "raster-image",
    "renditions": [
      {"name": "card", "width": 960, "height": 640, "fit": "cover"}
    ]
      }
    }
  ]
}
```

Profile задаёт допустимую категорию и варианты обработки. `/content` только выбирает существующий разрешённый файл; загрузка остаётся в разделе **Медиа**. Подписи и подсказки model fields задаются в ER-редакторе сайта, а не в manifest.

## Опишите токены фичи

Каждый элемент `designTokens` содержит:

- `key` — уникальный в manifest ключ `a-z`, цифры и дефисы;
- `label` и `group` — видимые русские подписи;
- `kind` — `color`, `length`, `number`, `font` или `shadow`;
- `default` — типизированное значение, соответствующее `kind`.

Примеры `default`:

```json
{"kind":"color","default":{"color":{"hex":"#274C59","opacity":100}}}
{"kind":"length","default":{"number":24,"unit":"px"}}
{"kind":"number","default":{"number":1.5}}
{"kind":"font","default":{"font":"system-ui"}}
{"kind":"shadow","default":{"shadow":{"x":0,"y":4,"blur":16,"spread":0,"color":{"hex":"#000000","opacity":12},"inset":false}}}
```

Для единиц длины поддерживаются `px`, `rem`, `em`, `%`, `vw`, `vh`, `svh` и `ch`. Uploaded font family назначается администратором на уровне снапшота и не задаётся произвольным значением автора в manifest.

Редактор показывает токены только если фича включена в выбранный снапшот. Одинаковый feature token общий для всех её размещений в этом снапшоте. Пользователь может связать его с базовым токеном графически; код значения остаётся в manifest только для определения типа и начального значения.

## Выберите runtime ABI

### `static-module-v1`

Используйте, когда фиче нужны контент, маршруты, collection SDK, формы или интерактивность. Entry — собранный ESM-модуль, который экспортирует `mount(root, context)` и lifecycle `ready`, `update`, `unmount`.

```js
export function mount(root, context) {
  const heading = document.createElement('h1')
  root.append(heading)

  const render = (next) => {
    heading.textContent = String(next.content.title ?? '')
  }
  render(context)

  return {
    ready: Promise.resolve(),
    update(next) { render(next) },
    unmount() { root.replaceChildren() },
  }
}
```

Модуль получает только публичный `FeatureContext` и SDK. В нём нет ветки, bearer-токена админки, закрытых файлов или доступа к серверной БД. Асинхронный компонент должен разрешить `ready` после готовности первого отображения; при `update` обновляйте данные, при `unmount` освобождайте подписки и framework root. Полный ABI описан в [Runtime reference](../reference/feature-runtime).

### `static-html-v1`

Используйте для неизменяемого фрагмента, который не зависит от редакционного контента. Entry — `.html` или `.htm` fragment, не полный документ. Inline scripts, event handlers, `style`-атрибуты, шаблонные выражения и опасные URL запрещены. Для динамики, форм и SDK используйте модуль.

### CSS и сборочные зависимости

Все импорты JavaScript должны быть статическими относительными путями к файлам в архиве; вычисляемые и bare imports, CDN и внешние runtime dependencies не поддерживаются. Положите клиентский framework и его зависимости в сборку. У движка нет обязательной глобальной React-зависимости для `static-module-v1`.

Стили ограничьте границей фичи:

```css
[data-engine-feature="page-intro"] .intro-title {
  color: var(--feature-page-intro-color-title);
}
```

Не используйте глобальные селекторы, CSS `@import` и внешние `url()`. Имена `@keyframes`, layers, counter styles, зарегистрированные свойства и `@font-face` должны быть уникальными для фичи. `runtime.sharedStyles` предназначен для общей локальной основы совместимых сборок и загружается движком один раз.

## Сформируйте ZIP

Результат должен иметь такую структуру:

```text
feature.zip
├── manifest.json
└── dist/
    ├── entry.js
    ├── feature.css
    └── images/
        └── illustration.webp
```

Пути в manifest — относительные от корня ZIP. Архивируйте сам manifest и выход сборки, а не каталог проекта с `node_modules`:

```sh
cd build-output
zip -r ../page-intro-1.0.0.zip manifest.json dist
unzip -l ../page-intro-1.0.0.zip
```

Проверьте, что каждый `entry`, CSS, import и локальный media resource существует, ZIP не содержит симлинков/дублирующихся путей и `manifest.json` расположен на верхнем уровне.

## Загрузите и проверьте

1. Войдите в админку как администратор или модератор.
2. Откройте **Снапшоты → Реестр фичей** и загрузите ZIP.
3. Исправьте ошибку импорта по сообщению API. Ошибка не должна создавать частично зарегистрированную сборку.
4. Добавьте точную версию фичи в дочерний снапшот и создайте размещение в редакторе сайта.
5. В `/site` настройте аргументы и свяжите typed inputs с сущностями/полями модели; в `/content` заполните и опубликуйте записи.
6. Если есть media или форма, проверьте разрешённый media profile, `form.submissionSchema` и реальную отправку через SDK на disposable-среде.
7. Проверьте исходный HTML, страницу с JavaScript и поведение при ошибке/тайм-ауте.

Загрузка не включает фичу в снапшот и не изменяет назначения веток. Снапшот фиксирует одну версию каждого `key`; новая сборка требует явного выбора.

## Частые ошибки

| Симптом | Причина и действие |
| --- | --- |
| ZIP не найден/не открывается | Положите `manifest.json` в корень и проверьте состав `unzip -l`. |
| `key` или `version` отклонены | Используйте lowercase key и SemVer. Для изменённой сборки поднимите версию. |
| Неизвестный ABI | Проверьте `runtime.abi` и `engineApi`; не смешивайте legacy root entry/style с `runtime`. |
| Entry или CSS не найден | Исправьте относительный путь от корня ZIP. |
| Импорт модуля отклонён | Уберите CDN/bare/dynamic computed import, включите зависимости в браузерный bundle. |
| CSS не проходит проверку | Ограничьте селекторы границей feature key, удалите внешние ресурсы и `@import`. |
| Manifest не принят | Проверьте `formatVersion: 2`, обязательный `dataInputs`, форму каждой typed port и отсутствие старых CMS-полей `content`. |
| Токен не принят | Сопоставьте `kind` и структуру `default`; используйте поддерживаемые единицы. |
| Значение не видно на сайте | Проверьте, что фича есть в назначенном снапшоте и данные опубликованы в нём. |

## Источники контракта

Поля manifest и валидаторы: `features/sitebuilder/manifest.go`, `features/sitebuilder/data_inputs.go`, `features/sitebuilder/data_model.go`, `features/sitebuilder/design_system_structured.go`, `features/media/profile.go`. Публичный ABI runtime: `pkg/runtime/scripts`; Go document templates: `pkg/runtime/templates`. Feature Lab отдельно проверяет исходные feature-контракты. Автоматические архивные проверки: `features/sitebuilder/*_test.go`. Изменения контракта должны совпадать с этими источниками и их тестами.
