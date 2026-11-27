# QA-отчёт

## Release-проверки без интеграционных флагов

`tests/qa/run-release-checks.sh --report-prefix output/readiness-single-router-static`
завершился с кодом 0. В отчёте 89 успешных гейтов. Из 84 сценариев в
сводке 70 получили pass, 14 остались blocked. Admin Vitest прошёл
284 теста из 61 файла. Изолированная интеграция запускалась отдельно.

## Запуск Router на чистой установке

YAML с `router_api.branch_id` отклоняется. Композиция использует один
listener и системную область хранения Router, publication и retirement.
Проверены main full, Router Engine, pkg и Core cache. E2E на новой БД
запустил full-профиль с одним публичным listener, создал FileSet и
импортировал фичу. Прогон остановился на `503` при создании дочернего
snapshot. Удаление поля из внутренних Router DTO остаётся открытым.

## Router Admin без branch ID

HTTP contract проверяет список, детали, создание и замену цели, а также
отклонение `branch_id` в create request. Router Presentation прошёл
`go test`, `go vet`, `go build`. Admin прошёл typecheck, unit-тесты,
сборку и `pnpm lint`. Проверки одного listener и перехода сохранённых
записей, а также browser QA остаются открытыми.

## Content publication delivery

Full-профиль восстанавливает очередь Content при старте через внутренние
операции DataBus. Подтверждение доставки отделено от результата активации
маршрутов. Unit-тесты проверяют повтор с прежним ключом после потери
подтверждения, продолжение после неподтверждённого запуска, сохранение
завершённого результата с отказами и границу доступа к очереди.
`go test`, `go vet` и `go build` прошли в `engines/content`, `pkg` и
`main` с тегом `full` для `main`. Проверки использовали
`/tmp/content-delivery-overlay.json`. Архитектурный линтер прошёл для 22
модулей. `TestContentPublicationDeliveryRestart` прошёл на отдельной
PostgreSQL с изолированным MinIO. После фиксации workflow тест теряет
ответ, закрывает Core и открывает его заново на той же БД. Повтор
использует исходные ID workflow и корневой задачи; два подтверждения
оставляют очередь пустой и сохраняют историю. Проверка восстановления
после старта full-профиля, параллельной доставки и старых публикаций
без intent остаётся открытой. В новом системном scope повторный
PostgreSQL-прогон прошёл после удаления `branch_id` из payload
publication и retirement. Форму обоих root payload проверяют unit-тесты. Для locale без настроенных маршрутов intent остаётся
pending.

## Publication: согласованность artifact (2026-11-27)

Результат сборки и новая запись artifact сохраняются одним Batch; совпадающий
повтор допустим, расходящийся сохранённый artifact отклоняется до изменения
target. Unit-тесты проверяют конфликт digest, повтор и отказ Batch через тестовое
хранилище. В `main` прошли `go test`, `go vet` с overlay
`/tmp/website-publication-artifact-overlay.json` и `go build ./...`; проверки
composition/publication и сборка также прошли с `-tags=full`. Атомарность и
конкуренция на disposable PostgreSQL в этом проходе не проверялись; WEB-021
остаётся заблокированным.

### Живой E2E после CAS/revision-исправления (2026-11-27)

Подтверждённый прогон: `e2e-20261127T230710Z-ca5cc5`, evidence в
`output/e2e/e2e-20261127T230710Z-ca5cc5/`. Полный sweep
`QA_BROWSER_WORKING_PORTS=8080,55432 bash tests/qa/run-release-checks.sh --with-e2e --report-prefix output/test-report-release-final-e2efix`
завершился кодом 0; отчёт фиксирует `git.head=9c174843`, `dirty=True`,
`89 pass / 0 fail / 0 blocked` гейтов и `70 pass / 0 fail / 0 not_run / 14 blocked`
сценариев при покрытии `70/70` исполняемых сценариев. Порт `5173` не использовался:
явно заданы рабочие порты `8080,55432`, чужие listener'ы не останавливались.

- `prepare-checkout.log`: `HEAD 9c17484390dc, 78 status entries; source unchanged, copy matches` — E2E проверял рабочее дерево поверх `9c174843`, а не только чистый HEAD.
- `setup-baseline.log`: `E2E baseline complete (run e2e-20261127T230710Z-ca5cc5)` — подготовительные публикации урегулированы до старта Playwright, поэтому тест не гоняется с собственной фикстурой.
- `verify-target.log`: слушают только checkout binary/config; `GET /healthz` и `/readyz` Admin вернули `204`, build identity совпал, рабочие порты свободны.
- `playwright.log`: точные строки `PASS E2E-PUB-001`, `PASS E2E-LOC-001`, `PASS E2E-RET-001`, финальные `PASS` и `ok github.com/Zubrium/cdn/tests/e2e 16.385s`, ни одной строки `FAIL`.

Исправление устранило две причины красного E2E: root workflow теперь подставляет
выданный Core artifact ID вместо детерминированного upload request key, а
фикстура фиксирует baseline после завершения подготовительного fan-out.
Результат получен на дереве с параллельной правкой удаления веточной топологии;
последующий коммит этой волны меняет только контрактные тексты, тестовые фикстуры
и сгенерированные Admin-ассеты. Интеграционные `WEB-021`, `WEB-022` и остальные
`blocked`-сценарии этим прогоном не снимаются.

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
Проверка конкурентных транзакций для этого изменения остаётся открытой.

## Website integration

На изолированной PostgreSQL и MinIO прошли `WEB-012` (импорт фичи),
`WEB-018` (границы хранения), `WEB-019` (медиа) и `WEB-020` (prerender).
Оба worker-сценария выполнены с закреплёнными Docker/Linux образами.
Для `WEB-021` и `WEB-022` остаётся ошибка компиляции старых
publication/retirement fixtures после перехода на DataBus и Coordinator.
Для этих двух сценариев runner добавляет тег
`website_orchestration_acceptance`; сбой одного сценария оставляет
остальные запуски доступными и сохраняет ненулевой общий exit code.
В Website ledger 27 pass и 2 blocked из 29.

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

Повтор старой публикации страницы после более поздней публикации другой
страницы той же locale проверяет закреплённую историю и возвращает conflict,
вместо передачи чужой revision в fan-out. Регрессионный unit-тест и `go test`,
`go vet` с `/tmp/content-old-publish-overlay.json`, `go build ./...` для
`engines/content` прошли; проверка на PostgreSQL не выполнялась.

Composition отклоняет publication до записи Content при отсутствии
dispatcher и не считает успешным результат без опубликованной revision.
Unit-тесты подтверждают оба случая; `main` прошёл `go test`, `go vet` с
overlay `/tmp/content-fanout-required-overlay.json`, `go build ./...` и
test/vet composition и сборку с `-tags=full`. Durable доставка при crash после
commit Content и до dispatch остаётся открытой.

Content теперь добавляет pending publication intent в тот же Batch, что
workspace и immutable history; locale, publication revision, model revision и
workflow key передаются из application. Unit-тесты проверяют содержимое intent
и отказ Batch с тестовым хранилищем. Для `engines/content`, `pkg` и `main`
прошли `go test`, `go vet` с `/tmp/content-intent-overlay.json` и `go build`;
для composition прошёл test и сборка `main` с `-tags=full`. Восстановитель,
подтверждение доставки, restart и реальная атомарность на PostgreSQL ещё не
проверены; pending records пока не обрабатываются автоматически.

Repository поддерживает постраничное чтение pending intents и подтверждение
run через CAS. Unit-тест воспроизводит гонку двух подтверждений одного run,
отклоняет подтверждение с другим run и проверяет сохранность истории.
Для `engines/content` прошли `go test` и `go vet` с overlay
`/tmp/content-intent-recovery-overlay.json` и `go build ./...`.
Подключение обработчика и проверка на PostgreSQL остаются открытыми.

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

## Предыдущая браузерная приёмка Admin и Forms

Сценарии `UI-POL-001..010` в [acceptance matrix](../tests/ACCEPTANCE-MATRIX.md)
фиксируют результаты браузерной проверки Admin на предыдущей ревизии.
Они включают список и создание Forms, переход в редактор, ошибки
валидации, публикацию схемы с редактируемым путём и подтверждение
публикации Router. Изолированный Forms-прогон также проверил сохранение
pinned schema revision у действующей привязки и ручное переключение на
новую версию. Эти результаты относятся к проверенному состоянию на тот
момент. После изменения Router Admin повтор браузерных сценариев открыт.
