# Sitepack transfer contract

> **dev_v2:** Этот документ описывает legacy-поведение, контракты или исторические результаты. Целевая внутренняя архитектура новой платформы определена в [SPEC-modular-platform.md](SPEC-modular-platform.md) и [SPEC-core-v2.md](SPEC-core-v2.md). Внешнюю совместимость необходимо реализовать и проверить на границах engines/presentation, за согласованным исключением: поле `role` и прежняя матрица ролей удаляются из нового API и админки. Совместимость остальных контрактов для нового кода не подтверждена.

Status: current flat-model contract, 15 November 2026. Sitepack version 1, manifest format 1, per-feature CMS payloads, `contentRelations`, relation envelopes, legacy collection-source data, and unconverted per-feature bindings are rejected.

## Exported state

A sitepack includes the selected snapshot payload, pinned feature builds and assets, public media required by references, model structure, placement bindings, draft and published model records, placement selections, and immutable model-content history. Users, access tokens, submissions, branch history, and branch assignments are not exported.

The flat model contains entities, ordered fields, optional legacy layout positions, and model-content records with own `values`. It contains no model relations or record relation maps. `positions` remains in the serialized contract for compatibility but is not used by the editor.

Public and private files retain owner/access checks. Only explicitly referenced public media is transferred. Secrets and private service files never enter the archive.

## Identity remapping

Import creates a new snapshot and regenerates IDs for model entities, fields, records, and publication-history rows. It updates:

- entity and field references in placement `dataInputs`;
- direct scalar `fieldId` mappings;
- record `inputPath`/`fieldId` mappings;
- unique-field route lookup and detail SEO mappings;
- record `entityId`, draft/published record IDs, and selection record IDs;
- historical records and selection IDs;
- legacy `positions` keys;
- public media/file references and packaged renditions.

Placement IDs, page/route structures, feature keys, versions, and selected builds retain their source identities. The source archive and source snapshot are not modified.

## Compatibility and rejection

The importer accepts absent, `null`, or empty legacy relation fields:

- `dataModel.relations` with zero entries;
- record `relations` with an empty object;
- placement `relationIds` with zero entries.

Any non-empty retired relation field causes controlled rejection with `legacy_model_relations_not_supported`. Non-empty relation data is never silently removed or converted. Rejection happens during complete preflight, before snapshot rows, file rows, objects, or build registrations are written.

No relation ID map, relation endpoints, relation diagnostic, relation report item, or relation-specific report count exists in the current format.

## Preflight and atomicity

Archive checks cover size, entry count, path safety, checksums, schema/version, feature manifest contract, build conflicts, model validation, direct bindings, referenced media, rendition data, records, selections, and publication history. The complete package is staged and validated before storage writes.

A failed validation returns the standard `invalid_sitepack` error envelope with a fixed safe locator such as `files[2].renditions[0]`, `builds[1]`, `publishedContent[0]`, or `dataModel.relations`. Error details do not contain content values, credentials, filenames, or arbitrary archive paths. Database/storage failures return a bounded request-identified error without raw internal messages.

Repeated import may reuse already registered builds and files, but always creates a new root snapshot. Existing branch assignments do not change.

## Report

The response reports registered/reused builds, created/reused files, published/draft record counts, history revisions, and remapped entity, field, and record counts. The item list is bounded and may be marked truncated. Reports contain no content values, credentials, source/target IDs, relation fields, or branch assignments.

## Verification

Focused Go tests cover flat-model schema validation, empty and non-empty legacy relation compatibility, direct scalar/record/collection bindings, runtime projection, record/history remapping, and sitepack report identity. Frontend tests cover direct binding controls and absence of relation UI. Full disposable cross-installation DB/S3 transfer remains part of the broader acceptance matrix in [TODO-admin-snapshots-content-files.md](TODO-admin-snapshots-content-files.md).
