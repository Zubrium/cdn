# Disposable Docker stack for integration QA

Набор файлов для запуска изолированного PostgreSQL + MinIO только для интеграционных проверок. Стек не использует корневой `compose.yaml`, не меняет продуктовые источники и не имеет доступа к живым ресурсам.

## Файлы

- `compose.yaml` — два сервиса: `postgres:16-alpine` и `alpine/minio:RELEASE.2025-10-15T17-29-55Z`.
- `up.sh` — создаёт run ID, генерирует случайные учётные данные в приватный env-файл `0600` под `${TMPDIR:-/tmp}`, поднимает только этот проект и создаёт bucket.
- `down.sh` — удаляет только проект `website-qa-<run_id>` и его приватный env-файл.
- `ensure-bucket.py` — host-side helper на стандартной библиотеке Python: подписывает S3-запрос AWS SigV4 и создаёт bucket без дополнительных Docker-образов.
- `owner-result-init.sh` — второй init-скрипт PostgreSQL: создаёт отдельную свежую БД `zubrium_core_qa_owner_result_<run_id>` и выдаёт `core_qa_migrator` те же привилегии, что и `postgres-init.sh`.
- `config.example.env` — только безвредные заглушки для `docker compose config`; **не использовать для запуска**.

## Быстрый запуск

```sh
tests/qa/docker/up.sh
```

`up.sh` печатает только run ID и путь к приватному env-файлу — никогда сами пароли или ключи. Загрузить переменные для runner:

```sh
set -a
. "<path printed by up.sh>"
set +a
python3 tests/qa/run-test-suite.py ci --integration
tests/qa/run-core-red.sh integration
```

`tests/qa/run-core-red.sh` потребляет `TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL` из этого env-файла; для него создаётся отдельная пустая БД, недоступная другим прогонам.

Для Core acceptance тот же файл можно передать как `QA_ENV_FILE`:

```sh
QA_ENV_FILE="<path printed by up.sh>" tests/qa/run-core-acceptance.sh run
```

Завершение:

```sh
tests/qa/docker/down.sh            # если WEBSITE_TEST_RUN_ID уже загружен
tests/qa/docker/down.sh <run_id>   # иначе явно
```

## Переменные

Входные переменные (все необязательны):

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `WEBSITE_TEST_RUN_ID` | идентификатор прогона; нормализуется в нижний регистр, недопустимые символы заменяются на `-` | `qa-<UTC>-<random>` |
| `QA_PG_PORT` | loopback-порт PostgreSQL | `55432` |
| `QA_S3_API_PORT` | loopback-порт MinIO S3 API | `59000` |
| `QA_S3_CONSOLE_PORT` | loopback-порт MinIO Console | `59001` |
| `WEBSITE_QA_WAIT_TIMEOUT` | секунд ожидания healthcheck при `up` | `120` |
| `WEBSITE_QA_BUCKET_TIMEOUT` | секунд ожидания создания bucket | `60` |
| `TMPDIR` | каталог для приватного env-файла | `/tmp` |

Имя `zubrium_core_qa_owner_result_<run_id>` ограничено 63 байтами PostgreSQL, поэтому слишком длинный `WEBSITE_TEST_RUN_ID` отклоняется.

`up.sh` генерирует и записывает в env-файл:

- `CORE_QA_POSTGRES_PASSWORD`, `CORE_QA_RUNTIME_PASSWORD`, `CORE_QA_MIGRATION_PASSWORD`, `CORE_QA_ELEVATED_PASSWORD`;
- `QA_S3_ACCESS_KEY`, `QA_S3_SECRET_KEY`;
- `WEBSITE_TEST_DISPOSABLE=1`, `WEBSITE_TEST_RUN_ID`, `COMPOSE_PROJECT_NAME`;
- `WEBSITE_TEST_POSTGRES_DSN` (база `zubrium_core_qa_platform`, пользователь `core_qa_migrator`);
- `CORE_QA_OWNER_RESULT_DATABASE` (`zubrium_core_qa_owner_result_<run_id>`) и `TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL` с тем же пользователем `core_qa_migrator`;
- `WEBSITE_TEST_S3_ENDPOINT`, `WEBSITE_TEST_S3_BUCKET` (`zubrium-core-qa-<run_id>`), `WEBSITE_TEST_S3_ACCESS_KEY`, `WEBSITE_TEST_S3_SECRET_KEY`;
- псевдонимы `TEST_CORE_PLATFORM_*`, `TEST_CORE_S3_*` и `CORE_QA_*_DATABASE_URL` для повторного использования стека Core QA.

Базы, роли и гранты создаются существующим `tests/qa/postgres-init.sh` (монтируется как `10-core-qa.sh`) и дополняются `owner-result-init.sh` (монтируется как `20-owner-result.sh`). Повторный `up.sh` с тем же run ID переиспользует существующий env-файл, чтобы не менять учётные данные у уже запущенных контейнеров.

## Изоляция

- Compose-проект всегда задаётся явно: `COMPOSE_PROJECT_NAME=website-qa-<run_id>`; `compose.yaml` имеет такой же fallback через `WEBSITE_TEST_RUN_ID`.
- Все порты слушают только `127.0.0.1`. Если порт занят, `up.sh` падает и откатывает только свой проект; **не останавливайте** конфликтующий контейнер — задайте другой `QA_*_PORT`.
- У PostgreSQL и MinIO нет именованных volume: данные живут в tmpfs и исчезают после `down.sh`. Образы не удаляются.
- `down.sh` не принимает шаблоны и не трогает другие проекты; при отсутствии env-файла он использует только безвредные заглушки для интерполяции Compose и всё равно указывает `--project-name website-qa-<run_id>`.
- Никогда не подставляйте живые DSN, bucket, endpoint или рабочие учётные данные в переменные этого стека и не запускайте интеграционные проверки без `WEBSITE_TEST_DISPOSABLE=1` и run-specific ресурсов.
- Не используйте одновременно корневой `compose.yaml` и этот стек на одном порту.
- MinIO bucket создаётся без публичных ACL и policy; helper идемпотентен для `BucketAlreadyOwnedByYou` / `BucketAlreadyExists`.

## Проверка конфигурации без запуска контейнеров

```sh
docker compose --env-file tests/qa/docker/config.example.env \
  -f tests/qa/docker/compose.yaml config
```

`config.example.env` содержит только заглушки; вывод `config` безопасен, но не пригоден для реального запуска.
