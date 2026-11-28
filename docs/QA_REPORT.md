# QA-отчёт

## Исходное состояние и границы

| Проверка | Evidence | Результат |
| --- | --- | --- |
| Статический sweep | `output/test-report-release-final-f4265fb.json`, base HEAD `f4265fb9`, рабочее дерево с локальными файлами | 89/89 гейтов прошли. Сводка записала 70/84 pass и 14 blocked. Runner переносил статусы из ledger, поэтому 70/84 включает исторические результаты. |
| Website / Router E2E | `output/e2e/e2e-20261127T231444Z-a5d914/`, base HEAD `f4265fb9`, отдельная копия checkout | `prepare-checkout.log` фиксирует совпадение копии с источником. `playwright.log` содержит 3/3 PASS — `E2E-PUB-001`, `E2E-LOC-001`, `E2E-RET-001`. Проверены публикация, выбор locale и штатный retirement. |
| Website и Core integration | `output/readiness-baseline-integration.json` и `output/readiness-orchestration-current.log` | Все 7 сценариев прошли в отдельных запусках на PostgreSQL/MinIO. `WEB-021/022` прошли с настоящими DataBus task pools, Core Coordinator, Router CAS и изолированными отказами. `WEB-021` использует контролируемый prerender result. Chromium отдельно проверяет `WEB-020`. |
| Подготовка media | `output/readiness-web019-schema-kind.log` | `WEB-019` проверил обе схемы, одинаковый путь и файл, конфликт общего ключа, отдельные immutable derivatives и сохранение значений. |
| Content delivery после рестарта | `TestContentPublicationDeliveryRestart` на disposable PostgreSQL/MinIO | Ранее подтвердил повтор workflow с теми же ID после потери ответа и рестарта Core, а также повторяемое подтверждение доставки. Автостарта full profile и конкурентной доставки этот тест не измеряет. |
| Core, concurrency, observability | Реестры и runbooks в `tests/qa/` | Исторически наблюдались scoped results, изоляция task classes и OTLP export. Новому checkout требуется свежий прогон интеграций. |

Статусы сценариев отбираются по [матрице приёмки](../tests/ACCEPTANCE-MATRIX.md).
Документированная доля ledger и свежая доля исполнения измеряются отдельно.
Готовность текущего checkout равна `pass / все согласованные сценарии`.
`blocked`, `not_run` и `fail` остаются в знаменателе. Гейты сборки,
линтеров и unit suite считаются отдельно от сценариев.

## Открытая приёмка

- Website и Content. Восстановление publication при старте и конкурентная доставка, media UI
  с реальным manifest в обоих `schema_kind`.
- Retirement. Рестарт между подготовкой job, изменением
  lifecycle страницы и Coordinator Start. Повтор после потери подтверждения.
- Forms и Loader. Live sessions, multipart, idempotency, restart, expiry и
  cleanup, CSV export и удаление submissions вместе с вложениями.
- Router, Core и Coordinator. Readiness, grant revocation, cache/proxy policy,
  конкуренция на PostgreSQL, retry и recovery.
- Admin, workers и observability. Сквозные браузерные операции на текущих
  контрактах, реальные ZIP и runtime, OTLP export и отказ Collector.

Рабочие пункты перечислены в [TODO](TODO-admin-snapshots-content-files.md).
