# Observability стек: запуск и просмотр сигналов

Изолированный стек для проверки контракта из [SPEC-observability](../../spec/observability/SPEC-observability.md): `zubrium-obs` → OTel Collector → OpenObserve, плюс одноразовые PostgreSQL и MinIO. Стек использует `profiles: [observability]` в корневом [compose.yaml](../../compose.yaml); сервис `core-qa-postgres` и рабочие API не затрагиваются.

> Все проверки этого стека пока ожидают реального прогона: статусы `OBS-001..OBS-010` в [acceptance matrix](../../tests/ACCEPTANCE-MATRIX.md) не выставлены в `pass`, результаты не записаны в [QA report](../../docs/QA_REPORT.md).

## 1. Предусловия

- Docker Compose v2, свободные порты `18080` (Admin), `18081` (публичный Router), `15080` (OpenObserve UI), `14317/14318` (OTLP gRPC/HTTP), `15900/15901` (MinIO API/console), `15432` (PostgreSQL).
- Только disposable-данные. PostgreSQL и MinIO живут в tmpfs и исчезают при остановке контейнеров; OpenObserve хранит сигналы в named volume `openobserve-data`.
- Не направляйте запросы на рабочий API. Для write-сценариев используйте только этот стек.

## 2. Переменные окружения

Значения генерируются локально; в документацию и коммит они не попадают. Пример с заглушками (замените на собственные случайные значения):

```sh
export OBS_POSTGRES_PASSWORD='<random-password>'
export OBS_POSTGRES_MIGRATION_PASSWORD='<different-random-password>'
export OBS_S3_ACCESS_KEY='<random-access-key>'
export OBS_S3_SECRET_KEY='<random-secret-key>'
export OBS_OPENOBSERVE_EMAIL='<local-admin@example.test>'
export OBS_OPENOBSERVE_PASSWORD='<random-password>'
export OBS_ROUTER_CREDENTIAL='<random-service-credential>'
export OBS_FORMS_CREDENTIAL='<random-service-credential>'
export OBS_CONTENT_CREDENTIAL='<random-service-credential>'
export OBS_BOOTSTRAP_SECRET='<random-bootstrap-secret>'
```

Необязательные несекретные переопределения: `OBS_POSTGRES_USER` (по умолчанию `obs_admin`), `OBS_POSTGRES_DB` (по умолчанию `zubrium_obs`).

Base64 для Basic auth Collector получаем из уже экспортированных email/password, **не записывая секрет в файл и не печатая его**:

```sh
_BASE64=$(printf '%s:%s' "$OBS_OPENOBSERVE_EMAIL" "$OBS_OPENOBSERVE_PASSWORD" | base64 | tr -d '\n')
export OBS_OPENOBSERVE_AUTH="$_BASE64"
unset _BASE64
```

`OBS_OPENOBSERVE_AUTH` используется только в `deploy/observability/otel-collector.yaml` как `${env:OBS_OPENOBSERVE_AUTH}`.

## 3. Конфиг приложения и старт

```sh
cd "$CHECKOUT"
cp -n main/config.observability.yaml.example main/config.observability.yaml
# При необходимости проверьте синтаксис до старта:
docker compose --profile observability run --rm --no-deps --entrypoint /usr/local/bin/zubrium \
  zubrium-obs --check-config /etc/zubrium/config.yaml
docker compose --profile observability up -d --build
```

Локальный `main/config.observability.yaml` — несекретный (только `*_env` и адреса внутри docker network), но является артефактом конкретного прогона; не коммитьте его. Compose монтирует его только для чтения в `/etc/zubrium/config.yaml`. Сборка использует корневой context и `main/Dockerfile` с `BUILD_TAGS=full`: пример конфига включает компоненты, которые есть только в full-профиле.

## 4. Что где смотреть

- Проверка доступности: `curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/healthz` (ожидается `204`). Публичный Router — `http://127.0.0.1:18081`.
- Admin UI: `http://127.0.0.1:18080/admin`. Bootstrap secret известен только из `OBS_BOOTSTRAP_SECRET`; не копируйте его в отчёты.
- OpenObserve UI: `http://127.0.0.1:15080`, логин из `OBS_OPENOBSERVE_EMAIL` / `OBS_OPENOBSERVE_PASSWORD`.
- Логи приложения: `docker compose --profile observability logs -f zubrium-obs`.
- Принятые/отправленные батчи: `docker compose --profile observability logs -f otel-collector` (debug exporter с `verbosity: normal`).
- Локальные метрики процесса (Prometheus text format, `no-store`):
  `docker compose --profile observability exec -T zubrium-obs wget -qO- http://127.0.0.1:8080/metrics`.

## 5. Примеры запросов в OpenObserve

Имена полей в SQL зависят от фактического OTLP-маппинга; после первого реального прогона сверьте их с записями в UI и поправьте примеры. Фильтры по `module.id`, `component.id`, `operation.id` и `trace_id` должны оставаться обязательными для расследования.

Logs (SQL):

```sql
SELECT timestamp, trace_id, module_id, component_id, operation_id,
       http_status_code, outcome, message
FROM logs
WHERE module_id = 'engine.forms'
  AND operation_id LIKE '%status%'
ORDER BY timestamp DESC
LIMIT 50;
```

```sql
-- Конфликт revision: ожидается http_status_code = 409 и outcome = error.
SELECT timestamp, trace_id, operation_id, error_type, http_status_code
FROM logs
WHERE http_status_code = 409 AND outcome = 'error'
ORDER BY timestamp DESC
LIMIT 20;
```

Traces: откройте раздел Traces и выполните поиск по `trace_id` (32 hex) из заголовка ответа `X-Trace-ID`; проверьте порядок span'ов `http.server` → `bus.dispatch` → `worker.task` и фильтры `module.kind = worker`, `operation.id = <stable operation ID>`.

PromQL (метрики):

```promql
sum by (module_id, operation_id, http_status_code) (
  rate(zubrium_http_server_requests_total[5m])
)
```

```promql
histogram_quantile(0.95,
  sum by (le, module_id, operation_id) (
    rate(zubrium_http_server_duration_seconds_bucket[5m])
  )
)
```

```promql
sum by (module_id, component_id, operation_id, outcome) (
  rate(zubrium_operation_total[5m])
)
```

Метрики `zubrium_cache_*` описаны в [SPEC-caching](../../spec/architecture/SPEC-caching.md) и [SPEC-observability](../../spec/observability/SPEC-observability.md), но кэши пока не испускают их: запросы к этим сериям вернут пустой результат до волны реализации кэширования и прогрева.

## 6. Переключение в file-режим

В `main/config.observability.yaml` замените `observability.mode: otel` на `file`; каталог уже настроен как `/var/lib/zubrium/observability` и живёт в tmpfs контейнера. Пересоздайте только приложение:

```sh
# в observability.mode конфига: file
docker compose --profile observability up -d --force-recreate zubrium-obs
docker compose --profile observability exec -T zubrium-obs \
  ls -l /var/lib/zubrium/observability
```

Создаются три файла: `logs.jsonl`, `spans.jsonl`, `metrics.jsonl`. Ротация даёт файлы вида `logs.jsonl.1`. Читать удобно через `jq` на host (в образе приложения `jq` отсутствует):

```sh
OBS_DIR=/var/lib/zubrium/observability
TRACE_ID='<32-hex-trace-id>'

docker compose --profile observability exec -T zubrium-obs cat "$OBS_DIR/logs.jsonl" \
  | jq -c --arg trace "$TRACE_ID" 'select(.trace_id == $trace)'

docker compose --profile observability exec -T zubrium-obs cat "$OBS_DIR/spans.jsonl" \
  | jq -r 'select(.["span.status"] == "error") | [.timestamp, .name, .["module.id"], .["operation.id"], .["http.status_code"]] | @tsv'

docker compose --profile observability exec -T zubrium-obs cat "$OBS_DIR/metrics.jsonl" \
  | jq -r '[.metric.name, .metric.kind, .outcome // ""] | @tsv' | sort | uniq -c
```

## 7. Остановка и очистка

```sh
docker compose --profile observability down       # named volume OpenObserve сохраняется
docker compose --profile observability down -v    # удаляет и телеметрию: только для disposable
unset OBS_OPENOBSERVE_AUTH
```

Перед `-v` убедитесь, что в стеке нет активных пользователей и что удаляется именно этот одноразовый проект. Обычный `down` сохраняет логи/трейсы в `openobserve-data` для повторного разбора.
