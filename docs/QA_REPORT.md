# QA-отчёт

## Publication: согласованность artifact (2026-11-27)

Результат сборки и новая запись artifact сохраняются одним Batch; совпадающий
повтор допустим, расходящийся сохранённый artifact отклоняется до изменения
target. Unit-тесты проверяют конфликт digest, повтор и отказ Batch через тестовое
хранилище. В `main` прошли `go test`, `go vet` с overlay
`/tmp/website-publication-artifact-overlay.json` и `go build ./...`; проверки
composition/publication и сборка также прошли с `-tags=full`. Атомарность и
конкуренция на disposable PostgreSQL в этом проходе не проверялись; WEB-021
остаётся заблокированным.

## Publication: гонка старта и завершения (2026-11-26)

Статус запуска записывается с CAS по ревизии, прочитанной до `Start`, поэтому
быстро завершившийся workflow не перезаписывается состоянием `running`. Ответ
публикации и повтора перечитывает текущий run. Unit-тест воспроизводит обычный
запуск и завершение до возврата `Start`. В `main` прошли `go test`, `go vet`
с overlay `/tmp/website-publication-race-overlay.json` и `go build ./...`;
проверки composition/publication и сборка прошли также с `-tags=full`.
Реальные конкурентные транзакции и независимые частичные отказы остаются
непроверенными; интеграционная приёмка WEB-021 заблокирована.

Конфликт одновременного создания одного run обрабатывается повторным чтением
durable-записи и сверкой trigger; unit-тест моделирует уже зафиксированный
конкурентный запрос. В `main` прошли `go test ./internal/publication -count=1`,
`go vet ./internal/publication` и `go build ./internal/publication`. Реальная
конкуренция на PostgreSQL этим тестом не подтверждается.

Запоздавший `FailTarget` не изменяет успешно активированную цель; при
конкурентной записи успеха CAS отклоняет устаревшую ошибку. Unit-тесты
проверили оба случая с тестовым хранилищем; из `main` прошли
`go test ./internal/publication -count=1`, `go vet ./internal/publication` и
`go build ./internal/publication`. Остальные переходы и реальные конкурентные
транзакции ещё требуют проверки.

Ошибки publication сохраняются как ограниченные коды: неизвестная ошибка
зависимости отображается как `publication_failed`, её исходный текст не попадает
в результат. Unit-тест проверяет известные ошибки и строки с внутренними
параметрами; из `main` прошли `go test ./internal/publication -count=1`,
`go vet ./internal/publication` и `go build ./internal/publication`.

Для Router CAS publication закрепляет исходную locale revision в target до
вызова; повтор сохраняет её для использования того же fingerprint. Явный
повтор после CAS-конфликта требует отдельного durable attempt. Ответ проверяется по locale
revision и artifact, а не по общей revision маршрута. Unit-тесты проверяют
закрепление и отбрасывание несовпадающих результатов; прошли
`go test` и `go vet` пакета publication, аналогичные проверки composition с
`-tags=full` и `go build -tags=full ./...` в `main`. Потеря подтверждения на
реальном Router/PostgreSQL и старые незавершённые jobs не проверены; WEB-021
остаётся blocked.

Router repository теперь читает locale-CAS receipt по тому же хешированному
ключу, под которым он записан. Тесты с настоящим repository и тестовым
хранилищем проверяют повтор после сохранения и конкурентное создание receipt;
повтор с другой исходной revision отклоняется. В `engines/router` прошли
`go test` и `go vet` с `/tmp/router-cas-replay-overlay.json`, а также
`go build ./...`. Реальная PostgreSQL и сквозная publication не проверены.

Конфликт общей записи Router при одновременном изменении другой locale
обрабатывается ограниченным повтором только при неизменной ревизии нужной
locale и маршрута. Тесты проверяют сохранение обеих locale, отказ при конфликте
той же locale, монотонность времени и отсутствие отката по старому receipt.
В `engines/router` прошли `go test`, `go vet` с overlay
`/tmp/router-locale-conflict-overlay.json` и `go build ./...`; после дополнения
теста повторно прошли test/vet repository. Межпроцессная проверка на PostgreSQL
остаётся открытой.

## Retirement: восстановление перехода страницы (2026-11-26)

Повтор по сохранённому receipt перед запуском workflow подтверждает либо
выполняет перевод страницы в `retiring`. Unit-проверка охватывает повтор до и
после перехода, уже завершённую job, имитацию конкурентного CAS и отказ при
изменившейся странице или чужом владельце. В `main` прошли `go test`, `go vet`
с overlay `/tmp/website-retirement-recovery-overlay.json` и `go build ./...`;
проверки composition/retirement и сборка также прошли с `-tags=full`. Перезапуск
и реальные конкурентные транзакции на disposable PostgreSQL не проверены;
интеграционная приёмка WEB-022 остаётся заблокированной.

Успешное удаление route сохраняется при запоздавшем ошибочном или повторном
успешном результате: unit-тест проверяет state, route revision и отсутствие
лишней записи job. В `main` прошли `go test`, `go vet` с overlay
`/tmp/website-retirement-outcome-overlay.json` и `go build ./...`; проверки
retirement/composition и сборка прошли также с `-tags=full`. Реальная
конкуренция на PostgreSQL и независимость целей ещё не подтверждены.

`Launch` использует ревизию job до `Start` и не перезаписывает более новое
завершение запоздавшей ошибкой; успешный повтор запуска обновляет сохранённый
`retryable_failed`. Unit-тест воспроизводит оба случая. Из `main` прошли
`go test ./internal/retirement -count=1`, `go vet ./internal/retirement` и
`go build ./internal/retirement`; реальный конкурентный запуск и restart не
проверены.

`Retry` проверяет состояние внутри CAS-петли и при конфликте перечитывает job;
unit-тест моделирует завершение между чтением и записью и проверяет отсутствие
повторного запуска. Из `main` прошли `go test ./internal/retirement -count=1`,
`go vet ./internal/retirement` и `go build ./internal/retirement`. Реальная
конкуренция на PostgreSQL не проверялась.

Завершённая retirement job сохраняет `succeeded` при запоздавших переходах и
повторах без лишнего изменения ревизии. Unit-тест сначала воспроизвёл откат
статуса, затем прошёл; из `main` также прошли `go vet ./internal/retirement` и
`go build ./internal/retirement`. Межпроцессная конкуренция не проверялась.

Коды ошибок withdrawal ограничены: конфликт и недоступность Router сохраняют
отдельные коды, неизвестный текст зависимости не возвращается клиенту.
Unit-тесты проверяют типизированные ошибки и строки с внутренними параметрами.
Для `pkg` прошли `go test`, `go vet` с overlay
`/tmp/retirement-code-overlay.json` и `go build ./...`; для composition прошли
test/vet и сборка `main` с `-tags=full`.

## Retirement: ревизия финализации (2026-11-26)

Финализатор сверяет payload с durable job и владельцем страницы, а текущую
ревизию страницы использует для CAS: переход в `retiring` уже заменяет исходную
ревизию. Повтор после записи `retired` для той же job допускается. Добавлена
unit-проверка принятия новой ревизии и отказа при несовпадении владельца,
исходной ревизии или ключа. В `main` прошли `go test` и `go vet` с overlay
`/tmp/website-acceptance-cont-overlay.json` и `go build ./...`; дополнительно
прошли `go test` и `go vet` пакетов composition/retirement и `go build ./...`
с `-tags=full`. Это не заменяет заблокированную интеграционную приёмку WEB-022:
независимые отказы и повтор целей ещё требуют исправления и проверки.

## Media: idempotency key (2026-11-26)

Исправлена проверка caller key перед повторным использованием готовой или
выполняющейся job: новый ключ атомарно закрепляется за совпадающей job,
использованный с другим fingerprint ключ возвращает conflict. Unit-проверки
охватывают прежний формат caller receipts, совпадение, конфликт версии
обработчика и имитацию конкурентного присваивания ключа. `go test`, `go vet`
с `/tmp/website-idempotency-overlay.json` и `go build ./...` в
`engines/website` прошли; overlay создан через `python3 tests/go-overlay.py`.
Живая проверка конкурентных транзакций на disposable PostgreSQL/S3 не
выполнена и остаётся частью заблокированного WEB-019.

## Website integration ledger (2026-11-26)

`go test -overlay=/tmp/website-acceptance-audit-overlay.json -tags='full
website_acceptance' -gcflags=-e ./cmd/zubrium -run '^$'` из `main` завершился
ошибкой компиляции: `WEB-021/022` fixtures используют удалённые поля
publication/retirement Config. Поэтому связанные сценарии `WEB-012`,
`WEB-018..022` переведены в `execution_status: blocked`, а их исторические
прогоны не считаются текущим pass. Валидатор ledger подтвердил denominator 29,
23 pass и 6 blocked; `python3 -m unittest tests/qa/test_scenario_ledger.py
tests/qa/test_run_test_suite.py` прошёл (28 тестов). `covered` отражает наличие
сценариев, а не успешный запуск.

## Инициализация workflow handlers (2026-11-26)

Регистрация publication/retirement происходила до `Start` компонентов и
захватывала ещё не установленный глобальный Core pointer. Обработчики теперь
получают owning Core из композиции при регистрации. Unit-проверка фиксирует
удержание экземпляра и независимость двух bindings; `go test`, `go vet`,
`go build` модуля `main` с overlay прошли в профилях `default`, `full`,
`router`, `devfixtures`, архитектурный линтер прошёл для 22 модулей.
Это не live-приёмка: `full website_acceptance` по-прежнему не компилируется,
а изоляция частичных отказов и повторов остаётся отдельной открытой задачей.

## Core: disposable PostgreSQL (2026-11-26)

На checkout `5201427` использован отдельный проект `website-qa-codex261126a`:
PostgreSQL `127.0.0.1:37431`, S3 `127.0.0.1:37432`, временные данные в
контейнерах; рабочие сервисы и данные не использовались. Тесты запускались
из `core/` с overlay `/tmp/website-acceptance-audit-overlay.json`, созданным
`python3 tests/go-overlay.py` из корня, и переменными только из private env
этого проекта. `go test -count=1 -v -overlay=... -run
'^TestDisposableOwnerScopedTaskResult$' ./infrastructure/postgres` подтвердил
`CORE-001`, включая отказ чтения чужого результата и неизвестных идентификаторов.
Аналогичный запуск с `-run '^TestDisposableCoordinatorLifecycleSuite$'` на
отдельной пустой disposable БД подтвердил восемь подсценариев: регистрацию,
конкурентный claim, зависимости и барьеры, rollback и отказы store. Это не
полный concurrency/recovery или Website acceptance; они остаются открытыми.

## Content: media в проекции (2026-11-26)

Trusted projection извлекает `media_accept` из проверенного закреплённого
manifest и передаёт его через Content workspace; редактор формирует отдельные
задания для связанных placements и ограничивает выбор пересечением типов.
Проверены сохранение метаданных и digest при передаче проекции, выдача из
Content и преобразование в Admin. `go test`, `go vet`, `go build` с overlay
прошли для `pkg`, `engines/content` и `main` (`-tags=full`); архитектурный
линтер прошёл. Admin typecheck, lint и test (61 файл, 284 теста), а также
Vite build в отдельный временный каталог прошли. Это не live browser или
PostgreSQL/S3 приёмка; media TODO остаётся открытым.

Content publication с HTTP idempotency key короче 16 символов получает стабильный
допустимый ключ workflow; ключи 16–128 символов сохраняются без изменения.
Unit-тест проверяет короткий ключ, повтор и границы; `go test`, `go vet` с
overlay `/tmp/content-publication-key-overlay.json` и `go build ./...` в
`main`, а также test/vet composition и сборка с `-tags=full` прошли. Живой
Content-to-publication workflow на PostgreSQL не проверялся.

Composition отклоняет publication до записи Content при отсутствии
dispatcher и не считает успешным результат без опубликованной revision.
Unit-тесты подтверждают оба случая; `main` прошёл `go test`, `go vet` с
overlay `/tmp/content-fanout-required-overlay.json`, `go build ./...` и
test/vet composition и сборку с `-tags=full`. Durable доставка при crash после
commit Content и до dispatch остаётся открытой.

## Идентичность media receipts (2026-11-26)

`TestMediaRequestFingerprintPreservesExistingInputIdentity` проверяет, что
fingerprint `dataInputs` сохраняет алгоритм до введения `schema_kind`, а
аргументы используют отдельную идентичность и сохраняют уже введённую для них
формулу. Для `engines/website` прошли `go test` и `go vet` с overlay
`/tmp/website-media-fingerprint-overlay.json`, а также `go build ./...`;
overlay создан `python3 tests/go-overlay.py` из корня. Это проверка алгоритма,
не live replay сохранённой job на PostgreSQL/S3.
Jobs `dataInputs`, созданные именно промежуточной версией `46d20a1` с другой
формулой fingerprint, требуют отдельной проверки и миграционного решения;
наличие таких пользовательских jobs не установлено.

## Admin: подготовка media (2026-11-26)

Picker вызывает Website derivative API с `schema_kind`, snapshot, placement и
полем, ожидает окончания job и не применяет выбранный файл при ошибке или
отмене ожидания. Из `frontend/` прошли Admin `typecheck`, `lint` и `test`
(61 файл, 284 теста); Vite production build прошёл с выходом в отдельный
`/tmp/website-admin-media-build`, без перезаписи существующего `pkg/static/dist`.
Тесты покрывают запрос, ожидание, ошибку и отмену; реальные браузер, worker и
PostgreSQL/S3 не запускались. Новые несохранённые placements и полный workflow
требуют отдельной реализации и приёмки. Пункт TODO остаётся открытым.

## Media schema kind и ограничение acceptance (2026-11-26)

Для Website media resolver проверены одинаковые пути в `arguments` и
`dataInputs`, вложенные массивы и экранирование ключей. `go test`, `go vet`,
`go build` с централизованным overlay для `pkg`, `engines/website`,
`presentation/admin/sites` и `main` с `-tags=full` прошли; Admin typecheck
прошёл. Полный Admin media workflow и живой media acceptance не пройдены.

`go test -overlay=/tmp/website-media-overlay.json -tags='full website_acceptance'
./cmd/zubrium -run '^TestWebsiteMediaDerivativeAcceptance$' -count=1 -v`
из `main` не компилируется: тесты publication/retirement используют удалённые
поля старой workflow composition. Запуск без `website_acceptance` выдал
`no tests to run` и не считается проверкой. Overlay создан командой
`python3 tests/go-overlay.py` из корня.

## Проверка media-деклараций в аргументах (2026-11-26)

`TestManifestRejectsInvalidArgumentMediaProfile` до изменения подтверждённо
завершался ошибкой: некорректный `x-media` в аргументе принимался. После
добавления проверки вложенных схем `go test -overlay=/tmp/website-media-overlay.json
./...`, `go vet -overlay=/tmp/website-media-overlay.json ./...` и `go build ./...`
в `engines/website` прошли; overlay создан командой `python3 tests/go-overlay.py`
из корня. Проверены валидный вложенный профиль, лишний параметр обработки и
профиль на не-media поле. Это не приёмка media workflow: поддержка
`schema_kind`, разрешение профилей аргументов и клиент добавлены позднее;
end-to-end обработка ещё не подтверждена.

## Финальный sweep строгих границ модулей (2026-11-25)

Статусы `ARCH-001..ARCH-006` в [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md)
выставлены `pass` только по результатам этого прогона. Рабочий стек
`zubriumprod205553`, его PostgreSQL/MinIO и пользовательские uncommitted-файлы
не изменялись; живые write-запросы не отправлялись.

Проверяемые команды (overlay пересоздан из `tests/go-overlay.py`):

```sh
seed=/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/opencode/zubrium-obs-20261125
python3 tests/go-overlay.py > "$seed/api-isolation-overlay.json"
GOWORK=off GO111MODULE=off go run ./tests/linter
GOWORK=off GO111MODULE=off go test ./tests/linter
(cd core && GOFLAGS="-overlay=$seed/api-isolation-overlay.json" go test -count=1 -p=1 ./...)
for module in $(grep -E '^\s*\./' go.work | tr -d '\t ./'); do
  (cd "$module" && GOWORK=off GOFLAGS="-overlay=$seed/api-isolation-overlay.json" go test -count=1 -p=1 ./... && GOWORK=off GOFLAGS="-overlay=$seed/api-isolation-overlay.json" go vet ./... && GOWORK=off go build ./...)
done
for tag in default full router devfixtures; do
  (cd main && GOWORK=off GOFLAGS="-overlay=$seed/api-isolation-overlay.json" go test -count=1 -tags="$tag" -p=1 ./... && GOWORK=off go build -tags="$tag" ./...)
done
git diff --check
```

Результат: все шаги завершились `PASS`, включая 22 repository-local Go модуля,
линтер с пустым strict baseline, точные allowlist-тесты (в том числе 67 Admin
engine operation keys) и все четыре скомпилированных профиля `main`. Полный
machine-readable лог с длительностями и выводом каждого шага —
[`output/architecture-boundaries-20261125-sweep.txt`](../output/architecture-boundaries-20261125-sweep.txt);
прогон 2026-11-25 06:22:18+0300 — 06:23:50+0300, итог `ALL PASS`.

После зелёного sweep правила линтера зафиксированы манифестом SHA-256 в
`tests/linter/rules_freeze_test.go`; тест отклоняет изменение любого rules-файла
и появление нового. Повторные `go run ./tests/linter` и
`GOWORK=off GO111MODULE=off go test ./tests/linter` после фиксации прошли.

Покрытие сценариев: ARCH-001 — AST-линтер и тесты границ; ARCH-002 — Admin
DataBus adapters и Router public operations; ARCH-003 — workflow-тесты
`main/internal/{publication,retirement,contentbridge}` и `engines/website`;
ARCH-004 — тесты `presentation/router/publicupload` и `router_public_bus`;
ARCH-005 — точные allowlist/module-scope тесты и запрет клиентского
`operation_id`; ARCH-006 — изолированные `test/vet/build` по каждому модулю и
отдельный модуль `presentation/admin/base/notification`.

Ограничения прогона: интеграционные сценарии с PostgreSQL/S3 и живой
browser/E2E в этой волне не выполнялись — для них нужны disposable ресурсы и
изолированный стек; это не трактуется как `pass`. Фронтенд Admin в этой волне
не изменялся, поэтому `pnpm typecheck/test/build` и ESLint повторно не
запускались.

## Изоляция HTTP/OpenAPI API-модулей (2026-11-25)

Проверялись HTTP-адаптеры Admin и публикация контрактов OpenAPI в текущем checkout. Рабочий стек `zubriumprod205553` не использовался; живые запросы выполнялись только к отдельному продукту `zubrium-obs-20261125` на `127.0.0.1:18080`. Секреты загружались из приватного env-файла `0600` вне репозитория; в отчёт и коммит не включены.

### Исправленные нарушения

- `presentation/admin/base/transport/transport.go`: вместо identity catch-all `/api/admin/` теперь монтируются только явные шаблоны `/api/admin/session`, `/api/admin/principals`, `/api/admin/principals/`, `/api/admin/credentials/`, `/api/admin/access-groups`. Добавлен terminal `NotFoundHandler` на `/api/admin/`, а также точные 404-якоря `/api/admin/content` и `/api/admin/forms`, чтобы Go `ServeMux` не создавал скрытые `307` на несуществующие корни поддеревьев. Все регистрации собираются в `map`, проверяются на дубликаты и registrations выводятся в отсортированном порядке.
- `presentation/admin/base/admin/notifications/notifications.go`: SSE-хаб и endpoint вынесены из Identity в отдельный компонент; `/api/admin/notifications` монтируется только при включённом NotificationHub и доступных Identity/Session зависимостях. Origin проверяется для браузерных read-запросов.
- `presentation/router/handler.go`: публичный `ServeHTTP` больше не содержит admin mux; Admin-поверхность доступна только через `AdminHTTPHandler()`. Router-админ catch-all сужен до `/api/admin/router/{path...}`, чужие Admin-пути на публичном listener не обслуживаются.
- `spec/presentation/openapi/admin-notifications.yaml`:scheme name `BearerCredential` приведён к общему для контрактов виду. `spec/presentation/openapi/router.yaml`: внешняя `$ref` на `forms.yaml` заменена локальной копией `PublicFieldDefinition`; эквивалентность полей Forms проверяется тестом.
- `tests/go/main/cmd/zubrium/openapi_contract_test.go`: проверка spec→handler теперь выполняется per-contract, а не против union всех YAML; добавлены проверки уникальности `operationId`, принадлежности префиксов, отсутствия внешних `$ref`, эквивалентности копии поля и ownership монтирований Admin transport.
- Новые runtime-тесты: `tests/go/presentation/admin/base/transport/api_isolation_test.go` и `tests/go/presentation/router/admin_boundary_test.go`.

### Команды и результат

```sh
seed=/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/opencode/zubrium-obs-20261125
python3 tests/go-overlay.py > "$seed/api-isolation-overlay.json"
git diff --check
(cd presentation/admin/base && go test -count=1 -overlay="$seed/api-isolation-overlay.json" ./... && go vet -overlay="$seed/api-isolation-overlay.json" ./... && go build ./...)
(cd presentation/router && go test -count=1 -overlay="$seed/api-isolation-overlay.json" ./... && go vet -overlay="$seed/api-isolation-overlay.json" ./... && go build ./...)
(cd main && go test -count=1 -overlay="$seed/api-isolation-overlay.json" ./... && go vet -overlay="$seed/api-isolation-overlay.json" ./... && go build ./... && go build -tags=full ./...)
```

Все команды завершились exit 0; новые тесты `API-ISO` прошли. Product-бинарник был пересобран с `-tags=full`, изолированный Admin перезапущен: `pid=40983`, `GET /healthz` → 204.

Анонимный GET-матрица после фикса: owned Admin-префиксы → 401; `/api/admin/content`, `/api/admin/content/`, `/api/admin/forms`, `/api/admin/forms/`, `/api/admin/unknown-module-route` → 404; `/api/files` и `/api/coordinator/workflows` → 401; `/api/admin/router/operations` → 401 на Admin listener. До добавления terminal fallback и 404-якорей `/api/admin/content` и `/api/admin/forms` давали скрытый `307`.

Авторизованные проверки в изолированном стеке: `POST /api/admin/session` → 201; `GET /api/admin/session`, `/api/admin/access-groups`, `/api/admin/principals` → 200; `/api/admin/notifications` без browser same-origin signals → 403, с `Accept: text/event-stream`, `Sec-Fetch-Site: same-origin` и доверенным `Referer` → 200 SSE. Ни один write-запрос не отправлялся в рабочий стек или его БД/buckets.

Полный machine-readable протокол: [`output/api-isolation-20261125/api-isolation-evidence.txt`](../output/api-isolation-20261125/api-isolation-evidence.txt). Статусы сценариев `API-ISO-001..API-ISO-004` выставлены в [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md) только после этого прогона.

### Найденный, но не исправленный в этой волне дефект

`frontend/admin/src/api.ts` в `media.prepareRenditions()` вызывает несуществующий маршрут `${ADMIN_ROOT}/media/{id}/renditions/prepare`, тогда как контракт объявляет только `POST /api/admin/media/derivatives` и `GET /api/admin/media/jobs/{jobID}`. Исправление требует расширить `MediaRenditionContext` полем `placement_id`, передать его из Site editor и Content model editor и добавить опрос состояния job в Admin SPA. Это изменение UI workflow, а не изоляции HTTP-модулей, поэтому оставлено вне текущей волны и не отмечено как `pass`.

## Observability: живой OTLP-прогон (2026-11-25)

Целевой стек: отдельный compose-проект `zubrium-obs-20261125`, profile `observability`.
Рабочий стек `zubriumprod205553` и его PostgreSQL/MinIO/S3 не пересоздавались и не
использовались для write-запросов. Секреты хранились только в приватном env-файле
`0600` вне репозитория; в отчёт и коммит не попали.

### Статические и стартовые проверки

- `docker compose --profile observability config --quiet` с заданными `OBS_*` и без
  `CORE_QA_*` — exit 0. Исправление compose: обязательные Secret-переменные
  `core-qa-postgres` переведены в runtime guard самого сервиса, поэтому profiling
  `observability` разбирается независимо; приложение использует только роль
  `obs_runtime` (DML через `zubrium_core_runtime`), миграции — `obs_migrator`.
- Свежий контейнер `postgres:16-alpine` с новым `deploy/observability/postgres-init.sh`
  на порту `15433`: `obs_admin|t`, `obs_migrator|t`, `obs_runtime|t`,
  `zubrium_core_runtime|f`; членство `zubrium_core_runtime` у `obs_admin` и `obs_runtime`.
  Контейнер удалён после проверки.
- `(cd main && go build -tags=full -o "$seed/zubrium" ./cmd/zubrium)` — exit 0.
  Хост-конфиг скопирован из `main/config.observability.yaml.example` с добавленным
  `observability.mode: otel`; `--check-config` этого файла — `configuration is valid`.
  В эталонный example-конфиг добавлен обязательный top-level блок `prerender` с
  явной sandbox-конфигурацией; staging-каталог создан с режимом `0700`.
- `(cd pkg && go test ./... && go vet ./... && go build ./...)` — PASS, включая
  новый `TestIdentityAttributesOmitEmptyValues`: пустые identity-атрибуты больше не
  перекрывают корректные resource attributes в OpenObserve.

### Исполняемые сценарии OBS-001..OBS-010

Команда (секреты и auth заданы в env до вызова, `GOCACHE` явно равен кэшу Go,
чтобы не пересобирать всё в tmpfs при почти полном диске):

```sh
GOCACHE="$(go env GOCACHE)" \
OBS_TEST_OTEL_ENDPOINT=http://127.0.0.1:14318 \
OBS_TEST_OTEL_FAILURE_ENDPOINT=http://127.0.0.1:14319 \
OBS_TEST_OQ_SEARCH_URL=http://127.0.0.1:15080/api/default/_search \
OBS_TEST_OQ_AUTH=<base64(email:password)> \
  sh tests/qa/run-obs-red.sh
```

Результат: exit 0; все `OBS-001` … `OBS-010` — `--- PASS`, включая ранее
blocked `OBS-006` и `OBS-008`. Лог: `output/observability-20261125/obs-scenarios-script.log`
(копия приватного evidence-каталога). Harness адаптирован к OpenObserve v1.1.2:
аутентифицированный `POST /api/default/_search` с SQL-окном, отдельный поиск по
метрическим потокам и retry до появления всех обязательных маркеров; secret
передаётся только через `OBS_TEST_OQ_AUTH`. В `OBS-006` исправлена передача
sampled parent trace в worker/bus, иначе их спаны не экспортировались отдельными
trace. В `OBS-008` отказ flush при недоступном Collector считается telemetry-only
ограничением: бизнес-статус 200 и ограниченное время shutdown обязательны,
а ошибка `Close` не меняет бизнес-результат (композиция её и так игнорирует).

### Живые проверки OpenObserve и отказа Collector

- Приложение в `otel` режиме: PID `35381`, `GET /healthz` → 204, `GET /metrics` → 200
  с `Cache-Control: no-store`. Collector debug-exporter принимает батчи с атрибутами
  `service.name=zubrium`, `deployment.profile=full`, `router.branch=main`,
  `module.kind`, `module.id`, `component.id`, `operation.id`.
- `POST /api/admin/session` с credentials из `OBS_BOOTSTRAP_SECRET` → 201;
  авторизованный `GET /api/admin/access-groups` с `traceparent:
  00-c0ffee0123456789abcdef0123456789-1122334455667788-01` → 200,
  `X-Request-Id: 6cf56e46f478c40e`, `X-Trace-Id: c0ffee0123456789abcdef0123456789`.
  В `logs/default` одна запись `http request completed` с `http_route=/api/admin/access-groups`,
  `http_status_code=200`, `module_id=admin`, `service_name=zubrium`,
  `deployment_profile=full`, `router_branch=main`; в `traces/default` — span
  `http.server` с тем же `trace_id`. Метрика `zubrium_http_server_requests_total`
  содержит exemplar с этим `trace_id` и span_id `6cf56e46f478c40e`.
- Ошибочный запрос `PATCH /api/admin/forms/submissions/{id}/status` с CSRF-токеном
  → 404 (маршрут не зарегистрирован в этом checkout, не 409). В логах `outcome=error`,
  `http_status_code=404`; в трассах — 4 спана с общим `trace_id`, включая
  `bus.dispatch` со `span_status=ERROR` и `operation.id=core.records.get`.
  Сценарий 409 ранее покрыт отдельным file-mode прогоном `OBS-002`; в этом стеке
  реального 409 не получено.
- OBS-008: при остановленном контейнере `otel-collector` `healthz=204`,
  `admin=200`, PID процесса не изменился. После восстановления Collector для
  `trace_id=44444444444444445555555555555555` снова найдены записи в `logs` и
  `traces`; для `11111111111111112222222222222222` во время outage записей нет —
  потеря батча зафиксирована как ограничение, а не как успешная доставка.
-Evidence live-поиска: `output/observability-20261125/obs-final-evidence.json`,
  `obs-006-final.json`, `obs-evidence.json`, `obs-scenarios-live.log`.

### Ограничения прогона

- OpenObserve v1.1.2 отдаёт 400 на поиск_metrics по потокам без данных/без поля
  `exemplars`; harness пропускает такие потоки и ищет маркеры по доступным record.
- В таймстемпах OpenObserve поиск метрик с окном 5–10 минут не находил уже
  скомпактированные записи; использовано окно 24 часа через SQL `start_time/end_time`.
- Router `GET /healthz` отвечает 404; для correlation использован рабочий
  Admin-маршрут `/api/admin/access-groups`. Endpoints `/api/admin/forms/` и
  `/api/admin/model` в этом checkout тоже дают 404 и не использовались как
  доказательство успешного tracing.
- `no space left on device` при сборке в Docker и при повторной сборке тестов в
  новом `GOCACHE`; финальный product-бинарник собран на хосте, тесты — с
  существующим кэшем Go. Docker image observability-стека в этом прогоне не
  пересобирался (`--no-build`).

## Router `core-file` disposition (2026-11-25)

Проверяемое состояние: ветка `readiness-20261123`, коммиты
`7e7c95a` (model/delivery), `50c6a58` (OpenAPI/SPEC), `f802436`
(Admin Router), `b7a6ef7` (удаление переключателя `/site`). Секреты и
приватные env-файлы в evidence не попали.

### RED → GREEN

- RED до production-кода: `TestCoreFileTargetDispositionIsOptionalAndStrictlyValidated` падал на неизвестном значении и disposition для artifact target; `TestCoreFileDispositionHonorsSafeMIMEDefaultAndExplicitOverride` и `TestCoreFileDispositionMatchesHEADAndNotModified` падали на `attachment` вместо `inline` и на пустом disposition при 304; `TestRouterOpenAPIRouteTargetDeclaresDisposition` падал с `RouteTarget must declare disposition`; Admin `route-target.test.ts` падал на потере `disposition: "attachment"`; Site-тесты падали на наличии контролов «Поведение»/«Скачивать файлом» и подписи режима в списке.
- GREEN: строгая валидация `inline|attachment` только для `core-file`, legacy JSON без поля принимается; fail-closed allowlist MIME в `presentation/router/delivery.go`; disposition сохраняется для GET/HEAD/304; OpenAPI и SPEC синхронизированы; Admin builder/UI передают поле только для `core-file`; `/site` больше не хранит `fileDisposition`.

### Статические проверки

Все команды запускались из корня checkout, если не указано иначе; логи в
`/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-disposition-20261125/`:

- `git diff --check`, `python3 -m unittest tests/qa/test_scenario_ledger.py`, `make lint-core-boundary` — exit 0.
- `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures` — exit 0.
- `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures` — exit 0.
- Из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (61 файл / 283 теста), `build`, `pnpm lint` — exit 0.
- Итог логов: `ALL_STATIC_PASS`.

### Живая проверка в disposable-стеке

- Стек `zubriumprod205553` не пересоздавался: PostgreSQL `127.0.0.1:54693`, MinIO API `127.0.0.1:54694`, S3 boundary proxy `127.0.0.1:55268` (PID `16937`) остались без изменений.
- Пересобран full-бинарник: `(cd main && go build -tags=full -o "$run_dir/zubrium.new" ./cmd/zubrium)`. Процесс продукта `17340` остановлен по проверенному PID, бинарник заменён, продукт перезапущен из `$run_dir` с `set -a; . ./runtime.env; set +a; ./zubrium --config ./config.yaml`; новый PID `24587`. `/readyz` Admin → `204`, `/admin/` → `200`.
- Существующий роут `host=localhost path=/` без поля disposition подтвердил дефолт. Рабочий URL: **`http://localhost:54692/`**.
- Через Admin API создан контрастный роут `id=bb5abde1-7e02-4009-aeaa-341a25b03ebe`, `host=localhost`, `path=/download`, `target.disposition=attachment` (idempotency key `disposition-attachment-20261125-001`, `201`). Роут с опечаткой `loacalhost` оставлен без изменений.

Точные наблюдаемые HTTP-заголовки (curl `-D - -o /dev/null`):

| Проверка | Результат |
| -------- | --------- |
| `GET /` | `200`, `Content-Type: image/webp`, `Content-Disposition: inline`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff` |
| `GET /download` | `200`, тот же `Content-Type`, `Content-Disposition: attachment` |
| `HEAD /` | `200`, `Content-Disposition: inline` |
| `GET /` с `If-None-Match: "sha256-382050…c4f"` | `304`, `Content-Disposition: inline`, тот же `ETag` |
| `GET /?download=true` | `200`, `Content-Disposition: inline` — query не влияет на режим |
| `POST /api/admin/router/routes` с `disposition: "download"` | `400 {"error":"invalid_request"}` |

### Ограничения

- Небезопасные MIME (`text/html`, `image/svg+xml`, unknown) проверены исполняемым Go-тестом `TestCoreFileDispositionHonorsSafeMIMEDefaultAndExplicitOverride`, но не отдельным live-файлом в disposable Core: соответствующий scenario помечен `pass (unit)`.
- Range/206 и кэширование ответа не входили в волну: `Cache-Control` остаётся `no-store`.
- Полный release/E2E sweep для этого коммита не запускался; baseline от `c662e38` сохранён ниже и не переопределён. Browser-скриншот отображения картинки не снимался: live-доказательство — HTTP-заголовки и body того же Core file.

## Финальный чистый checkout: release/E2E PASS и SPEC-audit (2026-11-24)

Проверяемое состояние: ветка `readiness-20261123`, HEAD
`c662e380a5936558610df261f0996154a9a5f7fd`, чистый clone
`/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-final-c662e38`.
Результаты ниже относятся именно к этому коммиту; более ранние записи сохранены
ниже как исторические и не заменяют финальный прогон.

- Полный sweep: `QA_BROWSER_WORKING_PORTS=8080,55432 bash tests/qa/run-release-checks.sh --with-e2e --report-prefix output/test-report-release-final-c662e38`, exit 0; лог `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-release-final-c662e38.log`. Переменная задана явно, потому что порт `5173` занят Vite другого проекта; чужой процесс не останавливался. Preflight продолжает проверять владельца listener'ов, build identity и несовпадение disposable портов с рабочими.
- До E2E прошли `git diff --check`, `pnpm --dir frontend build`, `make lint-core-boundary`, `make vet`, `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, product smoke build, Admin `typecheck/lint/test/build`, unit-тесты QA runner и Website contract ledger.
- Статический unified report `output/test-report-release-final-c662e38.json`: `83 pass, 0 fail, 0 blocked` gates; `67 pass, 0 fail, 0 not_run, 14 blocked`, coverage `67/67 executable = 100.00%`. Полный знаменатель `81`; `100%` — только покрытие исполняемых scenario ledger, не 100% соответствие SPEC.
- Отдельные модульные проверки на том же checkout: `go test`, `go vet`, `go build` из `core/` и `presentation/router/` с overlay — exit 0; лог `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-modules-final-c662e38.log`.
- Живой E2E внутри sweep: run `e2e-20261123T233329Z-681d30`, evidence `output/e2e/e2e-20261123T233329Z-681d30/`; `playwright.log` содержит точные строки `PASS E2E-PUB-001` и `PASS E2E-RET-001` без `FAIL`. `verify-target.log` подтверждает checkout binary/config, build identity и отсутствие доступа к рабочим портам.
- Цикл `POST /api/locale-preference` на HEAD после `c662e38`: операция скомпилирована в full-профиле и включается явной startup-конфигурацией; unit-, config-, topology- и OpenAPI-контрактные тесты прошли в составе `make test`, `make vet`, `make build` и `make lint-core-boundary`. Живой E2E: `QA_BROWSER_WORKING_PORTS=8080,55432 tests/qa/run-website-router-e2e.sh`, финальный proof скопирован в `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-locale-preference-evidence-final/`; `playwright.log` содержит `PASS E2E-PUB-001`, `PASS E2E-LOC-001`, `PASS E2E-RET-001` без `FAIL`. Переменная портов задана явно: порт `5173` занят Vite другого проекта, чужой процесс не останавливался. Два промежуточных run'а (`e2e-20261124T121131Z-cfa517`, `e2e-20261124T121317Z-59377e`) не учитываются: первый остановлен preflight из-за чужого listener'а, второй выявил порядок сценариев — locale preference нельзя проверять после retirement, когда route targets уже сняты. После E2E исправлен boundary: внешний Router handler использует facade `engines/router/presentation` вместо engine internals; `TestCheckConfig` стал зависеть от скомпилированного build plan, поэтому проходит во всех профилях. Финальные проверки на изменившемся дереве: `git diff --check`, `python3 -m unittest tests/qa/test_scenario_ledger.py`, `make lint-core-boundary`, `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, Admin `typecheck/test/build` и `pnpm lint` — exit 0 (ESLint запущен через штатный `frontend/package.json` script; прямой `pnpm exec eslint admin/` не является поддерживаемой командой репозитория).
- Integration-прогон на свежем disposable PostgreSQL/S3 `final-integr-c662e38`: `tests/qa/docker/up.sh` → полный sweep с `--integration-env` → `tests/qa/docker/down.sh final-integr-c662e38`. Логи: `/private/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/zubrium-release-final-integr-c662e38.log` и `zubrium-final-integr-down.log`. Отчёт `output/test-report-release-final-integr-c662e38-integration.json`: `2 pass, 0 fail, 0 blocked` gates; `74 pass, 0 fail, 0 not_run, 7 blocked`, coverage `74/74 executable = 100.00%`. Реально исполнены `WEB-012`, `WEB-018`, `WEB-019`, `WEB-020`, `WEB-021`, `WEB-022`, `CORE-001`, без `t.Skip`. Disposable контейнеры, сеть и private env удалены.
- Живой E2E внутри integration sweep: run `e2e-20261123T234350Z-cdaf0b`; подтверждены те же две PASS-строки, target ownership и build identity. Bootstrap secret в сохранённый evidence не попал.
- Оставшиеся blocked в baseline integration report `c662e38`: `OBS-006`, `OBS-008`, `CACHE-009`, `CONC-008`, `CONTENT-005`, `FORMS-005`, `LOADER-005`. Не переданные в integration static-прогон `WEB-012/018-022` и `CORE-001` исполнены отдельным integration gate и не считаются пропущенными. `CONC-008` снят последующим циклом ниже.
- Финальные исправления, которые сделали возможным чистый прогон: `62f7c10` (Admin assets до embedded UI tests), `723b5f6` (deployed observer в store config), `493473f` (bounds validation до внутренних cache dependencies), `5209027` (устранён typed-nil legacy byte cache при reconnect), `c662e38` (явный, валидируемый override рабочих портов).
- Полный аудит каждого requirements-раздела `spec/` со статусами `Implemented`, `Verified`, `Blocked` и `Not implemented` — в [SPEC compliance audit](SPEC-COMPLIANCE-AUDIT-20261124.md). Итог: release/E2E PASS получен, но 100% соответствие всему `spec/` **не заявляется**: есть незавершённые runtime-требования и blockers.
- Перечисленные blocked включали `CONC-008` только для baseline `c662e38`; после отдельного цикла он снят и подтверждён ниже.

## Цикл `CONC-008`: изолированные class-scoped worker pools (2026-11-24)

Проверяемое состояние: ветка `readiness-20261123`, HEAD кода
`ed3ec076d8ba59d72113df93170e13ae0f2071f1`. Код зафиксирован коммитом
`Implement isolated class-scoped worker pools`; статусы документов фиксируются
следующим evidence-коммитом. Baseline `c662e38` и locale-цикл `107bbde` остаются прежними.

### RED → GREEN

- RED-тест `TestCoordinatorServiceActivatesIsolatedClassPools` показал, что один общий executor не даёт независимой активации классов. GREEN: `ServiceConfig.Pools []TaskPoolConfig`, отдельный `TaskExecutor` на каждый включённый класс, общий owner/config epoch и class-scoped `Claim`.
- RED-тест `TestCoordinatorServiceIsolatesSaturatedWorkerPool` насыщал `media` при `Concurrency=1` и требовал завершения `prerender`/`publication`. GREEN: соседние пулы не заимствуют чужую ёмкость; `media` дренируется после release.
- На ревью найдены и исправлены три дефекта до финального прогона: отключённый класс терял registry binding и давал терминальный `operation_unavailable` для cross-class ребёнка; неудачный `Activate` разрешал повтор и мог перейти в Ready с нулём пулов; границы pool-параметров не проверялись для disabled/no-worker профилей. Соответствующие regression-тесты: `TestFullProfileDisabledClassKeepsResolvableFailClosedBinding`, `TestCoordinatorServiceFailedActivationIsTerminal`, `TestConcurrencyClassPoolScenario/CONC-008`.
- Расовая гонка в тестовом `fakeTaskStore` (`claimedScope`/`completeCalls`) устранена мьютексом; `go test -race` в `core/application/coordination` после этого прошёл.

### Статические проверки

Все команды запускались из корня checkout, если не указано иначе; логи — в
`output/conc008-20261124-final/`:

- `git diff --check` → `diff-check.log`, exit 0.
- `python3 -m unittest tests/qa/test_scenario_ledger.py` → `scenario-ledger-unit.log`, exit 0.
- `bash -n tests/qa/run-website-router-e2e.sh` → `shell-syntax.log`, exit 0.
- `make lint-core-boundary` → `lint-core-boundary.log`, exit 0; `Architecture boundaries OK` для 20 repository-local модулей.
- `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures` → `test.log`, `test-full.log`, `test-router.log`, `test-devfixtures.log`, exit 0; последний skipped `CONC-008` в `concurrency_budget_scenario_test.go` заменён исполняемым сценарием.
- `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures` → `vet.log`, `build*.log`, exit 0.
- `(cd core/application/coordination && go test -race -count=1 -overlay=<generated> ./...)` → `coordination-race.log`, `ok ... 1.290s`.
- `(cd main && go test -count=1 -tags='full website_acceptance' -overlay=<generated> -run '^$' ./cmd/zubrium)` → `acceptance-compile.log`, exit 0.
- Из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test`, `build` и `pnpm lint` → `admin.log`, exit 0.
- Overlay сгенерирован `python3 tests/go-overlay.py`; его SHA-256 сохранён в `overlay.sha256`.

### Integration

- Свежий изолированный стек `tests/qa/docker/up.sh` с run ID `conc008-final2`, PostgreSQL/MinIO на loopback-портах `56432/59990/59991`; приватный env-файл создан с режимом `0600` под `TMPDIR`, значения в отчёт и логи не попали.
- Отдельная свежая БД `zubrium_core_qa_isolated_pools_conc008_final2`; переменная `TEST_COORDINATOR_ISOLATED_POOLS_DATABASE_URL` задана только для тестового процесса, в логе заменена на redacted.
- `(cd core/infrastructure/postgres && go test -count=1 -v -overlay=<generated> -run TestDisposableIsolatedWorkerPoolsRunIndependently ./...)` → `integration.log`, точные строки `=== RUN TestDisposableIsolatedWorkerPoolsRunIndependently` и `--- PASS`. Проверено: насыщенный `media` не блокирует `prerender`/`publication`, соседние пулы не превышают лимиты, `media` удерживает ровно один lease и дренируется после release; workflow'ы всех трёх классов завершаются `succeeded`.
- `tests/qa/docker/down.sh conc008-final2` → `integration-stack-down.log`; контейнеры, сеть и приватный env удалены, повторной проверкой `docker ps` активных ресурсов запущено не осталось.

### Ограничения

- `CONC-008` подтверждён как функциональная изоляция классов с настоящим PostgreSQL, но не как нагрузочный p99-прогон; `CONC-001..004` и `CONC-009..011` остаются unit/contract-level сценариями.
- `CACHE-009`, `OBS-006/008`, `CONTENT-005`, `FORMS-005`, `LOADER-005`, `UI-POL-011` и deployer-owned proxy этим циклом не затронуты.
- 100% соответствие всему `spec/` по-прежнему не заявляется.

## Цикл `published_artifacts` и Router warm-up (2026-11-24)

Проверяемое состояние: ветка `readiness-20261123`; код и документы цикла фиксируются отдельными коммитами. Baseline `c662e38`, locale-цикл `107bbde`/`153f0d3` и `CONC-008` `ed3ec07`/`ae2b41a` остаются прежними.

### RED → GREEN

- RED-тесты до production-кода: `TestResolveDerivesPublishedArtifactWarmupKeys`, `TestResolveRejectsInvalidPublishedArtifactSources`, `TestRouterWarmupContractFromResolvedConfig`, `TestRouterCacheObserverUsesExactLowCardinalityLabels` и расширенный `TestCacheRouterScenario/CACHE-014`. Наблюдаемые ошибки: undefined symbols для нового config/router API, `merged warm-up configuration rejected: duplicate published artifact route/locale slot`, `fixture does not enable the published artifact source`, `optional failure counter = 0, want 1`.
- GREEN: `resolveWarmupKeys` выводит file ID из явных server-owned published artifact slot-ов, мержит с inline по file ID, сортирует и обрезает до `max_keys`; `validateScales` отвергает неявный список, дубли `(route_id, locale)`, конфликтующие identity, дубли principals/actions и списки, превышающие `max_keys`. Router warm-up выполняется внутри `routerEngineComponent.Start` до Ready через проверенные `manifestCache.read` и `routeCache.resolve`, с gating по startup-флагам, fail-closed по route/locale/file и разделением required/optional ошибок. Router cache публикует `zubrium_cache_operations_total`, `zubrium_cache_entries`, `zubrium_cache_bytes` и `zubrium_cache_invalidation_lag_seconds` для `router.manifests`/`router.routes`, включая нулевые series для выключенных целей; адаптер `routerCacheObserver` публикует только низкокардинальные метки `cache`, `tier`, `result`.
- RED для операционных метрик: `TestCacheRouterScenario/CACHE-014/primed_before_first_delivery` наблюдал `router.manifests MISS operations = 0, want 1`; RED для валидации — четыре кейса `TestResolveRejectsInvalidPublishedArtifactSources` принимали дубли principals/actions и чрезмерные `paths`/artifacts. После GREEN все перечисленные тесты прошли.
- На ревью до финального прогона исправлены: нарушение границы модуля (`presentation/router/warmup.go` импортировал engine internals — добавлены facade-функции `PathMatches`/`ValidateRoute` в `engines/router/presentation/contract.go`), нестартуемый пример `main/config.observability.yaml.example` (`inline: true` при пустых `keys` отвергается замороженным Core), optional-ошибка, ошибочно блокировавшая strict Ready, и отсутствие проверки отменённого `context` в цикле прогрева.

### Статические проверки

Все команды запускались из корня checkout, если не указано иначе; логи — в `output/cache-20261124-final/`:

- `git diff --check`, `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests/qa/test_scenario_ledger.py`, `python3 tests/qa/scenario-ledger.py tests/qa/core-cache-scenarios.json`, `python3 tests/qa/scenario-ledger.py tests/qa/cache-router-scenarios.json`, `make lint-core-boundary` — exit 0. Core-cache ledger: `11 covered, coverage 100.00%, execution_pass 11`. Cache-router ledger: `6 covered, coverage 83.33%, execution_pass 5, execution_blocked 1` (только `CACHE-009`).
- `make test`, `make test-full`, `make test-router`, `make test-dev-fixtures`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures` — exit 0; `static-sweep-final.log` заканчивается `ALL_STATIC_PASS`.
- `go test -race -count=1 -overlay=<generated> ./...` в `presentation/router` и `go test -race -count=1 -overlay=<generated> ./internal/config ./internal/composition` из `main/`, а также compile-only `(cd main && go test -count=1 -tags='full website_acceptance' -overlay=<generated> -run '^$' ./cmd/zubrium)` — exit 0; `race-compile.log` заканчивается `ALL_RACE_COMPILE_PASS`.
- Overlay сгенерирован `python3 tests/go-overlay.py`, SHA-256 сохранён в `overlay.sha256`. Точные targeted-прогоны: `TestWarmupSourceScenarios/CACHE-013`, `TestRouterWarmupContractFromResolvedConfig`, `TestRouterCacheObserverUsesExactLowCardinalityLabels`, `TestCacheRouterScenario/CACHE-014`.

### Integration

Disposable-прогон с PostgreSQL и временно patchированным `motoserver/moto:5.0.28` выполнялся через `bash output/cache-20261124-final/run-integration-moto.sh` (лог `integration-moto-runner.log`, полный sweep — `integration-moto-sweep.log`, private env не попадает в логи). Для S3-совместимости тестового окружения использован QA-обход: в moto добавлен обработчик `GetBucketPolicyStatus`, а через `configure-moto-bucket.py` включён public access block; продукт и его конфигурация не менялись.

- Основной unified-прогон: `python3 tests/qa/run-test-suite.py ci --with-build --report output/test-report-cache-final3-integration.json` — **83 gates pass, 0 fail, 0 blocked**; `70 pass, 0 fail, 0 not_run, 14 blocked`, `coverage 70/70 executable = 100.00%`. В число прошедших входят `core-cache-scenarios` (включая `TestWarmupSourceScenarios/CACHE-013` как unit и race) и `cache-router-scenarios` (`TestCacheRouterScenario/CACHE-014`). Гейт `core-cache-scenarios` расширен до второго легального расположения теста: `tests/qa/run-core-cache-red.sh` теперь отображает `tests/go/main/internal/config/*` в модуль `main`, пакет `./internal/config` и принимает symbol `TestWarmupSourceScenarios/*`.
- Core listener: `TestDisposableCoreV2FileGroupSetAndCacheAcceptance` прошёл на том же disposable стеке. Для детерминизма тестовая конфигурация задаёт явные `InvalidationBatchSize=1` и `InvalidationBatchWindow=20ms` (без них `listenInvalidations` отвергает границы и не поднимает LISTEN), а повторная инвалидация публикует `pg_notify(..., probeKey)` вместо широкого `'*'`: точечный ключ проверяет именно тот путь, который используется продуктом. Reconnect и targeted invalidation оба подтверждены.
- Дополнительная integration-фаза `run-test-suite.py ci --integration --only website-integration --only core-integration` **не пройдена по причинам окружения**: `website-integration` упёрся в `link: mapping output file failed: no space left on device` (на диске данных оставалось ~1 ГБ), а `core-integration` отказал в `TestDisposableOwnerScopedTaskResult/CORE-001`, потому что `TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL` указывал на БД, уже содержащую Core-схему после первого прогона. Оба отказа к кеш-циклу отношения не имеют и не могут считаться PASS; для полного integration-свипа нужны свободное место на диске и отдельная свежая disposable БД на каждый integration-гейт. Итоговый `runner exit=1` означает именно эту фазу, а не основной unified-прогон.

### Ограничения

- `targets.authorization` остаётся `false`: включение требует отдельного согласования `access_epoch` в замороженном Core и живой epoch-проверки.
- Router-таргеты используют явные `timeout`, `per_key_timeout`, `max_keys`, tier/gating и refresh без перекрытия циклов; параллелизм и retry базового Core-пайплайна остаются покрытыми `CACHE-003` на уровне Core runner. Операционные метрики `router.manifests`/`router.routes` добавлены и проверены на уровне контракта с observer; в живом прогоне с production-observability их значения не снимались.
- `CACHE-009`, `OBS-006/008`, live environment для `CONTENT-005`, `FORMS-005`, `LOADER-005`, `UI-POL-011` и deployer-owned proxy не затронуты; 100% соответствие всему `spec/` не заявляется.

## Историческая Core-cache / concurrency / security wave (2026-11-24, intermediate checkout)

Эти записи описывают промежуточный checkout `3e920f7` с незакоммиченными правками и сохранены для прослеживаемости; актуальный результат — в первом разделе.

## DataBus/Core isolation slice (2026-11-23, исторический проход)

Записи ниже описывают промежуточные состояния до исправлений публикационного fan-out, retirement E2E и финального release sweep. Актуальный результат — в первом разделе.

- Перенесены component descriptors и profile metadata из Core registry в `pkg/contracts` + `main/internal/composition`; Core registry хранит только зарегистрированные opaque owner IDs. Удалены Core `ComponentDescriptor`/`ComponentDescriptorProvider` и registry metadata lookup, а durable task API использует generic operation/task-class contracts вместо Core WorkerCatalog/WorkerRef. Внутренние identity/files adapters Core переименованы в нейтральные. Prerender artifact writer и файловые операции Archive/Media используют typed `core.files.*` DataBus clients; file bytes идут только scoped stream, а Core file capability выбирается по module scope в composition adapter.
- Ранее в run `files-databus-20261123-a` прошли `go test -tags=full -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...` из `main/`, `go test -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...` из `core/`, `go test -count=1 ./...` из `pkg/`, compile-only Website acceptance, targeted bus/file regressions и `git diff --check`; исходные логи находятся в `output/files-databus-20261123-a/`. В последующем run после Archive/Media migration прошли `go test -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...` в `workers/archive/` и `workers/media/`, `go test -tags=full -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...` в `main/`, а также `go test -count=1 ./...` в `pkg/`. Сейчас `GO111MODULE=off go test -run '^TestCoreDoesNotModelEngineOrWorkerComponents$' -count=1 ./tests/linter` и `git diff --check` также прошли.
- НЕ PASS: текущий `make lint-core-boundary` завершился ошибкой: `TestRuntimeModulesUseDataBusInsteadOfImportingCore` находит 33 импорта Core в 32 файлах `engines/` и `presentation/`. В `workers/archive/` и `workers/media/` прямых Core imports больше нет. Core types/metadata regression проходит. Это блокер требуемой архитектурной границы; полный release sweep для этого scope не выполнялся.
- Scope не завершён: identity/session/authorization/staging и межмодульные Presentation вызовы ещё требуется перевести на typed DataBus operations; предыдущий запуск `vet/build`, disposable integration, browser QA и release sweep относится к более раннему состоянию и не подтверждает текущий срез. Полная готовность не заявляется.

### Router file delivery follow-up

- Router HTTP delivery и проверка route targets больше не импортируют Core. Они принимают только read-only интерфейс с нейтральными `contracts.File` DTO; в `main` Router service credential связывается с `engine.router` scoped `CoreFiles` adapter, а HTTP presentation получает клиент, который вызывает allowlisted `core.files.stat/read` через DataBus. File body по-прежнему передаётся scoped stream. Acceptance test fixtures Archive/Media/Router обновлены к `contracts.CoreFiles` контракту.
- PASS: `go test -count=1 ./router` из `presentation/`; `go test -tags=full -count=1 -overlay=/tmp/website-engine-current-overlay.json ./internal/composition` из `main/`; аналогичный `go test` для `workers/archive/` и `workers/media/`; `go test -tags='full website_acceptance' -run '^$' -count=1 -overlay=/tmp/website-engine-current-overlay.json ./cmd/zubrium` (compile-only acceptance); overlay сгенерирован `python3 tests/go-overlay.py`. В `main/internal/composition` добавлен regression, что Core `ErrForbidden` преобразуется в neutral file contract error.
- НЕ PASS: последующий `make lint-core-boundary` всё ещё завершается ошибкой из-за 35 прямых импортов в 34 файлах `engines/` и `presentation/`. Остальные identity/session/authorization/staging/file capabilities и admin/public Presentation boundaries ещё не мигрированы; полного unit/vet/build, disposable integration или release sweep той волны нет.

### Последующая проверка текущего checkout

- После Router migration выполнены `go test`, `go vet` и `go build` для каждого Go-модуля workspace с `-overlay=/tmp/website-engine-current-overlay.json` (для `main` — `-tags=full`); все завершились успешно. Также прошли compile-only Website acceptance и `git diff --check`. Это подтверждает сборку текущего diff, но не заменяет boundary gate или release sweep.
- `make lint-core-boundary` остаётся красным: 33 нарушения в 32 файлах. Прямые зависимости включают Content/Forms/Loader/Website и HTTP Presentation; composition всё ещё передаёт части engines прямые `Core Runtime` capabilities (`Identity`, `Sessions`, `Files`, `GroupAuthorization`, workflow clients). Router Routes удалил прямой Core import: аутентификация credential/session token и проверка `router_routes` теперь идут через typed `core.access.*` DataBus contracts; bus разрешает только группы, объявленные profile descriptor вызывающего модуля. В Content repository ранее удалена прямая зависимость от `core/presentation.ErrRevisionConflict`.
- Website/Router E2E остаётся НЕ PASS. Disposable runs `e2e-20261123T105312Z-a7bb51`, `111006Z-a8b7a7`, `111614Z-8941bb`, `112433Z-824cfa`, `112959Z-ebc85a`, `113142Z-f1c2b0`, `113458Z-34e1eb`, `114320Z-1c32c1`, `114927Z-f57661`, `115328Z-9aadd6` и `120200Z-975d3d` последовательно выявили/проверяли snapshot fork/build. Реализованы FeatureVersion catalog reads, generic Core Batch в границах одной owner namespace с несколькими scopes, Content index projections, чтение фактических Core-issued revisions и snapshot CAS по фактической revision. В последних двух runs fork дошёл до `ready` и Content publish прошёл для обоих locales; `POST /api/admin/builds` дважды вернул 503 `unavailable`. Между runs исправлены ранняя инициализация Content capabilities и Content-authenticated context, но повторный E2E не изменился. Trace последнего run содержит только `core.records.list` от HTTP handler, без record reads/workflow start; причина отказа пока не установлена. Browser E2E и строки `PASS E2E-...` не получены. Evidence: `output/e2e/e2e-20261123T120200Z-975d3d/`; disposable cleanup завершён. Полный release sweep ещё не выполнен.
- Content module: `python3 tests/go-overlay.py`, `go test -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...`, `go vet -overlay=/tmp/website-engine-current-overlay.json ./...` и `go build -overlay=/tmp/website-engine-current-overlay.json ./...` из `engines/content/` завершились с exit code 0; те же команды модуля `engines/website/` завершились с exit code 0. `git diff --check` прошёл. После обновления generic Batch и фиксации фактических Core revisions модульные проверки снова прошли; E2E `e2e-20261123T115328Z-9aadd6` и `e2e-20261123T120200Z-975d3d` подтвердили snapshot fork и Content publication, но оба build вернули 503, так что acceptance не пройдена.
- Router access slice checks (overlay: `python3 tests/go-overlay.py` → `/tmp/website-engine-current-overlay.json`): из `engines/router/` выполнены `go test -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...`, `go vet -overlay=/tmp/website-engine-current-overlay.json ./...`, `go build -overlay=/tmp/website-engine-current-overlay.json ./...`; из `pkg/` выполнены те же три команды; из `main/` выполнены `go test -tags=full -count=1 -overlay=/tmp/website-engine-current-overlay.json ./...`, `go vet -tags=full -overlay=/tmp/website-engine-current-overlay.json ./...`, `go build -tags=full -overlay=/tmp/website-engine-current-overlay.json ./...`. Все эти команды завершились с exit code 0. `git diff --check` прошёл. `make lint-core-boundary` завершился с exit code 2 и перечислил 33 прямых imports в 32 файлах. Полный release readiness остаётся FAIL/не завершена; disposable integration, browser E2E и полный release sweep после этого среза не выполнены.

## Ранее зафиксированный acceptance/release статус

Это исторический проход до исправлений публикационного fan-out и retirement E2E. Актуальный результат — в разделе «Полный release sweep» выше.

Тогда полный release **не был принят**: обязательные E2E-сценарии не дошли до браузерной части. Реальные Website/Core integration-сценарии и статические проверки прошли, но это не компенсировало E2E-провал.

- Полная команда: `tests/qa/run-release-checks.sh --integration-env /var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T//website-qa-qa-full-e-20261123.env --with-e2e --report-prefix output/test-report-goal-20261123-e`. Окружение `qa-full-e-20261123` — свежие disposable PostgreSQL/S3; для Docker использовался временный config без пользовательского credential helper. Команда завершилась с ошибкой на E2E.
- До E2E: `make lint-core-boundary`, Go test/vet/build для профилей workspace, product smoke build, Admin typecheck/lint/test/build, QA-runner unit tests и Website contract acceptance прошли. Unified report `output/test-report-goal-20261123-e.json`: `67 pass, 0 fail, 0 blocked`; в этом статическом проходе оставались 7 integration-сценариев blocked и 2 E2E not_run.
- Integration-команда внутри полного прогона получила только `website-integration` и `core-integration` disposable credentials. `output/test-report-goal-20261123-e-integration.json`: `2 pass, 0 fail, 0 blocked`; 32/32 исполняемых сценария pass. Точные результаты: `PASS TestWebsiteFeatureImportAcceptance/WEB-012`, `PASS TestWebsiteIntegrationPersistenceBoundary/WEB-018`, `PASS TestWebsiteMediaDerivativeAcceptance/WEB-019`, `PASS TestWebsitePrerenderAcceptance/WEB-020`, `PASS TestWebsitePublicationFanoutAcceptance/WEB-021`, `PASS TestWebsitePageRetirementAcceptance/WEB-022`, `PASS TestDisposableOwnerScopedTaskResult/CORE-001`.
- E2E run `e2e-20261123T010410Z-7f7584`, evidence `output/e2e/e2e-20261123T010410Z-7f7584/`: fixture setup ожидал 180 секунд, но созданный child snapshot оставался `provisioning`; `tests/e2e/setup.mjs` завершился `snapshot timed out`. Browser publication/retirement и точные `PASS E2E-...` строки не получены; `E2E-PUB-001` и `E2E-RET-001` остаются незапущенными. Причину незавершения snapshot нужно устранить и повторить полный release sweep на fresh disposable ресурсах.
- В локальном `server-bootstrap.log` этого run начальный disposable administrator secret был выведен приложением; строка очищена из сохранённого evidence. Исправление: E2E runner запускает продукт с явным `--suppress-bootstrap-secret`, который оставляет штатный вывод без изменений при обычном `--config`. Regression `TestParseRunConfigArgumentsCanSuppressBootstrapSecret` прошёл; `go test`, `go vet`, `go build` `main` (`-tags=full`, overlay) и `bash -n tests/qa/run-website-router-e2e.sh` также прошли после изменения. Последующие run'ы с новым флагом (`e2e-20261123T155637Z-b63f32`, `e2e-20261123T160114Z-82a196`) подтвердили отсутствие секрета в сохранённом evidence end-to-end.
- Существующее правило `make lint-core-boundary` проверяет только отсутствие импортов локальных модулей из Core и не доказывает требуемую обратную границу. Regression-тест `TestRuntimeModulesUseDataBusInsteadOfImportingCore` в run `record-databus-20261123-b` завершился ожидаемым FAIL: 50 production-файлов и 3 тестовых файла в `engines/`, `workers/`, `presentation/` ещё импортируют `core/presentation`. Отдельный `TestCoreDoesNotModelEngineOrWorkerComponents` в run `core-boundary-20261123` также завершился ожидаемым FAIL: Core всё ещё содержит `ComponentDescriptor`, `WorkerCatalog`, `WorkerRef`, `WorkerHandler`, `EngineIdentityCapability` и `EngineFilesCapability`. Повторный combined run обоих boundary-тестов после DataBus executor slice завершился FAIL; сырой лог — `output/core-boundary-20261123/after-operation-executor.log` (exit code `1`). Целевые спеки синхронизированы с решением владельца, но архитектурная реализация и готовность не подтверждены.
- Проверенная волна record store: нейтральные `Record*` DTO/client в `pkg/contracts`; все вызовы record store Website, Content, Forms, Router, Loader, Archive и Media проходят через статически allowlisted DataBus operations и owner scope, привязанный composition root вне JSON payload. Evidence `output/record-databus-20261123-b/`: для `pkg`, `main`, `engines/website`, `engines/content`, `engines/forms`, `engines/loader`, `engines/router`, `workers/archive`, `workers/media` прошли `go test -count=1 ./...`, `go vet ./...`, `go build ./...`; `tests/qa/run-website-red.sh contract` — PASS. Эти результаты покрывают только record-store slice, не весь Core boundary и не release acceptance.
- Coordinator execution follow-up `coordinator-databus-20261123`: Core `TaskExecutor` вызывает generic `OperationExecutor`; `main` маршрутизирует task по статическому DataBus allowlist и отклоняет незарегистрированные child operations до atomic persistence. Новая проверенная Core волна: `go test -overlay=/tmp/website-engine-go-overlay.json ./...`, `go vet -overlay=/tmp/website-engine-go-overlay.json ./...`, `go build ./...` из `core/` и `go test -tags=full -overlay=/tmp/website-engine-go-overlay.json ./...` из `main/` завершились с exit code 0. Регрессионный PostgreSQL сценарий `TestDisposableTaskClaimsAreScopedToAllowedComponents` запускался отдельно, но был `SKIP`: `TEST_COORDINATOR_CLASS_CLAIM_DATABASE_URL` не задан; поэтому SQL class-scope поведение не имеет PASS evidence. `git diff --check` в этом проходе завершился с exit code 0. Это не release acceptance.
- Boundary regression run `core-boundary-class-claim-20261123`: `GOWORK=off GO111MODULE=off go test ./tests/linter` завершился с exit code `1`; первоначальные логи `output/core-boundary-class-claim-20261123/boundary-test.log` и `.exit`, повтор после перевода Website workflow handlers — `after-neutral-task-dto.log` и `.exit`. Повторный целевой прогон зафиксировал 48 прямых Core import findings в runtime modules и 74 Core model findings по engine/worker/component abstractions. Требуемая DataBus изоляция не выполнена; Coordinator всё ещё содержит `WorkerCatalog`/`WorkerRef`, внешние модули продолжают прямые Core imports, production class-pool wiring не готов. Полная acceptance/release не подтверждена.
- Class-scoped claim regression `core-claim-20261123`: только для проверки создан fresh disposable stack `tests/qa/docker/up.sh` (PostgreSQL 16/MinIO, run-scoped project, loopback ports `56432/59990/59991`). `TEST_COORDINATOR_CLASS_CLAIM_DATABASE_URL` был установлен на отдельную свежую БД `zubrium_core_qa_owner_result_core-claim-20261123`; `TestDisposableTaskClaimsAreScopedToAllowedComponents` подтвердил, что archive claim не забирает media task и наоборот. На той же disposable run `TestTaskExecutorCopiesClaimScopeAtConstruction` подтвердил неизменяемую границу пула после мутации исходного config slice. Оба test runs завершились с exit `0`; raw output и команды: `output/core-claim-20261123-class-scope/`. Стек и private env удалены через `tests/qa/docker/down.sh core-claim-20261123`. Это подтверждает только scope isolation; отдельные production pools ещё не включены.
- Нейтральный DataBus task DTO slice `core-boundary-class-claim-20261123`: Website build/media/fork handlers и Archive/Media/Prerender workers принимают `pkg/contracts.OperationTask` и возвращают `OperationOutcome`; operation/component identity остаётся в composition adapter. Проверки сгенерированным overlay прошли: `go test -count=1 -tags=website_acceptance -overlay=/tmp/website-engine-overlay-claim-20261123.json ./workflow` из `engines/website`, `go test -count=1 -overlay=... ./...` и `go vet -overlay=... ./...` из каждого из `workers/archive`, `workers/media`, `workers/prerender`, `go test -count=1 -tags=full -overlay=... ./...`, `go vet -tags=full ./...` и `go build -tags=full ./...` из `main`; Website `go vet ./...` и `go build ./...` также прошли. `git diff --check` прошёл. Это подтверждает только обработчики и DataBus adapter: полная Core boundary regression остаётся красной (54 Core imports, 74 Core engine/worker/component model findings), а release/E2E остаётся непрошедшим.

## Admin UI/UX polish и browser QA (2026-11-23)

### Целевая среда

- Изолированный run `ui-polish-20261122t211824z-b7cfbb`, отдельный checkout `$RUN_DIR/checkout`, Admin `127.0.0.1:49320`, public `127.0.0.1:49321`, S3-boundary proxy `127.0.0.1:49322`, PostgreSQL `49310`, secret-server `49330`. Рабочие порты `8080/5173/55432` не имеют слушателей.
- Перед каждым write-сценарием выполнялся `tests/qa/browser/verify-target.sh` с ожидаемым build identity: PID использует бинарник и конфиг checkout, `/healthz` и `/readyz` дают 204, public listener не экспонирует health. Write-запросы отправлялись только в этот disposable стек.
- Все правки — frontend (`frontend/admin/src`, `frontend/admin/index.html`, `frontend/ui/styles/admin.css`) и frontend-тесты. Generated `pkg/static/dist` пересобран из этого же source. Смежные пользовательские изменения в Go/spec/observability-файлах не изменялись.

### Статические проверки (финальный код)

- Из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `pnpm lint` (`--max-warnings=0`) и `pnpm --filter @website-engine/admin build` — PASS.
- `pnpm --filter @website-engine/admin test` — `61 files / 282 tests passed`, включая regression-тесты unsaved guard, `changesSnapshotOrLocale` контент-редактора и локализацию простых API-кодов.
- `git diff --check` — PASS. Коммит и push не выполнялись: отдельный запрос не поступал.

### Исполненные browser-проверки

- Responsive: светлая тема на `360/390/768/1024/1280/1440/1920 px`, ключевые экраны `/admin/`, `/admin/forms`, `/admin/content`, `/admin/media`, `/admin/site/pages`, `/admin/router/routes`, `/admin/submissions`. Горизонтального переполнения нет; на `360/390` интерактивные цели ≥44 px, включая кнопку закрытия Sonner-toast и «Изменить цель» в Router. Видимый фокус по первому `Tab` и навигация по ссылкам работают.
- Темы: system/light/dark через `ThemeToggle`, `body` background/foreground и `meta[name=theme-color]` соответствуют токенам (`#f7f7f7` / `#141414`); тема сохраняется между переходами.
- Lighthouse на `/admin/forms`: Accessibility `1`, Best Practices `1`, SEO `1`, failures нет. Ранее SEO было `0.75` из-за отсутствующего `meta[name=description]`; описание добавлено в `frontend/admin/index.html`.
- Media: grid/list, поиск с пустым результатом, actions menu и detail/upload dialogs закрываются по `Escape`; мобильные кнопки «Сетка»/«Список» имеют высоту 44 px.
- Forms: создана форма `qa-form`, сохранение прошло; невалидный Go RE2 шаблон теперь показывает локализованное `Некорректный запрос. Проверьте введённые данные.` вместо сырого `invalid_request`; publication host/base path валидируются; unsaved guard проверен обоими путями («Остаться» и «Покинуть страницу»).
- Site editor: создана страница `QA Главная` и снапшот `#1`, вкладки «Фичи»/«SEO» переключаются, dirty-состояние сохраняется при переходе «Настройки» → «Страницы». Guard на смену `snapshot` покрыт unit-тестом (`changesSnapshotOrLocale`).
- Router: создан точный маршрут `qa.example.test/qa` с Core file target, подтверждено диалоговое «Опубликовать файл по маршруту?» → «Сделать доступным»; маршрут показан как `active`, `exact`, `Ветка main · ревизия 1`.
- Submissions: в изолированном стеке фильтр по `form` и `key` меняет URL без дублирования history entries и оставляет список согласованным с выбранной формой.
- Snapshots: `/admin/snapshots/tree` и `/admin/snapshots/registry` открываются с корректными заголовками; single-root дерево показывает созданный снапшот `#1` со ссылками «Страницы», «Состав фичей», «Контент».

### Ограничения и невыполненные проверки

- Content deep QA заблокирован стеком: `PUT /api/admin/content/locales` успешно создал конфигурацию `en-US/ru-RU` (revision 1), но `GET /api/admin/content/locales` после этого стабильно возвращает `503 {"error":"unavailable"}` из fail-closed провайдера Content projections; редактор показывает `Не удалось загрузить локали контента unavailable` с рабочей кнопкой «Повторить». Это ограничение изолированного окружения, не frontend-регрессия; реальные content records/drafts/publication не проверялись.
- Проверка guard при смене `snapshot` в живом UI не выполнена: в стеке только один снапшот. Логика покрыта unit-тестом и остаётся ограниченной проверкой.
- Визуальный осмотр сохранённых скриншотов из `$RUN_DIR/qa-shots/` не выполнялся: текущая модель не читает изображения. QA опирается на DOM/accessibility/ComputedStyle/console/Lighthouse-проверки.
- `MediaFilters` остаётся не подключённым: `/api/files` принимает только `q/group/cursor/limit`, а фильтры `type/status/sort/from/to/min/max` требуют изменения API-контракта вне frontend-волны.
- В консоли изолированного стека присутствуют ожидаемые `503 /api/admin/content/locales` и `404 /api/admin/model` из недоступных в этой композиции engine capabilities; ошибок JS-рантайма в проверенных UI-потоках не зафиксировано.
- Integration/release/cybersecurity проверки в этой волне не запускались; коммит и push не выполнялись. Файл `frontend/admin/src/pages/site/RouterPage.tsx` не достижим из роутера, поэтому добавленные ранее контролы приоритета/родителя откачены, а не перенесены в рабочий UI без требования задачи.

## Кэширование и прогрев (2026-11-23)

Целевой контракт: [SPEC-caching](../spec/architecture/SPEC-caching.md). Исполняемые сценарии `CACHE-001..CACHE-012` определены в [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md). Все они находятся в статусе «планируется/ожидает проверки»: в этом отчёте нет evidence реализации кэша, Vary/сжатия, метрик кэша и прогрева до Ready. Метрики кэша перенесены в [SPEC-observability](../spec/observability/SPEC-observability.md), а наблюдатель получил поддержку gauge и redaction ключей кэша; сами кэши эти серии ещё не испускают. Старые проходы ниже к новой функциональности не применяются. Статус `pass` будет выставлен только после реального прогона с зафиксированными командами, целевым стеком и ограничениями.

## Конкуренция и устойчивость к нагрузке (2026-11-23, исторический проход)

Целевой контракт: [SPEC-concurrency](../spec/architecture/SPEC-concurrency.md). Исполняемые сценарии `CONC-001..CONC-012` определены в [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md). В этом историческом проходе не было evidence нагрузочного прогона, backpressure, шардирования, zero-copy, coalescing, изоляции worker-пулов или метрик конкуренции; `CONC-008` оставался blocked до отдельного согласования class-scoped `Claim`. Актуальный результат по `CONC-008` — в разделе «Цикл CONC-008» выше; остальные сценарии по-прежнему имеют уровень unit/contract без нагрузочного p99-прогона.

## Observability (2026-11-23)

### Фактически запущенные проверки

- Из каталога `pkg/`: `go test ./...`, `go vet ./...`, `go build ./...` и `go test -race ./observability/...` — PASS. Unit-тесты покрывают JSONL-сигналы с trace/span correlation, derived-key/value redaction (включая ключи кэша, file/principal ID, URL, locale и user-agent), низкокардинальные метки операций, gauge с last-write-wins, W3C traceparent, HTTP route/healthz поведение и OTLP-экспорт в `httptest`-сервер (logs/traces/metrics, parent propagation, error status).
- Из каталога `presentation/admin/base`: `go test ./...`, `go vet ./...` — PASS.
- Из каталога `core` с централизованным overlay: `go test ./infrastructure/objectstore/...`, `go vet ./infrastructure/objectstore/...`, `go build ./...` — PASS. Regression-тест подтверждает, что точный `InsecureAllowedHosts` принимается, а wildcard/постфиксные значения отклоняются.
- Из каталога `main`: `go test ./...`, `go vet ./...`, `go build ./...`, `go build -tags full ./...` — PASS; `go test ./internal/config` — PASS (разрешение observability-блока, отказ не-loopback HTTP S3 без явного allowlist).
- `--check-config` с фиктивными env и `-tags full` — `configuration is valid` (exit 0) для `noop`, `file`, `otel`. Отклонены (fail-fast): неизвестный `mode`, пустой `file.directory`, пустой `otlp.endpoint`, невалидный `min_level`, пустое/битое содержимое переменной `headers_env`, невалидное имя переменной `headers_env`. Отдельно подтверждено, что отсутствие `headers_env` допустимо, если кастомные заголовки OTLP не нужны.
- `docker compose --profile observability config --quiet` с фиктивными значениями обязательных env — exit 0; Collector-конфигурация прошла `otelcol-contrib validate`.
- Образ приложения собирается `docker compose --profile observability build zubrium-obs` с `BUILD_TAGS=full`; внутри образа `atlas version v1.3.0` и бинарник `zubrium` доступны.

### Блокирующие ограничения runtime-прогона

- Первый старт приложения в file-режиме завершился до HTTP-листенера: `start component "core": unencrypted S3 endpoints must use loopback`. Причина — собственный баг конфигурации: `s3_insecure_allowed_hosts` валидировался, но не передавался в `core/presentation.S3Config` и `objectstore.S3Config`. Исправлено коммитом `b3e7822` с regression-тестом.
- Второй старт завершился на `RequirePrivate`: disposable `alpine/minio:RELEASE.2025-10-15T17-29-55Z` (development-сборка) возвращает `NotImplemented` на `GetPublicAccessBlock`, хотя Core обязательно проверяет все четыре флага public access block. Та же ошибка воспроизведена напрямую через `aws s3api get-public-access-block`; `GetBucketPolicyStatus` в этом образе работает. Для полного прогона нужен S3-совместимый сервер с поддержкой public access block (в локальном кэше доступен `motoserver/moto`, альтернатива — LocalStack).
- Загрузки `public.ecr.aws/zinclabs/openobserve:v1.1.2` и `localstack/localstack:latest` прерваны остановом Docker daemon (`Command was killed: the process daemon stopped`). Статус `OBS-006` и все OpenObserve-зависимые проверки остаются ожидающими.

### Ожидающие проверки

Следующие пункты остаются «планируется/ожидает проверки» и не имеют статуса `pass`:

- Старт isolate-стека и исполняемые input/result сценарии `OBS-001..OBS-010` из [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md): healthz без лога но с метрикой, 409 + span error, `trace_id` в ответе/логах, worker identity, три JSONL-файла с ротацией, записи в OpenObserve, невалидный traceparent, недоступность Collector, redaction в живом API, параллелизм и shutdown.
- Реальные порты 18080/18081/15080/14318/14317/15900/15901/15432, tmpfs для PostgreSQL/MinIO, named volume только для OpenObserve, миграции PostgreSQL отдельной ролью и запись в MinIO.
- OTel Collector pipelines logs/traces/metrics и наличие сигналов в OpenObserve; SQL/PromQL примеры из [README deploy/observability](../deploy/observability/README.md) не выполнялись.
- Отказные случаи в живом стеке: переполнение файлов, сброс/завершение компонентов, таймаут экспорта, отсутствие утечки тела формы/PII в атрибуты.

`pass` по любому ожидающему пункту будет выставлен только после реального прогона с зафиксированными командами, целевыми сервисами, ограничениями и результатами. Наличие спецификации, compose-файлов, конфигурации Collector или unit-тестов не подменяет acceptance-прогон.

## Текущий проход — глобальные Forms, вложенные страницы Admin и публичный submit (2026-11-22)

### Подтверждённые проверки

- Изолированный стек `live-forms-20261122t220232z-0d0fb9` пересобран из этого checkout: `main` → `go build -tags=full ./cmd/zubrium`, Admin → `pnpm --filter @website-engine/admin build`. Live-процесс перезапущен на чистом (не `.debug`) binary; `verify-target.sh` повторно прошёл: рабочие порты свободны, PID 49267/49268 использует checkout binary и `live.yaml`, `/healthz` и `/readyz` отдают 204, build identity совпадает с ожидаемым.
- Browser QA на verification-цели `127.0.0.1:49267`: список `/admin/forms` показывает все созданные формы; переход по строке открывает `/admin/forms/:formId`; `/admin/forms/new` открывает отдельную страницу создания; через неё создана третья форма `qa.third-form-20261122` (ID `19be8082-f1b7-4b31-ae7f-27209a9af721`), после сохранения выполнен редирект в редактор конкретной формы и новая запись появилась в списке. Ранее в этом же стеке подтверждены duplicate key error, публикация v1, отсутствие автообновления pinned-привязки и ручное обновление маршрутов до v2 через UI.
- Публичный submit после исправления Router: `POST /forms/contact.qa-forms-2026` → 201 с cookie, затем `POST .../submit` с тем же cookie и `Idempotency-Key` → 201, receipt получен. Регрессия с exact-route, являющимся префиксом запроса, устранена; `TestFindMatchSkipsSiblingsThatArePrefixesOfTheRequest` зелёный.
- Submissions: новая заявка после перезапуска получила корректный `form_key`; в Admin `/admin/submissions?form=...&key=contact.qa-forms-2026` она найдена, а `key=missing_field` даёт пустой список. Старая заявка, созданная до исправления, не содержит `form_key` и в фильтр по ключу не попадает — это ожидаемое поведение для исторических записей текущего изолированного стека.
- Дополнительный battery публичных запросов на изолированном listener `49268` с `Host: forms.example.test`: `POST /forms/<key>` → 201 и cookie `Path=/; HttpOnly; SameSite=Strict; Max-Age=1799` (без `Secure`, стек HTTP), GET без cookie → 404, GET с cookie → 200, DELETE → 204 с cleared cookie `Max-Age=0`; `POST` с телом `{}` → 413 из-за `MaxRequestBytes: 1`; submit без валидного `Idempotency-Key` → 400, с ключом → 201, повтор с тем же ключом и телом → тот же receipt ID, с тем же ключом и другим телом → 409; upload PDF → 201 с `attachment_id`, повтор с тем же ключом → 200 и тот же ID, поле `message` или MIME `text/html` → 400, запрос без cookie → 404. Host `127.0.0.1:49268` отклоняется до обработки запроса (404). Все созданные заявки и файлы существуют только в disposable стеке.
- Статические проверки после последних правок: `go test/vet/build ./...` по каждому из 19 модулей `go.work` с централизованным overlay — PASS; `go test -tags=full` OpenAPI parity `TestOpenAPIOperationsMatchRegisteredHTTPHandlers` — PASS; `make lint-core-boundary` — PASS; `git diff --check` — PASS.
- Admin из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (49 файлов, 241 тест) и `build` — PASS; ESLint по всем изменённым Admin-файлам с `--max-warnings=0` — PASS.

### Найденные и исправленные дефекты текущего прохода

- **Router `FindMatch`:** exact-route с путём, являющимся префиксом запроса, приводил к `ErrInvalid` вместо продолжения поиска; добавлен regression test.
- **Forms submit:** при создании submission не сохранялся `form_key` из схемы, из-за чего фильтр заявок по ключу всегда возвращал пустой результат; ключ берётся из загруженной схемы сессии, добавлен assertions в `TestPublicFormSessionValidatesAndAcceptsImmutableSubmission`.
- **Admin Forms:** страница `/forms/new` не имела явного creating-режима при прямом рендере; введён prop `creating`, а detail-страница выводит путь публикации по умолчанию и версию в описании.
- **Admin Submissions:** поле фильтра называлось «Ключ значения» и не соответствовало контракту `key` (ключ формы); переименовано в «Ключ формы».
- **Admin publication warnings:** `formPublicationWarnings` читал только плоский `schema_version`; для уже существующего `public.form-upload` с версией во вложенном `form` предупреждение отсутствовало. Добавлено извлечение binding по той же логике, что и в `routeSuggestions`, и regression-тест на stale nested binding.
- **WEB-023 aggregation:** тест `TestManifestAcceptsRouteFormatStringArgument` не вызывался агрегатором `web023ABIAndManifestValidationAreStrict`; вызов добавлен, чтобы route-аргумент реально входил в acceptance-прогон.

### Ограничения текущего прохода

- Browser QA route-аргумента в site editor остаётся заблокированным: `GET /api/admin/features` в живом стеке возвращает `503 {"error":"unavailable"}` из fail-closed stub `engines/website/application/unavailable.go`; конфигурация `feature_import` каталог не чинит. UI-логика `format: "route"`/`source: "route"` проверена статическими тестами, но не реальным feature-каталогом.
- E2E-ledger содержит только `E2E-PUB-001` и `E2E-RET-001`; глобальные формы, вложенные Admin-страницы, фильтр `form_key`, route-аргумент и host без порта покрыты Go/frontend-тестами и browser QA, но не являются отдельными исполняемыми E2E-сценариями.
- Полный публичный E2E routing/publish, release checks и cybersecurity audit в этом проходе не запускались. Коммит и push в текущую ветку выполнены по запросу пользователя.

## Ранее зафиксированный проход — durable Website build, prerender sandbox, integration и browser QA (2026-11-22)

### Подтверждённые проверки

- `python3 tests/qa/run-test-suite.py ci --integration --report output/test-report-web022.json`: exit 0; `gates: 68 pass, 0 fail, 0 blocked`; `scenarios: 30 pass, 0 fail, 2 not_run, 0 blocked`; `coverage 32/32 executable = 100.00%`. Полный `tests/qa/run-website-red.sh integration` на одноразовых PostgreSQL/S3: exit 0, включены REAL `WEB-012`, `WEB-018`, `WEB-019`, `WEB-020`, `WEB-021`, `WEB-022`, `CORE-001`. Два публичных E2E-сценария остаются `not_run` и исключены только из знаменателя executable coverage.
- `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.qa.test_run_test_suite tests.qa.test_scenario_ledger`: 28 тестов, OK. `python3 tests/qa/scenario-ledger.py tests/qa/website-scenarios.json`: `denominator: 29`, `covered: 29` (100.00%), `gaps: 0`, `blockers: 0`, `execution_pass: 29`. Core ledger: `1/1`, 100.00%.
- `tests/qa/run-website-red.sh contract`: exit 0 (ledger validator, Go contract-сценарии, `TestWebsiteAdminHTTPAcceptanceWEB017`, отдельный Vitest UI-файл). `PYTHONDONTWRITEBYTECODE=1` используется для локальных unittest-прогонов, чтобы не создавать `__pycache__` в исходниках.
- RED/GREEN-тесты durable build и owner-scoped result прошли с централизованным overlay: `TestWebsiteBuildPageHandlerEmitsPinnedPrerenderChild`, `TestWebsiteBuildPageHandlerRejectsInvalidTaskAndPayload`, `TestWebsiteBuildWorkflowResolvesPrerenderEmissionKey`, `TestWebsiteBuildWorkflowFailsWhenPrerenderEmissionCannotBeResolved`, `TestWebsiteLocaleCatalogPinsRevisionOnlyForConfiguredLocale`, `TestWebsiteBuildWorkflowResolvesOnlyOwnedEmissionKey`, `TestDisposableOwnerScopedTaskResult/CORE-001`.
- `WEB-019` RED зафиксирован на той же disposable PostgreSQL/S3 до реализации capability: из `main` выполнялся `go test -count=1 -v -tags=website_acceptance -overlay="$TMPDIR/web019-overlay.json" -run '^TestWebsiteMediaDerivativeAcceptance$' ./cmd/zubrium` с env одноразового стека; exit `1`, `--- FAIL: TestWebsiteMediaDerivativeAcceptance/WEB-019`, failure line `website_media_derivative_acceptance_test.go:75`: `WEB-019 RED: pinned media derivative capability is unavailable: Website dependency unavailable`. GREEN после реализации: exit `0` и точная строка `--- PASS: TestWebsiteMediaDerivativeAcceptance/WEB-019`; тот же subtest проходит в полном integration-раннере.
- `WEB-020` RED зафиксирован на той же disposable PostgreSQL/S3 до рабочей prerender-композиции: из `main` выполнялся `go test -count=1 -v -tags=website_acceptance -overlay="$TMPDIR/web020-overlay.json" -run 'TestWebsitePrerenderAcceptance/WEB-020' ./cmd/zubrium`; exit `1`, `--- FAIL: TestWebsitePrerenderAcceptance/WEB-020` ( trusted capability ещё не доводила job до `ready`). GREEN после реализации: exit `0` и точная строка `--- PASS: TestWebsitePrerenderAcceptance/WEB-020`; тот же subtest проходит в полном integration-раннере. В логе зафиксировано in-container доказательство: `seccomp=2 cap_eff=0000000000000000 no_new_privs=true cpu=100000 100000 memory=268435456 pids=64 rootfs=ro network=denied browser="Chromium 124.0.6367.78 Alpine Linux"`.
- `WEB-021` RED зафиксирован до trusted fan-out capability: `go test -tags=website_acceptance -overlay="$TMPDIR/web021-overlay.json" -run '^TestWebsitePublicationFanoutAcceptance$' ./cmd/zubrium` из `main` с env disposable стека; exit `1`, failure line `website_publication_fanout_acceptance_test.go:18`: `WEB-021 RED: trusted publication fan-out service is unavailable: publication fan-out capability is unavailable`. RED-заглушка заменена реальным integration-тестом. GREEN после реализации: exit `0` и точная строка `--- PASS: TestWebsitePublicationFanoutAcceptance/WEB-021`; тот же subtest проходит в полном integration-раннере (`output/test-logs/website-integration.log`).
- `WEB-022` RED зафиксирован до реализации: компиляция integration-теста падала с `undefined: websiteapi.RetirePageRequest` (exit `1`, лог RED-contract), а prerequisite-тест композиции — `TestFullProfileRegistersDataModuleIdentities` с отсутствующими identity для `worker.archive`, `worker.media`, `worker.prerender`, `publication` (exit `1`). GREEN: та же проверка identity проходит (exit `0`), а REAL disposable subtest даёт точную строку `--- PASS: TestWebsitePageRetirementAcceptance/WEB-022` (exit `0`, лог `web022-integration-green.log`). Регистрация дескрипторов исправлена в workers, buildprofile и сгенерированных профилях без изменений Core.
- Все 19 модулей `go.work` по отдельности прошли `go test ./...`, `go vet ./...`, `go build ./...`; `main` дополнительно прошёл `go test/vet/build -tags=full ./...`, включая компиляцию root handler `engine.website/build-page/v1` и fail-closed child handlers.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `pnpm lint`, `pnpm --filter @website-engine/admin test` (48 файлов, 218 тестов) и `pnpm --filter @website-engine/admin build` — PASS. Build перезаписал `pkg/static/dist` и ассеты.
- `make lint-core-boundary`: exit 0. `TestOpenAPIOperationsMatchRegisteredHTTPHandlers`: PASS для всех 7 OpenAPI-документов. `git diff --check`: exit 0.
- Browser QA выполнен в отдельном checkout с build identity current run; disposable PostgreSQL и `moto` S3 использовались только для изолированного стека. Admin: `/admin`, `/admin/content`, `/admin/forms`, `/admin/submissions`, `/admin/media`, `/admin/coordinator/workflows`, `/admin/router/routes`, `/admin/site/pages`, `/admin/snapshots/registry`, `/admin/snapshots/tree`, `/admin/settings`, `/admin/access/users` — desktop 1440/light, без горизонтального overflow. Responsive 360/390/768/1024/1280/1920 px на репрезентативных экранах, темы system/light/dark, цикл `ThemeToggle`, видимый фокус по первому `Tab`, диалоги с закрытием по `Escape`, переключатель `Сетка/Список` — проверены. `/healthz` отдаёт ожидаемый `X-Zubrium-Build-Identity`. Публичный listener на проверенных путях отдаёт `404 {"error":"not_found"}` и не экспонирует `/healthz`, `/readyz` или Admin session.
- Write-проверка browser QA выполнялась только в изолированной disposable БД: создание страницы через Admin API вернуло `201`, последующее чтение `/api/admin/pages`, `/api/admin/snapshots` и `/api/admin/snapshots/{id}` вернуло `200`; страница отрисовалась в редакторе. Credential и приватные env в отчёт не попадают.

### Фактическое состояние durable build

- Core предоставляет owner-scoped `WorkflowClient.GetTaskResult`; владелец, instance lease и config epoch берутся только из сконструированного клиента. `CORE-001` подтверждает базу данных, миграции и owner-scoped чтение результата в живом PostgreSQL.
- `engines/website/workflow` регистрирует immutable root-операцию `engine.website/build-page/v1`, проверяет strict typed payload и эмитит child `worker.prerender/render-page/v1` с `render-plan/v2`, `plan_digest` и стабильным `prerender:<build_id>` emission key.
- `main/internal/composition` инъектирует workflow adapter, locale catalog из доверенного Content service context и константы операции/версии; в не-full профилях capability не инъектируется. Условный порядок ребер full profile учитывает `admin.files → engine.forms/engine.website/engine.router` и `engine.router → engine.forms/engine.content`.
- Website разрешает сгенерированный Core `Task.ID` по owner-scoped emission key, закрепляет его в `website/builds` и читает результат child только через `GetTaskResult`. Artifact manifest фиксируется в job только после typed validation.
- `WEB-012`: `BeginFeatureImport` фиксирует CAS-запись и idempotency receipt по request-only fingerprint, стартует owner-scoped `feature-import/v1` с минимальным payload и reconcile-ит durable результат. Feature version становится `ready` только после проверенного результата archive worker и подтверждённого удаления source file; повтор task не создаёт дублирующих output. Настоящий `archive.Handler` включается только при явной trusted-конфигурации full profile, остальные профили остаются fail-closed.
- `WEB-019`: каноническое описание `x-media`, его digest и `WorkContractVersion = 2` живут в `pkg/contracts/media` и используются Website и `workers/media`. Website root workflow эмитит по одному дочернему заданию на rendition; job использует CAS, workflow idempotency и per-child emission keys, а поле становится `ready` только после проверки всех immutable outputs. Source file и прежнее значение поля сохраняются при отказе/отмене, derivatives наследуют groups и FileSet. Процесс запускается только через Docker/Linux sandbox с seccomp, AppArmor, cgroups CPU/memory/pids, `read-only` rootfs, запретом сети и writable staging только через Core; тест подтверждает эти ограничения изнутри контейнера. Trusted processor version и sandbox-конфигурация приходят из startup configuration, клиентский override профиля отклоняется типизированным API.
- `WEB-020`: версионированный trusted input (`trusted-build-input/v1`) собирается поздно связанным Content capability только из доверенного service context; `render-plan/v2` и `website.build-result/v2` живут в `pkg/contracts/prerender`. Build identity включает snapshot revision/digest, catalog revision, content publication revision, locale selection, feature package и dependency lock digests, runtime ABI/APIVersion. `worker.prerender` исполняет реальный headless Chromium только внутри kernel-enforced Docker/Linux sandbox (seccomp, zero capabilities, no-new-privs, cgroups CPU/memory/pids, read-only rootfs, сетевой deny), пишет оба артефакта только через Core и возвращает manifest с digest. Website проверяет digest, versioned envelope, Core-файл по checksum/size/content-type и manifest перед переходом job в `ready`; stale result, дубликаты полей, digest mismatch и loopback/private/link-local/metadata адреса отклоняются, prior artifact и partial ready не создаются. Fail-closed без trusted sandbox configuration.
- `WEB-021`: `main/internal/publication` — доверенный composition-компонент с durable run/target records в Core engine-data; route/page/locale registry задаётся startup-конфигурацией и доступна только через operation `main.publication.publish` в static allowlist. Fan-out использует полный `TrustedInput` WEB-020, independent per-`(route, locale)` Router CAS, partial success без rollback, retry только failed targets и повторную проверку digest перед активацией. Website не выбирает route и не вызывает Router напрямую; stale build result и CAS conflict не меняют уже активный artifact. Router хранит логическую revision в payload и использует Core revision как свежий CAS-token; locale выбирается только из cookie, `Accept-Language` и server default.
- `WEB-022`: `main/internal/retirement` — доверенный composition-компонент с durable job/receipt/tombstone records в отдельном Core namespace `main/retirement` со scope `branch`. Запуск через `DELETE /api/admin/pages/{pageID}` с `If-Match`/`Idempotency-Key` возвращает `202 + Location`, статус читается через `GET .../retirement/{jobID}`; оба выхода доступны только как process-local операции static allowlist. Route target'ы задаются server-owned конфигурацией, withdrawal — независимый Router CAS (`GetRoute` + `DeleteRoute`), `ErrNotFound` идемпотентен. Страница переходит `active → retiring → retired` только после всех успешных withdrawal; partial failure оставляет только неуспешный target retryable. Snapshots, Content, Forms и Core-файлы не удаляются. Admin показывает destructive confirmation, lifecycle/revision/job состояние, loading/error/saving и polling с отменой.

### Найденные и исправленные дефекты текущего прохода

- **Full profile:** кондиционально упорядоченные ребра и созданный до финальной активации `NewWorkflowClient` устранили отказ живой композиции; компоненты без асинхронных ошибок получили открытый failure stream вместо ошибочно фатального закрытия канала.
- **Admin notifications:** same-origin `EventSource` не шлёт `Origin`; добавлен `TrustedReadOrigin` с проверкой `Sec-Fetch-Site: same-origin` и разрешённого `Referer`, write-запросы по-прежнему требуют точный `Origin` и CSRF. SSE держится открытым без console errors.
- **Website persistence:** `ListSnapshots` без `page_id` теперь возвращает снимки всех page trees вместо пустого результата; добавлен `TestListSnapshotsWithoutPageFilterListsEveryPageTree`.
- **Website/Core revisions:** декодеры Website сравнивали локальный `index.revision` с глобальным `engine_data.revision_seq`, из-за чего созданная страница возвращала `201`, но последующие чтения давали `503`. Инвариант заменён на проверку положительной ревизии в index; добавлен `TestCreatePageSurvivesCoreGlobalRevisionSequence` с эмуляцией глобальной sequence.
- **Site editor payload:** пустая страница создавалась с `metadata: {}`, который Admin отвергал как несоответствующий page-snapshot контракту; пустой payload теперь содержит `formatVersion: 1` и `siteName`.
- **Admin responsive:** `span.sr-only` в action column таблицы расширял layout viewport до `innerWidth=503` при `client=390`; `.table-wrap` получил `position: relative`, повторная проверка на 390 px дала `scroll=390`, `inner=390`.
- **Admin content:** запрос locale config больше не выполняется без выбранного snapshot, что убирает ожидаемый `404 /api/admin/content/locales` на пустом экране.
- **Profile/linter drift:** секция `composition` профиля `full.yaml` и компонент `composition.retirement` добавлены в парсер, registry и проверку imports boundary-линтера; `make lint-core-boundary` снова проходит.

### Ограничения текущего прохода

- `WEB-012`, `WEB-019`, `WEB-020`, `WEB-021` и `WEB-022` исполнены на disposable prerequisite и переведены в `pass`. `WEB-021` проверен с реальным fan-out, включёнными build failure/CAS conflict и stale-result защитой, но не является полным публичным E2E прогоном многоязычного routing. `WEB-022` проверен с реальным Router CAS, fault injection, retry, idempotency и отказом reuse, но не является полным публичным E2E retirement-прогоном через внешний proxy.
- `moto` выбран для browser QA, потому что MinIO в этом стеке возвращает `GetPublicAccessBlock ... NotImplemented`, а Core `RequirePrivate` требует этот API. Реальные S3-провайдеры в browser QA не использовались.
- `GET /api/admin/model → 404 {"error":"not_found"}` и `GET /api/admin/features → 503 {"error":"unavailable"}` остаются ожидаемыми для пустого реестра; UI показывает корректные empty/error состояния. Это не считается пройденной приёмкой соответствующих модулей.
- `/admin/coordinator/workflows` один раз завис при быстром sequential navigation; свежий reload, network и snapshot прошли чисто. Явный product-дефект не воспроизведён, случай зафиксирован как незавершённый harness/browser flake.
- Полный публичный E2E routing/crash-recovery не прогонялся. `WEB-019` и `WEB-020` проверили media/prerender supervisor в Docker/Linux sandbox на disposable стеке с реальным headless Chromium; `WEB-021` и `WEB-022` проверили durable publication fan-out и page retirement в живом integration-стеке с fault injection, но не заменяют отдельный публичный E2E.
- Release checks и полный cybersecurity audit в этом проходе не выполнялись; blocked сценарии остаются видимыми в отчёте и `diagnose`.

## Текущий проход — изоляция `main`, профили и runtime-интеграции (2026-11-21)

### Подтверждённые проверки

- `make lint-core-boundary` PASS: `Architecture boundaries OK: Core, cross-engine, runtime bus and compiled profiles checked across 19 repository-local Go modules.` Линтер в `tests/linter` проверяет импорты, stale/невалидные YAML-профили и сгенерированные файлы, прямой вызов raw `Dispatch` вне `main`, а также новые внутренние импорты engines из внешнего Presentation (для четырёх существующих legacy-импортов сохранён явный baseline).
- Из корня PASS: `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-full`, `make test-router`, `make test-dev-fixtures`. Все 19 модулей `go.work` отдельно прошли `go test ./...`, `go vet ./...` и `go build ./...`.
- Новые package-тесты PASS: `pkg/contracts`, `main/internal/bus` (scoped streams, consumed/closed refs, workflow operations, published-content operation), `main/internal/composition` (Content projection, profile composition, workflow wiring, чтение Content только через typed bus operation), `presentation/router/publiccontentruntime` (GET/HEAD, ETag/304, server-side route config, отказ visitor input).
- OpenAPI `spec/presentation/openapi/router.yaml` и `tests/ACCEPTANCE-MATRIX.md` синхронизированы с `public.content-runtime`: только GET/HEAD, `page_snapshot_id`/`locale` из server config, strict config schema без неизвестных полей, `no-store`, quoted strong ETag и 304 по `If-None-Match`. Контрактный тест Router прошёл в составе `make test`.
- Публичный Content runtime не хранит прямой ссылки на Content engine: Router-side adapter вызывает запечатанный dispatcher через статическую операцию `content.published.read`; allowlist операции включается только при выбранном `public.content-runtime`.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — PASS.

### Ограничения текущего прохода

- Worker handlers скомпилированы и выбираются профилем, однако archive/media/prerender остаются fail-closed без server-side execution configuration; реальная изоляция Media runner и prerender pipeline не реализованы в этой задаче.
- Full-профиль проходит сборку и статические тесты композиции, но не прогонялся против живого PostgreSQL/S3 и реальных feature builds. Trusted Content projections проверены unit-тестами на stub Website API.
- `pnpm lint` в `frontend/` падает до проверки кода: отсутствует каталог `pkg/runtime/scripts`. Прямой `npx eslint --config eslint.config.js admin` даёт две существующие ошибки `react-hooks/exhaustive-deps` в `MediaPickerDialog.tsx` и `MediaPage.tsx`; ESLint-конфигурация вне зоны изменений.
- Integration-, browser- и release-проверки, а также disposable PostgreSQL/S3 acceptance в этом проходе не запускались; их статус не меняется на `pass`.
- Коммит и push выполняются только по прямому запросу пользователя после проверки `git status`.

## Ранее зафиксированный проход — удаление мультисайтности (2026-11-21)

### Подтверждённые проверки

- Все 19 модулей `go.work` отдельно прошли `go test ./...`, `go vet ./...` и `go build ./...`: `core`, `engines/*`, `workers/*`, `presentation/*`, `pkg`, `main`.
- Из корня повторно прошли `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures`, `make lint-core-boundary`.
- Admin SPA из `frontend/`: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — PASS.
- OpenAPI-контракт и маршруты: `TestOpenAPIOperationsMatchRegisteredHTTPHandlers` проходит; `spec/presentation/openapi/website.yaml` содержит только плоские `/api/admin/...` пути без site-сегмента.
- `python3 tests/qa/scenario-ledger.py tests/qa/website-scenarios.json`: 29 сценариев `WEB-001..WEB-029`, `covered: 14`, `execution_pass: 11`, `execution_fail: 3`, `not_run: 9`, `blocked: 6`.
- `tests/qa/run-website-red.sh contract`: Go-часть (ledger + contract-сценарии) PASS.
- Поиск `site_id|siteID|SiteID|site_ids|/api/admin/sites|sites.yaml` по репозиторию (кроме `node_modules`/`dist`) не дал следов в коде, контрактах и сценариях; в конфиг-тесте осталась только негативная проверка, что `website.site_ids` больше не принимается.

### Ограничения текущего прохода

- `tests/qa/run-website-red.sh unit`: 11 сценариев pass, 3 сценария fail — `WEB-004` (stale CAS), `WEB-006` (relation traversal binding), `WEB-008` (route field в template). Те же три провала воспроизведены на чистом `HEAD` с тем же overlay; это pre-existing red-сценарии, не регрессия удаления мультисайтности.
- `tests/qa/run-website-red.sh contract` завершается ненулевым кодом из-за отсутствующего UI-селектора `tests/frontend/admin/src/pages/site/website-acceptance.test.tsx`; этого файла нет и на чистом `HEAD`. Go-часть контрактного прогона проходит.
- `pnpm exec eslint admin/` падает на два существующих inline-disable для не зарегистрированного правила `react-hooks/exhaustive-deps` в `MediaPickerDialog.tsx` и `MediaPage.tsx`; ESLint-конфигурация вне зоны изменений.
- Integration-прогон с disposable PostgreSQL/S3, browser- и release-проверки в этом проходе не запускались; `DELETE /api/admin/pages/{pageID}` остаётся ожидающей операцией в контрактном тесте.

## Ранее зафиксированный проход — Core и границы модулей (2026-11-21)

### Подтверждённые проверки

- Полный набор Core-проверок прошёл через `QA_ENV_FILE=<private> tests/qa/run-core-acceptance.sh run`: из `core/` выполнены `go test -count=1 -overlay=<generated> ./...`, `go vet -overlay=<generated> ./...` и `go build ./...`. Скрипт берёт DSN только из приватного env-файла, значения в отчёт не попадают.
- Acceptance выполнялся на отдельном disposable PostgreSQL-контейнере `core-hardening-7d86bd2` (tmpfs, 27 task-specific БД `zubrium_core_qa_*`, снимок Atlas CLI зафиксирован на v1.3.0). Перед финальным прогоном БД пересозданы. Проверены чистый baseline/контрольную сумму и повторный startup, ранний отказ на грязной схеме без DDL, runtime-привилегии и ограничение роли, Core v2 session/file/access/cache, Coordinator lifecycle и failure injection, engine-data persistence. S3-round-trip прошёл через локальный `s3mock`; внешние S3endpoint/credentials не использовались.
- `make lint-core-boundary` прошёл: `Architecture boundaries OK: Core has no repository-local module dependencies; checked Core API and cross-engine imports across 19 repository-local Go modules.`
- Denominator Coordinator зафиксирован в `tests/qa/core-coordinator-scenarios.json`: все 16 сценариев имеют status `covered` и fresh evidence; `TestDisposableCoordinatorLifecycleSuite` прошёл 8/8 подтестов на отдельной свежей БД.
- Website: `go test`, `go vet` и `go build` из `engines/website` прошли с централизованным overlay после исправления порядка валидации курсора и test fixture. Forms, Loader, Router, Archive, Media, Prerender, presentation/router, admin/sites, admin/files, admin/ui, admin/forms, admin/router и `pkg` прошли `go test` с overlay.
- Admin SPA: `pnpm --filter @website-engine/admin typecheck`, `test` и `build` прошли.
- Корневой `Makefile` — тонкий wrapper над `main/Makefile`; модули остаются изолированными, композиция живёт только в `main`.
- Sweep до ABI-правок: `make test`, `make vet`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures` и `make lint-core-boundary` из корня — все с exit code 0. Admin SPA: `pnpm --filter @website-engine/admin typecheck`, `test` (47 файлов, 213 тестов) и `build` — все PASS.
- После ABI-правок повторно пройдены из корня: `make lint-core-boundary`, `make build`, `make build-full`, `make build-router`, `make build-dev-fixtures`, `make test-dev-fixtures` — все с exit code 0.
- Content/locale-refactor ранее собирался: Website-проекции приходят через отдельную trusted-зависимость `ContentSchemas`; `VerifyScope` вызывается в handler и engine, level-проверку выполняет доверенный провайдер/engine, а handler сохраняет класс полученной ошибки. Текущие незавершённые locale-изменения в грязном дереве ломают тесты/vet Content (см. ограничения); ABI-правки их не касались.
- Target ABI `static-lifecycle` реализован в одном Go-пакете `engines/website/features/runtime`: manifest/ABI, ZIP preflight, secret-free context, lifecycle supervisor, dependency lock, publication CAS, artifact validation, fail-closed process executor и встроенный browser host. Прежнее имя `static-lifecycle-v2` отклоняется без compatibility alias как в runtime, так и в Archive.
- Централизованные проверки runtime после правок: `go test -count=3` и `go test -race -count=1` для `engines/website/features/runtime`, `go vet`/`go build` этого пакета, `go test -count=1 -overlay` для `workers/archive`, а также `TestWebsiteScenarioLedger` с 29 WEB-ID — все пройдены. Новые WEB-023..029 имеют `covered`/`pass` и содержат точные исполняемые subtests; формулировки ledger приведены к текущей схеме, новый прогон в этом проходе не выполнялся.
- Disposable контейнер `core-hardening-7d86bd2-core-qa-postgres-1` и его сеть удалены после acceptance; активных сессий в 27 QA-БД перед удалением не было. Контейнер `websiteengine-core-qa-postgres-1` на `127.0.0.1:55432` не изменялся.

### Найденные и исправленные дефекты

- **Coordinator, product:** `Service.Start` оставлял `started=true` после неудачного старта; `validTaskOutcome` проверял лимиты зависимостей по отдельности и пропускал сумму `200+200`. Оба воспроизведены RED-тестами и закрыты GREEN.
- **Engine-data, product:** PostgreSQL-адаптер отвергал легальные точечные `engine_id`, несовместимые с application и Atlas; `Store.List`/`ListExpired` не проверяли keyset-страницу перед возвратом курсора и проекции. Добавлены fail-closed проверки.
- **Website, product:** `ListSnapshots` проверял существование страницы раньше валидации курсора, из-за чего malformed cursor для неизвестной страницы давал `not found` вместо `invalid`; валидация курсора перенесена до lookup.
- **Тестовые фикстуры (не product-дефекты):** неверный namespace `loader.v2` вместо `loader/v2`, строковое сравнение нормализованного JSONB и неквалифицированное имя функции триггера при `search_path=pg_catalog`; в Website-тесте счётчик записей сравнивался с хардкодом вместо начального состояния.
- **Инфраструктура проверок:** в `main/Makefile` исправлены путь boundary-линтера, coverage-guard, `-count=1` и недостающие DSN `TEST_CORE_ENGINE_DATA_DATABASE_URL`, `TEST_COORDINATOR_LIFECYCLE_DATABASE_URL`; добавлен centralized acceptance entrypoint.
- **Композиция `main`:** устаревший alias `routerhttp` в `router_component.go` и несуществующий символ `compiledDevelopmentFixtures` в `devfixtures_enabled.go` блокировали сборку Router/devfixtures; заменены на реальные символы `router`/`DevelopmentFixturesEnabled`.
- **Централизованные тесты контрактов:** исправлены пути к spec/handler-файлам, слишком жёсткая проверка `info.version == 2.0.0`, сравнение catch-all-маршрутов Website и проверка Coordinator-группы через `ValidateBuildPlan` вместо `registerComponents`. Единственное ослабление — явный список двух ещё не подключённых endpoint'ов, описанный в ограничениях ниже.
- **Content:** снят блокер сборки переходом на `StoredWorkspace.PageSelections` и подключением отдельного trusted `SchemaSnapshots`-провайдера вместо повторного использования engine API в качестве источника projections.
- **Content, ревью:** handler превращал любую ошибку trusted-провайдера проекций в `ErrUnavailable` (503), теряя легальные `401/403/404/400`. Класс ошибки сохраняется и маппится `writeEngineError`; добавлен параметризованный регрессионный тест.
- **Content, ревью:** `CanonicalLocale` не проверял лимит `maxLength: 64` из `content.yaml`. Проверка длины канонического BCP-47 тега добавлена после канонизации; регрессионный тест использует валидный 101-символьный тег.
- **Feature runtime, product:** dependency resolver молча пере-resolve'л импортированную запись lock при изменённом digest; теперь несовпадение version/digest/API version отклоняется `ErrLock`. Token values публичного context принимали CSS-инъекцию (`url(...)`, `javascript:` и т.п.); добавлена fail-closed проверка с `ErrUnsupportedToken`. Artifact validation принимала `assets: null`; пустой/invalid assets map отклоняется.

### Состояние на конец прохода

- Все запланированные Core-проверки и сборки, а также проверки нового `static-lifecycle` runtime пройдены. Content-разблокировка включена в sweep; перечисленные ниже ограничения относятся к тому проходу. Коммит и push выполняются по прямому запросу пользователя после текущего прохода.

### Ограничения

- Перечисленные блокеры ABI-прохода (`prefix`, отсутствующий `strings`, неиспользуемый `reflect`, stale-тесты Content) устранены в текущем проходе; `engines/content` и `presentation/admin/content` проходят `go test`, `go vet` и `go build`.
- Host-side проверки `static-lifecycle` не заменяют browser acceptance: реальные CSP `connect-src`, typed request/response, prerender без внешней сети и реальные feature builds не прогонялись. WEB-015 и WEB-016 остаются открытыми в ledger.
- `pnpm exec eslint admin/` не прошёл по двум уже существующим inline-disable: для `react-hooks/exhaustive-deps` не зарегистрировано определение правила в `MediaPickerDialog.tsx` и `MediaPage.tsx`. Это вне зоны Core и не является регрессией текущих изменений.
- Trusted-провайдер проекций подключён только в fixture-ветке композиции. В full-профиле Content API не подключён, поэтому текущее состояние fail-closed; при подключении реального Content engine потребуется внешний `SchemaSnapshots`.
- `spec/presentation/openapi/content.yaml` объявляет обязательный query-параметр `locale` для publish/history, но handler его не использует; для history `?locale=...` отклоняется как `invalid_query` (400). Предшествующее расхождение контракта и реализации не исправлялось.
- Browser- и release-проверки в этом проходе не запускались.
- `DELETE /api/admin/pages/{pageID}` (durable page retirement) остаётся единственным endpoint'ом из этого исторического списка, который не подключён в этом checkout: он вынесен в явный список ожидающих операций в контрактном тесте. `POST /api/locale-preference` подключён и проверен живым E2E-сценарием `E2E-LOC-001`, поэтому больше не считается ожидающей операцией.
- `spec/presentation/openapi/admin-notifications.yaml` использует схему `AdminBearer` и ключ `x-reject-cookie-and-bearer-together`, тогда как остальные контракты используют `BearerCredential` и `x-rejects-cookie-and-bearer-together`. Расхождение не исправлялось: оно вне зоны Core и требует синхронизации с Presentation.
- Профиль `full` выбирает модули, отвергаемые `validateRunnableComposition`; `RegisterWorkerHandlers` и scoped streams не подключены в композиции. Это вне зоны текущей задачи Core.
- Acceptance с PostgreSQL/S3 требует отдельных disposable ресурсов; рабочие инстансы не использовались.
- Публичный Router остаётся за внешним proxy; per-IP rate limiting не реализован в Router и является ответственностью deployer.
