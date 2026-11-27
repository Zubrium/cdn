# Архитектура Website Engine

Схема показывает основные компоненты и направление продуктовых операций на
основании текущей композиции. Она не является доказательством готовности всех
сценариев. Целевые границы определяются [архитектурой Core](../spec/core/SPEC-core-architecture.md)
и [спецификацией модульной платформы](../spec/architecture/SPEC-modular-platform.md).

```mermaid
flowchart TD
    Admin[Admin SPA] --> HTTP[HTTP Presentation]
    Visitor[Посетитель] --> Proxy[Внешний proxy] --> HTTP
    HTTP --> Bus[Process-local DataBus в main]
    Bus --> Website[Website]
    Bus --> Content[Content]
    Bus --> Forms[Forms]
    Bus --> Loader[Loader]
    Bus --> Router[Router]
    Bus --> Core[Core capabilities]
    Bus --> Orchestration[Publication / retirement]
    Orchestration --> Coordinator[Core Coordinator]
    Coordinator --> Tasks[Задачи выбранных profiles]
    Tasks --> Bus
    Tasks --> Workers[Prerender / Media / Archive]
    Workers --> Bus
    Core --> Database[(PostgreSQL)]
    Core --> Objects[(S3 и файловый cache)]
    Coordinator --> Database
```

`main/` собирает выбранный статический profile, регистрирует компоненты и их
разрешённые операции. `pkg/contracts` содержит общие типизированные контракты;
стрелки на схеме обозначают назначение операций и зависимостей, а не гарантируют
способ каждого отдельного вызова в реализации. HTTP-клиент не выбирает operation
ID. Передача больших файлов выполняется через scoped stream capability; JSON
envelope не предназначен для file bodies. Внутренние вызовы внутри одного модуля
могут быть прямыми. Проверка соответствия межмодульных границ остаётся частью
приёмки.

Core владеет PostgreSQL, S3 и общим файловым cache. Engine records размещаются
в `engine_data.records` в namespace владельца; identity scope задаёт композиция.
Coordinator хранит долговечные workflow и задачи. DataBus является локальным
транспортом процесса и сам не предоставляет долговечность.

## Основной поток публикации

```mermaid
flowchart LR
    Edit[Структура Website и данные Content] --> Publish[Публикация Content]
    Publish --> Fanout[Publication orchestration]
    Fanout --> Build[Website build]
    Build --> Render[Prerender worker]
    Render --> Artifact[Проверенный artifact в Core]
    Artifact --> CAS[Router CAS для route и locale]
    CAS --> Delivery[Публичная выдача]
```

Website управляет страницами, snapshots, placements и bindings; Content —
редакционными записями и публикацией. Trusted input сборки закрепляет snapshot,
locale, Content и другие необходимые версии. Router хранит сопоставление
маршрута с artifact. По уточнению владельца системы используется один Router
listener и единый реестр маршрутов. Внешний Router Admin API не выдаёт
`branch_id`. Startup YAML принимает адрес listener. Новая установка использует
системную область хранения для Router, publication и retirement.
Поле `BranchID` пока встречается во внутренних DTO и runtime-коде.
При выводе страницы из эксплуатации отдельный retirement workflow должен
отозвать все зарегистрированные маршруты до перехода страницы в `retired`.

Текущая реализация содержит orchestration publication и retirement, но
независимость частичных отказов и повтор только неуспешных целей ещё не
подтверждены и требуют исправления. `WEB-021` и `WEB-022` заблокированы
компиляцией fixtures orchestration. Детали и evidence — в
[QA report](QA_REPORT.md), открытые задачи — в [активном плане](TODO-admin-snapshots-content-files.md),
сценарии и их статусы — в [ledger](../tests/qa/website-scenarios.json).

## Оценка по имеющимся реестрам

В таблице — доля записей со статусом `pass` в указанном реестре, а не процент
готовности всего модуля. Статус записи не означает, что сценарий повторно
исполнен на текущем checkout; уровень и ограничения проверки нужно сверять с
реестром и QA-отчётом. Для полной оценки нужны исполняемые сценарии на текущем
коде и применимая изолированная приёмка.

| Часть | Доля `pass` в указанном реестре | Граница оценки |
| --- | --- | --- |
| Website | 27/29 — 93,1% | [Website](../tests/qa/website-scenarios.json): 2 integration-сценария blocked, publication и retirement. |
| Content | 4/5 — 80% | [Content](../tests/qa/content-scenarios.json): четыре contract-сценария и один заблокированный E2E; полная приёмка Content не подтверждена. |
| Forms | 4/5 — 80% | [Forms](../tests/qa/forms-scenarios.json): четыре contract-сценария и один заблокированный E2E; это не оценка всех workflows Forms. |
| Loader | 4/5 — 80% | [Loader](../tests/qa/loader-scenarios.json): четыре contract-сценария и один заблокированный E2E; это не оценка всех workflows Loader. |
| Router cache | 5/6 — 83,3% | [Router cache](../tests/qa/cache-router-scenarios.json): ограниченный набор проверок cache, один E2E blocked; не оценка Router целиком. |
| Core | 1/1 — 100% | [Core](../tests/qa/core-scenarios.json): только `CORE-001`, owner-scoped доступ к результату task. Это не означает 100% готовности Core или Coordinator. |
| Observability | 10/10 — 100% | [OBS](../tests/qa/obs-scenarios.json): результаты отдельного прогона с OpenObserve; повтор на текущей ревизии открыт. |
| Core cache | 11/11 — 100% | [Core cache](../tests/qa/core-cache-scenarios.json): проверены зарегистрированные сценарии cache и concurrency. |
| Workers | Не определена | Website integration подтвердил media и prerender в Docker/Linux. Общего набора приёмки всех workers нет. |
| Admin и HTTP Presentation | Не определена | Имеются отдельные unit и browser-проверки, но полного согласованного набора для расчёта готовности нет. |
| Композиция и полная система | Не определена | Требуются актуальные сквозные проверки всех profiles и интеграции; исторический E2E не подтверждает текущий checkout. |
