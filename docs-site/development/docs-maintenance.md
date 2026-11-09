# Поддержка документации

VitePress хранится в `docs-site/`; рабочие спецификации и TODO в корневой `docs/` остаются внутренними материалами и не публикуются напрямую.

## Команды

Из папки `docs-site/`:

```sh
npm ci
npm run docs:dev
npm run docs:generate
npm run docs:check-generated
npm run docs:build
npm run docs:qa
```

Реестр HTTP-маршрутов строится из Go ServeMux-регистраций, таблицы переменных — из `.env.example`, runtime ABI guide — из `docs/FEATURE-RUNTIME-AUTHORING.md`. Не правьте сгенерированные `reference/api.md`, `reference/configuration.md` и `reference/feature-runtime.md` вручную.

## Обновить снимки интерфейса

Используйте отдельный изолированный QA Engine без реальных пользовательских данных:

```sh
DOCS_ADMIN_ORIGIN=http://127.0.0.1:38410 \
DOCS_UNASSIGNED_BRANCH_ORIGIN=http://127.0.0.1:38412 \
npm run docs:screenshots
```

Скрипт открывает только страницу входа и публичный listener без назначенного снапшота, сохраняет кадры в `public/assets` и не отправляет формы и API mutations. Не указывайте production endpoint или окружение с пользовательскими данными.

## Перед отправкой изменений

- перегенерируйте справочники и проверьте `docs:check-generated`;
- соберите сайт;
- выполните Playwright QA для навигации, Mermaid, локального поиска, тем и размеров экрана;
- проверьте, что все описанные возможности существуют в маршрутах, обработчиках и интерфейсе;
- помечайте незавершённые планы как план, а не как доступную функцию.
