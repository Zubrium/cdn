# QA-отчёт

## Исходное состояние и границы

| Проверка | Evidence | Результат |
| --- | --- | --- |
| Статический sweep | `output/test-report-release-final-f4265fb.json`, base HEAD `f4265fb9`, рабочее дерево с локальными файлами | 89/89 гейтов прошли. Сводка записала 70/84 pass и 14 blocked. Runner переносил статусы из ledger, поэтому 70/84 включает исторические результаты. |
| Website / Router E2E | `output/e2e/e2e-20261127T231444Z-a5d914/`, base HEAD `f4265fb9`, отдельная копия checkout | `prepare-checkout.log` фиксирует совпадение копии с источником. `playwright.log` содержит 3/3 PASS — `E2E-PUB-001`, `E2E-LOC-001`, `E2E-RET-001`. Проверены публикация, выбор locale и штатный retirement. |
| Website integration | Реестры и `tests/qa/run-website-red.sh integration` | `WEB-012`, `WEB-018..020` ранее прошли на disposable PostgreSQL/S3. `WEB-021` и `WEB-022` ожидают адаптации fixtures к текущей композиции. |
| Content delivery после рестарта | `TestContentPublicationDeliveryRestart` на disposable PostgreSQL/MinIO | Ранее подтвердил повтор workflow с теми же ID после потери ответа и рестарта Core, а также повторяемое подтверждение доставки. Автостарта full profile и конкурентной доставки этот тест не измеряет. |
| Core, concurrency, observability | Реестры и runbooks в `tests/qa/` | Исторически наблюдались scoped results, изоляция task classes и OTLP export. Новому checkout требуется свежий прогон интеграций. |

Статусы сценариев отбираются по [матрице приёмки](../tests/ACCEPTANCE-MATRIX.md).
Документированная доля ledger и свежая доля исполнения измеряются отдельно.
Готовность текущего checkout равна `pass / все согласованные сценарии`.
`blocked`, `not_run` и `fail` остаются в знаменателе. Гейты сборки,
линтеров и unit suite считаются отдельно от сценариев.

## Открытая приёмка

- Website и Content. Publication с частичными отказами, CAS и повтором только
  неуспешных целей, восстановление при старте и конкурентная доставка, media UI
  с реальным manifest в обоих `schema_kind`.
- Retirement. Частичный withdrawal, рестарт между подготовкой job, изменением
  lifecycle страницы и Coordinator Start. Повтор после потери подтверждения.
- Forms и Loader. Live sessions, multipart, idempotency, restart, expiry и
  cleanup, CSV export и удаление submissions вместе с вложениями.
- Router, Core и Coordinator. Readiness, grant revocation, cache/proxy policy,
  конкуренция на PostgreSQL, retry и recovery.
- Admin, workers и observability. Сквозные браузерные операции на текущих
  контрактах, реальные ZIP и runtime, OTLP export и отказ Collector.

Рабочие пункты перечислены в [TODO](TODO-admin-snapshots-content-files.md).
