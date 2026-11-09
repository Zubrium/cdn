# Связи данных фич

Архивная спецификация снятого контракта `contentRelations` (снят с эксплуатации 7 ноября 2026 года). Она описывает прежнюю реализацию и не является действующей схемой, API или чеклистом приёмки.

Текущая модель описана в [SPEC-data-model.md](SPEC-data-model.md). ER-схема и связи сущностей принадлежат снапшоту. В `/site` typed `dataInputs` фич сопоставляются с сущностями и полями модели; в `/content` редактируются общие записи и выбор/порядок записей для размещений.

Старые `contentRelations`, `relationEnvelope`, overlay-данные и per-feature CMS endpoints новая система не читает, не записывает, не публикует и не конвертирует. Совместимость и миграция старых данных не входят в контракт; старые схемы/таблицы отсутствуют в чистой initial migration. Датированные результаты прежней приёмки сохранены в [QA_REPORT.md](QA_REPORT.md) исключительно как историческая запись. Текущий контракт находится в [SPEC-data-model.md](SPEC-data-model.md), [SPEC-branches-snapshots.md](SPEC-branches-snapshots.md) и [SPEC-content-editor-acceptance.md](SPEC-content-editor-acceptance.md).
