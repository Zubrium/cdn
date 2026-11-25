# TODO: Observability

Незавершённые работы по [спецификации observability](../spec/observability/SPEC-observability.md).
Чекбокс закрывается только после исполняемого acceptance-сценария и записи evidence в
[QA report](QA_REPORT.md). Наличие кода, конфига, compose-сервиса или описания сценария
не является основанием отметить пункт выполненным.

## Незавершённые задачи

- [ ] Зафиксировать и согласовать контракт атрибутов: обязательные `service.*`, `deployment.profile`, `router.branch`, `module.*`, `component.id`, `operation.id`, `trace_id`/`span_id`; типизированные конструкторы и правила redaction; подтвердить имя заголовка `X-Trace-ID` и источник `deployment.profile` (YAML vs composition root).
- [ ] Завершить бэкенд `file`: `logs.jsonl`, `spans.jsonl`, `metrics.jsonl`, ротация по размеру/количеству, безопасные режимы файлов и каталога, проверка потери сигнала без изменения бизнес-ответа.
- [ ] Завершить набор метрик `zubrium_*` и локальный `/metrics` в Prometheus text format; подтвердить, что health-check'и учитываются в метриках, но не создают обычные логи. Код Core и Router содержит cache-метрики; полноту их фактического экспорта проверить по [SPEC-caching.md](../spec/architecture/SPEC-caching.md).
- [ ] Проверить испускание `zubrium_cache_operations_total`, `zubrium_cache_entries`, `zubrium_cache_bytes`, `zubrium_cache_warmup_keys_total`, `zubrium_cache_warmup_duration_seconds` и `zubrium_cache_invalidation_lag_seconds` с разрешёнными низкокардинальными метками; закрыть пункт только вместе с `CACHE-010`.
- [ ] Настроить OTel Collector pipelines logs/traces/metrics и OpenObserve streams/dashboard с фильтрами по `module.id`, `component.id`, `operation.id`, `trace_id`, `http.status_code=409`; проверить реальные записи в UI.
- [ ] Выполнить измерение покрытия по denominator исполняемых OBS-сценариев (не statement coverage) и отдельно отметить blocked/not_run без перевода в pass.
- [ ] Провести безопасный shutdown/restart: закрытие span'ов один раз, сброс буферов, отсутствие дублей метрик и утечки telemetry при сбросе компонентов.

## Порядок проверки

1. Документация и implementation sweep согласованы.
2. Локальные статические проверки конфигурации и YAML-синтаксиса.
3. Disposable compose-стек: `--profile observability`, без write-запросов к рабочим API.
4. Исполняемые input/result сценарии OBS-001..OBS-010.
5. QA report с командами, ограничениями и фактическими результатами; только после этого — закрытие чекбоксов.
