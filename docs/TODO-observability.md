# TODO: Observability

Незавершённые работы по [спецификации observability](../spec/observability/SPEC-observability.md).
Чекбокс закрывается только после исполняемого acceptance-сценария и записи evidence в
[QA report](QA_REPORT.md). Наличие кода, конфига, compose-сервиса или описания сценария
не является основанием отметить пункт выполненным.

## Незавершённые задачи

- [ ] Зафиксировать и согласовать контракт атрибутов: обязательные `service.*`, `deployment.profile`, `router.branch`, `module.*`, `component.id`, `operation.id`, `trace_id`/`span_id`; типизированные конструкторы и правила redaction; подтвердить имя заголовка `X-Trace-ID` и источник `deployment.profile` (YAML vs composition root).
- [ ] Завершить бэкенд `file`: `logs.jsonl`, `spans.jsonl`, `metrics.jsonl`, ротация по размеру/количеству, безопасные режимы файлов и каталога, проверка потери сигнала без изменения бизнес-ответа.
- [ ] Завершить бэкенд `otel`: OTLP/HTTP exporter, асинхронная отправка, bounded queue/timeout, заголовки только из `*_env`, проверка недоступности Collector.
- [ ] Подключить identity из composition root и instrumentation на границах HTTP (`http.server`), bus (`bus.dispatch`), worker (`worker.task`) и lifecycle (`component.lifecycle`); убедиться, что бизнес-код модулей не instruments и не выбирает бэкенд.
- [ ] Завершить набор метрик `zubrium_*` и локальный `/metrics` в Prometheus text format; подтвердить, что health-check'и учитываются в метриках, но не создают обычные логи.
- [ ] Проверить конфигурацию: `--check-config` для `noop`/`file`/`otel`, отрицательные тесты на неизвестный mode, пустые directory/endpoint, невалидный `headers_env` и `min_level`.
- [ ] Поднять compose-стек `--profile observability`, проверить изоляцию портов, tmpfs для disposable PostgreSQL/S3, named volume только для OpenObserve и отсутствие секретов в YAML.
- [ ] Настроить OTel Collector pipelines logs/traces/metrics и OpenObserve streams/dashboard с фильтрами по `module.id`, `component.id`, `operation.id`, `trace_id`, `http.status_code=409`; проверить реальные записи в UI.
- [ ] Исполнить acceptance-сценарии `OBS-001..OBS-010` из [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md) на disposable-стеке, включая 409 conflict, невалидный traceparent и отказ Collector; зафиксировать команды и результаты в QA report.
- [ ] Выполнить измерение покрытия по denominator исполняемых OBS-сценариев (не statement coverage) и отдельно отметить blocked/not_run без перевода в pass.
- [ ] Провести безопасный shutdown/restart: закрытие span'ов один раз, сброс буферов, отсутствие дублей метрик и утечки telemetry при сбросе компонентов.
- [ ] Обновить [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md) и [QA report](QA_REPORT.md) статусами только после реального прогона; незапущенные проверки оставить «ожидает проверки».

## Порядок проверки

1. Документация и implementation sweep согласованы.
2. Локальные статические проверки конфигурации и YAML-синтаксиса.
3. Disposable compose-стек: `--profile observability`, без write-запросов к рабочим API.
4. Исполняемые input/result сценарии OBS-001..OBS-010.
5. QA report с командами, ограничениями и фактическими результатами; только после этого — закрытие чекбоксов.
