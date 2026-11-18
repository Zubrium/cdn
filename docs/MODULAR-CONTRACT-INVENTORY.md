# Инвентаризация модульной платформы

Статус: карта целевых границ для `dev_v2`, не свидетельство полной реализации. Архитектурный источник истины — [SPEC-core-architecture.md](../spec/SPEC-core-architecture.md); внешние engine-контракты описаны в спецификациях `spec/SPEC-*-engine.md`. Исторические реализации и QA-результаты относятся только к указанным в отчётах коммитам.

| Граница | Целевой контракт | Владелец состояния и интеграции |
| --- | --- | --- |
| Core | Единый API, identities, права групп, файлы, staging, file sets, engine data, cache и durable workflows | Только Core владеет PostgreSQL, S3 и кэшем; компоненты получают scoped capabilities с нейтральными именами |
| Core Coordinator | Регистрация нейтральных `engine`, `worker`, `entrypoint`, lifecycle, readiness, задачи и durable workflow | Application слой Core; операции и payload остаются непрозрачными для Core |
| PostgreSQL | Одна DB на систему, schemas `access`, `files`, `uploads`, `file_index`, `engine_data`, `coordination`; один fresh Atlas baseline | Все SQL адаптеры находятся в Core infrastructure; DDL только в Atlas migration files |
| Engine data | Общая таблица `engine_data.records` с engine namespace, JSONB, CAS, TTL и ограниченными индексами | Core проверяет scope и лимиты, но не разбирает предметный payload |
| Composition | Статическая сборка модулей, YAML-конфигурация уже собранных компонентов и простой DI | `cmd/zubrium`; конфигурация не загружает произвольный Go-код |
| Presentation | Router, File API, Admin API и Admin UI | Каждый адаптер вызывает публичный контракт владельца сценария; прямой доступ к Core storage отсутствует |
| Website | Фичи, сайты, их сборка и статические артефакты | Website; изменения публикуются отдельными Core workflow и Router-вызовами |
| Content | Модели, записи, локали, черновики и публикации контента | Content; durable payload хранится через `engine_data.records` |
| Forms | Схемы форм, сессии, ответы и вложения | Forms; файлы и права остаются Core API, состояние Forms хранится через engine data |
| Loader | Возобновляемые upload sessions, части, idempotency и cleanup | Loader владеет протоколом; временные и конечные байты доступны только через Core |
| Router | Явные публичные маршруты, delivery и настройки маршрута | Router; Core не хранит таблицы продуктовых маршрутов или публикаций |
| Workers | Архив, медиа и prerender как нейтрально зарегистрированные операции | Каждый worker владеет своим handler и payload; Coordinator не знает продуктовых типов |

## Правила зависимости

```text
presentation -> application -> domain
infrastructure -> domain/application ports
engines and workers -> Core presentation contracts
```

`domain` содержит Core-модели, ошибки и repository ports; моделям разрешены только проверяющие конструкторы. `application` содержит политики доступа, валидацию входов, сценарии использования и Coordinator. `infrastructure` реализует PostgreSQL, S3 и cache. `presentation` публикует стабильные capabilities и связывает зависимости: в том числе `GroupAuthorizationCapability` и `StagedFilesCapability`. Имена контрактов описывают возможности Core, а не конкретного потребителя; внешний composition root назначает их компонентам. Реализации `application`, `domain` и `infrastructure` доступны внешним модулям только через `core/presentation`. SQL DDL отсутствует в production Go-коде.

Core не содержит понятий snapshots, sites, routes, forms, feature builds или публикаций. Межкомпонентная автоматизация строится на зарегистрированных operation ID/version, непрозрачных JSON payload/result, идемпотентности, зависимостях, leases, fencing и явной отмене. Синхронный контракт между движками Core не навязывает.

## Правило для legacy-контрактов

Legacy остаётся исходным материалом для конкретных product API и пользовательских сценариев, но не для внутренней схемы Core. Сохранение внешнего поведения определяется владельческой спецификацией в `spec/`. Поля `role`, старая ролевая матрица и старые Core/storage модели не входят в целевой API. Эта карта не означает, что перечисленные движки или маршруты полностью реализованы.
