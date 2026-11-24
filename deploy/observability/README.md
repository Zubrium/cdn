# Observability стек: запуск и просмотр сигналов

Изолированный стек для проверки контракта из [SPEC-observability](../../spec/observability/SPEC-observability.md): `zubrium-obs` → OTel Collector → OpenObserve, плюс одноразовые PostgreSQL и S3-эмулятор Moto. Стек использует `profiles: [observability]` в корневом [compose.yaml](../../compose.yaml); сервис `core-qa-postgres` и рабочие API не затрагиваются.

> Статусы сценариев и результаты прогона смотрите в [acceptance matrix](../../tests/ACCEPTANCE-MATRIX.md) и [QA report](../../docs/QA_REPORT.md). `pass` выставляется только после исполняемой проверки.

## 1. Предусловия

- Docker Compose v2, свободные порты `18080` (Admin), `18081` (публичный Router), `15080` (OpenObserve UI), `14317/14318` (OTLP gRPC/HTTP), `15900` (Moto S3 API), `15432` (PostgreSQL).
- Только disposable-данные. PostgreSQL живёт в tmpfs, Moto хранит состояние в памяти процесса; оба исчезают при остановке контейнеров. OpenObserve хранит сигналы в named volume `openobserve-data`.
- S3-эмулятор — `motoserver/moto`: он поддерживает `Get/PutPublicAccessBlock`, обязательный для Core `RequirePrivate` ([Moto S3 coverage](https://github.com/getmoto/moto/blob/master/docs/docs/services/s3.rst)). Ранее использованный `alpine/minio` является development-сборкой и возвращает `NotImplemented` на эту операцию.
- Образ OpenObserve зафиксирован как `public.ecr.aws/zinclabs/openobserve:v1.1.2`: тег `latest` в этом реестре нестабилен для повторяемого прогона.
- Не направляйте запросы на рабочий API. Для write-сценариев используйте только этот стек.

## 2. Переменные окружения

Значения генерируются локально; в документацию и коммит они не попадают. Пример с заглушками (замените на собственные случайные значения):

```sh
export OBS_POSTGRES_PASSWORD='<random-password>'
export OBS_POSTGRES_RUNTIME_PASSWORD='<different-random-password>'
export OBS_POSTGRES_MIGRATION_PASSWORD='<another-random-password>'
export OBS_S3_ACCESS_KEY='<random-access-key>'
export OBS_S3_SECRET_KEY='<random-secret-key>'
export OBS_OPENOBSERVE_EMAIL='<local-admin@example.test>'
export OBS_OPENOBSERVE_PASSWORD='<random-password>'
export OBS_ROUTER_CREDENTIAL='<random-service-credential>'
export OBS_FORMS_CREDENTIAL='<random-service-credential>'
export OBS_CONTENT_CREDENTIAL='<random-service-credential>'
export OBS_BOOTSTRAP_SECRET='<random-bootstrap-secret>'
```

Необязательные несекретные переопределения: `OBS_POSTGRES_USER` (по умолчанию `obs_admin`), `OBS_POSTGRES_DB` (по умолчанию `zubrium_obs`). Init-скрипт создаёт три роли: `obs_runtime` (DML Core через `zubrium_core_runtime`), `obs_migrator` (Atlas) и групповую роль `zubrium_core_runtime`; `OBS_POSTGRES_PASSWORD` принадлежит административной роли и не используется приложением.

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

Локальный `main/config.observability.yaml` — несекретный (только `*_env` и адреса внутри docker network), но является артефактом конкретного прогона; не коммитьте его. Compose монтирует его только для чтения в `/etc/zubrium/config.yaml`. Сборка использует корневой context и `main/Dockerfile` с `BUILD_TAGS=full`: пример конфига включает компоненты, которые есть только в full-профиле. Top-level блок `prerender` обязателен для full-профиля; при запуске на хосте создайте указанный `staging_directory` с режимом `0700`, иначе preflight sandbox отклонит конфигурацию.

## 4. Что где смотреть

- Проверка доступности: `curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/healthz` (ожидается `204`). Публичный Router — `http://127.0.0.1:18081`.
- Admin UI: `http://127.0.0.1:18080/admin`. Bootstrap secret известен только из `OBS_BOOTSTRAP_SECRET`; не копируйте его в отчёты.
- OpenObserve UI: `http://127.0.0.1:15080`, логин из `OBS_OPENOBSERVE_EMAIL` / `OBS_OPENOBSERVE_PASSWORD`.
- Логи приложения: `docker compose --profile observability logs -f zubrium-obs`.
- Принятые/отправленные батчи: `docker compose --profile observability logs -f otel-collector` (debug exporter с `verbosity: normal`).
- Локальные метрики процесса (Prometheus text format, `no-store`):
  `docker compose --profile observability exec -T zubrium-obs wget -qO- http://127.0.0.1:8080/metrics`.

## 5. Примеры запросов в OpenObserve

После реального OTLP-прогона потоки логов и трейсов называются `default`, а метрики попадают в отдельные потоки по имени серии (`zubrium_operation_total`, `zubrium_http_server_requests_total` и т.д.). Фильтры по `module_id`, `component_id`, `operation_id` и `trace_id` остаются обязательными для расследования.

Logs (SQL):

```sql
SELECT _timestamp, trace_id, module_id, component_id, operation_id,
       http_status_code, outcome, body
FROM "default"
WHERE module_id = 'admin'
  AND http_route LIKE '/api/admin/%'
ORDER BY _timestamp DESC
LIMIT 50;
```

```sql
-- Ошибочный HTTP-ответ: ожидается outcome = 'error'.
SELECT _timestamp, trace_id, operation_id, http_status_code, body
FROM "default"
WHERE http_status_code >= 400 AND outcome = 'error'
ORDER BY _timestamp DESC
LIMIT 20;
```

Traces: откройте раздел Traces и выполните поиск по `trace_id` (32 hex) из заголовка ответа `X-Trace-ID`; проверьте порядок span'ов `http.server` → `bus.dispatch` → `worker.task` и фильтры `module.kind = worker`, `operation.id = <stable operation ID>`. Тот же `trace_id` доступен в `exemplars` метрик:

```sql
SELECT _timestamp, operation_id, module_id, component_id, exemplars
FROM "zubrium_operation_total"
WHERE exemplars LIKE '%6f0b0e1a2c3d4e5f60718293a4b5c6d7%'
ORDER BY _timestamp DESC
LIMIT 20;
```

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

Исполняемые сценарии `OBS-001..OBS-010` запускаются из корня checkout:

```sh
sh tests/qa/run-obs-red.sh
```

Без live-стек сценарии `OBS-006`/`OBS-008` честно `SKIP`. Для полного прогона
дополнительно задайте (значения — только в env, не в файлах репозитория):

```sh
export OBS_TEST_OTEL_ENDPOINT=http://127.0.0.1:14318
export OBS_TEST_OTEL_FAILURE_ENDPOINT=http://127.0.0.1:14319  # закрытый порт
export OBS_TEST_OQ_SEARCH_URL=http://127.0.0.1:15080/api/default/_search
export OBS_TEST_OQ_AUTH="$(printf '%s:%s' "$OBS_OPENOBSERVE_EMAIL" "$OBS_OPENOBSERVE_PASSWORD" | base64 | tr -d '\n')"
```

`OBS_TEST_OQ_AUTH` — base64 от `email:password` для аутентифицированного
`POST /api/default/_search`; harness сам формирует SQL-окно и отдельно опрашивает
метрические потоки OpenObserve v1.1.2.

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
