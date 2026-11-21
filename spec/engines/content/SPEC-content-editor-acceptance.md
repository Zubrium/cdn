# Приёмка редактора model-backed контента

Целевой контракт плоской модели и записей определён в [SPEC-data-model.md](../website/SPEC-data-model.md). `/site` редактирует структуру модели и bindings; `/content` владеет record values, drafts, publication и редакционным выбором/порядком записей.

## Проверяемые сценарии

- Формы records строятся по model schema выбранного сайта. Проверяются field types, constraints, uniqueness, cardinality, media readiness и корректные typed bindings.
- Model entities поддерживают cardinality `many` и `singleton`. Новый singleton получает одну пустую draft-запись без автоматической публикации. Очистка сохраняет record ID; сервер отклоняет изменения cardinality, теряющие существующие records.
- Singleton `record`/`scalar` bindings разрешаются автоматически; `collection` остаётся массивом из нуля или одной записи. Для `many` проверяются ordered selections и стабильные record IDs.
- System-wide records и публикация отделены от page-snapshot-scoped selections/order. Snapshot fork сохраняет целостность данных по Website/Content контрактам и не меняет Router route targets.
- Draft save использует optimistic revision. Publication выполняется отдельно, проверяет schema/media readiness и не публикует частичное состояние при validation error, stale revision или конфликте.
- History показывает публикации; restore создаёт draft и не публикует автоматически. Draft-only данные не попадают в public runtime.
- Удаление или изменение модели не теряет records скрыто. Очистка media references и удаление связанных memberships проверяются согласно Core/Forms contracts.
- API authorization проверяет direct group levels и отдельное право управления администраторами; проверки выполняются на каждом запросе. При отказе нет частичного изменения.
- Browser acceptance проверяет доступные controls, destructive confirmation, unsaved-change protection, keyboard behavior и viewport widths 360, 390, 768 и 1280 CSS px.
- Independent synthetic features проверяют catalog, detail с уникальным route lookup и ordered collection; отдельные disposable PostgreSQL/S3 сценарии проверяют persistence, recovery и media delivery.
- Реальные feature placements проверяются в изолированной disposable среде. Не использовать рабочие API, credentials, базы, buckets или файлы.

## Статус

Эти критерии являются требованиями, а не заявлением о прохождении. Результаты, команды и ограничения проверок записываются в [QA_REPORT.md](../../../docs/QA_REPORT.md); незакрытые работы находятся в [TODO](../../../docs/TODO-admin-snapshots-content-files.md). Полная Website приёмка остаётся открытой до выполнения применимых сценариев и фиксации evidence.
