# Website, Content и Router: целевой production workflow

## Назначение

Довести Website Engine, Content Engine, Router Engine, их Presentation и worker composition до действующих контрактов SPEC: редактор управляет локализованным содержимым и публикацией, Website формирует неизменяемые locale-specific artifacts, Router активирует только явно привязанные результаты и безопасно выдаёт публичную страницу. Рабочая композиция должна проходить через `main/` и typed process-local bus; Core остаётся неизменным.

## Цели

- Реализовать locale catalog Content с канонизацией BCP 47 по IANA Preferred-Value, default locale, CAS и пользовательским правилом: удаление locale запрещено при наличии любой immutable history, а также всех references, указанных SPEC.
- Изолировать draft, publication, history и singleton/translation rules по locale; selections — по `(page_snapshot_id, locale)`.
- Завершить Website import/catalog/build/media/retirement workflow с Coordinator и production workers, сохраняя прежнюю публичную выдачу при ошибке.
- Реализовать locale-specific Router targets, CAS, cookie/Accept-Language/RFC 4647 preference, trusted route→page registry и retirement fan-out.
- Обеспечить реальные host sandbox adapters для Linux, Windows и macOS. Отсутствие поддерживаемого sandbox/tooling должно останавливать запуск worker без fallback.
- Передавать file bodies через call-scoped stream capability, не помещая bytes или Core Session в JSON envelope.

## Не цели и ограничения

- Не менять `core/`; при доказанном отсутствии необходимой уже заявленной Core capability остановиться с точным блокером.
- Не менять/создавать тесты и не запускать тесты, сборки, линтеры, отладку или E2E по прямому ограничению пользователя. Не заявлять проверку или готовность.
- Не коммитить и не пушить.
- Не менять имеющиеся пользовательские изменения без отдельной проверки содержимого и необходимости; не менять их в рамках данного плана без прямой связи с реализацией.
- Не добавлять внешние версионные URL, клиентские operation IDs, обходы Core, временные заглушки и fallback isolation.

## Архитектурный подход

Все межмодульные runtime вызовы остаются в статической composition root `main/`, проходят через общий JSON envelope и выбранный profile allowlist. Внутримодульные вызовы остаются прямыми. Для больших тел operation request передаёт только случайный opaque `StreamRef`; capability привязана к одному вызову и закрывает все ресурсы при выходе. Core Session не входит в typed request и JSON envelope: Dispatcher передаёт исходный `context.Context`, в котором Core presentation capability может хранить opaque Session, как уже принято engine APIs.

Content хранит locale catalog и system-wide records/publications/history в canonical global scope, selections — отдельно в page snapshot scope. Trusted publication registry принадлежит внешнему Presentation/coordinator в `main`, хранит server-owned связи route/page и поддерживает атомарные per-locale targets и durable retirement fan-out; он не является частью Website snapshot и не принимает visitor-authored IDs. Все durable workflow orchestration выполняет Core Coordinator через уже доступный контракт, без изменений Core.

Worker factories регистрируются статически. Archive использует scoped Core files/index/data capabilities; Media получает multi-output contract и platform sandbox adapter; Prerender получает supervised Chromium и artifact writer через имеющиеся Core capabilities. Linux sandbox использует обязательный изолирующий host backend; Windows — AppContainer плюс process/resource job controls; macOS — отдельный упакованный и подписанный helper с App Sandbox entitlements, запускаемый только в sandbox-enabled host deployment. Если helper не установлен, подпись/entitlements не подтверждаются или sandbox не активен, worker startup завершается ошибкой. Каждый backend обязан проверять эффективное применение ограничений до запуска недоверенного workload. Неэквивалентное или отсутствующее ограничение означает отказ запуска; `exec.CommandContext` сам по себе не считается sandbox. Apple документирует App Sandbox как kernel-enforced механизм, задаваемый entitlements, и поддерживает запуск встроенного command-line helper из sandboxed app; поэтому macOS worker не пытается динамически назначить sandbox обычному дочернему процессу.

## Рассмотренные подходы

1. **Локальные заглушки/обходы для каждого API.** Быстрее по отдельным endpoint, но нарушает durable workflow, единый bus, безопасности и SPEC; отклонён.
2. **Прямые вызовы между engines и изменение Core для недостающих связей.** Упростили бы wiring, но нарушили бы границы модулей и явный запрет пользователя; отклонён.
3. **Единая вертикальная реализация в существующей архитектуре.** Typed contracts и profile allowlist в `pkg/contracts`/`main`, capability-адаптеры в composition, bounded engine state и durable workflows в Coordinator, явная registry для route/page. Выбран как единственный совместимый с архитектурой вариант.

## Продуктовые решения

- Локали: полная BCP 47 canonicalization с IANA Preferred-Value и нормализацией casing.
- Удаление локали: запрещено, пока существует immutable history; также запрещено при активных drafts/publications/selections/Router targets по SPEC.
- Изоляция: host sandbox на Linux, Windows и macOS; разрешены точечные platform-specific зависимости и обязательные OS tools. Нет гарантированной изоляции — worker startup завершается ошибкой.
- Публичная локаль выбирается из валидной host-only cookie `zubrium_locale`, затем `Accept-Language` по RFC 4647 Lookup, затем настроенный default; нет fallback содержимого между локалями.
- Content publication фиксируется атомарно; последующая сборка и per-route/per-locale CAS activation — durable асинхронный workflow с частичными результатами. Ошибка не переключает failed target и не удаляет действующий artifact.

## Открытые условия исполнения

- Перед реализацией проверить, что каждая нужная операция уже покрывается Core capabilities и Coordinator. Неизбежное изменение Core — блокер; остановиться вместо его изменения.
- Внешний route→page registry и startup cookie policy оформить как typed composition contracts, не как неоговорённые HTTP inputs. Существующая OpenAPI может уточняться синхронно с handler/admin client только при необходимости контракта.
- Документ должен быть проверен builder-check до выдачи плана. План делится на отдельные вертикальные задачи для bus/session/streams, Content locales, Website workflows, Router registry/locale delivery и production workers/composition; интеграция является одним согласованным объёмом.
