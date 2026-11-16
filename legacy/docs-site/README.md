# Website Engine documentation

Русскоязычная документация именно Website Engine. VitePress source находится в этой папке; внутренние TODO/specs остаются в `../docs/` и не публикуются напрямую.

```sh
npm ci
npm run docs:dev
```

Справочники из исходников: `npm run docs:generate`. Проверка актуальности: `npm run docs:check-generated`. Production build и Playwright QA: `npm run docs:build`, затем `npm run docs:qa`. Снимки интерфейса обновляются только из изолированной среды через `npm run docs:screenshots`.
