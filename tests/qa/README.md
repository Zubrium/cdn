# Website Engine test runner

`run-test-suite.py` — единая точка запуска проверок с машинно-читаемым отчётом,
статусами `WEB-001..WEB-029` и `CORE-001`, диагностикой падений и CI-гейтами. Runner не имеет
fallback на живой API, PostgreSQL или S3: интеграционные проверки включаются
только явным `--integration` и только с подтверждёнными disposable-переменными.

## Команды

```sh
python3 tests/qa/run-test-suite.py run
python3 tests/qa/run-test-suite.py ci --with-build
python3 tests/qa/run-test-suite.py status
python3 tests/qa/run-test-suite.py diagnose
python3 tests/qa/run-test-suite.py diagnose --gate website-unit
tests/qa/run-release-checks.sh
tests/qa/run-release-checks.sh --integration-env /path/to/private-disposable.env
tests/qa/run-release-checks.sh --integration-env /path/to/private-disposable.env --with-e2e
```

- `run` и `ci` выполняют гейты из `tests/qa/gates.json` плюс `go test`, `go vet`,
  `go build` для каждого модуля `go.work`. `--with-build` включает Admin build,
  который перезаписывает `pkg/static/dist`.
- `--integration` включает disposable-проверки `website-integration` и
  `core-integration`; каждый гейт требует свои переменные из `requires_env`.
  `website-integration` принимает `WEBSITE_TEST_S3_MOCK=1` вместо endpoint/keys,
  но по-прежнему требует run-specific PostgreSQL DSN, bucket и
  `WEBSITE_TEST_DISPOSABLE=1`.
- Диспозитивный стек поднимается через `tests/qa/docker/up.sh` и удаляется
  только через `tests/qa/docker/down.sh` для того же `WEBSITE_TEST_RUN_ID`.
  Browser QA использует отдельный checkout и `tests/qa/browser/prepare-checkout.sh`;
  перед write-сценариями обязателен `tests/qa/browser/verify-target.sh`.
- `--only PREFIX` / `--skip PREFIX` фильтруют гейты по префиксу ID.
- `--strict-scenarios` требует `pass` для всех сценариев всех ledger, включая integration.
- `run-release-checks.sh` выполняет полный sweep репозитория: boundary lint, Go
  test/vet/build по всем профилям, binary smoke build, frontend
  typecheck/lint/test/build, ledger-валидаторы, unified gates и, по явным
  флагам, disposable integration и live E2E. Отсутствие E2E-harness при
  `--with-e2e` считается жёстким prerequisite failure, а не пропуском.
- `tests/qa/run-website-router-e2e.sh` — живой E2E-runner публикации и retirement.
  Он создаёт отдельную копию чекаута, собирает в ней Admin и `bin/zubrium`,
  поднимает run-specific PostgreSQL/MinIO, проверяет target через
  `browser/verify-target.sh`, выполняет Playwright-сценарии через Go-ledger shim
  (`tests/e2e`) и требует точные строки `PASS E2E-PUB-001`, `PASS E2E-LOC-001` и
  `PASS E2E-RET-001`; locale-preference выполняется до retirement, пока route
  targets ещё активны.
  Логи и артефакты прогона остаются в `output/e2e/<run-id>/`; Docker-стек,
  приватный env-файл и временная копия удаляются только для этого run ID.
  MinIO не реализует S3 public access block, поэтому runner поднимает
  `tests/e2e/s3-boundary-proxy.py`: он отвечает только на проверку приватности
  bucket и прозрачно проксирует остальные S3-запросы в MinIO, сохраняя
  подписанные Host/path/query/body. Каждое перехваченное обращение пишется в
  `output/e2e/<run-id>/s3-proxy.log`. Версия Atlas проверяется через
  run-specific wrapper, который использует закреплённый `arigaio/atlas:1.3.0`.
- `status` печатает краткую сводку последнего отчёта, `diagnose` — причины
  провалов и заблокированные сценарии.

По умолчанию отчёт пишется в `output/test-report.json`, логи — в
`output/test-logs/`. Оба каталога ignorable.

## Статусы сценариев

Runner комбинирует три источника:

1. `tests/qa/website-scenarios.json`, `tests/qa/core-scenarios.json` и
   `tests/qa/e2e-scenarios.json` — объявленное покрытие и статусы ledger.
2. Логи гейтов `website-*` и `core-*`: `--- PASS/FAIL/SKIP: TestX/WEB-NNN` (или
   `CORE-NNN`) из `go test -v` переопределяют ledger-статус, поэтому отчёт отражает
   реальный прогон.
3. Наличие успешно выполненного гейта `website-integration` или `core-integration`:
   без `--integration` все integration-сценарии получают `blocked`, а не `pass`/`fail`.

`tests/qa/run-website-red.sh integration` исполняет каждый integration-символ в
том Go-модуле, где лежит centralized test file, и требует реальную строку
`--- PASS: TestX/WEB-NNN`; zero-test success запрещён. `tests/qa/run-core-red.sh`
делает то же для `CORE-NNN`.

Сценарий со статусом `blocked` исключается из знаменателя coverage-gate, но
остаётся видимым в отчёте и `diagnose`. Сценарий с `coverage_status: gap` никогда
не становится `pass` без реального исполняемого subtest.

## Локальные проверки генератора отчёта

```sh
python3 -m unittest tests/qa/test_run_test_suite.py tests/qa/test_scenario_ledger.py
```
