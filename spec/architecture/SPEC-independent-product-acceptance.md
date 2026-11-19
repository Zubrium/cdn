# Независимая приёмка Website Engine

## Цель

Подтвердить Website/Content/Admin contracts на синтетических feature builds независимо от Vitasil. Проверки не используют рабочие API, credentials, базы, object storage или файлы.

## Синтетические builds

Тесты создают минимальные ZIP с `manifest.json`, target runtime ABI, локальными ESM assets и typed `dataInputs`.

- Catalog feature: collection input для Product, typed arguments и media input.
- Detail feature: record input Product и lookup по уникальному model field.
- Featured collection: ordered selection Product без копирования record fields.

Модель содержит flat entities. Direct scalar/record/collection bindings поддерживаются; неизвестные модели/поля, несовместимые типы и поля вне контракта отклоняются до сохранения.

## Сквозные сценарии

На disposable PostgreSQL и object storage проверить:

1. Чистую начальную схему, feature import и создание root snapshot.
2. Создание модели/полей и direct bindings в `/site`; отсутствие model record value editing в этом UI.
3. Сохранение структурных изменений в child snapshot без изменения parent и Router route targets.
4. Создание и изменение records, draft save, selection/order и явную публикацию в `/content`.
5. Media selection и readiness; загрузка остаётся в `/media`.
6. Catalog/detail/collection на одной published Product модели, route lookup, prerender, cache identity и отсутствие draft values в публичной выдаче.
7. Snapshot fork, history restore в новый draft и отсутствие автоматической публикации.
8. Revision conflicts, concurrent edits, network retries, authorization failure и отсутствие частичного изменения.
9. Responsive browser/keyboard behavior, destructive confirmations и защита несохранённых изменений.

Все доступа контролируются direct group levels и отдельным правом управления администраторами. Проверки соответствуют фактическим Core и Presentation contracts; роль пользователя не является источником авторизации.

## Результаты и ограничения

Эти сценарии — критерии, а не утверждение о прохождении. Результаты, команды и ограничения записываются в [QA_REPORT.md](../../docs/QA_REPORT.md); незакрытые пункты находятся в [TODO](../../docs/TODO-admin-snapshots-content-files.md). Полный независимый browser/API run и реальная Vitasil acceptance остаются открытыми до выполнения применимых сценариев на disposable ресурсах.
